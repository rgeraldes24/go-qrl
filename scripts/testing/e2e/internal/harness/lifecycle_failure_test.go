// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/config"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/provision"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
)

const (
	harnessLifecycleSHA    = "abababababababababababababababababababab"
	harnessLifecycleDigest = "cdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcdcd"
	harnessLifecycleTreeID = "efefefefefefefefefefefefefefefefefefefefefefefefefefefefefefefef"
)

type creationDeadlineClient struct {
	*kurtosis.FakeClient
	deadline time.Time
}

func (client *creationDeadlineClient) CreateEnclave(ctx context.Context, name string) (lifecycle.EnclaveRef, error) {
	client.deadline, _ = ctx.Deadline()
	return client.FakeClient.CreateEnclave(ctx, name)
}

func TestRunOwnedNameCollisionPreservesUncapturedIntent(t *testing.T) {
	fake := kurtosis.NewFakeClient()
	if _, err := fake.CreateEnclave(t.Context(), "existing-vm64"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	runConfig := newOwnedHarnessConfig(t, now, "existing-vm64")

	outcome, err := RunOwned(t.Context(), "run-collision", runConfig, Dependencies{
		Client: fake,
		Now:    func() time.Time { return now },
	})
	if err == nil || !strings.Contains(err.Error(), "name collision") {
		t.Fatalf("RunOwned error = %v, want name collision", err)
	}
	record, loadErr := lifecycle.LoadOwnership(outcome.Ownership)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if record.UUID != nil || !record.Preserved || !strings.Contains(record.PreserveReason, "ambiguous") {
		t.Fatalf("collision ownership = %+v", record)
	}
	if hasCallPrefix(fake.Calls, "destroy:") {
		t.Fatalf("name collision triggered cleanup: %v", fake.Calls)
	}
}

func TestRunOwnedCaptureFailurePreservesCreatedEnclave(t *testing.T) {
	fake := kurtosis.NewFakeClient()
	now := time.Now().UTC()
	runConfig := newOwnedHarnessConfig(t, now, "capture-failure-vm64")
	captureFailure := errors.New("injected ownership persistence failure")
	captureCalled := false

	outcome, err := RunOwned(t.Context(), "run-capture-failure", runConfig, Dependencies{
		Client: fake,
		Now:    func() time.Time { return now },
		CaptureOwnershipUUID: func(path, name, uuid string) (lifecycle.OwnershipRecord, error) {
			captureCalled = true
			if path == "" || name != runConfig.EnclaveName || uuid != "00000000000000000000000000000001" {
				return lifecycle.OwnershipRecord{}, errors.New("capture hook received the wrong ownership identity")
			}
			return lifecycle.OwnershipRecord{}, captureFailure
		},
	})
	if !captureCalled || !errors.Is(err, captureFailure) {
		t.Fatalf("capture called = %t, RunOwned error = %v", captureCalled, err)
	}
	record, loadErr := lifecycle.LoadOwnership(outcome.Ownership)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if record.UUID != nil || !record.Preserved || !strings.Contains(record.PreserveReason, "refusing automatic cleanup") {
		t.Fatalf("capture-failure ownership = %+v", record)
	}
	if _, getErr := fake.GetEnclave(t.Context(), "00000000000000000000000000000001"); getErr != nil {
		t.Fatalf("created enclave was not preserved: %v", getErr)
	}
	if hasCallPrefix(fake.Calls, "destroy:") {
		t.Fatalf("uncaptured enclave triggered cleanup: %v", fake.Calls)
	}
}

func TestRunOwnedBoundsEnclaveCreationBeforeCleanupReserve(t *testing.T) {
	now := time.Now().UTC()
	createFailure := errors.New("injected bounded create failure")
	fake := kurtosis.NewFakeClient()
	fake.CreateError = createFailure
	client := &creationDeadlineClient{FakeClient: fake}
	runConfig := newOwnedHarnessConfig(t, now, "bounded-create-vm64")
	runConfig.GlobalDeadline = now.Add(2 * time.Hour)
	runConfig.CleanupReserve = 30 * time.Minute

	_, err := RunOwned(t.Context(), "run-bounded-create", runConfig, Dependencies{
		Client: client,
		Now:    func() time.Time { return now },
	})
	if !errors.Is(err, createFailure) {
		t.Fatalf("RunOwned create error = %v", err)
	}
	if want := now.Add(enclaveCreationTimeout); !client.deadline.Equal(want) {
		t.Fatalf("create deadline = %s, want %s", client.deadline, want)
	}
}

func TestPackageFailureIsCheckpointedWithoutOutputArtifact(t *testing.T) {
	fake := kurtosis.NewFakeClient()
	enclave, err := fake.CreateEnclave(t.Context(), "package-failure-vm64")
	if err != nil {
		t.Fatal(err)
	}
	packageFailure := errors.New("injected Kurtosis package failure")
	fake.PackageError = packageFailure
	root := t.TempDir()
	writer, err := report.New(root)
	if err != nil {
		t.Fatal(err)
	}
	params := []byte("participants: []\n")
	paramsPath := filepath.Join(root, "effective.yaml")
	if err := os.WriteFile(paramsPath, params, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(params)
	preparation := &provision.Preparation{
		QRLPackage: provision.Source{Repository: "github.com/theqrl/qrl-package", Revision: harnessLifecycleSHA},
		EffectiveParams: provision.EffectiveParams{
			Path: paramsPath, SHA256: hex.EncodeToString(digest[:]),
		},
	}
	now := time.Now().UTC()
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: []string{"network-package"}, Now: time.Now}
	state := lifecycle.NewCheckpoint("run-package-failure", harnessLifecycleSHA, harnessLifecycleDigest, root, harnessLifecycleTreeID, enclave, now)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		RunID: "run-package-failure", Enclave: enclave, Writer: writer, Store: store, Preparation: preparation,
		Dependencies: Dependencies{Client: fake, Now: time.Now},
	}
	packageStage := stage("network-package", time.Minute, time.Millisecond, lifecycle.InspectBeforeRetry, true, runtime.packageStage, runtime.packageReconcile)
	runner := lifecycle.Runner{
		Store: store, Stages: []lifecycle.Stage{packageStage}, GlobalDeadline: now.Add(time.Hour),
		CleanupReserve: time.Minute, AllowDisruptive: true, Now: time.Now, Classify: classifyFailure,
	}
	err = runner.Run(t.Context(), &lifecycle.RunEnvironment{Enclave: enclave})
	if !errors.Is(err, packageFailure) {
		t.Fatalf("package stage error = %v", err)
	}
	failed, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if failed.Status != lifecycle.StatusFailed || failed.FailureCategory != lifecycle.FailureSDK || failed.CurrentStage == nil || *failed.CurrentStage != "network-package" {
		t.Fatalf("package failure checkpoint = %+v", failed)
	}
	if runtime.PackageResult != nil {
		t.Fatalf("failed package unexpectedly produced a result: %+v", runtime.PackageResult)
	}
	if _, err := os.Stat(filepath.Join(writer.Layout().Kurtosis, "package-output.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed package output artifact exists or stat failed unexpectedly: %v", err)
	}
	intent, err := runtime.loadPackageInvocationIntent(kurtosis.PackageRun{
		Locator: preparation.PackageLocator(), SerializedParams: string(params),
	})
	if err != nil {
		t.Fatalf("package intent was not durable before the failed external call: %v", err)
	}
	if intent.ParamsSHA256 != hex.EncodeToString(digest[:]) || intent.Enclave.UUID != enclave.UUID {
		t.Fatalf("durable package intent = %+v", intent)
	}
	last, err := fake.LastPackageInvocation(t.Context(), enclave)
	if err != nil {
		t.Fatal(err)
	}
	if last.Locator != intent.Locator || last.SerializedParams != string(params) {
		t.Fatalf("Kurtosis retained invocation = %+v, intent = %+v", last, intent)
	}
}

func TestResumeRejectsConfigurationAndEnclaveUUIDMismatch(t *testing.T) {
	t.Run("configuration digest", func(t *testing.T) {
		fixture := newOwnedResumeFixture(t, "")
		writer, err := report.New(filepath.Dir(fixture.options.CheckpointPath))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.WriteManifest(report.ManifestMetadata{RunID: "run-resume", GeneratedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
		state, err := fixture.store.Load()
		if err != nil {
			t.Fatal(err)
		}
		state.ConfigurationDigest = strings.Repeat("1", 64)
		if err := fixture.store.Save(state); err != nil {
			t.Fatal(err)
		}
		_, err = Resume(t.Context(), fixture.options, fixture.dependencies)
		if err == nil || !strings.Contains(err.Error(), "does not match requested configuration") {
			t.Fatalf("resume configuration mismatch error = %v", err)
		}
		if _, statErr := os.Stat(writer.Layout().Manifest); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("failed resume retained stale manifest: %v", statErr)
		}
	})

	t.Run("enclave UUID", func(t *testing.T) {
		fixture := newOwnedResumeFixture(t, strings.Repeat("f", 32))
		_, err := Resume(t.Context(), fixture.options, fixture.dependencies)
		if err == nil || !strings.Contains(err.Error(), "identity is unavailable or changed") {
			t.Fatalf("resume UUID mismatch error = %v", err)
		}
		if hasCallPrefix(fixture.fake.Calls, "destroy:") {
			t.Fatalf("resume UUID mismatch triggered cleanup: %v", fixture.fake.Calls)
		}
	})
}

func TestResumeCompletesAfterCleanupWasDurableButCheckpointCompletionWasInterrupted(t *testing.T) {
	fixture := newOwnedResumeFixture(t, "")
	state, err := fixture.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	started := state.CreatedAt.Add(time.Minute)
	for index, name := range fixture.store.StageOrder {
		finished := started.Add(time.Second)
		exitCode := 0
		state.Attempts = append(state.Attempts, lifecycle.Attempt{
			Stage: name, Attempt: 1, StartedAt: started,
			FinishedAt: &finished, ExitCode: &exitCode,
		})
		state.Completed = append(state.Completed, name)
		state.UpdatedAt = finished
		started = finished.Add(time.Duration(index+1) * time.Millisecond)
	}
	if err := fixture.store.Save(state); err != nil {
		t.Fatal(err)
	}
	if err := fixture.fake.DestroyEnclave(t.Context(), state.Enclave); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.MarkOwnershipDestroyed(
		filepath.Join(filepath.Dir(fixture.options.CheckpointPath), "ownership.json"),
		state.Enclave.UUID,
		time.Now(),
	); err != nil {
		t.Fatal(err)
	}

	outcome, err := Resume(t.Context(), fixture.options, fixture.dependencies)
	if err != nil {
		t.Fatalf("resume after durable cleanup: %v", err)
	}
	if outcome.Status != lifecycle.StatusCompleteAfterResume {
		t.Fatalf("resume outcome = %+v", outcome)
	}
	completed, err := fixture.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != lifecycle.StatusCompleteAfterResume || !completed.Resumed {
		t.Fatalf("completed checkpoint = %+v", completed)
	}
	if hasCallPrefix(fixture.fake.Calls, "get:"+state.Enclave.UUID) {
		t.Fatalf("resume queried an enclave already durably recorded as destroyed: %v", fixture.fake.Calls)
	}
}

func TestResumeRejectsCleanupRequestBeforeAllStagesCompleted(t *testing.T) {
	fixture := newOwnedResumeFixture(t, "")
	state, err := fixture.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	ownershipPath := filepath.Join(filepath.Dir(fixture.options.CheckpointPath), "ownership.json")
	if err := lifecycle.MarkOwnershipDestroyRequested(ownershipPath, state.Enclave.UUID, state.CreatedAt.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	_, err = Resume(t.Context(), fixture.options, fixture.dependencies)
	if err == nil || !strings.Contains(err.Error(), "cleanup in progress before every stage completed") {
		t.Fatalf("incomplete checkpoint with cleanup request error = %v", err)
	}
	if hasCallPrefix(fixture.fake.Calls, "destroy:") || hasCallPrefix(fixture.fake.Calls, "get:"+state.Enclave.UUID) {
		t.Fatalf("unsafe external call after inconsistent cleanup request: %v", fixture.fake.Calls)
	}
}

func TestResumeCompletesAfterDestroyResponseAndOwnershipMarkerWereInterrupted(t *testing.T) {
	fixture := newOwnedResumeFixture(t, "")
	state, err := fixture.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	started := state.CreatedAt.Add(time.Minute)
	for index, name := range fixture.store.StageOrder {
		finished := started.Add(time.Second)
		exitCode := 0
		state.Attempts = append(state.Attempts, lifecycle.Attempt{
			Stage: name, Attempt: 1, StartedAt: started,
			FinishedAt: &finished, ExitCode: &exitCode,
		})
		state.Completed = append(state.Completed, name)
		state.UpdatedAt = finished
		started = finished.Add(time.Duration(index+1) * time.Millisecond)
	}
	if err := fixture.store.Save(state); err != nil {
		t.Fatal(err)
	}
	ownershipPath := filepath.Join(filepath.Dir(fixture.options.CheckpointPath), "ownership.json")
	requestedAt := state.UpdatedAt.Add(time.Second)
	fixture.dependencies.Now = func() time.Time { return requestedAt.Add(time.Minute) }
	if err := lifecycle.MarkOwnershipDestroyRequested(ownershipPath, state.Enclave.UUID, requestedAt); err != nil {
		t.Fatal(err)
	}
	if err := fixture.fake.DestroyEnclave(t.Context(), state.Enclave); err != nil {
		t.Fatal(err)
	}

	outcome, err := Resume(t.Context(), fixture.options, fixture.dependencies)
	if err != nil {
		t.Fatalf("resume after destroy response loss: %v", err)
	}
	if outcome.Status != lifecycle.StatusCompleteAfterResume {
		t.Fatalf("resume outcome = %+v", outcome)
	}
	completed, err := fixture.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != lifecycle.StatusCompleteAfterResume || !completed.Resumed {
		t.Fatalf("completed checkpoint = %+v", completed)
	}
	ownership, err := lifecycle.LoadOwnership(ownershipPath)
	if err != nil {
		t.Fatal(err)
	}
	if ownership.DestroyRequestedAt == nil || ownership.DestroyedAt == nil {
		t.Fatalf("cleanup boundary was not completed: %+v", ownership)
	}
	if hasCallPrefix(fixture.fake.Calls, "get:"+state.Enclave.UUID) {
		t.Fatalf("resume queried an enclave after durable destruction intent: %v", fixture.fake.Calls)
	}
	if !hasCallPrefix(fixture.fake.Calls, "exists:"+state.Enclave.UUID) {
		t.Fatalf("resume did not reconcile exact UUID absence: %v", fixture.fake.Calls)
	}
}

func TestLoggedMutationResumeRefusesBlindReplay(t *testing.T) {
	fake := kurtosis.NewFakeClient()
	enclave, err := fake.CreateEnclave(t.Context(), "mutation-resume-vm64")
	if err != nil {
		t.Fatal(err)
	}
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: []string{"system-signer"}, Now: time.Now}
	state := lifecycle.NewCheckpoint("run-mutation", harnessLifecycleSHA, harnessLifecycleDigest, writer.Root(), harnessLifecycleTreeID, enclave, now)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{RunID: "run-mutation", Enclave: enclave, Writer: writer, Store: store, Dependencies: Dependencies{Client: fake, Now: time.Now}}
	runs := 0
	mutationStage := stage(
		"system-signer", time.Minute, time.Millisecond, lifecycle.InspectBeforeRetry, true,
		func(context.Context, *lifecycle.RunEnvironment) error {
			runs++
			return errors.New("restart interrupted after mutation")
		},
		runtime.loggedMutationReconcile("system-signer"),
	)
	runner := lifecycle.Runner{
		Store: store, Stages: []lifecycle.Stage{mutationStage}, GlobalDeadline: now.Add(time.Hour),
		CleanupReserve: time.Minute, AllowDisruptive: true, Now: time.Now,
	}
	environment := &lifecycle.RunEnvironment{Enclave: enclave}
	if err := runner.Run(t.Context(), environment); err == nil {
		t.Fatal("injected state-changing failure passed")
	}
	failed, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if err := failed.PrepareResume(store, harnessLifecycleSHA, harnessLifecycleDigest, strings.Repeat("2", 64), "system-signer", time.Now()); err != nil {
		t.Fatal(err)
	}
	err = runner.Run(t.Context(), environment)
	if err == nil || !strings.Contains(err.Error(), "refusing blind replay") {
		t.Fatalf("resume reconciliation error = %v", err)
	}
	if runs != 1 {
		t.Fatalf("state-changing stage executions = %d, want 1", runs)
	}
	resumed, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(resumed.Attempts) != 1 || len(resumed.Completed) != 0 {
		t.Fatalf("blind replay changed checkpoint evidence: %+v", resumed)
	}
}

func TestBorrowedFailureNeverDestroysEnclave(t *testing.T) {
	fake := kurtosis.NewFakeClient()
	enclave, err := fake.CreateEnclave(t.Context(), "borrowed-failure-vm64")
	if err != nil {
		t.Fatal(err)
	}
	enclave.Owned = false
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	runConfig := config.New(config.ModeTest, now)
	runConfig.SourceSHA = harnessLifecycleSHA
	runConfig.ResultsDir = writer.Root()
	runConfig.EnclaveIdentifier = enclave.UUID
	runConfig.GlobalDeadline = now.Add(time.Hour)
	runConfig.CleanupReserve = time.Minute
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: []string{"readonly-check"}, Now: time.Now}
	digest, err := runConfig.Digest()
	if err != nil {
		t.Fatal(err)
	}
	state := lifecycle.NewCheckpoint("run-borrowed-failure", harnessLifecycleSHA, digest, writer.Root(), harnessLifecycleTreeID, enclave, now)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected borrowed assertion failure")
	runtime := &Runtime{
		RunID: "run-borrowed-failure", Config: runConfig, Enclave: enclave, Writer: writer, Store: store,
		Dependencies: Dependencies{Client: fake, Now: time.Now}, StartedAt: now,
		Timeline: report.NewTimelineRecorder("run-borrowed-failure", time.Now),
	}
	readonlyStage := stage("readonly-check", time.Minute, time.Millisecond, lifecycle.RetrySafe, false, func(context.Context, *lifecycle.RunEnvironment) error {
		return injected
	}, nil)
	outcome, err := executeLocked(t.Context(), runtime, []lifecycle.Stage{readonlyStage}, false)
	if !errors.Is(err, injected) || outcome.Status != lifecycle.StatusFailed {
		t.Fatalf("borrowed outcome = %+v, error = %v", outcome, err)
	}
	if hasCallPrefix(fake.Calls, "destroy:") {
		t.Fatalf("borrowed failure triggered cleanup: %v", fake.Calls)
	}
	if _, err := fake.GetEnclave(t.Context(), enclave.UUID); err != nil {
		t.Fatalf("borrowed enclave was not preserved: %v", err)
	}
}

type ownedResumeFixture struct {
	fake         *kurtosis.FakeClient
	store        lifecycle.Store
	options      ResumeOptions
	dependencies Dependencies
}

func newOwnedResumeFixture(t *testing.T, recordedUUID string) ownedResumeFixture {
	t.Helper()
	fake := kurtosis.NewFakeClient()
	actual, err := fake.CreateEnclave(t.Context(), "resume-vm64")
	if err != nil {
		t.Fatal(err)
	}
	if recordedUUID == "" {
		recordedUUID = actual.UUID
	}
	now := time.Now().UTC()
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "results"))
	if err != nil {
		t.Fatal(err)
	}
	runConfig := config.New(config.ModeRun, now)
	runConfig.SourceSHA = harnessLifecycleSHA
	runConfig.RepoRoot = root
	runConfig.ResultsDir = writer.Root()
	runConfig.CheckpointPath = writer.Layout().Checkpoint
	runConfig.OwnershipPath = writer.Layout().Ownership
	runConfig.EnclaveName = actual.Name
	digest, err := runConfig.Digest()
	if err != nil {
		t.Fatal(err)
	}
	enclave := lifecycle.EnclaveRef{Name: actual.Name, UUID: recordedUUID, Owned: true}
	stages := ownedStages(&Runtime{})
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: stageOrder(stages), Now: time.Now}
	state := lifecycle.NewCheckpoint("run-resume", harnessLifecycleSHA, digest, writer.Root(), harnessLifecycleTreeID, enclave, now)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.NewOwnership(writer.Layout().Ownership, "run-resume", actual.Name, now); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(writer.Layout().Ownership, actual.Name, recordedUUID); err != nil {
		t.Fatal(err)
	}
	effective := EffectiveConfiguration{
		Schema: EffectiveConfigurationSchema, RunID: "run-resume", Config: runConfig,
		TopologySpec: topology.DefaultSpec(""),
	}
	if err := writer.WriteEffectiveConfig(effective); err != nil {
		t.Fatal(err)
	}
	return ownedResumeFixture{
		fake:  fake,
		store: store,
		options: ResumeOptions{
			CheckpointPath: writer.Layout().Checkpoint, SourceSHA: harnessLifecycleSHA,
			RepoRoot: root, GlobalDeadline: now.Add(config.DefaultGlobalRuntime), TreeID: strings.Repeat("3", 64),
		},
		dependencies: Dependencies{Client: fake, Now: time.Now},
	}
}

func newOwnedHarnessConfig(t *testing.T, now time.Time, enclaveName string) config.RunConfig {
	t.Helper()
	root := t.TempDir()
	runConfig := config.New(config.ModeRun, now)
	runConfig.SourceSHA = harnessLifecycleSHA
	runConfig.RepoRoot = root
	runConfig.ResultsDir = filepath.Join(root, "results")
	runConfig.EnclaveName = enclaveName
	return runConfig
}

func hasCallPrefix(calls []string, prefix string) bool {
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}
