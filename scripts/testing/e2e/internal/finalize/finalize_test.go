package finalize

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
)

const testUUID = "00000000000000000000000000000001"

type fakeDumper struct {
	uuid     string
	err      error
	deadline time.Time
}

func (dumper *fakeDumper) Dump(ctx context.Context, uuid, destination string) ([]byte, error) {
	dumper.uuid = uuid
	dumper.deadline, _ = ctx.Deadline()
	if dumper.err == nil {
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return nil, err
		}
	}
	return []byte("dump output"), dumper.err
}

type deadlineDestroyClient struct {
	*kurtosis.FakeClient
	getDeadline      time.Time
	servicesDeadline time.Time
	logsDeadline     time.Time
	destroyDeadline  time.Time
}

type blockingGetClient struct {
	*kurtosis.FakeClient
	getDeadline     time.Time
	servicesCalls   int
	logsCalls       int
	destroyRef      lifecycle.EnclaveRef
	destroyDeadline time.Time
	destroyCtxErr   error
}

func (client *blockingGetClient) GetEnclave(ctx context.Context, _ string) (lifecycle.EnclaveRef, error) {
	client.getDeadline, _ = ctx.Deadline()
	<-ctx.Done()
	return lifecycle.EnclaveRef{}, ctx.Err()
}

func (client *blockingGetClient) Services(context.Context, lifecycle.EnclaveRef) ([]kurtosis.Service, error) {
	client.servicesCalls++
	return nil, errors.New("services must not run after the diagnostic cutoff")
}

func (client *blockingGetClient) ServiceLogs(context.Context, lifecycle.EnclaveRef, []string) (map[string][]byte, error) {
	client.logsCalls++
	return nil, errors.New("service logs must not run after the diagnostic cutoff")
}

func (client *blockingGetClient) DestroyEnclave(ctx context.Context, ref lifecycle.EnclaveRef) error {
	client.destroyRef = ref
	client.destroyDeadline, _ = ctx.Deadline()
	client.destroyCtxErr = ctx.Err()
	return client.FakeClient.DestroyEnclave(ctx, ref)
}

type mismatchClient struct {
	*kurtosis.FakeClient
	destroyCalls int
}

func TestRunInvalidatesExistingManifestBeforeEarlyFailure(t *testing.T) {
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteManifest(report.ManifestMetadata{RunID: "stale-run", GeneratedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	_, err = Run(t.Context(), Options{
		OwnershipPath: filepath.Join(writer.Root(), "missing-ownership.json"),
		Writer:        writer, Client: kurtosis.NewFakeClient(),
	})
	if err == nil || !strings.Contains(err.Error(), "load ownership record") {
		t.Fatalf("early finalizer error = %v", err)
	}
	if _, statErr := os.Stat(writer.Layout().Manifest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("early finalizer failure retained stale manifest: %v", statErr)
	}
}

func (client *mismatchClient) GetEnclave(context.Context, string) (lifecycle.EnclaveRef, error) {
	return lifecycle.EnclaveRef{Name: "different-name", UUID: strings.Repeat("f", 32)}, nil
}

func (client *mismatchClient) DestroyEnclave(ctx context.Context, ref lifecycle.EnclaveRef) error {
	client.destroyCalls++
	return client.FakeClient.DestroyEnclave(ctx, ref)
}

func (client *deadlineDestroyClient) GetEnclave(ctx context.Context, identifier string) (lifecycle.EnclaveRef, error) {
	client.getDeadline, _ = ctx.Deadline()
	return client.FakeClient.GetEnclave(ctx, identifier)
}

func (client *deadlineDestroyClient) Services(ctx context.Context, ref lifecycle.EnclaveRef) ([]kurtosis.Service, error) {
	client.servicesDeadline, _ = ctx.Deadline()
	return client.FakeClient.Services(ctx, ref)
}

func (client *deadlineDestroyClient) ServiceLogs(ctx context.Context, ref lifecycle.EnclaveRef, identifiers []string) (map[string][]byte, error) {
	client.logsDeadline, _ = ctx.Deadline()
	return client.FakeClient.ServiceLogs(ctx, ref, identifiers)
}

func (client *deadlineDestroyClient) DestroyEnclave(ctx context.Context, ref lifecycle.EnclaveRef) error {
	client.destroyDeadline, _ = ctx.Deadline()
	return client.FakeClient.DestroyEnclave(ctx, ref)
}

func TestRunDestroysOnlyCapturedFullUUID(t *testing.T) {
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	ownership := writer.Layout().Ownership
	if _, err := lifecycle.NewOwnership(ownership, "run-1", "vm64", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(ownership, "vm64", testUUID); err != nil {
		t.Fatal(err)
	}
	client := kurtosis.NewFakeClient()
	ref, err := client.CreateEnclave(context.Background(), "vm64")
	if err != nil {
		t.Fatal(err)
	}
	service := kurtosis.Service{Name: "el-1", UUID: strings.Repeat("b", 32), PrivateIP: "10.0.0.1", PublicIP: "127.0.0.1", PrivatePorts: map[string]kurtosis.Port{}, PublicPorts: map[string]kurtosis.Port{}}
	if err := client.AddService(ref, service); err != nil {
		t.Fatal(err)
	}
	dumper := &fakeDumper{}
	result, err := Run(context.Background(), Options{OwnershipPath: ownership, Writer: writer, Client: client, Dumper: dumper})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Destroyed || dumper.uuid != testUUID {
		t.Fatalf("finalizer result = %+v, dumped UUID = %q", result, dumper.uuid)
	}
	if _, err := client.GetEnclave(context.Background(), testUUID); err == nil {
		t.Fatal("enclave still exists")
	}
	record, err := lifecycle.LoadOwnership(ownership)
	if err != nil {
		t.Fatal(err)
	}
	if record.DestroyedAt == nil {
		t.Fatal("destroyed ownership was not persisted")
	}
}

func TestRunResumesDurablyRequestedDestroyAfterResponseAndMarkerLoss(t *testing.T) {
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	requestedAt := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	if _, err := lifecycle.NewOwnership(writer.Layout().Ownership, "run-destroy-resume", "vm64", requestedAt.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(writer.Layout().Ownership, "vm64", testUUID); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.MarkOwnershipDestroyRequested(writer.Layout().Ownership, testUUID, requestedAt); err != nil {
		t.Fatal(err)
	}
	client := kurtosis.NewFakeClient()
	ref, err := client.CreateEnclave(t.Context(), "vm64")
	if err != nil {
		t.Fatal(err)
	}
	if ref.UUID != testUUID {
		t.Fatalf("fake UUID = %s, want %s", ref.UUID, testUUID)
	}
	// Model a successful external destroy followed by process death before the
	// ownership record could be advanced to DestroyedAt.
	if err := client.DestroyEnclave(t.Context(), ref); err != nil {
		t.Fatal(err)
	}

	finishedAt := requestedAt.Add(time.Minute)
	result, err := Run(t.Context(), Options{
		OwnershipPath: writer.Layout().Ownership, Writer: writer, Client: client,
		Now: func() time.Time { return finishedAt },
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Destroyed || !result.AlreadyClean || result.Preserved {
		t.Fatalf("resumed destruction result = %+v", result)
	}
	record, err := lifecycle.LoadOwnership(writer.Layout().Ownership)
	if err != nil {
		t.Fatal(err)
	}
	if record.DestroyRequestedAt == nil || record.DestroyedAt == nil || !record.DestroyedAt.Equal(finishedAt) {
		t.Fatalf("resumed destruction ownership = %+v", record)
	}
	reconciled := false
	for _, call := range client.Calls {
		if call == "exists:"+testUUID {
			reconciled = true
			break
		}
	}
	if !reconciled {
		t.Fatalf("lost response was not reconciled by exact UUID: %v", client.Calls)
	}
}

func TestRunPreservesNullAndMismatchedOwnership(t *testing.T) {
	for _, test := range []struct {
		name    string
		capture bool
		create  bool
	}{
		{name: "null UUID", capture: false, create: true},
		{name: "mismatch", capture: true, create: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writer, err := report.New(filepath.Join(root, "artifacts"))
			if err != nil {
				t.Fatal(err)
			}
			ownership := writer.Layout().Ownership
			if _, err := lifecycle.NewOwnership(ownership, "run-1", "vm64", time.Now()); err != nil {
				t.Fatal(err)
			}
			if test.capture {
				if _, err := lifecycle.CaptureOwnershipUUID(ownership, "vm64", testUUID); err != nil {
					t.Fatal(err)
				}
			}
			client := kurtosis.NewFakeClient()
			if test.create {
				if _, err := client.CreateEnclave(context.Background(), "vm64"); err != nil {
					t.Fatal(err)
				}
			}
			result, err := Run(context.Background(), Options{OwnershipPath: ownership, Writer: writer, Client: client, Dumper: &fakeDumper{}})
			if err == nil || !result.Preserved {
				t.Fatalf("finalizer result = %+v, error = %v", result, err)
			}
		})
	}
}

func TestRunStillDestroysWhenDiagnosticsFail(t *testing.T) {
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	ownership := writer.Layout().Ownership
	if _, err := lifecycle.NewOwnership(ownership, "run-1", "vm64", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(ownership, "vm64", testUUID); err != nil {
		t.Fatal(err)
	}
	client := kurtosis.NewFakeClient()
	if _, err := client.CreateEnclave(context.Background(), "vm64"); err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), Options{
		OwnershipPath: ownership, Writer: writer, Client: client,
		Dumper: &fakeDumper{err: errors.New("injected dump failure")},
	})
	if err == nil || !strings.Contains(err.Error(), "injected dump failure") || !result.Destroyed {
		t.Fatalf("finalizer result = %+v, error = %v", result, err)
	}
}

func TestRunStillDestroysWhenArtifactOwnershipCopyFails(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory permission semantics differ on Windows")
	}
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	ownershipDir := filepath.Join(root, "ownership-source")
	if err := os.MkdirAll(ownershipDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ownership := filepath.Join(ownershipDir, report.OwnershipFilename)
	if _, err := lifecycle.NewOwnership(ownership, "run-write-failure", "vm64", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(ownership, "vm64", testUUID); err != nil {
		t.Fatal(err)
	}
	client := kurtosis.NewFakeClient()
	if _, err := client.CreateEnclave(context.Background(), "vm64"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(writer.Root(), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(writer.Root(), 0o755) })

	result, err := Run(context.Background(), Options{
		OwnershipPath: ownership, Writer: writer, Client: client, Dumper: &fakeDumper{},
	})
	if err == nil || !strings.Contains(err.Error(), "copy ownership into artifacts") || !result.Destroyed {
		t.Fatalf("finalizer result = %+v, error = %v", result, err)
	}
	if _, getErr := client.GetEnclave(context.Background(), testUUID); getErr == nil {
		t.Fatal("artifact write failure prevented enclave destruction")
	}
}

func TestRunReservesParentDeadlineForDestroy(t *testing.T) {
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	ownership := writer.Layout().Ownership
	if _, err := lifecycle.NewOwnership(ownership, "run-deadline", "vm64", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(ownership, "vm64", testUUID); err != nil {
		t.Fatal(err)
	}
	client := &deadlineDestroyClient{FakeClient: kurtosis.NewFakeClient()}
	if _, err := client.CreateEnclave(context.Background(), "vm64"); err != nil {
		t.Fatal(err)
	}
	parentDeadline := time.Now().Add(30 * time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), parentDeadline)
	defer cancel()
	destroyReserve := 7 * time.Minute
	dumper := &fakeDumper{}

	result, err := Run(ctx, Options{
		OwnershipPath: ownership, Writer: writer, Client: client, Dumper: dumper,
		DestroyReserve: destroyReserve,
	})
	if err != nil || !result.Destroyed {
		t.Fatalf("finalizer result = %+v, error = %v", result, err)
	}
	if want := parentDeadline.Add(-destroyReserve); !dumper.deadline.Equal(want) {
		t.Fatalf("diagnostic deadline = %s, want %s", dumper.deadline, want)
	}
	for operation, deadline := range map[string]time.Time{
		"get enclave":  client.getDeadline,
		"services":     client.servicesDeadline,
		"service logs": client.logsDeadline,
	} {
		if want := parentDeadline.Add(-destroyReserve); !deadline.Equal(want) {
			t.Fatalf("%s deadline = %s, want %s", operation, deadline, want)
		}
	}
	if !client.destroyDeadline.Equal(parentDeadline) {
		t.Fatalf("destroy deadline = %s, want %s", client.destroyDeadline, parentDeadline)
	}
}

func TestRunUsesReservedDestroyPhaseWhenIdentityLookupReachesDiagnosticCutoff(t *testing.T) {
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	ownership := writer.Layout().Ownership
	if _, err := lifecycle.NewOwnership(ownership, "run-cutoff", "vm64", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(ownership, "vm64", testUUID); err != nil {
		t.Fatal(err)
	}
	client := &blockingGetClient{FakeClient: kurtosis.NewFakeClient()}
	if _, err := client.CreateEnclave(context.Background(), "vm64"); err != nil {
		t.Fatal(err)
	}
	parentDeadline := time.Now().Add(2 * time.Second)
	destroyReserve := 1900 * time.Millisecond
	ctx, cancel := context.WithDeadline(context.Background(), parentDeadline)
	defer cancel()
	dumper := &fakeDumper{}

	result, err := Run(ctx, Options{
		OwnershipPath: ownership, Writer: writer, Client: client, Dumper: dumper,
		DestroyReserve: destroyReserve,
	})
	if !errors.Is(err, context.DeadlineExceeded) || !result.Destroyed || result.Preserved {
		t.Fatalf("finalizer result = %+v, error = %v", result, err)
	}
	wantDiagnosticDeadline := parentDeadline.Add(-destroyReserve)
	if !client.getDeadline.Equal(wantDiagnosticDeadline) {
		t.Fatalf("get enclave deadline = %s, want %s", client.getDeadline, wantDiagnosticDeadline)
	}
	if client.servicesCalls != 0 || client.logsCalls != 0 || dumper.uuid != "" {
		t.Fatalf("post-cutoff diagnostics ran: services=%d logs=%d dump UUID=%q", client.servicesCalls, client.logsCalls, dumper.uuid)
	}
	if client.destroyRef.Name != "vm64" || client.destroyRef.UUID != testUUID || !client.destroyRef.Owned {
		t.Fatalf("destroy ref = %+v, want captured owned full UUID", client.destroyRef)
	}
	if client.destroyCtxErr != nil || !client.destroyDeadline.Equal(parentDeadline) {
		t.Fatalf("destroy context error = %v, deadline = %s, want %s", client.destroyCtxErr, client.destroyDeadline, parentDeadline)
	}
	if _, getErr := client.FakeClient.GetEnclave(context.Background(), testUUID); getErr == nil {
		t.Fatal("diagnostic cutoff bypassed owned-enclave destruction")
	}
	record, loadErr := lifecycle.LoadOwnership(ownership)
	if loadErr != nil || record.DestroyedAt == nil || record.Preserved {
		t.Fatalf("ownership after cutoff cleanup = %+v, error = %v", record, loadErr)
	}
}

func TestRunPreservesExplicitIdentityMismatchWithoutDestroyAttempt(t *testing.T) {
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	ownership := writer.Layout().Ownership
	if _, err := lifecycle.NewOwnership(ownership, "run-mismatch", "vm64", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(ownership, "vm64", testUUID); err != nil {
		t.Fatal(err)
	}
	client := &mismatchClient{FakeClient: kurtosis.NewFakeClient()}
	if _, err := client.CreateEnclave(context.Background(), "vm64"); err != nil {
		t.Fatal(err)
	}

	result, err := Run(context.Background(), Options{
		OwnershipPath: ownership, Writer: writer, Client: client, Dumper: &fakeDumper{},
	})
	if err == nil || !strings.Contains(err.Error(), "identity mismatch") || !result.Preserved || result.Destroyed || client.destroyCalls != 0 {
		t.Fatalf("finalizer result = %+v, destroy calls = %d, error = %v", result, client.destroyCalls, err)
	}
	if _, getErr := client.FakeClient.GetEnclave(context.Background(), testUUID); getErr != nil {
		t.Fatalf("identity mismatch removed enclave: %v", getErr)
	}
}

func TestRunPreservesEnclaveWhenDiagnosticsAndCleanupFail(t *testing.T) {
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	ownership := writer.Layout().Ownership
	if _, err := lifecycle.NewOwnership(ownership, "run-1", "vm64", time.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(ownership, "vm64", testUUID); err != nil {
		t.Fatal(err)
	}
	client := kurtosis.NewFakeClient()
	if _, err := client.CreateEnclave(context.Background(), "vm64"); err != nil {
		t.Fatal(err)
	}
	dumpFailure := errors.New("injected dump failure")
	destroyFailure := errors.New("injected destroy failure")
	client.DestroyError = destroyFailure
	result, err := Run(context.Background(), Options{
		OwnershipPath: ownership, Writer: writer, Client: client,
		Dumper: &fakeDumper{err: dumpFailure},
	})
	if !errors.Is(err, dumpFailure) || !errors.Is(err, destroyFailure) || !result.Preserved || result.Destroyed {
		t.Fatalf("finalizer result = %+v, error = %v", result, err)
	}
	record, err := lifecycle.LoadOwnership(ownership)
	if err != nil {
		t.Fatal(err)
	}
	if !record.Preserved || record.DestroyedAt != nil || !strings.Contains(record.PreserveReason, dumpFailure.Error()) || !strings.Contains(record.PreserveReason, destroyFailure.Error()) {
		t.Fatalf("cleanup-failure ownership = %+v", record)
	}
	if _, err := client.GetEnclave(context.Background(), testUUID); err != nil {
		t.Fatalf("cleanup failure did not preserve the enclave: %v", err)
	}
}

func TestCompleteStandaloneArtifactsCreatesInterruptedFailureBundle(t *testing.T) {
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	created := finished.Add(-time.Hour)
	if _, err := lifecycle.NewOwnership(writer.Layout().Ownership, "run-interrupted", "vm64", created); err != nil {
		t.Fatal(err)
	}
	store := createInterruptedCheckpoint(t, writer, "run-interrupted", created)
	originalEvent := report.TimelineEvent{
		Sequence: 1, At: created.Add(time.Minute), Kind: "lifecycle-created",
		Status: report.StatusRunning, Message: "runner started",
		Fields: map[string]any{"source": "runner"},
	}
	if err := writer.WriteTimeline(report.Timeline{
		RunID: "run-interrupted", Events: []report.TimelineEvent{originalEvent},
	}); err != nil {
		t.Fatal(err)
	}
	if err := CompleteStandaloneArtifacts(ArtifactOptions{
		OwnershipPath: writer.Layout().Ownership, Writer: writer,
		Result: Result{Destroyed: true}, Now: func() time.Time { return finished },
	}); err != nil {
		t.Fatal(err)
	}

	reason, ok, err := readOptionalJSON[report.DiagnosticReason](writer.Layout().Reason)
	if err != nil || !ok || reason.Category != report.FailureCancellation || !strings.Contains(reason.Message, "closed an interrupted attempt") || reason.Stage != "system-base" {
		t.Fatalf("standalone reason = %+v, exists=%t, error=%v", reason, ok, err)
	}
	results, ok, err := readOptionalJSON[report.Results](writer.Layout().Results)
	if err != nil || !ok || results.Status != report.StatusFailed {
		t.Fatalf("standalone results = %+v, exists=%t, error=%v", results, ok, err)
	}
	interrupted, interruptedOK := resultStage(results, interruptedStageName)
	validate, validateOK := resultStage(results, "validate")
	system, systemOK := resultStage(results, "system-base")
	finalizeStage, finalizeOK := resultStage(results, standaloneStageName)
	if interruptedOK || interrupted.Name != "" || !validateOK || validate.Status != report.StatusPassed || !systemOK || system.Status != report.StatusCanceled || system.FailureCategory != report.FailureCancellation || !finalizeOK || finalizeStage.Status != report.StatusPassed {
		t.Fatalf("standalone stages = %+v", results.Stages)
	}
	checkpoint, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	last := checkpoint.Attempts[len(checkpoint.Attempts)-1]
	if checkpoint.Status != lifecycle.StatusCleanedAfterFailure || len(checkpoint.Attempts) != 2 || len(checkpoint.Completed) != 1 || checkpoint.Completed[0] != "validate" || last.FinishedAt == nil || last.ExitCode == nil || *last.ExitCode != 255 || last.FailureCategory != lifecycle.FailureCancellation || checkpoint.FailureCategory != lifecycle.FailureCancellation {
		t.Fatalf("repaired checkpoint = %+v", checkpoint)
	}
	timeline, err := report.LoadTimeline(writer.Layout().Timeline)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Events) != 2 || timeline.Events[0].Sequence != originalEvent.Sequence || timeline.Events[0].Kind != originalEvent.Kind || timeline.Events[0].Message != originalEvent.Message || timeline.Events[0].Fields["source"] != "runner" {
		t.Fatalf("standalone timeline did not preserve history: %+v", timeline.Events)
	}
	finalEvent := timeline.Events[1]
	if finalEvent.Sequence != 2 || finalEvent.Kind != standaloneStageName || finalEvent.Status != report.StatusPassed || finalEvent.Fields["cleanup_status"] != "destroyed" || finalEvent.Fields["lifecycle_status"] != string(report.StatusFailed) || finalEvent.Fields["checkpoint_status"] != string(lifecycle.StatusCleanedAfterFailure) || finalEvent.Fields["checkpoint_terminalized"] != true || finalEvent.Fields["interrupted_attempt_closed"] != true {
		t.Fatalf("standalone timeline event = %+v", finalEvent)
	}
	artifact, ok, err := readOptionalJSON[StandaloneArtifact](writer.Layout().Finalize)
	if err != nil || !ok || artifact.CleanupStatus != "destroyed" || artifact.LifecycleStatus != report.StatusFailed || !artifact.Result.Destroyed {
		t.Fatalf("standalone finalize artifact = %+v, exists=%t, error=%v", artifact, ok, err)
	}
	junit, err := os.ReadFile(writer.Layout().JUnit)
	if err != nil || !strings.Contains(string(junit), `type="cancellation"`) {
		t.Fatalf("standalone JUnit = %q, error=%v", junit, err)
	}
	manifest, ok, err := readOptionalJSON[report.Manifest](writer.Layout().Manifest)
	if err != nil || !ok {
		t.Fatalf("standalone manifest exists=%t, error=%v", ok, err)
	}
	for _, path := range []string{"checkpoint.json", "diagnostics/reason.json", "finalize.json", "junit.xml", "results.json", "timeline.json"} {
		if !manifestHasPath(manifest, path) {
			t.Fatalf("manifest does not inventory %s: %+v", path, manifest.Artifacts)
		}
	}
}

func TestCompleteStandaloneArtifactsDoesNotRetainPriorSealWhenResealFails(t *testing.T) {
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	created := time.Now().UTC().Add(-time.Minute)
	if _, err := lifecycle.NewOwnership(writer.Layout().Ownership, "run-stale-repair", "vm64", created); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteManifest(report.ManifestMetadata{RunID: "run-stale-repair", GeneratedAt: created}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteResults(report.Results{
		RunID: "run-stale-repair", Status: report.StatusFailed, StartedAt: created,
		Stages: []report.StageResult{},
		Suites: []report.SuiteResult{{
			Name: "failed-suite", Stage: "el1", Attempt: 1, Status: report.StatusFailed,
			FailureCategory: report.FailureAssertion, LogPath: "logs/missing.log",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	err = CompleteStandaloneArtifacts(ArtifactOptions{
		OwnershipPath: writer.Layout().Ownership, Writer: writer,
		Result: Result{AlreadyClean: true}, Now: func() time.Time { return created.Add(time.Minute) },
	})
	if err == nil || !strings.Contains(err.Error(), "not a manifest-inventoried regular artifact") {
		t.Fatalf("standalone repair reseal error = %v", err)
	}
	if _, statErr := os.Stat(writer.Layout().Manifest); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed standalone repair retained prior manifest: %v", statErr)
	}
}

func TestCompleteStandaloneArtifactsPreservesRootReasonOnCleanupFailure(t *testing.T) {
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	ownership := writer.Layout().Ownership
	if _, err := lifecycle.NewOwnership(ownership, "run-cleanup-failure", "vm64", finished.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(ownership, "vm64", testUUID); err != nil {
		t.Fatal(err)
	}
	store := createInterruptedCheckpoint(t, writer, "run-cleanup-failure", finished.Add(-time.Hour))
	if err := writer.WriteTimeline(report.Timeline{
		RunID:  "run-cleanup-failure",
		Events: []report.TimelineEvent{{Sequence: 1, At: finished.Add(-30 * time.Minute), Kind: "lifecycle-created", Status: report.StatusRunning}},
	}); err != nil {
		t.Fatal(err)
	}
	rootReason := report.DiagnosticReason{
		Category: report.FailureAssertion, At: finished.Add(-time.Minute),
		Stage: "system-base", Message: "original VM64 assertion failed",
	}
	if err := writer.WriteReason(rootReason); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(writer.Layout().Reason)
	if err != nil {
		t.Fatal(err)
	}
	client := kurtosis.NewFakeClient()
	if _, err := client.CreateEnclave(t.Context(), "vm64"); err != nil {
		t.Fatal(err)
	}
	destroyFailure := errors.New("injected destroy failure")
	client.DestroyError = destroyFailure
	result, finalizeErr := Run(t.Context(), Options{
		OwnershipPath: ownership, Writer: writer, Client: client, Dumper: &fakeDumper{},
		Now: func() time.Time { return finished },
	})
	if !errors.Is(finalizeErr, destroyFailure) {
		t.Fatalf("finalize error = %v", finalizeErr)
	}
	if err := CompleteStandaloneArtifacts(ArtifactOptions{
		OwnershipPath: ownership, Writer: writer, Result: result,
		FinalizeError: finalizeErr, Now: func() time.Time { return finished },
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(writer.Layout().Reason)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("root-cause reason was overwritten\nbefore=%s\nafter=%s", before, after)
	}
	results, ok, err := readOptionalJSON[report.Results](writer.Layout().Results)
	if err != nil || !ok || results.Status != report.StatusFailed {
		t.Fatalf("cleanup-failure results = %+v, exists=%t, error=%v", results, ok, err)
	}
	stage, ok := resultStage(results, standaloneStageName)
	if !ok || stage.Status != report.StatusFailed || stage.FailureCategory != report.FailureCleanup || !strings.Contains(stage.Message, destroyFailure.Error()) {
		t.Fatalf("cleanup-failure stage = %+v, exists=%t", stage, ok)
	}
	artifact, ok, err := readOptionalJSON[StandaloneArtifact](writer.Layout().Finalize)
	if err != nil || !ok || artifact.CleanupStatus != "failed" || artifact.Error == "" || artifact.LifecycleStatus != report.StatusFailed {
		t.Fatalf("cleanup-failure finalize artifact = %+v, exists=%t, error=%v", artifact, ok, err)
	}
	checkpoint, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	last := checkpoint.Attempts[len(checkpoint.Attempts)-1]
	if checkpoint.Status != lifecycle.StatusRunning || last.FinishedAt != nil || last.ExitCode != nil {
		t.Fatalf("failed cleanup mutated resumable checkpoint = %+v", checkpoint)
	}
	timeline, err := report.LoadTimeline(writer.Layout().Timeline)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Events) != 2 || timeline.Events[0].Kind != "lifecycle-created" {
		t.Fatalf("cleanup-failure timeline history = %+v", timeline.Events)
	}
	finalEvent := timeline.Events[1]
	if finalEvent.Kind != standaloneStageName || finalEvent.Status != report.StatusFailed || finalEvent.Fields["checkpoint_status"] != string(lifecycle.StatusRunning) || finalEvent.Fields["checkpoint_terminalized"] != false || finalEvent.Fields["interrupted_attempt_closed"] != false {
		t.Fatalf("cleanup-failure timeline event = %+v", finalEvent)
	}
	junit, err := os.ReadFile(writer.Layout().JUnit)
	if err != nil || !strings.Contains(string(junit), `type="cleanup_failure"`) {
		t.Fatalf("cleanup-failure JUnit = %q, error=%v", junit, err)
	}
}

func TestCompleteStandaloneArtifactsPreservesTerminalSuccessfulCheckpoint(t *testing.T) {
	root := t.TempDir()
	writer, err := report.New(filepath.Join(root, "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	finished := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	created := finished.Add(-time.Hour)
	const runID = "run-complete"
	if _, err := lifecycle.NewOwnership(writer.Layout().Ownership, runID, "vm64", created); err != nil {
		t.Fatal(err)
	}
	stageFinished := created.Add(10 * time.Minute)
	exitCode := 0
	checkpoint := lifecycle.NewCheckpoint(
		runID, strings.Repeat("a", 40), strings.Repeat("b", 64), filepath.Join(root, "dump"), strings.Repeat("c", 64),
		lifecycle.EnclaveRef{Name: "vm64", UUID: testUUID, Owned: true}, created,
	)
	checkpoint.Attempts = []lifecycle.Attempt{{
		Stage: "validate", Attempt: 1, StartedAt: created.Add(time.Minute), FinishedAt: &stageFinished, ExitCode: &exitCode,
	}}
	checkpoint.Completed = []string{"validate"}
	checkpoint.UpdatedAt = stageFinished
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: []string{"validate"}}
	if err := store.Create(checkpoint); err != nil {
		t.Fatal(err)
	}
	if err := checkpoint.MarkComplete(store, stageFinished); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(writer.Layout().Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if err := CompleteStandaloneArtifacts(ArtifactOptions{
		OwnershipPath: writer.Layout().Ownership, Writer: writer,
		Result: Result{AlreadyClean: true}, Now: func() time.Time { return finished },
	}); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(writer.Layout().Checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("terminal successful checkpoint was rewritten\nbefore=%s\nafter=%s", before, after)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != lifecycle.StatusCompleteClean {
		t.Fatalf("terminal checkpoint status = %q", loaded.Status)
	}
	results, ok, err := readOptionalJSON[report.Results](writer.Layout().Results)
	if err != nil || !ok || results.Status != report.StatusPassed {
		t.Fatalf("terminal checkpoint results = %+v, exists=%t, error=%v", results, ok, err)
	}
	stage, ok := resultStage(results, "validate")
	if !ok || stage.Status != report.StatusPassed {
		t.Fatalf("terminal checkpoint stage results = %+v", results.Stages)
	}
	if _, exists, err := readOptionalJSON[report.DiagnosticReason](writer.Layout().Reason); err != nil || exists {
		t.Fatalf("successful checkpoint created failure reason: exists=%t, error=%v", exists, err)
	}
	timeline, err := report.LoadTimeline(writer.Layout().Timeline)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Events) != 1 || timeline.Events[0].Kind != standaloneStageName || timeline.Events[0].Status != report.StatusPassed || timeline.Events[0].Fields["checkpoint_status"] != string(lifecycle.StatusCompleteClean) || timeline.Events[0].Fields["checkpoint_terminalized"] != false {
		t.Fatalf("terminal checkpoint timeline = %+v", timeline.Events)
	}
}

func createInterruptedCheckpoint(t *testing.T, writer *report.Writer, runID string, created time.Time) lifecycle.Store {
	t.Helper()
	validateFinished := created.Add(2 * time.Minute)
	validateExit := 0
	currentStage := "system-base"
	checkpoint := lifecycle.NewCheckpoint(
		runID, strings.Repeat("a", 40), strings.Repeat("b", 64), filepath.Join(writer.Root(), "dump"), strings.Repeat("c", 64),
		lifecycle.EnclaveRef{Name: "vm64", UUID: testUUID, Owned: true}, created,
	)
	checkpoint.Attempts = []lifecycle.Attempt{
		{Stage: "validate", Attempt: 1, StartedAt: created.Add(time.Minute), FinishedAt: &validateFinished, ExitCode: &validateExit},
		{Stage: currentStage, Attempt: 1, StartedAt: created.Add(3 * time.Minute)},
	}
	checkpoint.Completed = []string{"validate"}
	checkpoint.CurrentStage = &currentStage
	checkpoint.UpdatedAt = created.Add(3 * time.Minute)
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: []string{"validate", currentStage}}
	if err := store.Create(checkpoint); err != nil {
		t.Fatal(err)
	}
	return store
}

func resultStage(results report.Results, name string) (report.StageResult, bool) {
	for _, stage := range results.Stages {
		if stage.Name == name {
			return stage, true
		}
	}
	return report.StageResult{}, false
}

func manifestHasPath(manifest report.Manifest, path string) bool {
	for _, artifact := range manifest.Artifacts {
		if artifact.Path == path {
			return true
		}
	}
	return false
}
