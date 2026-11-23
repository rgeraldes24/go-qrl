// Copyright 2017 The go-ethereum Authors
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

package qrl

import (
	"bytes"
	"fmt"
	"math/big"
	"reflect"
	"slices"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/state"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/trie"
)

var dumper = spew.ConfigState{Indent: "    "}

func accountRangeTest(t *testing.T, trie *state.Trie, statedb *state.StateDB, start common.Hash512, requestedNum int, expectedNum int) state.IteratorDump {
	result := statedb.IteratorDump(&state.DumpConfig{
		SkipCode:          true,
		SkipStorage:       true,
		OnlyWithAddresses: false,
		Start:             start.Bytes(),
		Max:               uint64(requestedNum),
	})

	if len(result.Accounts) != expectedNum {
		t.Fatalf("expected %d results, got %d", expectedNum, len(result.Accounts))
	}
	for address := range result.Accounts {
		if address == (common.Address{}) {
			t.Fatalf("empty address returned")
		}
		if !statedb.Exist(address) {
			t.Fatalf("account not found in state %s", address.Hex())
		}
	}
	return result
}

func TestAccountRange(t *testing.T) {
	t.Parallel()

	var (
		statedb = state.NewDatabaseWithConfig(rawdb.NewMemoryDatabase(), &trie.Config{Preimages: true})
		sdb, _  = state.New(types.EmptyRootHash, statedb, nil)
		addrs   = [AccountRangeMaxResults * 2]common.Address{}
		m       = map[common.Address]bool{}
	)

	for i := range addrs {
		hash := common.HexToHash512(fmt.Sprintf("%x", i))
		addr := common.BytesToAddress(crypto.Keccak512Hash(hash.Bytes()).Bytes())
		addrs[i] = addr
		sdb.SetBalance(addrs[i], big.NewInt(1))
		if _, ok := m[addr]; ok {
			t.Fatalf("bad")
		} else {
			m[addr] = true
		}
	}
	root, _ := sdb.Commit(0, true)
	sdb, _ = state.New(root, statedb, nil)

	trie, err := statedb.OpenTrie(root)
	if err != nil {
		t.Fatal(err)
	}
	accountRangeTest(t, &trie, sdb, common.Hash512{}, AccountRangeMaxResults/2, AccountRangeMaxResults/2)
	// test pagination
	firstResult := accountRangeTest(t, &trie, sdb, common.Hash512{}, AccountRangeMaxResults, AccountRangeMaxResults)
	secondResult := accountRangeTest(t, &trie, sdb, common.BytesToHash512(firstResult.Next), AccountRangeMaxResults, AccountRangeMaxResults)

	hList := make([]common.Hash512, 0)
	for addr1 := range firstResult.Accounts {
		// If address is empty, then it makes no sense to compare
		// them as they might be two different accounts.
		if addr1 == (common.Address{}) {
			continue
		}
		if _, duplicate := secondResult.Accounts[addr1]; duplicate {
			t.Fatalf("pagination test failed:  results should not overlap")
		}
		hList = append(hList, crypto.Keccak512Hash(addr1.Bytes()))
	}
	// Test to see if it's possible to recover from the middle of the previous
	// set and get an even split between the first and second sets.
	slices.SortFunc(hList, common.Hash512.Cmp)
	middleH := hList[AccountRangeMaxResults/2]
	middleResult := accountRangeTest(t, &trie, sdb, middleH, AccountRangeMaxResults, AccountRangeMaxResults)
	missing, infirst, insecond := 0, 0, 0
	for h := range middleResult.Accounts {
		if _, ok := firstResult.Accounts[h]; ok {
			infirst++
		} else if _, ok := secondResult.Accounts[h]; ok {
			insecond++
		} else {
			missing++
		}
	}
	if missing != 0 {
		t.Fatalf("%d hashes in the 'middle' set were neither in the first not the second set", missing)
	}
	if infirst != AccountRangeMaxResults/2 {
		t.Fatalf("Imbalance in the number of first-test results: %d != %d", infirst, AccountRangeMaxResults/2)
	}
	if insecond != AccountRangeMaxResults/2 {
		t.Fatalf("Imbalance in the number of second-test results: %d != %d", insecond, AccountRangeMaxResults/2)
	}
}

func TestEmptyAccountRange(t *testing.T) {
	t.Parallel()

	var (
		statedb = state.NewDatabase(rawdb.NewMemoryDatabase())
		st, _   = state.New(types.EmptyRootHash, statedb, nil)
	)
	// Commit(although nothing to flush) and re-init the statedb
	st.Commit(0, true)
	st, _ = state.New(types.EmptyRootHash, statedb, nil)

	results := st.IteratorDump(&state.DumpConfig{
		SkipCode:          true,
		SkipStorage:       true,
		OnlyWithAddresses: true,
		Max:               uint64(AccountRangeMaxResults),
	})
	if bytes.Equal(results.Next, (common.Hash512{}).Bytes()) {
		t.Fatalf("Empty results should not return a second page")
	}
	if len(results.Accounts) != 0 {
		t.Fatalf("Empty state should not return addresses: %v", results.Accounts)
	}
}

func TestStorageRangeAt(t *testing.T) {
	t.Parallel()

	// Create a state where account 0x010000... has a few storage entries.
	var (
		db     = state.NewDatabaseWithConfig(rawdb.NewMemoryDatabase(), &trie.Config{Preimages: true})
		sdb, _ = state.New(types.EmptyRootHash, db, nil)
		addr   = common.Address{0x01}
		keys   = []common.Hash512{ // Keccak-512 hashes of the full 64-byte storage keys.
			common.HexToHash512("2bd627c5ce0e5271cb2bdd3a6d41a244c096e1fcf077641aec2a2d2cb56a5ca1a492fa678df48995cd2013d71ddc6f7b12bde3c0b995f11d98c25c5ca5ee8de8"),
			common.HexToHash512("814ff99d5a7b359389335e51b068c05dfc652932a7267034959912c9ac265c0b06d15431e0889f4eac982dc0f8aa85fc5adbf5a87f6e7d1f43e14ebe1c2f91cf"),
			common.HexToHash512("e537d435e06a289739884b5badf4e7925ff5eee760855aba76376969feaef63e61f27a5bb7e5e3ef53add5caa23d9e4eea445e326745e0cc5f0dc03218e80c84"),
			common.HexToHash512("f704854c445d4d892a7448e435676ab54d8d3aa5beac9e2c1595e709fc5405ac3a3d614d2cb7b04c97c10bd04b645eade488317cccf37b6c6ee2640b5898bbe7"),
		}
		storage = storageMap{
			keys[0]: {Key: &common.Hash512{0x03}, Value: common.StorageValue64{0x04}},
			keys[1]: {Key: &common.Hash512{0x01}, Value: common.StorageValue64{0x03}},
			keys[2]: {Key: &common.Hash512{0x02}, Value: common.StorageValue64{0x01}},
			keys[3]: {Key: &common.Hash512{0x04}, Value: common.StorageValue64{0x02}},
		}
	)
	for _, entry := range storage {
		sdb.SetState(addr, *entry.Key, entry.Value)
	}
	root, _ := sdb.Commit(0, false)
	sdb, _ = state.New(root, db, nil)

	// Check a few combinations of limit and start/end.
	tests := []struct {
		start []byte
		limit int
		want  StorageRangeResult
	}{
		{
			start: []byte{}, limit: 0,
			want: StorageRangeResult{storageMap{}, &keys[0]},
		},
		{
			start: []byte{}, limit: 100,
			want: StorageRangeResult{storage, nil},
		},
		{
			start: []byte{}, limit: 2,
			want: StorageRangeResult{storageMap{keys[0]: storage[keys[0]], keys[1]: storage[keys[1]]}, &keys[2]},
		},
		{
			start: []byte{0x00}, limit: 4,
			want: StorageRangeResult{storage, nil},
		},
		{
			start: keys[1][:], limit: 2,
			want: StorageRangeResult{storageMap{keys[1]: storage[keys[1]], keys[2]: storage[keys[2]]}, &keys[3]},
		},
	}
	for _, test := range tests {
		result, err := storageRangeAt(sdb, root, addr, test.start, test.limit)
		if err != nil {
			t.Error(err)
		}
		if !reflect.DeepEqual(result, test.want) {
			t.Fatalf("wrong result for range %#x.., limit %d:\ngot %s\nwant %s",
				test.start, test.limit, dumper.Sdump(result), dumper.Sdump(&test.want))
		}
	}
}
