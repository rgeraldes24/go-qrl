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

package core

import (
	"fmt"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/consensus"
	"github.com/theQRL/go-qrl/consensus/beacon"
	"github.com/theQRL/go-qrl/consensus/misc/eip1559"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/trie"
	"golang.org/x/crypto/sha3"
)

// TestStateProcessorErrors tests the output from the 'core' errors
// as defined in core/error.go. These errors are generated when the
// blockchain imports bad blocks, meaning blocks which have valid headers but
// contain invalid transactions
func TestStateProcessorErrors(t *testing.T) {
	var (
		config = &params.ChainConfig{
			ChainID: big.NewInt(1),
		}
		signer     = types.LatestSigner(config)
		wallet1, _ = wallet.RestoreFromSeedHex("0x010000f29f58aff0b00de2844f7e20bd9eeaacc379150043beeb328335817512b29fbb7184da84a092f842b2a06d72a24a5d28")
		wallet2, _ = wallet.RestoreFromSeedHex("0x010000a7b1a3005d9e110009c48d45deb43f0a0e31846ed2c5aaefb6d4238040ad4c08794ffe65585c13eb6948c2faf6db90c2")
	)

	var mkDynamicTx = func(wallet wallet.Wallet, nonce uint64, to common.Address, value *big.Int, gasLimit uint64, gasTipCap, gasFeeCap *big.Int) *types.Transaction {
		tx, _ := types.SignTx(types.NewTx(&types.DynamicFeeTx{
			Nonce:     nonce,
			GasTipCap: gasTipCap,
			GasFeeCap: gasFeeCap,
			Gas:       gasLimit,
			To:        &to,
			Value:     value,
		}), signer, wallet)
		return tx
	}
	var mkDynamicCreationTx = func(nonce uint64, gasLimit uint64, gasTipCap, gasFeeCap *big.Int, data []byte) *types.Transaction {
		tx, _ := types.SignTx(types.NewTx(&types.DynamicFeeTx{
			Nonce:     nonce,
			GasTipCap: gasTipCap,
			GasFeeCap: gasFeeCap,
			Gas:       gasLimit,
			Value:     big.NewInt(0),
			Data:      data,
		}), signer, wallet1)
		return tx
	}
	txError := func(index int, txs []*types.Transaction, msg string) string {
		return fmt.Sprintf("could not apply tx %d [%s]: %s", index, txs[index].Hash(), msg)
	}

	{ // Tests against a 'recent' chain definition
		var (
			address0 = common.Address(wallet1.GetAddress())
			address1 = common.Address(wallet2.GetAddress())
			db       = rawdb.NewMemoryDatabase()
			gspec    = &Genesis{
				Config: config,
				Alloc: GenesisAlloc{
					address0: GenesisAccount{
						Balance: new(big.Int).Mul(big.NewInt(10), big.NewInt(params.Quanta)), // 10 quanta
						Nonce:   0,
					},
					address1: GenesisAccount{
						Balance: new(big.Int).Mul(big.NewInt(10), big.NewInt(params.Quanta)), // 10 quanta
						Nonce:   math.MaxUint64,
					},
				},
			}
			blockchain, _  = NewBlockChain(db, nil, gspec, beacon.New(), vm.Config{}, nil)
			tooBigInitCode = [params.MaxInitCodeSize + 1]byte{}
		)

		defer blockchain.Stop()
		bigNumber := new(big.Int).SetBytes(common.MaxHash.Bytes())
		tooBigNumber := new(big.Int).Set(bigNumber)
		tooBigNumber.Add(tooBigNumber, common.Big1)
		for i, tt := range []struct {
			txs   []*types.Transaction
			errTx int
			want  string
		}{

			{ // ErrNonceTooLow
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas, big.NewInt(0), big.NewInt(params.InitialBaseFee)),
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas, big.NewInt(0), big.NewInt(params.InitialBaseFee)),
				},
				errTx: 1,
				want:  "nonce too low: address " + address0.Hex() + ", tx: 0 state: 1",
			},
			{ // ErrNonceTooHigh
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 100, common.Address{}, big.NewInt(0), params.TxGas, big.NewInt(0), big.NewInt(params.InitialBaseFee)),
				},
				want: "nonce too high: address " + address0.Hex() + ", tx: 100 state: 0",
			},
			{ // ErrNonceMax
				txs: []*types.Transaction{
					mkDynamicTx(wallet2, math.MaxUint64, common.Address{}, big.NewInt(0), params.TxGas, big.NewInt(0), big.NewInt(params.InitialBaseFee)),
				},
				want: "nonce has max value: address " + address1.Hex() + ", nonce: 18446744073709551615",
			},
			{ // ErrGasLimitReached
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), 21000000, big.NewInt(0), big.NewInt(params.InitialBaseFee)),
				},
				want: "gas limit reached",
			},
			{ // ErrInsufficientFundsForTransfer
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, new(big.Int).Mul(big.NewInt(10), big.NewInt(params.Quanta)), params.TxGas, big.NewInt(0), big.NewInt(params.InitialBaseFee)),
				},
				want: "insufficient funds for gas * price + value: address " + address0.Hex() + " have 10000000000000000000 want 10002100000000000000",
			},
			{ // ErrInsufficientFunds
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas, big.NewInt(0), big.NewInt(900000000000000000)),
				},
				want: "insufficient funds for gas * price + value: address " + address0.Hex() + " have 10000000000000000000 want 18900000000000000000000",
			},
			// ErrGasUintOverflow
			// One missing 'core' error is ErrGasUintOverflow: "gas uint64 overflow",
			// In order to trigger that one, we'd have to allocate a _huge_ chunk of data, such that the
			// multiplication len(data) +gas_per_byte overflows uint64. Not testable at the moment
			{ // ErrIntrinsicGas
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas-1000, big.NewInt(0), big.NewInt(params.InitialBaseFee)),
				},
				want: "intrinsic gas too low: have 20000, want 21000",
			},
			{ // ErrGasLimitReached
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas*1000, big.NewInt(0), big.NewInt(params.InitialBaseFee)),
				},
				want: "gas limit reached",
			},
			{ // ErrFeeCapTooLow
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas, big.NewInt(0), big.NewInt(0)),
				},
				want: "max fee per gas less than block base fee: address " + address0.Hex() + ", maxFeePerGas: 0 baseFee: 87500000000",
			},
			{ // ErrTipVeryHigh
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas, tooBigNumber, big.NewInt(1)),
				},
				want: "max priority fee per gas higher than 2^256-1: address " + address0.Hex() + ", maxPriorityFeePerGas bit length: 257",
			},
			{ // ErrFeeCapVeryHigh
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas, big.NewInt(1), tooBigNumber),
				},
				want: "max fee per gas higher than 2^256-1: address " + address0.Hex() + ", maxFeePerGas bit length: 257",
			},
			{ // ErrTipAboveFeeCap
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas, big.NewInt(2), big.NewInt(1)),
				},
				want: "max priority fee per gas higher than max fee per gas: address " + address0.Hex() + ", maxPriorityFeePerGas: 2, maxFeePerGas: 1",
			},
			{ // ErrInsufficientFunds
				// Available balance:          10000000000000000000
				// Effective cost:                   87500000021000
				// FeeCap * gas:               10500000000000000000
				// This test is designed to have the effective cost be covered by the balance, but
				// the extended requirement on FeeCap*gas < balance to fail
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas, big.NewInt(1), big.NewInt(500000000000000)),
				},
				want: "insufficient funds for gas * price + value: address " + address0.Hex() + " have 10000000000000000000 want 10500000000000000000",
			},
			{ // Another ErrInsufficientFunds, this one to ensure that feecap/tip of max u256 is allowed
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas, bigNumber, bigNumber),
				},
				want: "insufficient funds for gas * price + value: address " + address0.Hex() + " have 10000000000000000000 want 2431633873983640103894990685182446064918669677978451844828609264166175722438635000",
			},
			{ // ErrMaxInitCodeSizeExceeded
				txs: []*types.Transaction{
					mkDynamicCreationTx(0, 500000, common.Big0, big.NewInt(params.InitialBaseFee), tooBigInitCode[:]),
				},
				want: "max initcode size exceeded: code size 49153 limit 49152",
			},
			{ // ErrIntrinsicGas: Not enough gas to cover init code
				txs: []*types.Transaction{
					mkDynamicCreationTx(0, 54299, common.Big0, big.NewInt(params.InitialBaseFee), make([]byte, 320)),
				},
				want: "intrinsic gas too low: have 54299, want 54300",
			},
		} {
			block := GenerateBadBlock(gspec.ToBlock(), beacon.New(), tt.txs, gspec.Config)
			_, err := blockchain.InsertChain(types.Blocks{block})
			if err == nil {
				t.Fatal("block imported without errors")
			}
			if have, want := err.Error(), txError(tt.errTx, tt.txs, tt.want); have != want {
				t.Errorf("test %d:\nhave \"%v\"\nwant \"%v\"\n", i, have, want)
			}
		}
	}

	// NOTE(rgeraldes24): test not valid for now
	/*
		// ErrTxTypeNotSupported, For this, we need an older chain
		{
			var (
				db    = rawdb.NewMemoryDatabase()
				gspec = &Genesis{
					Config: &params.ChainConfig{
						ChainID: big.NewInt(1),
					},
					Alloc: GenesisAlloc{
						common.HexToAddress("Q000000000000000000000000000000000000000000000000000000000000000000000000000000000000000071562b71999873db5b286df957af199ec94617f7"): GenesisAccount{
							Balance: big.NewInt(1000000000000000000), // 1 quanta
							Nonce:   0,
						},
					},
				}
				blockchain, _ = NewBlockChain(db, nil, gspec, beacon.NewFaker(), vm.Config{}, nil)
			)
			defer blockchain.Stop()
			for i, tt := range []struct {
				txs   []*types.Transaction
				errTx int
				want  string
			}{
				{ // ErrTxTypeNotSupported
					txs: []*types.Transaction{
						mkDynamicTx(0, common.Address{}, params.TxGas-1000, big.NewInt(0), big.NewInt(0)),
					},
					want: "could not apply tx 0 [0x88626ac0d53cb65308f2416103c62bb1f18b805573d4f96a3640bbbfff13c14f]: transaction type not supported",
				},
			} {
				block := GenerateBadBlock(gspec.ToBlock(), beacon.NewFaker(), tt.txs, gspec.Config)
				_, err := blockchain.InsertChain(types.Blocks{block})
				if err == nil {
					t.Fatal("block imported without errors")
				}
				if have, want := err.Error(), txError(tt.errTx, tt.txs, tt.want); have != want {
					t.Errorf("test %d:\nhave \"%v\"\nwant \"%v\"\n", i, have, want)
				}
			}
		}
	*/

	// ErrSenderNoEOA, for this we need the sender to have contract code
	{
		var (
			address = common.Address(wallet1.GetAddress())
			db      = rawdb.NewMemoryDatabase()
			gspec   = &Genesis{
				Config: config,
				Alloc: GenesisAlloc{
					address: GenesisAccount{
						Balance: new(big.Int).Mul(big.NewInt(10), big.NewInt(params.Quanta)), // 10 quanta
						Nonce:   0,
						Code:    common.FromHex("0xB0B0FACE"),
					},
				},
			}
			blockchain, _ = NewBlockChain(db, nil, gspec, beacon.New(), vm.Config{}, nil)
		)
		defer blockchain.Stop()
		for i, tt := range []struct {
			txs   []*types.Transaction
			errTx int
			want  string
		}{
			{ // ErrSenderNoEOA
				txs: []*types.Transaction{
					mkDynamicTx(wallet1, 0, common.Address{}, big.NewInt(0), params.TxGas-1000, big.NewInt(params.InitialBaseFee), big.NewInt(params.InitialBaseFee)),
				},
				want: "sender not an eoa: address " + address.Hex() + ", codehash: 0x9280914443471259d4570a8661015ae4a5b80186dbc619658fb494bebc3da3d1",
			},
		} {
			block := GenerateBadBlock(gspec.ToBlock(), beacon.New(), tt.txs, gspec.Config)
			_, err := blockchain.InsertChain(types.Blocks{block})
			if err == nil {
				t.Fatal("block imported without errors")
			}
			if have, want := err.Error(), txError(tt.errTx, tt.txs, tt.want); have != want {
				t.Errorf("test %d:\nhave \"%v\"\nwant \"%v\"\n", i, have, want)
			}
		}
	}
}

func TestStateProcessorRejectsNonEmptyExtraParams(t *testing.T) {
	var (
		config = &params.ChainConfig{
			ChainID: big.NewInt(1),
		}
		signer     = types.LatestSigner(config)
		wallet1, _ = wallet.RestoreFromSeedHex("0x010000f29f58aff0b00de2844f7e20bd9eeaacc379150043beeb328335817512b29fbb7184da84a092f842b2a06d72a24a5d28")
		from       = common.Address(wallet1.GetAddress())
		db         = rawdb.NewMemoryDatabase()
		gspec      = &Genesis{
			Config: config,
			Alloc: GenesisAlloc{
				from: GenesisAccount{
					Balance: new(big.Int).Mul(big.NewInt(10), big.NewInt(params.Quanta)),
					Nonce:   0,
				},
			},
		}
		blockchain, _ = NewBlockChain(db, nil, gspec, beacon.New(), vm.Config{}, nil)
	)
	defer blockchain.Stop()

	tx, err := types.SignTx(types.NewTx(&types.DynamicFeeTx{
		Nonce:     0,
		GasTipCap: big.NewInt(0),
		GasFeeCap: big.NewInt(params.InitialBaseFee),
		Gas:       params.TxGas,
		To:        &common.Address{},
		Value:     big.NewInt(0),
	}), signer, wallet1)
	if err != nil {
		t.Fatalf("sign tx: %v", err)
	}
	tampered, err := tx.WithAuthValues(signer, tx.RawSignatureValue(), tx.RawPublicKeyValue(), tx.Descriptor(), []byte{0x01})
	if err != nil {
		t.Fatalf("re-wrap with extra params: %v", err)
	}

	block := GenerateBadBlock(gspec.ToBlock(), beacon.New(), types.Transactions{tampered}, gspec.Config)
	_, err = blockchain.InsertChain(types.Blocks{block})
	if err == nil {
		t.Fatal("block imported without errors")
	}
	if got := err.Error(); !strings.Contains(got, "non-empty extraParams not supported") {
		t.Fatalf("unexpected error: %v", got)
	}
}

// GenerateBadBlock constructs a "block" which contains the transactions. The transactions are not expected to be
// valid, and no proper post-state can be made. But from the perspective of the blockchain, the block is sufficiently
// valid to be considered for import:
// - valid pow (fake), ancestry, difficulty, gaslimit etc
func GenerateBadBlock(parent *types.Block, engine consensus.Engine, txs types.Transactions, config *params.ChainConfig) *types.Block {
	header := &types.Header{
		ParentHash: parent.Hash(),
		Coinbase:   parent.Coinbase(),
		GasLimit:   parent.GasLimit(),
		Number:     new(big.Int).Add(parent.Number(), common.Big1),
		Time:       parent.Time() + 10,
	}
	header.BaseFee = eip1559.CalcBaseFee(config, parent.Header())
	header.WithdrawalsHash = &types.EmptyWithdrawalsHash
	var receipts []*types.Receipt
	// The post-state result doesn't need to be correct (this is a bad block), but we do need something there
	// Preferably something unique. So let's use a combo of blocknum + txhash
	hasher := sha3.NewLegacyKeccak256()
	hasher.Write(header.Number.Bytes())
	var cumulativeGas uint64
	for _, tx := range txs {
		txh := tx.Hash()
		hasher.Write(txh[:])
		receipt := &types.Receipt{
			Type:              types.DynamicFeeTxType,
			PostState:         common.CopyBytes(nil),
			CumulativeGasUsed: cumulativeGas + tx.Gas(),
			Status:            types.ReceiptStatusSuccessful,
		}
		receipt.TxHash = tx.Hash()
		receipt.GasUsed = tx.Gas()
		receipts = append(receipts, receipt)
		cumulativeGas += tx.Gas()
	}
	header.Root = common.BytesToHash(hasher.Sum(nil))

	// Assemble and return the final block for sealing
	body := &types.Body{Transactions: txs, Withdrawals: []*types.Withdrawal{}}
	return types.NewBlock(header, body, receipts, trie.NewStackTrie(nil))
}
