// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/poll"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/source"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
)

type Manager struct {
	NewClient func() (kurtosis.Client, error)
	Commands  commandRunner
	Getenv    func(string) string
	Stdout    io.Writer
	Stderr    io.Writer
	Prepare   func(context.Context, commandRunner, StartRequest, string, string, io.Writer, io.Writer) (preparedNetwork, error)
	Probe     func(context.Context, probeRequest) (probeResult, error)
	Wallet    func(string) (string, error)
	Capture   func(OwnershipRecord) error
}

func NewManager() *Manager { return &Manager{} }

func (manager *Manager) normalize() {
	if manager.NewClient == nil {
		manager.NewClient = func() (kurtosis.Client, error) { return kurtosis.NewSDKClient() }
	}
	if manager.Commands == nil {
		manager.Commands = execRunner{}
	}
	if manager.Getenv == nil {
		manager.Getenv = os.Getenv
	}
	if manager.Stdout == nil {
		manager.Stdout = io.Discard
	}
	if manager.Stderr == nil {
		manager.Stderr = io.Discard
	}
	if manager.Prepare == nil {
		manager.Prepare = prepareNetwork
	}
	if manager.Probe == nil {
		manager.Probe = probeNetwork
	}
	if manager.Wallet == nil {
		manager.Wallet = ensureWallet
	}
	if manager.Capture == nil {
		manager.Capture = captureOwnership
	}
}

func (manager *Manager) Start(ctx context.Context, request StartRequest) (Result, error) {
	manager.normalize()
	repoRoot, err := canonicalExistingDirectory(request.RepoRoot, "repository root")
	if err != nil {
		return Result{}, err
	}
	request.RepoRoot = repoRoot
	networkDir, err := ensureNetworkDirectory(request.NetworkDir)
	if err != nil {
		return Result{}, err
	}
	request.NetworkDir = networkDir
	if relative, err := filepath.Rel(repoRoot, networkDir); err != nil {
		return Result{}, err
	} else if relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return Result{}, errors.New("network directory must not be the repository or one of its descendants")
	}
	mutation, err := acquireMutationLease(networkDir)
	if err != nil {
		return Result{}, err
	}
	defer mutation.Close()
	if request.StartTimeout <= 0 {
		request.StartTimeout = 150 * time.Minute
	}
	startCtx, cancel := context.WithTimeout(ctx, request.StartTimeout)
	defer cancel()

	readyExists, err := pathExists(statePath(networkDir))
	if err != nil {
		return Result{}, err
	}
	ownershipExists, err := pathExists(ownershipPath(networkDir))
	if err != nil {
		return Result{}, err
	}
	switch {
	case readyExists && !ownershipExists:
		return Result{}, errors.New("ready network state has no exact-UUID ownership record")
	case ownershipExists && !readyExists:
		ownership, loadErr := loadOwnership(networkDir)
		if loadErr != nil {
			return Result{}, loadErr
		}
		if _, ownershipErr := ownership.OwnedEnclave(); ownershipErr != nil {
			return Result{}, ownershipErr
		}
		return Result{}, errors.New("network provisioning is incomplete; run network-stop before starting again")
	case readyExists:
		status, err := manager.status(startCtx, networkDir)
		if err != nil {
			return Result{}, fmt.Errorf("authenticate existing network before reuse: %w", err)
		}
		commit, err := source.Commit(startCtx, nil, repoRoot)
		if err != nil {
			return Result{}, err
		}
		if status.state.SourceCommit != commit {
			return Result{}, errors.New("a different authenticated network already occupies the requested directory")
		}
		return status, nil
	}

	commit, err := source.Commit(startCtx, nil, repoRoot)
	if err != nil {
		return Result{}, err
	}
	walletAddress, err := manager.Wallet(privatePath(networkDir))
	if err != nil {
		return Result{}, fmt.Errorf("prepare private E2E wallet: %w", err)
	}
	client, err := manager.NewClient()
	if err != nil {
		return Result{}, fmt.Errorf("connect to Kurtosis engine: %w", err)
	}
	prepared, err := manager.Prepare(startCtx, manager.Commands, request, commit, walletAddress, manager.Stdout, manager.Stderr)
	if err != nil {
		return Result{}, err
	}
	name := request.EnclaveName
	if name == "" {
		name = defaultEnclaveName(networkDir, commit)
	}
	intent := OwnershipRecord{
		NetworkDir: networkDir,
		Name:       name,
	}
	if err := createOwnership(intent); err != nil {
		return Result{}, fmt.Errorf("persist enclave creation intent: %w", err)
	}
	enclave, err := client.CreateEnclave(startCtx, name)
	if err != nil {
		return Result{}, fmt.Errorf(
			"create Kurtosis enclave %q returned an ambiguous result; creation intent was retained and create will not be replayed: %w",
			name,
			err,
		)
	}
	if enclave.Name != name || !enclave.Owned || enclave.Validate() != nil {
		cleanupErr := cleanupCreatedEnclave(client, enclave)
		if cleanupErr == nil {
			cleanupErr = removeOwnership(intent)
		}
		return Result{}, errors.Join(
			errors.New("Kurtosis returned an unexpected or unowned enclave identity"),
			wrapCleanupError(cleanupErr),
		)
	}
	ownership := intent
	ownership.UUID = enclave.UUID
	if err := manager.Capture(ownership); err != nil {
		cleanupErr := cleanupCreatedEnclave(client, enclave)
		if cleanupErr == nil {
			cleanupErr = removeOwnership(intent)
		} else if recoveryErr := manager.Capture(ownership); recoveryErr != nil {
			cleanupErr = errors.Join(
				cleanupErr,
				fmt.Errorf(
					"persist recovery ownership for enclave %s/%s: %w",
					enclave.Name,
					enclave.UUID,
					recoveryErr,
				),
			)
		}
		return Result{}, errors.Join(
			fmt.Errorf("persist exact enclave ownership: %w", err),
			wrapCleanupError(cleanupErr),
		)
	}

	if err := client.RunRemotePackage(startCtx, enclave, kurtosis.PackageRun{
		Locator:          packageLocator,
		SerializedParams: prepared.Params,
	}); err != nil {
		return Result{}, fmt.Errorf("run pinned qrl-package; network ownership was retained for network-stop: %w", err)
	}
	if err := authenticateInvocation(startCtx, client, enclave, packageID, prepared.ParamsDigest); err != nil {
		return Result{}, fmt.Errorf("authenticate qrl-package invocation; network ownership was retained for network-stop: %w", err)
	}

	var snapshot topology.Snapshot
	if err := poll.Until(startCtx, 2*time.Second, func(attempt context.Context) error {
		services, err := client.Services(attempt, enclave)
		if err != nil {
			return err
		}
		discovered, err := topology.Discover(networkTopology, services)
		if err != nil {
			return err
		}
		snapshot = discovered
		return nil
	}); err != nil {
		return Result{}, fmt.Errorf("discover qrl-package topology; network ownership was retained for network-stop: %w", err)
	}
	if err := verifySnapshotImages(snapshot, prepared.Images); err != nil {
		return Result{}, err
	}
	binaryDigest, err := authenticateBinary(startCtx, client, enclave, snapshot.Execution.UUID, executionBinaryPath, commit, "")
	if err != nil {
		return Result{}, err
	}
	var readiness probeResult
	if err := poll.Until(startCtx, 2*time.Second, func(attempt context.Context) error {
		observed, err := manager.Probe(attempt, probeRequest{
			RPCURL: snapshot.RPC.URL, GraphQLURL: snapshot.GraphQL, WebSocketURL: snapshot.WebSocket.URL,
			Address: walletAddress, ExpectedChainID: expectedChainID,
		})
		if err == nil {
			readiness = observed
		}
		return err
	}); err != nil {
		return Result{}, fmt.Errorf("wait for network readiness; network ownership was retained for network-stop: %w", err)
	}
	state := State{
		ParamsSHA256:  prepared.ParamsDigest,
		SourceCommit:  commit,
		WalletAddress: walletAddress,
		Topology:      snapshot,
		Images:        slices.Clone(prepared.Images),
		BinarySHA256:  binaryDigest,
		GenesisHash:   readiness.GenesisHash,
	}
	if err := writeState(networkDir, state); err != nil {
		return Result{}, fmt.Errorf("publish ready network state; exact ownership was retained for network-stop: %w", err)
	}
	return Result{state: state, Ready: true}, nil
}

func (manager *Manager) Status(ctx context.Context, requestedDir string) (Result, error) {
	return manager.status(ctx, requestedDir)
}

func (manager *Manager) status(ctx context.Context, requestedDir string) (Result, error) {
	manager.normalize()
	networkDir, err := canonicalExistingDirectory(requestedDir, "network directory")
	if err != nil {
		return Result{}, err
	}
	readyExists, err := pathExists(statePath(networkDir))
	if err != nil {
		return Result{}, err
	}
	ownershipExists, err := pathExists(ownershipPath(networkDir))
	if err != nil {
		return Result{}, err
	}
	if !ownershipExists {
		if readyExists {
			return Result{}, errors.New("ready network state has no exact-UUID ownership record")
		}
		return Result{Message: "network is not running"}, nil
	}
	ownership, err := loadOwnership(networkDir)
	if err != nil {
		return Result{}, err
	}
	enclave, err := ownership.OwnedEnclave()
	if err != nil {
		return Result{Message: err.Error()}, nil
	}
	client, err := manager.NewClient()
	if err != nil {
		return Result{}, err
	}
	if err := authenticateOwnedEnclave(ctx, client, enclave); err != nil {
		return Result{}, err
	}
	if !readyExists {
		return Result{Message: "network provisioning is incomplete; run network-stop before starting again"}, nil
	}
	state, err := loadState(networkDir)
	if err != nil {
		return Result{}, err
	}
	if err := authenticateInvocation(ctx, client, enclave, packageID, state.ParamsSHA256); err != nil {
		return Result{}, err
	}
	services, err := client.Services(ctx, enclave)
	if err != nil {
		return Result{}, err
	}
	currentTopology, err := topology.Discover(networkTopology, services)
	if err != nil {
		return Result{}, err
	}
	if !reflect.DeepEqual(currentTopology, state.Topology) {
		return Result{}, errors.New("network service identity or endpoint changed")
	}
	if err := manager.authenticateImages(ctx, state.Images); err != nil {
		return Result{}, err
	}
	if err := verifySnapshotImages(state.Topology, state.Images); err != nil {
		return Result{}, err
	}
	walletAddress, err := validateWalletSeed(walletSeedPath(networkDir))
	if err != nil || walletAddress != state.WalletAddress {
		return Result{}, errors.New("private wallet no longer matches persisted address identity")
	}
	if _, err = authenticateBinary(ctx, client, enclave, state.Topology.Execution.UUID, executionBinaryPath, state.SourceCommit, state.BinarySHA256); err != nil {
		return Result{}, err
	}
	if _, err = manager.Probe(ctx, probeRequest{
		RPCURL: state.Topology.RPC.URL, GraphQLURL: state.Topology.GraphQL, WebSocketURL: state.Topology.WebSocket.URL,
		Address: state.WalletAddress, ExpectedChainID: expectedChainID, ExpectedGenesis: state.GenesisHash,
	}); err != nil {
		return Result{}, err
	}
	return Result{state: state, Ready: true}, nil
}

func (manager *Manager) Authenticate(
	ctx context.Context,
	repoRoot,
	networkDir string,
) (Environment, error) {
	manager.normalize()
	root, err := canonicalExistingDirectory(repoRoot, "repository root")
	if err != nil {
		return Environment{}, err
	}
	canonicalNetworkDir, err := canonicalExistingDirectory(networkDir, "network directory")
	if err != nil {
		return Environment{}, err
	}
	result, err := manager.status(ctx, canonicalNetworkDir)
	if err != nil {
		return Environment{}, err
	}
	if !result.Ready {
		return Environment{}, errors.New("E2E network is not ready")
	}
	commit, err := source.Commit(ctx, nil, root)
	if err != nil {
		return Environment{}, err
	}
	if result.state.SourceCommit != commit {
		return Environment{}, errors.New("network source commit differs from the requested checkout")
	}
	if err := source.ValidateE2EOnlyTreeDrift(ctx, nil, root); err != nil {
		return Environment{}, fmt.Errorf("checkout contains runtime-affecting same-commit changes: %w", err)
	}
	state := result.state
	environment := Environment{
		NetworkDir:   canonicalNetworkDir,
		RPCURL:       state.Topology.RPC.URL,
		GraphQLURL:   state.Topology.GraphQL,
		WebSocketURL: state.Topology.WebSocket.URL,
		SeedFile:     walletSeedPath(canonicalNetworkDir),
	}
	return environment, nil
}

func (manager *Manager) Stop(ctx context.Context, requestedDir string) (Result, error) {
	manager.normalize()
	networkDir, err := canonicalExistingDirectory(requestedDir, "network directory")
	if err != nil {
		return Result{}, err
	}
	mutation, err := acquireMutationLease(networkDir)
	if err != nil {
		return Result{}, err
	}
	defer mutation.Close()

	readyExists, err := pathExists(statePath(networkDir))
	if err != nil {
		return Result{}, err
	}
	ownershipExists, err := pathExists(ownershipPath(networkDir))
	if err != nil {
		return Result{}, err
	}
	if !ownershipExists {
		if readyExists {
			return Result{}, errors.New("refusing stop: ready network state has no exact-UUID ownership record")
		}
		return Result{Message: "network is not running"}, nil
	}
	ownership, err := loadOwnership(networkDir)
	if err != nil {
		return Result{}, err
	}
	client, err := manager.NewClient()
	if err != nil {
		return Result{}, err
	}
	enclave, err := ownership.OwnedEnclave()
	if err != nil {
		return Result{}, err
	}
	if err := destroyExactEnclave(ctx, client, enclave); err != nil {
		return Result{}, err
	}
	// Remove the public ready state first. If either removal is interrupted,
	// the private exact-UUID record remains available for an idempotent stop.
	if readyExists {
		if err := removeState(networkDir); err != nil {
			return Result{}, err
		}
	}
	if err := removeOwnership(ownership); err != nil {
		return Result{}, err
	}
	return Result{Message: "network stopped"}, nil
}

func destroyExactEnclave(ctx context.Context, client kurtosis.Client, enclave kurtosis.EnclaveRef) error {
	exists, err := client.EnclaveExists(ctx, enclave.UUID)
	if err != nil {
		return fmt.Errorf("inspect owned enclave existence by exact UUID: %w", err)
	}
	if !exists {
		return nil
	}
	if err := authenticateOwnedEnclave(ctx, client, enclave); err != nil {
		return err
	}
	destroyErr := client.DestroyEnclave(ctx, enclave)
	exists, inspectErr := client.EnclaveExists(ctx, enclave.UUID)
	if inspectErr != nil {
		return errors.Join(destroyErr, fmt.Errorf("confirm owned enclave destruction: %w", inspectErr))
	}
	if exists {
		return errors.Join(destroyErr, errors.New("owned enclave still exists after destruction"))
	}
	return nil
}

func cleanupCreatedEnclave(client kurtosis.Client, enclave kurtosis.EnclaveRef) error {
	if err := enclave.Validate(); err != nil || !enclave.Owned {
		return errors.New("cannot clean up an invalid or unowned returned enclave identity")
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return destroyExactEnclave(cleanupCtx, client, enclave)
}

func authenticateOwnedEnclave(ctx context.Context, client kurtosis.Client, enclave kurtosis.EnclaveRef) error {
	current, err := client.GetEnclave(ctx, enclave.UUID)
	if err != nil {
		return fmt.Errorf("inspect owned enclave by exact UUID: %w", err)
	}
	if current.Name != enclave.Name || current.UUID != enclave.UUID {
		return errors.New("refusing operation: owned enclave name/UUID changed")
	}
	return nil
}

func wrapCleanupError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("clean up returned enclave identity: %w", err)
}

func (manager *Manager) authenticateImages(ctx context.Context, expected []ImageIdentity) error {
	dockerBin := manager.Getenv("E2E_DOCKER_BIN")
	if dockerBin == "" {
		dockerBin = "docker"
	}
	for _, identity := range expected {
		actual, err := inspectImage(ctx, manager.Commands, dockerBin, identity.Role, identity.Ref)
		if err != nil {
			return err
		}
		if actual.ID != identity.ID {
			return fmt.Errorf("%s image ID changed", identity.Role)
		}
	}
	return nil
}

func pathExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func authenticateInvocation(ctx context.Context, client kurtosis.Client, enclave kurtosis.EnclaveRef, packageID, paramsDigest string) error {
	invocation, err := client.LastPackageInvocation(ctx, enclave)
	if err != nil {
		return fmt.Errorf("authenticate Kurtosis package invocation: %w", err)
	}
	if invocation.ID != packageID || digestCanonicalJSON(invocation.SerializedParams) != paramsDigest {
		return errors.New("Kurtosis package ID or canonical parameters changed")
	}
	return nil
}

func authenticateBinary(ctx context.Context, client kurtosis.Client, enclave kurtosis.EnclaveRef, serviceUUID, path, sourceCommit, expectedDigest string) (string, error) {
	exitCode, output, err := client.ExecCommand(ctx, enclave, serviceUUID, []string{"sha256sum", path})
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("hash execution binary in service %s: exit=%d err=%w", serviceUUID, exitCode, err)
	}
	fields := strings.Fields(output)
	if len(fields) < 2 || !digestPattern.MatchString(fields[0]) || fields[1] != path {
		return "", errors.New("execution binary hash output is malformed")
	}
	if expectedDigest != "" && fields[0] != expectedDigest {
		return "", errors.New("execution binary digest changed")
	}
	exitCode, output, err = client.ExecCommand(ctx, enclave, serviceUUID, []string{path, "version"})
	if err != nil || exitCode != 0 {
		return "", fmt.Errorf("read execution binary version: exit=%d err=%w", exitCode, err)
	}
	version := strings.TrimSpace(output)
	if reportedCommit(version) != sourceCommit {
		return "", fmt.Errorf("execution binary does not report full Git Commit %s", sourceCommit)
	}
	return fields[0], nil
}

func reportedCommit(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if value, ok := strings.CutPrefix(line, "Git Commit:"); ok {
			value = strings.TrimSpace(value)
			if commitPattern.MatchString(value) {
				return value
			}
		}
	}
	return ""
}

func verifySnapshotImages(snapshot topology.Snapshot, images []ImageIdentity) error {
	roles := map[string]string{snapshot.Execution.Role: snapshot.Execution.Image}
	for _, service := range snapshot.Required {
		roles[service.Role] = service.Image
	}
	for role, ref := range roles {
		if image := imageByRole(images, role); image.Ref != ref || image.ID == "" {
			return fmt.Errorf("%s service image %q differs from prepared image identity", role, ref)
		}
	}
	return nil
}

func defaultEnclaveName(canonicalNetworkDir, commit string) string {
	return "go-qrl-e2e-" + networkInstanceID(commit, canonicalNetworkDir)[:12]
}

var _ Authenticator = (*Manager)(nil)
var _ Controller = (*Manager)(nil)
