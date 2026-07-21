// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/config"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
)

const appTestSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type appDoctorRunner struct {
	root          string
	engineVersion string
	onOutput      func(context.Context)
}

func (runner appDoctorRunner) LookPath(name string) (string, error) {
	return "/usr/bin/" + name, nil
}

func (runner appDoctorRunner) Output(ctx context.Context, name string, arguments ...string) ([]byte, error) {
	if runner.onOutput != nil {
		runner.onOutput(ctx)
	}
	key := filepath.Base(name) + " " + strings.Join(arguments, " ")
	switch key {
	case "git -C " + runner.root + " rev-parse HEAD":
		return []byte(appTestSHA + "\n"), nil
	case "kurtosis version":
		return []byte("CLI Version:   1.20.0\n"), nil
	case "kurtosis engine status":
		version := runner.engineVersion
		if version == "" {
			version = "1.20.0"
		}
		return []byte("A Kurtosis engine is running with the following info:\nVersion:   " + version + "\n"), nil
	default:
		return nil, errors.New("unexpected doctor command: " + key)
	}
}

func TestParseCommonKeepsBorrowedNetworkSeparate(t *testing.T) {
	root, params := appTestRepository(t)
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	application := &App{Now: func() time.Time { return now }, Stderr: &bytes.Buffer{}}
	result, err := application.parseCommon("test", config.ModeTest, []string{
		"--repo-root", root,
		"--source-sha", appTestSHA,
		"--network-params", params,
		"--results", filepath.Join(root, "results"),
		"--enclave", "existing-network",
		"--global-timeout", "2h",
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.config.EnclaveIdentifier != "existing-network" || result.config.EnclaveName != "" {
		t.Fatalf("borrowed identity mapping = %#v", result.config)
	}
	if result.config.Mode != config.ModeTest || result.config.AllowDisruptive || result.config.PreserveOnFailure != true {
		t.Fatalf("borrowed defaults = %#v", result.config)
	}
	if got := result.config.GlobalDeadline; !got.Equal(now.Add(2 * time.Hour)) {
		t.Fatalf("deadline = %s", got)
	}
}

func TestDoctorDoesNotConstructKurtosisClient(t *testing.T) {
	root, params := appTestRepository(t)
	var stdout bytes.Buffer
	clientCalls := 0
	application := &App{
		Stdout: &stdout, Stderr: &bytes.Buffer{}, Now: time.Now,
		DoctorRunner: appDoctorRunner{root: root},
		ClientFactory: func() (kurtosis.Client, error) {
			clientCalls++
			return nil, errors.New("doctor must not create a client")
		},
	}
	err := application.Execute(t.Context(), []string{
		"doctor", "--repo-root", root, "--source-sha", appTestSHA,
		"--network-params", params, "--results", filepath.Join(root, "results"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if clientCalls != 0 {
		t.Fatalf("doctor constructed %d Kurtosis clients", clientCalls)
	}
	if !strings.Contains(stdout.String(), `"required_kurtosis_version": "1.20.0"`) || !strings.Contains(stdout.String(), `"passed": true`) {
		t.Fatalf("doctor output = %s", stdout.String())
	}
}

func TestTestCommandRejectsMissingEnclaveBeforeClientConstruction(t *testing.T) {
	root, params := appTestRepository(t)
	clientCalls := 0
	application := &App{
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Now: time.Now,
		ClientFactory: func() (kurtosis.Client, error) {
			clientCalls++
			return nil, errors.New("unexpected client construction")
		},
	}
	err := application.Execute(t.Context(), []string{
		"test", "--repo-root", root, "--source-sha", appTestSHA,
		"--network-params", params, "--results", filepath.Join(root, "results"),
	})
	var usage *UsageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "requires --enclave") {
		t.Fatalf("error = %v, want missing-enclave usage error", err)
	}
	if clientCalls != 0 {
		t.Fatalf("invalid test command constructed %d clients", clientCalls)
	}
}

func TestRunValidatesEnvironmentBeforeClientConstruction(t *testing.T) {
	root, params := appTestRepository(t)
	now := time.Now().UTC()
	clientCalls := 0
	var observedDeadline time.Time
	application := &App{
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Now: func() time.Time { return now },
		DoctorRunner: appDoctorRunner{
			root: root, engineVersion: "1.19.0",
			onOutput: func(ctx context.Context) {
				if deadline, ok := ctx.Deadline(); ok {
					observedDeadline = deadline
				}
			},
		},
		ClientFactory: func() (kurtosis.Client, error) {
			clientCalls++
			return nil, errors.New("invalid preflight must not construct a client")
		},
	}
	err := application.Execute(t.Context(), []string{
		"run", "--repo-root", root, "--source-sha", appTestSHA,
		"--network-params", params, "--results", filepath.Join(root, "results"),
		"--enclave", "preflight-must-not-create",
		"--global-timeout", "2h", "--cleanup-reserve", "30m",
	})
	if err == nil || !strings.Contains(err.Error(), "preflight validation before enclave creation") || !strings.Contains(err.Error(), "1.19.0") {
		t.Fatalf("run preflight error = %v", err)
	}
	if clientCalls != 0 {
		t.Fatalf("invalid preflight constructed %d Kurtosis clients", clientCalls)
	}
	if want := now.Add(90 * time.Minute); !observedDeadline.Equal(want) {
		t.Fatalf("preflight deadline = %s, want %s", observedDeadline, want)
	}
}

func TestPinnedImagesRejectsUnreadableFileAndReadsOnlyImages(t *testing.T) {
	if _, err := pinnedImages(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing image lock was accepted")
	}
	path := filepath.Join(t.TempDir(), "images.lock.env")
	payload := "PINNED_COMMIT='" + appTestSHA + "'\nPINNED_BASE_IMAGE='repo/image:tag@sha256:" + strings.Repeat("b", 64) + "'\n"
	if err := os.WriteFile(path, []byte(payload), 0o600); err != nil {
		t.Fatal(err)
	}
	images, err := pinnedImages(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(images) != 1 || !strings.HasPrefix(images[0], "repo/image:") {
		t.Fatalf("pinned images = %v", images)
	}
}

type appFinalizeDumper struct{}

func (appFinalizeDumper) Dump(_ context.Context, _ string, destination string) ([]byte, error) {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return nil, err
	}
	return []byte("dump output"), nil
}

func TestFinalizeCommandRepairsMissingFailureArtifacts(t *testing.T) {
	finished := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	writer, client, ownership := appFinalizeFixture(t, "run-interrupted", finished)
	var stdout bytes.Buffer
	application := &App{
		Stdout: &stdout, Stderr: &bytes.Buffer{}, Now: func() time.Time { return finished },
		ClientFactory: func() (kurtosis.Client, error) { return client, nil },
	}
	application.Dependencies.Dumper = appFinalizeDumper{}
	if err := application.Execute(t.Context(), []string{
		"finalize", "--ownership", ownership, "--results", writer.Root(),
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), `"destroyed": true`) {
		t.Fatalf("finalize stdout = %s", stdout.String())
	}
	for _, path := range []string{writer.Layout().Reason, writer.Layout().Results, writer.Layout().JUnit, writer.Layout().Timeline, writer.Layout().Finalize, writer.Layout().Manifest} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing standalone artifact %s: %v", path, err)
		}
	}
	var results report.Results
	appReadJSON(t, writer.Layout().Results, &results)
	if results.Status != report.StatusFailed {
		t.Fatalf("standalone finalize marked interrupted lifecycle %q", results.Status)
	}
	var artifact struct {
		CleanupStatus   string        `json:"cleanup_status"`
		LifecycleStatus report.Status `json:"lifecycle_status"`
	}
	appReadJSON(t, writer.Layout().Finalize, &artifact)
	if artifact.CleanupStatus != "destroyed" || artifact.LifecycleStatus != report.StatusFailed {
		t.Fatalf("standalone finalize artifact = %+v", artifact)
	}
	timeline, err := report.LoadTimeline(writer.Layout().Timeline)
	if err != nil {
		t.Fatal(err)
	}
	if len(timeline.Events) != 1 || timeline.Events[0].Kind != "standalone-finalize" || timeline.Events[0].Status != report.StatusPassed {
		t.Fatalf("standalone finalize timeline = %+v", timeline.Events)
	}
}

func TestFinalizeCommandPreservesRootReasonWhenCleanupFails(t *testing.T) {
	finished := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	writer, client, ownership := appFinalizeFixture(t, "run-cleanup-failure", finished)
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
	destroyFailure := errors.New("injected app destroy failure")
	client.DestroyError = destroyFailure
	application := &App{
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Now: func() time.Time { return finished },
		ClientFactory: func() (kurtosis.Client, error) { return client, nil },
	}
	application.Dependencies.Dumper = appFinalizeDumper{}
	err = application.Execute(t.Context(), []string{
		"finalize", "--ownership", ownership, "--results", writer.Root(),
	})
	if !errors.Is(err, destroyFailure) {
		t.Fatalf("finalize cleanup error = %v", err)
	}
	after, err := os.ReadFile(writer.Layout().Reason)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("finalize overwrote primary reason\nbefore=%s\nafter=%s", before, after)
	}
	var results report.Results
	appReadJSON(t, writer.Layout().Results, &results)
	if results.Status != report.StatusFailed {
		t.Fatalf("cleanup-failure results status = %q", results.Status)
	}
	var artifact struct {
		CleanupStatus   string        `json:"cleanup_status"`
		LifecycleStatus report.Status `json:"lifecycle_status"`
		Error           string        `json:"error"`
	}
	appReadJSON(t, writer.Layout().Finalize, &artifact)
	if artifact.CleanupStatus != "failed" || artifact.LifecycleStatus != report.StatusFailed || !strings.Contains(artifact.Error, destroyFailure.Error()) {
		t.Fatalf("cleanup-failure finalize artifact = %+v", artifact)
	}
	timeline, timelineErr := report.LoadTimeline(writer.Layout().Timeline)
	if timelineErr != nil {
		t.Fatal(timelineErr)
	}
	if len(timeline.Events) != 1 || timeline.Events[0].Kind != "standalone-finalize" || timeline.Events[0].Status != report.StatusFailed {
		t.Fatalf("cleanup-failure timeline = %+v", timeline.Events)
	}
	if _, err := os.Stat(writer.Layout().Manifest); err != nil {
		t.Fatalf("cleanup failure did not seal manifest: %v", err)
	}
}

func TestFinalizeRepairsArtifactsWhenClientConstructionFails(t *testing.T) {
	finished := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	writer, client, ownership := appFinalizeFixture(t, "run-client-failure", finished)
	clientFailure := errors.New("injected SDK connection failure")
	application := &App{
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Now: func() time.Time { return finished },
		ClientFactory: func() (kurtosis.Client, error) { return nil, clientFailure },
	}
	err := application.Execute(t.Context(), []string{
		"finalize", "--ownership", ownership, "--results", writer.Root(),
	})
	if !errors.Is(err, clientFailure) {
		t.Fatalf("finalize client error = %v", err)
	}
	for _, path := range []string{writer.Layout().Reason, writer.Layout().Results, writer.Layout().JUnit, writer.Layout().Timeline, writer.Layout().Finalize, writer.Layout().Manifest} {
		if _, statErr := os.Stat(path); statErr != nil {
			t.Fatalf("client failure did not repair %s: %v", path, statErr)
		}
	}
	if _, getErr := client.GetEnclave(t.Context(), "vm64"); getErr != nil {
		t.Fatalf("client-construction failure unexpectedly removed enclave: %v", getErr)
	}
}

func TestFinalizeRejectsTimeoutWithoutDestroyReserve(t *testing.T) {
	clientCalls := 0
	application := &App{
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Now: time.Now,
		ClientFactory: func() (kurtosis.Client, error) {
			clientCalls++
			return nil, errors.New("unexpected client construction")
		},
	}
	err := application.Execute(t.Context(), []string{
		"finalize", "--ownership", filepath.Join(t.TempDir(), "ownership.json"),
		"--timeout", "5m",
	})
	var usage *UsageError
	if !errors.As(err, &usage) || !strings.Contains(err.Error(), "must exceed") {
		t.Fatalf("finalize timeout error = %v", err)
	}
	if clientCalls != 0 {
		t.Fatalf("invalid timeout constructed %d clients", clientCalls)
	}
}

func appFinalizeFixture(t *testing.T, runID string, now time.Time) (*report.Writer, *kurtosis.FakeClient, string) {
	t.Helper()
	writer, err := report.New(filepath.Join(t.TempDir(), "artifacts"))
	if err != nil {
		t.Fatal(err)
	}
	ownership := writer.Layout().Ownership
	if _, err := lifecycle.NewOwnership(ownership, runID, "vm64", now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	client := kurtosis.NewFakeClient()
	ref, err := client.CreateEnclave(t.Context(), "vm64")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lifecycle.CaptureOwnershipUUID(ownership, "vm64", ref.UUID); err != nil {
		t.Fatal(err)
	}
	return writer, client, ownership
}

func appReadJSON(t *testing.T, path string, value any) {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(payload, value); err != nil {
		t.Fatalf("decode %s: %v", path, err)
	}
}

func appTestRepository(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module github.com/theQRL/go-qrl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	params := filepath.Join(root, "network_params.yaml")
	if err := os.WriteFile(params, []byte("participants: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lockDir := filepath.Join(root, "scripts", "local_testnet")
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		t.Fatal(err)
	}
	lock := "PINNED_BASE_IMAGE='repo/image:tag@sha256:" + strings.Repeat("b", 64) + "'\n"
	if err := os.WriteFile(filepath.Join(lockDir, "images.lock.env"), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
	return root, params
}
