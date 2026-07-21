// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package deposit

import (
	"context"
	"errors"
	"math/big"
	"path/filepath"
	"strings"
	"testing"
	"time"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

type depositSubmitterStub struct {
	lookups []struct {
		tx      *types.Transaction
		pending bool
		err     error
	}
	sendErr       error
	sent          []*types.Transaction
	lookupsCalled int
}

type depositNonceReaderStub struct {
	confirmed uint64
	pending   uint64
	err       error
}

func (stub depositNonceReaderStub) NonceAt(context.Context, common.Address, *big.Int) (uint64, error) {
	return stub.confirmed, stub.err
}

func (stub depositNonceReaderStub) PendingNonceAt(context.Context, common.Address) (uint64, error) {
	return stub.pending, stub.err
}

func (stub *depositSubmitterStub) TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error) {
	stub.lookupsCalled++
	if len(stub.lookups) == 0 {
		return nil, false, errors.New("unexpected lookup")
	}
	result := stub.lookups[0]
	stub.lookups = stub.lookups[1:]
	return result.tx, result.pending, result.err
}

func (stub *depositSubmitterStub) SendTransaction(_ context.Context, tx *types.Transaction) error {
	stub.sent = append(stub.sent, tx)
	return stub.sendErr
}

func preparedDepositFixture(nonce uint64) (*types.Transaction, PreparedTransaction) {
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: big.NewInt(1), Nonce: nonce, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
		Gas: 100_000, Value: big.NewInt(32), Data: []byte{byte(nonce)},
	})
	raw, err := tx.MarshalBinary()
	if err != nil {
		panic(err)
	}
	return tx, PreparedTransaction{Hash: tx.Hash().Hex(), Raw: hexutil.Encode(raw)}
}

func TestTransactionRecorderAdaptsLifecycleCheckpointDurably(t *testing.T) {
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	stageOrder := []string{"deposit"}
	store := lifecycle.Store{
		Path:       filepath.Join(t.TempDir(), "checkpoint.json"),
		StageOrder: stageOrder,
		Now:        func() time.Time { return now },
	}
	state := lifecycle.NewCheckpoint(
		"deposit-recorder-test",
		strings.Repeat("a", 40),
		strings.Repeat("b", 64),
		t.TempDir(),
		strings.Repeat("c", 64),
		lifecycle.EnclaveRef{Name: "deposit-test", UUID: strings.Repeat("d", 32), Owned: true},
		now,
	)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}

	recorder := TransactionRecorderFunc(func(label, hash string) error {
		return state.RecordTransaction(store, label, hash, now.Add(time.Second))
	})
	waited := false
	_, err := recordAndWaitForDeposit(t.Context(), recorder, "deposit-2", "0xfeed", func(context.Context) (*types.Receipt, error) {
		persisted, err := store.Load()
		if err != nil {
			t.Fatalf("load checkpoint during receipt wait: %v", err)
		}
		if got := persisted.Transactions["deposit-2"]; got != "0xfeed" {
			t.Fatalf("persisted transaction = %q, want 0xfeed", got)
		}
		waited = true
		return &types.Receipt{}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !waited {
		t.Fatal("receipt wait was not reached")
	}
}

func TestRecordedDepositPrefixValidation(t *testing.T) {
	t.Parallel()
	hash := common.HexToHash("0x1234").Hex()
	recorded, err := validateRecordedDeposits(map[string]string{"deposit-0": hash, "deposit-1": hash}, 3)
	if err != nil || len(recorded) != 2 || recorded[1].Hex() != hash {
		t.Fatalf("recorded deposits = %v, error = %v", recorded, err)
	}
	if _, err := validateRecordedDeposits(map[string]string{"deposit-1": hash}, 3); err == nil {
		t.Fatal("deposit checkpoint hole was accepted")
	}
	if _, err := validateRecordedDeposits(map[string]string{"deposit-0": "0x1234"}, 3); err == nil {
		t.Fatal("non-canonical deposit hash was accepted")
	}
}

func TestPreparedDepositValidationBindsRawHashAndOrder(t *testing.T) {
	t.Parallel()
	_, first := preparedDepositFixture(1)
	_, second := preparedDepositFixture(2)
	bad := first
	bad.Hash = second.Hash
	if _, err := validatePreparedDeposits(map[string]PreparedTransaction{"deposit-0": bad}, nil, 3); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("raw/hash mismatch error = %v", err)
	}
	if _, err := validatePreparedDeposits(map[string]PreparedTransaction{"deposit-1": second}, nil, 3); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("prepared order error = %v", err)
	}
	recorded := map[int]common.Hash{0: common.HexToHash(first.Hash)}
	if _, err := validatePreparedDeposits(map[string]PreparedTransaction{"deposit-0": second}, recorded, 3); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("submitted/prepared mismatch error = %v", err)
	}
}

func TestPreparedDepositJournalFailurePreventsSubmission(t *testing.T) {
	t.Parallel()
	tx, _ := preparedDepositFixture(3)
	recordErr := errors.New("checkpoint unavailable")
	client := &depositSubmitterStub{}
	err := journalAndSubmitDeposit(t.Context(), client, tx, PreparedTransactionRecorderFunc(func(string, string, string) error {
		return recordErr
	}), "deposit-0")
	if !errors.Is(err, recordErr) {
		t.Fatalf("journalAndSubmitDeposit error = %v, want %v", err, recordErr)
	}
	if len(client.sent) != 0 || len(client.lookups) != 0 {
		t.Fatalf("submission occurred after journal failure: sent=%d lookups=%d", len(client.sent), len(client.lookups))
	}
}

func TestPreparedDepositReconciliationUsesExactTransaction(t *testing.T) {
	t.Parallel()
	tx, prepared := preparedDepositFixture(4)
	tests := []struct {
		name    string
		lookups []struct {
			tx      *types.Transaction
			pending bool
			err     error
		}
		sendErr   error
		wantErr   bool
		wantSends int
	}{
		{name: "found", lookups: []struct {
			tx      *types.Transaction
			pending bool
			err     error
		}{{tx: tx}}},
		{name: "absent", lookups: []struct {
			tx      *types.Transaction
			pending bool
			err     error
		}{{err: qrl.NotFound}}, wantSends: 1},
		{name: "lost response then found", lookups: []struct {
			tx      *types.Transaction
			pending bool
			err     error
		}{{err: qrl.NotFound}, {tx: tx}}, sendErr: errors.New("lost"), wantSends: 1},
		{name: "rejected and absent", lookups: []struct {
			tx      *types.Transaction
			pending bool
			err     error
		}{{err: qrl.NotFound}, {err: qrl.NotFound}}, sendErr: errors.New("rejected"), wantSends: 1, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := &depositSubmitterStub{lookups: test.lookups, sendErr: test.sendErr}
			err := ensurePreparedDepositSubmitted(t.Context(), client, tx)
			if (err != nil) != test.wantErr {
				t.Fatalf("ensure error = %v, wantErr=%t", err, test.wantErr)
			}
			if len(client.sent) != test.wantSends {
				t.Fatalf("sends = %d, want %d", len(client.sent), test.wantSends)
			}
			if len(client.sent) != 0 {
				raw, marshalErr := client.sent[0].MarshalBinary()
				if marshalErr != nil || hexutil.Encode(raw) != prepared.Raw {
					t.Fatalf("rebroadcast bytes changed: raw=%s err=%v want=%s", hexutil.Encode(raw), marshalErr, prepared.Raw)
				}
			}
		})
	}
}

func TestPreparedDepositZeroNonceIntentSurvivesMinedResponseLoss(t *testing.T) {
	t.Parallel()
	tx, _ := preparedDepositFixture(9)
	readers := [2]depositNonceReader{
		depositNonceReaderStub{confirmed: 10, pending: 10},
		depositNonceReaderStub{confirmed: 10, pending: 10},
	}
	got, err := expectedPreparedDepositNonceFromReaders(t.Context(), readers, common.Address{}, 0, 1, nil, tx)
	if err != nil {
		t.Fatal(err)
	}
	if got != tx.Nonce() {
		t.Fatalf("expected nonce = %d, want prepared nonce %d", got, tx.Nonce())
	}
}

func TestPreparedDepositZeroNonceIntentAcceptsOnlyDurablePrefixWindow(t *testing.T) {
	t.Parallel()
	tx, _ := preparedDepositFixture(20)
	tests := []struct {
		name    string
		prefix  int
		first   depositNonceReaderStub
		second  depositNonceReaderStub
		wantErr bool
	}{
		{name: "before submission", prefix: 1, first: depositNonceReaderStub{confirmed: 20, pending: 20}, second: depositNonceReaderStub{confirmed: 20, pending: 20}},
		{name: "pending on one peer", prefix: 1, first: depositNonceReaderStub{confirmed: 20, pending: 21}, second: depositNonceReaderStub{confirmed: 20, pending: 20}},
		{name: "complete recorded prefix", prefix: 3, first: depositNonceReaderStub{confirmed: 23, pending: 23}, second: depositNonceReaderStub{confirmed: 23, pending: 23}},
		{name: "unrelated nonce advance", prefix: 1, first: depositNonceReaderStub{confirmed: 22, pending: 22}, second: depositNonceReaderStub{confirmed: 22, pending: 22}, wantErr: true},
		{name: "pending behind confirmed", prefix: 1, first: depositNonceReaderStub{confirmed: 21, pending: 20}, second: depositNonceReaderStub{confirmed: 21, pending: 21}, wantErr: true},
		{name: "empty durable prefix", prefix: 0, first: depositNonceReaderStub{confirmed: 20, pending: 20}, second: depositNonceReaderStub{confirmed: 20, pending: 20}, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			readers := [2]depositNonceReader{test.first, test.second}
			_, err := expectedPreparedDepositNonceFromReaders(t.Context(), readers, common.Address{}, 0, test.prefix, nil, tx)
			if (err != nil) != test.wantErr {
				t.Fatalf("nonce window error = %v, wantErr=%t", err, test.wantErr)
			}
		})
	}
}

func TestPreparedDepositContinuationUsesSettledBaseNonce(t *testing.T) {
	t.Parallel()
	first, _ := preparedDepositFixture(41)
	third, _ := preparedDepositFixture(43)
	settled := map[int]depositResult{0: {tx: first}}
	got, err := expectedPreparedDepositNonceFromReaders(t.Context(), [2]depositNonceReader{}, common.Address{}, 2, 3, settled, third)
	if err != nil {
		t.Fatal(err)
	}
	if got != 43 {
		t.Fatalf("continuation nonce = %d, want 43", got)
	}
}

func TestPreparedDepositSemanticCorruptionNeverReachesSubmission(t *testing.T) {
	t.Parallel()
	parsed, err := parseDepositABI()
	if err != nil {
		t.Fatal(err)
	}
	contract, err := common.NewAddressFromString(defaultDepositContract)
	if err != nil {
		t.Fatal(err)
	}
	data := depositData{
		publicKey: []byte{1, 2, 3}, withdrawalCredentials: make([]byte, common.AddressLength),
		amount: 32_000_000_000, signature: []byte{4, 5, 6}, root: [32]byte{7},
	}
	w, err := wallet.RestoreFromSeedHex(defaultFundingSeed)
	if err != nil {
		t.Fatal(err)
	}
	from := common.Address(w.GetAddress())
	chainID := big.NewInt(1337)
	nonce := uint64(9)
	calldata, err := parsed.Pack("deposit", data.publicKey, data.withdrawalCredentials, data.signature, data.root)
	if err != nil {
		t.Fatal(err)
	}
	value := new(big.Int).Mul(new(big.Int).SetUint64(data.amount), big.NewInt(params.Shor))
	otherContract := contract
	otherContract[0] ^= 0xff
	otherWallet, err := wallet.RestoreFromSeedHex("010000a7b1a3005d9e110009c48d45deb43f0a0e31846ed2c5aaefb6d4238040ad4c08794ffe65585c13eb6948c2faf6db90c2")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		to      common.Address
		value   *big.Int
		data    []byte
		nonce   uint64
		chainID *big.Int
		wallet  wallet.Wallet
	}{
		{name: "recipient", to: otherContract, value: value, data: calldata, nonce: nonce, chainID: chainID, wallet: w},
		{name: "value", to: contract, value: new(big.Int).Add(value, big.NewInt(1)), data: calldata, nonce: nonce, chainID: chainID, wallet: w},
		{name: "calldata", to: contract, value: value, data: append(append([]byte(nil), calldata...), 0xff), nonce: nonce, chainID: chainID, wallet: w},
		{name: "nonce", to: contract, value: value, data: calldata, nonce: nonce + 1, chainID: chainID, wallet: w},
		{name: "chain", to: contract, value: value, data: calldata, nonce: nonce, chainID: big.NewInt(1338), wallet: w},
		{name: "sender", to: contract, value: value, data: calldata, nonce: nonce, chainID: chainID, wallet: otherWallet},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			unsigned := types.NewTx(&types.DynamicFeeTx{
				ChainID: test.chainID, Nonce: test.nonce, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2), Gas: 500_000,
				To: &test.to, Value: test.value, Data: test.data,
			})
			tx, err := types.SignTx(unsigned, types.LatestSignerForChainID(test.chainID), test.wallet)
			if err != nil {
				t.Fatal(err)
			}
			client := &depositSubmitterStub{}
			if err := validateAndEnsurePreparedDeposit(t.Context(), client, tx, contract, data, parsed, from, nonce, chainID); err == nil {
				t.Fatal("corrupt prepared deposit was accepted")
			}
			if client.lookupsCalled != 0 || len(client.sent) != 0 {
				t.Fatalf("corrupt transaction reached submission path: lookups=%d sends=%d", client.lookupsCalled, len(client.sent))
			}
		})
	}
}
