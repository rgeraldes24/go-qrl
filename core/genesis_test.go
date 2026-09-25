// Copyright 2017 The go-ethereum Authors
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
	"encoding/json"
	"fmt"
	"math/big"
	"reflect"
	"testing"

	"github.com/davecgh/go-spew/spew"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/consensus/beacon"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/qrldb"
	"github.com/theQRL/go-qrl/trie"
	"github.com/theQRL/go-qrl/trie/triedb/pathdb"
)

func TestSetupGenesis(t *testing.T) {
	testSetupGenesis(t, rawdb.HashScheme)
	testSetupGenesis(t, rawdb.PathScheme)
}

func testSetupGenesis(t *testing.T, scheme string) {
	var (
		customghash = common.HexToHash("0x1856ad799c28a5f8f726255d7fe1e02648749baa6a2194836756a63e1e24f463")
		customg     = Genesis{
			Config: &params.ChainConfig{},
			Alloc: GenesisAlloc{
				{1}: {Balance: big.NewInt(1), Storage: map[common.Hash]common.StorageValue64{{1}: {1}}},
			},
		}
		oldcustomg = customg
	)
	oldcustomg.Config = &params.ChainConfig{}

	tests := []struct {
		name       string
		fn         func(qrldb.Database) (*params.ChainConfig, common.Hash, error)
		wantConfig *params.ChainConfig
		wantHash   common.Hash
		wantErr    error
	}{
		{
			name: "genesis without ChainConfig",
			fn: func(db qrldb.Database) (*params.ChainConfig, common.Hash, error) {
				return SetupGenesisBlock(db, trie.NewDatabase(db, newDbConfig(scheme)), new(Genesis))
			},
			wantErr:    errGenesisNoConfig,
			wantConfig: params.AllBeaconProtocolChanges,
		},
		// TODO(now.youtrack.cloud/issue/TGZ-16)
		/*
			{
				name: "no block in DB, genesis == nil",
				fn: func(db qrldb.Database) (*params.ChainConfig, common.Hash, error) {
					return SetupGenesisBlock(db, trie.NewDatabase(db, newDbConfig(scheme)), nil)
				},
				// wantHash:   params.MainnetGenesisHash,
				wantHash:   common.HexToHash("b3de630542cf9acf842e24f428c7c21b7824b38a7718a632e424b58ba0f562c6"),
				wantConfig: params.MainnetChainConfig,
			},
			{
				name: "mainnet block in DB, genesis == nil",
				fn: func(db qrldb.Database) (*params.ChainConfig, common.Hash, error) {
					DefaultGenesisBlock().MustCommit(db, trie.NewDatabase(db, newDbConfig(scheme)))
					return SetupGenesisBlock(db, trie.NewDatabase(db, newDbConfig(scheme)), nil)
				},
				wantHash:   params.MainnetGenesisHash,
				wantConfig: params.MainnetChainConfig,
			},
		*/
		{
			name: "custom block in DB, genesis == nil",
			fn: func(db qrldb.Database) (*params.ChainConfig, common.Hash, error) {
				tdb := trie.NewDatabase(db, newDbConfig(scheme))
				customg.Commit(db, tdb)
				return SetupGenesisBlock(db, tdb, nil)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
		// TODO(now.youtrack.cloud/issue/TGZ-16)
		/*
			{
				name: "custom block in DB, genesis == goerli",
				fn: func(db qrldb.Database) (*params.ChainConfig, common.Hash, error) {
					tdb := trie.NewDatabase(db, newDbConfig(scheme))
					customg.Commit(db, tdb)
					return SetupGenesisBlock(db, tdb, DefaultGoerliGenesisBlock())
				},
				wantErr:    &GenesisMismatchError{Stored: customghash, New: params.GoerliGenesisHash},
				wantHash:   params.GoerliGenesisHash,
				wantConfig: params.GoerliChainConfig,
			},
		*/
		{
			name: "compatible config in DB",
			fn: func(db qrldb.Database) (*params.ChainConfig, common.Hash, error) {
				tdb := trie.NewDatabase(db, newDbConfig(scheme))
				oldcustomg.Commit(db, tdb)
				return SetupGenesisBlock(db, tdb, &customg)
			},
			wantHash:   customghash,
			wantConfig: customg.Config,
		},
	}

	for _, test := range tests {
		db := rawdb.NewMemoryDatabase()
		config, hash, err := test.fn(db)
		// Check the return values.
		if !reflect.DeepEqual(err, test.wantErr) {
			spew := spew.ConfigState{DisablePointerAddresses: true, DisableCapacities: true}
			t.Errorf("%s: returned error %#v, want %#v", test.name, spew.NewFormatter(err), spew.NewFormatter(test.wantErr))
		}
		if !reflect.DeepEqual(config, test.wantConfig) {
			t.Errorf("%s:\nreturned %v\nwant     %v", test.name, config, test.wantConfig)
		}
		if hash != test.wantHash {
			t.Errorf("%s: returned hash %s, want %s", test.name, hash.Hex(), test.wantHash.Hex())
		} else if err == nil {
			// Check database content.
			stored := rawdb.ReadBlock(db, test.wantHash, 0)
			if stored.Hash() != test.wantHash {
				t.Errorf("%s: block in DB has hash %s, want %s", test.name, stored.Hash(), test.wantHash)
			}
		}
	}
}

// Tests that reopening a database with a genesis whose chain ID differs from
// the stored one fails and leaves the stored chain config unchanged.
func TestReopenWithDifferentChainID(t *testing.T) {
	for _, ids := range [][2]int64{{1, 1337}, {1337, 32382}} {
		var (
			db   = rawdb.NewMemoryDatabase()
			oldg = &Genesis{Config: &params.ChainConfig{ChainID: big.NewInt(ids[0])}}
			newg = &Genesis{Config: &params.ChainConfig{ChainID: big.NewInt(ids[1])}}
		)
		chain, err := NewBlockChain(db, nil, oldg, beacon.NewFaker(), vm.Config{}, nil)
		if err != nil {
			t.Fatalf("failed to create chain: %v", err)
		}
		_, blocks, _ := GenerateChainWithGenesis(oldg, beacon.NewFaker(), 4, nil)
		if _, err := chain.InsertChain(blocks); err != nil {
			t.Fatalf("failed to insert chain: %v", err)
		}
		chain.Stop()

		want := fmt.Sprintf("mismatching chain ID in database (have %d, want %d)", ids[0], ids[1])
		if _, err := NewBlockChain(db, nil, newg, beacon.NewFaker(), vm.Config{}, nil); err == nil || err.Error() != want {
			t.Errorf("chain ID %d -> %d: have error %v, want %s", ids[0], ids[1], err, want)
		}
		if stored := rawdb.ReadChainConfig(db, chain.Genesis().Hash()); stored.ChainID.Int64() != ids[0] {
			t.Errorf("chain ID %d -> %d: stored chain ID changed to %v", ids[0], ids[1], stored.ChainID)
		}
	}
}

// TestGenesisHashes checks the congruity of default genesis data to
// corresponding hardcoded genesis hash values.
func TestGenesisHashes(t *testing.T) {
	for i, c := range []struct {
		genesis *Genesis
		want    common.Hash
	}{
		// TODO(now.youtrack.cloud/issue/TGZ-16)
		{DefaultGenesisBlock(), params.MainnetGenesisHash},
		{DefaultBetaNetGenesisBlock(), params.BetaNetGenesisHash},
		{DefaultTestnetGenesisBlock(), params.TestnetGenesisHash},
	} {
		// Test via MustCommit
		db := rawdb.NewMemoryDatabase()
		if have := c.genesis.MustCommit(db, trie.NewDatabase(db, trie.HashDefaults)).Hash(); have != c.want {
			t.Errorf("case: %d a), want: %s, got: %s", i, c.want.Hex(), have.Hex())
		}
		// Test via ToBlock
		if have := c.genesis.ToBlock().Hash(); have != c.want {
			t.Errorf("case: %d a), want: %s, got: %s", i, c.want.Hex(), have.Hex())
		}
	}
}

// TestGenesisExtraDataLen checks length of extra data
// should be exactly 32 bytes
func TestGenesisExtraDataLen(t *testing.T) {
	for i, c := range []struct {
		genesis *Genesis
	}{
		{DefaultGenesisBlock()},
		{DefaultBetaNetGenesisBlock()},
		{DefaultTestnetGenesisBlock()},
	} {
		if len(c.genesis.ExtraData) != 32 {
			t.Errorf("case: %d a), want: %d, got: %d", i, 32, len(c.genesis.ExtraData))
		}
	}
}

func TestGenesis_Commit(t *testing.T) {
	genesis := &Genesis{
		Config: params.TestChainConfig,
		// basefee is nil
	}

	db := rawdb.NewMemoryDatabase()
	genesisBlock := genesis.MustCommit(db, trie.NewDatabase(db, trie.HashDefaults))

	if genesis.BaseFee != nil {
		t.Fatalf("assumption wrong")
	}

	// This value should have been set as default in the ToBlock method.
	if genesisBlock.BaseFee().Cmp(new(big.Int).SetUint64(params.InitialBaseFee)) != 0 {
		t.Errorf("assumption wrong: want: %d, got: %v", params.InitialBaseFee, genesisBlock.BaseFee())
	}

	// Expect the stored basefee to be the basefee of the genesis block.
	blk := rawdb.ReadBlock(db, genesisBlock.Hash(), 0)
	if blk == nil {
		t.Errorf("unable to retrieve block %d for canonical hash: %s", blk.NumberU64(), blk.Hash())
		return
	}

	if blk.BaseFee().Cmp(genesisBlock.BaseFee()) != 0 {
		t.Errorf("inequal difficulty; stored: %v, genesisBlock: %v", blk.BaseFee(), genesisBlock.BaseFee())
	}
}

func TestReadWriteGenesisAlloc(t *testing.T) {
	var (
		db    = rawdb.NewMemoryDatabase()
		alloc = &GenesisAlloc{
			{1}: {Balance: big.NewInt(1), Storage: map[common.Hash]common.StorageValue64{{1}: {1}}},
			{2}: {Balance: big.NewInt(2), Storage: map[common.Hash]common.StorageValue64{{2}: {2}}},
		}
		hash, _ = alloc.deriveHash()
	)
	blob, _ := json.Marshal(alloc)
	rawdb.WriteGenesisStateSpec(db, hash, blob)

	var reload GenesisAlloc
	err := reload.UnmarshalJSON(rawdb.ReadGenesisStateSpec(db, hash))
	if err != nil {
		t.Fatalf("Failed to load genesis state %v", err)
	}
	if len(reload) != len(*alloc) {
		t.Fatal("Unexpected genesis allocation")
	}
	for addr, account := range reload {
		want, ok := (*alloc)[addr]
		if !ok {
			t.Fatal("Account is not found")
		}
		if !reflect.DeepEqual(want, account) {
			t.Fatal("Unexpected account")
		}
	}
}

func newDbConfig(scheme string) *trie.Config {
	if scheme == rawdb.HashScheme {
		return trie.HashDefaults
	}
	return &trie.Config{PathDB: pathdb.Defaults}
}
