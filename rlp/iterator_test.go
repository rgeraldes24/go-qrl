// Copyright 2020 The go-ethereum Authors
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

package rlp

import (
	"testing"

	"github.com/theQRL/go-qrl/common/hexutil"
)

// TestIterator tests some basic things about the ListIterator. A more
// comprehensive test can be found in core/rlp_test.go, where we can
// use both types and rlp without dependency cycles.
func TestIterator(t *testing.T) {
	bodyRlpHex := "0xc4c20102c0"
	bodyRlp := hexutil.MustDecode(bodyRlpHex)

	it, err := NewListIterator(bodyRlp)
	if err != nil {
		t.Fatal(err)
	}
	// Check that txs exist
	if !it.Next() {
		t.Fatal("expected two elems, got zero")
	}
	txs := it.Value()

	// Check that uncles exist
	if !it.Next() {
		t.Fatal("expected two elems, got one")
	}
	txit, err := NewListIterator(txs)
	if err != nil {
		t.Fatal(err)
	}
	var i = 0
	for txit.Next() {
		if txit.err != nil {
			t.Fatal(txit.err)
		}
		i++
	}
	if exp := 2; i != exp {
		t.Errorf("count wrong, expected %d got %d", i, exp)
	}
}
