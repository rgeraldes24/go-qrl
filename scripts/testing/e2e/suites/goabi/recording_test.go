// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package goabi

import (
	"context"
	"errors"
	"math/big"
	"reflect"
	"strings"
	"testing"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

const (
	goABITestSeed      = "010000f29f58aff0b00de2844f7e20bd9eeaacc379150043beeb328335817512b29fbb7184da84a092f842b2a06d72a24a5d28"
	goABIAlternateSeed = "010000f29f58aff0b00de2844f7e20bd9eeaacc379150043beeb328335817512b29fbb7184da84a092f842b2a06d72a24a5d29"
)

type scriptedTransactionSubmitter struct {
	lookups []struct {
		tx      *types.Transaction
		pending bool
		err     error
	}
	sendErr error
	sent    []*types.Transaction
	looked  []common.Hash
}

func (client *scriptedTransactionSubmitter) TransactionByHash(_ context.Context, hash common.Hash) (*types.Transaction, bool, error) {
	client.looked = append(client.looked, hash)
	if len(client.lookups) == 0 {
		return nil, false, errors.New("unexpected transaction lookup")
	}
	result := client.lookups[0]
	client.lookups = client.lookups[1:]
	return result.tx, result.pending, result.err
}

func (client *scriptedTransactionSubmitter) SendTransaction(_ context.Context, tx *types.Transaction) error {
	client.sent = append(client.sent, tx)
	return client.sendErr
}

type countingBackend struct {
	bind.ContractBackend
	sends int
}

func (backend *countingBackend) SendTransaction(context.Context, *types.Transaction) error {
	backend.sends++
	return nil
}

func testSignedTransaction(seed string, chainID *big.Int, nonce uint64, recipient *common.Address, value *big.Int, input []byte) *types.Transaction {
	w, err := wallet.RestoreFromSeedHex(seed)
	if err != nil {
		panic(err)
	}
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
		Gas: 21_000, To: recipient, Value: value, Data: input,
	})
	tx, err := types.SignTx(unsigned, types.LatestSignerForChainID(chainID), w)
	if err != nil {
		panic(err)
	}
	return tx
}

func testPreparedTransaction(nonce uint64) (*types.Transaction, PreparedTransaction) {
	tx := testSignedTransaction(goABITestSeed, big.NewInt(1), nonce, nil, new(big.Int), []byte{byte(nonce)})
	return tx, preparedFixture(tx)
}

func preparedFixture(tx *types.Transaction) PreparedTransaction {
	raw, err := tx.MarshalBinary()
	if err != nil {
		panic(err)
	}
	return PreparedTransaction{Hash: tx.Hash().Hex(), Raw: hexutil.Encode(raw)}
}

func setTestTransactionIdentity(run *suiteRun) {
	w, err := wallet.RestoreFromSeedHex(goABITestSeed)
	if err != nil {
		panic(err)
	}
	if err := run.setTransactionIdentity(common.Address(w.GetAddress()), big.NewInt(1)); err != nil {
		panic(err)
	}
}

func testTransactionSender() common.Address {
	w, err := wallet.RestoreFromSeedHex(goABITestSeed)
	if err != nil {
		panic(err)
	}
	return common.Address(w.GetAddress())
}

func successfulTestReceipt(hash common.Hash) *types.Receipt {
	return &types.Receipt{
		TxHash: hash, BlockNumber: big.NewInt(1), Status: types.ReceiptStatusSuccessful,
	}
}

func semanticsOf(tx *types.Transaction) transactionSemantics {
	return newTransactionSemantics(tx.To(), tx.Value(), tx.Data())
}

func TestTransactionLabelsInSuiteOrder(t *testing.T) {
	t.Parallel()
	want := []string{
		"goabi/01-event-emitter-deploy",
		"goabi/02-event-emitter-store",
		"goabi/03-event-emitter-clear",
		"goabi/04-storage-contract-deploy",
		"goabi/05-address-isolation-first",
		"goabi/06-address-isolation-second",
		"goabi/vm64/01-context-deploy",
		"goabi/vm64/02-context-fund",
		"goabi/vm64/03-collision-fund",
		"goabi/vm64/04-call-router-deploy",
		"goabi/vm64/05-introspection-deploy",
		"goabi/vm64/06-warmth-deploy",
		"goabi/vm64/07-creator-deploy",
		"goabi/vm64/08-create",
		"goabi/vm64/09-create2",
		"goabi/vm64/10-reverter-deploy",
		"goabi/vm64/11-catcher-deploy",
		"goabi/vm64/12-caught-revert",
		"goabi/vm64/13-top-level-revert",
		"goabi/07-graphql-send-raw-transaction",
		"goabi/08-websocket-emitter-deploy",
	}
	if got := transactionLabelsInSuiteOrder[:]; !reflect.DeepEqual(got, want) {
		t.Fatalf("transaction labels = %v, want %v", got, want)
	}
}

func TestRecordAndWaitReceiptRecordsBeforeWaiting(t *testing.T) {
	t.Parallel()
	var hash common.Hash
	hash[len(hash)-1] = 0x42
	var order []string
	run := &suiteRun{
		recorder: TransactionRecorderFunc(func(label, got string) error {
			if label != TransactionEventEmitterStore {
				t.Fatalf("record label = %q, want %q", label, TransactionEventEmitterStore)
			}
			if got != hash.Hex() {
				t.Fatalf("record hash = %s, want %s", got, hash.Hex())
			}
			order = append(order, "record")
			return nil
		}),
		receiptWaiter: func(_ context.Context, _ *qrlclient.Client, got common.Hash) (*types.Receipt, error) {
			if got != hash {
				t.Fatalf("wait hash = %s, want %s", got, hash)
			}
			order = append(order, "wait")
			return &types.Receipt{TxHash: hash}, nil
		},
	}

	receipt, err := run.recordAndWaitReceipt(t.Context(), TransactionEventEmitterStore, nil, hash)
	if err != nil {
		t.Fatalf("recordAndWaitReceipt() error = %v", err)
	}
	if receipt.TxHash != hash {
		t.Fatalf("receipt hash = %s, want %s", receipt.TxHash, hash)
	}
	if want := []string{"record", "wait"}; !reflect.DeepEqual(order, want) {
		t.Fatalf("operation order = %v, want %v", order, want)
	}
}

func TestRecordAndWaitReceiptDoesNotWaitAfterRecordFailure(t *testing.T) {
	t.Parallel()
	recordErr := errors.New("durable checkpoint unavailable")
	var hash common.Hash
	hash[len(hash)-1] = 0x7f
	waited := false
	run := &suiteRun{
		recorder: TransactionRecorderFunc(func(string, string) error {
			return recordErr
		}),
		receiptWaiter: func(context.Context, *qrlclient.Client, common.Hash) (*types.Receipt, error) {
			waited = true
			return nil, nil
		},
	}

	receipt, err := run.recordAndWaitReceipt(t.Context(), TransactionGraphQLSendRawTransaction, nil, hash)
	if receipt != nil {
		t.Fatalf("receipt = %+v, want nil", receipt)
	}
	if !errors.Is(err, recordErr) {
		t.Fatalf("recordAndWaitReceipt() error = %v, want wrapped %v", err, recordErr)
	}
	if !strings.Contains(err.Error(), TransactionGraphQLSendRawTransaction) || !strings.Contains(err.Error(), hash.Hex()) {
		t.Fatalf("recording error %q does not identify label and hash", err)
	}
	if waited {
		t.Fatal("receipt wait began after the durable recorder failed")
	}
}

func TestRecordAndWaitReceiptWithoutRecorderPreservesCompatibility(t *testing.T) {
	t.Parallel()
	waited := false
	run := &suiteRun{
		receiptWaiter: func(context.Context, *qrlclient.Client, common.Hash) (*types.Receipt, error) {
			waited = true
			return &types.Receipt{}, nil
		},
	}
	if _, err := run.recordAndWaitReceipt(t.Context(), TransactionEventEmitterDeploy, nil, common.Hash{}); err != nil {
		t.Fatalf("recordAndWaitReceipt() error = %v", err)
	}
	if !waited {
		t.Fatal("receipt wait did not run when recorder was omitted")
	}
}

func TestRecordedTransactionResumesWithoutSubmission(t *testing.T) {
	t.Parallel()
	hash := common.HexToHash("0x1234")
	run, err := newSuiteRun(Options{RecordedTransactions: map[string]string{
		TransactionEventEmitterDeploy: hash.Hex(),
	}})
	if err != nil {
		t.Fatal(err)
	}
	run.receiptWaiter = func(_ context.Context, _ *qrlclient.Client, got common.Hash) (*types.Receipt, error) {
		if got != hash {
			t.Fatalf("wait hash = %s, want %s", got, hash)
		}
		return &types.Receipt{TxHash: hash}, nil
	}
	submitted := false
	receipt, err := run.resumeOrRecordAndWaitReceipt(t.Context(), TransactionEventEmitterDeploy, nil, transactionSemantics{}, func() (common.Hash, error) {
		submitted = true
		return common.Hash{}, nil
	})
	if err != nil || receipt.TxHash != hash || submitted {
		t.Fatalf("receipt = %+v submitted=%t error=%v", receipt, submitted, err)
	}
}

func TestRecordedTransactionsRejectHolesAndAcceptWebsocketHistory(t *testing.T) {
	t.Parallel()
	hash := common.HexToHash("0x1234").Hex()
	if _, err := newSuiteRun(Options{RecordedTransactions: map[string]string{TransactionEventEmitterStore: hash}}); err == nil {
		t.Fatal("recorded transaction hole was accepted")
	}
	values := make(map[string]string)
	for _, label := range transactionLabelsInSuiteOrder[:6] {
		values[label] = hash
	}
	values[TransactionWebSocketEmitterDeploy] = hash
	if _, err := newSuiteRun(Options{RecordedTransactions: values}); err != nil {
		t.Fatal(err)
	}
}

func TestPreparedTransactionRecorderFailurePreventsSendAndMemoryEvidence(t *testing.T) {
	t.Parallel()
	tx, _ := testPreparedTransaction(1)
	recordErr := errors.New("checkpoint fsync failed")
	run := &suiteRun{
		prepared: make(map[string]*types.Transaction),
		preparedRecorder: PreparedTransactionRecorderFunc(func(string, string, string) error {
			return recordErr
		}),
	}
	setTestTransactionIdentity(run)
	transport := &countingBackend{}
	backend := &journalBackend{ContractBackend: transport, run: run, label: TransactionEventEmitterDeploy, semantics: semanticsOf(tx)}
	if err := backend.SendTransaction(t.Context(), tx); !errors.Is(err, recordErr) {
		t.Fatalf("SendTransaction error = %v, want %v", err, recordErr)
	}
	if transport.sends != 0 {
		t.Fatalf("transport sends = %d, want zero", transport.sends)
	}
	if len(run.prepared) != 0 || len(run.recorded) != 0 {
		t.Fatalf("failed durable write left in-memory evidence: prepared=%v recorded=%v", run.prepared, run.recorded)
	}
}

func TestEnsurePreparedSubmittedReconcilesExactRawTransaction(t *testing.T) {
	t.Parallel()
	tx, prepared := testPreparedTransaction(2)
	newRun := func() *suiteRun {
		run := &suiteRun{
			prepared: map[string]*types.Transaction{TransactionEventEmitterDeploy: tx},
			recorded: make(map[string]common.Hash),
		}
		setTestTransactionIdentity(run)
		return run
	}
	tests := []struct {
		name    string
		lookups []struct {
			tx      *types.Transaction
			pending bool
			err     error
		}
		sendErr      error
		wantErr      bool
		wantSends    int
		wantRecorded bool
	}{
		{
			name: "already found",
			lookups: []struct {
				tx      *types.Transaction
				pending bool
				err     error
			}{{tx: tx}},
			wantRecorded: true,
		},
		{
			name: "not found rebroadcasts once",
			lookups: []struct {
				tx      *types.Transaction
				pending bool
				err     error
			}{{err: qrl.NotFound}},
			wantSends: 1, wantRecorded: true,
		},
		{
			name: "send error then lookup found",
			lookups: []struct {
				tx      *types.Transaction
				pending bool
				err     error
			}{{err: qrl.NotFound}, {tx: tx}},
			sendErr: errors.New("response lost"), wantSends: 1, wantRecorded: true,
		},
		{
			name: "send error and still absent",
			lookups: []struct {
				tx      *types.Transaction
				pending bool
				err     error
			}{{err: qrl.NotFound}, {err: qrl.NotFound}},
			sendErr: errors.New("rejected"), wantSends: 1, wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := newRun()
			client := &scriptedTransactionSubmitter{lookups: test.lookups, sendErr: test.sendErr}
			_, ok, err := run.ensurePreparedSubmitted(t.Context(), TransactionEventEmitterDeploy, client, semanticsOf(tx))
			if (err != nil) != test.wantErr {
				t.Fatalf("ensurePreparedSubmitted error = %v, wantErr=%t", err, test.wantErr)
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
			_, recorded := run.recorded[TransactionEventEmitterDeploy]
			if recorded != test.wantRecorded || ok != test.wantRecorded {
				t.Fatalf("recorded=%t ok=%t, want %t", recorded, ok, test.wantRecorded)
			}
		})
	}
}

func TestEnsurePreparedSubmittedReconcilesRecordedPreparedTransaction(t *testing.T) {
	t.Parallel()
	tx, prepared := testPreparedTransaction(20)
	other, _ := testPreparedTransaction(21)
	lookupErr := errors.New("lookup transport failed")
	responseLost := errors.New("submission response lost")
	tests := []struct {
		name    string
		lookups []struct {
			tx      *types.Transaction
			pending bool
			err     error
		}
		sendErr         error
		wantSends       int
		wantLookups     int
		wantErrContains string
	}{
		{
			name: "present exact transaction continues",
			lookups: []struct {
				tx      *types.Transaction
				pending bool
				err     error
			}{{tx: tx}},
			wantLookups: 1,
		},
		{
			name: "not found rebroadcasts exact raw",
			lookups: []struct {
				tx      *types.Transaction
				pending bool
				err     error
			}{{err: qrl.NotFound}},
			wantSends: 1, wantLookups: 1,
		},
		{
			name: "response loss verifies exact transaction",
			lookups: []struct {
				tx      *types.Transaction
				pending bool
				err     error
			}{{err: qrl.NotFound}, {tx: tx}},
			sendErr: responseLost, wantSends: 1, wantLookups: 2,
		},
		{
			name: "lookup semantic mismatch fails closed",
			lookups: []struct {
				tx      *types.Transaction
				pending bool
				err     error
			}{{tx: other}},
			wantLookups: 1, wantErrContains: "different transaction",
		},
		{
			name: "lookup error fails closed",
			lookups: []struct {
				tx      *types.Transaction
				pending bool
				err     error
			}{{err: lookupErr}},
			wantLookups: 1, wantErrContains: lookupErr.Error(),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			recordCalls := 0
			run, err := newSuiteRun(Options{
				RecordedTransactions: map[string]string{TransactionEventEmitterDeploy: tx.Hash().Hex()},
				PreparedTransactions: map[string]PreparedTransaction{TransactionEventEmitterDeploy: prepared},
				TransactionRecorder: TransactionRecorderFunc(func(string, string) error {
					recordCalls++
					return errors.New("recorded transaction was journaled twice")
				}),
			})
			if err != nil {
				t.Fatal(err)
			}
			setTestTransactionIdentity(run)
			client := &scriptedTransactionSubmitter{lookups: test.lookups, sendErr: test.sendErr}
			recorded, ok, err := run.ensurePreparedSubmitted(t.Context(), TransactionEventEmitterDeploy, client, semanticsOf(tx))
			if test.wantErrContains == "" {
				if err != nil {
					t.Fatalf("ensurePreparedSubmitted error = %v", err)
				}
				if !ok || recorded.hash != tx.Hash() {
					t.Fatalf("recorded = (%s, %t), want (%s, true)", recorded.hash, ok, tx.Hash())
				}
			} else if err == nil || !strings.Contains(err.Error(), test.wantErrContains) {
				t.Fatalf("ensurePreparedSubmitted error = %v, want containing %q", err, test.wantErrContains)
			}
			if recordCalls != 0 {
				t.Fatalf("durable recorded journal writes = %d, want zero", recordCalls)
			}
			if len(client.looked) != test.wantLookups {
				t.Fatalf("lookups = %d, want %d", len(client.looked), test.wantLookups)
			}
			for _, hash := range client.looked {
				if hash != tx.Hash() {
					t.Fatalf("lookup hash = %s, want %s", hash, tx.Hash())
				}
			}
			if len(client.sent) != test.wantSends {
				t.Fatalf("sends = %d, want %d", len(client.sent), test.wantSends)
			}
			for _, sent := range client.sent {
				raw, marshalErr := sent.MarshalBinary()
				if marshalErr != nil || hexutil.Encode(raw) != prepared.Raw {
					t.Fatalf("rebroadcast bytes changed: raw=%s err=%v want=%s", hexutil.Encode(raw), marshalErr, prepared.Raw)
				}
			}
		})
	}
}

func TestPreparedTransactionSemanticsFailBeforeAnyRPCCall(t *testing.T) {
	t.Parallel()
	chainID := big.NewInt(1)
	input := []byte{0xaa, 0xbb, 0xcc}
	expected := newTransactionSemantics(nil, new(big.Int), input)
	var recipient common.Address
	recipient[len(recipient)-1] = 0x42
	tests := []struct {
		name string
		tx   *types.Transaction
		want string
	}{
		{
			name: "wrong sender",
			tx:   testSignedTransaction(goABIAlternateSeed, chainID, 30, nil, new(big.Int), input),
			want: "changed sender",
		},
		{
			name: "wrong chain",
			tx:   testSignedTransaction(goABITestSeed, big.NewInt(2), 30, nil, new(big.Int), input),
			want: "changed chain",
		},
		{
			name: "wrong recipient",
			tx:   testSignedTransaction(goABITestSeed, chainID, 30, &recipient, new(big.Int), input),
			want: "changed recipient",
		},
		{
			name: "wrong value",
			tx:   testSignedTransaction(goABITestSeed, chainID, 30, nil, big.NewInt(1), input),
			want: "changed value",
		},
		{
			name: "wrong input",
			tx:   testSignedTransaction(goABITestSeed, chainID, 30, nil, new(big.Int), []byte{0xde, 0xad}),
			want: "changed calldata or creation bytecode",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			run := &suiteRun{
				prepared: map[string]*types.Transaction{TransactionEventEmitterDeploy: test.tx},
				recorded: make(map[string]common.Hash),
			}
			setTestTransactionIdentity(run)
			client := new(scriptedTransactionSubmitter)
			_, ok, err := run.ensurePreparedSubmitted(t.Context(), TransactionEventEmitterDeploy, client, expected)
			if err == nil || !strings.Contains(err.Error(), test.want) || !strings.Contains(err.Error(), TransactionEventEmitterDeploy) {
				t.Fatalf("semantic validation error = %v, want label and %q", err, test.want)
			}
			if ok || len(client.looked) != 0 || len(client.sent) != 0 {
				t.Fatalf("invalid prepared transaction reached RPC: ok=%t lookups=%d sends=%d", ok, len(client.looked), len(client.sent))
			}
		})
	}
}

func TestEnsurePreparedSubmittedRejectsRecordedPreparedHashMismatch(t *testing.T) {
	t.Parallel()
	recordedTx, _ := testPreparedTransaction(22)
	preparedTx, _ := testPreparedTransaction(23)
	run := &suiteRun{
		recorded: map[string]common.Hash{TransactionEventEmitterDeploy: recordedTx.Hash()},
		prepared: map[string]*types.Transaction{TransactionEventEmitterDeploy: preparedTx},
	}
	setTestTransactionIdentity(run)
	client := new(scriptedTransactionSubmitter)
	_, ok, err := run.ensurePreparedSubmitted(t.Context(), TransactionEventEmitterDeploy, client, semanticsOf(preparedTx))
	if err == nil || !strings.Contains(err.Error(), "differs from prepared") {
		t.Fatalf("ensurePreparedSubmitted mismatch error = %v", err)
	}
	if ok || len(client.looked) != 0 || len(client.sent) != 0 {
		t.Fatalf("mismatch result ok=%t lookups=%d sends=%d", ok, len(client.looked), len(client.sent))
	}
}

func TestPreparedTransactionsRejectRawHashMismatchAndOutOfOrderState(t *testing.T) {
	t.Parallel()
	_, first := testPreparedTransaction(3)
	_, second := testPreparedTransaction(4)
	bad := first
	bad.Hash = second.Hash
	if _, err := newSuiteRun(Options{PreparedTransactions: map[string]PreparedTransaction{TransactionEventEmitterDeploy: bad}}); err == nil || !strings.Contains(err.Error(), "hash") {
		t.Fatalf("raw/hash mismatch error = %v", err)
	}
	if _, err := newSuiteRun(Options{PreparedTransactions: map[string]PreparedTransaction{TransactionEventEmitterStore: second}}); err == nil || !strings.Contains(err.Error(), "out of order") {
		t.Fatalf("prepared hole error = %v", err)
	}
	if _, err := newSuiteRun(Options{PreparedTransactions: map[string]PreparedTransaction{
		TransactionEventEmitterDeploy: first,
		TransactionEventEmitterStore:  second,
	}}); err == nil || !strings.Contains(err.Error(), "both unsubmitted") {
		t.Fatalf("multiple prepared-only error = %v", err)
	}
}

func TestSubmittedPreparedHashMismatchRejected(t *testing.T) {
	t.Parallel()
	_, first := testPreparedTransaction(5)
	_, second := testPreparedTransaction(6)
	_, err := newSuiteRun(Options{
		RecordedTransactions: map[string]string{TransactionEventEmitterDeploy: first.Hash},
		PreparedTransactions: map[string]PreparedTransaction{TransactionEventEmitterDeploy: second},
	})
	if err == nil || !strings.Contains(err.Error(), "differs from submitted") {
		t.Fatalf("submitted/prepared mismatch error = %v", err)
	}
}

func TestSubmitPreparedWaitUsesCallerCancellation(t *testing.T) {
	t.Parallel()
	tx, _ := testPreparedTransaction(7)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	run := &suiteRun{
		recorded: map[string]common.Hash{TransactionEventEmitterDeploy: tx.Hash()},
		receiptWaiter: func(got context.Context, _ *qrlclient.Client, _ common.Hash) (*types.Receipt, error) {
			return nil, got.Err()
		},
	}
	setTestTransactionIdentity(run)
	if _, err := run.submitPreparedAndWait(ctx, TransactionEventEmitterDeploy, nil, tx, semanticsOf(tx)); !errors.Is(err, context.Canceled) {
		t.Fatalf("receipt wait error = %v, want canceled caller context", err)
	}
}

func TestGraphQLPredecessorsReplayAndMineInNonceOrderBeforeContinuation(t *testing.T) {
	t.Parallel()
	from := testTransactionSender()
	expected := newTransactionSemantics(&from, new(big.Int), nil)
	base := testSignedTransaction(goABITestSeed, big.NewInt(1), 8, &from, new(big.Int), nil)
	continuation := testSignedTransaction(goABITestSeed, big.NewInt(1), 9, &from, new(big.Int), nil)
	recorded := make(map[string]string)
	for index, label := range mandatoryTransactionLabelsInSuiteOrder {
		recorded[label] = common.BigToHash(big.NewInt(int64(index + 1))).Hex()
	}
	recorded[TransactionGraphQLSendRawTransaction+"/recovered"] = base.Hash().Hex()
	run, err := newSuiteRun(Options{
		PreparedTransactions: map[string]PreparedTransaction{
			TransactionGraphQLSendRawTransaction:               preparedFixture(base),
			TransactionGraphQLSendRawTransaction + "/resume-1": preparedFixture(continuation),
		},
		RecordedTransactions: recorded,
	})
	if err != nil {
		t.Fatal(err)
	}
	setTestTransactionIdentity(run)
	client := &scriptedTransactionSubmitter{lookups: []struct {
		tx      *types.Transaction
		pending bool
		err     error
	}{{err: qrl.NotFound}, {err: qrl.NotFound}}}
	var waited []common.Hash
	wait := func(_ context.Context, hash common.Hash) (*types.Receipt, error) {
		if len(client.sent) != len(waited)+1 {
			t.Fatalf("receipt wait for %s began before its exact replay: sends=%d waits=%d", hash, len(client.sent), len(waited))
		}
		waited = append(waited, hash)
		return successfulTestReceipt(hash), nil
	}
	next, _, validated, err := run.reconcileGraphQLPredecessors(t.Context(), client, expected, wait)
	if err != nil || validated || next != TransactionGraphQLSendRawTransaction+"/resume-2" {
		t.Fatalf("reconcile next=%q validated=%t error=%v", next, validated, err)
	}
	if len(client.sent) != 2 || client.sent[0].Hash() != base.Hash() || client.sent[1].Hash() != continuation.Hash() {
		t.Fatalf("exact GraphQL replays = %#v", client.sent)
	}
	if client.sent[0].Nonce() != 8 || client.sent[1].Nonce() != 9 || !reflect.DeepEqual(waited, []common.Hash{base.Hash(), continuation.Hash()}) {
		t.Fatalf("GraphQL replay order/nonces = %d,%d waits=%v", client.sent[0].Nonce(), client.sent[1].Nonce(), waited)
	}
	if got := run.recorded[TransactionGraphQLSendRawTransaction+"/resume-1/recovered"]; got != continuation.Hash() {
		t.Fatalf("continuation recovery evidence = %s, want %s", got, continuation.Hash())
	}
}

func TestWebSocketPredecessorsReplayAndMineInNonceOrderBeforeContinuation(t *testing.T) {
	t.Parallel()
	input := []byte{0xca, 0xfe}
	expected := newTransactionSemantics(nil, new(big.Int), input)
	base := testSignedTransaction(goABITestSeed, big.NewInt(1), 70, nil, new(big.Int), input)
	continuation := testSignedTransaction(goABITestSeed, big.NewInt(1), 71, nil, new(big.Int), input)
	recorded := make(map[string]string)
	for index, label := range mandatoryTransactionLabelsInSuiteOrder {
		recorded[label] = common.BigToHash(big.NewInt(int64(index + 1))).Hex()
	}
	recorded[TransactionWebSocketEmitterDeploy] = base.Hash().Hex()
	run, err := newSuiteRun(Options{
		PreparedTransactions: map[string]PreparedTransaction{
			TransactionWebSocketEmitterDeploy:               preparedFixture(base),
			TransactionWebSocketEmitterDeploy + "/resume-1": preparedFixture(continuation),
		},
		RecordedTransactions: recorded,
	})
	if err != nil {
		t.Fatal(err)
	}
	setTestTransactionIdentity(run)
	client := &scriptedTransactionSubmitter{lookups: []struct {
		tx      *types.Transaction
		pending bool
		err     error
	}{{err: qrl.NotFound}, {err: qrl.NotFound}}}
	var waited []common.Hash
	wait := func(_ context.Context, hash common.Hash) (*types.Receipt, error) {
		if len(client.sent) != len(waited)+1 {
			t.Fatalf("receipt wait for %s began before its exact replay: sends=%d waits=%d", hash, len(client.sent), len(waited))
		}
		waited = append(waited, hash)
		return successfulTestReceipt(hash), nil
	}
	next, err := run.reconcileWebSocketPredecessors(t.Context(), client, expected, wait)
	if err != nil || next != TransactionWebSocketEmitterDeploy+"/resume-2" {
		t.Fatalf("reconcile next=%q error=%v", next, err)
	}
	if len(client.sent) != 2 || client.sent[0].Hash() != base.Hash() || client.sent[1].Hash() != continuation.Hash() {
		t.Fatalf("exact websocket replays = %#v", client.sent)
	}
	if client.sent[0].Nonce() != 70 || client.sent[1].Nonce() != 71 || !reflect.DeepEqual(waited, []common.Hash{base.Hash(), continuation.Hash()}) {
		t.Fatalf("websocket replay order/nonces = %d,%d waits=%v", client.sent[0].Nonce(), client.sent[1].Nonce(), waited)
	}
	if got := run.recorded[TransactionWebSocketEmitterDeploy+"/resume-1"]; got != continuation.Hash() {
		t.Fatalf("continuation submission evidence = %s, want %s", got, continuation.Hash())
	}
}

func TestWebSocketHashOnlyPredecessorFailsBeforeNonceGapContinuationRPC(t *testing.T) {
	t.Parallel()
	input := []byte{0xde, 0xad}
	expected := newTransactionSemantics(nil, new(big.Int), input)
	baseHash := common.HexToHash("0xfeed")
	continuation := testSignedTransaction(goABITestSeed, big.NewInt(1), 102, nil, new(big.Int), input)
	recorded := make(map[string]string)
	for index, label := range mandatoryTransactionLabelsInSuiteOrder {
		recorded[label] = common.BigToHash(big.NewInt(int64(index + 1))).Hex()
	}
	recorded[TransactionWebSocketEmitterDeploy] = baseHash.Hex()
	run, err := newSuiteRun(Options{
		PreparedTransactions: map[string]PreparedTransaction{
			TransactionWebSocketEmitterDeploy + "/resume-1": preparedFixture(continuation),
		},
		RecordedTransactions: recorded,
	})
	if err != nil {
		t.Fatal(err)
	}
	setTestTransactionIdentity(run)
	client := new(scriptedTransactionSubmitter)
	waited := false
	wait := func(context.Context, common.Hash) (*types.Receipt, error) {
		waited = true
		return nil, errors.New("unexpected websocket receipt wait")
	}

	if _, err := run.reconcileWebSocketPredecessors(t.Context(), client, expected, wait); err == nil || !strings.Contains(err.Error(), "nonce adjacency cannot be proven") {
		t.Fatalf("hash-only predecessor error = %v", err)
	}
	if len(client.looked) != 0 || len(client.sent) != 0 || waited {
		t.Fatalf("hash-only predecessor reached RPC: lookups=%d sends=%d waited=%t", len(client.looked), len(client.sent), waited)
	}
	if _, ok := run.recorded[TransactionWebSocketEmitterDeploy+"/resume-1"]; ok {
		t.Fatal("hash-only predecessor recorded a nonce-gap continuation")
	}
}

func TestGraphQLPredecessorFailureAndNonceGapBlockContinuation(t *testing.T) {
	t.Parallel()
	from := testTransactionSender()
	expected := newTransactionSemantics(&from, new(big.Int), nil)
	newRun := func(t *testing.T, nextNonce uint64) (*suiteRun, *types.Transaction, *types.Transaction) {
		t.Helper()
		base := testSignedTransaction(goABITestSeed, big.NewInt(1), 80, &from, new(big.Int), nil)
		continuation := testSignedTransaction(goABITestSeed, big.NewInt(1), nextNonce, &from, new(big.Int), nil)
		recorded := make(map[string]string)
		for index, label := range mandatoryTransactionLabelsInSuiteOrder {
			recorded[label] = common.BigToHash(big.NewInt(int64(index + 1))).Hex()
		}
		recorded[TransactionGraphQLSendRawTransaction+"/recovered"] = base.Hash().Hex()
		run, err := newSuiteRun(Options{
			PreparedTransactions: map[string]PreparedTransaction{
				TransactionGraphQLSendRawTransaction:               preparedFixture(base),
				TransactionGraphQLSendRawTransaction + "/resume-1": preparedFixture(continuation),
			},
			RecordedTransactions: recorded,
		})
		if err != nil {
			t.Fatal(err)
		}
		setTestTransactionIdentity(run)
		return run, base, continuation
	}

	t.Run("failed receipt", func(t *testing.T) {
		run, base, _ := newRun(t, 81)
		client := &scriptedTransactionSubmitter{lookups: []struct {
			tx      *types.Transaction
			pending bool
			err     error
		}{{tx: base}}}
		wait := func(_ context.Context, hash common.Hash) (*types.Receipt, error) {
			receipt := successfulTestReceipt(hash)
			receipt.Status = types.ReceiptStatusFailed
			return receipt, nil
		}
		if _, _, _, err := run.reconcileGraphQLPredecessors(t.Context(), client, expected, wait); err == nil || !strings.Contains(err.Error(), "failed with status") {
			t.Fatalf("failed predecessor receipt error = %v", err)
		}
		if len(client.looked) != 1 || len(client.sent) != 0 {
			t.Fatalf("failed predecessor advanced to continuation: lookups=%d sends=%d", len(client.looked), len(client.sent))
		}
	})

	t.Run("nonce gap", func(t *testing.T) {
		run, base, _ := newRun(t, 82)
		client := &scriptedTransactionSubmitter{lookups: []struct {
			tx      *types.Transaction
			pending bool
			err     error
		}{{tx: base}}}
		if _, _, _, err := run.reconcileGraphQLPredecessors(t.Context(), client, expected, func(_ context.Context, hash common.Hash) (*types.Receipt, error) {
			return successfulTestReceipt(hash), nil
		}); err == nil || !strings.Contains(err.Error(), "nonce 82, want 81") {
			t.Fatalf("nonce gap error = %v", err)
		}
		if len(client.looked) != 1 || len(client.sent) != 0 {
			t.Fatalf("nonce-gap continuation reached RPC: lookups=%d sends=%d", len(client.looked), len(client.sent))
		}
	})
}

func TestWebSocketFailedPredecessorReceiptBlocksContinuation(t *testing.T) {
	t.Parallel()
	input := []byte{0xba, 0xdd}
	expected := newTransactionSemantics(nil, new(big.Int), input)
	base := testSignedTransaction(goABITestSeed, big.NewInt(1), 90, nil, new(big.Int), input)
	continuation := testSignedTransaction(goABITestSeed, big.NewInt(1), 91, nil, new(big.Int), input)
	recorded := make(map[string]string)
	for index, label := range mandatoryTransactionLabelsInSuiteOrder {
		recorded[label] = common.BigToHash(big.NewInt(int64(index + 1))).Hex()
	}
	recorded[TransactionWebSocketEmitterDeploy] = base.Hash().Hex()
	run, err := newSuiteRun(Options{
		PreparedTransactions: map[string]PreparedTransaction{
			TransactionWebSocketEmitterDeploy:               preparedFixture(base),
			TransactionWebSocketEmitterDeploy + "/resume-1": preparedFixture(continuation),
		},
		RecordedTransactions: recorded,
	})
	if err != nil {
		t.Fatal(err)
	}
	setTestTransactionIdentity(run)
	client := &scriptedTransactionSubmitter{lookups: []struct {
		tx      *types.Transaction
		pending bool
		err     error
	}{{tx: base}}}
	wait := func(_ context.Context, hash common.Hash) (*types.Receipt, error) {
		receipt := successfulTestReceipt(hash)
		receipt.Status = types.ReceiptStatusFailed
		return receipt, nil
	}
	if _, err := run.reconcileWebSocketPredecessors(t.Context(), client, expected, wait); err == nil || !strings.Contains(err.Error(), "failed with status") {
		t.Fatalf("failed predecessor receipt error = %v", err)
	}
	if len(client.looked) != 1 || len(client.sent) != 0 {
		t.Fatalf("failed websocket predecessor advanced to continuation: lookups=%d sends=%d", len(client.looked), len(client.sent))
	}
}

func TestGraphQLProbeHistoryRejectsGapsAndPreparedContinuationOrder(t *testing.T) {
	t.Parallel()
	hash := common.HexToHash("0x99").Hex()
	if _, err := validateRecordedTransactions(map[string]string{
		TransactionGraphQLSendRawTransaction + "/resume-1/recovered": hash,
	}); err == nil || !strings.Contains(err.Error(), "gap") {
		t.Fatalf("GraphQL history gap error = %v", err)
	}
}
