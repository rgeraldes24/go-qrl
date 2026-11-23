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

package lru

import (
	"testing"
)

func weightOfBytes(value []byte) uint64 { return uint64(len(value)) }

func TestWeightedCacheEvictsByWeight(t *testing.T) {
	cache := NewWeightedCache[int, []byte](100, 10, weightOfBytes)

	for key := range 5 {
		cache.Add(key, make([]byte, 2))
	}
	if have, want := cache.Weight(), uint64(10); have != want {
		t.Fatalf("weight: have %d, want %d", have, want)
	}

	// A value of weight 5 takes the place of the three oldest.
	if !cache.Add(5, make([]byte, 5)) {
		t.Fatal("expected an eviction")
	}
	for key := range 3 {
		if cache.Contains(key) {
			t.Errorf("key %d should have been evicted", key)
		}
	}
	for _, key := range []int{3, 4, 5} {
		if !cache.Contains(key) {
			t.Errorf("key %d should be cached", key)
		}
	}
	if have, want := cache.Weight(), uint64(9); have != want {
		t.Fatalf("weight: have %d, want %d", have, want)
	}
}

func TestWeightedCacheEvictsByCount(t *testing.T) {
	cache := NewWeightedCache[int, []byte](2, 1000, weightOfBytes)

	cache.Add(0, []byte{0})
	cache.Add(1, []byte{1})
	cache.Get(0) // 1 is now the least recently used
	cache.Add(2, []byte{2})

	if cache.Contains(1) {
		t.Error("key 1 should have been evicted")
	}
	if !cache.Contains(0) || !cache.Contains(2) {
		t.Error("keys 0 and 2 should be cached")
	}
	if have, want := cache.Len(), 2; have != want {
		t.Fatalf("len: have %d, want %d", have, want)
	}
}

func TestWeightedCacheSkipsValueHeavierThanCache(t *testing.T) {
	cache := NewWeightedCache[int, []byte](10, 10, weightOfBytes)
	cache.Add(0, make([]byte, 4))

	if cache.Add(1, make([]byte, 11)) {
		t.Error("a value heavier than the cache should not evict anything")
	}
	if cache.Contains(1) || !cache.Contains(0) {
		t.Error("the heavy value should be skipped and the light one kept")
	}
}

func TestWeightedCacheAddExistingKeepsWeight(t *testing.T) {
	cache := NewWeightedCache[int, []byte](10, 10, weightOfBytes)
	cache.Add(0, make([]byte, 4))
	cache.Add(0, make([]byte, 4))

	if have, want := cache.Weight(), uint64(4); have != want {
		t.Fatalf("weight: have %d, want %d", have, want)
	}
	cache.Purge()
	if cache.Len() != 0 || cache.Weight() != 0 {
		t.Fatalf("purge left %d items of weight %d", cache.Len(), cache.Weight())
	}
}
