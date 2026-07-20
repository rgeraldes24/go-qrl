// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.

package qrltest

import (
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core"
	"github.com/theQRL/go-qrl/core/forkid"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/qrl/protocols/qrl"
	"github.com/theQRL/go-qrl/rlp"
)

// Chain is the fixture view used by the wire conformance tests. The blocks
// include genesis at index zero.
type Chain struct {
	genesis core.Genesis
	blocks  []*types.Block
	config  *params.ChainConfig
}

func (c *Chain) Len() int                    { return len(c.blocks) }
func (c *Chain) Head() *types.Block          { return c.blocks[len(c.blocks)-1] }
func (c *Chain) Genesis() *types.Block       { return c.blocks[0] }
func (c *Chain) Config() *params.ChainConfig { return c.config }

func (c *Chain) RootAt(height int) common.Hash {
	if height < 0 || height >= len(c.blocks) {
		return common.Hash{}
	}
	return c.blocks[height].Root()
}

func (c *Chain) ForkID() forkid.ID {
	return forkid.NewID(c.config, c.Genesis(), c.Head().NumberU64(), c.Head().Time())
}

func (c *Chain) Shorten(height int) (*Chain, error) {
	if height < 1 || height > len(c.blocks) {
		return nil, fmt.Errorf("invalid chain height %d (fixture has %d blocks including genesis)", height, len(c.blocks))
	}
	blocks := make([]*types.Block, height)
	copy(blocks, c.blocks[:height])
	config := *c.config
	return &Chain{genesis: c.genesis, blocks: blocks, config: &config}, nil
}

// GetHeaders computes the exact response expected for a header query. It
// mirrors the protocol's boundary behavior by returning a short result when a
// request walks beyond genesis or the fixture head.
func (c *Chain) GetHeaders(req *qrl.GetBlockHeadersPacket) ([]*types.Header, error) {
	if req == nil || req.GetBlockHeadersRequest == nil {
		return nil, errors.New("nil block header request")
	}
	if req.Amount == 0 {
		return nil, errors.New("no block headers requested")
	}
	var origin int = -1
	if req.Origin.Hash != (common.Hash{}) {
		for i, block := range c.blocks {
			if block.Hash() == req.Origin.Hash {
				origin = i
				break
			}
		}
	} else if req.Origin.Number < uint64(len(c.blocks)) {
		origin = int(req.Origin.Number)
	}
	if origin < 0 {
		return nil, fmt.Errorf("header origin not present (number %d, hash %x)", req.Origin.Number, req.Origin.Hash)
	}
	if req.Skip == math.MaxUint64 {
		return nil, errors.New("header skip overflows step")
	}
	step := req.Skip + 1
	amount := req.Amount
	if amount > uint64(len(c.blocks)) {
		amount = uint64(len(c.blocks))
	}
	headers := make([]*types.Header, 0, amount)
	index := uint64(origin)
	for uint64(len(headers)) < amount {
		if index >= uint64(len(c.blocks)) {
			break
		}
		headers = append(headers, c.blocks[index].Header())
		if req.Reverse {
			if index < step {
				break
			}
			index -= step
		} else {
			if index > math.MaxUint64-step {
				break
			}
			index += step
		}
	}
	return headers, nil
}

func loadChain(chainFile, genesisFile string) (*Chain, error) {
	genesis, err := loadGenesis(genesisFile)
	if err != nil {
		return nil, fmt.Errorf("load genesis: %w", err)
	}
	if genesis.Config == nil || genesis.Config.ChainID == nil {
		return nil, errors.New("genesis is missing config.chainId")
	}
	blocks, err := blocksFromFile(chainFile, genesis.ToBlock())
	if err != nil {
		return nil, err
	}
	chain := &Chain{genesis: genesis, blocks: blocks, config: genesis.Config}
	if err := chain.validateVM64Fixture(); err != nil {
		return nil, err
	}
	return chain, nil
}

func loadGenesis(path string) (core.Genesis, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return core.Genesis{}, err
	}
	var genesis core.Genesis
	if err := json.Unmarshal(data, &genesis); err != nil {
		return core.Genesis{}, err
	}
	return genesis, nil
}

func blocksFromFile(path string, genesis *types.Block) ([]*types.Block, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open chain fixture: %w", err)
	}
	defer file.Close()
	var reader io.Reader = file
	if strings.HasSuffix(path, ".gz") {
		gzipReader, err := gzip.NewReader(file)
		if err != nil {
			return nil, fmt.Errorf("open compressed chain fixture: %w", err)
		}
		defer gzipReader.Close()
		reader = gzipReader
	}
	blocks := []*types.Block{genesis}
	stream := rlp.NewStream(reader, 0)
	for index := 0; ; index++ {
		var block types.Block
		err := stream.Decode(&block)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("decode block fixture index %d: %w", index, err)
		}
		wantNumber := uint64(index + 1)
		if block.NumberU64() != wantNumber {
			return nil, fmt.Errorf("block fixture index %d has number %d, want %d", index, block.NumberU64(), wantNumber)
		}
		if block.ParentHash() != blocks[len(blocks)-1].Hash() {
			return nil, fmt.Errorf("block %d parent hash %x does not match fixture predecessor %x", wantNumber, block.ParentHash(), blocks[len(blocks)-1].Hash())
		}
		blocks = append(blocks, &block)
	}
	if len(blocks) < 2 {
		return nil, errors.New("chain fixture contains no non-genesis blocks")
	}
	return blocks, nil
}

// selectTargetChain preserves the documented fixture convention: chain.rlp
// contains future blocks used by propagation tests, while a sibling
// halfchain.rlp is the state imported into the node under test. The prefix is
// verified instead of trusting the filenames.
func selectTargetChain(full *Chain, chainFile, genesisFile string) (*Chain, error) {
	if filepath.Base(chainFile) == "halfchain.rlp" {
		return full, nil
	}
	halfPath := filepath.Join(filepath.Dir(chainFile), "halfchain.rlp")
	if _, err := os.Stat(halfPath); errors.Is(err, os.ErrNotExist) {
		return full, nil
	} else if err != nil {
		return nil, fmt.Errorf("inspect half-chain fixture: %w", err)
	}
	half, err := loadChain(halfPath, genesisFile)
	if err != nil {
		return nil, fmt.Errorf("load half-chain fixture: %w", err)
	}
	if half.Len() >= full.Len() {
		return nil, fmt.Errorf("half-chain has %d blocks, full chain has %d; future blocks are required", half.Len(), full.Len())
	}
	for i := range half.blocks {
		if half.blocks[i].Hash() != full.blocks[i].Hash() {
			return nil, fmt.Errorf("half-chain diverges from full chain at block %d", i)
		}
	}
	return full.Shorten(half.Len())
}

func (c *Chain) validateVM64Fixture() error {
	if common.AddressLength != 64 {
		return fmt.Errorf("VM64 conformance requires 64-byte addresses, build has %d", common.AddressLength)
	}
	if common.HashLength != 32 {
		return fmt.Errorf("VM64 conformance requires 32-byte trie and protocol hashes, build has %d", common.HashLength)
	}
	if common.StorageValue64Length != 64 {
		return fmt.Errorf("VM64 conformance requires 64-byte storage values, build has %d", common.StorageValue64Length)
	}
	if len(c.genesis.Alloc) == 0 {
		return errors.New("genesis fixture has no allocated VM64 account")
	}
	wideAddress := false
	for address := range c.genesis.Alloc {
		if len(address[:]) != common.AddressLength {
			return fmt.Errorf("genesis address has %d bytes, want %d", len(address[:]), common.AddressLength)
		}
		for _, b := range address[:common.AddressLength-common.HashLength] {
			if b != 0 {
				wideAddress = true
				break
			}
		}
	}
	if !wideAddress {
		return errors.New("genesis fixture does not exercise the high 32 bytes of a VM64 address")
	}
	return nil
}

func (c *Chain) futureTransactions(after int, limit int) []*types.Transaction {
	var transactions []*types.Transaction
	for _, block := range c.blocks[after:] {
		for _, transaction := range block.Transactions() {
			transactions = append(transactions, transaction)
			if len(transactions) == limit {
				return transactions
			}
		}
	}
	return transactions
}
