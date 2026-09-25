// Copyright 2016 The go-ethereum Authors
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

package params

import (
	"fmt"
	"math/big"

	"github.com/theQRL/go-qrl/common"
)

// TODO(now.youtrack.cloud/issue/TGZ-16)
// Genesis hashes to enforce below configs on.
var (
	MainnetGenesisHash = common.HexToHash("0x35cc0617c9aae4d8d67cc380fad8262a25b12687c913dbd170a350c192adf204")
	BetaNetGenesisHash = common.HexToHash("0x7bf03c3943e81064f9b3b2eea52fab3b21a87c9934fa6ed6901ba0114d61df1f")
	TestnetGenesisHash = common.HexToHash("0xf496c30579f6efe724bb180ce2f113a9de8f0a2c9ef583bd12008959892a08dd")
)

// NOTE(rgeraldes24): unused atm
// func newUint64(val uint64) *uint64 { return &val }

var (
	// MainnetChainConfig is the chain parameters to run a node on the main network.
	MainnetChainConfig = &ChainConfig{
		ChainID: big.NewInt(1),
	}
	// TestnetChainConfig contains the chain parameters to run a node on the BetaNet test network.
	TestnetChainConfig = &ChainConfig{
		ChainID: big.NewInt(1337),
	}
	// BetaNetChainConfig contains the chain parameters to run a node on the BetaNet test network.
	BetaNetChainConfig = &ChainConfig{
		ChainID: big.NewInt(32382),
	}

	// AllBeaconProtocolChanges contains every protocol change (QIPs) introduced
	// and accepted by the QRL core developers into the Beacon consensus.
	AllBeaconProtocolChanges = &ChainConfig{
		ChainID: big.NewInt(1337),
	}

	AllDevChainProtocolChanges = &ChainConfig{
		ChainID:   big.NewInt(1337),
		IsDevMode: true,
	}

	// TestChainConfig contains every protocol change (QIPs) introduced
	// and accepted by the QRL core developers for testing proposes.
	TestChainConfig = &ChainConfig{
		ChainID: big.NewInt(1),
	}

	// NonActivatedConfig defines the chain configuration without activating
	// any protocol change (QIPs).
	NonActivatedConfig = &ChainConfig{
		ChainID: big.NewInt(1),
	}
	TestRules = TestChainConfig.Rules(new(big.Int), 0)
)

// NetworkNames are user friendly names to use in the chain spec banner.
var NetworkNames = map[string]string{
	MainnetChainConfig.ChainID.String(): "mainnet",
}

// ChainConfig is the core config which determines the blockchain settings.
//
// ChainConfig is stored in the database on a per block basis. This means
// that any network, identified by its genesis block, can have its own
// set of configuration options.
type ChainConfig struct {
	ChainID *big.Int `json:"chainId"` // chainId identifies the current chain and is used for replay protection

	IsDevMode bool `json:"isDev,omitempty"`
}

// Description returns a human-readable description of ChainConfig.
func (c *ChainConfig) Description() string {
	var banner string

	// Create some basic network config output
	network := NetworkNames[c.ChainID.String()]
	if network == "" {
		network = "unknown"
	}
	banner += fmt.Sprintf("Chain ID:  %v (%s)\n", c.ChainID, network)
	banner += "Consensus: Beacon (proof-of-stake)\n"
	banner += "\n"

	return banner
}

// CheckCompatible checks whether newcfg can replace the stored configuration c.
// The chain ID must match, since every transaction signs it.
func (c *ChainConfig) CheckCompatible(newcfg *ChainConfig) error {
	if !configBlockEqual(c.ChainID, newcfg.ChainID) {
		return fmt.Errorf("mismatching chain ID in database (have %v, want %v)", c.ChainID, newcfg.ChainID)
	}
	return nil
}

// BaseFeeChangeDenominator bounds the amount the base fee can change between blocks.
func (c *ChainConfig) BaseFeeChangeDenominator() uint64 {
	return DefaultBaseFeeChangeDenominator
}

// ElasticityMultiplier bounds the maximum gas limit an EIP-1559 block may have.
func (c *ChainConfig) ElasticityMultiplier() uint64 {
	return DefaultElasticityMultiplier
}

func configBlockEqual(x, y *big.Int) bool {
	if x == nil {
		return y == nil
	}
	if y == nil {
		return x == nil
	}
	return x.Cmp(y) == 0
}

// Rules wraps ChainConfig and is merely syntactic sugar or can be used for functions
// that do not have or require information about the block.
//
// Rules is a one time interface meaning that it shouldn't be used in between transition
// phases.
type Rules struct {
	ChainID *big.Int
}

// Rules ensures c's ChainID is not nil.
func (c *ChainConfig) Rules(num *big.Int, timestamp uint64) Rules {
	chainID := c.ChainID
	if chainID == nil {
		chainID = new(big.Int)
	}
	return Rules{
		ChainID: new(big.Int).Set(chainID),
	}
}
