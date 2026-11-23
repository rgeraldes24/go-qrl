// Copyright 2026 The go-ethereum Authors
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

package core

import (
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/lru"
	"github.com/theQRL/go-qrl/core/types"
)

// TestBodyCacheBoundedInBytes checks that large bodies are evicted by the
// cache's byte budget well before its item count is reached.
func TestBodyCacheBoundedInBytes(t *testing.T) {
	// A body of one transaction carrying 1 MB of data.
	tx := types.NewTx(&types.DynamicFeeTx{Data: make([]byte, 1024*1024)})
	body := &types.Body{Transactions: []*types.Transaction{tx}, Withdrawals: []*types.Withdrawal{{}}}
	if size := bodySize(body); size <= tx.Size() {
		t.Fatalf("body size %d does not count its withdrawal", size)
	}

	cache := lru.NewWeightedCache[common.Hash](bodyCacheLimit, 8*bodySize(body), bodySize)
	for i := range 64 {
		cache.Add(common.Hash{byte(i)}, body)
	}
	if have, want := cache.Len(), 8; have != want {
		t.Fatalf("cached bodies: have %d, want %d", have, want)
	}
	if cache.Weight() > 8*bodySize(body) {
		t.Fatalf("cache weight %d over its budget %d", cache.Weight(), 8*bodySize(body))
	}
}

func TestReceiptsSize(t *testing.T) {
	receipts := []*types.Receipt{
		{Logs: []*types.Log{{Data: make([]byte, 1000), Topics: make([]common.LogTopic, 2)}}},
		{},
	}
	if have, want := receiptsSize(receipts), uint64(receipts[0].Size()+receipts[1].Size()); have != want {
		t.Fatalf("receipts size: have %d, want %d", have, want)
	}
	if receiptsSize(receipts) < 1000 {
		t.Fatalf("receipts size %d does not count the logged data", receiptsSize(receipts))
	}
}
