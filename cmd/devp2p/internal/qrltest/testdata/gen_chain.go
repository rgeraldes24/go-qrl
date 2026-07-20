// Command gen_chain re-roots the empty devp2p chain fixtures after genesis
// state changes. Run from any directory with:
//
//	go run ./cmd/devp2p/internal/qrltest/testdata/gen_chain.go
//
// The existing header schedule is retained, but every state root and parent
// hash is recomputed from the current genesis. The command refuses fixtures
// containing transactions or withdrawals because those require state
// execution rather than a mechanical re-root.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"

	"github.com/theQRL/go-qrl/core"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/rlp"
)

func main() {
	_, source, _, _ := runtime.Caller(0)
	dir := filepath.Dir(source)
	genesisPath := flag.String("genesis", filepath.Join(dir, "genesis.json"), "genesis JSON path")
	fullPath := flag.String("full", filepath.Join(dir, "chain.rlp"), "full chain RLP path")
	halfPath := flag.String("half", filepath.Join(dir, "halfchain.rlp"), "half chain RLP path")
	flag.Parse()

	genesis, err := readGenesis(*genesisPath)
	check(err)
	full, err := readBlocks(*fullPath)
	check(err)
	half, err := readBlocks(*halfPath)
	check(err)
	if len(half) >= len(full) {
		check(fmt.Errorf("half chain has %d blocks, full chain has %d", len(half), len(full)))
	}

	regenerated, err := regenerate(genesis.ToBlock(), full)
	check(err)
	check(writeBlocks(*fullPath, regenerated))
	check(writeBlocks(*halfPath, regenerated[:len(half)]))
	fmt.Printf("regenerated %d full and %d half-chain blocks from genesis %s\n", len(regenerated), len(half), genesis.ToBlock().Hash())
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "gen_chain:", err)
		os.Exit(1)
	}
}

func readGenesis(path string) (*core.Genesis, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var genesis core.Genesis
	if err := json.Unmarshal(data, &genesis); err != nil {
		return nil, err
	}
	return &genesis, nil
}

func readBlocks(path string) ([]*types.Block, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	stream := rlp.NewStream(file, 0)
	var blocks []*types.Block
	for {
		var block types.Block
		err := stream.Decode(&block)
		if errors.Is(err, io.EOF) {
			return blocks, nil
		}
		if err != nil {
			return nil, fmt.Errorf("decode block %d from %s: %w", len(blocks)+1, path, err)
		}
		blocks = append(blocks, &block)
	}
}

func regenerate(genesis *types.Block, source []*types.Block) ([]*types.Block, error) {
	blocks := make([]*types.Block, len(source))
	parent := genesis
	for i, block := range source {
		if len(block.Transactions()) != 0 || len(block.Withdrawals()) != 0 {
			return nil, fmt.Errorf("block %d is not empty", block.NumberU64())
		}
		header := block.Header()
		if header.Number.Uint64() != uint64(i+1) {
			return nil, fmt.Errorf("block index %d has number %d", i, header.Number.Uint64())
		}
		header.ParentHash = parent.Hash()
		header.Root = genesis.Root()
		header.GasLimit = genesis.GasLimit()
		emptyWithdrawals := types.EmptyWithdrawalsHash
		header.WithdrawalsHash = &emptyWithdrawals
		blocks[i] = types.NewBlockWithHeader(header).WithBody(types.Body{Withdrawals: types.Withdrawals{}})
		parent = blocks[i]
	}
	return blocks, nil
}

func writeBlocks(path string, blocks []*types.Block) (err error) {
	temp := path + ".tmp"
	file, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer func() {
		file.Close()
		if err != nil {
			os.Remove(temp)
		}
	}()
	for i, block := range blocks {
		if err := rlp.Encode(file, block); err != nil {
			return fmt.Errorf("encode block %d: %w", i+1, err)
		}
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
