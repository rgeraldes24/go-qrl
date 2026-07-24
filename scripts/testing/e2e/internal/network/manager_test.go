// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/source"
)

type imageCommandRunner struct{ images map[string]ImageIdentity }

func (runner imageCommandRunner) Run(context.Context, command) error { return nil }
func (runner imageCommandRunner) CombinedOutput(_ context.Context, specification command) ([]byte, error) {
	if len(specification.Args) != 3 || specification.Args[0] != "image" || specification.Args[1] != "inspect" {
		return nil, fmt.Errorf("unexpected command: %+v", specification)
	}
	for _, image := range runner.images {
		if image.Ref == specification.Args[2] {
			return json.Marshal([]dockerInspection{{ID: image.ID, Config: struct {
				Labels map[string]string `json:"Labels"`
			}{Labels: image.Labels}}})
		}
	}
	return nil, errors.New("image not found")
}

type managerFixture struct {
	manager    *Manager
	client     *kurtosis.Fake
	request    StartRequest
	networkDir string
}

func newManagerFixture(t *testing.T) managerFixture {
	t.Helper()
	repoRoot, err := filepath.Abs("../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	commit, err := source.Commit(context.Background(), nil, repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	networkDir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	images := []ImageIdentity{
		{Role: "consensus", Ref: "local/qrysm-beacon:8b80fa0c3f5a", ID: "sha256:" + strings.Repeat("1", 64), Labels: map[string]string{"revision": "consensus"}},
		{Role: "execution", Ref: "local/go-qrl:network", ID: "sha256:" + strings.Repeat("2", 64), Labels: map[string]string{"revision": commit}},
		{Role: "genesis", Ref: "local/qrl-genesis-generator:3884e4228ef3-8b80fa0c3f5a", ID: "sha256:" + strings.Repeat("3", 64), Labels: map[string]string{"revision": "genesis"}},
		{Role: "validator", Ref: "local/qrysm-validator:8b80fa0c3f5a", ID: "sha256:" + strings.Repeat("4", 64), Labels: map[string]string{"revision": "validator"}},
	}
	services := []kurtosis.Service{
		{Name: "el-1-gqrl-qrysm", UUID: strings.Repeat("b", 32), Status: kurtosis.ServiceStatusRunning, Image: images[1].Ref, PublicIP: "127.0.0.1", PublicPorts: map[string]kurtosis.Port{"rpc": {Number: 18545}, "ws": {Number: 18546}}},
		{Name: "cl-1-qrysm-gqrl", UUID: strings.Repeat("c", 32), Status: kurtosis.ServiceStatusRunning, Image: images[0].Ref},
		{Name: "vc-1-gqrl-qrysm", UUID: strings.Repeat("d", 32), Status: kurtosis.ServiceStatusRunning, Image: images[3].Ref},
	}
	client := &kurtosis.Fake{
		Enclave: kurtosis.EnclaveRef{
			Name:  defaultEnclaveName(networkDir, commit),
			UUID:  strings.Repeat("a", 32),
			Owned: true,
		},
		RetainedPackageID: packageID,
		ServiceList:       services,
		ExecResults: map[string]kurtosis.ExecResult{
			fmt.Sprint([]string{"sha256sum", executionBinaryPath}): {Output: strings.Repeat("5", 64) + "  " + executionBinaryPath + "\n"},
			fmt.Sprint([]string{executionBinaryPath, "version"}):   {Output: "GQRL\nGit Commit: " + commit + "\n"},
		},
	}
	prepared := preparedNetwork{Images: images}
	commands := imageCommandRunner{images: make(map[string]ImageIdentity)}
	for _, image := range images {
		commands.images[image.Role] = image
	}
	manager := &Manager{
		NewClient: func() (kurtosis.Client, error) { return client, nil },
		Commands:  commands,
		Getenv:    func(string) string { return "docker" },
		Stdout:    io.Discard,
		Stderr:    io.Discard,
		Probe: func(context.Context, probeRequest) (probeResult, error) {
			return probeResult{ChainID: "0x539", GenesisHash: "0x" + strings.Repeat("6", 64)}, nil
		},
	}
	manager.Prepare = func(_ context.Context, _ commandRunner, request StartRequest, _, walletAddress string, _, _ io.Writer) (preparedNetwork, error) {
		value := fmt.Sprintf(`{"address":%q}`, walletAddress)
		prepared.Params, prepared.ParamsDigest = value, digestCanonicalJSON(value)
		return prepared, nil
	}
	request := StartRequest{
		RepoRoot: repoRoot, NetworkDir: networkDir,
		BuildTool: "/unused/build", DockerBin: "docker", StartTimeout: time.Minute,
	}
	return managerFixture{manager: manager, client: client, request: request, networkDir: networkDir}
}

func TestStartPublishesReadyStateAndReusesNetwork(t *testing.T) {
	fixture := newManagerFixture(t)
	first, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Ready {
		t.Fatalf("first start = %+v", first)
	}
	ownership, err := loadOwnership(fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if ownership.Name != fixture.client.Enclave.Name || ownership.UUID != fixture.client.Enclave.UUID {
		t.Fatalf("ownership = %+v, client enclave = %+v", ownership, fixture.client.Enclave)
	}
	for _, path := range []string{ownershipPath(fixture.networkDir), statePath(fixture.networkDir)} {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s metadata = %v, err=%v", path, info, err)
		}
	}

	second, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Ready || second.state.SourceCommit != first.state.SourceCommit {
		t.Fatalf("reused start = %+v, first = %+v", second, first)
	}
	if got := countCalls(fixture.client.Calls, "create:"); got != 1 {
		t.Fatalf("create calls = %d, want 1: %v", got, fixture.client.Calls)
	}
	if got := countCalls(fixture.client.Calls, "run:"); got != 1 {
		t.Fatalf("package calls = %d, want 1: %v", got, fixture.client.Calls)
	}
}

func TestFailedProvisioningRetainsOwnershipAndRequiresStop(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.RunError = errors.New("package failed")
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil {
		t.Fatal("failed provisioning unexpectedly succeeded")
	}
	ownership, err := loadOwnership(fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if ownership.UUID != fixture.client.Enclave.UUID {
		t.Fatalf("ownership = %+v", ownership)
	}
	if _, err := os.Lstat(statePath(fixture.networkDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed provisioning published ready state: %v", err)
	}

	fixture.client.RunError = nil
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil ||
		!strings.Contains(err.Error(), "network-stop") {
		t.Fatalf("incomplete restart error = %v", err)
	}
	if got := countCalls(fixture.client.Calls, "create:"); got != 1 {
		t.Fatalf("incomplete restart created another enclave: %v", fixture.client.Calls)
	}
	if got := countCalls(fixture.client.Calls, "run:"); got != 1 {
		t.Fatalf("incomplete restart replayed package: %v", fixture.client.Calls)
	}
}

func TestAmbiguousCreateRetainsIntentAndCannotReplayOrNameClean(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.CreateError = errors.New("create response lost")
	fixture.client.CreateAfterError = true
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil ||
		!strings.Contains(err.Error(), "will not be replayed") {
		t.Fatalf("ambiguous create error = %v", err)
	}
	ownership, err := loadOwnership(fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if ownership.UUID != "" || ownership.Name != fixture.client.Enclave.Name {
		t.Fatalf("ambiguous ownership = %+v", ownership)
	}
	fixture.client.CreateError = nil
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil ||
		!strings.Contains(err.Error(), "exact UUID") {
		t.Fatalf("ambiguous restart error = %v", err)
	}
	if got := countCalls(fixture.client.Calls, "create:"); got != 1 {
		t.Fatalf("ambiguous start replayed create: %v", fixture.client.Calls)
	}
	if _, err := fixture.manager.Stop(context.Background(), fixture.networkDir); err == nil ||
		!strings.Contains(err.Error(), "exact UUID") {
		t.Fatalf("ambiguous stop error = %v", err)
	}
	if fixture.client.Destroyed {
		t.Fatal("name-only creation intent destroyed an enclave")
	}
}

func TestUnexpectedReturnedIdentityIsCleanedByExactUUID(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name += "-unexpected"
	startCtx, cancel := context.WithCancel(context.Background())
	fixture.manager.NewClient = func() (kurtosis.Client, error) {
		return cancelAfterCreateClient{Fake: fixture.client, Cancel: cancel}, nil
	}
	if _, err := fixture.manager.Start(startCtx, fixture.request); err == nil ||
		!strings.Contains(err.Error(), "unexpected") {
		t.Fatalf("unexpected identity error = %v", err)
	}
	if !fixture.client.Destroyed || countCalls(fixture.client.Calls, "destroy:") != 1 {
		t.Fatalf("unexpected identity cleanup: destroyed=%t calls=%v", fixture.client.Destroyed, fixture.client.Calls)
	}
	if _, err := os.Lstat(ownershipPath(fixture.networkDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("creation intent remains after exact cleanup: %v", err)
	}
}

type cancelAfterCreateClient struct {
	*kurtosis.Fake
	Cancel context.CancelFunc
}

func (client cancelAfterCreateClient) CreateEnclave(ctx context.Context, name string) (kurtosis.EnclaveRef, error) {
	enclave, err := client.Fake.CreateEnclave(ctx, name)
	client.Cancel()
	return enclave, err
}

func (client cancelAfterCreateClient) EnclaveExists(ctx context.Context, uuid string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return client.Fake.EnclaveExists(ctx, uuid)
}

func TestCaptureAndCleanupFailureRetainsExactRecoveryOwnership(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.DestroyError = errors.New("cleanup failed")
	captures := 0
	fixture.manager.Capture = func(record OwnershipRecord) error {
		captures++
		if captures == 1 {
			return errors.New("capture failed")
		}
		return captureOwnership(record)
	}
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil ||
		!strings.Contains(err.Error(), "capture failed") ||
		!strings.Contains(err.Error(), "cleanup failed") {
		t.Fatalf("capture/cleanup error = %v", err)
	}
	ownership, err := loadOwnership(fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if ownership.UUID != fixture.client.Enclave.UUID {
		t.Fatalf("recovery ownership = %+v", ownership)
	}
	fixture.client.DestroyError = nil
	if _, err := fixture.manager.Stop(context.Background(), fixture.networkDir); err != nil {
		t.Fatalf("stop exact recovery ownership: %v", err)
	}
}

func TestStopUsesExactOwnershipEvenWhenReadyStateIsUnreadable(t *testing.T) {
	fixture := newManagerFixture(t)
	_, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath(fixture.networkDir), []byte("{not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Stop(context.Background(), fixture.networkDir); err != nil {
		t.Fatal(err)
	}
	if got := countCalls(fixture.client.Calls, "destroy:"+fixture.client.Enclave.UUID); got != 1 {
		t.Fatalf("exact destroy calls = %d, calls=%v", got, fixture.client.Calls)
	}
	for _, path := range []string{statePath(fixture.networkDir), ownershipPath(fixture.networkDir)} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s remains after stop: %v", path, err)
		}
	}
	result, err := fixture.manager.Stop(context.Background(), fixture.networkDir)
	if err != nil || result.Message != "network is not running" {
		t.Fatalf("idempotent stop = %+v, err=%v", result, err)
	}
}

type enclaveDriftClient struct{ *kurtosis.Fake }

func (client enclaveDriftClient) GetEnclave(context.Context, string) (kurtosis.EnclaveRef, error) {
	return kurtosis.EnclaveRef{
		Name:  client.Enclave.Name + "-other",
		UUID:  client.Enclave.UUID,
		Owned: true,
	}, nil
}

func TestStopRefusesEnclaveIdentityMismatch(t *testing.T) {
	fixture := newManagerFixture(t)
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	fixture.manager.NewClient = func() (kurtosis.Client, error) {
		return enclaveDriftClient{fixture.client}, nil
	}
	if _, err := fixture.manager.Stop(context.Background(), fixture.networkDir); err == nil ||
		!strings.Contains(err.Error(), "name/UUID changed") {
		t.Fatalf("mismatch stop error = %v", err)
	}
	if fixture.client.Destroyed {
		t.Fatal("mismatched enclave was destroyed")
	}
	if _, err := loadOwnership(fixture.networkDir); err != nil {
		t.Fatalf("ownership was removed after refused stop: %v", err)
	}
}

func TestStopSafelyReconcilesLostDestroyResponseAndIncompleteNetwork(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.RunError = errors.New("package failed")
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil {
		t.Fatal("failed provisioning unexpectedly succeeded")
	}
	fixture.client.DestroyError = errors.New("destroy response lost")
	fixture.client.DestroyAfterError = true

	result, err := fixture.manager.Stop(context.Background(), fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Ready || !fixture.client.Destroyed {
		t.Fatalf("safe stop = %+v, destroyed=%t", result, fixture.client.Destroyed)
	}
	if got := countCalls(fixture.client.Calls, "destroy:"); got != 1 {
		t.Fatalf("destroy calls = %d, want 1: %v", got, fixture.client.Calls)
	}
	if _, err := os.Lstat(ownershipPath(fixture.networkDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("ownership remains after confirmed destroy: %v", err)
	}
}

func countCalls(calls []string, prefix string) int {
	count := 0
	for _, call := range calls {
		if strings.HasPrefix(call, prefix) {
			count++
		}
	}
	return count
}
