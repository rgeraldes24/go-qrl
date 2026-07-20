// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.

package qrltest

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/p2p"
	"github.com/theQRL/go-qrl/qrl/protocols/qrl"
	"github.com/theQRL/go-qrl/qrl/protocols/snap"
	"github.com/theQRL/go-qrl/rlp"
)

func TestVM64FixturePreflight(t *testing.T) {
	fullPath := filepath.Join("testdata", "chain.rlp")
	genesisPath := filepath.Join("testdata", "genesis.json")
	full, err := loadChain(fullPath, genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	target, err := selectTargetChain(full, fullPath, genesisPath)
	if err != nil {
		t.Fatal(err)
	}
	if target.Len() >= full.Len() {
		t.Fatalf("target fixture has %d blocks and full fixture has %d; no future test data", target.Len(), full.Len())
	}
	transactions, err := makeFixtureTransactions(target, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture target blocks=%d full blocks=%d total transactions=%d generated transactions=%d", target.Len(), full.Len(), len(full.futureTransactions(0, 1_000_000)), len(transactions))
	if len(transactions) != 2 {
		t.Fatalf("generated %d fixture transactions, want 2", len(transactions))
	}
	for i, transaction := range transactions {
		if err := validateVM64Transaction(full.Config(), transaction); err != nil {
			t.Fatalf("future transaction %d: %v", i, err)
		}
	}
	t.Logf("fixture target head=%d full head=%d", target.Head().NumberU64(), full.Head().NumberU64())
	for _, index := range []int{0, 1, target.Len() - 1, full.Len() - 1} {
		block := full.blocks[index]
		t.Logf("block=%d hash=%x parent=%x root=%x time=%d gasLimit=%d baseFee=%v", block.NumberU64(), block.Hash(), block.ParentHash(), block.Root(), block.Time(), block.GasLimit(), block.BaseFee())
	}
}

func TestGetHeadersUsesSkipPlusOneInBothDirections(t *testing.T) {
	full, err := loadChain(filepath.Join("testdata", "chain.rlp"), filepath.Join("testdata", "genesis.json"))
	if err != nil {
		t.Fatal(err)
	}
	forward := &qrl.GetBlockHeadersPacket{
		GetBlockHeadersRequest: &qrl.GetBlockHeadersRequest{
			Origin: qrl.HashOrNumber{Number: 2}, Amount: 3, Skip: 1,
		},
	}
	headers, err := full.GetHeaders(forward)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []uint64{2, 4, 6} {
		if got := headers[i].Number.Uint64(); got != want {
			t.Fatalf("forward header %d number %d, want %d", i, got, want)
		}
	}
	reverse := &qrl.GetBlockHeadersPacket{
		GetBlockHeadersRequest: &qrl.GetBlockHeadersRequest{
			Origin: qrl.HashOrNumber{Number: 6}, Amount: 4, Skip: 1, Reverse: true,
		},
	}
	headers, err = full.GetHeaders(reverse)
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range []uint64{6, 4, 2, 0} {
		if got := headers[i].Number.Uint64(); got != want {
			t.Fatalf("reverse header %d number %d, want %d", i, got, want)
		}
	}
}

func TestCurrentProtocolOffsetsAndNegotiation(t *testing.T) {
	if qrlProtoLen != 17 {
		t.Fatalf("qrl protocol length %d, want 17", qrlProtoLen)
	}
	if snapProtoLen != 8 {
		t.Fatalf("snap protocol length %d, want 8", snapProtoLen)
	}
	if got, want := protoOffset(snapProto), uint64(33); got != want {
		t.Fatalf("snap offset %d, want %d", got, want)
	}
	conn := &Conn{requireSnap: true}
	conn.negotiate([]p2p.Cap{
		{Name: "eth", Version: 68},
		{Name: qrl.ProtocolName, Version: qrl.QRL1},
		{Name: snap.ProtocolName, Version: snap.SNAP1},
	})
	if conn.qrlVersion != qrl.QRL1 || conn.snapVersion != snap.SNAP1 {
		t.Fatalf("negotiated qrl/%d snap/%d", conn.qrlVersion, conn.snapVersion)
	}
	withoutQRL := &Conn{requireSnap: true}
	withoutQRL.negotiate([]p2p.Cap{{Name: "eth", Version: 68}, {Name: snap.ProtocolName, Version: snap.SNAP1}})
	if withoutQRL.qrlVersion != 0 {
		t.Fatalf("negotiated qrl/%d without qrl capability", withoutQRL.qrlVersion)
	}
}

func TestSnapStoragePacketPreservesFullVM64Value(t *testing.T) {
	raw := bytes.Repeat([]byte{0xa5}, common.StorageValue64Length)
	encoded, err := rlp.EncodeToBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	want := &snap.StorageRangesPacket{
		ID: 7,
		Slots: [][]*snap.StorageData{{{
			Hash: common.Hash{0: 1, common.HashLength - 1: 2},
			Body: encoded,
		}}},
	}
	wire, err := rlp.EncodeToBytes(want)
	if err != nil {
		t.Fatal(err)
	}
	var got snap.StorageRangesPacket
	if err := rlp.DecodeBytes(wire, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Slots) != 1 || len(got.Slots[0]) != 1 {
		t.Fatalf("unexpected decoded slot shape %#v", got.Slots)
	}
	var decoded []byte
	if err := rlp.DecodeBytes(got.Slots[0][0].Body, &decoded); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded, raw) {
		t.Fatalf("decoded storage value has %d bytes and differs from VM64 input", len(decoded))
	}
}

func TestIncrementHash(t *testing.T) {
	start := common.Hash{common.HashLength - 2: 0x12, common.HashLength - 1: 0xff}
	next, ok := incrementHash(start)
	if !ok || next[common.HashLength-2] != 0x13 || next[common.HashLength-1] != 0 {
		t.Fatalf("increment %x = %x, ok=%v", start, next, ok)
	}
	if _, ok := incrementHash(common.MaxHash); ok {
		t.Fatal("maximum hash increment unexpectedly succeeded")
	}
}
