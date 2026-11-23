// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package miner

import (
	"testing"

	"github.com/theQRL/go-qrl/core/types"
)

// Tests that the block builder stops adding transactions before the block
// reaches the size it is allowed to build.
func TestTxFitsSize(t *testing.T) {
	tx := types.NewTx(&types.DynamicFeeTx{Data: make([]byte, 1024)})

	env := &environment{size: maxBuildBlockSize - tx.Size() - 1}
	if !env.txFitsSize(tx) {
		t.Fatal("transaction below the size limit does not fit")
	}
	env.size++
	if env.txFitsSize(tx) {
		t.Fatal("transaction reaching the size limit fits")
	}
}
