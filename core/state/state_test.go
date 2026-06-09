// Copyright 2014 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package state

import (
	"bytes"
	"encoding/json"
	"math/big"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/qrldb"
	"github.com/theQRL/go-qrl/trie"
)

type stateEnv struct {
	db    qrldb.Database
	state *StateDB
}

func newStateEnv() *stateEnv {
	db := rawdb.NewMemoryDatabase()
	sdb, _ := New(types.EmptyRootHash, NewDatabase(db), nil)
	return &stateEnv{db: db, state: sdb}
}

func TestDump(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	tdb := NewDatabaseWithConfig(db, &trie.Config{Preimages: true})
	sdb, _ := New(types.EmptyRootHash, tdb, nil)
	s := &stateEnv{db: db, state: sdb}

	// generate a few entries
	obj1 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x01, 0}))
	obj1.AddBalance(big.NewInt(22))
	obj2 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x01, 0x02}))
	obj2.SetCode(crypto.Keccak256Hash([]byte{3, 3, 3, 3, 3, 3, 3}), []byte{3, 3, 3, 3, 3, 3, 3})
	obj3 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x02, 0}))
	obj3.SetBalance(big.NewInt(44))

	// write some of them to the trie
	s.state.updateStateObject(obj1)
	s.state.updateStateObject(obj2)
	root, _ := s.state.Commit(0, false)

	// check that DumpToCollector contains the state objects that are in trie
	s.state, _ = New(root, tdb, nil)
	got := string(s.state.Dump(nil))
	want := `{
    "root": "7c2f4592d05c3ffe9cf58b0193a1e266e838e724c8a48abbc1528dc72a5359fc",
    "accounts": {
        "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000100": {
            "balance": "22",
            "nonce": 0,
            "root": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
            "codeHash": "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470",
            "key": "0x1c215b54eec9ee6aca29c851c685652f7166e29c4e460f5b894f8abee449ad3b"
        },
        "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000102": {
            "balance": "0",
            "nonce": 0,
            "root": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
            "codeHash": "0x87874902497a5bb968da31a2998d8f22e949d1ef6214bcdedd8bae24cca4b9e3",
            "code": "0x03030303030303",
            "key": "0x565a22c1af7fcc038f06206699a6bd0ad8c85d23dafe9aebac3e0df68e8fb320"
        },
        "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000200": {
            "balance": "44",
            "nonce": 0,
            "root": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421",
            "codeHash": "0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470",
            "key": "0xe0a266959b1660a4c10ed88a7c087d7da493cae19cf1cc6ea603236845893f27"
        }
    }
}`
	if got != want {
		t.Errorf("DumpToCollector mismatch:\ngot: %s\nwant: %s\n", got, want)
	}
}

func TestIterativeDump(t *testing.T) {
	db := rawdb.NewMemoryDatabase()
	tdb := NewDatabaseWithConfig(db, &trie.Config{Preimages: true})
	sdb, _ := New(types.EmptyRootHash, tdb, nil)
	s := &stateEnv{db: db, state: sdb}

	// generate a few entries
	obj1 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x01, 0}))
	obj1.AddBalance(big.NewInt(22))
	obj2 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x01, 0x02}))
	obj2.SetCode(crypto.Keccak256Hash([]byte{3, 3, 3, 3, 3, 3, 3}), []byte{3, 3, 3, 3, 3, 3, 3})
	obj3 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x02, 0}))
	obj3.SetBalance(big.NewInt(44))
	obj4 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x00, 0}))
	obj4.AddBalance(big.NewInt(1337))

	// write some of them to the trie
	s.state.updateStateObject(obj1)
	s.state.updateStateObject(obj2)
	root, _ := s.state.Commit(0, false)
	s.state, _ = New(root, tdb, nil)

	b := &bytes.Buffer{}
	s.state.IterativeDump(nil, json.NewEncoder(b))
	// check that DumpToCollector contains the state objects that are in trie
	got := b.String()
	want := `{"root":"0x4fb91c01fa112fa0d076b59eeec6398a307d37c80a4a01bc0379bbdee6672c5a"}
{"balance":"22","nonce":0,"root":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","codeHash":"0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470","address":"Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000100","key":"0x1c215b54eec9ee6aca29c851c685652f7166e29c4e460f5b894f8abee449ad3b"}
{"balance":"0","nonce":0,"root":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","codeHash":"0x87874902497a5bb968da31a2998d8f22e949d1ef6214bcdedd8bae24cca4b9e3","code":"0x03030303030303","address":"Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000102","key":"0x565a22c1af7fcc038f06206699a6bd0ad8c85d23dafe9aebac3e0df68e8fb320"}
{"balance":"1337","nonce":0,"root":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","codeHash":"0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470","address":"Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000","key":"0xad3228b676f7d3cd4284a5443f17f1962b36e491b30a40b2405849e597ba5fb5"}
{"balance":"44","nonce":0,"root":"0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421","codeHash":"0xc5d2460186f7233c927e7db2dcc703c0e500b653ca82273b7bfad8045d85a470","address":"Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000200","key":"0xe0a266959b1660a4c10ed88a7c087d7da493cae19cf1cc6ea603236845893f27"}
`
	if got != want {
		t.Errorf("DumpToCollector mismatch:\ngot: %s\nwant: %s\n", got, want)
	}
}

func TestNull(t *testing.T) {
	s := newStateEnv()
	address, _ := common.NewAddressFromString("Q000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000823140710bf13990e4500136726d8b55")
	s.state.CreateAccount(address)
	//value := common.FromHex("0x823140710bf13990e4500136726d8b55")
	var value common.Hash

	s.state.SetState(address, common.Hash{}, value)
	s.state.Commit(0, false)

	if value := s.state.GetState(address, common.Hash{}); value != (common.Hash{}) {
		t.Errorf("expected empty current value, got %x", value)
	}
	if value := s.state.GetCommittedState(address, common.Hash{}); value != (common.Hash{}) {
		t.Errorf("expected empty committed value, got %x", value)
	}
}

func TestSnapshot(t *testing.T) {
	stateobjaddr := common.BytesToAddress([]byte("aa"))
	var storageaddr common.Hash
	data1 := common.BytesToHash([]byte{42})
	data2 := common.BytesToHash([]byte{43})
	s := newStateEnv()

	// snapshot the genesis state
	genesis := s.state.Snapshot()

	// set initial state object value
	s.state.SetState(stateobjaddr, storageaddr, data1)
	snapshot := s.state.Snapshot()

	// set a new state object value, revert it and ensure correct content
	s.state.SetState(stateobjaddr, storageaddr, data2)
	s.state.RevertToSnapshot(snapshot)

	if v := s.state.GetState(stateobjaddr, storageaddr); v != data1 {
		t.Errorf("wrong storage value %v, want %v", v, data1)
	}
	if v := s.state.GetCommittedState(stateobjaddr, storageaddr); v != (common.Hash{}) {
		t.Errorf("wrong committed storage value %v, want %v", v, common.Hash{})
	}

	// revert up to the genesis state and ensure correct content
	s.state.RevertToSnapshot(genesis)
	if v := s.state.GetState(stateobjaddr, storageaddr); v != (common.Hash{}) {
		t.Errorf("wrong storage value %v, want %v", v, common.Hash{})
	}
	if v := s.state.GetCommittedState(stateobjaddr, storageaddr); v != (common.Hash{}) {
		t.Errorf("wrong committed storage value %v, want %v", v, common.Hash{})
	}
}

func TestSnapshotEmpty(t *testing.T) {
	s := newStateEnv()
	s.state.RevertToSnapshot(s.state.Snapshot())
}

func TestSnapshot2(t *testing.T) {
	state, _ := New(types.EmptyRootHash, NewDatabase(rawdb.NewMemoryDatabase()), nil)

	stateobjaddr0 := common.BytesToAddress([]byte("so0"))
	stateobjaddr1 := common.BytesToAddress([]byte("so1"))
	var storageaddr common.Hash

	data0 := common.BytesToHash([]byte{17})
	data1 := common.BytesToHash([]byte{18})

	state.SetState(stateobjaddr0, storageaddr, data0)
	state.SetState(stateobjaddr1, storageaddr, data1)

	// db, trie are already non-empty values
	so0 := state.getStateObject(stateobjaddr0)
	so0.SetBalance(big.NewInt(42))
	so0.SetNonce(43)
	so0.SetCode(crypto.Keccak256Hash([]byte{'c', 'a', 'f', 'e'}), []byte{'c', 'a', 'f', 'e'})
	so0.deleted = false
	state.setStateObject(so0)

	root, _ := state.Commit(0, false)
	state, _ = New(root, state.db, state.snaps)

	// and one with deleted == true
	so1 := state.getStateObject(stateobjaddr1)
	so1.SetBalance(big.NewInt(52))
	so1.SetNonce(53)
	so1.SetCode(crypto.Keccak256Hash([]byte{'c', 'a', 'f', 'e', '2'}), []byte{'c', 'a', 'f', 'e', '2'})
	so1.deleted = true
	state.setStateObject(so1)

	so1 = state.getStateObject(stateobjaddr1)
	if so1 != nil {
		t.Fatalf("deleted object not nil when getting")
	}

	snapshot := state.Snapshot()
	state.RevertToSnapshot(snapshot)

	so0Restored := state.getStateObject(stateobjaddr0)
	// Update lazily-loaded values before comparing.
	so0Restored.GetState(storageaddr)
	so0Restored.Code()
	// non-deleted is equal (restored)
	compareStateObjects(so0Restored, so0, t)

	// deleted should be nil, both before and after restore of state copy
	so1Restored := state.getStateObject(stateobjaddr1)
	if so1Restored != nil {
		t.Fatalf("deleted object not nil after restoring snapshot: %+v", so1Restored)
	}
}

func compareStateObjects(so0, so1 *stateObject, t *testing.T) {
	if so0.Address() != so1.Address() {
		t.Fatalf("Address mismatch: have %v, want %v", so0.address, so1.address)
	}
	if so0.Balance().Cmp(so1.Balance()) != 0 {
		t.Fatalf("Balance mismatch: have %v, want %v", so0.Balance(), so1.Balance())
	}
	if so0.Nonce() != so1.Nonce() {
		t.Fatalf("Nonce mismatch: have %v, want %v", so0.Nonce(), so1.Nonce())
	}
	if so0.data.Root != so1.data.Root {
		t.Errorf("Root mismatch: have %x, want %x", so0.data.Root[:], so1.data.Root[:])
	}
	if !bytes.Equal(so0.CodeHash(), so1.CodeHash()) {
		t.Fatalf("CodeHash mismatch: have %v, want %v", so0.CodeHash(), so1.CodeHash())
	}
	if !bytes.Equal(so0.code, so1.code) {
		t.Fatalf("Code mismatch: have %v, want %v", so0.code, so1.code)
	}

	if len(so1.dirtyStorage) != len(so0.dirtyStorage) {
		t.Errorf("Dirty storage size mismatch: have %d, want %d", len(so1.dirtyStorage), len(so0.dirtyStorage))
	}
	for k, v := range so1.dirtyStorage {
		if so0.dirtyStorage[k] != v {
			t.Errorf("Dirty storage key %x mismatch: have %v, want %v", k, so0.dirtyStorage[k], v)
		}
	}
	for k, v := range so0.dirtyStorage {
		if so1.dirtyStorage[k] != v {
			t.Errorf("Dirty storage key %x mismatch: have %v, want none.", k, v)
		}
	}
	if len(so1.originStorage) != len(so0.originStorage) {
		t.Errorf("Origin storage size mismatch: have %d, want %d", len(so1.originStorage), len(so0.originStorage))
	}
	for k, v := range so1.originStorage {
		if so0.originStorage[k] != v {
			t.Errorf("Origin storage key %x mismatch: have %v, want %v", k, so0.originStorage[k], v)
		}
	}
	for k, v := range so0.originStorage {
		if so1.originStorage[k] != v {
			t.Errorf("Origin storage key %x mismatch: have %v, want none.", k, v)
		}
	}
}
