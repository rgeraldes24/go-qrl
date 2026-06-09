// Copyright 2020 The go-ethereum Authors
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
// You should have received a copy of the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package qrl

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/rlp"
)

// Tests that the custom union field encoder and decoder works correctly.
func TestGetBlockHeadersDataEncodeDecode(t *testing.T) {
	// Create a "random" hash for testing
	var hash common.Hash
	for i := range hash {
		hash[i] = byte(i)
	}
	// Assemble some table driven tests
	tests := []struct {
		packet *GetBlockHeadersRequest
		fail   bool
	}{
		// Providing the origin as either a hash or a number should both work
		{fail: false, packet: &GetBlockHeadersRequest{Origin: HashOrNumber{Number: 314}}},
		{fail: false, packet: &GetBlockHeadersRequest{Origin: HashOrNumber{Hash: hash}}},

		// Providing arbitrary query field should also work
		{fail: false, packet: &GetBlockHeadersRequest{Origin: HashOrNumber{Number: 314}, Amount: 314, Skip: 1, Reverse: true}},
		{fail: false, packet: &GetBlockHeadersRequest{Origin: HashOrNumber{Hash: hash}, Amount: 314, Skip: 1, Reverse: true}},

		// Providing both the origin hash and origin number must fail
		{fail: true, packet: &GetBlockHeadersRequest{Origin: HashOrNumber{Hash: hash, Number: 314}}},
	}
	// Iterate over each of the tests and try to encode and then decode
	for i, tt := range tests {
		bytes, err := rlp.EncodeToBytes(tt.packet)
		if err != nil && !tt.fail {
			t.Fatalf("test %d: failed to encode packet: %v", i, err)
		} else if err == nil && tt.fail {
			t.Fatalf("test %d: encode should have failed", i)
		}
		if !tt.fail {
			packet := new(GetBlockHeadersRequest)
			if err := rlp.DecodeBytes(bytes, packet); err != nil {
				t.Fatalf("test %d: failed to decode packet: %v", i, err)
			}
			if packet.Origin.Hash != tt.packet.Origin.Hash || packet.Origin.Number != tt.packet.Origin.Number || packet.Amount != tt.packet.Amount ||
				packet.Skip != tt.packet.Skip || packet.Reverse != tt.packet.Reverse {
				t.Fatalf("test %d: encode decode mismatch: have %+v, want %+v", i, packet, tt.packet)
			}
		}
	}
}

// TestEmptyMessages tests encoding of empty messages.
func TestEmptyMessages(t *testing.T) {
	// All empty messages encodes to the same format
	want := common.FromHex("c4820457c0")

	for i, msg := range []any{
		// Headers
		GetBlockHeadersPacket{1111, nil},
		BlockHeadersPacket{1111, nil},
		// Bodies
		GetBlockBodiesPacket{1111, nil},
		BlockBodiesPacket{1111, nil},
		BlockBodiesRLPPacket{1111, nil},
		// Receipts
		GetReceiptsPacket{1111, nil},
		ReceiptsPacket{1111, nil},
		// Transactions
		GetPooledTransactionsPacket{1111, nil},
		PooledTransactionsPacket{1111, nil},
		PooledTransactionsRLPPacket{1111, nil},

		// Headers
		BlockHeadersPacket{1111, BlockHeadersRequest([]*types.Header{})},
		// Bodies
		GetBlockBodiesPacket{1111, GetBlockBodiesRequest([]common.Hash{})},
		BlockBodiesPacket{1111, BlockBodiesResponse([]*BlockBody{})},
		BlockBodiesRLPPacket{1111, BlockBodiesRLPResponse([]rlp.RawValue{})},
		// Receipts
		GetReceiptsPacket{1111, GetReceiptsRequest([]common.Hash{})},
		ReceiptsPacket{1111, ReceiptsResponse([][]*types.Receipt{})},
		// Transactions
		GetPooledTransactionsPacket{1111, GetPooledTransactionsRequest([]common.Hash{})},
		PooledTransactionsPacket{1111, PooledTransactionsResponse([]*types.Transaction{})},
		PooledTransactionsRLPPacket{1111, PooledTransactionsRLPResponse([]rlp.RawValue{})},
	} {
		if have, _ := rlp.EncodeToBytes(msg); !bytes.Equal(have, want) {
			t.Errorf("test %d, type %T, have\n\t%x\nwant\n\t%x", i, msg, have, want)
		}
	}
}

// TestMessages tests the encoding of all non-empty message shapes.
func TestMessages(t *testing.T) {
	header := &types.Header{
		Number:   big.NewInt(3333),
		GasLimit: 4444,
		GasUsed:  5555,
		Time:     6666,
		Extra:    []byte{0x77, 0x88},
	}

	txs, txRlps := protocolTestTransactions(t)
	blockBody := &BlockBody{Transactions: txs}
	blockBodyRlp := mustEncodeRLP(t, blockBody)

	hashes := []common.Hash{
		common.HexToHash("deadc0de"),
		common.HexToHash("feedbeef"),
	}
	receipts := []*types.Receipt{
		{
			Type:              types.DynamicFeeTxType,
			Status:            types.ReceiptStatusFailed,
			CumulativeGasUsed: 1,
			Logs: []*types.Log{
				{
					Address: common.BytesToAddress([]byte{0x11, 0}),
					Topics:  []common.Hash{common.HexToHash("dead"), common.HexToHash("beef")},
					Data:    []byte{0x01, 0x00, 0xff},
				},
			},
			TxHash:          hashes[0],
			ContractAddress: common.BytesToAddress([]byte{0x01, 0x11, 0x11}),
			GasUsed:         111111,
		},
	}
	receiptsRlp := mustEncodeRLP(t, receipts)

	for i, tc := range []struct {
		message any
		want    []byte
	}{
		{
			GetBlockHeadersPacket{1111, &GetBlockHeadersRequest{HashOrNumber{hashes[0], 0}, 5, 5, false}},
			common.FromHex("e8820457e4a000000000000000000000000000000000000000000000000000000000deadc0de050580"),
		},
		{
			GetBlockHeadersPacket{1111, &GetBlockHeadersRequest{HashOrNumber{common.Hash{}, 9999}, 5, 5, false}},
			common.FromHex("ca820457c682270f050580"),
		},
		{
			GetBlockBodiesPacket{1111, GetBlockBodiesRequest(hashes)},
			common.FromHex("f847820457f842a000000000000000000000000000000000000000000000000000000000deadc0dea000000000000000000000000000000000000000000000000000000000feedbeef"),
		},
		{
			GetReceiptsPacket{1111, GetReceiptsRequest(hashes)},
			common.FromHex("f847820457f842a000000000000000000000000000000000000000000000000000000000deadc0dea000000000000000000000000000000000000000000000000000000000feedbeef"),
		},
		{
			GetPooledTransactionsPacket{1111, GetPooledTransactionsRequest(hashes)},
			common.FromHex("f847820457f842a000000000000000000000000000000000000000000000000000000000deadc0dea000000000000000000000000000000000000000000000000000000000feedbeef"),
		},
		{
			BlockHeadersPacket{1111, BlockHeadersRequest{header}},
			mustEncodeRLP(t, BlockHeadersPacket{1111, BlockHeadersRequest{header}}),
		},
		{
			BlockBodiesPacket{1111, BlockBodiesResponse([]*BlockBody{blockBody})},
			mustEncodeRLP(t, BlockBodiesRLPPacket{1111, BlockBodiesRLPResponse([]rlp.RawValue{blockBodyRlp})}),
		},
		{
			BlockBodiesRLPPacket{1111, BlockBodiesRLPResponse([]rlp.RawValue{blockBodyRlp})},
			mustEncodeRLP(t, BlockBodiesPacket{1111, BlockBodiesResponse([]*BlockBody{blockBody})}),
		},
		{
			ReceiptsPacket{1111, ReceiptsResponse([][]*types.Receipt{receipts})},
			mustEncodeRLP(t, ReceiptsRLPPacket{1111, ReceiptsRLPResponse([]rlp.RawValue{receiptsRlp})}),
		},
		{
			ReceiptsRLPPacket{1111, ReceiptsRLPResponse([]rlp.RawValue{receiptsRlp})},
			mustEncodeRLP(t, ReceiptsPacket{1111, ReceiptsResponse([][]*types.Receipt{receipts})}),
		},
		{
			PooledTransactionsPacket{1111, PooledTransactionsResponse(txs)},
			mustEncodeRLP(t, PooledTransactionsRLPPacket{1111, PooledTransactionsRLPResponse(txRlps)}),
		},
		{
			PooledTransactionsRLPPacket{1111, PooledTransactionsRLPResponse(txRlps)},
			mustEncodeRLP(t, PooledTransactionsPacket{1111, PooledTransactionsResponse(txs)}),
		},
	} {
		if have, _ := rlp.EncodeToBytes(tc.message); !bytes.Equal(have, tc.want) {
			t.Errorf("test %d, type %T, have\n\t%x\nwant\n\t%x", i, tc.message, have, tc.want)
		}
	}
}

func protocolTestTransactions(t *testing.T) ([]*types.Transaction, []rlp.RawValue) {
	t.Helper()

	txs := []*types.Transaction{
		protocolTestTransaction(0, common.BytesToAddress([]byte{0xb9, 0x4f, 0x00})),
		protocolTestTransaction(1, common.BytesToAddress([]byte{0xa8, 0x80, 0x00})),
	}
	txRlps := make([]rlp.RawValue, len(txs))
	for i, tx := range txs {
		txRlps[i] = mustEncodeRLP(t, tx)
	}
	return txs, txRlps
}

func protocolTestTransaction(nonce uint64, to common.Address) *types.Transaction {
	return types.NewTx(&types.DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     nonce,
		GasTipCap: big.NewInt(1),
		GasFeeCap: big.NewInt(2),
		Gas:       25_000,
		To:        &to,
		Value:     big.NewInt(3),
		Data:      []byte{byte(nonce), 0x55, 0x44},
		AccessList: types.AccessList{
			{
				Address:     common.BytesToAddress([]byte{0xac, byte(nonce), 0x00}),
				StorageKeys: []common.Hash{common.HexToHash("1234"), common.HexToHash("5678")},
			},
		},
		Descriptor:  [3]byte{0x01, 0x02, 0x03},
		ExtraParams: []byte{0x04, byte(nonce)},
	})
}

func mustEncodeRLP(t *testing.T, value any) []byte {
	t.Helper()

	encoded, err := rlp.EncodeToBytes(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
