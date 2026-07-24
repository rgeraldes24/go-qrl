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
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/source"
)

type blockingCreateClient struct {
	*kurtosis.Fake
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (client *blockingCreateClient) CreateEnclave(ctx context.Context, name string) (kurtosis.EnclaveRef, error) {
	client.once.Do(func() { close(client.entered) })
	select {
	case <-client.release:
		return client.Fake.CreateEnclave(ctx, name)
	case <-ctx.Done():
		return kurtosis.EnclaveRef{}, ctx.Err()
	}
}

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
	prepared   preparedNetwork
	repoRoot   string
	networkDir string
	commit     string
}

func newManagerFixture(t *testing.T) managerFixture {
	t.Helper()
	repoRoot, err := filepath.Abs("../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	return newManagerFixtureForSource(t, repoRoot)
}

func newManagerFixtureForSource(t *testing.T, repoRoot string) managerFixture {
	t.Helper()
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
	binaryDigest := strings.Repeat("5", 64)
	client := &kurtosis.Fake{
		Enclave:           kurtosis.EnclaveRef{Name: defaultEnclaveName(networkDir, commit), UUID: strings.Repeat("a", 32), Owned: true},
		RetainedPackageID: packageID,
		ServiceList:       services,
		ExecResults: map[string]kurtosis.ExecResult{
			fmt.Sprint([]string{"sha256sum", executionBinaryPath}): {Output: binaryDigest + "  " + executionBinaryPath + "\n"},
			fmt.Sprint([]string{executionBinaryPath, "version"}):   {Output: "GQRL\nGit Commit: " + commit + "\n"},
		},
	}
	prepared := preparedNetwork{Images: images}
	commands := imageCommandRunner{images: map[string]ImageIdentity{}}
	for _, image := range images {
		commands.images[image.Role] = image
	}
	manager := &Manager{
		NewClient: func() (kurtosis.Client, error) { return client, nil }, Commands: commands,
		Now: func() time.Time { return time.Unix(100, 0).UTC() }, Getenv: func(string) string { return "docker" },
		Stdout: io.Discard, Stderr: io.Discard,
		Probe: func(context.Context, probeRequest) (probeResult, error) {
			return probeResult{ChainID: "0x539", GenesisHash: "0x" + strings.Repeat("6", 64)}, nil
		},
	}
	manager.Prepare = func(_ context.Context, _ commandRunner, request StartRequest, _ SourceIdentity, wallet WalletIdentity, _, _ io.Writer) (preparedNetwork, error) {
		value := fmt.Sprintf(`{"address":%q}`, wallet.Address)
		prepared.Params, prepared.ParamsDigest = value, digestCanonicalJSON(value)
		if err := writePrivateFile(filepath.Join(privatePath(request.NetworkDir), "effective-params.json"), []byte(value+"\n")); err != nil {
			return preparedNetwork{}, err
		}
		return prepared, nil
	}
	request := StartRequest{
		RepoRoot: repoRoot, NetworkDir: networkDir,
		BuildTool: "/unused/build", DockerBin: "docker", StartTimeout: time.Minute,
	}
	return managerFixture{manager: manager, client: client, request: request, prepared: prepared, repoRoot: repoRoot, networkDir: networkDir, commit: commit}
}

func TestStartReplaysOnlyWhenSDKProvesPackageIntentWasNotAccepted(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	fixture.client.RunError = errors.New("package response lost")
	_, err := fixture.manager.Start(context.Background(), fixture.request)
	if err == nil {
		t.Fatal("lost package response unexpectedly completed")
	}
	record, err := loadLifecycle(fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if record.Phase != LifecyclePackageIntent || fixture.client.Destroyed {
		t.Fatalf("journal=%+v destroyed=%t", record, fixture.client.Destroyed)
	}
	if countCalls(fixture.client.Calls, "create:") != 1 || countCalls(fixture.client.Calls, "run:") != 1 {
		t.Fatalf("first calls = %v", fixture.client.Calls)
	}

	fixture.client.RunError = nil
	result, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Phase != PhaseRunning || countCalls(fixture.client.Calls, "create:") != 1 || countCalls(fixture.client.Calls, "run:") != 2 || countCalls(fixture.client.Calls, "destroy:") != 0 {
		t.Fatalf("resumed state=%+v calls=%v", result.State, fixture.client.Calls)
	}
	assertSeparatedPackageIdentity(t, fixture, result.State.Package, 2)
	record, err = loadLifecycle(fixture.networkDir)
	if err != nil || record.Phase != LifecycleReady || record.NetworkFingerprint != result.State.Fingerprint {
		t.Fatalf("ready journal=%+v err=%v", record, err)
	}
}

func TestStartDoesNotReplayPackageAfterLostResponseRetainsInvocation(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	fixture.client.RunError = errors.New("package response lost after acceptance")
	fixture.client.RetainInvocationOnRunError = true

	first, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if first.State.Phase != PhaseRunning || countCalls(fixture.client.Calls, "create:") != 1 || countCalls(fixture.client.Calls, "run:") != 1 {
		t.Fatalf("response-loss reconciliation state=%+v calls=%v", first.State, fixture.client.Calls)
	}
	assertSeparatedPackageIdentity(t, fixture, first.State.Package, 1)

	fixture.client.RunError = nil
	second, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if second.State.Fingerprint != first.State.Fingerprint || countCalls(fixture.client.Calls, "create:") != 1 || countCalls(fixture.client.Calls, "run:") != 1 {
		t.Fatalf("rerun replayed retained package invocation: state=%+v calls=%v", second.State, fixture.client.Calls)
	}
}

func TestStartRetryReconcilesRetainedPackageIDWithoutReplayingLocator(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	fixture.client.RunError = errors.New("package response and immediate reconciliation lost")
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil {
		t.Fatal("lost package response unexpectedly completed")
	}
	if len(fixture.client.Runs) != 1 {
		t.Fatalf("first package calls = %d; want 1", len(fixture.client.Runs))
	}

	fixture.client.RunError = nil
	fixture.client.Invocation = kurtosis.PackageInvocation{
		ID:               packageID,
		SerializedParams: fixture.client.Runs[0].SerializedParams,
	}
	result, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	assertSeparatedPackageIdentity(t, fixture, result.State.Package, 1)
}

func TestConcurrentStartAndStopFailBusyWithoutExternalMutation(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	blocking := &blockingCreateClient{Fake: fixture.client, entered: make(chan struct{}), release: make(chan struct{})}
	fixture.manager.NewClient = func() (kurtosis.Client, error) { return blocking, nil }
	type outcome struct {
		result Result
		err    error
	}
	firstDone := make(chan outcome, 1)
	go func() {
		result, err := fixture.manager.Start(context.Background(), fixture.request)
		firstDone <- outcome{result: result, err: err}
	}()
	select {
	case <-blocking.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("first Start did not reach enclave creation")
	}

	secondClient := &kurtosis.Fake{RetainedPackageID: packageID}
	secondManager := *fixture.manager
	secondManager.NewClient = func() (kurtosis.Client, error) { return secondClient, nil }
	if _, err := secondManager.Start(context.Background(), fixture.request); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("concurrent Start error = %v", err)
	}
	if _, err := secondManager.Stop(context.Background(), fixture.networkDir); err == nil || !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("concurrent Stop error = %v", err)
	}
	if countCalls(secondClient.Calls, "create:") != 0 || countCalls(secondClient.Calls, "run:") != 0 || countCalls(secondClient.Calls, "destroy:") != 0 {
		t.Fatalf("busy lifecycle reached backend mutation: %v", secondClient.Calls)
	}
	close(blocking.release)
	finished := <-firstDone
	if finished.err != nil || finished.result.State.Phase != PhaseRunning {
		t.Fatalf("first Start result=%+v err=%v", finished.result, finished.err)
	}
}

func TestCreateResponseLossLeavesAmbiguousIntentAndNeverReplaysByName(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	fixture.client.CreateError = errors.New("create response lost")
	fixture.client.CreateAfterError = true
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("create response-loss error = %v", err)
	}
	lifecycle, err := loadLifecycle(fixture.networkDir)
	if err != nil || lifecycle.Enclave != nil || lifecycle.EnclaveCapturedAt != nil {
		t.Fatalf("ambiguous lifecycle=%+v err=%v", lifecycle, err)
	}
	before, err := os.ReadFile(lifecyclePath(fixture.networkDir))
	if err != nil {
		t.Fatal(err)
	}
	if err := createLifecycle(lifecycle); !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive intent collision error = %v", err)
	}
	after, err := os.ReadFile(lifecyclePath(fixture.networkDir))
	if err != nil || string(after) != string(before) {
		t.Fatalf("intent collision overwrote lifecycle: err=%v", err)
	}

	fixture.client.CreateError = nil
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil || !strings.Contains(err.Error(), "refusing to replay create") {
		t.Fatalf("ambiguous Start replay error = %v", err)
	}
	if _, err := fixture.manager.Stop(context.Background(), fixture.networkDir); err == nil || !strings.Contains(err.Error(), "name-only") {
		t.Fatalf("name-only Stop error = %v", err)
	}
	if countCalls(fixture.client.Calls, "create:") != 1 || countCalls(fixture.client.Calls, "run:") != 0 || countCalls(fixture.client.Calls, "destroy:") != 0 {
		t.Fatalf("ambiguous creation was replayed or cleaned by name: %v", fixture.client.Calls)
	}
}

func TestStartResumesCrashGapAfterExactCreationUUIDCapture(t *testing.T) {
	fixture := newManagerFixture(t)
	prepareCapturedCreationGap(t, &fixture)

	result, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if result.State.Enclave.UUID != fixture.client.Enclave.UUID || countCalls(fixture.client.Calls, "create:") != 0 || countCalls(fixture.client.Calls, "run:") != 1 {
		t.Fatalf("captured creation was not resumed exactly: state=%+v calls=%v", result.State.Enclave, fixture.client.Calls)
	}
}

func TestStopUsesCapturedCreationUUIDBeforeProvisionAndReconcilesResponseLoss(t *testing.T) {
	fixture := newManagerFixture(t)
	lifecycle := prepareCapturedCreationGap(t, &fixture)
	fixture.client.DestroyError = errors.New("destroy response lost")
	fixture.client.DestroyAfterError = true

	result, err := fixture.manager.Stop(context.Background(), fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if result.Lifecycle == nil || result.Lifecycle.Enclave == nil || result.Lifecycle.Enclave.UUID != fixture.client.Enclave.UUID || countCalls(fixture.client.Calls, "destroy:") != 1 {
		t.Fatalf("captured creation stop=%+v calls=%v", result, fixture.client.Calls)
	}
	stopped, err := loadLifecycle(fixture.networkDir)
	if err != nil || stopped.DestroyRequestedAt == nil || stopped.DestroyedAt == nil || lifecycle.Enclave == nil {
		t.Fatalf("captured creation lifecycle=%+v initial=%+v err=%v", stopped, lifecycle, err)
	}
	if _, err := fixture.manager.Stop(context.Background(), fixture.networkDir); err != nil {
		t.Fatal(err)
	}
	if countCalls(fixture.client.Calls, "destroy:") != 1 {
		t.Fatalf("reconciled captured creation was destroyed twice: %v", fixture.client.Calls)
	}
}

func prepareCapturedCreationGap(t *testing.T, fixture *managerFixture) LifecycleRecord {
	t.Helper()
	fixture.manager.normalize()
	networkDir, err := ensureNetworkDirectory(fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	sourceIdentity := SourceIdentity{Commit: fixture.commit}
	wallet, err := fixture.manager.Wallet(privatePath(networkDir))
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := fixture.manager.Prepare(context.Background(), fixture.manager.Commands, fixture.request, sourceIdentity, wallet, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	name := defaultEnclaveName(networkDir, fixture.commit)
	lifecycle := lifecycleFromPrepared(networkDir, name, sourceIdentity, prepared, fixture.manager.Now().UTC())
	if err := createLifecycle(lifecycle); err != nil {
		t.Fatal(err)
	}
	fixture.client.Enclave = kurtosis.EnclaveRef{Name: name, UUID: strings.Repeat("a", 32), Owned: true}
	capturedAt := fixture.manager.Now().UTC()
	lifecycle.Phase, lifecycle.Enclave, lifecycle.EnclaveCapturedAt = LifecyclePackageIntent, &fixture.client.Enclave, &capturedAt
	if err := writeLifecycle(lifecycle); err != nil {
		t.Fatal(err)
	}
	return lifecycle
}

func TestStatusIsReadOnlyAndStopUsesExactUUID(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	result, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	beforeCreate := countCalls(fixture.client.Calls, "create:")
	beforeRun := countCalls(fixture.client.Calls, "run:")
	beforeDestroy := countCalls(fixture.client.Calls, "destroy:")
	status, err := fixture.manager.Status(context.Background(), fixture.networkDir)
	if err != nil || status.State.Fingerprint != result.State.Fingerprint {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	if countCalls(fixture.client.Calls, "create:") != beforeCreate || countCalls(fixture.client.Calls, "run:") != beforeRun || countCalls(fixture.client.Calls, "destroy:") != beforeDestroy {
		t.Fatalf("status mutated lifecycle: %v", fixture.client.Calls)
	}
	stopped, err := fixture.manager.Stop(context.Background(), fixture.networkDir)
	if err != nil || stopped.State.Phase != PhaseStopped || !fixture.client.Destroyed {
		t.Fatalf("stop=%+v destroyed=%t err=%v", stopped, fixture.client.Destroyed, err)
	}
	if last := fixture.client.Calls[len(fixture.client.Calls)-1]; last != "destroy:"+result.State.Enclave.UUID {
		t.Fatalf("last call = %q", last)
	}
}

func TestStatusAuthenticatesBinaryDigestAndReportedCommitWithoutPersistingVersionText(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	running, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	versionCommand := fmt.Sprint([]string{running.State.Execution.BinaryPath, "version"})
	fixture.client.ExecResults[versionCommand] = kurtosis.ExecResult{
		Output: "GQRL changed presentation\nGit Commit: " + fixture.commit + "\nBuild Date: later\n",
	}
	if _, err := fixture.manager.Status(context.Background(), fixture.networkDir); err != nil {
		t.Fatalf("status rejected changed non-identity version text: %v", err)
	}

	fixture.client.ExecResults[versionCommand] = kurtosis.ExecResult{
		Output: "GQRL\nGit Commit: " + strings.Repeat("9", 40) + "\n",
	}
	if _, err := fixture.manager.Status(context.Background(), fixture.networkDir); err == nil ||
		!strings.Contains(err.Error(), "does not report full Git Commit") {
		t.Fatalf("reported-commit drift error = %v", err)
	}

	hashCommand := fmt.Sprint([]string{"sha256sum", running.State.Execution.BinaryPath})
	fixture.client.ExecResults[versionCommand] = kurtosis.ExecResult{
		Output: "GQRL\nGit Commit: " + fixture.commit + "\n",
	}
	fixture.client.ExecResults[hashCommand] = kurtosis.ExecResult{
		Output: strings.Repeat("8", 64) + "  " + running.State.Execution.BinaryPath + "\n",
	}
	if _, err := fixture.manager.Status(context.Background(), fixture.networkDir); err == nil ||
		!strings.Contains(err.Error(), "binary digest changed") {
		t.Fatalf("binary-digest drift error = %v", err)
	}
}

func TestAuthenticationRejectsPendingDestroyLifecycle(t *testing.T) {
	repoRoot := newCommittedSourceRepository(t)
	fixture := newManagerFixtureForSource(t, repoRoot)
	fixture.client.Enclave.Name = ""
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	lifecycle, err := loadLifecycle(fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	requestedAt := fixture.manager.Now().UTC()
	lifecycle.Phase, lifecycle.DestroyRequestedAt = LifecycleDestroyIntent, &requestedAt
	if err := writeLifecycle(lifecycle); err != nil {
		t.Fatal(err)
	}
	status, err := fixture.manager.Status(context.Background(), fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready || countCalls(fixture.client.Calls, "destroy:") != 0 {
		t.Fatalf("pending destroy status=%+v calls=%v", status, fixture.client.Calls)
	}
	if _, err := fixture.manager.Authenticate(context.Background(), repoRoot, fixture.networkDir, FullRequirements()); err == nil ||
		!strings.Contains(err.Error(), "not ready") {
		t.Fatalf("pending destroy authentication error = %v", err)
	}
}

func TestStatusLeavesReadyPublishGapForLockedStartToReconcile(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	running, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle, err := loadLifecycle(fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	lifecycle.Phase, lifecycle.NetworkFingerprint = LifecyclePackageAccepted, ""
	if err := writeLifecycle(lifecycle); err != nil {
		t.Fatal(err)
	}

	status, err := fixture.manager.Status(context.Background(), fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if status.Ready {
		t.Fatalf("publish-gap status unexpectedly ready: %+v", status)
	}
	unchanged, err := loadLifecycle(fixture.networkDir)
	if err != nil || unchanged.Phase != LifecyclePackageAccepted || unchanged.NetworkFingerprint != "" {
		t.Fatalf("Status mutated lifecycle: %+v err=%v", unchanged, err)
	}

	resumed, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State.Fingerprint != running.State.Fingerprint ||
		countCalls(fixture.client.Calls, "create:") != 1 ||
		countCalls(fixture.client.Calls, "run:") != 1 {
		t.Fatalf("locked reconciliation state=%+v calls=%v", resumed.State, fixture.client.Calls)
	}
	reconciled, err := loadLifecycle(fixture.networkDir)
	if err != nil || reconciled.Phase != LifecycleReady ||
		reconciled.NetworkFingerprint != running.State.Fingerprint {
		t.Fatalf("reconciled lifecycle=%+v err=%v", reconciled, err)
	}
}

func TestAuthenticateAcceptsSameCommitE2EOnlyTreeDrift(t *testing.T) {
	repoRoot := newCommittedSourceRepository(t)
	fixture := newManagerFixtureForSource(t, repoRoot)
	fixture.client.Enclave.Name = ""
	running, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	repairDirectory := filepath.Join(repoRoot, "scripts", "testing", "e2e")
	if err := os.MkdirAll(repairDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repairDirectory, "repaired_test.go"), []byte("package repaired\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	environment, err := fixture.manager.Authenticate(context.Background(), repoRoot, fixture.networkDir, FullRequirements())
	if err != nil {
		t.Fatal(err)
	}
	if environment.NetworkDir != running.State.NetworkDir {
		t.Fatalf("authenticated network directory = %s, want %s", environment.NetworkDir, running.State.NetworkDir)
	}
	reloaded, err := loadState(fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Source != running.State.Source {
		t.Fatalf("authentication mutated runtime source: have %+v want %+v", reloaded.Source, running.State.Source)
	}
}

func TestAuthenticateUsesOnlyRequestedCapabilities(t *testing.T) {
	repoRoot := newCommittedSourceRepository(t)
	fixture := newManagerFixtureForSource(t, repoRoot)
	fixture.client.Enclave.Name = ""
	running, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fullEnvironment, err := fixture.manager.Authenticate(
		context.Background(),
		fixture.repoRoot,
		fixture.networkDir,
		FullRequirements(),
	)
	if err != nil {
		t.Fatal(err)
	}
	seedPath := walletSeedPath(running.State.NetworkDir)
	if fullEnvironment.SeedFile != seedPath {
		t.Fatalf("derived signer seed path = %q, want %q", fullEnvironment.SeedFile, seedPath)
	}
	var observed Requirements
	fixture.manager.Probe = func(_ context.Context, request probeRequest) (probeResult, error) {
		observed = request.Requirements
		return probeResult{
			ChainID: running.State.Chain.ChainID, GenesisHash: running.State.Chain.GenesisHash,
		}, nil
	}
	if err := os.Rename(seedPath, seedPath+".unavailable"); err != nil {
		t.Fatal(err)
	}

	requirements := Requirements{GraphQL: true}
	environment, err := fixture.manager.Authenticate(
		context.Background(),
		fixture.repoRoot,
		fixture.networkDir,
		requirements,
	)
	if err != nil {
		t.Fatal(err)
	}
	if observed != requirements {
		t.Fatalf("probe requirements = %+v, want %+v", observed, requirements)
	}
	if environment.RPCURL == "" || environment.GraphQLURL == "" ||
		environment.SeedFile != "" || environment.WebSocketURL != "" {
		t.Fatalf("least-privilege environment = %+v", environment)
	}
	if environment.NetworkDir != running.State.NetworkDir {
		t.Fatalf("authenticated network directory = %s, want %s", environment.NetworkDir, running.State.NetworkDir)
	}
	if _, err := fixture.manager.Status(context.Background(), fixture.networkDir); err == nil ||
		!strings.Contains(err.Error(), "private wallet") {
		t.Fatalf("full public status did not retain signer validation: %v", err)
	}
}

func TestAuthenticateRejectsDifferentCheckoutCommit(t *testing.T) {
	repoRoot := newCommittedSourceRepository(t)
	fixture := newManagerFixtureForSource(t, repoRoot)
	fixture.client.Enclave.Name = ""
	running, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repoRoot, "source.txt"), []byte("second revision\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, repoRoot, "add", "source.txt")
	runGit(t, repoRoot, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "second")
	commit, err := source.Commit(context.Background(), nil, repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if commit == running.State.Source.Commit {
		t.Fatal("second test commit did not change HEAD")
	}

	_, err = fixture.manager.Authenticate(context.Background(), repoRoot, fixture.networkDir, FullRequirements())
	if err == nil || !strings.Contains(err.Error(), "source commit differs") {
		t.Fatalf("different-commit authentication error = %v", err)
	}
}

func TestAuthenticateRejectsSameCommitRuntimeTreeDrift(t *testing.T) {
	tests := map[string]func(*testing.T, string){
		"tracked accounts ABI": func(t *testing.T, repoRoot string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(repoRoot, "accounts", "abi", "runtime.go"), []byte("package abi\n// changed\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"staged network build script": func(t *testing.T, repoRoot string) {
			t.Helper()
			path := filepath.Join(repoRoot, "scripts", "local_testnet", "build_network_images.sh")
			if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\n# changed\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			runGit(t, repoRoot, "add", "scripts/local_testnet/build_network_images.sh")
		},
		"untracked root test": func(t *testing.T, repoRoot string) {
			t.Helper()
			if err := os.WriteFile(filepath.Join(repoRoot, "unexpected_test.go"), []byte("package unexpected\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			repoRoot := newCommittedSourceRepository(t)
			fixture := newManagerFixtureForSource(t, repoRoot)
			fixture.client.Enclave.Name = ""
			if _, err := fixture.manager.Start(context.Background(), fixture.request); err != nil {
				t.Fatal(err)
			}
			mutate(t, repoRoot)
			_, err := fixture.manager.Authenticate(context.Background(), repoRoot, fixture.networkDir, FullRequirements())
			if err == nil || !strings.Contains(err.Error(), "runtime-affecting same-commit change") {
				t.Fatalf("runtime-tree authentication error = %v", err)
			}
		})
	}
}

func TestStopConsumesLifecycleBeforeFinalState(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	fixture.client.RunError = errors.New("package unavailable")
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil {
		t.Fatal("failed start unexpectedly succeeded")
	}
	result, err := fixture.manager.Stop(context.Background(), fixture.networkDir)
	if err != nil || result.Lifecycle == nil || result.Lifecycle.Phase != LifecycleStopped || !fixture.client.Destroyed {
		t.Fatalf("lifecycle stop=%+v destroyed=%t err=%v", result, fixture.client.Destroyed, err)
	}
	if _, err := os.Lstat(statePath(fixture.networkDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial stop wrote final network state: %v", err)
	}
}

func TestStopReconcilesLostSuccessfulDestroyResponse(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	_, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	fixture.client.DestroyError = errors.New("destroy response lost")
	fixture.client.DestroyAfterError = true

	stopped, err := fixture.manager.Stop(context.Background(), fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State.Phase != PhaseStopped || countCalls(fixture.client.Calls, "destroy:") != 1 {
		t.Fatalf("lost destroy response was not reconciled: state=%+v calls=%v", stopped.State, fixture.client.Calls)
	}
	record, err := loadLifecycle(fixture.networkDir)
	if err != nil || record.DestroyRequestedAt == nil || record.DestroyedAt == nil {
		t.Fatalf("destroy lifecycle=%+v err=%v", record, err)
	}
	if _, err := fixture.manager.Stop(context.Background(), fixture.networkDir); err != nil {
		t.Fatal(err)
	}
	if countCalls(fixture.client.Calls, "destroy:") != 1 {
		t.Fatalf("completed destroy was repeated: %v", fixture.client.Calls)
	}
}

func TestStopResumesDurableDestroyRequestWhenEnclaveAlreadyAbsent(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	running, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	requestedAt := fixture.manager.Now().UTC()
	record, err := loadLifecycle(fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	record.Phase, record.NetworkFingerprint, record.DestroyRequestedAt = LifecycleDestroyIntent, running.State.Fingerprint, &requestedAt
	if err := writeLifecycle(record); err != nil {
		t.Fatal(err)
	}
	// Model an accepted destroy followed by process death before DestroyedAt
	// and the public stopped phase could be persisted.
	fixture.client.Destroyed = true

	stopped, err := fixture.manager.Stop(context.Background(), fixture.networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.State.Phase != PhaseStopped || countCalls(fixture.client.Calls, "destroy:") != 0 {
		t.Fatalf("already-absent enclave was destroyed again: state=%+v calls=%v", stopped.State, fixture.client.Calls)
	}
	record, err = loadLifecycle(fixture.networkDir)
	if err != nil || record.DestroyedAt == nil {
		t.Fatalf("reconciled destroy lifecycle=%+v err=%v", record, err)
	}
}

func TestRestartAfterStopRetiresOldStateAndResumesNewJournal(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	first, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.manager.Stop(context.Background(), fixture.networkDir); err != nil {
		t.Fatal(err)
	}
	fixture.client.Enclave = kurtosis.EnclaveRef{Name: "", UUID: strings.Repeat("e", 32), Owned: true}
	fixture.client.Invocation = kurtosis.PackageInvocation{}
	fixture.client.Destroyed = false
	fixture.client.RunError = errors.New("replacement package response lost")
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err == nil {
		t.Fatal("replacement failure unexpectedly succeeded")
	}
	if _, err := os.Lstat(statePath(fixture.networkDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old stopped state still masks replacement journal: %v", err)
	}
	record, err := loadLifecycle(fixture.networkDir)
	if err != nil || record.Phase != LifecyclePackageIntent || record.Enclave == nil || record.Enclave.UUID == first.State.Enclave.UUID {
		t.Fatalf("replacement lifecycle=%+v err=%v", record, err)
	}
	if countCalls(fixture.client.Calls, "create:") != 2 {
		t.Fatalf("replacement create calls = %v", fixture.client.Calls)
	}

	fixture.client.RunError = nil
	replacement, err := fixture.manager.Start(context.Background(), fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.State.Enclave.UUID != strings.Repeat("e", 32) || countCalls(fixture.client.Calls, "create:") != 2 {
		t.Fatalf("replacement state=%+v calls=%v", replacement.State.Enclave, fixture.client.Calls)
	}
	if _, err := fixture.manager.Stop(context.Background(), fixture.networkDir); err != nil {
		t.Fatal(err)
	}
	if countCalls(fixture.client.Calls, "destroy:") != 2 {
		t.Fatalf("destroy calls = %v", fixture.client.Calls)
	}
}

type enclaveDriftClient struct{ *kurtosis.Fake }

func (client enclaveDriftClient) GetEnclave(context.Context, string) (kurtosis.EnclaveRef, error) {
	return kurtosis.EnclaveRef{Name: client.Enclave.Name, UUID: strings.Repeat("f", 32)}, nil
}

func TestStatusRejectsSameNameWithDifferentEnclaveUUID(t *testing.T) {
	fixture := newManagerFixture(t)
	fixture.client.Enclave.Name = ""
	if _, err := fixture.manager.Start(context.Background(), fixture.request); err != nil {
		t.Fatal(err)
	}
	fixture.manager.NewClient = func() (kurtosis.Client, error) { return enclaveDriftClient{fixture.client}, nil }
	if _, err := fixture.manager.Status(context.Background(), fixture.networkDir); err == nil || !strings.Contains(err.Error(), "name/UUID") {
		t.Fatalf("UUID drift error = %v", err)
	}
}

func TestStatusFailureReturnsSanitizedTransientState(t *testing.T) {
	const backendSecret = "raw-package-output-should-not-persist"
	for name, mutate := range map[string]func(*managerFixture) string{
		"UUID drift": func(fixture *managerFixture) string {
			fixture.manager.NewClient = func() (kurtosis.Client, error) { return enclaveDriftClient{fixture.client}, nil }
			return "name/UUID"
		},
		"image drift": func(fixture *managerFixture) string {
			runner := fixture.manager.Commands.(imageCommandRunner)
			image := runner.images["execution"]
			image.ID = "sha256:" + strings.Repeat("9", 64)
			runner.images["execution"] = image
			return "image ID"
		},
		"package ID drift": func(fixture *managerFixture) string {
			fixture.client.Invocation.ID = "github.com/example/other-package"
			return "package ID"
		},
		"RPC failure": func(fixture *managerFixture) string {
			fixture.manager.Probe = func(context.Context, probeRequest) (probeResult, error) {
				return probeResult{}, errors.New(backendSecret)
			}
			return backendSecret
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newManagerFixture(t)
			fixture.client.Enclave.Name = ""
			running, err := fixture.manager.Start(context.Background(), fixture.request)
			if err != nil {
				t.Fatal(err)
			}
			originalNeedle := mutate(&fixture)
			status, err := fixture.manager.Status(context.Background(), fixture.networkDir)
			if err == nil || !strings.Contains(err.Error(), originalNeedle) {
				t.Fatalf("Status original error = %v", err)
			}
			if status.Ready || status.State.Fingerprint != running.State.Fingerprint ||
				strings.Contains(status.Message, backendSecret) ||
				!strings.Contains(status.Message, "authentication failed") {
				t.Fatalf("stale or unsanitized status=%+v", status)
			}
			if _, statErr := os.Lstat(filepath.Join(fixture.networkDir, "diagnostics.json")); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("status persisted diagnostics: %v", statErr)
			}
		})
	}
}

func assertSeparatedPackageIdentity(t *testing.T, fixture managerFixture, identity PackageIdentity, runCount int) {
	t.Helper()
	wantLocator, wantID := packageLocator, packageID
	if identity.Locator != wantLocator || identity.ID != wantID || !strings.Contains(identity.Locator, "@") || strings.Contains(identity.ID, "@") {
		t.Fatalf("package identity = %+v; want locator %q and retained ID %q", identity, wantLocator, wantID)
	}
	if fixture.client.Invocation.ID != wantID {
		t.Fatalf("retained invocation ID = %q; want %q", fixture.client.Invocation.ID, wantID)
	}
	if len(fixture.client.Runs) != runCount {
		t.Fatalf("package runs = %d; want %d", len(fixture.client.Runs), runCount)
	}
	for index, run := range fixture.client.Runs {
		if run.Locator != wantLocator {
			t.Fatalf("package run %d locator = %q; want %q", index, run.Locator, wantLocator)
		}
	}
}

func newCommittedSourceRepository(t *testing.T) string {
	t.Helper()
	repoRoot := t.TempDir()
	runGit(t, repoRoot, "init", "-q")
	runGit(t, repoRoot, "config", "user.name", "E2E Test")
	runGit(t, repoRoot, "config", "user.email", "e2e@example.invalid")
	files := map[string]string{
		"source.txt":              "initial revision\n",
		"accounts/abi/runtime.go": "package abi\n",
		"scripts/local_testnet/build_network_images.sh": "#!/usr/bin/env bash\n",
	}
	for relative, contents := range files {
		path := filepath.Join(repoRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial")
	resolved, err := filepath.EvalSymlinks(repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	return resolved
}

func runGit(t *testing.T, directory string, arguments ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
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
