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
	"math"
	"sync"
)

// WeightedCache is an LRU cache bounded both by its number of items and by
// their total weight, which a function of each value gives. It is for values
// whose size varies widely, such as blocks: a count alone lets a few large
// ones take far more memory than many small ones.
//
// OBS: Like SizeConstrainedCache, it assumes that items are content-addressed:
// two Add(..) with the same key K always have the same value V, and so the
// same weight.
type WeightedCache[K comparable, V any] struct {
	lock      sync.Mutex
	lru       BasicLRU[K, V]
	weightOf  func(V) uint64
	weight    uint64
	maxItems  int
	maxWeight uint64
}

// NewWeightedCache creates a cache of at most maxItems items whose weights,
// as given by weightOf, add up to at most maxWeight.
func NewWeightedCache[K comparable, V any](maxItems int, maxWeight uint64, weightOf func(V) uint64) *WeightedCache[K, V] {
	return &WeightedCache[K, V]{
		lru:       NewBasicLRU[K, V](math.MaxInt),
		weightOf:  weightOf,
		maxItems:  max(maxItems, 1),
		maxWeight: maxWeight,
	}
}

// Add adds a value to the cache, evicting the least recently used items until
// it fits. It returns true if an item was evicted. A value heavier than the
// whole cache is not added.
// OBS: The value is _not_ copied on Add, so the caller must not modify it afterwards.
func (c *WeightedCache[K, V]) Add(key K, value V) (evicted bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	// A key that is present already has its value and weight: only bump it.
	if _, ok := c.lru.Get(key); ok {
		return false
	}
	weight := c.weightOf(value)
	if weight > c.maxWeight {
		return false
	}
	for c.lru.Len() >= c.maxItems || c.weight+weight > c.maxWeight {
		_, oldest, ok := c.lru.RemoveOldest()
		if !ok {
			break
		}
		c.weight -= c.weightOf(oldest)
		evicted = true
	}
	c.lru.Add(key, value)
	c.weight += weight
	return evicted
}

// Get looks up a key's value from the cache.
func (c *WeightedCache[K, V]) Get(key K) (V, bool) {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.lru.Get(key)
}

// Contains reports whether the given key exists in the cache.
func (c *WeightedCache[K, V]) Contains(key K) bool {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.lru.Contains(key)
}

// Len returns the number of items in the cache.
func (c *WeightedCache[K, V]) Len() int {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.lru.Len()
}

// Weight returns the total weight of the items in the cache.
func (c *WeightedCache[K, V]) Weight() uint64 {
	c.lock.Lock()
	defer c.lock.Unlock()

	return c.weight
}

// Purge empties the cache.
func (c *WeightedCache[K, V]) Purge() {
	c.lock.Lock()
	defer c.lock.Unlock()

	c.lru.Purge()
	c.weight = 0
}
