// Copyright 2022 The go-ethereum Authors
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

package snapshot

import (
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/rawdb"
)

// Tests that the disk check resolves the full 64-byte account hash from a
// storage key, both halves of which must take part in the account lookup.
func TestCheckDanglingDiskStorage(t *testing.T) {
	var (
		account = common.Hash512{0: 0xaa, 40: 0xbb, 63: 0xcc}
		slot    = common.Hash512{0: 0x11, 63: 0x22}
	)
	db := rawdb.NewMemoryDatabase()
	rawdb.WriteAccountSnapshot(db, account, []byte{0x01})
	rawdb.WriteStorageSnapshot(db, account, slot, []byte{0x02})
	if err := checkDanglingDiskStorage(db); err != nil {
		t.Fatalf("storage with an account reported as dangling: %v", err)
	}
	// An account sharing only the first half of the hash does not own the storage.
	orphan := account
	orphan[63] = 0xdd
	rawdb.WriteStorageSnapshot(db, orphan, slot, []byte{0x02})
	if err := checkDanglingDiskStorage(db); err == nil {
		t.Fatal("storage without an account not reported as dangling")
	}
}
