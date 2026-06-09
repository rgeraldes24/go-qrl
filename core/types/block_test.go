// Copyright 2014 The go-ethereum Authors
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

package types

import (
	"bytes"
	gomath "math"
	"math/big"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/crypto/pqcrypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/internal/blocktest"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/rlp"
)

func TestBlockEncoding(t *testing.T) {
	coinbase := common.BytesToAddress([]byte{0x01, 0})
	to := common.BytesToAddress([]byte{0x9a, 0})
	header := &Header{
		ParentHash: common.HexToHash("0x01"),
		Coinbase:   coinbase,
		Root:       common.HexToHash("0xe0d841b3f9f2ca592e4075480aeb7d434a94dc45270d855afe432764d83eaec9"),
		Number:     big.NewInt(46147),
		GasLimit:   4712388,
		GasUsed:    params.TxGas,
		Time:       9150,
		Extra:      []byte("simple block"),
		BaseFee:    big.NewInt(1000000000),
	}
	block := NewBlock(header, &Body{
		Transactions: Transactions{newBlockEncodingTx(9, to, nil)},
		Withdrawals:  []*Withdrawal{},
	}, nil, blocktest.NewHasher())

	if block.GasLimit() != 4712388 {
		t.Fatalf("GasLimit mismatch: got %d, want %d", block.GasLimit(), 4712388)
	}
	if block.GasUsed() != params.TxGas {
		t.Fatalf("GasUsed mismatch: got %d, want %d", block.GasUsed(), params.TxGas)
	}
	if block.Coinbase() != coinbase {
		t.Fatalf("Coinbase mismatch: got %v, want %v", block.Coinbase(), coinbase)
	}
	if block.Root() != header.Root {
		t.Fatalf("Root mismatch: got %v, want %v", block.Root(), header.Root)
	}
	if block.Time() != 9150 {
		t.Fatalf("Time mismatch: got %d, want %d", block.Time(), 9150)
	}
	if len(block.Transactions()) != 1 {
		t.Fatalf("len(Transactions) mismatch: got %d, want 1", len(block.Transactions()))
	}
	assertBlockRoundTrip(t, block)
}

func TestEIP1559BlockEncoding(t *testing.T) {
	coinbase := common.BytesToAddress([]byte{0x02, 0})
	to := common.BytesToAddress([]byte{0x9a, 0x01})
	header := &Header{
		ParentHash: common.HexToHash("0x02"),
		Coinbase:   coinbase,
		Root:       common.HexToHash("0x1234"),
		Number:     big.NewInt(46147),
		GasLimit:   8000000,
		GasUsed:    params.TxGas * 2,
		Time:       9160,
		Extra:      []byte("eip1559 block"),
		BaseFee:    big.NewInt(269797233),
	}
	accessList := AccessList{
		{
			Address:     to,
			StorageKeys: []common.Hash{common.HexToHash("0x01")},
		},
	}
	block := NewBlock(header, &Body{
		Transactions: Transactions{
			newBlockEncodingTx(9, to, nil),
			newBlockEncodingTx(19, to, accessList),
		},
		Withdrawals: []*Withdrawal{},
	}, nil, blocktest.NewHasher())

	if block.BaseFee().Cmp(header.BaseFee) != 0 {
		t.Fatalf("BaseFee mismatch: got %v, want %v", block.BaseFee(), header.BaseFee)
	}
	if len(block.Transactions()) != 2 {
		t.Fatalf("len(Transactions) mismatch: got %d, want 2", len(block.Transactions()))
	}
	if block.Transactions()[1].Type() != DynamicFeeTxType {
		t.Fatalf("Transactions[1].Type mismatch: got %d, want %d", block.Transactions()[1].Type(), DynamicFeeTxType)
	}
	assertBlockRoundTrip(t, block)
}

func TestEIP2718BlockEncoding(t *testing.T) {
	to := common.BytesToAddress([]byte{0x9a, 0x02})
	header := &Header{
		ParentHash: common.HexToHash("0x03"),
		Coinbase:   common.BytesToAddress([]byte{0x03, 0}),
		Root:       common.HexToHash("0x5678"),
		Number:     big.NewInt(46148),
		GasLimit:   9000000,
		GasUsed:    params.TxGas * 2,
		Time:       9170,
		Extra:      []byte("typed block"),
		BaseFee:    big.NewInt(269797233),
	}
	withdrawals := []*Withdrawal{
		{Index: 1, Validator: 2, Address: to, Amount: 3},
	}
	block := NewBlock(header, &Body{
		Transactions: Transactions{
			newBlockEncodingTx(29, to, nil),
			newBlockEncodingTx(39, to, AccessList{{Address: to, StorageKeys: []common.Hash{common.HexToHash("0x02")}}}),
		},
		Withdrawals: withdrawals,
	}, nil, blocktest.NewHasher())

	if len(block.Transactions()) != 2 {
		t.Fatalf("len(Transactions) mismatch: got %d, want 2", len(block.Transactions()))
	}
	if len(block.Withdrawals()) != 1 {
		t.Fatalf("len(Withdrawals) mismatch: got %d, want 1", len(block.Withdrawals()))
	}
	if block.Withdrawals()[0].Address != to {
		t.Fatalf("Withdrawal address mismatch: got %v, want %v", block.Withdrawals()[0].Address, to)
	}
	assertBlockRoundTrip(t, block)
}

func newBlockEncodingTx(nonce uint64, to common.Address, accessList AccessList) *Transaction {
	return NewTx(&DynamicFeeTx{
		ChainID:     big.NewInt(1337),
		Nonce:       nonce,
		To:          &to,
		Value:       big.NewInt(1),
		Gas:         params.TxGas,
		GasFeeCap:   big.NewInt(875000000),
		GasTipCap:   big.NewInt(params.Shor / 1000),
		AccessList:  accessList,
		Descriptor:  [3]byte{1, 0, 0},
		ExtraParams: []byte{},
		Signature:   bytes.Repeat([]byte{byte(nonce)}, pqcrypto.MLDSA87SignatureLength),
		PublicKey:   bytes.Repeat([]byte{byte(nonce + 1)}, pqcrypto.MLDSA87PublicKeyLength),
	})
}

func assertBlockRoundTrip(t *testing.T, block *Block) {
	t.Helper()
	encoded, err := rlp.EncodeToBytes(block)
	if err != nil {
		t.Fatalf("encode error: %v", err)
	}
	var decoded Block
	if err := rlp.DecodeBytes(encoded, &decoded); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	encodedAgain, err := rlp.EncodeToBytes(&decoded)
	if err != nil {
		t.Fatalf("re-encode error: %v", err)
	}
	if !bytes.Equal(encoded, encodedAgain) {
		t.Fatalf("encoded block mismatch:\ngot:  %x\nwant: %x", encodedAgain, encoded)
	}
	if uint64(len(encoded)) != block.Size() {
		t.Fatalf("Size mismatch: got %d, want %d", block.Size(), len(encoded))
	}
}

var benchBuffer = bytes.NewBuffer(make([]byte, 0, 32000))

func BenchmarkEncodeBlock(b *testing.B) {
	block := makeBenchBlock()

	for b.Loop() {
		benchBuffer.Reset()
		if err := rlp.Encode(benchBuffer, block); err != nil {
			b.Fatal(err)
		}
	}
}

func makeBenchBlock() *Block {
	var (
		w, _     = wallet.Generate(wallet.ML_DSA_87)
		txs      = make([]*Transaction, 70)
		receipts = make([]*Receipt, len(txs))
		signer   = LatestSigner(params.TestChainConfig)
	)
	header := &Header{
		Number:   math.BigPow(2, 9),
		GasLimit: 12345678,
		GasUsed:  1476322,
		Time:     9876543,
		Extra:    []byte("coolest block on chain"),
	}
	for i := range txs {
		amount := math.BigPow(2, int64(i))
		gasFeeCap := big.NewInt(300000)
		data := make([]byte, 100)
		tx := NewTx(&DynamicFeeTx{
			Nonce:     uint64(i),
			To:        &common.Address{},
			Value:     amount,
			Gas:       123457,
			GasFeeCap: gasFeeCap,
			Data:      data,
		})
		signedTx, err := SignTx(tx, signer, w)
		if err != nil {
			panic(err)
		}
		txs[i] = signedTx
		receipts[i] = &Receipt{
			Type:              DynamicFeeTxType,
			PostState:         common.CopyBytes(make([]byte, 32)),
			CumulativeGasUsed: tx.Gas(),
			Status:            ReceiptStatusSuccessful,
		}
	}

	return NewBlock(header, &Body{Transactions: txs}, receipts, blocktest.NewHasher())
}

func TestRlpDecodeParentHash(t *testing.T) {
	// A minimum one
	want := common.HexToHash("0x112233445566778899001122334455667788990011223344556677889900aabb")
	if rlpData, err := rlp.EncodeToBytes(&Header{ParentHash: want}); err != nil {
		t.Fatal(err)
	} else {
		if have := HeaderParentHashFromRLP(rlpData); have != want {
			t.Fatalf("have %x, want %x", have, want)
		}
	}
	// And a maximum one
	// | Number      | dynamic| *big.Int       | 64 bits               |
	// | Extra       | dynamic| []byte         | 65+32 byte (clique)   |
	// | BaseFee     | dynamic| *big.Int       | 64 bits               |
	if rlpData, err := rlp.EncodeToBytes(&Header{
		ParentHash: want,
		Number:     new(big.Int).SetUint64(gomath.MaxUint64),
		Extra:      make([]byte, 65+32),
		BaseFee:    new(big.Int).SetUint64(gomath.MaxUint64),
	}); err != nil {
		t.Fatal(err)
	} else {
		if have := HeaderParentHashFromRLP(rlpData); have != want {
			t.Fatalf("have %x, want %x", have, want)
		}
	}
	// Also test a very very large header.
	{
		// The rlp-encoding of the header belowCauses _total_ length of 65540,
		// which is the first to blow the fast-path.
		h := &Header{
			ParentHash: want,
			Extra:      make([]byte, 65041),
		}
		if rlpData, err := rlp.EncodeToBytes(h); err != nil {
			t.Fatal(err)
		} else {
			if have := HeaderParentHashFromRLP(rlpData); have != want {
				t.Fatalf("have %x, want %x", have, want)
			}
		}
	}
	{
		// Test some invalid erroneous stuff
		for i, rlpData := range [][]byte{
			nil,
			common.FromHex("0x"),
			common.FromHex("0x01"),
			common.FromHex("0x3031323334"),
		} {
			if have, want := HeaderParentHashFromRLP(rlpData), (common.Hash{}); have != want {
				t.Fatalf("invalid %d: have %x, want %x", i, have, want)
			}
		}
	}
}

// TestMaxBlockSize computes the maximum possible block size when the block is
// full of simple transfer transactions that consume the entire gas limit.
func TestMaxBlockSize(t *testing.T) {
	gasLimit := params.MaxGasLimit
	numTxs := gasLimit / params.TxGas

	to := common.BytesToAddress(common.Hex2Bytes("9a9070028361f7aabeb3f2f2dc07f82c4a98a02a"))

	txs := make([]*Transaction, numTxs)
	for i := uint64(0); i < numTxs; i++ {
		tx := NewTx(&DynamicFeeTx{
			ChainID:     big.NewInt(1),
			Nonce:       i,
			GasTipCap:   big.NewInt(1),
			GasFeeCap:   big.NewInt(1000000000),
			Gas:         params.TxGas,
			To:          &to,
			Value:       big.NewInt(1),
			Descriptor:  [3]byte{},
			ExtraParams: []byte{},
			Signature:   make([]byte, pqcrypto.MLDSA87SignatureLength),
			PublicKey:   make([]byte, pqcrypto.MLDSA87PublicKeyLength),
		})
		txs[i] = tx
	}

	header := &Header{
		ParentHash: common.Hash{},
		Number:     big.NewInt(1),
		GasLimit:   gasLimit,
		GasUsed:    numTxs * params.TxGas,
		BaseFee:    big.NewInt(1000000000),
		Extra:      make([]byte, params.MaximumExtraDataSize),
	}

	block := NewBlock(header, &Body{Transactions: txs}, nil, blocktest.NewHasher())

	txSize := txs[0].Size()
	blockSize := block.Size()

	t.Logf("Gas limit:          %d", gasLimit)
	t.Logf("Tx gas:             %d", params.TxGas)
	t.Logf("Number of txs:      %d", numTxs)
	t.Logf("Single tx size:     %d bytes", txSize)
	t.Logf("Block size:         %d bytes (%.2f MB)", blockSize, float64(blockSize)/(1024*1024))

	if block.GasLimit() != 20000000 {
		t.Errorf("gas limit mismatch: got %d, want %d", block.GasLimit(), 20000000)
	}
	if block.GasUsed() != 19992000 {
		t.Errorf("gas used mismatch: got %d, want %d", block.GasUsed(), 19992000)
	}
	const expectedBlockSize = uint64(6967855)
	if blockSize != expectedBlockSize {
		t.Errorf("block size mismatch: got %d, want %d", blockSize, expectedBlockSize)
	}
}
