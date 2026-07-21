// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
	freshSyncSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/freshsync"
)

const (
	freshTestSHA    = "dddddddddddddddddddddddddddddddddddddddd"
	freshTestDigest = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	freshTestTreeID = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	freshELUUID     = "11111111111111111111111111111111"
	freshCLUUID     = "22222222222222222222222222222222"
)

func TestFreshSyncStageRunsImportableSuiteAndPersistsUUIDs(t *testing.T) {
	runtime, environment, fake := newFreshSyncHarnessFixture(t, "fresh-snap", false)
	processCalled := false
	runtime.Dependencies.Process = func(context.Context, process.Command) (process.Result, error) {
		processCalled = true
		return process.Result{}, fmt.Errorf("process runner must not be used for the importable fresh-sync suite")
	}
	var captured freshSyncSuite.Config
	runtime.Dependencies.FreshSync = func(ctx context.Context, configuration freshSyncSuite.Config, options freshSyncSuite.Options) error {
		captured = configuration
		if options.Client != fake || options.Enclave.UUID != runtime.Enclave.UUID || options.Recorder == nil || options.TransactionRecorder == nil || options.ManagedTransactionRecorder == nil || options.Now == nil {
			return fmt.Errorf("fresh-sync integrations are incomplete")
		}
		if options.RecordedServiceCreationIntents == nil || options.RecordedServices == nil || options.RecordedManagedTransactionIntents == nil || options.ManagedTransactionInitialAttempts == nil || options.ManagedTransactionResubmits == nil {
			return fmt.Errorf("fresh-sync managed resume maps are missing")
		}
		creationRecorder, ok := options.Recorder.(freshSyncSuite.TemporaryServiceCreationRecorder)
		if !ok {
			return fmt.Errorf("fresh-sync creation-intent recorder is missing")
		}
		for index, service := range []freshSyncSuite.TemporaryService{
			{Name: configuration.FreshELService, UUID: freshELUUID},
			{Name: configuration.FreshCLService, UUID: freshCLUUID},
		} {
			intent := lifecycle.TemporaryServiceCreationIntent{
				Name: service.Name, EnclaveUUID: runtime.Enclave.UUID,
				ConfigDigest: strings.Repeat(fmt.Sprintf("%x", index+1), 64),
				Marker:       strings.Repeat(fmt.Sprintf("%x", index+3), 64), PreparedAt: options.Now().UTC(),
			}
			if err := creationRecorder.RecordTemporaryServiceCreationIntent(ctx, intent); err != nil {
				return err
			}
			if err := options.Recorder.RecordTemporaryService(ctx, service); err != nil {
				return err
			}
		}
		label := "fresh-snap-transfer"
		if err := options.ManagedTransactionRecorder.RecordManagedTransactionIntent(ctx, label, freshManagedIntent(options.Now().UTC())); err != nil {
			return err
		}
		if err := options.ManagedTransactionRecorder.RecordManagedTransactionInitialAttempt(ctx, label); err != nil {
			return err
		}
		return options.TransactionRecorder.RecordTransaction(ctx, label, "0x"+strings.Repeat("ab", 32))
	}
	if err := runtime.freshSyncStage("snap")(t.Context(), environment); err != nil {
		t.Fatal(err)
	}
	if processCalled {
		t.Fatal("fresh-sync stage spawned the legacy command")
	}
	if captured.SyncMode != "snap" || captured.FreshELService != "fresh-sync-el-snap" || captured.FreshCLService != "fresh-sync-cl-snap" || captured.CleanupOnFailure {
		t.Fatalf("captured config = %+v", captured)
	}
	for name, want := range map[string]string{"fresh-sync-el-snap": freshELUUID, "fresh-sync-cl-snap": freshCLUUID} {
		if environment.State.TemporaryServices[name] != want {
			t.Fatalf("in-memory checkpoint %s = %q, want %q", name, environment.State.TemporaryServices[name], want)
		}
	}
	loaded, err := runtime.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TemporaryServices["fresh-sync-el-snap"] != freshELUUID || loaded.TemporaryServices["fresh-sync-cl-snap"] != freshCLUUID {
		t.Fatalf("durable temporary services = %v", loaded.TemporaryServices)
	}
	if len(loaded.TemporaryServiceCreationIntents) != 2 {
		t.Fatalf("durable temporary service creation intents = %+v", loaded.TemporaryServiceCreationIntents)
	}
	if loaded.Transactions["fresh-snap-transfer"] != "0x"+strings.Repeat("ab", 32) {
		t.Fatalf("durable transactions = %v", loaded.Transactions)
	}
	if intent, ok := loaded.ManagedTransactionIntents["fresh-snap-transfer"]; !ok || intent.AccessList == nil {
		t.Fatalf("durable managed intents = %+v", loaded.ManagedTransactionIntents)
	}
	if _, ok := loaded.ManagedTransactionInitialAttempts["fresh-snap-transfer"]; !ok {
		t.Fatalf("durable managed initial attempts = %v", loaded.ManagedTransactionInitialAttempts)
	}
	if len(loaded.ManagedTransactionResubmits) != 0 {
		t.Fatalf("unexpected durable managed resubmits = %v", loaded.ManagedTransactionResubmits)
	}
	if _, err := os.Stat(filepath.Join(runtime.Writer.Layout().Logs, "fresh-snap-attempt-1.log")); err != nil {
		t.Fatalf("fresh-sync stage log missing: %v", err)
	}
}

func TestFreshSyncReconcileRemovesOnlyRecordedModeUUIDsThenRetries(t *testing.T) {
	runtime, environment, fake := newFreshSyncHarnessFixture(t, "fresh-snap", true)
	for _, service := range []kurtosis.Service{
		{Name: "fresh-sync-el-snap", UUID: freshELUUID},
		{Name: "fresh-sync-cl-snap", UUID: freshCLUUID},
	} {
		if err := fake.AddService(runtime.Enclave, service); err != nil {
			t.Fatal(err)
		}
	}
	environment.State.TemporaryServices = map[string]string{
		"fresh-sync-el-snap": freshELUUID,
		"fresh-sync-cl-snap": freshCLUUID,
		"fresh-sync-el-full": "33333333333333333333333333333333",
	}
	environment.State.Transactions = map[string]string{"fresh-snap-transfer": "0x" + strings.Repeat("ab", 32)}
	now := runtime.Dependencies.Now().UTC()
	environment.State.ManagedTransactionIntents = map[string]lifecycle.ManagedTransactionIntent{"fresh-snap-transfer": freshManagedIntent(now)}
	environment.State.ManagedTransactionInitialAttempts = map[string]time.Time{"fresh-snap-transfer": now}
	environment.State.ManagedTransactionResubmits = map[string]time.Time{"fresh-snap-transfer": now}
	if err := runtime.Store.Save(*environment.State); err != nil {
		t.Fatal(err)
	}
	action, err := runtime.freshSyncReconcile("snap")(t.Context(), environment)
	if err != nil {
		t.Fatal(err)
	}
	if action != lifecycle.ReconcileRetry {
		t.Fatalf("action = %q, want retry", action)
	}
	for _, name := range []string{"fresh-sync-el-snap", "fresh-sync-cl-snap"} {
		if _, err := fake.Service(t.Context(), runtime.Enclave, name); err == nil {
			t.Fatalf("reconciled service %s remains", name)
		}
		if _, exists := environment.State.TemporaryServices[name]; exists {
			t.Fatalf("reconciled UUID %s remains in checkpoint", name)
		}
	}
	if environment.State.TemporaryServices["fresh-sync-el-full"] == "" {
		t.Fatal("snap reconciliation removed full-sync evidence")
	}
	if environment.State.Transactions["fresh-snap-transfer"] == "" {
		t.Fatal("snap reconciliation removed the transaction needed for resumed verification")
	}
	if _, ok := environment.State.ManagedTransactionIntents["fresh-snap-transfer"]; !ok {
		t.Fatal("snap reconciliation removed the managed transaction intent")
	}
	if _, ok := environment.State.ManagedTransactionInitialAttempts["fresh-snap-transfer"]; !ok {
		t.Fatal("snap reconciliation removed the managed initial-attempt marker")
	}
	if _, ok := environment.State.ManagedTransactionResubmits["fresh-snap-transfer"]; !ok {
		t.Fatal("snap reconciliation removed the managed resubmit marker")
	}
}

func TestFreshSyncReconcileBindsHardInterruptedCreationAndResumesInPlace(t *testing.T) {
	runtime, environment, fake := newFreshSyncHarnessFixture(t, "fresh-snap", true)
	now := runtime.Dependencies.Now().UTC()
	intent := lifecycle.TemporaryServiceCreationIntent{
		Name: "fresh-sync-el-snap", EnclaveUUID: runtime.Enclave.UUID,
		ConfigDigest: strings.Repeat("a", 64), Marker: strings.Repeat("b", 64), PreparedAt: now,
	}
	if err := fake.AddService(runtime.Enclave, kurtosis.Service{
		Name: intent.Name, UUID: freshELUUID,
		Labels: map[string]string{freshSyncSuite.TemporaryServiceCreationIntentLabel: intent.Marker},
	}); err != nil {
		t.Fatal(err)
	}
	environment.State.TemporaryServiceCreationIntents = map[string]lifecycle.TemporaryServiceCreationIntent{intent.Name: intent}
	if err := runtime.Store.Save(*environment.State); err != nil {
		t.Fatal(err)
	}
	action, err := runtime.freshSyncReconcile("snap")(t.Context(), environment)
	if err != nil {
		t.Fatal(err)
	}
	if action != lifecycle.ReconcileRetry {
		t.Fatalf("action = %q, want retry", action)
	}
	if environment.State.TemporaryServices[intent.Name] != freshELUUID || environment.State.TemporaryServiceCreationIntents[intent.Name] != intent {
		t.Fatalf("recovered checkpoint = intents %+v services %+v", environment.State.TemporaryServiceCreationIntents, environment.State.TemporaryServices)
	}
	service, err := fake.Service(t.Context(), runtime.Enclave, intent.Name)
	if err != nil {
		t.Fatalf("recovered service was removed: %v", err)
	}
	if service.UUID != freshELUUID {
		t.Fatalf("recovered service UUID = %s, want %s", service.UUID, freshELUUID)
	}
}

func TestFreshSyncReconcileRejectsUUIDMismatchWithoutMutation(t *testing.T) {
	runtime, environment, fake := newFreshSyncHarnessFixture(t, "fresh-full", true)
	currentUUID := "44444444444444444444444444444444"
	if err := fake.AddService(runtime.Enclave, kurtosis.Service{Name: "fresh-sync-el-full", UUID: currentUUID}); err != nil {
		t.Fatal(err)
	}
	environment.State.TemporaryServices = map[string]string{"fresh-sync-el-full": freshELUUID}
	if err := runtime.Store.Save(*environment.State); err != nil {
		t.Fatal(err)
	}
	action, err := runtime.freshSyncReconcile("full")(t.Context(), environment)
	if err == nil || !strings.Contains(err.Error(), "UUID changed") {
		t.Fatalf("action=%q error=%v, want UUID mismatch", action, err)
	}
	if _, err := fake.Service(t.Context(), runtime.Enclave, "fresh-sync-el-full"); err != nil {
		t.Fatalf("mismatched service was not preserved: %v", err)
	}
	loaded, err := runtime.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TemporaryServices["fresh-sync-el-full"] != freshELUUID {
		t.Fatalf("mismatched durable UUID was changed: %v", loaded.TemporaryServices)
	}
}

func TestFreshSyncPassedAttemptReconcilesUUIDsBeforeCompletion(t *testing.T) {
	runtime, environment, fake := newFreshSyncHarnessFixture(t, "fresh-snap", true)
	if err := fake.AddService(runtime.Enclave, kurtosis.Service{Name: "fresh-sync-el-snap", UUID: freshELUUID}); err != nil {
		t.Fatal(err)
	}
	environment.State.TemporaryServices = map[string]string{"fresh-sync-el-snap": freshELUUID}
	if err := runtime.Store.Save(*environment.State); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Writer.WriteSuiteLog("fresh-snap-attempt-1", []byte("SUITE fresh-snap: PASSED\n")); err != nil {
		t.Fatal(err)
	}
	action, err := runtime.freshSyncReconcile("snap")(t.Context(), environment)
	if err != nil {
		t.Fatal(err)
	}
	if action != lifecycle.ReconcileComplete {
		t.Fatalf("action = %q, want complete", action)
	}
	if _, err := fake.Service(t.Context(), runtime.Enclave, "fresh-sync-el-snap"); err == nil {
		t.Fatal("passed attempt left its recorded temporary service behind")
	}
}

func newFreshSyncHarnessFixture(t *testing.T, stageName string, failed bool) (*Runtime, *lifecycle.RunEnvironment, *kurtosis.FakeClient) {
	t.Helper()
	fake := kurtosis.NewFakeClient()
	enclave, err := fake.CreateEnclave(t.Context(), "fresh-sync-test")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	writer, err := report.New(root)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: []string{stageName}, Now: func() time.Time { return now }}
	state := lifecycle.NewCheckpoint("run-fresh", freshTestSHA, freshTestDigest, filepath.Join(root, "dump"), freshTestTreeID, enclave, now)
	if failed {
		finished := now.Add(time.Second)
		exitCode := 1
		state.Attempts = []lifecycle.Attempt{{Stage: stageName, Attempt: 1, StartedAt: now, FinishedAt: &finished, ExitCode: &exitCode, FailureCategory: lifecycle.FailureAssertion, FailureMessage: "interrupted"}}
		state.Status = lifecycle.StatusFailed
		state.CurrentStage = &state.Attempts[0].Stage
		state.FailureCategory = lifecycle.FailureAssertion
		state.FailureMessage = "interrupted"
	}
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		RunID: "run-fresh", Enclave: enclave, Writer: writer, Store: store,
		Dependencies: Dependencies{Client: fake, Now: func() time.Time { return now }},
	}
	environment := &lifecycle.RunEnvironment{Enclave: enclave, State: &state, Values: map[string]any{}}
	return runtime, environment, fake
}

func freshManagedIntent(now time.Time) lifecycle.ManagedTransactionIntent {
	return lifecycle.ManagedTransactionIntent{
		Phase: "fresh-snap", Label: "fresh-snap-transfer", Origin: 0,
		OriginServiceName: "el-1-gqrl-qrysm", OriginServiceUUID: "33333333333333333333333333333333",
		ChainID: "0x539", From: "Q" + strings.Repeat("1", 128), To: "Q" + strings.Repeat("2", 128),
		Value: "0x1", Input: "0x", AccessList: []lifecycle.ManagedAccessTuple{}, Nonce: 7,
		StartBlock: 12, StartBlockHash: "0x" + strings.Repeat("4", 64), PreparedAt: now.UTC(),
	}
}
