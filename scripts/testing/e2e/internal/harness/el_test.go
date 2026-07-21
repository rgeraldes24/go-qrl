// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"bytes"
	"context"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/config"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
	consoleSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/console"
)

type consoleLookupStub struct {
	tx      *types.Transaction
	err     error
	sendErr error
	looked  *int
	sent    *[]*types.Transaction
}

type consoleTransactionLookupFunc func(context.Context, common.Hash) (*types.Transaction, bool, error)

func (fn consoleTransactionLookupFunc) TransactionByHash(ctx context.Context, hash common.Hash) (*types.Transaction, bool, error) {
	return fn(ctx, hash)
}

func (consoleTransactionLookupFunc) SendTransaction(context.Context, *types.Transaction) error {
	return errors.New("unexpected console transaction send")
}

func (stub consoleLookupStub) TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error) {
	if stub.looked != nil {
		(*stub.looked)++
	}
	return stub.tx, false, stub.err
}

func (stub consoleLookupStub) SendTransaction(_ context.Context, tx *types.Transaction) error {
	if stub.sent != nil {
		*stub.sent = append(*stub.sent, tx)
	}
	return stub.sendErr
}

func consolePreparedFixture(nonce uint64) (*types.Transaction, lifecycle.PreparedTransaction) {
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID: big.NewInt(1), Nonce: nonce, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
		Gas: 100_000, Value: big.NewInt(0), Data: []byte{0xaa, 0xbb},
	})
	w, err := wallet.RestoreFromSeedHex(prefundedDeployerSeed)
	if err != nil {
		panic(err)
	}
	tx, err := types.SignTx(unsigned, types.LatestSignerForChainID(big.NewInt(1)), w)
	if err != nil {
		panic(err)
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		panic(err)
	}
	return tx, lifecycle.PreparedTransaction{Hash: tx.Hash().Hex(), Raw: hexutil.Encode(raw)}
}

func TestDeploymentBytecodeRequiresCanonicalHex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "EventEmitter.bin")
	if err := os.WriteFile(path, []byte("00abff\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := deploymentBytecode(path); err != nil || got != "0x00abff" {
		t.Fatalf("bytecode = %q, %v", got, err)
	}
	for _, invalid := range []string{"", "0", "00AF", "00zz"} {
		if err := os.WriteFile(path, []byte(invalid), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := deploymentBytecode(path); err == nil {
			t.Fatalf("invalid bytecode %q accepted", invalid)
		}
	}
}

func TestPrepareConsoleWorkspaceStagesOnlyRequiredAssets(t *testing.T) {
	source := t.TempDir()
	if err := os.MkdirAll(filepath.Join(source, "console"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(source, "contracts"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, definition := range consoleSuite.Definitions {
		if err := os.WriteFile(filepath.Join(source, "console", definition.Name+".js"), []byte(definition.Name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(source, "contracts", "emitter.js"), []byte("emitter"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "runtime")
	parameters := []byte("var PARAMS = {\"rawTransaction\":\"0x01\"};\n")
	if err := prepareConsoleWorkspace(source, destination, parameters); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(destination, "console", "params.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, parameters) {
		t.Fatalf("parameters = %q", got)
	}
	info, err := os.Stat(filepath.Join(destination, "console", "params.js"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("parameter mode = %o", info.Mode().Perm())
	}
	if err := prepareConsoleWorkspace(source, destination, parameters); err == nil {
		t.Fatal("existing runtime workspace was silently replaced")
	}
}

func TestSignConsoleDeploymentUsesInjectedProcessAndRedactsSeed(t *testing.T) {
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	called := false
	runtime := &Runtime{
		Config: config.RunConfig{RepoRoot: "/repo"}, Writer: writer,
		Dependencies: Dependencies{
			Now: func() time.Time { return time.Unix(1, 0).UTC() },
			Process: func(_ context.Context, command process.Command) (process.Result, error) {
				called = true
				if command.Name != "el1-txsigner" || len(command.Secrets) != 1 || command.Secrets[0] != prefundedDeployerSeed {
					t.Fatalf("txsigner command = %#v", command)
				}
				joined := strings.Join(command.Args, " ")
				if !strings.Contains(joined, "-rpc http://127.0.0.1:8545") || !strings.Contains(joined, "-data 0x00") {
					t.Fatalf("txsigner arguments = %v", command.Args)
				}
				return process.Result{ExitCode: 0, Stdout: []byte("var PARAMS = {};\n"), Stderr: []byte("diagnostic\n")}, nil
			},
		},
	}
	parameters, err := runtime.signConsoleDeployment(t.Context(), "el1", 2, "http://127.0.0.1:8545", "0x00")
	if err != nil {
		t.Fatal(err)
	}
	if !called || string(parameters) != "var PARAMS = {};\n" {
		t.Fatalf("called=%t parameters=%q", called, parameters)
	}
	log, err := os.ReadFile(filepath.Join(writer.Layout().Logs, "el1-txsigner-attempt-2.log"))
	if err != nil {
		t.Fatal(err)
	}
	if string(log) != "diagnostic\n" || strings.Contains(string(log), prefundedDeployerSeed) {
		t.Fatalf("txsigner log = %q", log)
	}
}

func TestTransactionalStageReconcileContinuesFromDurableSubmission(t *testing.T) {
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := lifecycle.Store{Path: filepath.Join(t.TempDir(), "checkpoint.json"), StageOrder: []string{"el1"}}
	state := lifecycle.NewCheckpoint(
		"resume-test", strings.Repeat("a", 40), strings.Repeat("b", 64), t.TempDir(), strings.Repeat("c", 64),
		lifecycle.EnclaveRef{Name: "resume-test", UUID: strings.Repeat("d", 32), Owned: true}, time.Now(),
	)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{Writer: writer, Store: store}
	environment := &lifecycle.RunEnvironment{State: &state}
	reconcile := runtime.transactionalStageReconcile("el1", "el1/")
	action, err := reconcile(t.Context(), environment)
	if err != nil || action != lifecycle.ReconcileRetry {
		t.Fatalf("pre-submission reconciliation = %q, %v", action, err)
	}
	state.Transactions["el1/console/event-deploy"] = "0xabc"
	if action, err := reconcile(t.Context(), environment); err != nil || action != lifecycle.ReconcileRetry {
		t.Fatalf("post-submission reconciliation = %q, %v", action, err)
	}
	recoveredHash := "0x" + strings.Repeat("ab", common.HashLength)
	state.Transactions = map[string]string{}
	started := time.Now().UTC()
	finished := started.Add(time.Second)
	exitCode := 1
	stageName := "el1"
	state.Attempts = []lifecycle.Attempt{{Stage: stageName, Attempt: 1, StartedAt: started, FinishedAt: &finished, ExitCode: &exitCode, FailureMessage: "transaction " + recoveredHash + " as console/event-deploy was submitted but could not be recorded: disk full"}}
	state.Status = lifecycle.StatusFailed
	state.CurrentStage = &stageName
	if action, err := reconcile(t.Context(), environment); err != nil || action != lifecycle.ReconcileRetry {
		t.Fatalf("recoverable submission reconciliation = %q, %v", action, err)
	}
	if got := state.Transactions["el1/console/event-deploy"]; got != recoveredHash {
		t.Fatalf("recovered transaction = %q, want %q", got, recoveredHash)
	}
	state.Attempts = nil
	state.Transactions = map[string]string{"el2/console/event-deploy": "0xdef"}
	action, err = reconcile(t.Context(), environment)
	if err != nil || action != lifecycle.ReconcileRetry {
		t.Fatalf("unrelated transaction reconciliation = %q, %v", action, err)
	}
}

func TestRecordedConsoleTransactionIsInjectedWithoutChangingSignedFallback(t *testing.T) {
	parameters := []byte("var PARAMS = {txHash: 'new'};\n")
	hash := "0x" + strings.Repeat("ab", common.HashLength)
	got, err := appendRecordedConsoleTransaction(parameters, hash)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("PARAMS.recordedTransactionHash = \""+hash+"\";")) || !bytes.Equal(parameters, []byte("var PARAMS = {txHash: 'new'};\n")) {
		t.Fatalf("injected parameters = %q; original = %q", got, parameters)
	}
}

func TestPreparedConsoleTransactionIsParsedAndOverridesFreshFallback(t *testing.T) {
	parameters := []byte("var PARAMS = {\"txHash\":\"fresh\",\"rawTransaction\":\"0xf0\",\"address\":\"Q00\"};\n")
	prepared, err := consolePreparedTransaction(parameters)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Hash != "fresh" || prepared.Raw != "0xf0" {
		t.Fatalf("parsed prepared transaction = %#v", prepared)
	}
	durable := lifecycle.PreparedTransaction{Hash: "0x" + strings.Repeat("ab", common.HashLength), Raw: "0x0102"}
	got, err := appendPreparedConsoleTransaction(parameters, durable, "event-deploy/resume-1")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(got, []byte("PARAMS.txHash = \""+durable.Hash+"\";")) || !bytes.Contains(got, []byte("PARAMS.rawTransaction = \""+durable.Raw+"\";")) || !bytes.Contains(got, []byte("PARAMS.transactionLabel = \"event-deploy/resume-1\";")) {
		t.Fatalf("prepared override = %q", got)
	}
	if _, err := consolePreparedTransaction([]byte("PARAMS = {};")); err == nil {
		t.Fatal("invalid txsigner envelope was accepted")
	}
}

func TestSelectConsoleTransactionUsesAcceptedPreparedTransactionAsIdempotentProof(t *testing.T) {
	t.Parallel()
	accepted, base := consolePreparedFixture(10)
	_, fresh := consolePreparedFixture(11)
	state := lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), "/tmp/dump", strings.Repeat("c", 64), lifecycle.EnclaveRef{Name: "e", UUID: strings.Repeat("d", 32)}, time.Now())
	state.PreparedTransactions["el1/console/event-deploy"] = base
	preparedWrites := 0
	recordPrepared := func(label string, value lifecycle.PreparedTransaction) error {
		preparedWrites++
		state.PreparedTransactions[label] = value
		return nil
	}
	recordRecovered := func(label, hash string) error {
		state.Transactions[label] = hash
		return nil
	}
	label, selected, submitted, err := selectConsoleTransaction(t.Context(), "el1", &state, fresh, consoleLookupStub{tx: accepted}, recordPrepared, recordRecovered)
	if err != nil {
		t.Fatal(err)
	}
	if label != "console/event-deploy" || selected != base || submitted != base.Hash {
		t.Fatalf("selected label=%q tx=%#v submitted=%q", label, selected, submitted)
	}
	if state.Transactions["el1/console/event-deploy/recovered"] != base.Hash || len(state.PreparedTransactions) != 1 || preparedWrites != 0 {
		t.Fatalf("idempotent evidence = prepared %#v transactions %#v writes=%d", state.PreparedTransactions, state.Transactions, preparedWrites)
	}
	label, selected, submitted, err = selectConsoleTransaction(t.Context(), "el1", &state, fresh, consoleLookupStub{tx: accepted}, recordPrepared, recordRecovered)
	if err != nil || label != "console/event-deploy" || selected != base || submitted != base.Hash {
		t.Fatalf("recovered transaction selection = label=%q selected=%#v submitted=%q err=%v", label, selected, submitted, err)
	}
}

func TestSelectConsoleTransactionReusesAbsentPreparedRawAndFailsClosedOnJournalError(t *testing.T) {
	t.Parallel()
	_, base := consolePreparedFixture(20)
	state := lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), "/tmp/dump", strings.Repeat("c", 64), lifecycle.EnclaveRef{Name: "e", UUID: strings.Repeat("d", 32)}, time.Now())
	state.PreparedTransactions["el1/console/event-deploy"] = base
	label, selected, submitted, err := selectConsoleTransaction(t.Context(), "el1", &state, base, consoleLookupStub{err: qrl.NotFound}, func(string, lifecycle.PreparedTransaction) error {
		t.Fatal("prepared recorder called for an existing raw transaction")
		return nil
	}, func(string, string) error {
		t.Fatal("recovered recorder called for an absent transaction")
		return nil
	})
	if err != nil || label != "console/event-deploy" || selected != base || submitted != "" {
		t.Fatalf("absent prepared selection = label=%q selected=%#v submitted=%q err=%v", label, selected, submitted, err)
	}

	empty := lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), "/tmp/dump", strings.Repeat("c", 64), lifecycle.EnclaveRef{Name: "e", UUID: strings.Repeat("d", 32)}, time.Now())
	recordErr := errors.New("checkpoint write failed")
	if _, _, _, err := selectConsoleTransaction(t.Context(), "el1", &empty, base, consoleLookupStub{err: errors.New("must not query")}, func(string, lifecycle.PreparedTransaction) error {
		return recordErr
	}, func(string, string) error { return nil }); !errors.Is(err, recordErr) {
		t.Fatalf("journal failure error = %v, want %v", err, recordErr)
	}
	if len(empty.PreparedTransactions) != 0 || len(empty.Transactions) != 0 {
		t.Fatalf("journal failure left in-memory evidence: %#v", empty)
	}
}

func TestConsoleJournalRejectsGapsUnknownLabelsAndConflictingEvidence(t *testing.T) {
	t.Parallel()
	_, base := consolePreparedFixture(30)
	_, continuation := consolePreparedFixture(31)
	newState := func() lifecycle.Checkpoint {
		return lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), "/tmp/dump", strings.Repeat("c", 64), lifecycle.EnclaveRef{Name: "e", UUID: strings.Repeat("d", 32)}, time.Now())
	}
	tests := []struct {
		name   string
		mutate func(*lifecycle.Checkpoint)
	}{
		{name: "prepared gap", mutate: func(state *lifecycle.Checkpoint) {
			state.PreparedTransactions["el1/console/event-deploy/resume-1"] = continuation
		}},
		{name: "unknown label", mutate: func(state *lifecycle.Checkpoint) {
			state.PreparedTransactions["el1/console/unexpected"] = base
		}},
		{name: "continuation without recovery", mutate: func(state *lifecycle.Checkpoint) {
			state.PreparedTransactions["el1/console/event-deploy"] = base
			state.PreparedTransactions["el1/console/event-deploy/resume-1"] = continuation
		}},
		{name: "recorded continuation", mutate: func(state *lifecycle.Checkpoint) {
			state.PreparedTransactions["el1/console/event-deploy"] = base
			state.Transactions["el1/console/event-deploy/resume-1"] = continuation.Hash
		}},
		{name: "recovered continuation", mutate: func(state *lifecycle.Checkpoint) {
			state.PreparedTransactions["el1/console/event-deploy"] = base
			state.Transactions["el1/console/event-deploy/resume-1/recovered"] = continuation.Hash
		}},
		{name: "both recovered and validated", mutate: func(state *lifecycle.Checkpoint) {
			state.PreparedTransactions["el1/console/event-deploy"] = base
			state.Transactions["el1/console/event-deploy"] = base.Hash
			state.Transactions["el1/console/event-deploy/recovered"] = base.Hash
		}},
		{name: "record without prepared", mutate: func(state *lifecycle.Checkpoint) {
			state.Transactions["el1/console/event-deploy"] = base.Hash
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := newState()
			test.mutate(&state)
			if err := validateConsoleJournal("el1", &state); err == nil {
				t.Fatalf("corrupt console journal was accepted: prepared=%#v transactions=%#v", state.PreparedTransactions, state.Transactions)
			}
		})
	}
}

func TestConsoleJournalRejectsPreparedContinuationAfterRecoveredBaseBeforeRPC(t *testing.T) {
	t.Parallel()
	_, base := consolePreparedFixture(35)
	_, continuation := consolePreparedFixture(36)
	state := lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), "/tmp/dump", strings.Repeat("c", 64), lifecycle.EnclaveRef{Name: "e", UUID: strings.Repeat("d", 32)}, time.Now())
	baseLabel := "el1/console/event-deploy"
	state.PreparedTransactions[baseLabel] = base
	state.Transactions[baseLabel+"/recovered"] = base.Hash
	state.PreparedTransactions[baseLabel+"/resume-1"] = continuation

	if err := validateConsoleJournal("el1", &state); err == nil || !strings.Contains(err.Error(), "continuation journal") || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("console continuation journal error = %v", err)
	}
	lookups := 0
	var sent []*types.Transaction
	preparedWrites := 0
	recoveredWrites := 0
	if _, _, _, err := selectConsoleTransaction(
		t.Context(), "el1", &state, continuation,
		consoleLookupStub{err: qrl.NotFound, looked: &lookups, sent: &sent},
		func(string, lifecycle.PreparedTransaction) error {
			preparedWrites++
			return nil
		},
		func(string, string) error {
			recoveredWrites++
			return nil
		},
	); err == nil || !strings.Contains(err.Error(), "continuation journal") || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("console continuation selection error = %v", err)
	}
	if lookups != 0 || len(sent) != 0 || preparedWrites != 0 || recoveredWrites != 0 {
		t.Fatalf("console continuation reached mutation path: lookups=%d sends=%d prepared writes=%d recovered writes=%d", lookups, len(sent), preparedWrites, recoveredWrites)
	}
}

func TestSelectConsoleTransactionRejectsWrongPreparedSemanticsBeforeReplay(t *testing.T) {
	t.Parallel()
	_, expected := consolePreparedFixture(40)
	wrongUnsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID: big.NewInt(2), Nonce: 40, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
		Gas: 100_000, Value: big.NewInt(0), Data: []byte{0xde, 0xad},
	})
	w, err := wallet.RestoreFromSeedHex(prefundedDeployerSeed)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := types.SignTx(wrongUnsigned, types.LatestSignerForChainID(big.NewInt(2)), w)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := wrong.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	corrupt := lifecycle.PreparedTransaction{Hash: wrong.Hash().Hex(), Raw: hexutil.Encode(raw)}
	state := lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), "/tmp/dump", strings.Repeat("c", 64), lifecycle.EnclaveRef{Name: "e", UUID: strings.Repeat("d", 32)}, time.Now())
	state.PreparedTransactions["el1/console/event-deploy"] = corrupt
	lookupCalled := false
	lookup := consoleTransactionLookupFunc(func(context.Context, common.Hash) (*types.Transaction, bool, error) {
		lookupCalled = true
		return nil, false, qrl.NotFound
	})
	if _, _, _, err := selectConsoleTransaction(t.Context(), "el1", &state, expected, lookup, func(string, lifecycle.PreparedTransaction) error { return nil }, func(string, string) error { return nil }); err == nil || !strings.Contains(err.Error(), "changed sender, chain") {
		t.Fatalf("wrong prepared semantics error = %v", err)
	}
	if lookupCalled {
		t.Fatal("wrong prepared semantics reached the transaction client")
	}
}

func TestRecordedConsoleTransactionRebroadcastsExactRawWhenDropped(t *testing.T) {
	t.Parallel()
	tx, prepared := consolePreparedFixture(50)
	state := lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), "/tmp/dump", strings.Repeat("c", 64), lifecycle.EnclaveRef{Name: "e", UUID: strings.Repeat("d", 32)}, time.Now())
	label := "el1/console/event-deploy"
	state.PreparedTransactions[label] = prepared
	state.Transactions[label] = prepared.Hash
	lookups := 0
	var sent []*types.Transaction

	suiteLabel, selected, submitted, err := selectConsoleTransaction(
		t.Context(), "el1", &state, prepared,
		consoleLookupStub{err: qrl.NotFound, looked: &lookups, sent: &sent},
		func(string, lifecycle.PreparedTransaction) error {
			t.Fatal("recorded transaction unexpectedly wrote another prepared journal entry")
			return nil
		},
		func(string, string) error {
			t.Fatal("recorded transaction unexpectedly wrote recovery evidence")
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if suiteLabel != "console/event-deploy" || selected != prepared || submitted != prepared.Hash {
		t.Fatalf("selection = label=%q prepared=%#v submitted=%q", suiteLabel, selected, submitted)
	}
	if lookups != 1 || len(sent) != 1 {
		t.Fatalf("reconciliation calls = lookups=%d sends=%d, want 1 and 1", lookups, len(sent))
	}
	raw, err := sent[0].MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if sent[0].Hash() != tx.Hash() || hexutil.Encode(raw) != prepared.Raw {
		t.Fatalf("rebroadcast changed transaction: hash=%s raw=%s", sent[0].Hash(), hexutil.Encode(raw))
	}
}

func TestRecoveredConsoleTransactionDropReplaysSameNonceWithoutContinuation(t *testing.T) {
	t.Parallel()
	tx, prepared := consolePreparedFixture(60)
	_, fresh := consolePreparedFixture(61)
	state := lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), "/tmp/dump", strings.Repeat("c", 64), lifecycle.EnclaveRef{Name: "e", UUID: strings.Repeat("d", 32)}, time.Now())
	label := "el1/console/event-deploy"
	state.PreparedTransactions[label] = prepared
	state.Transactions[label+"/recovered"] = prepared.Hash
	var sent []*types.Transaction

	suiteLabel, selected, submitted, err := selectConsoleTransaction(
		t.Context(), "el1", &state, fresh,
		consoleLookupStub{err: qrl.NotFound, sent: &sent},
		func(string, lifecycle.PreparedTransaction) error {
			t.Fatal("response-loss recovery prepared a duplicate deployment")
			return nil
		},
		func(string, string) error {
			t.Fatal("existing recovery evidence was rewritten")
			return nil
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if suiteLabel != "console/event-deploy" || selected != prepared || submitted != prepared.Hash {
		t.Fatalf("selection = label=%q prepared=%#v submitted=%q", suiteLabel, selected, submitted)
	}
	if len(sent) != 1 || sent[0].Hash() != tx.Hash() || sent[0].Nonce() != 60 {
		t.Fatalf("replay = %#v, want exact nonce-60 transaction", sent)
	}
	if len(state.PreparedTransactions) != 1 {
		t.Fatalf("response-loss recovery created a continuation: %#v", state.PreparedTransactions)
	}

	// A later resume still selects the original deployment for receipt/state
	// validation; it never advances to the freshly signed nonce-61 fallback.
	suiteLabel, selected, submitted, err = selectConsoleTransaction(
		t.Context(), "el1", &state, fresh, consoleLookupStub{tx: tx},
		func(string, lifecycle.PreparedTransaction) error {
			return errors.New("unexpected duplicate preparation")
		},
		func(string, string) error { return errors.New("unexpected recovery rewrite") },
	)
	if err != nil || suiteLabel != "console/event-deploy" || selected != prepared || submitted != prepared.Hash {
		t.Fatalf("later resume = label=%q prepared=%#v submitted=%q error=%v", suiteLabel, selected, submitted, err)
	}
}

func TestStageTransactionFiltersPreserveSuiteLabelsAndPreparedBytes(t *testing.T) {
	hash := "0x" + strings.Repeat("ab", common.HashLength)
	prepared := lifecycle.PreparedTransaction{Hash: hash, Raw: "0x0102"}
	if got := stageTransactions(map[string]string{
		"el1/goabi/01-event-emitter-deploy": hash,
		"el2/goabi/01-event-emitter-deploy": "other-stage",
		"el1/console/event-deploy":          "other-suite",
	}, "el1/", "goabi/"); len(got) != 1 || got["goabi/01-event-emitter-deploy"] != hash {
		t.Fatalf("filtered submitted transactions = %#v", got)
	}
	got := stagePreparedTransactions(map[string]lifecycle.PreparedTransaction{
		"el1/goabi/01-event-emitter-deploy": prepared,
		"el2/goabi/01-event-emitter-deploy": {Hash: "other-stage", Raw: "0x03"},
		"el1/console/event-deploy":          {Hash: "other-suite", Raw: "0x04"},
	}, "el1/", "goabi/")
	if len(got) != 1 || got["goabi/01-event-emitter-deploy"].Hash != prepared.Hash || got["goabi/01-event-emitter-deploy"].Raw != prepared.Raw {
		t.Fatalf("filtered prepared transactions = %#v", got)
	}
}
