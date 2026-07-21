package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
)

const (
	testSHA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testUUID   = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	testDigest = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	testTreeID = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
)

func TestOwnershipIntentPrecedesUUIDCapture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ownership.json")
	now := time.Unix(1_700_000_000, 0).UTC()
	record, err := NewOwnership(path, "run-1", "vm64", now)
	if err != nil {
		t.Fatal(err)
	}
	if record.UUID != nil {
		t.Fatal("ownership intent unexpectedly has a UUID")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"uuid": null`) {
		t.Fatalf("ownership intent did not durably encode a null UUID: %s", raw)
	}
	if _, err := CaptureOwnershipUUID(path, "other", testUUID); err == nil {
		t.Fatal("ownership name mismatch was accepted")
	}
	captured, err := CaptureOwnershipUUID(path, "vm64", testUUID)
	if err != nil {
		t.Fatal(err)
	}
	if captured.UUID == nil || *captured.UUID != testUUID {
		t.Fatalf("captured UUID = %v", captured.UUID)
	}
	if _, err := CaptureOwnershipUUID(path, "vm64", testUUID); err == nil {
		t.Fatal("second UUID capture was accepted")
	}
	if err := MarkOwnershipDestroyed(path, strings.Repeat("e", 32), now); err == nil {
		t.Fatal("mismatched cleanup UUID was accepted")
	}
	requestedAt := now.Add(time.Minute)
	if err := MarkOwnershipDestroyRequested(path, testUUID, requestedAt); err != nil {
		t.Fatal(err)
	}
	if err := MarkOwnershipDestroyRequested(path, testUUID, requestedAt.Add(time.Minute)); err != nil {
		t.Fatalf("idempotent destruction request: %v", err)
	}
	requested, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if requested.DestroyRequestedAt == nil || !requested.DestroyRequestedAt.Equal(requestedAt) || requested.DestroyedAt != nil {
		t.Fatalf("durable destruction request = %+v", requested)
	}
	destroyedAt := requestedAt.Add(time.Minute)
	if err := MarkOwnershipDestroyed(path, testUUID, destroyedAt); err != nil {
		t.Fatal(err)
	}
	destroyed, err := LoadOwnership(path)
	if err != nil {
		t.Fatal(err)
	}
	if destroyed.DestroyRequestedAt == nil || destroyed.DestroyedAt == nil || !destroyed.DestroyedAt.Equal(destroyedAt) {
		t.Fatalf("destroyed ownership lost request boundary: %+v", destroyed)
	}
}

func TestOwnershipRejectsInvalidCleanupTimestamps(t *testing.T) {
	createdAt := time.Unix(1_700_000_000, 0).UTC()
	uuid := testUUID
	validRequest := createdAt.Add(time.Minute)
	validDestroyed := validRequest.Add(time.Minute)
	nonUTC := validDestroyed.In(time.FixedZone("not-utc", 60))
	zero := time.Time{}
	predatesCreation := createdAt.Add(-time.Second)
	predatesRequest := validRequest.Add(-time.Second)

	for _, test := range []struct {
		name      string
		requested *time.Time
		destroyed *time.Time
	}{
		{name: "zero request", requested: &zero},
		{name: "request before creation", requested: &predatesCreation},
		{name: "zero destruction", destroyed: &zero},
		{name: "non-UTC destruction", destroyed: &nonUTC},
		{name: "destruction before creation", destroyed: &predatesCreation},
		{name: "destruction before request", requested: &validRequest, destroyed: &predatesRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			record := OwnershipRecord{
				Schema: SchemaVersion, RunID: "run-1", RequestedName: "vm64",
				CreatedAt: createdAt, UUID: &uuid,
				DestroyRequestedAt: test.requested, DestroyedAt: test.destroyed,
			}
			if err := record.Validate(); err == nil {
				t.Fatalf("invalid ownership record was accepted: %+v", record)
			}
		})
	}

	valid := OwnershipRecord{
		Schema: SchemaVersion, RunID: "run-1", RequestedName: "vm64",
		CreatedAt: createdAt, UUID: &uuid,
		DestroyRequestedAt: &validRequest, DestroyedAt: &validDestroyed,
	}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid cleanup timestamps were rejected: %v", err)
	}
}

func TestStoreReadsLegacyPythonSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	raw := map[string]any{
		"schema": 1, "source_sha": testSHA,
		"enclave":  map[string]any{"name": "vm64", "uuid": testUUID},
		"dump_dir": "/tmp/dump", "initial_tree_id": testTreeID,
		"resume_tree_ids": []string{}, "status": "running", "current_stage": nil,
		"completed": []string{}, "attempts": []any{}, "resumed": false,
		"created_at": "2026-07-21T00:00:00Z", "updated_at": "2026-07-21T00:00:00Z",
	}
	payload, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	store := Store{Path: path, StageOrder: []string{"one"}}
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.ConfigurationDigest != "" || state.Enclave.Owned {
		t.Fatalf("legacy optional fields were not preserved as absent: %+v", state)
	}
	if err := state.PrepareResume(store, testSHA, testDigest, testTreeID, "", time.Now()); err != nil {
		t.Fatal(err)
	}
	state, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.ConfigurationDigest != testDigest || !state.Resumed || len(state.ResumeHistory) != 1 {
		t.Fatalf("legacy checkpoint was not migrated on resume: %+v", state)
	}
}

func TestStoreRejectsNonEmptyLegacyHistoryWithMigrationGuidance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	finished := time.Date(2026, 7, 21, 0, 1, 0, 0, time.UTC)
	exitCode := 0
	state := Checkpoint{
		Schema: SchemaVersion, SourceSHA: testSHA,
		Enclave: EnclaveRef{Name: "vm64", UUID: testUUID},
		DumpDir: "/tmp/dump", InitialTreeID: testTreeID,
		ResumeTreeIDs: []string{}, Status: StatusRunning,
		Completed: []string{"fixture"},
		Attempts: []Attempt{{
			Stage: "fixture", Attempt: 1,
			StartedAt: finished.Add(-time.Minute), FinishedAt: &finished, ExitCode: &exitCode,
		}},
		CreatedAt: finished.Add(-time.Minute), UpdatedAt: finished,
	}
	if err := writeJSONAtomic(path, state); err != nil {
		t.Fatal(err)
	}

	legacyStore := Store{Path: path, StageOrder: []string{"fixture", "host-preflight"}}
	if _, err := legacyStore.Load(); err != nil {
		t.Fatalf("legacy runner could not read its own checkpoint: %v", err)
	}
	goStore := Store{Path: path, StageOrder: []string{"validate", "reserve", "fixture"}}
	_, err := goStore.Load()
	if err == nil || !strings.Contains(err.Error(), "legacy Python stage history") || !strings.Contains(err.Error(), "run_e2e_from_scratch.sh -r") {
		t.Fatalf("Go-runner legacy rejection = %v", err)
	}
}

func TestStoreRejectsCheckpointCorruption(t *testing.T) {
	now := time.Date(2026, 7, 21, 0, 0, 0, 0, time.UTC)
	base := NewCheckpoint(
		"run-corruption", testSHA, testDigest, "/tmp/dump", testTreeID,
		EnclaveRef{Name: "vm64", UUID: testUUID, Owned: true}, now,
	)
	stageOrder := []string{"one", "two"}

	tests := []struct {
		name   string
		mutate func(*Checkpoint)
		encode func([]byte) []byte
		want   string
	}{
		{
			name: "truncated JSON",
			encode: func(payload []byte) []byte {
				return payload[:len(payload)-1]
			},
			want: "unexpected EOF",
		},
		{
			name: "trailing JSON",
			encode: func(payload []byte) []byte {
				return append(payload, []byte("\n{}")...)
			},
			want: "trailing data",
		},
		{
			name:   "wrong schema",
			mutate: func(state *Checkpoint) { state.Schema = SchemaVersion + 1 },
			want:   "checkpoint schema",
		},
		{
			name:   "invalid source SHA",
			mutate: func(state *Checkpoint) { state.SourceSHA = "wrong-source" },
			want:   "source SHA is invalid",
		},
		{
			name:   "invalid completed prefix",
			mutate: func(state *Checkpoint) { state.Completed = []string{"two"} },
			want:   "not an exact ordered prefix",
		},
		{
			name: "invalid current stage",
			mutate: func(state *Checkpoint) {
				current := "missing"
				state.CurrentStage = &current
			},
			want: "current stage",
		},
		{
			name: "running attempt disagrees with current stage",
			mutate: func(state *Checkpoint) {
				current := "two"
				state.CurrentStage = &current
				state.Attempts = []Attempt{{Stage: "one", Attempt: 1, StartedAt: now}}
			},
			want: "running attempt does not match current stage",
		},
		{
			name: "failed attempt disagrees with current stage",
			mutate: func(state *Checkpoint) {
				current := "two"
				finished := now.Add(time.Minute)
				exitCode := 17
				state.Status = StatusFailed
				state.CurrentStage = &current
				state.Attempts = []Attempt{{
					Stage: "one", Attempt: 1, StartedAt: now,
					FinishedAt: &finished, ExitCode: &exitCode,
				}}
			},
			want: "current stage does not match failed attempt",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			state := base
			if test.mutate != nil {
				test.mutate(&state)
			}
			payload, err := json.Marshal(state)
			if err != nil {
				t.Fatal(err)
			}
			if test.encode != nil {
				payload = test.encode(payload)
			}
			path := filepath.Join(t.TempDir(), "checkpoint.json")
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			_, err = (Store{Path: path, StageOrder: stageOrder}).Load()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Store.Load error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRunnerPersistsFailureAndReconcilesRetry(t *testing.T) {
	stages := []string{"mutate", "observe"}
	store, state := newTestStore(t, stages)
	attempts := 0
	reconciled := 0
	now := time.Unix(1_700_000_000, 0).UTC()
	runner := Runner{
		Store: store, GlobalDeadline: now.Add(2 * time.Hour), CleanupReserve: time.Hour, Now: func() time.Time { return now },
		Stages: []Stage{
			{Name: "mutate", Timeout: 10 * time.Minute, MinimumRuntime: time.Minute, ResumePolicy: InspectBeforeRetry,
				Run: func(context.Context, *RunEnvironment) error {
					attempts++
					if attempts == 1 {
						return errors.New("injected mutation failure")
					}
					return nil
				},
				Reconcile: func(context.Context, *RunEnvironment) (ReconcileAction, error) {
					reconciled++
					return ReconcileRetry, nil
				}},
			{Name: "observe", Timeout: 10 * time.Minute, MinimumRuntime: time.Minute, ResumePolicy: RetrySafe,
				Run: func(context.Context, *RunEnvironment) error { return nil }},
		},
	}
	environment := &RunEnvironment{Enclave: state.Enclave}
	if err := runner.Run(context.Background(), environment); err == nil {
		t.Fatal("injected failure passed")
	}
	failed, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != StatusFailed || failed.CurrentStage == nil || *failed.CurrentStage != "mutate" {
		t.Fatalf("failure was not checkpointed: %+v", failed)
	}
	if err := failed.PrepareResume(store, testSHA, testDigest, testTreeID, "mutate", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), environment); err != nil {
		t.Fatal(err)
	}
	if reconciled != 1 || attempts != 2 {
		t.Fatalf("reconciled=%d attempts=%d, want 1/2", reconciled, attempts)
	}
	completed, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := completed.MarkComplete(store, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	completed, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != StatusCompleteAfterResume {
		t.Fatalf("terminal status = %s", completed.Status)
	}
}

func TestRunnerPropagatesAttemptEvidenceFailureWithoutReplayingStage(t *testing.T) {
	stages := []string{"one"}
	store, state := newTestStore(t, stages)
	now := time.Unix(1_700_000_000, 0).UTC()
	runs := 0
	evidenceError := errors.New("artifact write failed")
	runner := Runner{
		Store: store, GlobalDeadline: now.Add(2 * time.Hour), CleanupReserve: time.Hour,
		Now: func() time.Time { return now },
		Stages: []Stage{{
			Name: "one", Timeout: 10 * time.Minute, MinimumRuntime: time.Minute,
			ResumePolicy: RetrySafe,
			Run:          func(context.Context, *RunEnvironment) error { runs++; return nil },
		}},
		OnAttemptFinished: func(Attempt) error { return evidenceError },
	}
	if err := runner.Run(t.Context(), &RunEnvironment{Enclave: state.Enclave}); !errors.Is(err, evidenceError) {
		t.Fatalf("runner error = %v, want evidence error", err)
	}
	if runs != 1 {
		t.Fatalf("stage runs = %d, want 1", runs)
	}
	checkpoint, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !checkpoint.StageComplete("one") {
		t.Fatal("successful stage was not durably completed before evidence failure")
	}

	runner.OnAttemptFinished = nil
	if err := runner.Run(t.Context(), &RunEnvironment{Enclave: state.Enclave}); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("completed stage replayed after evidence failure: %d runs", runs)
	}
}

func TestRunnerReconciliationCompletesStageWithoutReplay(t *testing.T) {
	store, state := newTestStore(t, []string{"deposit"})
	now := time.Unix(1_700_000_000, 0).UTC()
	runs := 0
	runner := Runner{
		Store: store, GlobalDeadline: now.Add(2 * time.Hour), CleanupReserve: time.Hour, Now: func() time.Time { return now },
		Stages: []Stage{{
			Name: "deposit", Timeout: 10 * time.Minute, MinimumRuntime: time.Minute, ResumePolicy: InspectBeforeRetry,
			Run: func(context.Context, *RunEnvironment) error {
				runs++
				return errors.New("receipt wait interrupted after transaction submission")
			},
			Reconcile: func(context.Context, *RunEnvironment) (ReconcileAction, error) {
				return ReconcileComplete, nil
			},
		}},
	}
	environment := &RunEnvironment{Enclave: state.Enclave}
	if err := runner.Run(context.Background(), environment); err == nil {
		t.Fatal("injected first attempt passed")
	}
	failed, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.PrepareResume(store, testSHA, testDigest, testTreeID, "deposit", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := runner.Run(context.Background(), environment); err != nil {
		t.Fatal(err)
	}
	if runs != 1 {
		t.Fatalf("state-changing stage replayed %d times, want one original execution", runs)
	}
	completed, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(completed.Completed) != 1 || len(completed.Attempts) != 2 || !completed.Attempts[1].Reconciled {
		t.Fatalf("reconciled completion evidence is incomplete: %+v", completed)
	}
}

func TestRunnerCheckpointSaveFailuresLeaveMemoryAndDiskAtLastDurablePoint(t *testing.T) {
	t.Run("reconciled completion", func(t *testing.T) {
		store, state := newTestStore(t, []string{"one"})
		started := time.Now().UTC()
		finished := started.Add(time.Second)
		exitCode := 1
		stageName := "one"
		state.Status = StatusFailed
		state.CurrentStage = &stageName
		state.Attempts = []Attempt{{
			Stage: stageName, Attempt: 1, StartedAt: started, FinishedAt: &finished, ExitCode: &exitCode,
			FailureCategory: FailureAssertion, FailureMessage: "injected failure",
		}}
		state.FailureCategory = FailureAssertion
		state.FailureMessage = "injected failure"
		state.UpdatedAt = finished
		if err := store.Save(state); err != nil {
			t.Fatal(err)
		}
		want, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}

		badStore := store
		badStore.Path = t.TempDir()
		callbacks := 0
		runner := Runner{
			Store: badStore,
			Now:   func() time.Time { return finished.Add(time.Minute) },
			OnAttemptFinished: func(Attempt) error {
				callbacks++
				return nil
			},
		}
		environment := &RunEnvironment{Enclave: state.Enclave, State: &state}
		err = runner.recordReconciledCompletion(Stage{Name: stageName}, environment, &state)
		if err == nil || !strings.Contains(err.Error(), "checkpoint reconciled stage one") {
			t.Fatalf("reconciled checkpoint error = %v", err)
		}
		if callbacks != 0 {
			t.Fatalf("attempt callback ran %d times after failed checkpoint save", callbacks)
		}
		assertCheckpointEqual(t, want, state)
		assertCheckpointEqual(t, want, *environment.State)
		persisted, loadErr := store.Load()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		assertCheckpointEqual(t, want, persisted)
	})

	t.Run("stage start", func(t *testing.T) {
		store, state := newTestStore(t, []string{"one"})
		started := time.Now().UTC()
		finished := started.Add(time.Second)
		exitCode := 1
		stageName := "one"
		state.Status = StatusFailed
		state.CurrentStage = &stageName
		state.Attempts = []Attempt{{
			Stage: stageName, Attempt: 1, StartedAt: started, FinishedAt: &finished, ExitCode: &exitCode,
			FailureCategory: FailureAssertion, FailureMessage: "injected failure",
		}}
		state.FailureCategory = FailureAssertion
		state.FailureMessage = "injected failure"
		state.UpdatedAt = finished
		if err := store.Save(state); err != nil {
			t.Fatal(err)
		}
		want, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}

		badStore := store
		badStore.Path = t.TempDir()
		runs := 0
		callbacks := 0
		now := time.Now().UTC()
		runner := Runner{
			Store: badStore, GlobalDeadline: now.Add(time.Hour), CleanupReserve: time.Minute,
			Now: func() time.Time { return now },
			OnAttemptFinished: func(Attempt) error {
				callbacks++
				return nil
			},
		}
		stage := Stage{
			Name: stageName, Timeout: time.Minute, MinimumRuntime: time.Second,
			Run: func(context.Context, *RunEnvironment) error {
				runs++
				return nil
			},
		}
		environment := &RunEnvironment{Enclave: state.Enclave, State: &state}
		err = runner.runStage(t.Context(), stage, environment, &state)
		if err == nil || !strings.Contains(err.Error(), "checkpoint stage one start") {
			t.Fatalf("stage-start checkpoint error = %v", err)
		}
		if runs != 0 || callbacks != 0 {
			t.Fatalf("stage runs/callbacks after failed start save = %d/%d, want 0/0", runs, callbacks)
		}
		assertCheckpointEqual(t, want, state)
		assertCheckpointEqual(t, want, *environment.State)
		persisted, loadErr := store.Load()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		assertCheckpointEqual(t, want, persisted)
	})

	for _, test := range []struct {
		name     string
		stageErr error
	}{
		{name: "successful stage result"},
		{name: "failed stage result", stageErr: errors.New("injected stage failure")},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, state := newTestStore(t, []string{"one"})
			badPath := t.TempDir()
			started := time.Now().UTC()
			finished := started.Add(time.Second)
			callbacks := 0
			var want Checkpoint
			var runner Runner
			nowCalls := 0
			runner = Runner{
				Store: store, GlobalDeadline: started.Add(time.Hour), CleanupReserve: time.Minute,
				Now: func() time.Time {
					nowCalls++
					if nowCalls == 2 {
						runner.Store.Path = badPath
						return finished
					}
					return started
				},
				OnAttemptFinished: func(Attempt) error {
					callbacks++
					return nil
				},
			}
			stage := Stage{
				Name: "one", Timeout: time.Minute, MinimumRuntime: time.Second,
				Run: func(context.Context, *RunEnvironment) error {
					var err error
					want, err = store.Load()
					if err != nil {
						t.Fatalf("load durable stage-start checkpoint: %v", err)
					}
					return test.stageErr
				},
			}
			environment := &RunEnvironment{Enclave: state.Enclave, State: &state}
			err := runner.runStage(t.Context(), stage, environment, &state)
			if err == nil || !strings.Contains(err.Error(), "checkpoint stage one result after exit") {
				t.Fatalf("stage-result checkpoint error = %v", err)
			}
			if callbacks != 0 {
				t.Fatalf("attempt callback ran %d times after failed result save", callbacks)
			}
			if len(want.Attempts) != 1 || want.Attempts[0].FinishedAt != nil || want.Attempts[0].ExitCode != nil ||
				want.Status != StatusRunning || want.CurrentStage == nil || *want.CurrentStage != "one" {
				t.Fatalf("durable stage-start checkpoint is incomplete: %+v", want)
			}
			assertCheckpointEqual(t, want, state)
			assertCheckpointEqual(t, want, *environment.State)
			persisted, loadErr := store.Load()
			if loadErr != nil {
				t.Fatal(loadErr)
			}
			assertCheckpointEqual(t, want, persisted)
		})
	}
}

func assertCheckpointEqual(t *testing.T, want, got Checkpoint) {
	t.Helper()
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	gotJSON, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("checkpoint changed past the last durable point\nwant: %s\n got: %s", wantJSON, gotJSON)
	}
}

func TestRunnerRejectsResumeWhenReconciliationIsImpossible(t *testing.T) {
	store, state := newTestStore(t, []string{"restart"})
	now := time.Unix(1_700_000_000, 0).UTC()
	runs := 0
	runner := Runner{
		Store: store, GlobalDeadline: now.Add(2 * time.Hour), CleanupReserve: time.Hour, Now: func() time.Time { return now },
		Stages: []Stage{{
			Name: "restart", Timeout: 10 * time.Minute, MinimumRuntime: time.Minute, ResumePolicy: InspectBeforeRetry,
			Run: func(context.Context, *RunEnvironment) error {
				runs++
				return errors.New("interrupted restart")
			},
			Reconcile: func(context.Context, *RunEnvironment) (ReconcileAction, error) {
				return "", errors.New("service UUID and state cannot be determined")
			},
		}},
	}
	environment := &RunEnvironment{Enclave: state.Enclave}
	if err := runner.Run(context.Background(), environment); err == nil {
		t.Fatal("injected first attempt passed")
	}
	failed, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.PrepareResume(store, testSHA, testDigest, testTreeID, "restart", now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	err = runner.Run(context.Background(), environment)
	if err == nil || !strings.Contains(err.Error(), "cannot be determined") {
		t.Fatalf("resume reconciliation error = %v", err)
	}
	if runs != 1 {
		t.Fatalf("unreconciled stage replayed %d times", runs)
	}
}

func TestRunnerRejectsBorrowedDisruptionAndInsufficientBudget(t *testing.T) {
	store, state := newTestStore(t, []string{"restart"})
	now := time.Unix(1_700_000_000, 0).UTC()
	stage := Stage{Name: "restart", Timeout: time.Hour, MinimumRuntime: 10 * time.Minute, ResumePolicy: InspectBeforeRetry, Disruptive: true, Run: func(context.Context, *RunEnvironment) error { return nil }}
	runner := Runner{Store: store, Stages: []Stage{stage}, GlobalDeadline: now.Add(2 * time.Hour), CleanupReserve: time.Hour, Now: func() time.Time { return now }}
	borrowed := state.Enclave
	borrowed.Owned = false
	if err := runner.Run(context.Background(), &RunEnvironment{Enclave: borrowed}); err == nil || !strings.Contains(err.Error(), "borrowed-network") {
		t.Fatalf("borrowed disruption error = %v", err)
	}
	runner.AllowDisruptive = true
	runner.GlobalDeadline = now.Add(time.Hour + 5*time.Minute)
	if err := runner.Run(context.Background(), &RunEnvironment{Enclave: borrowed}); err == nil || !strings.Contains(err.Error(), "refusing stage") {
		t.Fatalf("minimum-runtime error = %v", err)
	}
}

func TestRunnerPersistsCancellationAndDeadline(t *testing.T) {
	for _, test := range []struct {
		name         string
		parent       func() context.Context
		timeout      time.Duration
		wantError    error
		wantCategory FailureCategory
	}{
		{
			name: "cancellation",
			parent: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			timeout: time.Second, wantError: context.Canceled, wantCategory: FailureCancellation,
		},
		{
			name:    "deadline",
			parent:  func() context.Context { return context.Background() },
			timeout: 50 * time.Millisecond, wantError: context.DeadlineExceeded, wantCategory: FailureTimeout,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			store, state := newTestStore(t, []string{"wait"})
			now := time.Now().UTC()
			runner := Runner{
				Store: store, GlobalDeadline: now.Add(time.Hour), CleanupReserve: time.Minute, Now: time.Now,
				Stages: []Stage{{
					Name: "wait", Timeout: test.timeout, MinimumRuntime: time.Millisecond, ResumePolicy: RetrySafe,
					Run: func(ctx context.Context, _ *RunEnvironment) error {
						<-ctx.Done()
						return ctx.Err()
					},
				}},
			}
			err := runner.Run(test.parent(), &RunEnvironment{Enclave: state.Enclave})
			if !errors.Is(err, test.wantError) {
				t.Fatalf("runner error = %v, want %v", err, test.wantError)
			}
			failed, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			if failed.Status != StatusFailed || failed.FailureCategory != test.wantCategory || len(failed.Attempts) != 1 || failed.Attempts[0].FinishedAt == nil {
				t.Fatalf("persisted %s checkpoint = %+v", test.name, failed)
			}
		})
	}
}

func TestPrepareResumeRejectsConfigurationMismatchAndRecordsTreeChange(t *testing.T) {
	t.Run("source mismatch", func(t *testing.T) {
		store, state := newTestStore(t, []string{"one"})
		err := state.PrepareResume(store, strings.Repeat("b", 40), testDigest, testTreeID, "", time.Now())
		if err == nil || !strings.Contains(err.Error(), "does not match checkout") {
			t.Fatalf("source mismatch error = %v", err)
		}
		unchanged, loadErr := store.Load()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if unchanged.Resumed || len(unchanged.ResumeHistory) != 0 {
			t.Fatalf("rejected resume mutated checkpoint: %+v", unchanged)
		}
	})

	t.Run("configuration mismatch", func(t *testing.T) {
		store, state := newTestStore(t, []string{"one"})
		err := state.PrepareResume(store, testSHA, strings.Repeat("1", 64), testTreeID, "", time.Now())
		if err == nil || !strings.Contains(err.Error(), "does not match requested configuration") {
			t.Fatalf("configuration mismatch error = %v", err)
		}
		unchanged, loadErr := store.Load()
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if unchanged.Resumed || len(unchanged.ResumeHistory) != 0 {
			t.Fatalf("rejected resume mutated checkpoint: %+v", unchanged)
		}
	})

	t.Run("changed tree is explicit evidence", func(t *testing.T) {
		store, state := newTestStore(t, []string{"one"})
		changedTree := strings.Repeat("2", 64)
		if err := state.PrepareResume(store, testSHA, testDigest, changedTree, "", time.Now()); err != nil {
			t.Fatal(err)
		}
		resumed, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		if resumed.InitialTreeID != testTreeID || len(resumed.ResumeTreeIDs) != 1 || resumed.ResumeTreeIDs[0] != changedTree || len(resumed.ResumeHistory) != 1 || resumed.ResumeHistory[0].TreeID != changedTree {
			t.Fatalf("changed checkout tree was not durably identified: %+v", resumed)
		}
	})

	t.Run("invalid tree", func(t *testing.T) {
		store, state := newTestStore(t, []string{"one"})
		if err := state.PrepareResume(store, testSHA, testDigest, "short", "", time.Now()); err == nil || !strings.Contains(err.Error(), "tree ID is invalid") {
			t.Fatalf("invalid tree error = %v", err)
		}
	})
}

func TestTerminalLifecycleSaveFailuresLeaveMemoryAndDiskUnchanged(t *testing.T) {
	t.Run("prepare resume", func(t *testing.T) {
		store, state := newTestStore(t, []string{"one"})
		stage := "one"
		started := time.Unix(1_700_000_100, 0).UTC()
		state.CurrentStage = &stage
		state.Attempts = []Attempt{{Stage: stage, Attempt: 1, StartedAt: started}}
		state.UpdatedAt = started
		if err := store.Save(state); err != nil {
			t.Fatal(err)
		}

		badStore := store
		badStore.Path = t.TempDir()
		if err := state.PrepareResume(badStore, testSHA, testDigest, testTreeID, stage, started.Add(time.Minute)); err == nil {
			t.Fatal("PrepareResume unexpectedly persisted to a directory path")
		}
		assertUnpreparedResumeState(t, state, stage, started)
		persisted, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		assertUnpreparedResumeState(t, persisted, stage, started)
	})

	t.Run("mark complete", func(t *testing.T) {
		store, state := newTestStore(t, []string{"one"})
		started := time.Unix(1_700_000_200, 0).UTC()
		finished := started.Add(time.Second)
		exitCode := 0
		state.Completed = []string{"one"}
		state.Attempts = []Attempt{{
			Stage: "one", Attempt: 1, StartedAt: started, FinishedAt: &finished, ExitCode: &exitCode,
		}}
		state.FailureCategory = FailureAssertion
		state.FailureMessage = "prior diagnostic"
		state.UpdatedAt = finished
		if err := store.Save(state); err != nil {
			t.Fatal(err)
		}

		badStore := store
		badStore.Path = t.TempDir()
		if err := state.MarkComplete(badStore, finished.Add(time.Minute)); err == nil {
			t.Fatal("MarkComplete unexpectedly persisted to a directory path")
		}
		assertUncompletedState(t, state, finished)
		persisted, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		assertUncompletedState(t, persisted, finished)
	})

	t.Run("mark cleaned after failure", func(t *testing.T) {
		store, state := newTestStore(t, []string{"one"})
		started := time.Unix(1_700_000_300, 0).UTC()
		finished := started.Add(time.Second)
		exitCode := 1
		stage := "one"
		state.Status = StatusFailed
		state.CurrentStage = &stage
		state.Attempts = []Attempt{{
			Stage: stage, Attempt: 1, StartedAt: started, FinishedAt: &finished, ExitCode: &exitCode,
			FailureCategory: FailureAssertion, FailureMessage: "injected failure",
		}}
		state.FailureCategory = FailureAssertion
		state.FailureMessage = "injected failure"
		state.UpdatedAt = finished
		if err := store.Save(state); err != nil {
			t.Fatal(err)
		}

		badStore := store
		badStore.Path = t.TempDir()
		if err := state.MarkCleanedAfterFailure(badStore, finished.Add(time.Minute)); err == nil {
			t.Fatal("MarkCleanedAfterFailure unexpectedly persisted to a directory path")
		}
		assertUncleanedFailedState(t, state, stage, finished)
		persisted, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		assertUncleanedFailedState(t, persisted, stage, finished)
	})
}

func assertUnpreparedResumeState(t *testing.T, state Checkpoint, stage string, updated time.Time) {
	t.Helper()
	if state.Resumed || len(state.ResumeTreeIDs) != 0 || len(state.ResumeHistory) != 0 || state.Status != StatusRunning ||
		state.CurrentStage == nil || *state.CurrentStage != stage || state.UpdatedAt != updated || len(state.Attempts) != 1 ||
		state.Attempts[0].FinishedAt != nil || state.Attempts[0].ExitCode != nil || state.Attempts[0].FailureCategory != FailureNone ||
		state.Attempts[0].FailureMessage != "" {
		t.Fatalf("failed PrepareResume changed checkpoint: %+v", state)
	}
}

func assertUncompletedState(t *testing.T, state Checkpoint, updated time.Time) {
	t.Helper()
	if state.Status != StatusRunning || state.FailureCategory != FailureAssertion || state.FailureMessage != "prior diagnostic" || state.UpdatedAt != updated {
		t.Fatalf("failed MarkComplete changed checkpoint: %+v", state)
	}
}

func assertUncleanedFailedState(t *testing.T, state Checkpoint, stage string, updated time.Time) {
	t.Helper()
	if state.Status != StatusFailed || state.CurrentStage == nil || *state.CurrentStage != stage || state.FailureCategory != FailureAssertion ||
		state.FailureMessage != "injected failure" || state.UpdatedAt != updated {
		t.Fatalf("failed MarkCleanedAfterFailure changed checkpoint: %+v", state)
	}
}

func TestCheckpointLockContentionPreservesOwner(t *testing.T) {
	store, _ := newTestStore(t, []string{"one"})
	lock, err := store.Acquire(false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Acquire(true); err == nil {
		t.Fatal("live lock contention was accepted")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointLockIgnoresInterruptedUnpublishedCandidate(t *testing.T) {
	store, _ := newTestStore(t, []string{"one"})
	lockPath := store.Path + ".lock"
	candidate, err := writeJSONTemp(lockPath, checkpointTestLockOwner(t, store, strings.Repeat("a", 48)))
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(candidate)

	lock, err := store.Acquire(false)
	if err != nil {
		t.Fatalf("Acquire after interrupted pre-publication = %v", err)
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(candidate); err != nil {
		t.Fatalf("unpublished candidate was unexpectedly consumed: %v", err)
	}
}

func TestCheckpointLockRecoversInterruptedPublishedOwner(t *testing.T) {
	store, _ := newTestStore(t, []string{"one"})
	lockPath := store.Path + ".lock"
	stale := checkpointTestLockOwner(t, store, strings.Repeat("b", 48))
	if err := writeJSONNew(lockPath, stale); err != nil {
		t.Fatal(err)
	}

	lock, err := store.Acquire(true)
	if err != nil {
		t.Fatalf("Acquire stale atomically published owner = %v", err)
	}
	if sameLockOwner(lock.owner, stale) {
		t.Fatal("stale checkpoint owner was not replaced")
	}
	if err := lock.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckpointLockRecoversInterruptedStaleReplacement(t *testing.T) {
	tests := []struct {
		name    string
		prepare func(t *testing.T, path, recoveryPath string, stale lockOwner)
	}{
		{
			name: "claim published before stale owner removal",
			prepare: func(t *testing.T, path, recoveryPath string, stale lockOwner) {
				if err := writeJSONNew(path, stale); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(path, recoveryPath); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "stale owner removed before replacement publication",
			prepare: func(t *testing.T, path, recoveryPath string, stale lockOwner) {
				if err := writeJSONNew(path, stale); err != nil {
					t.Fatal(err)
				}
				if err := os.Link(path, recoveryPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, _ := newTestStore(t, []string{"one"})
			path := store.Path + ".lock"
			recoveryPath := path + ".recovery"
			stale := checkpointTestLockOwner(t, store, strings.Repeat("c", 48))
			test.prepare(t, path, recoveryPath, stale)

			lock, err := store.Acquire(true)
			if err != nil {
				t.Fatalf("Acquire interrupted stale replacement = %v", err)
			}
			if _, err := os.Stat(recoveryPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("recovery claim remains after acquisition: %v", err)
			}
			if err := lock.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCheckpointLockRetiresCompletedRecoveryWithoutStealingLiveReplacement(t *testing.T) {
	store, _ := newTestStore(t, []string{"one"})
	path := store.Path + ".lock"
	recoveryPath := path + ".recovery"
	stale := checkpointTestLockOwner(t, store, strings.Repeat("d", 48))
	if err := writeJSONNew(recoveryPath, stale); err != nil {
		t.Fatal(err)
	}
	live := lockOwner{
		Host: stale.Host, PID: os.Getpid(), Token: strings.Repeat("e", 48), CreatedAt: store.now().Add(time.Second),
	}
	if err := writeJSONNew(path, live); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Acquire(true); err == nil || !strings.Contains(err.Error(), "live, remote, or unverifiable") {
		t.Fatalf("Acquire with live replacement error = %v", err)
	}
	var current lockOwner
	if err := readJSON(path, &current); err != nil {
		t.Fatal(err)
	}
	if !sameLockOwner(current, live) {
		t.Fatalf("live replacement changed during recovery: got %+v want %+v", current, live)
	}
	if _, err := os.Stat(recoveryPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed stale recovery claim remains: %v", err)
	}
}

func TestCheckpointLockMalformedPublishedOwnerFailsClosed(t *testing.T) {
	store, _ := newTestStore(t, []string{"one"})
	path := store.Path + ".lock"
	malformed := []byte("{\n")
	if err := os.WriteFile(path, malformed, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := store.Acquire(true); err == nil || !strings.Contains(err.Error(), "unverifiable") {
		t.Fatalf("Acquire malformed lock error = %v", err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(current) != string(malformed) {
		t.Fatalf("malformed fail-closed lock was replaced: %q", current)
	}
}

func checkpointTestLockOwner(t *testing.T, store Store, token string) lockOwner {
	t.Helper()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	return lockOwner{Host: host, PID: 1 << 30, Token: token, CreatedAt: store.now()}
}

func TestTransactionAndTemporaryServiceEvidenceIsDurable(t *testing.T) {
	store, state := newTestStore(t, []string{"one"})
	now := time.Now()
	if err := state.RecordTransaction(store, "deposit-0", "0xabc", now); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordTemporaryService(store, "fresh-el", testUUID, now); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Transactions["deposit-0"] != "0xabc" || loaded.TemporaryServices["fresh-el"] != testUUID {
		t.Fatalf("durable evidence missing: %+v", loaded)
	}
}

func TestTemporaryServiceCreationIntentIsDurableAndCopyOnWrite(t *testing.T) {
	store, state := newTestStore(t, []string{"one"})
	now := time.Now().UTC()
	intent := TemporaryServiceCreationIntent{
		Name: "fresh-el", EnclaveUUID: testUUID,
		ConfigDigest: strings.Repeat("a", 64), Marker: strings.Repeat("b", 64), PreparedAt: now,
	}
	if err := state.RecordTemporaryServiceCreationIntent(store, intent, now); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TemporaryServiceCreationIntents[intent.Name] != intent {
		t.Fatalf("durable creation intent = %+v", loaded.TemporaryServiceCreationIntents)
	}
	if err := state.RecordTemporaryServiceCreationIntent(store, intent, now.Add(time.Second)); err != nil {
		t.Fatalf("idempotent creation intent write: %v", err)
	}
	changed := intent
	changed.Marker = strings.Repeat("c", 64)
	if err := state.RecordTemporaryServiceCreationIntent(store, changed, now.Add(time.Second)); err == nil || !strings.Contains(err.Error(), "different creation intent") {
		t.Fatalf("changed creation intent error = %v", err)
	}

	second := intent
	second.Name = "fresh-cl"
	second.Marker = strings.Repeat("d", 64)
	badStore := store
	badStore.Path = t.TempDir()
	if err := state.RecordTemporaryServiceCreationIntent(badStore, second, now.Add(time.Second)); err == nil {
		t.Fatal("creation intent unexpectedly persisted to a directory path")
	}
	if _, exists := state.TemporaryServiceCreationIntents[second.Name]; exists {
		t.Fatalf("failed save mutated in-memory checkpoint: %+v", state.TemporaryServiceCreationIntents)
	}
	persisted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if persisted.TemporaryServiceCreationIntents[intent.Name] != intent || len(persisted.TemporaryServiceCreationIntents) != 1 {
		t.Fatalf("failed save mutated durable checkpoint: %+v", persisted.TemporaryServiceCreationIntents)
	}
}

func TestPreparedTransactionEvidenceIsDurableAndHashBound(t *testing.T) {
	store, state := newTestStore(t, []string{"one"})
	now := time.Now().UTC()
	tx := testSignedTransaction(t, 7)
	raw, err := tx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	label := "el1/goabi/01-event-emitter-deploy"
	encoded := hexutil.Encode(raw)

	if err := state.RecordPreparedTransaction(store, label, tx.Hash().Hex(), encoded, now); err != nil {
		t.Fatalf("RecordPreparedTransaction() error = %v", err)
	}
	// The identical journal write is idempotent, which is important when a
	// caller loses only the acknowledgement of the checkpoint write.
	if err := state.RecordPreparedTransaction(store, label, tx.Hash().Hex(), encoded, now.Add(time.Second)); err != nil {
		t.Fatalf("idempotent RecordPreparedTransaction() error = %v", err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.PreparedTransactions[label]; got.Hash != tx.Hash().Hex() || got.Raw != encoded {
		t.Fatalf("prepared transaction = %+v, want hash=%s raw=%s", got, tx.Hash(), encoded)
	}

	other := testSignedTransaction(t, 8)
	otherRaw, err := other.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordPreparedTransaction(store, label+"/fresh-mismatch", tx.Hash().Hex(), hexutil.Encode(otherRaw), now); err == nil {
		t.Fatal("first prepared write with a mismatched raw/hash pair was accepted")
	}
	if err := state.RecordPreparedTransaction(store, label+"/malformed", tx.Hash().Hex(), "0x01", now); err == nil {
		t.Fatal("first prepared write with malformed transaction bytes was accepted")
	}
	unsignedTo := common.Address{2}
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID: big.NewInt(1337), Nonce: 9, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
		Gas: 100_000, To: &unsignedTo, Value: big.NewInt(3),
	})
	unsignedRaw, err := unsigned.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if err := state.RecordPreparedTransaction(store, label+"/unsigned", unsigned.Hash().Hex(), hexutil.Encode(unsignedRaw), now); err == nil {
		t.Fatal("first prepared write with an unsigned transaction was accepted")
	}
	if err := state.RecordPreparedTransaction(store, label, tx.Hash().Hex(), hexutil.Encode(otherRaw), now); err == nil {
		t.Fatal("prepared transaction with a mismatched raw/hash pair was accepted")
	}
	if err := state.RecordTransaction(store, label, other.Hash().Hex(), now); err == nil {
		t.Fatal("submitted hash different from the prepared hash was accepted")
	}
	if err := state.RecordTransaction(store, label, tx.Hash().Hex(), now); err != nil {
		t.Fatalf("record matching submitted transaction: %v", err)
	}
	loaded, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Transactions[label] != tx.Hash().Hex() {
		t.Fatalf("submitted transaction = %q, want %q", loaded.Transactions[label], tx.Hash().Hex())
	}
	if _, exists := loaded.PreparedTransactions[label+"/fresh-mismatch"]; exists {
		t.Fatal("rejected first-write raw/hash mismatch reached the checkpoint")
	}
	if _, exists := loaded.PreparedTransactions[label+"/malformed"]; exists {
		t.Fatal("rejected malformed raw transaction reached the checkpoint")
	}
	if _, exists := loaded.PreparedTransactions[label+"/unsigned"]; exists {
		t.Fatal("rejected unsigned raw transaction reached the checkpoint")
	}
}

func TestMutationEvidenceSaveFailureLeavesMemoryAndDiskUnchanged(t *testing.T) {
	prepared := testSignedTransaction(t, 11)
	raw, err := prepared.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		record func(*Checkpoint, Store) error
		check  func(*testing.T, Checkpoint)
	}{
		{
			name: "prepared transaction",
			record: func(state *Checkpoint, store Store) error {
				return state.RecordPreparedTransaction(store, "goabi/01-event-emitter-deploy", prepared.Hash().Hex(), hexutil.Encode(raw), time.Now())
			},
			check: func(t *testing.T, state Checkpoint) {
				if len(state.PreparedTransactions) != 0 {
					t.Fatalf("failed save changed prepared transactions: %+v", state.PreparedTransactions)
				}
			},
		},
		{
			name: "submitted transaction",
			record: func(state *Checkpoint, store Store) error {
				return state.RecordTransaction(store, "deposit-0", prepared.Hash().Hex(), time.Now())
			},
			check: func(t *testing.T, state Checkpoint) {
				if len(state.Transactions) != 0 {
					t.Fatalf("failed save changed transactions: %+v", state.Transactions)
				}
			},
		},
		{
			name: "managed transaction intent",
			record: func(state *Checkpoint, store Store) error {
				label := "base/01-managed-transfer-el1"
				preparedAt := time.Now().UTC().Add(-time.Second)
				return state.RecordManagedTransactionIntent(store, label, ManagedTransactionIntent{
					Phase: "base", Label: label, Origin: 0, OriginServiceName: "el-1", OriginServiceUUID: testUUID,
					ChainID: "0x539", From: (common.Address{}).Hex(), To: (common.Address{1}).Hex(), Value: "0x1", Input: "0x",
					AccessList: nil, Nonce: 3, StartBlock: 9, StartBlockHash: (common.Hash{1}).Hex(), PreparedAt: preparedAt,
				}, time.Now())
			},
			check: func(t *testing.T, state Checkpoint) {
				if len(state.ManagedTransactionIntents) != 0 {
					t.Fatalf("failed save changed managed intents: %+v", state.ManagedTransactionIntents)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, state := newTestStore(t, []string{"one"})
			badStore := store
			badStore.Path = t.TempDir() // Rename cannot replace this directory.
			if err := test.record(&state, badStore); err == nil {
				t.Fatal("evidence save unexpectedly succeeded")
			}
			test.check(t, state)
			persisted, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, persisted)
		})
	}
}

func TestManagedTransactionIntentAndOneShotAttemptMarkersAreDurable(t *testing.T) {
	store, state := newTestStore(t, []string{"one"})
	preparedAt := time.Now().UTC().Add(-3 * time.Second)
	label := "base/01-managed-transfer-el1"
	intent := ManagedTransactionIntent{
		Phase: "base", Label: label, Origin: 0, OriginServiceName: "el-1", OriginServiceUUID: testUUID,
		ChainID: "0x539", From: (common.Address{1}).Hex(), To: (common.Address{2}).Hex(), Value: "0x0", Input: "0x0102",
		AccessList: []ManagedAccessTuple{{Address: (common.Address{3}).Hex(), StorageKeys: []string{(common.Hash{4}).Hex()}}},
		Nonce:      7, StartBlock: 11, StartBlockHash: (common.Hash{5}).Hex(), PreparedAt: preparedAt,
	}
	if err := state.RecordManagedTransactionIntent(store, label, intent, preparedAt); err != nil {
		t.Fatal(err)
	}
	// Prove the checkpoint owns an immutable deep copy of nested access-list data.
	intent.AccessList[0].StorageKeys[0] = (common.Hash{9}).Hex()
	if got := state.ManagedTransactionIntents[label].AccessList[0].StorageKeys[0]; got != (common.Hash{4}).Hex() {
		t.Fatalf("checkpoint intent aliased caller memory: %s", got)
	}
	initialAt := preparedAt.Add(time.Second)
	if err := state.RecordManagedTransactionInitialAttempt(store, label, initialAt); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordManagedTransactionInitialAttempt(store, label, initialAt.Add(time.Second)); err == nil {
		t.Fatal("duplicate managed initial attempt was accepted")
	}
	recoveryAt := initialAt.Add(time.Second)
	if err := state.RecordManagedTransactionResubmit(store, label, recoveryAt); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordManagedTransactionResubmit(store, label, recoveryAt.Add(time.Second)); err == nil {
		t.Fatal("second managed recovery replay was accepted")
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ManagedTransactionInitialAttempts[label] != initialAt || loaded.ManagedTransactionResubmits[label] != recoveryAt {
		t.Fatalf("managed attempt markers were not durable: initial=%v recovery=%v", loaded.ManagedTransactionInitialAttempts, loaded.ManagedTransactionResubmits)
	}
}

func TestManagedTransactionAttemptMarkerSaveFailureIsCopyOnWrite(t *testing.T) {
	store, state := newTestStore(t, []string{"one"})
	preparedAt := time.Now().UTC().Add(-time.Second)
	label := "base/01-managed-transfer-el1"
	intent := ManagedTransactionIntent{
		Phase: "base", Label: label, Origin: 0, OriginServiceName: "el-1", OriginServiceUUID: testUUID,
		ChainID: "0x1", From: (common.Address{1}).Hex(), To: (common.Address{2}).Hex(), Value: "0x0", Input: "0x",
		AccessList: []ManagedAccessTuple{}, Nonce: 1, StartBlock: 2, StartBlockHash: (common.Hash{3}).Hex(), PreparedAt: preparedAt,
	}
	if err := state.RecordManagedTransactionIntent(store, label, intent, preparedAt); err != nil {
		t.Fatal(err)
	}
	badStore := store
	badStore.Path = t.TempDir()
	if err := state.RecordManagedTransactionInitialAttempt(badStore, label, preparedAt.Add(time.Second)); err == nil {
		t.Fatal("failed initial-attempt save unexpectedly succeeded")
	}
	if len(state.ManagedTransactionInitialAttempts) != 0 {
		t.Fatalf("failed initial marker save changed memory: %v", state.ManagedTransactionInitialAttempts)
	}
	if err := state.RecordManagedTransactionInitialAttempt(store, label, preparedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordManagedTransactionResubmit(badStore, label, preparedAt.Add(2*time.Second)); err == nil {
		t.Fatal("failed resubmit save unexpectedly succeeded")
	}
	if len(state.ManagedTransactionResubmits) != 0 {
		t.Fatalf("failed resubmit marker save changed memory: %v", state.ManagedTransactionResubmits)
	}
}

func TestSystemObservationIsImmutableDurableAndCopyOnWrite(t *testing.T) {
	store, state := newTestStore(t, []string{"one"})
	label := "system-signer/signer-restart/account-baseline"
	value := `{"version":1,"el":[{"head":1},{"head":1}]}`
	badStore := store
	badStore.Path = t.TempDir()
	if err := state.RecordSystemObservation(badStore, label, value, time.Now().UTC()); err == nil {
		t.Fatal("failed system observation save unexpectedly succeeded")
	}
	if len(state.SystemObservations) != 0 {
		t.Fatalf("failed save changed in-memory observations: %v", state.SystemObservations)
	}
	if err := state.RecordSystemObservation(store, label, value, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := state.RecordSystemObservation(store, label, `{"version":2}`, time.Now().UTC()); err == nil {
		t.Fatal("immutable system observation accepted different evidence")
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.SystemObservations[label] != value {
		t.Fatalf("durable observation = %q, want %q", loaded.SystemObservations[label], value)
	}
}

func testSignedTransaction(t *testing.T, nonce uint64) *types.Transaction {
	t.Helper()
	w, err := wallet.RestoreFromSeedHex("010000a7b1a3005d9e110009c48d45deb43f0a0e31846ed2c5aaefb6d4238040ad4c08794ffe65585c13eb6948c2faf6db90c2")
	if err != nil {
		t.Fatal(err)
	}
	to := common.Address{1}
	chainID := big.NewInt(1337)
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
		Gas: 100_000, To: &to, Value: big.NewInt(3), Data: []byte{0x64},
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), w)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func newTestStore(t *testing.T, stages []string) (Store, Checkpoint) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	now := time.Unix(1_700_000_000, 0).UTC()
	state := NewCheckpoint("run-1", testSHA, testDigest, "/tmp/dump", testTreeID, EnclaveRef{Name: "vm64", UUID: testUUID, Owned: true}, now)
	store := Store{Path: path, StageOrder: stages, Now: func() time.Time { return now }}
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	return store, state
}
