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

package qrlstats

import (
	"context"
	"math/big"
	"strconv"
	"testing"

	"github.com/theQRL/go-qrl/consensus/beacon"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/qrl"
	"github.com/theQRL/go-qrl/rpc"
	"github.com/theQRL/go-qrl/trie"
)

func TestParseQRLstatsURL(t *testing.T) {
	cases := []struct {
		url              string
		node, pass, host string
	}{
		{
			url:  `"debug meowsbits":mypass@ws://mordor.dash.fault.dev:3000`,
			node: "debug meowsbits", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `"debug @meowsbits":mypass@ws://mordor.dash.fault.dev:3000`,
			node: "debug @meowsbits", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `"debug: @meowsbits":mypass@ws://mordor.dash.fault.dev:3000`,
			node: "debug: @meowsbits", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `name:@ws://mordor.dash.fault.dev:3000`,
			node: "name", pass: "", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `name@ws://mordor.dash.fault.dev:3000`,
			node: "name", pass: "", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `:mypass@ws://mordor.dash.fault.dev:3000`,
			node: "", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `:@ws://mordor.dash.fault.dev:3000`,
			node: "", pass: "", host: "ws://mordor.dash.fault.dev:3000",
		},
	}

	for i, c := range cases {
		parts, err := parseQRLstatsURL(c.url)
		if err != nil {
			t.Fatal(err)
		}
		node, pass, host := parts[0], parts[1], parts[2]

		// unquote because the value provided will be used as a CLI flag value, so unescaped quotes will be removed
		nodeUnquote, err := strconv.Unquote(node)
		if err == nil {
			node = nodeUnquote
		}

		if node != c.node {
			t.Errorf("case=%d mismatch node value, got: %v ,want: %v", i, node, c.node)
		}
		if pass != c.pass {
			t.Errorf("case=%d mismatch pass value, got: %v ,want: %v", i, pass, c.pass)
		}
		if host != c.host {
			t.Errorf("case=%d mismatch host value, got: %v ,want: %v", i, host, c.host)
		}
	}
}

var _ fullNodeBackend = (*qrl.QRLAPIBackend)(nil)

type testBackend struct {
	backend
	head  *types.Block
	block *types.Block
}

func (b *testBackend) CurrentBlock() *types.Header {
	return b.head.Header()
}

func (b *testBackend) BlockByNumber(ctx context.Context, number rpc.BlockNumber) (*types.Block, error) {
	if b.block == nil || b.block.NumberU64() != uint64(number) {
		return nil, nil
	}
	return b.block, nil
}

func (b *testBackend) SuggestGasTipCap(ctx context.Context) (*big.Int, error) {
	return big.NewInt(1), nil
}

func TestAssembleBlockStatsFullNode(t *testing.T) {
	tx := types.NewTx(&types.DynamicFeeTx{})
	block := types.NewBlock(&types.Header{Number: big.NewInt(1)}, &types.Body{Transactions: []*types.Transaction{tx}}, nil, trie.NewStackTrie(nil))

	s := &Service{backend: &testBackend{head: block, block: block}, engine: beacon.NewFaker()}
	stats := s.assembleBlockStats(nil)
	if stats == nil {
		t.Fatal("no stats for the current head")
	}
	if stats.Hash != block.Hash() {
		t.Errorf("hash mismatch, got: %v, want: %v", stats.Hash, block.Hash())
	}
	if len(stats.Txs) != 1 || stats.Txs[0].Hash != tx.Hash() {
		t.Errorf("transactions mismatch, got: %v, want: [%v]", stats.Txs, tx.Hash())
	}

	s = &Service{backend: &testBackend{head: block}, engine: beacon.NewFaker()}
	if stats := s.assembleBlockStats(nil); stats != nil {
		t.Errorf("got stats for an unavailable head: %v", stats)
	}
}
