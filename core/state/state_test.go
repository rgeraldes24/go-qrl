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
	obj1 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x01}))
	obj1.AddBalance(big.NewInt(22))
	obj2 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x01, 0x02}))
	obj2.SetCode(crypto.Keccak512Hash([]byte{3, 3, 3, 3, 3, 3, 3}), []byte{3, 3, 3, 3, 3, 3, 3})
	obj3 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x02}))
	obj3.SetBalance(big.NewInt(44))

	// write some of them to the trie
	s.state.updateStateObject(obj1)
	s.state.updateStateObject(obj2)
	root, _ := s.state.Commit(0, false)

	// check that DumpToCollector contains the state objects that are in trie
	s.state, _ = New(root, tdb, nil)
	got := string(s.state.Dump(nil))
	want := `{
    "root": "0117d7e2fced3478dcac83c7fe6ebc1361b01750114fb260684eca4941f974be892fb31416c3ceee4e7f1ec15ad596fcf27373e08dedcda1836850f7463d7f0b",
    "accounts": {
        "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001": {
            "balance": "22",
            "nonce": 0,
            "root": "0xa088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52e",
            "codeHash": "0x0eab42de4c3ceb9235fc91acffe746b29c29a8c366b7c60e4e67c466f36a4304c00fa9caf9d87976ba469bcbe06713b435f091ef2769fb160cdab33d3670680e",
            "key": "0xf9f9eb9ec7d3702a99c3fc3b833198efecb2b9a79b8767a5be54677c7d792b475c53b915275dd885297f70c1b2786ecfdef744fe4f467412cf13578bdfcf2253"
        },
        "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000002": {
            "balance": "44",
            "nonce": 0,
            "root": "0xa088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52e",
            "codeHash": "0x0eab42de4c3ceb9235fc91acffe746b29c29a8c366b7c60e4e67c466f36a4304c00fa9caf9d87976ba469bcbe06713b435f091ef2769fb160cdab33d3670680e",
            "key": "0x313afc48e961fe7c11204f3e10097ce9e53b17998614717ab19066874b400d5785b42327ba0eaed7336b06c7e36a472d9186bd86c90edd87a910c739d421b60e"
        },
        "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000102": {
            "balance": "0",
            "nonce": 0,
            "root": "0xa088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52e",
            "codeHash": "0xfa8bf5899d2d8e5861752987c14275e5f4a07d117bfed0cde4da98e290766a5896f730257962b0f23dba44b0c07544c3ef30c400aa4e94bee4b4997c85f5bc3a",
            "code": "0x03030303030303",
            "key": "0x82031ff34c79fb3dbb85b0ed3e7ac22ea265287ed60b3b4ee58ba01a2af820bad1acba2446792e714fc9eb64e837485e26d6ec4e209d220cde34ac4fcf2cd682"
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
	obj1 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x01}))
	obj1.AddBalance(big.NewInt(22))
	obj2 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x01, 0x02}))
	obj2.SetCode(crypto.Keccak512Hash([]byte{3, 3, 3, 3, 3, 3, 3}), []byte{3, 3, 3, 3, 3, 3, 3})
	obj3 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x02}))
	obj3.SetBalance(big.NewInt(44))
	obj4 := s.state.GetOrNewStateObject(common.BytesToAddress([]byte{0x00}))
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
	want := `{"root":"0x653c28c5861f202130bac9569e9c89fd616adb4336d8d13c092056c7dcdb34eb0c2bdb4039487be51b273c751f589e482b9398645cce41ef71505165fa4db41e"}
{"balance":"44","nonce":0,"root":"0xa088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52e","codeHash":"0x0eab42de4c3ceb9235fc91acffe746b29c29a8c366b7c60e4e67c466f36a4304c00fa9caf9d87976ba469bcbe06713b435f091ef2769fb160cdab33d3670680e","address":"Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000002","key":"0x313afc48e961fe7c11204f3e10097ce9e53b17998614717ab19066874b400d5785b42327ba0eaed7336b06c7e36a472d9186bd86c90edd87a910c739d421b60e"}
{"balance":"0","nonce":0,"root":"0xa088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52e","codeHash":"0xfa8bf5899d2d8e5861752987c14275e5f4a07d117bfed0cde4da98e290766a5896f730257962b0f23dba44b0c07544c3ef30c400aa4e94bee4b4997c85f5bc3a","code":"0x03030303030303","address":"Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000102","key":"0x82031ff34c79fb3dbb85b0ed3e7ac22ea265287ed60b3b4ee58ba01a2af820bad1acba2446792e714fc9eb64e837485e26d6ec4e209d220cde34ac4fcf2cd682"}
{"balance":"1337","nonce":0,"root":"0xa088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52e","codeHash":"0x0eab42de4c3ceb9235fc91acffe746b29c29a8c366b7c60e4e67c466f36a4304c00fa9caf9d87976ba469bcbe06713b435f091ef2769fb160cdab33d3670680e","address":"Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000","key":"0xa8620b2ebeca41fbc773bb837b5e724d6eb2de570d99858df0d7d97067fb8103b21757873b735097b35d3bea8fd1c359a9e8a63c1540c76c9784cf8d975e995c"}
{"balance":"22","nonce":0,"root":"0xa088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52e","codeHash":"0x0eab42de4c3ceb9235fc91acffe746b29c29a8c366b7c60e4e67c466f36a4304c00fa9caf9d87976ba469bcbe06713b435f091ef2769fb160cdab33d3670680e","address":"Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001","key":"0xf9f9eb9ec7d3702a99c3fc3b833198efecb2b9a79b8767a5be54677c7d792b475c53b915275dd885297f70c1b2786ecfdef744fe4f467412cf13578bdfcf2253"}
`
	if got != want {
		t.Errorf("DumpToCollector mismatch:\ngot: %s\nwant: %s\n", got, want)
	}
}

func TestNull(t *testing.T) {
	s := newStateEnv()
	address := common.MustParseAddress("Q0000000000000000000000000000000000000000000000000000000000000000823140710bf13990e4500136726d8b5599aabbccddeeff001122334455667788")
	s.state.CreateAccount(address)
	//value := common.FromHex("0x823140710bf13990e4500136726d8b55")
	var value common.StorageValue64

	s.state.SetState(address, common.Hash512{}, value)
	s.state.Commit(0, false)

	if value := s.state.GetState(address, common.Hash512{}); value != (common.StorageValue64{}) {
		t.Errorf("expected empty current value, got %x", value)
	}
	if value := s.state.GetCommittedState(address, common.Hash512{}); value != (common.StorageValue64{}) {
		t.Errorf("expected empty committed value, got %x", value)
	}
}

func TestSnapshot(t *testing.T) {
	stateobjaddr := common.BytesToAddress([]byte("aa"))
	var storageaddr common.Hash512
	data1 := common.BytesToStorageValue64([]byte{42})
	data2 := common.BytesToStorageValue64([]byte{43})
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
	if v := s.state.GetCommittedState(stateobjaddr, storageaddr); v != (common.StorageValue64{}) {
		t.Errorf("wrong committed storage value %v, want %v", v, common.StorageValue64{})
	}

	// revert up to the genesis state and ensure correct content
	s.state.RevertToSnapshot(genesis)
	if v := s.state.GetState(stateobjaddr, storageaddr); v != (common.StorageValue64{}) {
		t.Errorf("wrong storage value %v, want %v", v, common.StorageValue64{})
	}
	if v := s.state.GetCommittedState(stateobjaddr, storageaddr); v != (common.StorageValue64{}) {
		t.Errorf("wrong committed storage value %v, want %v", v, common.StorageValue64{})
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
	var storageaddr common.Hash512

	data0 := common.BytesToStorageValue64([]byte{17})
	data1 := common.BytesToStorageValue64([]byte{18})

	state.SetState(stateobjaddr0, storageaddr, data0)
	state.SetState(stateobjaddr1, storageaddr, data1)

	// db, trie are already non-empty values
	so0 := state.getStateObject(stateobjaddr0)
	so0.SetBalance(big.NewInt(42))
	so0.SetNonce(43)
	so0.SetCode(crypto.Keccak512Hash([]byte{'c', 'a', 'f', 'e'}), []byte{'c', 'a', 'f', 'e'})
	so0.deleted = false
	state.setStateObject(so0)

	root, _ := state.Commit(0, false)
	state, _ = New(root, state.db, state.snaps)

	// and one with deleted == true
	so1 := state.getStateObject(stateobjaddr1)
	so1.SetBalance(big.NewInt(52))
	so1.SetNonce(53)
	so1.SetCode(crypto.Keccak512Hash([]byte{'c', 'a', 'f', 'e', '2'}), []byte{'c', 'a', 'f', 'e', '2'})
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
