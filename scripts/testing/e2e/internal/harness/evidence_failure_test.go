package harness

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/config"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
)

func TestFailureReturnsPrimaryReasonWriteError(t *testing.T) {
	fake := kurtosis.NewFakeClient()
	enclave, err := fake.CreateEnclave(t.Context(), "reason-write-failure")
	if err != nil {
		t.Fatal(err)
	}
	enclave.Owned = false
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(writer.Layout().Reason, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	runConfig := config.New(config.ModeTest, now)
	runConfig.SourceSHA = harnessLifecycleSHA
	runConfig.ResultsDir = writer.Root()
	runConfig.EnclaveIdentifier = enclave.UUID
	runConfig.GlobalDeadline = now.Add(time.Hour)
	runConfig.CleanupReserve = time.Minute
	digest, err := runConfig.Digest()
	if err != nil {
		t.Fatal(err)
	}
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: []string{"assertion"}, Now: time.Now}
	state := lifecycle.NewCheckpoint("run-reason-write", harnessLifecycleSHA, digest, writer.Root(), harnessLifecycleTreeID, enclave, now)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	injected := errors.New("injected assertion failure")
	runtime := &Runtime{
		RunID: "run-reason-write", Config: runConfig, Enclave: enclave,
		Writer: writer, Store: store, Dependencies: Dependencies{Client: fake, Now: time.Now},
		StartedAt: now, Timeline: report.NewTimelineRecorder("run-reason-write", time.Now),
	}
	assertion := stage("assertion", time.Minute, time.Millisecond, lifecycle.RetrySafe, false, func(context.Context, *lifecycle.RunEnvironment) error {
		return injected
	}, nil)

	_, err = executeLocked(t.Context(), runtime, []lifecycle.Stage{assertion}, false)
	if !errors.Is(err, injected) {
		t.Fatalf("failure lost original assertion: %v", err)
	}
	if !strings.Contains(err.Error(), "diagnostics/reason.json") {
		t.Fatalf("failure swallowed reason-write error: %v", err)
	}
}

func TestCleanupContextSurvivesCancellationButKeepsGlobalDeadline(t *testing.T) {
	now := time.Now().UTC()
	globalDeadline := now.Add(25 * time.Minute)
	runtime := &Runtime{Config: config.RunConfig{GlobalDeadline: globalDeadline}}
	parent, cancelParent := context.WithCancel(context.Background())
	cancelParent()

	cleanup, cancelCleanup := runtime.cleanupContext(parent)
	defer cancelCleanup()
	if err := cleanup.Err(); err != nil {
		t.Fatalf("cleanup inherited parent cancellation: %v", err)
	}
	deadline, ok := cleanup.Deadline()
	if !ok || !deadline.Equal(globalDeadline) {
		t.Fatalf("cleanup deadline = %s, %t; want %s", deadline, ok, globalDeadline)
	}
}
