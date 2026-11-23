// Copyright 2021 The go-ethereum Authors
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

package snap

import (
	"math/big"

	"github.com/theQRL/go-qrl/common"
)

// hashRange is a utility to handle ranges of hashes, Split up the
// hash-space into sections, and 'walk' over the sections
type hashRange struct {
	current *big.Int
	step    *big.Int
}

// newHashRange creates a new hashRange, initiated at the start position,
// and with the step set to fill the desired 'num' chunks
func newHashRange(start common.Hash512, num uint64) *hashRange {
	left := new(big.Int).Sub(hashSpace, start.Big())
	step := new(big.Int).Div(
		new(big.Int).Add(left, new(big.Int).SetUint64(num-1)),
		new(big.Int).SetUint64(num),
	)
	return &hashRange{
		current: start.Big(),
		step:    step,
	}
}

// Next pushes the hash range to the next interval.
func (r *hashRange) Next() bool {
	next := new(big.Int).Add(r.current, r.step)
	if next.Cmp(hashSpace) >= 0 {
		return false
	}
	r.current = next
	return true
}

// Start returns the first hash in the current interval.
func (r *hashRange) Start() common.Hash512 {
	return common.BigToHash512(r.current)
}

// End returns the last hash in the current interval.
func (r *hashRange) End() common.Hash512 {
	// If the end overflows (non divisible range), return a shorter interval
	next := new(big.Int).Add(r.current, r.step)
	if next.Cmp(hashSpace) >= 0 {
		return common.MaxHash512
	}
	return common.BigToHash512(next.Sub(next, common.Big1))
}

// incHash returns the next hash, in lexicographical order (a.k.a plus one)
func incHash(h common.Hash512) common.Hash512 {
	for i := len(h) - 1; i >= 0; i-- {
		h[i]++
		if h[i] != 0 {
			break
		}
	}
	return h
}
