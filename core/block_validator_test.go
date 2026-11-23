// Copyright 2015 The go-ethereum Authors
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
	"errors"
	"math/big"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/consensus/beacon"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/trie"
)

// Tests that simple header verification works, for both good and bad blocks.
func TestHeaderVerification(t *testing.T) {
	testHeaderVerification(t, rawdb.HashScheme)
	testHeaderVerification(t, rawdb.PathScheme)
}

func testHeaderVerification(t *testing.T, scheme string) {
	// Create a simple chain to verify
	var (
		gspec        = &Genesis{Config: params.TestChainConfig}
		_, blocks, _ = GenerateChainWithGenesis(gspec, beacon.NewFaker(), 8, nil)
	)
	headers := make([]*types.Header, len(blocks))
	for i, block := range blocks {
		headers[i] = block.Header()
	}
	// Run the header checker for blocks one-by-one, checking for both valid and invalid nonces
	chain, _ := NewBlockChain(rawdb.NewMemoryDatabase(), DefaultCacheConfigWithScheme(scheme), gspec, beacon.NewFaker(), vm.Config{}, nil)
	defer chain.Stop()

	for i := range blocks {
		for j, valid := range []bool{true, false} {
			var results <-chan error

			if valid {
				engine := beacon.NewFaker()
				_, results = engine.VerifyHeaders(chain, []*types.Header{headers[i]})
			} else {
				engine := beacon.NewFakeFailer(headers[i].Number.Uint64())
				_, results = engine.VerifyHeaders(chain, []*types.Header{headers[i]})
			}
			// Wait for the verification result
			select {
			case result := <-results:
				if (result == nil) != valid {
					t.Errorf("test %d.%d: validity mismatch: have %v, want %v", i, j, result, valid)
				}
			case <-time.After(time.Second):
				t.Fatalf("test %d.%d: verification timeout", i, j)
			}
			// Make sure no more data is returned
			select {
			case result := <-results:
				t.Fatalf("test %d.%d: unexpected result returned: %v", i, j, result)
			case <-time.After(25 * time.Millisecond):
			}
		}
		chain.InsertChain(blocks[i : i+1])
	}
}

// Tests that a block is accepted at the EIP-7934 size cap and rejected above it.
func TestBlockSizeCap(t *testing.T) {
	gspec := &Genesis{Config: params.TestChainConfig}
	chain, err := NewBlockChain(rawdb.NewMemoryDatabase(), nil, gspec, beacon.NewFaker(), vm.Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer chain.Stop()

	// makeBlock builds a child of genesis holding one transaction with the
	// given amount of calldata.
	makeBlock := func(dataLen uint64) *types.Block {
		tx := types.NewTx(&types.DynamicFeeTx{Data: make([]byte, dataLen)})
		header := &types.Header{ParentHash: chain.Genesis().Hash(), Number: big.NewInt(1)}
		return types.NewBlock(header, &types.Body{Transactions: []*types.Transaction{tx}}, nil, trie.NewStackTrie(nil))
	}
	// Everything around the calldata has a fixed size at this scale, so the
	// calldata length can be chosen to land the block exactly on the cap.
	overhead := makeBlock(params.MaxBlockSize).Size() - params.MaxBlockSize
	atCap := makeBlock(params.MaxBlockSize - overhead)
	if atCap.Size() != params.MaxBlockSize {
		t.Fatalf("block size mismatch: have %d, want %d", atCap.Size(), params.MaxBlockSize)
	}
	if err := chain.Validator().ValidateBody(atCap); err != nil {
		t.Fatalf("block at the size cap rejected: %v", err)
	}
	overCap := makeBlock(params.MaxBlockSize - overhead + 1)
	if err := chain.Validator().ValidateBody(overCap); !errors.Is(err, ErrBlockOversized) {
		t.Fatalf("block above the size cap: have error %v, want %v", err, ErrBlockOversized)
	}
}
