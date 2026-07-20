// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.

package qrltest

import (
	"bytes"
	"errors"
	"fmt"
	"sort"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	qrlcrypto "github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/internal/utesting"
	"github.com/theQRL/go-qrl/qrl/protocols/snap"
	"github.com/theQRL/go-qrl/rlp"
	"github.com/theQRL/go-qrl/trie"
	"github.com/theQRL/go-qrl/trie/trienode"
)

const snapPageBytes = uint64(512 * 1024)

type snapAccount struct {
	hash    common.Hash
	body    []byte
	account *types.StateAccount
}

func (s *Suite) SnapTests() []utesting.Test {
	return []utesting.Test{
		{Name: "SnapHandshakeAndStatus", Fn: s.TestSnapHandshakeAndStatus},
		{Name: "SnapAccountRangeVM64", Fn: s.TestSnapAccountRangeVM64},
		{Name: "SnapStorageRangesVM64", Fn: s.TestSnapStorageRangesVM64},
		{Name: "SnapByteCodes", Fn: s.TestSnapByteCodes},
		{Name: "SnapTrieNodes", Fn: s.TestSnapTrieNodes},
	}
}

func (s *Suite) TestSnapHandshakeAndStatus(t *utesting.T) {
	conn, err := s.connect(true)
	if err != nil {
		t.Fatalf("snap + qrl handshake/status failed: %v", err)
	}
	defer conn.Close()
	if conn.snapVersion != snap.SNAP1 {
		t.Fatalf("negotiated snap/%d, want snap/%d", conn.snapVersion, snap.SNAP1)
	}
}

func (s *Suite) TestSnapAccountRangeVM64(t *utesting.T) {
	conn, err := s.connect(true)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	addresses := make([]common.Address, 0, len(s.chain.genesis.Alloc))
	for address := range s.chain.genesis.Alloc {
		addresses = append(addresses, address)
	}
	sort.Slice(addresses, func(i, j int) bool { return bytes.Compare(addresses[i][:], addresses[j][:]) < 0 })
	if len(addresses) == 0 {
		t.Fatal("genesis has no VM64 allocation")
	}
	address := addresses[0]
	if len(address[:]) != 64 {
		t.Fatalf("fixture address has %d bytes, want 64", len(address[:]))
	}
	addressHash := qrlcrypto.Keccak256Hash(address[:])
	if len(addressHash[:]) != 32 {
		t.Fatalf("hashed account key has %d bytes, want 32", len(addressHash[:]))
	}
	request := &snap.GetAccountRangePacket{
		ID:     0x534e0001,
		Root:   s.chain.Head().Root(),
		Origin: addressHash,
		Limit:  addressHash,
		Bytes:  snapPageBytes,
	}
	response, err := requestAccountRange(conn, request)
	if err != nil {
		t.Fatalf("account range request: %v", err)
	}
	if len(response.Accounts) != 1 || response.Accounts[0].Hash != addressHash {
		t.Fatalf("VM64 address hash %x not returned exactly; got %v", addressHash, accountHashes(response.Accounts))
	}
	if _, err := verifyAccountRange(request, response); err != nil {
		t.Fatalf("account range proof: %v", err)
	}
	if _, err := types.FullAccount(response.Accounts[0].Body); err != nil {
		t.Fatalf("decode returned account body: %v", err)
	}
}

func (s *Suite) TestSnapStorageRangesVM64(t *utesting.T) {
	conn, err := s.connect(true)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	accounts, err := fetchAllAccounts(conn, s.chain.Head().Root())
	if err != nil {
		t.Fatalf("fetch accounts: %v", err)
	}
	storageAccounts := 0
	wideValue := false
	for _, account := range accounts {
		if account.account.Root == types.EmptyRootHash {
			continue
		}
		storageAccounts++
		values, err := fetchAllStorage(conn, s.chain.Head().Root(), account)
		if err != nil {
			t.Fatalf("storage account %x: %v", account.hash, err)
		}
		for _, encoded := range values {
			var value []byte
			if err := rlp.DecodeBytes(encoded, &value); err != nil {
				t.Fatalf("storage account %x returned invalid RLP value %x: %v", account.hash, encoded, err)
			}
			if len(value) > common.StorageValue64Length {
				t.Fatalf("storage value has %d decoded bytes, exceeds VM64 width %d", len(value), common.StorageValue64Length)
			}
			if len(value) == common.StorageValue64Length {
				wideValue = true
			}
		}
	}
	if storageAccounts == 0 {
		t.Fatal("head state has no account with storage; storage range behavior was not exercised")
	}
	if !wideValue {
		t.Fatal("head state storage has no full 64-byte value; VM64 storage width is not covered by the fixture")
	}
}

func (s *Suite) TestSnapByteCodes(t *utesting.T) {
	conn, err := s.connect(true)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	accounts, err := fetchAllAccounts(conn, s.chain.Head().Root())
	if err != nil {
		t.Fatalf("fetch accounts: %v", err)
	}
	var hashes []common.Hash
	seen := make(map[common.Hash]struct{})
	for _, account := range accounts {
		hash := common.BytesToHash(account.account.CodeHash)
		if hash == types.EmptyCodeHash {
			continue
		}
		if _, ok := seen[hash]; ok {
			continue
		}
		seen[hash] = struct{}{}
		hashes = append(hashes, hash)
		if len(hashes) == 16 {
			break
		}
	}
	if len(hashes) == 0 {
		t.Fatal("head state has no contract bytecode; bytecode retrieval was not exercised")
	}
	request := &snap.GetByteCodesPacket{ID: 0x534e2001, Hashes: hashes, Bytes: snapPageBytes}
	if err := conn.write(snapProto, snap.GetByteCodesMsg, request); err != nil {
		t.Fatalf("send bytecode request: %v", err)
	}
	packet, err := conn.readSnap()
	if err != nil {
		t.Fatalf("read bytecode response: %v", err)
	}
	response, ok := packet.(*snap.ByteCodesPacket)
	if !ok {
		t.Fatalf("received %T, want *snap.ByteCodesPacket", packet)
	}
	if response.ID != request.ID {
		t.Fatalf("bytecode response ID %d, want %d", response.ID, request.ID)
	}
	if len(response.Codes) != len(hashes) {
		t.Fatalf("received %d bytecodes, want %d", len(response.Codes), len(hashes))
	}
	for i, code := range response.Codes {
		if got := qrlcrypto.Keccak256Hash(code); got != hashes[i] {
			t.Fatalf("bytecode %d hash %x, want %x", i, got, hashes[i])
		}
	}
}

func (s *Suite) TestSnapTrieNodes(t *utesting.T) {
	conn, err := s.connect(true)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer conn.Close()
	root := s.chain.Head().Root()
	request := &snap.GetTrieNodesPacket{
		ID:    0x534e3001,
		Root:  root,
		Paths: []snap.TrieNodePathSet{{[]byte{}}},
		Bytes: snapPageBytes,
	}
	response, err := requestTrieNodes(conn, request)
	if err != nil {
		t.Fatalf("account root node request: %v", err)
	}
	if len(response.Nodes) != 1 || qrlcrypto.Keccak256Hash(response.Nodes[0]) != root {
		t.Fatalf("account root response does not hash to requested 32-byte root %x", root)
	}

	accounts, err := fetchAllAccounts(conn, root)
	if err != nil {
		t.Fatalf("fetch accounts: %v", err)
	}
	var storage *snapAccount
	for i := range accounts {
		if accounts[i].account.Root != types.EmptyRootHash {
			storage = &accounts[i]
			break
		}
	}
	if storage == nil {
		t.Fatal("head state has no storage trie; storage trie-node behavior was not exercised")
	}
	accountPath := append([]byte(nil), storage.hash[:]...)
	request = &snap.GetTrieNodesPacket{
		ID:    0x534e3002,
		Root:  root,
		Paths: []snap.TrieNodePathSet{{accountPath, []byte{}}},
		Bytes: snapPageBytes,
	}
	response, err = requestTrieNodes(conn, request)
	if err != nil {
		t.Fatalf("storage root node request: %v", err)
	}
	storageRoot := storage.account.Root
	if len(response.Nodes) != 1 || qrlcrypto.Keccak256Hash(response.Nodes[0]) != storageRoot {
		t.Fatalf("storage root response does not hash to requested 32-byte root %x", storageRoot)
	}
}

func requestAccountRange(conn *Conn, request *snap.GetAccountRangePacket) (*snap.AccountRangePacket, error) {
	if err := conn.write(snapProto, snap.GetAccountRangeMsg, request); err != nil {
		return nil, err
	}
	packet, err := conn.readSnap()
	if err != nil {
		return nil, err
	}
	response, ok := packet.(*snap.AccountRangePacket)
	if !ok {
		return nil, fmt.Errorf("received %T, want *snap.AccountRangePacket", packet)
	}
	if response.ID != request.ID {
		return nil, fmt.Errorf("account response ID %d, want %d", response.ID, request.ID)
	}
	return response, nil
}

func verifyAccountRange(request *snap.GetAccountRangePacket, response *snap.AccountRangePacket) (bool, error) {
	hashes, values, err := response.Unpack()
	if err != nil {
		return false, err
	}
	keys := make([][]byte, len(hashes))
	for i, hash := range hashes {
		keys[i] = append([]byte(nil), hash[:]...)
	}
	end := request.Origin[:]
	if len(keys) > 0 {
		end = keys[len(keys)-1]
	}
	return verifyRangeProof(request.Root, request.Origin[:], end, keys, values, response.Proof)
}

func verifyRangeProof(root common.Hash, origin, end []byte, keys, values, proof [][]byte) (bool, error) {
	if len(proof) == 0 {
		return trie.VerifyRangeProof(root, origin, end, keys, values, nil)
	}
	nodes := make(trienode.ProofList, len(proof))
	for i := range proof {
		nodes[i] = proof[i]
	}
	return trie.VerifyRangeProof(root, origin, end, keys, values, nodes.Set())
}

func fetchAllAccounts(conn *Conn, root common.Hash) ([]snapAccount, error) {
	var (
		accounts []snapAccount
		origin   common.Hash
	)
	for page := 0; page < 64; page++ {
		request := &snap.GetAccountRangePacket{
			ID:     0x534e1000 + uint64(page),
			Root:   root,
			Origin: origin,
			Limit:  common.MaxHash,
			Bytes:  snapPageBytes,
		}
		response, err := requestAccountRange(conn, request)
		if err != nil {
			return nil, err
		}
		more, err := verifyAccountRange(request, response)
		if err != nil {
			return nil, fmt.Errorf("page %d proof: %w", page, err)
		}
		if len(response.Accounts) == 0 {
			if len(accounts) == 0 {
				return nil, errors.New("non-empty head state returned no accounts")
			}
			if more {
				return nil, fmt.Errorf("page %d proves more accounts but returned none", page)
			}
			return accounts, nil
		}
		for _, entry := range response.Accounts {
			if len(accounts) > 0 && bytes.Compare(accounts[len(accounts)-1].hash[:], entry.Hash[:]) >= 0 {
				return nil, fmt.Errorf("account hash %x is not after previous %x", entry.Hash, accounts[len(accounts)-1].hash)
			}
			account, err := types.FullAccount(entry.Body)
			if err != nil {
				return nil, fmt.Errorf("decode account %x: %w", entry.Hash, err)
			}
			accounts = append(accounts, snapAccount{hash: entry.Hash, body: append([]byte(nil), entry.Body...), account: account})
		}
		if !more {
			return accounts, nil
		}
		last := response.Accounts[len(response.Accounts)-1].Hash
		next, ok := incrementHash(last)
		if !ok {
			return nil, errors.New("account proof claims more data after maximum hash")
		}
		origin = next
	}
	return nil, errors.New("account range exceeded 64 proof-verified pages")
}

func fetchAllStorage(conn *Conn, root common.Hash, account snapAccount) ([][]byte, error) {
	var (
		values   [][]byte
		previous common.Hash
		origin   common.Hash
	)
	for page := 0; page < 64; page++ {
		request := &snap.GetStorageRangesPacket{
			ID:       0x534e4000 + uint64(page),
			Root:     root,
			Accounts: []common.Hash{account.hash},
			Origin:   append([]byte(nil), origin[:]...),
			Limit:    append([]byte(nil), common.MaxHash[:]...),
			Bytes:    snapPageBytes,
		}
		if err := conn.write(snapProto, snap.GetStorageRangesMsg, request); err != nil {
			return nil, err
		}
		packet, err := conn.readSnap()
		if err != nil {
			return nil, err
		}
		response, ok := packet.(*snap.StorageRangesPacket)
		if !ok {
			return nil, fmt.Errorf("received %T, want *snap.StorageRangesPacket", packet)
		}
		if response.ID != request.ID {
			return nil, fmt.Errorf("storage response ID %d, want %d", response.ID, request.ID)
		}
		if len(response.Slots) > 1 {
			return nil, fmt.Errorf("single-account request returned %d slot ranges", len(response.Slots))
		}
		var slots []*snap.StorageData
		if len(response.Slots) == 1 {
			slots = response.Slots[0]
		}
		keys := make([][]byte, len(slots))
		pageValues := make([][]byte, len(slots))
		for i, slot := range slots {
			if (len(values) > 0 || i > 0) && bytes.Compare(previous[:], slot.Hash[:]) >= 0 {
				return nil, fmt.Errorf("storage hash %x is not after previous %x", slot.Hash, previous)
			}
			previous = slot.Hash
			keys[i] = append([]byte(nil), slot.Hash[:]...)
			pageValues[i] = append([]byte(nil), slot.Body...)
		}
		end := origin[:]
		if len(keys) > 0 {
			end = keys[len(keys)-1]
		}
		more, err := verifyRangeProof(account.account.Root, origin[:], end, keys, pageValues, response.Proof)
		if err != nil {
			return nil, fmt.Errorf("storage page %d proof: %w", page, err)
		}
		values = append(values, pageValues...)
		if !more {
			if len(values) == 0 {
				return nil, errors.New("non-empty storage root returned no slots")
			}
			return values, nil
		}
		if len(slots) == 0 {
			return nil, fmt.Errorf("storage page %d proves more slots but returned none", page)
		}
		next, ok := incrementHash(slots[len(slots)-1].Hash)
		if !ok {
			return nil, errors.New("storage proof claims more data after maximum hash")
		}
		origin = next
	}
	return nil, errors.New("storage range exceeded 64 proof-verified pages")
}

func requestTrieNodes(conn *Conn, request *snap.GetTrieNodesPacket) (*snap.TrieNodesPacket, error) {
	if err := conn.write(snapProto, snap.GetTrieNodesMsg, request); err != nil {
		return nil, err
	}
	packet, err := conn.readSnap()
	if err != nil {
		return nil, err
	}
	response, ok := packet.(*snap.TrieNodesPacket)
	if !ok {
		return nil, fmt.Errorf("received %T, want *snap.TrieNodesPacket", packet)
	}
	if response.ID != request.ID {
		return nil, fmt.Errorf("trie-node response ID %d, want %d", response.ID, request.ID)
	}
	return response, nil
}

func incrementHash(hash common.Hash) (common.Hash, bool) {
	next := hash
	for i := len(next) - 1; i >= 0; i-- {
		next[i]++
		if next[i] != 0 {
			return next, true
		}
	}
	return common.Hash{}, false
}

func accountHashes(accounts []*snap.AccountData) []common.Hash {
	hashes := make([]common.Hash, len(accounts))
	for i, account := range accounts {
		hashes[i] = account.Hash
	}
	return hashes
}
