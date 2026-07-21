// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/ghodss/yaml"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/beacon"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/config"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/doctor"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/poll"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/provision"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/rpc"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
	consoleSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/console"
	depositSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/deposit"
	systemSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/system"
)

func ownedStages(runtime *Runtime) []lifecycle.Stage {
	return []lifecycle.Stage{
		stage("validate", 10*time.Minute, time.Minute, lifecycle.RetrySafe, false, runtime.validateStage, nil),
		stage("reserve", 5*time.Minute, 10*time.Second, lifecycle.InspectBeforeRetry, false, runtime.reserveStage, runtime.reserveReconcile),
		stage("fixture", 45*time.Minute, 5*time.Minute, lifecycle.RetrySafe, false, runtime.fixtureStage, nil),
		stage("host-preflight", 90*time.Minute, 15*time.Minute, lifecycle.RetrySafe, false, runtime.hostPreflightStage, nil),
		stage("network-package", 75*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.packageStage, runtime.packageReconcile),
		stage("topology", 10*time.Minute, time.Minute, lifecycle.RetrySafe, false, runtime.topologyStage, nil),
		stage("readiness", 45*time.Minute, 10*time.Minute, lifecycle.RetrySafe, false, runtime.readinessStage, nil),
		stage("el1", 60*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.elStage(0), runtime.transactionalStageReconcile("el1", "el1/")),
		stage("el2", 60*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.elStage(1), runtime.transactionalStageReconcile("el2", "el2/")),
		stage("deposit", 60*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.depositStage, runtime.transactionalStageReconcile("deposit", "deposit-")),
		stage("system-base", 60*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.systemStage("base"), runtime.systemMutationReconcile("system-base", systemSuite.PhaseBase)),
		stage("system-signer", 60*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.systemStage("signer-restart"), runtime.systemMutationReconcile("system-signer", systemSuite.PhaseSignerRestart)),
		stage("system-participant", 105*time.Minute, 30*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.systemStage("participant-restart"), runtime.systemMutationReconcile("system-participant", systemSuite.PhaseParticipantRestart)),
		stage("fresh-snap", 75*time.Minute, 20*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.freshSyncStage("snap"), runtime.freshSyncReconcile("snap")),
		stage("fresh-full", 75*time.Minute, 20*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.freshSyncStage("full"), runtime.freshSyncReconcile("full")),
	}
}

func borrowedStages(runtime *Runtime, allowDisruptive bool) []lifecycle.Stage {
	stages := []lifecycle.Stage{
		stage("validate", 10*time.Minute, time.Minute, lifecycle.RetrySafe, false, runtime.validateStage, nil),
		stage("host-preflight", 30*time.Minute, 5*time.Minute, lifecycle.RetrySafe, false, runtime.borrowedHostPreflightStage, nil),
		stage("topology", 10*time.Minute, time.Minute, lifecycle.RetrySafe, false, runtime.topologyStage, nil),
		stage("readiness", 45*time.Minute, 10*time.Minute, lifecycle.RetrySafe, false, runtime.readinessStage, nil),
		stage("readonly-el1", 30*time.Minute, 5*time.Minute, lifecycle.RetrySafe, false, runtime.readOnlyELStage(0), nil),
		stage("readonly-el2", 30*time.Minute, 5*time.Minute, lifecycle.RetrySafe, false, runtime.readOnlyELStage(1), nil),
	}
	if !allowDisruptive {
		return stages
	}
	return append(stages,
		stage("el1", 60*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.elStage(0), runtime.transactionalStageReconcile("el1", "el1/")),
		stage("el2", 60*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.elStage(1), runtime.transactionalStageReconcile("el2", "el2/")),
		stage("deposit", 60*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.depositStage, runtime.transactionalStageReconcile("deposit", "deposit-")),
		stage("system-base", 60*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.systemStage("base"), runtime.systemMutationReconcile("system-base", systemSuite.PhaseBase)),
		stage("system-signer", 60*time.Minute, 15*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.systemStage("signer-restart"), runtime.systemMutationReconcile("system-signer", systemSuite.PhaseSignerRestart)),
		stage("system-participant", 105*time.Minute, 30*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.systemStage("participant-restart"), runtime.systemMutationReconcile("system-participant", systemSuite.PhaseParticipantRestart)),
		stage("fresh-snap", 75*time.Minute, 20*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.freshSyncStage("snap"), runtime.freshSyncReconcile("snap")),
		stage("fresh-full", 75*time.Minute, 20*time.Minute, lifecycle.InspectBeforeRetry, true, runtime.freshSyncStage("full"), runtime.freshSyncReconcile("full")),
	)
}

func stage(name string, timeout, minimum time.Duration, policy lifecycle.ResumePolicy, disruptive bool, run func(context.Context, *lifecycle.RunEnvironment) error, reconcile func(context.Context, *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error)) lifecycle.Stage {
	return lifecycle.Stage{Name: name, Timeout: timeout, MinimumRuntime: minimum, ResumePolicy: policy, Disruptive: disruptive, Run: run, Reconcile: reconcile}
}

func (runtime *Runtime) validateStage(ctx context.Context, _ *lifecycle.RunEnvironment) error {
	report := doctor.Run(ctx, nil, doctor.Options{
		RepoRoot: runtime.Config.RepoRoot, SourceSHA: runtime.Config.SourceSHA,
		NetworkParams: runtime.Config.NetworkParams, RequireEngine: true,
	})
	payload, _ := json.MarshalIndent(report, "", "  ")
	if _, err := runtime.Writer.WriteSuiteLog("doctor", append(payload, '\n')); err != nil {
		return err
	}
	return report.Validate()
}

func (runtime *Runtime) reserveStage(ctx context.Context, _ *lifecycle.RunEnvironment) error {
	_, err := runtime.verifyOwnedEnclave(ctx)
	return err
}

func (runtime *Runtime) reserveReconcile(ctx context.Context, _ *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error) {
	if _, err := runtime.verifyOwnedEnclave(ctx); err != nil {
		return "", err
	}
	return lifecycle.ReconcileComplete, nil
}

func (runtime *Runtime) verifyOwnedEnclave(ctx context.Context) (lifecycle.OwnershipRecord, error) {
	record, err := lifecycle.LoadOwnership(runtime.Writer.Layout().Ownership)
	if err != nil {
		return lifecycle.OwnershipRecord{}, err
	}
	if record.UUID == nil || *record.UUID != runtime.Enclave.UUID || record.RequestedName != runtime.Enclave.Name {
		return lifecycle.OwnershipRecord{}, errors.New("ownership record does not match the full enclave identity")
	}
	current, err := runtime.Dependencies.Client.GetEnclave(ctx, runtime.Enclave.UUID)
	if err != nil {
		return lifecycle.OwnershipRecord{}, err
	}
	if current.UUID != runtime.Enclave.UUID || current.Name != runtime.Enclave.Name {
		return lifecycle.OwnershipRecord{}, errors.New("Kurtosis enclave identity changed")
	}
	return record, nil
}

func (runtime *Runtime) fixtureStage(ctx context.Context, _ *lifecycle.RunEnvironment) error {
	return runtime.runCommand(ctx, "fixture", filepath.Join(runtime.Config.RepoRoot, "scripts", "testing", "e2e", "testdata", "contracts", "verify_hyperion_fixture.sh"), nil, nil)
}

func (runtime *Runtime) hostPreflightStage(ctx context.Context, _ *lifecycle.RunEnvironment) error {
	if err := runtime.runCommand(ctx, "host-preflight", "make", []string{"local-testnet-host-preflight"}, nil); err != nil {
		return err
	}
	preparationOutput := filepath.Join(runtime.Writer.Layout().Kurtosis, "preparation.json")
	effectiveOutput := filepath.Join(runtime.Writer.Layout().Kurtosis, "network-params.effective.yaml")
	adapter := provisionProcessRunner{runtime: runtime, stage: "preparation"}
	preparation, err := provision.Prepare(ctx, adapter, provision.Options{
		RepoRoot: runtime.Config.RepoRoot, NetworkParams: runtime.Config.NetworkParams,
		EffectiveOutput: effectiveOutput, PreparationOutput: preparationOutput,
		SourceSHA: runtime.Config.SourceSHA, CI: runtime.Config.CI,
		ExtraEnvironment: packagePreparationEnvironment(runtime.Config),
	}, runtime.Dependencies.Output)
	if err != nil {
		return err
	}
	runtime.Preparation = &preparation
	runtime.Config.PackageLocator = preparation.QRLPackage.Repository
	runtime.Config.PackageRevision = preparation.QRLPackage.Revision
	return runtime.writeEffectiveConfiguration()
}

func packagePreparationEnvironment(runConfig config.RunConfig) map[string]string {
	return map[string]string{
		"QRL_PACKAGE_REPO": runConfig.PackageLocator,
		"QRL_PKG_VERSION":  runConfig.PackageRevision,
	}
}

func (runtime *Runtime) borrowedHostPreflightStage(ctx context.Context, _ *lifecycle.RunEnvironment) error {
	return runtime.runCommand(ctx, "host-preflight", "go", []string{"run", "build/ci.go", "install", "./cmd/gqrl"}, nil)
}

func (runtime *Runtime) packageStage(ctx context.Context, _ *lifecycle.RunEnvironment) error {
	if runtime.Preparation == nil {
		return errors.New("network package cannot run without preparation metadata")
	}
	params, err := runtime.Preparation.SerializedParams()
	if err != nil {
		return err
	}
	run := kurtosis.PackageRun{Locator: runtime.Preparation.PackageLocator(), SerializedParams: params}
	if _, err := runtime.ensurePackageInvocationIntent(run); err != nil {
		return err
	}
	result, err := runtime.Dependencies.Client.RunRemotePackage(ctx, runtime.Enclave, run)
	if err != nil {
		return err
	}
	runtime.PackageResult = &result
	if _, err := runtime.Writer.WriteKurtosisArtifact("package-output.json", []byte(result.SerializedOutput)); err != nil {
		return err
	}
	return nil
}

func (runtime *Runtime) packageReconcile(ctx context.Context, _ *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error) {
	services, err := runtime.Dependencies.Client.Services(ctx, runtime.Enclave)
	if err != nil {
		return "", err
	}
	if len(services) == 0 {
		if runtime.Preparation == nil {
			return "", errors.New("network package reconciliation requires preparation metadata")
		}
		params, err := runtime.Preparation.SerializedParams()
		if err != nil {
			return "", err
		}
		run := kurtosis.PackageRun{Locator: runtime.Preparation.PackageLocator(), SerializedParams: params}
		if _, err := runtime.loadPackageInvocationIntent(run); err != nil {
			return "", err
		}
		invocation, err := runtime.Dependencies.Client.LastPackageInvocation(ctx, runtime.Enclave)
		if errors.Is(err, kurtosis.ErrPackageInvocationNotFound) {
			return lifecycle.ReconcileRetry, nil
		}
		if err != nil {
			return "", fmt.Errorf("inspect retained package invocation before replay: %w", err)
		}
		if err := verifyPackageInvocation(run, invocation); err != nil {
			return "", fmt.Errorf("zero-service enclave retains a different or corrupt package invocation: %w", err)
		}
		return "", errors.New("the exact package invocation was accepted but has not produced any services; refusing a duplicate package run, preserve the enclave and resume this checkpoint after the retained invocation progresses")
	}
	if runtime.PackageResult == nil {
		if err := runtime.recoverPackageResult(ctx); err != nil {
			return "", err
		}
		return lifecycle.ReconcileComplete, nil
	}
	if _, err := topology.DiscoverSerialized(runtime.TopologySpec, runtime.PackageResult.SerializedOutput, services); err != nil {
		return "", fmt.Errorf("package created partial or inconsistent external state: %w", err)
	}
	return lifecycle.ReconcileComplete, nil
}

const packageInvocationSchema = 1

type packageInvocationIntent struct {
	Schema       int                  `json:"schema"`
	Enclave      lifecycle.EnclaveRef `json:"enclave"`
	Locator      string               `json:"locator"`
	ParamsSHA256 string               `json:"params_sha256"`
	StartedAt    time.Time            `json:"started_at"`
}

type packageRecoveryEvidence struct {
	Schema      int                        `json:"schema"`
	RecoveredAt time.Time                  `json:"recovered_at"`
	Enclave     lifecycle.EnclaveRef       `json:"enclave"`
	Intent      packageInvocationIntent    `json:"intent"`
	Invocation  kurtosis.PackageInvocation `json:"kurtosis_invocation"`
	Metadata    PackageNetworkMetadata     `json:"metadata"`
	Topology    topology.Topology          `json:"topology"`
	Output      topology.PackageOutput     `json:"recovered_output"`
}

func packageParamsDigest(params string) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(params)))
}

func (runtime *Runtime) expectedPackageInvocationIntent(run kurtosis.PackageRun) (packageInvocationIntent, error) {
	if run.Locator == "" || run.SerializedParams == "" {
		return packageInvocationIntent{}, errors.New("package invocation is incomplete")
	}
	return packageInvocationIntent{
		Schema: packageInvocationSchema, Enclave: runtime.Enclave,
		Locator: run.Locator, ParamsSHA256: packageParamsDigest(run.SerializedParams),
	}, nil
}

func (runtime *Runtime) loadPackageInvocationIntent(run kurtosis.PackageRun) (packageInvocationIntent, error) {
	expected, err := runtime.expectedPackageInvocationIntent(run)
	if err != nil {
		return packageInvocationIntent{}, err
	}
	path := filepath.Join(runtime.Writer.Layout().Kurtosis, "package-invocation.json")
	payload, err := os.ReadFile(path)
	if err != nil {
		return packageInvocationIntent{}, fmt.Errorf("read durable package invocation: %w", err)
	}
	var existing packageInvocationIntent
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&existing); err != nil {
		return packageInvocationIntent{}, fmt.Errorf("decode durable package invocation: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return packageInvocationIntent{}, errors.New("decode durable package invocation: trailing data")
	}
	if existing.Schema != expected.Schema || existing.Enclave.Name != expected.Enclave.Name || existing.Enclave.UUID != expected.Enclave.UUID || existing.Locator != expected.Locator || existing.ParamsSHA256 != expected.ParamsSHA256 || existing.StartedAt.IsZero() {
		return packageInvocationIntent{}, errors.New("durable package invocation differs from the requested enclave, package, or parameters")
	}
	return existing, nil
}

func (runtime *Runtime) ensurePackageInvocationIntent(run kurtosis.PackageRun) (packageInvocationIntent, error) {
	expected, err := runtime.expectedPackageInvocationIntent(run)
	if err != nil {
		return packageInvocationIntent{}, err
	}
	if existing, err := runtime.loadPackageInvocationIntent(run); err == nil {
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return packageInvocationIntent{}, err
	}
	expected.StartedAt = runtime.Dependencies.Now().UTC()
	payload, err := json.MarshalIndent(expected, "", "  ")
	if err != nil {
		return packageInvocationIntent{}, err
	}
	payload = append(payload, '\n')
	if _, err := runtime.Writer.WriteKurtosisArtifact("package-invocation.json", payload); err != nil {
		return packageInvocationIntent{}, err
	}
	return expected, nil
}

func normalizePackageParams(params string) ([]byte, error) {
	converted := []byte(params)
	if !json.Valid(converted) {
		var err error
		converted, err = yaml.YAMLToJSON(converted)
		if err != nil {
			return nil, err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(converted))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, errors.New("package parameters contain trailing data")
	}
	return json.Marshal(value)
}

func verifyPackageInvocation(expected kurtosis.PackageRun, actual kurtosis.PackageInvocation) error {
	if actual.Locator != expected.Locator {
		return fmt.Errorf("Kurtosis last package locator is %q, want %q", actual.Locator, expected.Locator)
	}
	want, err := normalizePackageParams(expected.SerializedParams)
	if err != nil {
		return fmt.Errorf("normalize expected package parameters: %w", err)
	}
	got, err := normalizePackageParams(actual.SerializedParams)
	if err != nil {
		return fmt.Errorf("normalize retained package parameters: %w", err)
	}
	if !bytes.Equal(got, want) {
		return errors.New("Kurtosis last package parameters differ from the durable invocation")
	}
	return nil
}

func (runtime *Runtime) recoverPackageResult(ctx context.Context) error {
	if runtime.Preparation == nil {
		return errors.New("cannot recover package output without preparation metadata")
	}
	params, err := runtime.Preparation.SerializedParams()
	if err != nil {
		return err
	}
	run := kurtosis.PackageRun{Locator: runtime.Preparation.PackageLocator(), SerializedParams: params}
	intent, err := runtime.loadPackageInvocationIntent(run)
	if err != nil {
		return err
	}
	invocation, err := runtime.Dependencies.Client.LastPackageInvocation(ctx, runtime.Enclave)
	if err != nil {
		return fmt.Errorf("read Kurtosis package invocation: %w", err)
	}
	if err := verifyPackageInvocation(run, invocation); err != nil {
		return err
	}

	var services []kurtosis.Service
	var discovered topology.Topology
	var metadata PackageNetworkMetadata
	if err := poll.Do(ctx, poll.Options{Interval: 5 * time.Second, Timeout: 10 * time.Minute, RetryErrors: true}, func(observation context.Context) (bool, error) {
		current, err := runtime.Dependencies.Client.Services(observation, runtime.Enclave)
		if err != nil {
			return false, err
		}
		candidate, err := topology.Discover(runtime.TopologySpec, nil, current)
		if err != nil {
			return false, err
		}
		observed, err := runtime.Dependencies.PackageMetadata(observation, candidate)
		if err != nil {
			return false, err
		}
		services, discovered, metadata = current, candidate, observed
		return true, nil
	}); err != nil {
		return fmt.Errorf("recover accepted package from live services: %w", err)
	}
	recovered, err := topology.RecoverPackageOutput(runtime.TopologySpec, services, metadata.NetworkID, metadata.FinalGenesisTimestamp)
	if err != nil {
		return err
	}
	serialized, err := json.Marshal(recovered)
	if err != nil {
		return err
	}
	evidence := packageRecoveryEvidence{
		Schema: packageInvocationSchema, RecoveredAt: runtime.Dependencies.Now().UTC(), Enclave: runtime.Enclave,
		Intent: intent, Invocation: invocation, Metadata: metadata, Topology: discovered, Output: recovered,
	}
	evidencePayload, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil {
		return err
	}
	if _, err := runtime.Writer.WriteKurtosisArtifact("package-recovery.json", append(evidencePayload, '\n')); err != nil {
		return err
	}
	if _, err := runtime.Writer.WriteKurtosisArtifact("package-output.json", serialized); err != nil {
		return err
	}
	runtime.PackageResult = &kurtosis.PackageResult{SerializedOutput: string(serialized)}
	return nil
}

func readPackageNetworkMetadata(ctx context.Context, discovered topology.Topology) (PackageNetworkMetadata, error) {
	var metadata PackageNetworkMetadata
	for i, node := range discovered.Execution {
		client, err := rpc.New(node.RPC.PublicURL, rpc.HTTPOptions{})
		if err != nil {
			return PackageNetworkMetadata{}, err
		}
		var networkID string
		if err := client.Call(ctx, "net_version", []any{}, &networkID); err != nil {
			return PackageNetworkMetadata{}, fmt.Errorf("execution[%d] network ID: %w", i, err)
		}
		if value, err := strconv.ParseUint(networkID, 10, 64); err != nil || value == 0 {
			return PackageNetworkMetadata{}, fmt.Errorf("execution[%d] returned invalid network ID %q", i, networkID)
		}
		if metadata.NetworkID != "" && metadata.NetworkID != networkID {
			return PackageNetworkMetadata{}, fmt.Errorf("execution network IDs differ: %s != %s", metadata.NetworkID, networkID)
		}
		metadata.NetworkID = networkID
	}
	for i, node := range discovered.Consensus {
		client, err := beacon.New(node.HTTP.PublicURL, beacon.Options{})
		if err != nil {
			return PackageNetworkMetadata{}, err
		}
		genesis, err := client.Genesis(ctx)
		if err != nil {
			return PackageNetworkMetadata{}, fmt.Errorf("consensus[%d] genesis: %w", i, err)
		}
		if genesis.GenesisTime == 0 || genesis.GenesisValidatorsRoot == "" || genesis.GenesisForkVersion == "" {
			return PackageNetworkMetadata{}, fmt.Errorf("consensus[%d] returned incomplete genesis metadata", i)
		}
		timestamp := strconv.FormatUint(genesis.GenesisTime, 10)
		if metadata.FinalGenesisTimestamp != "" && (metadata.FinalGenesisTimestamp != timestamp || metadata.GenesisValidatorsRoot != genesis.GenesisValidatorsRoot || metadata.GenesisForkVersion != genesis.GenesisForkVersion) {
			return PackageNetworkMetadata{}, errors.New("consensus nodes returned different genesis metadata")
		}
		metadata.FinalGenesisTimestamp = timestamp
		metadata.GenesisValidatorsRoot = genesis.GenesisValidatorsRoot
		metadata.GenesisForkVersion = genesis.GenesisForkVersion
	}
	if metadata.NetworkID == "" || metadata.FinalGenesisTimestamp == "" {
		return PackageNetworkMetadata{}, errors.New("recovered package metadata is incomplete")
	}
	return metadata, nil
}

func (runtime *Runtime) topologyStage(ctx context.Context, _ *lifecycle.RunEnvironment) error {
	services, err := runtime.Dependencies.Client.Services(ctx, runtime.Enclave)
	if err != nil {
		return err
	}
	var discovered topology.Topology
	if runtime.PackageResult != nil {
		discovered, err = topology.DiscoverSerialized(runtime.TopologySpec, runtime.PackageResult.SerializedOutput, services)
	} else {
		discovered, err = topology.Discover(runtime.TopologySpec, nil, services)
	}
	if err != nil {
		return err
	}
	if err := runtime.Writer.WriteTopology(discovered); err != nil {
		return err
	}
	runtime.Topology = &discovered
	return nil
}

func (runtime *Runtime) readinessStage(ctx context.Context, _ *lifecycle.RunEnvironment) error {
	if runtime.Topology == nil {
		return errors.New("network readiness requires discovered topology")
	}
	for _, node := range runtime.Topology.Execution {
		client, err := rpc.New(node.RPC.PublicURL, rpc.HTTPOptions{})
		if err != nil {
			return err
		}
		if err := poll.Retry(ctx, 5*time.Second, func(observation context.Context) (bool, error) {
			var block string
			if err := client.Call(observation, "qrl_blockNumber", []any{}, &block); err != nil {
				return false, err
			}
			value, err := strconv.ParseUint(strings.TrimPrefix(block, "0x"), 16, 64)
			return value > 0, err
		}); err != nil {
			return fmt.Errorf("execution service %s readiness: %w", node.Service.Name, err)
		}
		var version string
		if err := client.Call(ctx, "web3_clientVersion", []any{}, &version); err != nil {
			return err
		}
		if runtime.Config.SourceSHA != "" && !strings.Contains(version, runtime.Config.SourceSHA[:8]) {
			return fmt.Errorf("execution service %s reports %q without source %s", node.Service.Name, version, runtime.Config.SourceSHA[:8])
		}
		graphQL, err := rpc.NewGraphQL(strings.TrimRight(node.RPC.PublicURL, "/")+"/graphql", rpc.HTTPOptions{})
		if err != nil {
			return err
		}
		var graph struct {
			ChainID string `json:"chainID"`
		}
		if err := graphQL.Query(ctx, "{chainID}", nil, &graph); err != nil || graph.ChainID == "" {
			return fmt.Errorf("execution service %s GraphQL readiness: %w", node.Service.Name, errors.Join(err, emptyValueError(graph.ChainID)))
		}
		webSocket, err := rpc.NewWebSocket(node.WS.PublicURL, rpc.WebSocketOptions{})
		if err != nil {
			return err
		}
		var wsVersion string
		if err := webSocket.Call(ctx, "web3_clientVersion", []any{}, &wsVersion); err != nil || wsVersion == "" {
			return fmt.Errorf("execution service %s WebSocket readiness: %w", node.Service.Name, errors.Join(err, emptyValueError(wsVersion)))
		}
	}
	for _, node := range runtime.Topology.Consensus {
		client, err := beacon.New(node.HTTP.PublicURL, beacon.Options{})
		if err != nil {
			return err
		}
		if err := poll.Retry(ctx, 5*time.Second, func(observation context.Context) (bool, error) {
			status, err := client.Syncing(observation)
			return err == nil && status.HeadSlot > 0 && !status.ELOffline, err
		}); err != nil {
			return fmt.Errorf("consensus service %s readiness: %w", node.Service.Name, err)
		}
	}
	return nil
}

func emptyValueError(value string) error {
	if value == "" {
		return errors.New("response value is empty")
	}
	return nil
}

func (runtime *Runtime) readOnlyELStage(index int) func(context.Context, *lifecycle.RunEnvironment) error {
	return func(ctx context.Context, _ *lifecycle.RunEnvironment) error {
		if runtime.Topology == nil || index >= len(runtime.Topology.Execution) {
			return errors.New("read-only console stage requires execution topology")
		}
		node := runtime.Topology.Execution[index]
		stageName := fmt.Sprintf("readonly-el%d", index+1)
		results, err := consoleSuite.Run(ctx, consoleSuite.Config{
			GQRLPath: filepath.Join(runtime.Config.RepoRoot, "build", "bin", "gqrl"),
			JSPath:   filepath.Join(runtime.Config.RepoRoot, "scripts", "testing", "e2e", "testdata"),
			RPCURL:   node.RPC.PublicURL, Suites: consoleSuite.ReadOnlyDefinitions(),
			OutputDir: filepath.Join(runtime.Writer.Layout().Logs, fmt.Sprintf("%s-attempt-%d", stageName, runtime.currentAttempt(stageName))),
		})
		for _, suite := range results {
			status := reportStatus(suite.Status == "passed")
			runtime.recordSuiteResult(suiteReport(node.Service.Name+"-"+suite.Name, stageName, status, suite.StartedAt, suite.FinishedAt, suite.OutputTruncated))
		}
		return err
	}
}

func (runtime *Runtime) depositStage(ctx context.Context, environment *lifecycle.RunEnvironment) (stageErr error) {
	now := runtime.Dependencies.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	defer func() {
		finished := now()
		runtime.recordSuite("deposit", "deposit", started, finished, stageErr)
		stageErr = errors.Join(stageErr, runtime.writeStageSuiteMarker("deposit", stageErr))
	}()
	if runtime.Preparation == nil {
		return errors.New("deposit stage requires preparation metadata")
	}
	if runtime.Topology == nil {
		return errors.New("deposit stage requires topology")
	}
	args := []string{
		"-enclave", runtime.Enclave.UUID,
		"-generator-image", runtime.Preparation.Images["genesis"].Name,
		"-rpc1", runtime.Topology.Execution[0].RPC.PublicURL,
		"-rpc2", runtime.Topology.Execution[1].RPC.PublicURL,
		"-cl1", runtime.Topology.Consensus[0].HTTP.PublicURL,
		"-cl2", runtime.Topology.Consensus[1].HTTP.PublicURL,
	}
	configuration, err := depositSuite.ParseConfig(args)
	if err != nil {
		return err
	}
	recorder := depositSuite.TransactionRecorderFunc(func(label, hash string) error {
		return environment.State.RecordTransaction(runtime.Store, label, hash, runtime.Dependencies.Now())
	})
	preparedRecorder := depositSuite.PreparedTransactionRecorderFunc(func(label, hash, raw string) error {
		return environment.State.RecordPreparedTransaction(runtime.Store, label, hash, raw, runtime.Dependencies.Now())
	})
	return depositSuite.Run(ctx, configuration, depositSuite.Options{
		TransactionRecorder:         recorder,
		PreparedTransactionRecorder: preparedRecorder,
		RecordedTransactions:        stageTransactions(environment.State.Transactions, "", "deposit-"),
		PreparedTransactions:        depositPreparedTransactions(environment.State.PreparedTransactions),
	})
}

func depositPreparedTransactions(transactions map[string]lifecycle.PreparedTransaction) map[string]depositSuite.PreparedTransaction {
	result := make(map[string]depositSuite.PreparedTransaction)
	for label, transaction := range transactions {
		if !strings.HasPrefix(label, "deposit-") {
			continue
		}
		result[label] = depositSuite.PreparedTransaction{Hash: transaction.Hash, Raw: transaction.Raw}
	}
	return result
}

func (runtime *Runtime) systemStage(phase string) func(context.Context, *lifecycle.RunEnvironment) error {
	return func(ctx context.Context, environment *lifecycle.RunEnvironment) error {
		configuration, name, err := runtime.systemConfiguration(phase)
		if err != nil {
			return err
		}
		if environment == nil || environment.State == nil {
			return errors.New("system suite requires lifecycle checkpoint state")
		}
		if runtime.Writer == nil {
			return errors.New("system suite requires an artifact writer")
		}
		configuration.RequireZeroDutyHistory = runtime.currentAttempt(name) == 1
		runner := runtime.Dependencies.System
		if runner == nil {
			runner = systemSuite.Run
		}
		now := runtime.Dependencies.Now
		if now == nil {
			now = time.Now
		}
		started := now().UTC()
		recorder := systemEvidenceRecorder{runtime: runtime, environment: environment, phase: configuration.Phase, stageName: name}
		restartHistory, historyErr := systemRestartHistory(environment.State.ServiceTransitions, configuration.Phase, *runtime.Topology)
		if historyErr != nil {
			return historyErr
		}
		err = runner(ctx, configuration, systemSuite.Options{
			Controller:                        systemServiceController{runtime: runtime},
			Evidence:                          recorder,
			TransactionRecorder:               recorder,
			ManagedJournal:                    recorder,
			ObservationRecorder:               recorder,
			RecordedTransactions:              stageTransactions(environment.State.Transactions, name+"/", string(configuration.Phase)+"/"),
			RestartHistory:                    restartHistory,
			ManagedTransactionIntents:         systemManagedTransactionIntents(environment.State.ManagedTransactionIntents, name),
			ManagedTransactionInitialAttempts: systemManagedTransactionTimes(environment.State.ManagedTransactionInitialAttempts, name),
			ManagedTransactionResubmits:       systemManagedTransactionTimes(environment.State.ManagedTransactionResubmits, name),
			ServiceUUIDs:                      systemServiceUUIDs(*runtime.Topology),
			RecordedObservations:              systemObservations(environment.State.SystemObservations, name),
		})
		finished := now().UTC()
		status := reportStatus(err == nil)
		runtime.recordSuiteResult(suiteReport(name, name, status, started, finished, false))
		message := fmt.Sprintf("SUITE %s: PASSED\n", name)
		if err != nil {
			message = fmt.Sprintf("SUITE %s: FAILED -- %v\n", name, err)
		}
		attempt := runtime.currentAttempt(name)
		_, writeErr := runtime.Writer.WriteSuiteLog(fmt.Sprintf("%s-attempt-%d", name, attempt), []byte(message))
		return errors.Join(err, writeErr)
	}
}

func (runtime *Runtime) freshSyncStage(mode string) func(context.Context, *lifecycle.RunEnvironment) error {
	return func(ctx context.Context, environment *lifecycle.RunEnvironment) error {
		return runtime.runFreshSyncSuite(ctx, environment, mode)
	}
}

func (runtime *Runtime) loggedMutationReconcile(stageName string) func(context.Context, *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error) {
	return func(_ context.Context, _ *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error) {
		if runtime.stageLogPassed(stageName) {
			return lifecycle.ReconcileComplete, nil
		}
		return "", fmt.Errorf("stage %s changed external state but has no durable success evidence; refusing blind replay", stageName)
	}
}

func (runtime *Runtime) systemMutationReconcile(stageName string, phase systemSuite.Phase) func(context.Context, *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error) {
	return func(_ context.Context, environment *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error) {
		if runtime.stageLogPassed(stageName) {
			return lifecycle.ReconcileComplete, nil
		}
		if environment == nil || environment.State == nil {
			return "", errors.New("system reconciliation requires lifecycle checkpoint state")
		}
		for _, attempt := range environment.State.Attempts {
			if attempt.Stage == stageName && strings.Contains(attempt.FailureMessage, "was submitted but could not be recorded") {
				if _, err := runtime.recoverSubmittedTransaction(environment.State, attempt.FailureMessage, func(label string) string {
					return stageName + "/" + label
				}); err != nil {
					return "", fmt.Errorf("stage %s recover submitted transaction evidence: %w", stageName, err)
				}
			}
		}
		_ = phase
		// Durable transaction hashes, completed service transitions, and endpoint
		// refreshes are consumed by the resume-aware system suite. Re-running the
		// stage continues observations or the next unrecorded mutation.
		return lifecycle.ReconcileRetry, nil
	}
}

func (runtime *Runtime) transactionalStageReconcile(stageName string, transactionPrefixes ...string) func(context.Context, *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error) {
	return func(_ context.Context, environment *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error) {
		if runtime.stageLogPassed(stageName) {
			return lifecycle.ReconcileComplete, nil
		}
		if environment == nil || environment.State == nil {
			return "", fmt.Errorf("stage %s reconciliation requires checkpoint state", stageName)
		}
		for _, attempt := range environment.State.Attempts {
			if attempt.Stage == stageName && strings.Contains(attempt.FailureMessage, "was submitted but could not be recorded") {
				if _, err := runtime.recoverSubmittedTransaction(environment.State, attempt.FailureMessage, func(label string) string {
					if strings.HasPrefix(label, "deposit-") {
						return label
					}
					return stageName + "/" + label
				}); err != nil {
					return "", fmt.Errorf("stage %s recover submitted transaction evidence: %w", stageName, err)
				}
			}
		}
		// The suite validates transaction labels/hashes and re-observes each
		// durable mutation. Prefixes remain part of this constructor's contract
		// for call-site readability and future stage-specific validation.
		_ = transactionPrefixes
		return lifecycle.ReconcileRetry, nil
	}
}

var submittedTransactionFailurePattern = regexp.MustCompile(`transaction (0x[0-9a-f]{64}) as ([a-z0-9][a-z0-9/_-]*) was submitted but could not be recorded`)

func (runtime *Runtime) recoverSubmittedTransaction(state *lifecycle.Checkpoint, failure string, checkpointLabel func(string) string) (bool, error) {
	match := submittedTransactionFailurePattern.FindStringSubmatch(failure)
	if len(match) != 3 {
		return false, errors.New("failure did not retain a canonical transaction hash and label")
	}
	if state == nil || checkpointLabel == nil {
		return false, errors.New("transaction recovery is not initialized")
	}
	label := checkpointLabel(match[2])
	now := time.Now
	if runtime.Dependencies.Now != nil {
		now = runtime.Dependencies.Now
	}
	if err := state.RecordTransaction(runtime.Store, label, match[1], now()); err != nil {
		return false, err
	}
	return true, nil
}

func (runtime *Runtime) writeStageSuiteMarker(stageName string, stageErr error) error {
	message := fmt.Sprintf("SUITE %s: PASSED\n", stageName)
	if stageErr != nil {
		message = fmt.Sprintf("SUITE %s: FAILED -- %v\n", stageName, stageErr)
	}
	_, err := runtime.Writer.WriteSuiteLog(fmt.Sprintf("%s-attempt-%d", stageName, runtime.currentAttempt(stageName)), []byte(message))
	return err
}

func (runtime *Runtime) freshSyncReconcile(mode string) func(context.Context, *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error) {
	return func(ctx context.Context, environment *lifecycle.RunEnvironment) (lifecycle.ReconcileAction, error) {
		return runtime.reconcileFreshSyncSuite(ctx, environment, mode)
	}
}

func (runtime *Runtime) runCommand(ctx context.Context, stageName, path string, args, environment []string) error {
	var live bytes.Buffer
	result, err := runtime.Dependencies.Process(ctx, process.Command{
		Path: path, Args: args, Dir: runtime.Config.RepoRoot, Env: environment, Name: stageName,
		Stdout: io.MultiWriter(runtime.Dependencies.Output, &live), Stderr: io.MultiWriter(runtime.Dependencies.Output, &live),
		Logger: runtime.Dependencies.Logger,
	})
	output := append(append([]byte(nil), result.Stdout...), result.Stderr...)
	if live.Len() > len(output) {
		output = live.Bytes()
	}
	logName := fmt.Sprintf("%s-attempt-%d", stageName, runtime.currentAttempt(stageName))
	if _, writeErr := runtime.Writer.WriteSuiteLog(logName, output); writeErr != nil {
		return errors.Join(err, writeErr)
	}
	return err
}

func (runtime *Runtime) stageLogPassed(stageName string) bool {
	attempts := runtime.attemptCount(stageName)
	for attempt := attempts; attempt >= 1; attempt-- {
		path := filepath.Join(runtime.Writer.Layout().Logs, fmt.Sprintf("%s-attempt-%d.log", stageName, attempt))
		payload, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		text := string(payload)
		if strings.Contains(text, "SUITE "+stageName+": PASSED") || strings.Contains(text, "All ") && strings.Contains(text, " suites passed") {
			return true
		}
	}
	return false
}

func (runtime *Runtime) attemptCount(stageName string) int {
	state, err := runtime.Store.Load()
	if err != nil {
		return 0
	}
	count := 0
	for _, attempt := range state.Attempts {
		if attempt.Stage == stageName {
			count++
		}
	}
	return count
}

func (runtime *Runtime) currentAttempt(stageName string) int {
	count := runtime.attemptCount(stageName)
	if count < 1 {
		return 1
	}
	return count
}

type provisionProcessRunner struct {
	runtime *Runtime
	stage   string
}

func (runner provisionProcessRunner) Run(ctx context.Context, command string, args, environment []string, stdout, stderr io.Writer) error {
	_, err := runner.runtime.Dependencies.Process(ctx, process.Command{
		Path: command, Args: args, Dir: runner.runtime.Config.RepoRoot, Env: environment,
		Name: runner.stage, Stdout: stdout, Stderr: stderr, Logger: runner.runtime.Dependencies.Logger,
	})
	return err
}

func reportStatus(passed bool) report.Status {
	if passed {
		return report.StatusPassed
	}
	return report.StatusFailed
}

func suiteReport(name, stage string, status report.Status, started, finished time.Time, truncated bool) report.SuiteResult {
	result := report.SuiteResult{Name: name, Stage: stage, Status: status, StartedAt: started, FinishedAt: finished, DurationMillis: finished.Sub(started).Milliseconds()}
	if !passedStatus(status) {
		result.FailureCategory = report.FailureAssertion
	}
	if truncated {
		result.Details = "command output was truncated"
	}
	return result
}

func passedStatus(status report.Status) bool { return status == report.StatusPassed }

var _ provision.CommandRunner = provisionProcessRunner{}
