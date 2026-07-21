// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package freshsync

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/big"
	"strings"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/qrlclient/gqrlclient"
	"github.com/theQRL/go-qrl/qrldb/memorydb"
	"github.com/theQRL/go-qrl/rlp"
	"github.com/theQRL/go-qrl/trie"
)

const (
	// After depositcheck's three transitions, Hyperion's VM64 layout packs
	// branch[1] || branch[0] into the mutable 64-byte word at slot zero. Both
	// halves must therefore be nonzero at the finalized fresh-sync target.
	depositBranchFirstSlot = uint64(0x00)
	depositReadABI         = `[
		{"inputs":[],"name":"get_deposit_count","outputs":[{"name":"","type":"bytes"}],"stateMutability":"view","type":"function"},
		{"inputs":[],"name":"get_deposit_root","outputs":[{"name":"","type":"bytes32"}],"stateMutability":"view","type":"function"}
	]`
)

var emptyDepositRoot = common.HexToHash("0xd70a234731285c6804c2a4f56711ddb8c82c99740f207854891028af34e27e5e")

type depositTargetState struct {
	slot      common.Hash
	value     common.StorageValue64
	root      [32]byte
	count     uint64
	rootCall  []byte
	countCall []byte
}

func depositStorageKey(slot uint64) common.Hash {
	var key common.Hash
	for i := len(key) - 1; slot != 0; i-- {
		key[i] = byte(slot)
		slot >>= 8
	}
	return key
}

func decodeMutableDepositBranch(storage []byte) (common.StorageValue64, error) {
	var value common.StorageValue64
	if len(storage) != common.StorageValue64Length {
		return value, fmt.Errorf("mutable deposit branch returned %d bytes, want %d", len(storage), common.StorageValue64Length)
	}
	copy(value[:], storage)
	zeroHalf := make([]byte, 32)
	if bytes.Equal(value[:32], zeroHalf) || bytes.Equal(value[32:], zeroHalf) {
		return common.StorageValue64{}, fmt.Errorf("mutable deposit branch must populate both VM64 halves, got 0x%x", value)
	}
	return value, nil
}

func parseDepositReadABI() (abi.ABI, error) {
	parsed, err := abi.JSON(strings.NewReader(depositReadABI))
	if err != nil {
		return abi.ABI{}, fmt.Errorf("parse deposit read ABI: %w", err)
	}
	return parsed, nil
}

func readAndVerifyDepositTarget(ctx context.Context, client *qrlclient.Client, address common.Address, target *types.Header) (depositTargetState, error) {
	if target == nil || target.Number == nil || target.Number.Sign() <= 0 {
		return depositTargetState{}, fmt.Errorf("deposit target has no non-genesis block number")
	}
	if len(address.Bytes()) != common.AddressLength {
		return depositTargetState{}, fmt.Errorf("deposit contract address width is %d, want %d", len(address.Bytes()), common.AddressLength)
	}
	code, err := client.CodeAt(ctx, address, target.Number)
	if err != nil {
		return depositTargetState{}, fmt.Errorf("read deposit contract code at finalized target: %w", err)
	}
	if len(code) == 0 {
		return depositTargetState{}, fmt.Errorf("no deposit contract code at %s at finalized block %d", address.Hex(), target.Number.Uint64())
	}

	slot := depositStorageKey(depositBranchFirstSlot)
	storage, err := client.StorageAt(ctx, address, slot, target.Number)
	if err != nil {
		return depositTargetState{}, fmt.Errorf("qrl_getStorageAt deposit slot %s at finalized target: %w", slot.Hex(), err)
	}
	want, err := decodeMutableDepositBranch(storage)
	if err != nil {
		return depositTargetState{}, fmt.Errorf("deposit slot %s: %w", slot.Hex(), err)
	}

	proof, err := gqrlclient.New(client.Client()).GetProof(ctx, address, []string{slot.Hex()}, target.Number)
	if err != nil {
		return depositTargetState{}, fmt.Errorf("qrl_getProof deposit slot %s at finalized target: %w", slot.Hex(), err)
	}
	if proof == nil {
		return depositTargetState{}, fmt.Errorf("qrl_getProof returned nil for deposit contract")
	}
	codeHash := crypto.Keccak256Hash(code)
	if proof.CodeHash != codeHash {
		return depositTargetState{}, fmt.Errorf("deposit contract code hash %s differs from qrl_getProof account %s", codeHash.Hex(), proof.CodeHash.Hex())
	}
	if err := verifyDepositProof(target.Root, address, slot, want, proof); err != nil {
		return depositTargetState{}, err
	}

	parsed, err := parseDepositReadABI()
	if err != nil {
		return depositTargetState{}, err
	}
	rootCall, err := callDepositView(ctx, client, address, target.Number, parsed, "get_deposit_root")
	if err != nil {
		return depositTargetState{}, err
	}
	countCall, err := callDepositView(ctx, client, address, target.Number, parsed, "get_deposit_count")
	if err != nil {
		return depositTargetState{}, err
	}
	root, count, err := decodeDepositCalls(parsed, rootCall, countCall)
	if err != nil {
		return depositTargetState{}, err
	}
	return depositTargetState{
		slot:      slot,
		value:     want,
		root:      root,
		count:     count,
		rootCall:  bytes.Clone(rootCall),
		countCall: bytes.Clone(countCall),
	}, nil
}

func callDepositView(ctx context.Context, client *qrlclient.Client, address common.Address, block *big.Int, parsed abi.ABI, method string) ([]byte, error) {
	calldata, err := parsed.Pack(method)
	if err != nil {
		return nil, fmt.Errorf("pack %s call: %w", method, err)
	}
	output, err := client.CallContract(ctx, qrl.CallMsg{To: &address, Data: calldata}, block)
	if err != nil {
		return nil, fmt.Errorf("qrl_call %s at finalized target: %w", method, err)
	}
	if len(output) == 0 {
		return nil, fmt.Errorf("qrl_call %s returned empty output", method)
	}
	return output, nil
}

func decodeDepositCalls(parsed abi.ABI, rootCall, countCall []byte) ([32]byte, uint64, error) {
	var root [32]byte
	rootValues, err := parsed.Unpack("get_deposit_root", rootCall)
	if err != nil {
		return root, 0, fmt.Errorf("decode get_deposit_root output: %w", err)
	}
	if len(rootValues) != 1 {
		return root, 0, fmt.Errorf("get_deposit_root returned %d values, want 1", len(rootValues))
	}
	root = *abi.ConvertType(rootValues[0], new([32]byte)).(*[32]byte)
	if root == ([32]byte{}) || common.Hash(root) == emptyDepositRoot {
		return root, 0, fmt.Errorf("get_deposit_root returned an empty pre-deposit root 0x%x", root)
	}

	countValues, err := parsed.Unpack("get_deposit_count", countCall)
	if err != nil {
		return root, 0, fmt.Errorf("decode get_deposit_count output: %w", err)
	}
	if len(countValues) != 1 {
		return root, 0, fmt.Errorf("get_deposit_count returned %d values, want 1", len(countValues))
	}
	countBytes := *abi.ConvertType(countValues[0], new([]byte)).(*[]byte)
	if len(countBytes) != 8 {
		return root, 0, fmt.Errorf("deposit count encoding is %d bytes, want 8", len(countBytes))
	}
	count := binary.LittleEndian.Uint64(countBytes)
	if count == 0 {
		return root, 0, fmt.Errorf("deposit count remains zero at finalized target")
	}
	return root, count, nil
}

func compareDepositTarget(got, want depositTargetState) error {
	if got.slot != want.slot || got.value != want.value {
		return fmt.Errorf("fresh deposit storage leaf differs: got %s/0x%x want %s/0x%x", got.slot.Hex(), got.value, want.slot.Hex(), want.value)
	}
	if got.root != want.root || got.count != want.count {
		return fmt.Errorf("fresh deposit call state differs: root/count=0x%x/%d want 0x%x/%d", got.root, got.count, want.root, want.count)
	}
	if !bytes.Equal(got.rootCall, want.rootCall) || !bytes.Equal(got.countCall, want.countCall) {
		return fmt.Errorf("fresh deposit ABI call encoding differs from reference")
	}
	return nil
}

func verifyDepositProof(stateRoot common.Hash, address common.Address, slot common.Hash, want common.StorageValue64, proof *gqrlclient.AccountResult) error {
	if stateRoot == (common.Hash{}) {
		return fmt.Errorf("finalized header has a zero state root")
	}
	if proof == nil {
		return fmt.Errorf("qrl_getProof returned nil")
	}
	if proof.Address != address {
		return fmt.Errorf("account proof address %s, want %s", proof.Address.Hex(), address.Hex())
	}
	if proof.Balance == nil {
		return fmt.Errorf("account proof has nil balance")
	}
	if len(proof.AccountProof) == 0 {
		return fmt.Errorf("account proof has no nodes")
	}
	if len(proof.StorageProof) != 1 {
		return fmt.Errorf("storage proof count is %d, want 1", len(proof.StorageProof))
	}
	storageProof := proof.StorageProof[0]
	if storageProof.Key != slot.Hex() {
		return fmt.Errorf("storage proof key %s, want %s", storageProof.Key, slot.Hex())
	}
	if storageProof.Value == nil || storageProof.Value.Cmp(new(big.Int).SetBytes(want[:])) != 0 {
		return fmt.Errorf("storage proof RPC value %v, want 0x%x", storageProof.Value, want)
	}
	if len(storageProof.Proof) == 0 {
		return fmt.Errorf("storage proof has no nodes")
	}

	accountLeaf, err := verifyProofNodes(stateRoot, crypto.Keccak256(address.Bytes()), proof.AccountProof)
	if err != nil {
		return fmt.Errorf("verify deposit account proof: %w", err)
	}
	if accountLeaf == nil {
		return fmt.Errorf("verified deposit account proof returned absence")
	}
	var account types.StateAccount
	if err := rlp.DecodeBytes(accountLeaf, &account); err != nil {
		return fmt.Errorf("decode deposit account proof leaf: %w", err)
	}
	if account.Balance == nil || account.Nonce != proof.Nonce || account.Balance.Cmp(proof.Balance) != 0 || account.Root != proof.StorageHash || !bytes.Equal(account.CodeHash, proof.CodeHash[:]) {
		return fmt.Errorf("verified deposit account leaf differs from qrl_getProof result")
	}

	storageLeaf, err := verifyProofNodes(proof.StorageHash, crypto.Keccak256(slot.Bytes()), storageProof.Proof)
	if err != nil {
		return fmt.Errorf("verify deposit storage inclusion proof: %w", err)
	}
	if storageLeaf == nil {
		return fmt.Errorf("verified deposit storage proof returned absence")
	}
	var trimmed []byte
	if err := rlp.DecodeBytes(storageLeaf, &trimmed); err != nil {
		return fmt.Errorf("decode deposit storage proof leaf: %w", err)
	}
	if got := common.BytesToStorageValue64(trimmed); got != want {
		return fmt.Errorf("verified deposit storage leaf = 0x%x, want 0x%x", got, want)
	}
	return nil
}

func verifyProofNodes(root common.Hash, key []byte, nodes []string) ([]byte, error) {
	db := memorydb.New()
	defer db.Close()
	for i, encoded := range nodes {
		node, err := hexutil.Decode(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode proof node %d: %w", i, err)
		}
		if len(node) == 0 {
			return nil, fmt.Errorf("proof node %d is empty", i)
		}
		if err := db.Put(crypto.Keccak256(node), node); err != nil {
			return nil, fmt.Errorf("store proof node %d: %w", i, err)
		}
	}
	return trie.VerifyProof(root, key, db)
}
