// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.

package qrltest

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/internal/utesting"
	"github.com/theQRL/go-qrl/node"
	"github.com/theQRL/go-qrl/p2p"
	qrlservice "github.com/theQRL/go-qrl/qrl"
	"github.com/theQRL/go-qrl/qrl/qrlconfig"
)

func TestInProcessWireConformance(t *testing.T) {
	if testing.Short() {
		t.Skip("wire conformance starts an in-process QRL node")
	}
	chainPath := filepath.Join("testdata", "chain.rlp")
	genesisPath := filepath.Join("testdata", "genesis.json")
	full, err := loadChain(chainPath, genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := selectTargetChain(full, chainPath, genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	stack, err := node.New(&node.Config{
		Name:    "devp2p-conformance",
		DataDir: t.TempDir(),
		P2P: p2p.Config{
			ListenAddr:  "127.0.0.1:0",
			NoDiscovery: true,
			NoDial:      true,
			MaxPeers:    20,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer stack.Close()
	config := qrlconfig.Defaults
	config.Genesis = &target.genesis
	config.NetworkId = target.Config().ChainID.Uint64()
	config.StateScheme = rawdb.HashScheme
	config.DatabaseCache = 32
	config.TrieCleanCache = 16
	config.TrieDirtyCache = 16
	config.SnapshotCache = 16
	backend, err := qrlservice.New(stack, &config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := backend.BlockChain().InsertChain(target.blocks[1:]); err != nil {
		t.Fatalf("import half-chain fixture: %v", err)
	}
	if err := stack.Start(); err != nil {
		t.Fatal(err)
	}
	if snapshots := backend.BlockChain().Snapshots(); snapshots == nil {
		t.Fatal("snapshot tree is disabled")
	} else {
		deadline := time.Now().Add(20 * time.Second)
		for snapshots.Snapshot(target.Head().Root()) == nil && time.Now().Before(deadline) {
			time.Sleep(20 * time.Millisecond)
		}
		if snapshots.Snapshot(target.Head().Root()) == nil {
			t.Fatal("head state snapshot was not generated within 20 seconds")
		}
	}
	// The production downloader enables transaction processing when initial sync
	// completes. This isolated node starts directly from the target fixture and
	// has no higher peer to drive that transition, so mark that equivalent state
	// explicitly before exercising transaction ingress and propagation.
	backend.SetSynced()
	suite, err := NewSuite(stack.Server().Self(), chainPath, genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	tests := append(suite.QRLTests(), suite.SnapTests()...)
	for _, test := range tests {
		test := test
		t.Run(test.Name, func(t *testing.T) {
			failed, output := utesting.Run(test)
			if output != "" {
				t.Log(output)
			}
			if failed {
				t.Fail()
			}
		})
	}
}
