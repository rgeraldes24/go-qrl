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
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/rlp"
	walletmldsa87 "github.com/theQRL/go-qrllib/wallet/ml_dsa_87"
)

var (
	testAddr = common.BytesToAddress(common.Hex2Bytes("b94f5374fce5edbc8e2a8697c15331677e6ebf0b"))

	emptyEip2718Tx = NewTx(&DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     3,
		To:        &testAddr,
		Value:     big.NewInt(10),
		Gas:       25000,
		GasFeeCap: big.NewInt(1),
		GasTipCap: big.NewInt(0),
		Data:      common.FromHex("5544"),
	})
)

func TestDecodeEmptyTypedTx(t *testing.T) {
	input := []byte{0x80}
	var tx Transaction
	err := rlp.DecodeBytes(input, &tx)
	if err != errShortTypedTx {
		t.Fatal("wrong error:", err)
	}
}

func TestEIP2718TransactionSigHash(t *testing.T) {
	extraParams := []byte{}
	s := NewZondSigner(big.NewInt(1))
	desc, err := walletmldsa87.NewMLDSA87Descriptor()
	if err != nil {
		t.Fatal(err)
	}
	descBytes := desc.ToDescriptor().ToBytes()
	emptyHash := s.Hash(emptyEip2718Tx, descBytes, extraParams)
	if emptyHash == (common.Hash{}) {
		t.Fatal("empty EIP-2718 transaction produced zero sig hash")
	}

	testWallet, _ := defaultTestWallet()
	signedTx, err := SignTx(emptyEip2718Tx, s, testWallet)
	if err != nil {
		t.Fatal(err)
	}
	signedHash := s.Hash(signedTx, signedTx.Descriptor(), signedTx.ExtraParams())
	if signedHash != emptyHash {
		t.Fatalf("signed EIP-2718 transaction hash mismatch, got %x want %x", signedHash, emptyHash)
	}
}

// This test checks signature operations on dynamic fee transactions.
func TestEIP2930Signer(t *testing.T) {
	var (
		testWallet, _ = wallet.RestoreFromSeedHex("010000010000b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f29100000000000000000000000000")
		keyAddr       = testWallet.GetAddress()
		signer1       = NewZondSigner(big.NewInt(1))
		signer2       = NewZondSigner(big.NewInt(2))
		tx0           = NewTx(&DynamicFeeTx{Nonce: 1})
		tx1           = NewTx(&DynamicFeeTx{ChainID: big.NewInt(1), Nonce: 1})
		tx2, _        = SignNewTx(testWallet, signer2, &DynamicFeeTx{ChainID: big.NewInt(2), Nonce: 1})
		to            = common.BytesToAddress(common.Hex2Bytes("cccccccccccccccccccccccccccccccccccccccc"))
		tx3, _        = SignNewTx(testWallet, signer1, &DynamicFeeTx{
			Data:      common.Hex2Bytes("00"),
			Value:     big.NewInt(0),
			ChainID:   big.NewInt(1),
			Nonce:     1,
			Gas:       4000000,
			GasFeeCap: big.NewInt(2000),
			GasTipCap: big.NewInt(10),
			To:        &to,
			AccessList: []AccessTuple{
				{
					Address: to,
					StorageKeys: []common.Hash{
						common.HexToHash("0000000000000000000000000000000000000000000000000000000000000000"),
						common.HexToHash("0000000000000000000000000000000000000000000000000000000000000001"),
					},
				},
			},
		})
		to2    = common.BytesToAddress(common.Hex2Bytes("c02aaa39b223fe8d0a0e5c4f27ead9083c756cc2"))
		tx4, _ = SignNewTx(testWallet, signer1, &DynamicFeeTx{
			Data:       common.Hex2Bytes("095ea7b30000000000000000000000001111111254eeb25477b68fb85ed929f73a960582ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"),
			Value:      big.NewInt(0),
			ChainID:    big.NewInt(1),
			Nonce:      47,
			Gas:        53319,
			GasFeeCap:  big.NewInt(14358031378),
			GasTipCap:  big.NewInt(576312105),
			To:         &to2,
			AccessList: []AccessTuple{},
		})
		to3    = common.BytesToAddress(common.Hex2Bytes("535b918f3724001fd6fb52fcc6cbc220592990a3"))
		tx5, _ = SignNewTx(testWallet, signer1, &DynamicFeeTx{
			Data:       []byte{},
			Value:      big.NewInt(73360267083380739),
			ChainID:    big.NewInt(1),
			Nonce:      132949,
			Gas:        30000,
			GasFeeCap:  big.NewInt(14237787676),
			GasTipCap:  big.NewInt(0),
			To:         &to3,
			AccessList: []AccessTuple{},
		})
	)

	tests := []struct {
		tx            *Transaction
		signer        Signer
		wantSenderErr error
		wantSignErr   error
	}{
		{
			tx:            tx0,
			signer:        signer1,
			wantSenderErr: ErrInvalidChainId,
		},
		{
			// This checks what happens when trying to sign an unsigned tx for the wrong chain.
			tx:            tx1,
			signer:        signer2,
			wantSenderErr: ErrInvalidChainId,
			wantSignErr:   ErrInvalidChainId,
		},
		{
			// This checks what happens when trying to re-sign a signed tx for the wrong chain.
			tx:            tx2,
			signer:        signer1,
			wantSenderErr: ErrInvalidChainId,
			wantSignErr:   ErrInvalidChainId,
		},
		{
			tx:     tx3,
			signer: signer1,
		},
		{
			tx:     tx4,
			signer: signer1,
		},
		{
			tx:     tx5,
			signer: signer1,
		},
	}

	for i, test := range tests {
		sender, err := Sender(test.signer, test.tx)
		if !errors.Is(err, test.wantSenderErr) {
			t.Errorf("test %d: wrong Sender error %q", i, err)
		}
		if err == nil && sender != keyAddr {
			t.Errorf("test %d: wrong sender address %x", i, sender)
		}
		signedTx, err := SignTx(test.tx, test.signer, testWallet)
		if !errors.Is(err, test.wantSignErr) {
			t.Fatalf("test %d: wrong SignTx error %q", i, err)
		}
		if signedTx != nil {
			sender, err := Sender(test.signer, signedTx)
			if err != nil {
				t.Fatalf("test %d: signed tx sender failed: %v", i, err)
			}
			if sender != keyAddr {
				t.Errorf("test %d: wrong signed tx sender address %x", i, sender)
			}
		}
	}
}

func TestEIP2718TransactionEncode(t *testing.T) {
	signedTx := signedEip2718Tx(t)
	binary, err := signedTx.MarshalBinary()
	if err != nil {
		t.Fatalf("binary encode error: %v", err)
	}
	if binary[0] != DynamicFeeTxType {
		t.Fatalf("wrong typed transaction prefix: got %x", binary[0])
	}

	haveRLP, err := rlp.EncodeToBytes(signedTx)
	if err != nil {
		t.Fatalf("rlp encode error: %v", err)
	}
	wantRLP, err := rlp.EncodeToBytes(binary)
	if err != nil {
		t.Fatalf("binary rlp encode error: %v", err)
	}
	if !reflect.DeepEqual(haveRLP, wantRLP) {
		t.Fatalf("encoded RLP mismatch, got %x want %x", haveRLP, wantRLP)
	}

	var decodedRLP Transaction
	if err := rlp.DecodeBytes(haveRLP, &decodedRLP); err != nil {
		t.Fatalf("rlp decode error: %v", err)
	}
	if err := assertEqual(&decodedRLP, signedTx); err != nil {
		t.Fatal(err)
	}

	var decodedBinary Transaction
	if err := decodedBinary.UnmarshalBinary(binary); err != nil {
		t.Fatalf("binary decode error: %v", err)
	}
	if err := assertEqual(&decodedBinary, signedTx); err != nil {
		t.Fatal(err)
	}
}

func signedEip2718Tx(t *testing.T) *Transaction {
	t.Helper()
	testWallet, _ := defaultTestWallet()
	tx, err := SignTx(emptyEip2718Tx, NewZondSigner(big.NewInt(1)), testWallet)
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func defaultTestWallet() (wallet.Wallet, common.Address) {
	testWallet, _ := wallet.RestoreFromSeedHex("010000a7b1a3005d9e110009c48d45deb43f0a0e31846ed2c5aaefb6d4238040ad4c08794ffe65585c13eb6948c2faf6db90c2")
	return testWallet, testWallet.GetAddress()
}

func TestRecipientEmpty(t *testing.T) {
	testWallet, addr := defaultTestWallet()
	tx, err := SignNewTx(testWallet, ZondSigner{ChainId: big.NewInt(1)}, &DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     1,
		Value:     big.NewInt(10),
		Gas:       25000,
		GasFeeCap: big.NewInt(1),
		GasTipCap: big.NewInt(0),
		Data:      common.FromHex("5544"),
	})
	if err != nil {
		t.Fatal(err)
	}

	from, err := Sender(ZondSigner{ChainId: big.NewInt(1)}, tx)
	if err != nil {
		t.Fatal(err)
	}
	if addr != from {
		t.Fatal("derived address doesn't match")
	}
	if tx.To() != nil {
		t.Fatal("contract-creation transaction unexpectedly has recipient")
	}
}

func TestRecipientNormal(t *testing.T) {
	testWallet, addr := defaultTestWallet()
	recipient := common.BytesToAddress(common.Hex2Bytes("095e7baea6a6c7c4c2dfeb977efac326af552d87"))
	tx, err := SignNewTx(testWallet, ZondSigner{ChainId: big.NewInt(1)}, &DynamicFeeTx{
		ChainID:   big.NewInt(1),
		Nonce:     1,
		To:        &recipient,
		Value:     big.NewInt(10),
		Gas:       25000,
		GasFeeCap: big.NewInt(1),
		GasTipCap: big.NewInt(0),
		Data:      common.FromHex("5544"),
	})
	if err != nil {
		t.Fatal(err)
	}

	from, err := Sender(ZondSigner{ChainId: big.NewInt(1)}, tx)
	if err != nil {
		t.Fatal(err)
	}
	if addr != from {
		t.Fatal("derived address doesn't match")
	}
	if tx.To() == nil || *tx.To() != recipient {
		t.Fatal("recipient address doesn't match")
	}
}

// TestTransactionCoding tests serializing/de-serializing to/from rlp and JSON.
func TestTransactionCoding(t *testing.T) {
	testWallet, err := wallet.Generate(wallet.ML_DSA_87)
	if err != nil {
		t.Fatalf("could not generate wallet: %v", err)
	}
	var (
		signer    = NewZondSigner(common.Big1)
		addr      = common.BytesToAddress([]byte{0x01, 0})
		recipient = common.BytesToAddress(common.Hex2Bytes("095e7baea6a6c7c4c2dfeb977efac326af552d87"))
		accesses  = AccessList{{Address: addr, StorageKeys: []common.Hash{{0}}}}
	)
	for i := range uint64(500) {
		var txdata TxData
		switch i % 5 {
		case 0:
			// Dynamic fee tx.
			txdata = &DynamicFeeTx{
				Nonce:     i,
				To:        &recipient,
				Gas:       1,
				GasFeeCap: big.NewInt(2),
				GasTipCap: big.NewInt(0),
				Data:      []byte("abcdef"),
			}
		case 1:
			// Dynamic fee tx contract creation.
			txdata = &DynamicFeeTx{
				Nonce:     i,
				Gas:       1,
				GasFeeCap: big.NewInt(2),
				GasTipCap: big.NewInt(0),
				Data:      []byte("abcdef"),
			}
		case 2:
			// Tx with non-zero access list.
			txdata = &DynamicFeeTx{
				ChainID:    big.NewInt(1),
				Nonce:      i,
				To:         &recipient,
				Gas:        123457,
				GasFeeCap:  big.NewInt(2),
				GasTipCap:  big.NewInt(0),
				AccessList: accesses,
				Data:       []byte("abcdef"),
			}
		case 3:
			// Tx with empty access list.
			txdata = &DynamicFeeTx{
				ChainID:   big.NewInt(1),
				Nonce:     i,
				To:        &recipient,
				Gas:       123457,
				GasFeeCap: big.NewInt(2),
				GasTipCap: big.NewInt(0),
				Data:      []byte("abcdef"),
			}
		case 4:
			// Contract creation with access list.
			txdata = &DynamicFeeTx{
				ChainID:    big.NewInt(1),
				Nonce:      i,
				Gas:        123457,
				GasFeeCap:  big.NewInt(2),
				GasTipCap:  big.NewInt(0),
				AccessList: accesses,
			}
		}
		tx, err := SignNewTx(testWallet, signer, txdata)
		if err != nil {
			t.Fatalf("could not sign transaction: %v", err)
		}
		// RLP
		parsedTx, err := encodeDecodeBinary(tx)
		if err != nil {
			t.Fatal(err)
		}
		if err := assertEqual(parsedTx, tx); err != nil {
			t.Fatal(err)
		}

		// JSON
		parsedTx, err = encodeDecodeJSON(tx)
		if err != nil {
			t.Fatal(err)
		}
		if err := assertEqual(parsedTx, tx); err != nil {
			t.Fatal(err)
		}
	}
}

func encodeDecodeJSON(tx *Transaction) (*Transaction, error) {
	data, err := json.Marshal(tx)
	if err != nil {
		return nil, fmt.Errorf("json encoding failed: %v", err)
	}
	var parsedTx = &Transaction{}
	if err := json.Unmarshal(data, &parsedTx); err != nil {
		return nil, fmt.Errorf("json decoding failed: %v", err)
	}
	return parsedTx, nil
}

func encodeDecodeBinary(tx *Transaction) (*Transaction, error) {
	data, err := tx.MarshalBinary()
	if err != nil {
		return nil, fmt.Errorf("rlp encoding failed: %v", err)
	}
	var parsedTx = &Transaction{}
	if err := parsedTx.UnmarshalBinary(data); err != nil {
		return nil, fmt.Errorf("rlp decoding failed: %v", err)
	}
	return parsedTx, nil
}

func assertEqual(orig *Transaction, cpy *Transaction) error {
	if want, got := orig.Hash(), cpy.Hash(); want != got {
		return fmt.Errorf("parsed tx differs from original tx, want %v, got %v", want, got)
	}
	if want, got := orig.ChainId(), cpy.ChainId(); want.Cmp(got) != 0 {
		return fmt.Errorf("invalid chain id, want %d, got %d", want, got)
	}
	if orig.AccessList() != nil {
		if !reflect.DeepEqual(orig.AccessList(), cpy.AccessList()) {
			return fmt.Errorf("access list wrong")
		}
	}
	return nil
}

func TestTransactionSizes(t *testing.T) {
	signer := NewZondSigner(big.NewInt(123))
	testWallet, _ := wallet.RestoreFromSeedHex("0x010000b71c71a67e1177ad4e901695e1b4b9ee17ae16c6668d313eac2f96dbcda3f29100000000000000000000000000000000")
	to := common.BytesToAddress([]byte{0x01, 0})
	for i, txdata := range []TxData{
		&DynamicFeeTx{
			ChainID:   big.NewInt(123),
			Nonce:     1,
			GasFeeCap: big.NewInt(500),
			Gas:       1000000,
			To:        &to,
			Value:     big.NewInt(1),
			AccessList: AccessList{
				AccessTuple{
					Address:     to,
					StorageKeys: []common.Hash{common.HexToHash("0x01")},
				}},
		},
		&DynamicFeeTx{
			ChainID:   big.NewInt(123),
			Nonce:     1,
			Gas:       1000000,
			To:        &to,
			Value:     big.NewInt(1),
			GasTipCap: big.NewInt(500),
			GasFeeCap: big.NewInt(500),
		},
	} {
		tx, err := SignNewTx(testWallet, signer, txdata)
		if err != nil {
			t.Fatalf("test %d: %v", i, err)
		}
		bin, _ := tx.MarshalBinary()

		// Check initial calc
		if have, want := int(tx.Size()), len(bin); have != want {
			t.Errorf("test %d: size wrong, have %d want %d", i, have, want)
		}
		// Check cached version too
		if have, want := int(tx.Size()), len(bin); have != want {
			t.Errorf("test %d: (cached) size wrong, have %d want %d", i, have, want)
		}
		// Check unmarshalled version too
		utx := new(Transaction)
		if err := utx.UnmarshalBinary(bin); err != nil {
			t.Fatalf("test %d: failed to unmarshal tx: %v", i, err)
		}
		if have, want := int(utx.Size()), len(bin); have != want {
			t.Errorf("test %d: (unmarshalled) size wrong, have %d want %d", i, have, want)
		}
	}
}
