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

package system

import (
	"bytes"
	"context"
	"fmt"
	"math/big"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/qrlclient/gqrlclient"
)

func (s *systemCheck) checkManagedAccessListTransaction(ctx context.Context) (common.Hash, error) {
	creationCode, runtimeCode := managedAccessListContractCode()
	deployHash, err := s.sendManagedContractTransaction(ctx, 0, nil, creationCode, nil, TransactionLabelBaseAccessListDeploy)
	if err != nil {
		return common.Hash{}, fmt.Errorf("deploy access-list storage contract: %w", err)
	}
	deployReceipts, err := s.waitMatchingSuccessfulReceipts(ctx, "access-list contract deployment", deployHash)
	if err != nil {
		return common.Hash{}, err
	}
	contract := deployReceipts[0].ContractAddress
	if contract == (common.Address{}) || deployReceipts[1].ContractAddress != contract {
		return common.Hash{}, fmt.Errorf("access-list contract address mismatch: EL1=%s EL2=%s", contract, deployReceipts[1].ContractAddress)
	}
	if len(contract.Bytes()) != common.AddressLength {
		return common.Hash{}, fmt.Errorf("access-list contract address width is %d, want %d", len(contract.Bytes()), common.AddressLength)
	}
	var upperHalfNonzero, lowerHalfNonzero bool
	for i, value := range contract {
		if i < common.AddressLength/2 {
			upperHalfNonzero = upperHalfNonzero || value != 0
		} else {
			lowerHalfNonzero = lowerHalfNonzero || value != 0
		}
	}
	if !upperHalfNonzero || !lowerHalfNonzero {
		return common.Hash{}, fmt.Errorf("access-list contract address does not exercise both VM64 halves: %s", contract)
	}
	for i, client := range s.clients {
		code, err := client.CodeAt(ctx, contract, deployReceipts[i].BlockNumber)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list contract code: %w", i+1, err)
		}
		if !bytes.Equal(code, runtimeCode) {
			return common.Hash{}, fmt.Errorf("EL%d access-list contract runtime mismatch: have %x want %x", i+1, code, runtimeCode)
		}
	}

	slot := common.Hash{}
	var nextValue common.StorageValue64
	for i := range nextValue {
		nextValue[i] = byte((i*37 + 11) & 0xff)
	}
	// Keep the high half observably nonzero so a 32-byte value truncation cannot
	// satisfy the post-state assertion by accident.
	nextValue[0] = 0x80
	expectedAccessList := types.AccessList{{
		Address:     contract,
		StorageKeys: []common.Hash{slot},
	}}
	call := qrl.CallMsg{
		From: s.cfg.signerAddress,
		To:   &contract,
		Data: append([]byte(nil), nextValue[:]...),
	}
	_, writeWasRecorded := s.recordedTransaction(TransactionLabelBaseAccessListWrite)
	for i, client := range s.clients {
		created, gasUsed, vmError, err := gqrlclient.New(client.Client()).CreateAccessList(ctx, call)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d qrl_createAccessList: %w", i+1, err)
		}
		if vmError != "" {
			return common.Hash{}, fmt.Errorf("EL%d qrl_createAccessList VM error: %s", i+1, vmError)
		}
		if gasUsed == 0 {
			return common.Hash{}, fmt.Errorf("EL%d qrl_createAccessList reported zero gas", i+1)
		}
		if err := validateManagedAccessList(created, contract, slot); err != nil {
			return common.Hash{}, fmt.Errorf("EL%d qrl_createAccessList result: %w", i+1, err)
		}
		if !writeWasRecorded {
			stored, err := readVM64StorageSlot(ctx, client, contract, slot, nil)
			if err != nil {
				return common.Hash{}, fmt.Errorf("EL%d storage after qrl_createAccessList: %w", i+1, err)
			}
			if !stored.IsZero() {
				return common.Hash{}, fmt.Errorf("EL%d qrl_createAccessList mutated storage to %s", i+1, stored.Hex())
			}
		}
	}

	// Submit the independently derived list, not either RPC result. This keeps
	// qrl_createAccessList validation separate from transaction execution.
	hash, err := s.sendManagedContractTransaction(ctx, 0, &contract, nextValue[:], &expectedAccessList, TransactionLabelBaseAccessListWrite)
	if err != nil {
		return common.Hash{}, fmt.Errorf("submit topology-Clef access-list transaction: %w", err)
	}
	receipts, err := s.waitMatchingSuccessfulReceipts(ctx, "access-list transaction", hash)
	if err != nil {
		return common.Hash{}, err
	}
	if receipts[0].BlockNumber.Cmp(deployReceipts[0].BlockNumber) <= 0 {
		return common.Hash{}, fmt.Errorf("access-list transaction block %s did not follow deployment block %s", receipts[0].BlockNumber, deployReceipts[0].BlockNumber)
	}
	blockNumber := new(big.Int).Set(receipts[0].BlockNumber)
	previousBlock := new(big.Int).Sub(new(big.Int).Set(blockNumber), big.NewInt(1))
	var headers [2]*types.Header
	for i, client := range s.clients {
		tx, pending, err := client.TransactionByHash(ctx, hash)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list transaction %s: %w", i+1, hash, err)
		}
		if pending {
			return common.Hash{}, fmt.Errorf("EL%d still reports access-list transaction %s as pending", i+1, hash)
		}
		if tx.Hash() != hash || tx.Type() != types.DynamicFeeTxType {
			return common.Hash{}, fmt.Errorf("EL%d access-list transaction hash/type mismatch: hash=%s type=%d", i+1, tx.Hash(), tx.Type())
		}
		if tx.To() == nil || *tx.To() != contract {
			return common.Hash{}, fmt.Errorf("EL%d access-list transaction recipient mismatch: have %v want %s", i+1, tx.To(), contract)
		}
		if tx.Value().Sign() != 0 || !bytes.Equal(tx.Data(), nextValue[:]) {
			return common.Hash{}, fmt.Errorf("EL%d access-list transaction value/body mismatch: value=%s body=%x", i+1, tx.Value(), tx.Data())
		}
		observedAccessList := tx.AccessList()
		if err := validateManagedAccessList(&observedAccessList, contract, slot); err != nil {
			return common.Hash{}, fmt.Errorf("EL%d mined access list: %w", i+1, err)
		}
		sender, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d recover topology-Clef access-list sender: %w", i+1, err)
		}
		if sender != s.cfg.signerAddress {
			return common.Hash{}, fmt.Errorf("EL%d recovered access-list sender %s, want topology-Clef account %s", i+1, sender, s.cfg.signerAddress)
		}

		before, err := readVM64StorageSlot(ctx, client, contract, slot, previousBlock)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list storage before transaction: %w", i+1, err)
		}
		if !before.IsZero() {
			return common.Hash{}, fmt.Errorf("EL%d access-list storage before transaction is %s, want zero", i+1, before.Hex())
		}
		after, err := readVM64StorageSlot(ctx, client, contract, slot, blockNumber)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list storage after transaction: %w", i+1, err)
		}
		if after != nextValue {
			return common.Hash{}, fmt.Errorf("EL%d access-list storage mismatch: have %s want %s", i+1, after.Hex(), nextValue.Hex())
		}
		code, err := client.CodeAt(ctx, contract, blockNumber)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list contract code after transaction: %w", i+1, err)
		}
		if !bytes.Equal(code, runtimeCode) {
			return common.Hash{}, fmt.Errorf("EL%d access-list contract runtime changed: have %x want %x", i+1, code, runtimeCode)
		}
		headers[i], err = client.HeaderByNumber(ctx, blockNumber)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list transaction header: %w", i+1, err)
		}
	}
	if err := compareExecutionHeaders(headers[0], headers[1]); err != nil {
		return common.Hash{}, fmt.Errorf("access-list transaction inclusion: %w", err)
	}
	return hash, nil
}

func (s *systemCheck) sendManagedContractTransaction(ctx context.Context, origin int, to *common.Address, input []byte, accessList *types.AccessList, evidenceLabel string) (common.Hash, error) {
	if origin < 0 || origin >= len(s.clients) || s.clients[origin] == nil {
		return common.Hash{}, fmt.Errorf("invalid execution origin EL%d", origin+1)
	}
	if len(input) == 0 {
		return common.Hash{}, fmt.Errorf("managed contract transaction input is empty")
	}
	if hash, ok := s.recordedTransaction(evidenceLabel); ok {
		return hash, nil
	}
	if s.managedJournal != nil {
		return s.sendJournaledManagedTransaction(ctx, evidenceLabel, managedTransactionRequest{
			origin: origin, to: to, value: new(big.Int), input: append([]byte(nil), input...), accessList: accessList,
		})
	}
	args := struct {
		From       common.Address    `json:"from"`
		To         *common.Address   `json:"to,omitempty"`
		Input      hexutil.Bytes     `json:"input"`
		AccessList *types.AccessList `json:"accessList,omitempty"`
	}{
		From:       s.cfg.signerAddress,
		To:         to,
		Input:      hexutil.Bytes(append([]byte(nil), input...)),
		AccessList: accessList,
	}
	var hash common.Hash
	if err := s.clients[origin].Client().CallContext(ctx, &hash, "qrl_sendTransaction", args); err != nil {
		return common.Hash{}, err
	}
	if hash == (common.Hash{}) {
		return common.Hash{}, fmt.Errorf("qrl_sendTransaction returned a zero hash")
	}
	if err := s.recordTransaction(ctx, evidenceLabel, hash); err != nil {
		return common.Hash{}, err
	}
	return hash, nil
}

func (s *systemCheck) waitMatchingSuccessfulReceipts(ctx context.Context, label string, hash common.Hash) ([2]*types.Receipt, error) {
	var receipts [2]*types.Receipt
	for i := range s.clients {
		receipt, err := s.waitReceipt(ctx, i, hash)
		if err != nil {
			return receipts, fmt.Errorf("%s EL%d receipt: %w", label, i+1, err)
		}
		receipts[i] = receipt
		if receipt.TxHash != hash {
			return receipts, fmt.Errorf("%s EL%d receipt transaction hash is %s, want %s", label, i+1, receipt.TxHash, hash)
		}
		if receipt.Status != types.ReceiptStatusSuccessful {
			return receipts, fmt.Errorf("%s failed on EL%d with status %d", label, i+1, receipt.Status)
		}
		if receipt.BlockNumber == nil || receipt.BlockNumber.Sign() <= 0 {
			return receipts, fmt.Errorf("%s has invalid EL%d inclusion block", label, i+1)
		}
	}
	if receipts[0].BlockNumber.Cmp(receipts[1].BlockNumber) != 0 || receipts[0].BlockHash != receipts[1].BlockHash {
		return receipts, fmt.Errorf("%s inclusion differs: EL1=%s/%s EL2=%s/%s", label,
			receipts[0].BlockNumber, receipts[0].BlockHash, receipts[1].BlockNumber, receipts[1].BlockHash)
	}
	return receipts, nil
}

func validateManagedAccessList(list *types.AccessList, contract common.Address, slot common.Hash) error {
	if list == nil {
		return fmt.Errorf("access list is nil")
	}
	if len(*list) != 1 {
		return fmt.Errorf("access list has %d entries, want 1", len(*list))
	}
	tuple := (*list)[0]
	if len(tuple.Address.Bytes()) != common.AddressLength {
		return fmt.Errorf("access-list address width is %d, want %d", len(tuple.Address.Bytes()), common.AddressLength)
	}
	if tuple.Address != contract {
		return fmt.Errorf("access-list address is %s, want full VM64 contract address %s", tuple.Address, contract)
	}
	if len(tuple.StorageKeys) != 1 {
		return fmt.Errorf("access-list tuple has %d storage keys, want 1", len(tuple.StorageKeys))
	}
	if tuple.StorageKeys[0] != slot {
		return fmt.Errorf("access-list storage key is %s, want %s", tuple.StorageKeys[0], slot)
	}
	return nil
}

func readVM64StorageSlot(ctx context.Context, client *qrlclient.Client, contract common.Address, slot common.Hash, blockNumber *big.Int) (common.StorageValue64, error) {
	raw, err := client.StorageAt(ctx, contract, slot, blockNumber)
	if err != nil {
		return common.StorageValue64{}, err
	}
	if len(raw) != common.StorageValue64Length {
		return common.StorageValue64{}, fmt.Errorf("storage value width is %d, want %d", len(raw), common.StorageValue64Length)
	}
	return common.BytesToStorageValue64(raw), nil
}

func managedAccessListContractCode() (creationCode, runtimeCode []byte) {
	runtimeCode = []byte{
		byte(vm.PUSH1), 0x00,
		byte(vm.CALLDATALOAD),
		byte(vm.PUSH1), 0x00,
		byte(vm.SSTORE),
		byte(vm.STOP),
	}
	initCode := []byte{
		byte(vm.PUSH1), byte(len(runtimeCode)),
		byte(vm.PUSH1), 0x00, // Patched below with the runtime offset.
		byte(vm.PUSH1), 0x00,
		byte(vm.CODECOPY),
		byte(vm.PUSH1), byte(len(runtimeCode)),
		byte(vm.PUSH1), 0x00,
		byte(vm.RETURN),
	}
	initCode[3] = byte(len(initCode))
	creationCode = append(append([]byte(nil), initCode...), runtimeCode...)
	return creationCode, append([]byte(nil), runtimeCode...)
}
