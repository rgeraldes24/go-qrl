// Copyright 2023 The go-ethereum Authors
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

package types

import (
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/log"
)

var (
	// EmptyRootHash is the known root hash of an empty trie.
	EmptyRootHash = crypto.Keccak512Hash([]byte{0x80})

	// EmptyCodeHash is the known hash of the empty QRVM bytecode.
	EmptyCodeHash = crypto.Keccak512Hash(nil)

	// EmptyTxsHash is the known hash of the empty transaction set.
	EmptyTxsHash = crypto.Keccak512Hash([]byte{0x80})

	// EmptyReceiptsHash is the known hash of the empty receipt set.
	EmptyReceiptsHash = crypto.Keccak512Hash([]byte{0x80})

	// EmptyWithdrawalsHash is the known hash of the empty withdrawal set.
	EmptyWithdrawalsHash = crypto.Keccak512Hash([]byte{0x80})
)

// TrieRootHash returns the hash itself if it's non-empty or the predefined
// emptyHash one instead.
func TrieRootHash(hash common.Hash512) common.Hash512 {
	if hash == (common.Hash512{}) {
		log.Error("Zero trie root hash!")
		return EmptyRootHash
	}
	return hash
}
