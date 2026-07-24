// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
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
	Now       func() time.Time
	Getenv    func(string) string
	Stdout    io.Writer
	Stderr    io.Writer
	Prepare   func(context.Context, commandRunner, StartRequest, SourceIdentity, WalletIdentity, io.Writer, io.Writer) (preparedNetwork, error)
	Probe     func(context.Context, probeRequest) (probeResult, error)
	Wallet    func(string) (WalletIdentity, error)
}

func NewManager() *Manager { return &Manager{} }

func (manager *Manager) normalize() {
	if manager.NewClient == nil {
		manager.NewClient = func() (kurtosis.Client, error) { return kurtosis.NewSDKClient() }
	}
	if manager.Commands == nil {
		manager.Commands = execRunner{}
	}
	if manager.Now == nil {
		manager.Now = time.Now
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

	if _, statErr := os.Lstat(statePath(networkDir)); statErr == nil {
		existing, err := loadState(networkDir)
		if err != nil {
			return Result{}, err
		}
		lifecycle, err := loadLifecycle(networkDir)
		if err != nil {
			return Result{}, err
		}
		if err := verifyLifecycleState(lifecycle, existing); err != nil {
			return Result{}, err
		}
		switch {
		case existing.Phase == PhaseRunning && lifecycle.Phase == LifecyclePackageAccepted:
			lifecycle.Phase, lifecycle.NetworkFingerprint = LifecycleReady, existing.Fingerprint
			if err := writeLifecycle(lifecycle); err != nil {
				return Result{}, fmt.Errorf("reconcile ready lifecycle: %w", err)
			}
		case existing.Phase == PhaseRunning && lifecycle.Phase == LifecycleStopped:
			when := *lifecycle.DestroyedAt
			existing.Phase, existing.StoppedAt = PhaseStopped, &when
			if err := writeState(existing); err != nil {
				return Result{}, fmt.Errorf("reconcile stopped public state: %w", err)
			}
		}
		if existing.Phase == PhaseStopped && lifecycle.Phase != LifecycleStopped {
			return Result{}, errors.New("stopped public state and private lifecycle disagree")
		}
		if existing.Phase == PhaseRunning {
			status, err := manager.Status(startCtx, networkDir)
			if err != nil {
				return Result{}, fmt.Errorf("authenticate existing network before reuse: %w", err)
			}
			if status.State.Phase == PhaseRunning {
				if !status.Ready {
					return Result{}, errors.New("existing network lifecycle is not reusable; inspect status or resume stop")
				}
				commit, err := source.Commit(startCtx, nil, repoRoot)
				if err != nil {
					return Result{}, err
				}
				if existing.Source.Commit != commit {
					return Result{}, errors.New("a different authenticated network already occupies the requested directory")
				}
				return status, nil
			}
			existing = status.State
		}
		if err := retireStoppedState(existing); err != nil {
			return Result{}, fmt.Errorf("retire stopped network state: %w", err)
		}
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return Result{}, statErr
	}

	commit, err := source.Commit(startCtx, nil, repoRoot)
	if err != nil {
		return Result{}, err
	}
	sourceIdentity := SourceIdentity{Commit: commit}
	wallet, err := manager.Wallet(privatePath(networkDir))
	if err != nil {
		return Result{}, fmt.Errorf("prepare private E2E wallet: %w", err)
	}
	client, err := manager.NewClient()
	if err != nil {
		return Result{}, fmt.Errorf("connect to Kurtosis engine: %w", err)
	}
	var prepared preparedNetwork
	var enclave kurtosis.EnclaveRef
	lifecycle, lifecycleErr := loadLifecycle(networkDir)
	if lifecycleErr == nil && lifecycle.Phase == LifecycleStopped {
		if err := retireLifecycle(lifecycle); err != nil {
			return Result{}, fmt.Errorf("retire stopped lifecycle: %w", err)
		}
		lifecycleErr = os.ErrNotExist
	}
	if lifecycleErr == nil {
		switch lifecycle.Phase {
		case LifecycleCreateIntent:
			return Result{}, errors.New("enclave creation outcome is ambiguous because no exact UUID was captured; refusing to replay create")
		case LifecyclePackageIntent, LifecyclePackageAccepted:
			if lifecycle.Source != sourceIdentity {
				return Result{}, errors.New("lifecycle cannot resume this source/network state")
			}
			prepared, err = resumePreparedNetwork(startCtx, manager.Commands, request, lifecycle)
			if err != nil {
				return Result{}, err
			}
			enclave, err = lifecycle.OwnedEnclave()
			if err != nil {
				return Result{}, err
			}
			current, err := client.GetEnclave(startCtx, enclave.UUID)
			if err != nil {
				return Result{}, fmt.Errorf("recover lifecycle enclave: %w", err)
			}
			if current.Name != enclave.Name || current.UUID != enclave.UUID {
				return Result{}, errors.New("lifecycle enclave name/UUID changed")
			}
		case LifecycleReady:
			return Result{}, errors.New("ready lifecycle has no public network state; refusing ambiguous reconstruction")
		case LifecycleDestroyIntent:
			return Result{}, errors.New("exact-UUID network destruction is pending; resume network stop")
		default:
			return Result{}, fmt.Errorf("unsupported lifecycle phase %q", lifecycle.Phase)
		}
	} else if !errors.Is(lifecycleErr, os.ErrNotExist) {
		return Result{}, lifecycleErr
	} else {
		prepared, err = manager.Prepare(startCtx, manager.Commands, request, sourceIdentity, wallet, manager.Stdout, manager.Stderr)
		if err != nil {
			return Result{}, err
		}
		name := request.EnclaveName
		if name == "" {
			name = defaultEnclaveName(networkDir, commit)
		}
		lifecycle = lifecycleFromPrepared(networkDir, name, sourceIdentity, prepared, manager.Now().UTC())
		if err := createLifecycle(lifecycle); err != nil {
			if errors.Is(err, os.ErrExist) {
				return Result{}, errors.New("private lifecycle already exists; refusing a concurrent or ambiguous create")
			}
			return Result{}, fmt.Errorf("publish pre-create lifecycle intent: %w", err)
		}
		enclave, err = client.CreateEnclave(startCtx, name)
		if err != nil {
			return Result{}, fmt.Errorf("create Kurtosis enclave %q returned an ambiguous result; exact UUID was not captured and creation must not be replayed: %w", name, err)
		}
		if enclave.Name != name || !enclave.Owned || enclave.Validate() != nil {
			return Result{}, errors.New("Kurtosis returned an unexpected or unowned enclave identity")
		}
		capturedAt := manager.Now().UTC()
		lifecycle.Phase, lifecycle.Enclave, lifecycle.EnclaveCapturedAt = LifecyclePackageIntent, &enclave, &capturedAt
		if err := writeLifecycle(lifecycle); err != nil {
			return Result{}, fmt.Errorf("capture exact enclave UUID and package intent: %w", err)
		}
	}

	if lifecycle.Phase == LifecyclePackageIntent {
		invocationAccepted := false
		invocation, invocationErr := client.LastPackageInvocation(startCtx, enclave)
		switch {
		case invocationErr == nil && invocation.ID == lifecycle.Package.ID && digestCanonicalJSON(invocation.SerializedParams) == prepared.ParamsDigest:
			invocationAccepted = true
		case errors.Is(invocationErr, kurtosis.ErrPackageInvocationNotFound):
			// The SDK proved the journaled call was not retained; replay is safe.
		case invocationErr != nil:
			return Result{}, fmt.Errorf("reconcile journaled package invocation: %w", invocationErr)
		default:
			return Result{}, errors.New("lifecycle package invocation differs from retained Kurtosis invocation")
		}
		if !invocationAccepted {
			runErr := client.RunRemotePackage(startCtx, enclave, kurtosis.PackageRun{Locator: lifecycle.Package.Locator, SerializedParams: prepared.Params})
			if authErr := authenticateInvocation(startCtx, client, enclave, lifecycle.Package.ID, prepared.ParamsDigest); authErr != nil {
				if runErr != nil {
					return Result{}, errors.Join(fmt.Errorf("run pinned qrl-package: %w", runErr), authErr)
				}
				return Result{}, authErr
			}
		}
		acceptedAt := manager.Now().UTC()
		lifecycle.Phase, lifecycle.PackageAcceptedAt = LifecyclePackageAccepted, &acceptedAt
		if err := writeLifecycle(lifecycle); err != nil {
			return Result{}, fmt.Errorf("record accepted package invocation: %w", err)
		}
	} else if err := authenticateInvocation(startCtx, client, enclave, lifecycle.Package.ID, prepared.ParamsDigest); err != nil {
		return Result{}, err
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
		return Result{}, fmt.Errorf("discover qrl-package topology: %w", err)
	}
	if err := verifySnapshotImages(snapshot, prepared.Images); err != nil {
		return Result{}, err
	}
	binaryDigest, err := authenticateBinary(startCtx, client, enclave, snapshot.Execution.UUID, executionBinaryPath, sourceIdentity.Commit, "")
	if err != nil {
		return Result{}, err
	}
	var readiness probeResult
	if err := poll.Until(startCtx, 2*time.Second, func(attempt context.Context) error {
		observed, err := manager.Probe(attempt, probeRequest{
			RPCURL: snapshot.RPC.URL, GraphQLURL: snapshot.GraphQL, WebSocketURL: snapshot.WebSocket.URL,
			Address: wallet.Address, ExpectedChainID: expectedChainID,
			Requirements: FullRequirements(),
		})
		if err == nil {
			readiness = observed
		}
		return err
	}); err != nil {
		return Result{}, fmt.Errorf("wait for network readiness: %w", err)
	}
	state := State{
		SchemaVersion: StateSchemaVersion, Backend: BackendKurtosis, Phase: PhaseRunning,
		NetworkDir: networkDir, Enclave: enclave, Package: lifecycle.Package,
		Source: sourceIdentity, Wallet: wallet, Topology: snapshot, Images: slices.Clone(prepared.Images),
		Execution: ExecutionIdentity{BinaryPath: executionBinaryPath, BinarySHA256: binaryDigest},
		Chain:     ChainIdentity{ChainID: readiness.ChainID, GenesisHash: readiness.GenesisHash}, CreatedAt: lifecycle.CreatedAt,
	}
	state.Fingerprint, err = state.IdentityFingerprint()
	if err != nil {
		return Result{}, err
	}
	if err := writeState(state); err != nil {
		return Result{}, err
	}
	lifecycle.Phase, lifecycle.NetworkFingerprint = LifecycleReady, state.Fingerprint
	if err := writeLifecycle(lifecycle); err != nil {
		return Result{}, fmt.Errorf("record ready lifecycle: %w", err)
	}
	return Result{State: state, Ready: true, Lifecycle: &lifecycle}, nil
}

func (manager *Manager) Status(ctx context.Context, requestedDir string) (Result, error) {
	return manager.status(ctx, requestedDir, FullRequirements())
}

func (manager *Manager) status(
	ctx context.Context,
	requestedDir string,
	requirements Requirements,
) (result Result, returnErr error) {
	manager.normalize()
	networkDir, err := canonicalExistingDirectory(requestedDir, "network directory")
	if err != nil {
		return Result{}, err
	}
	var authenticatedState *State
	defer func() {
		if returnErr == nil {
			return
		}
		result.Ready = false
		result.Message = "network status authentication failed; no raw backend response was persisted"
		if authenticatedState != nil {
			result.State = *authenticatedState
		}
	}()
	if _, statErr := os.Lstat(statePath(networkDir)); errors.Is(statErr, os.ErrNotExist) {
		lifecycle, err := loadLifecycle(networkDir)
		if err != nil {
			return Result{}, err
		}
		return Result{Message: "network lifecycle is " + lifecycle.Phase, Lifecycle: &lifecycle}, nil
	} else if statErr != nil {
		return Result{}, statErr
	}
	state, err := loadState(networkDir)
	if err != nil {
		return Result{}, err
	}
	authenticatedState = &state
	lifecycle, err := loadLifecycle(networkDir)
	if err != nil {
		return Result{}, err
	}
	if err := verifyLifecycleState(lifecycle, state); err != nil {
		return Result{}, err
	}
	if state.Phase == PhaseStopped {
		if lifecycle.Phase != LifecycleStopped {
			return Result{}, errors.New("stopped public state and private lifecycle disagree")
		}
		return Result{State: state, Message: "network is stopped", Lifecycle: &lifecycle}, nil
	}
	if lifecycle.Phase == LifecyclePackageAccepted {
		return Result{State: state, Message: "public network state is published but lifecycle readiness is pending; resume network start", Lifecycle: &lifecycle}, nil
	}
	if lifecycle.Phase == LifecycleStopped {
		return Result{State: state, Message: "enclave destruction completed but public stopped state is pending; resume network stop", Lifecycle: &lifecycle}, nil
	}
	if lifecycle.Phase == LifecycleDestroyIntent {
		return Result{State: state, Message: "network destruction is pending; run network stop", Lifecycle: &lifecycle}, nil
	}
	if lifecycle.Phase != LifecycleReady || lifecycle.NetworkFingerprint != state.Fingerprint {
		return Result{}, errors.New("running public state and private lifecycle disagree")
	}
	client, err := manager.NewClient()
	if err != nil {
		return Result{}, err
	}
	current, err := client.GetEnclave(ctx, state.Enclave.UUID)
	if err != nil {
		return Result{}, err
	}
	if current.Name != state.Enclave.Name || current.UUID != state.Enclave.UUID {
		return Result{}, errors.New("Kurtosis enclave name/UUID differs from persisted identity")
	}
	if err := authenticateInvocation(ctx, client, state.Enclave, state.Package.ID, state.Package.ParamsSHA256); err != nil {
		return Result{}, err
	}
	services, err := client.Services(ctx, state.Enclave)
	if err != nil {
		return Result{}, err
	}
	if err := topology.VerifySnapshot(state.Topology, services); err != nil {
		return Result{}, err
	}
	if err := manager.authenticateImages(ctx, state.Images); err != nil {
		return Result{}, err
	}
	if err := verifySnapshotImages(state.Topology, state.Images); err != nil {
		return Result{}, err
	}
	if requirements.Signer {
		wallet, err := validateWalletSeed(walletSeedPath(state.NetworkDir))
		if err != nil || wallet != state.Wallet {
			return Result{}, errors.New("private wallet no longer matches persisted address identity")
		}
	}
	_, err = authenticateBinary(ctx, client, state.Enclave, state.Topology.Execution.UUID, state.Execution.BinaryPath, state.Source.Commit, state.Execution.BinarySHA256)
	if err != nil {
		return Result{}, err
	}
	_, err = manager.Probe(ctx, probeRequest{
		RPCURL: state.Topology.RPC.URL, GraphQLURL: state.Topology.GraphQL, WebSocketURL: state.Topology.WebSocket.URL,
		Address: state.Wallet.Address, ExpectedChainID: state.Chain.ChainID, ExpectedGenesis: state.Chain.GenesisHash,
		Requirements: requirements,
	})
	if err != nil {
		return Result{}, err
	}
	return Result{State: state, Ready: true, Lifecycle: &lifecycle}, nil
}

func (manager *Manager) Authenticate(
	ctx context.Context,
	repoRoot,
	networkDir string,
	requirements Requirements,
) (Environment, error) {
	manager.normalize()
	root, err := canonicalExistingDirectory(repoRoot, "repository root")
	if err != nil {
		return Environment{}, err
	}
	result, err := manager.status(ctx, networkDir, requirements)
	if err != nil {
		return Environment{}, err
	}
	if result.State.Phase != PhaseRunning || !result.Ready {
		return Environment{}, errors.New("E2E network lifecycle is not ready")
	}
	commit, err := source.Commit(ctx, nil, root)
	if err != nil {
		return Environment{}, err
	}
	// Status above authenticates the immutable runtime image, binary, package,
	// topology, and original source identity. Same-commit test and E2E
	// changes may reuse that network, but runtime-affecting source drift must
	// rebuild it.
	if result.State.Source.Commit != commit {
		return Environment{}, errors.New("network source commit differs from the requested checkout")
	}
	if err := source.ValidateE2EOnlyTreeDrift(ctx, nil, root); err != nil {
		return Environment{}, fmt.Errorf("checkout contains runtime-affecting same-commit changes: %w", err)
	}
	state := result.State
	environment := Environment{
		NetworkDir: state.NetworkDir,
		RPCURL:     state.Topology.RPC.URL,
	}
	if requirements.Signer {
		environment.SeedFile = walletSeedPath(state.NetworkDir)
	}
	if requirements.GraphQL {
		environment.GraphQLURL = state.Topology.GraphQL
	}
	if requirements.WebSocket {
		environment.WebSocketURL = state.Topology.WebSocket.URL
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
	lifecycle, err := loadLifecycle(networkDir)
	if err != nil {
		return Result{}, err
	}
	if _, statErr := os.Lstat(statePath(networkDir)); errors.Is(statErr, os.ErrNotExist) {
		if lifecycle.Phase == LifecycleCreateIntent {
			return Result{}, errors.New("refusing name-only cleanup: lifecycle has no durably captured full enclave UUID")
		}
		if lifecycle.Phase != LifecycleStopped {
			client, err := manager.NewClient()
			if err != nil {
				return Result{}, err
			}
			lifecycle, err = manager.destroyLifecycle(ctx, client, lifecycle, lifecycle.NetworkFingerprint)
			if err != nil {
				return Result{}, err
			}
		}
		return Result{Message: "network lifecycle stopped before public readiness", Lifecycle: &lifecycle}, nil
	} else if statErr != nil {
		return Result{}, statErr
	}
	state, err := loadState(networkDir)
	if err != nil {
		return Result{}, err
	}
	if state.Phase == PhaseStopped {
		return manager.Status(ctx, networkDir)
	}
	if err := verifyLifecycleState(lifecycle, state); err != nil {
		return Result{}, err
	}
	if lifecycle.Phase != LifecycleReady && lifecycle.Phase != LifecycleDestroyIntent && lifecycle.Phase != LifecycleStopped {
		return Result{}, errors.New("refusing stop: lifecycle differs from running network identity")
	}
	client, err := manager.NewClient()
	if err != nil {
		return Result{}, err
	}
	lifecycle, err = manager.destroyLifecycle(ctx, client, lifecycle, state.Fingerprint)
	if err != nil {
		return Result{}, err
	}
	now := *lifecycle.DestroyedAt
	state.Phase, state.StoppedAt = PhaseStopped, &now
	if err := writeState(state); err != nil {
		return Result{}, err
	}
	return Result{State: state, Message: "network stopped", Lifecycle: &lifecycle}, nil
}

func (manager *Manager) destroyLifecycle(ctx context.Context, client kurtosis.Client, record LifecycleRecord, fingerprint string) (LifecycleRecord, error) {
	enclave, err := record.OwnedEnclave()
	if err != nil {
		return LifecycleRecord{}, err
	}
	if record.Phase != LifecycleDestroyIntent && record.Phase != LifecycleStopped {
		requestedAt := manager.Now().UTC()
		record.Phase, record.NetworkFingerprint, record.DestroyRequestedAt = LifecycleDestroyIntent, fingerprint, &requestedAt
		if err := writeLifecycle(record); err != nil {
			return LifecycleRecord{}, fmt.Errorf("journal enclave destruction before external mutation: %w", err)
		}
	}
	if record.NetworkFingerprint != fingerprint {
		return LifecycleRecord{}, errors.New("destroy lifecycle belongs to a different network fingerprint")
	}
	if record.Phase == LifecycleStopped {
		return record, nil
	}

	exists, err := client.EnclaveExists(ctx, enclave.UUID)
	if err != nil {
		return LifecycleRecord{}, fmt.Errorf("inspect lifecycle enclave existence by exact UUID: %w", err)
	}
	if exists {
		current, getErr := client.GetEnclave(ctx, enclave.UUID)
		if getErr != nil {
			existsAfterError, inspectErr := client.EnclaveExists(ctx, enclave.UUID)
			if inspectErr != nil || existsAfterError {
				return LifecycleRecord{}, errors.Join(
					fmt.Errorf("inspect lifecycle enclave by exact UUID: %w", getErr),
					wrapExistenceReconciliationError(inspectErr, existsAfterError),
				)
			}
		} else {
			if current.Name != enclave.Name || current.UUID != enclave.UUID {
				return LifecycleRecord{}, errors.New("refusing stop: lifecycle enclave name/UUID changed")
			}
			if destroyErr := client.DestroyEnclave(ctx, enclave); destroyErr != nil {
				existsAfterError, inspectErr := client.EnclaveExists(ctx, enclave.UUID)
				if inspectErr != nil || existsAfterError {
					return LifecycleRecord{}, errors.Join(
						fmt.Errorf("destroy lifecycle enclave by exact UUID: %w", destroyErr),
						wrapExistenceReconciliationError(inspectErr, existsAfterError),
					)
				}
			}
		}
	}
	destroyedAt := manager.Now().UTC()
	record.Phase, record.DestroyedAt = LifecycleStopped, &destroyedAt
	if err := writeLifecycle(record); err != nil {
		return LifecycleRecord{}, fmt.Errorf("record completed enclave destruction: %w", err)
	}
	return record, nil
}

func wrapExistenceReconciliationError(err error, exists bool) error {
	if err != nil {
		return fmt.Errorf("reconcile journaled enclave existence: %w", err)
	}
	if exists {
		return errors.New("journaled enclave still exists after failed external operation")
	}
	return nil
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
		if actual.ID != identity.ID || !maps.Equal(actual.Labels, identity.Labels) {
			return fmt.Errorf("%s image ID or labels changed", identity.Role)
		}
	}
	return nil
}

func lifecycleFromPrepared(networkDir, requestedName string, source SourceIdentity, prepared preparedNetwork, createdAt time.Time) LifecycleRecord {
	return LifecycleRecord{
		SchemaVersion: 1, Phase: LifecycleCreateIntent, NetworkDir: networkDir, RequestedName: requestedName,
		Package: PackageIdentity{Locator: packageLocator, ID: packageID, ParamsSHA256: prepared.ParamsDigest},
		Source:  source, Images: slices.Clone(prepared.Images), CreatedAt: createdAt.UTC(),
	}
}

func verifyLifecycleState(lifecycle LifecycleRecord, state State) error {
	enclave, err := lifecycle.OwnedEnclave()
	if err != nil {
		return fmt.Errorf("network state has no exact captured lifecycle ownership: %w", err)
	}
	if enclave.Name != state.Enclave.Name || enclave.UUID != state.Enclave.UUID ||
		lifecycle.NetworkDir != state.NetworkDir || lifecycle.Package != state.Package ||
		lifecycle.Source != state.Source || !imageIdentitiesEqual(lifecycle.Images, state.Images) {
		return errors.New("private lifecycle and public network state identify different networks")
	}
	if lifecycle.NetworkFingerprint != "" && lifecycle.NetworkFingerprint != state.Fingerprint {
		return errors.New("private lifecycle and public network fingerprints differ")
	}
	return nil
}

func imageIdentitiesEqual(left, right []ImageIdentity) bool {
	return slices.EqualFunc(left, right, func(a, b ImageIdentity) bool {
		return a.Role == b.Role && a.Ref == b.Ref && a.ID == b.ID && maps.Equal(a.Labels, b.Labels)
	})
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
