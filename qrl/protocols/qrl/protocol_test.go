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
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package qrl

import (
	"bytes"
	"math/big"
	"reflect"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
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

// TestMessages tests the encoding of all messages
func TestMessages(t *testing.T) {
	// Some basic structs used during testing
	var (
		header       *types.Header
		blockBody    *BlockBody
		blockBodyRlp rlp.RawValue
		txs          []*types.Transaction
		txRlps       []rlp.RawValue
		hashes       []common.Hash
		receipts     []*types.Receipt
		receiptsRlp  rlp.RawValue

		err error
	)
	// Fill WithdrawalsHash with a concrete value so the RLP-generated
	// encoder emits every optional trailing field and the round-trip
	// decoder sees the same number of elements it expects.
	withdrawalsHash := types.EmptyWithdrawalsHash
	header = &types.Header{
		Number:          big.NewInt(3333),
		GasLimit:        4444,
		GasUsed:         5555,
		Time:            6666,
		Extra:           []byte{0x77, 0x88},
		WithdrawalsHash: &withdrawalsHash,
	}
	// Init the transactions by freshly signing them — the original hex
	// blobs were for 20-byte addresses, and we only need the txs to
	// exercise qrl-protocol message encoding below.
	{
		genWallet, werr := wallet.Generate(wallet.ML_DSA_87)
		if werr != nil {
			t.Fatal(werr)
		}
		signer := types.NewZondSigner(big.NewInt(1))
		to := common.BytesToAddress([]byte{0xb9, 0x4f, 0x53, 0x74})
		for _, nonce := range []uint64{3, 5} {
			tx, err := types.SignNewTx(genWallet, signer, &types.DynamicFeeTx{
				ChainID:   big.NewInt(1),
				Nonce:     nonce,
				GasTipCap: big.NewInt(1),
				GasFeeCap: big.NewInt(3),
				Gas:       0x5208,
				To:        &to,
				Value:     big.NewInt(10),
				Data:      []byte{0x55, 0x44},
			})
			if err != nil {
				t.Fatal(err)
			}
			txs = append(txs, tx)
			rlpdata, err := rlp.EncodeToBytes(tx)
			if err != nil {
				t.Fatal(err)
			}
			txRlps = append(txRlps, rlpdata)
		}
	}
	// init the block body data, both object and rlp form
	blockBody = &BlockBody{
		Transactions: txs,
	}
	blockBodyRlp, err = rlp.EncodeToBytes(blockBody)
	if err != nil {
		t.Fatal(err)
	}

	hashes = []common.Hash{
		common.HexToHash("deadc0de"),
		common.HexToHash("feedbeef"),
	}
	// init the receipts
	{
		receipts = []*types.Receipt{
			{
				Type:              types.DynamicFeeTxType,
				Status:            types.ReceiptStatusFailed,
				CumulativeGasUsed: 1,
				Logs: []*types.Log{
					{
						Address: common.BytesToAddress([]byte{0x11}),
						Topics:  []common.LogTopic{common.HexToLogTopic("dead"), common.HexToLogTopic("beef")},
						Data:    []byte{0x01, 0x00, 0xff},
					},
				},
				TxHash:          hashes[0],
				ContractAddress: common.BytesToAddress([]byte{0x01, 0x11, 0x11}),
				GasUsed:         111111,
			},
		}
		rlpData, err := rlp.EncodeToBytes(receipts)
		if err != nil {
			t.Fatal(err)
		}
		receiptsRlp = rlpData
	}

	for i, message := range []any{
		GetBlockHeadersPacket{1111, &GetBlockHeadersRequest{HashOrNumber{hashes[0], 0}, 5, 5, false}},
		GetBlockHeadersPacket{1111, &GetBlockHeadersRequest{HashOrNumber{common.Hash{}, 9999}, 5, 5, false}},
		BlockHeadersPacket{1111, BlockHeadersRequest{header}},
		GetBlockBodiesPacket{1111, GetBlockBodiesRequest(hashes)},
		BlockBodiesPacket{1111, BlockBodiesResponse([]*BlockBody{blockBody})},
		BlockBodiesRLPPacket{1111, BlockBodiesRLPResponse([]rlp.RawValue{blockBodyRlp})},
		GetReceiptsPacket{1111, GetReceiptsRequest(hashes)},
		ReceiptsPacket{1111, ReceiptsResponse([][]*types.Receipt{receipts})},
		ReceiptsRLPPacket{1111, ReceiptsRLPResponse([]rlp.RawValue{receiptsRlp})},
		GetPooledTransactionsPacket{1111, GetPooledTransactionsRequest(hashes)},
		PooledTransactionsPacket{1111, PooledTransactionsResponse(txs)},
		PooledTransactionsRLPPacket{1111, PooledTransactionsRLPResponse(txRlps)},
	} {
		have, err := rlp.EncodeToBytes(message)
		if err != nil {
			t.Errorf("test %d, type %T: encode error: %v", i, message, err)
			continue
		}
		roundTrip := reflect.New(reflect.TypeOf(message)).Interface()
		if err := rlp.DecodeBytes(have, roundTrip); err != nil {
			t.Errorf("test %d, type %T: decode error: %v", i, message, err)
			continue
		}
		got := reflect.ValueOf(roundTrip).Elem().Interface()
		if reEncoded, err := rlp.EncodeToBytes(got); err != nil {
			t.Errorf("test %d, type %T: re-encode error: %v", i, message, err)
		} else if !bytes.Equal(reEncoded, have) {
			t.Errorf("test %d, type %T: round-trip mismatch\n have: %x\n want: %x", i, message, reEncoded, have)
		}
	}
}
