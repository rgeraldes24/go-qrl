// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
	systemSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/system"
)

const (
	systemTestSHA    = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	systemTestDigest = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	systemTestTreeID = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

type recordingSystemClient struct {
	*kurtosis.FakeClient
	startedWith string
	stoppedWith string
}

func (client *recordingSystemClient) StartService(ctx context.Context, enclave lifecycle.EnclaveRef, identifier string) error {
	client.startedWith = identifier
	return client.FakeClient.StartService(ctx, enclave, identifier)
}

func (client *recordingSystemClient) StopService(ctx context.Context, enclave lifecycle.EnclaveRef, identifier string) error {
	client.stoppedWith = identifier
	return client.FakeClient.StopService(ctx, enclave, identifier)
}

func TestSystemStageRunsImportableSuiteWithSDKAndDurableEvidence(t *testing.T) {
	runtime, environment, client := newSystemHarnessFixture(t, []string{"system-base"})
	processCalled := false
	runtime.Dependencies.Process = func(context.Context, process.Command) (process.Result, error) {
		processCalled = true
		return process.Result{}, fmt.Errorf("process runner must not be used by system stage")
	}
	var captured systemSuite.Config
	runtime.Dependencies.System = func(ctx context.Context, configuration systemSuite.Config, options systemSuite.Options) error {
		captured = configuration
		if options.Controller == nil || options.Evidence == nil || options.TransactionRecorder == nil {
			return fmt.Errorf("system integrations are missing")
		}
		if err := options.TransactionRecorder.RecordTransaction(ctx, systemSuite.TransactionEvidence{
			Phase: configuration.Phase, Label: systemSuite.TransactionLabelBaseEL1Transfer,
			Hash: common.HexToHash("0x1234"), At: time.Unix(1_700_000_009, 0).UTC(),
		}); err != nil {
			return err
		}
		oldEndpoint, err := options.Controller.Endpoint(ctx, configuration.SignerService, "http", "http")
		if err != nil {
			return err
		}
		now := time.Unix(1_700_000_010, 0).UTC()
		record := func(state systemSuite.RestartState) error {
			now = now.Add(time.Second)
			return options.Evidence.RecordRestart(ctx, systemSuite.RestartEvidence{
				Phase: configuration.Phase, Service: configuration.SignerService, State: state, At: now,
			})
		}
		if err := record(systemSuite.RestartStopIntent); err != nil {
			return err
		}
		if err := options.Controller.Stop(ctx, configuration.SignerService); err != nil {
			return err
		}
		if err := record(systemSuite.RestartStopped); err != nil {
			return err
		}
		if err := record(systemSuite.RestartStartIntent); err != nil {
			return err
		}
		if err := options.Controller.Start(ctx, configuration.SignerService); err != nil {
			return err
		}
		if err := record(systemSuite.RestartStarted); err != nil {
			return err
		}
		newEndpoint, err := options.Controller.Endpoint(ctx, configuration.SignerService, "http", "http")
		if err != nil {
			return err
		}
		if newEndpoint == oldEndpoint {
			return fmt.Errorf("fake restart did not refresh the endpoint")
		}
		now = now.Add(time.Second)
		if err := options.Evidence.RecordEndpoint(ctx, systemSuite.EndpointEvidence{
			Phase: configuration.Phase, Service: configuration.SignerService, Kind: "signer-http",
			Previous: oldEndpoint, Current: newEndpoint, At: now,
		}); err != nil {
			return err
		}
		return record(systemSuite.RestartHealthy)
	}

	if err := runtime.systemStage("base")(t.Context(), environment); err != nil {
		t.Fatal(err)
	}
	if processCalled {
		t.Fatal("system stage invoked the subprocess runner")
	}
	if captured.Phase != systemSuite.PhaseBase || !captured.RequireZeroDutyHistory {
		t.Fatalf("first-attempt system configuration = phase %q strict %t", captured.Phase, captured.RequireZeroDutyHistory)
	}
	if captured.RPCURLs != [2]string{} || captured.SignerURL != "" {
		t.Fatalf("system endpoints bypassed SDK discovery: RPC=%v signer=%q", captured.RPCURLs, captured.SignerURL)
	}
	signerUUID := runtime.Topology.Signer.Service.UUID
	if client.stoppedWith != signerUUID || client.startedWith != signerUUID {
		t.Fatalf("service lifecycle identifiers = stop %q start %q, want UUID %q", client.stoppedWith, client.startedWith, signerUUID)
	}
	loaded, err := runtime.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	wantStates := []string{"stop-intent", "stopped", "start-intent", "started", "healthy"}
	if len(loaded.ServiceTransitions) != len(wantStates) {
		t.Fatalf("service transitions = %+v", loaded.ServiceTransitions)
	}
	for i, want := range wantStates {
		transition := loaded.ServiceTransitions[i]
		if transition.State != want || transition.ServiceUUID != signerUUID || transition.Phase != string(systemSuite.PhaseBase) {
			t.Fatalf("transition[%d] = %+v, want state %q UUID %q", i, transition, want, signerUUID)
		}
	}
	if len(loaded.EndpointRefreshes) != 1 || loaded.EndpointRefreshes[0].ServiceUUID != signerUUID || loaded.EndpointRefreshes[0].Previous == loaded.EndpointRefreshes[0].Current {
		t.Fatalf("endpoint refresh evidence = %+v", loaded.EndpointRefreshes)
	}
	transactionLabel := "system-base/" + systemSuite.TransactionLabelBaseEL1Transfer
	if loaded.Transactions[transactionLabel] != common.HexToHash("0x1234").Hex() {
		t.Fatalf("system transaction evidence = %v", loaded.Transactions)
	}
	persisted, err := os.ReadFile(runtime.Writer.Layout().Topology)
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := topology.ParseTopology(persisted)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Signer.HTTP.PublicURL != runtime.Topology.Signer.HTTP.PublicURL || refreshed.Signer.HTTP.PublicURL != loaded.EndpointRefreshes[0].Current {
		t.Fatalf("refreshed topology endpoint = %q, runtime %q, evidence %q", refreshed.Signer.HTTP.PublicURL, runtime.Topology.Signer.HTTP.PublicURL, loaded.EndpointRefreshes[0].Current)
	}
	logPath := filepath.Join(runtime.Writer.Layout().Logs, "system-base-attempt-1.log")
	logData, err := os.ReadFile(logPath)
	if err != nil || string(logData) != "SUITE system-base: PASSED\n" {
		t.Fatalf("system suite log = %q, %v", logData, err)
	}
}

func TestSystemServiceControllerUsesExplicitKurtosisStatus(t *testing.T) {
	runtime, _, client := newSystemHarnessFixture(t, []string{"system-base"})
	controller := systemServiceController{runtime: runtime}
	serviceName := runtime.Topology.Signer.Service.Name
	serviceUUID := runtime.Topology.Signer.Service.UUID

	status, err := controller.Status(t.Context(), serviceName)
	if err != nil || status != systemSuite.ServiceRunning {
		t.Fatalf("running status = %q, %v", status, err)
	}
	if err := client.StopService(t.Context(), runtime.Enclave, serviceUUID); err != nil {
		t.Fatal(err)
	}
	stopped, err := client.Service(t.Context(), runtime.Enclave, serviceUUID)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.PublicIP == "" || len(stopped.PublicPorts) == 0 {
		t.Fatalf("fake did not retain stale endpoint metadata: %+v", stopped)
	}
	status, err = controller.Status(t.Context(), serviceName)
	if err != nil || status != systemSuite.ServiceStopped {
		t.Fatalf("stopped status with published endpoint = %q, %v", status, err)
	}

	if err := client.SetServiceStatus(runtime.Enclave, serviceUUID, kurtosis.ServiceStatusUnknown); err != nil {
		t.Fatal(err)
	}
	if status, err = controller.Status(t.Context(), serviceName); err == nil || !strings.Contains(err.Error(), "unsafe Kurtosis status") {
		t.Fatalf("unknown status = %q, %v", status, err)
	}
}

func TestSystemStageStrictDutyHistoryOnlyOnFirstAttempt(t *testing.T) {
	runtime, environment, _ := newSystemHarnessFixture(t, []string{"system-signer"})
	state := *environment.State
	stageName := "system-signer"
	started := time.Unix(1_700_000_000, 0).UTC()
	finished := started.Add(time.Second)
	exitCode := 1
	state.Attempts = []lifecycle.Attempt{
		{Stage: stageName, Attempt: 1, StartedAt: started, FinishedAt: &finished, ExitCode: &exitCode},
		{Stage: stageName, Attempt: 2, StartedAt: finished.Add(time.Second)},
	}
	state.CurrentStage = &stageName
	state.UpdatedAt = finished.Add(time.Second)
	if err := runtime.Store.Save(state); err != nil {
		t.Fatal(err)
	}
	environment.State = &state
	var captured systemSuite.Config
	runtime.Dependencies.System = func(_ context.Context, configuration systemSuite.Config, _ systemSuite.Options) error {
		captured = configuration
		return nil
	}

	if err := runtime.systemStage("signer-restart")(t.Context(), environment); err != nil {
		t.Fatal(err)
	}
	if captured.Phase != systemSuite.PhaseSignerRestart || captured.RequireZeroDutyHistory {
		t.Fatalf("retry configuration = phase %q strict %t", captured.Phase, captured.RequireZeroDutyHistory)
	}
	if _, err := os.Stat(filepath.Join(runtime.Writer.Layout().Logs, "system-signer-attempt-2.log")); err != nil {
		t.Fatalf("retry log was not attempt-scoped: %v", err)
	}
}

func TestSystemObservationRecorderPersistsStageScopedEvidence(t *testing.T) {
	runtime, environment, _ := newSystemHarnessFixture(t, []string{"system-signer"})
	recorder := systemEvidenceRecorder{
		runtime: runtime, environment: environment, phase: systemSuite.PhaseSignerRestart, stageName: "system-signer",
	}
	value := `{"version":1,"completed":true}`
	if err := recorder.RecordSystemObservation(t.Context(), "signer-restart/outage-asserted", value, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	loaded, err := runtime.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	wantLabel := "system-signer/signer-restart/outage-asserted"
	if loaded.SystemObservations[wantLabel] != value {
		t.Fatalf("durable observations = %v", loaded.SystemObservations)
	}
	filtered := systemObservations(loaded.SystemObservations, "system-signer")
	if filtered["signer-restart/outage-asserted"] != value || len(filtered) != 1 {
		t.Fatalf("filtered observations = %v", filtered)
	}
}

func TestSystemObservationRecorderPersistsAutomaticWithdrawalUnderExactBaseKey(t *testing.T) {
	runtime, environment, _ := newSystemHarnessFixture(t, []string{"system-base"})
	recorder := systemEvidenceRecorder{
		runtime: runtime, environment: environment, phase: systemSuite.PhaseBase, stageName: "system-base",
	}
	const label = "base/automatic-withdrawal-fresh-balance-verified/0000000000000000000000000000000000000000000000000000000000000042"
	value := `{"version":1,"block_number":42}`
	if err := recorder.RecordSystemObservation(t.Context(), label, value, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	loaded, err := runtime.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	const wantKey = "system-base/base/automatic-withdrawal-fresh-balance-verified/0000000000000000000000000000000000000000000000000000000000000042"
	if loaded.SystemObservations[wantKey] != value {
		t.Fatalf("durable automatic-withdrawal observation = %v", loaded.SystemObservations)
	}
	filtered := systemObservations(loaded.SystemObservations, "system-base")
	if filtered[label] != value || len(filtered) != 1 {
		t.Fatalf("filtered base observations = %v", filtered)
	}
}

func TestSystemMutationReconcileUsesDurableExternalState(t *testing.T) {
	for _, test := range []struct {
		name       string
		prepare    func(*testing.T, *Runtime, *lifecycle.RunEnvironment)
		wantAction lifecycle.ReconcileAction
		wantError  string
	}{
		{
			name: "no mutation retries", wantAction: lifecycle.ReconcileRetry,
			prepare: func(*testing.T, *Runtime, *lifecycle.RunEnvironment) {},
		},
		{
			name: "submitted transaction continues from checkpoint", wantAction: lifecycle.ReconcileRetry,
			prepare: func(t *testing.T, runtime *Runtime, environment *lifecycle.RunEnvironment) {
				t.Helper()
				if err := environment.State.RecordTransaction(runtime.Store, "system-base/"+systemSuite.TransactionLabelBaseEL1Transfer, common.HexToHash("0x1234").Hex(), time.Now()); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "failed transaction checkpoint recovers hash", wantAction: lifecycle.ReconcileRetry,
			prepare: func(t *testing.T, runtime *Runtime, environment *lifecycle.RunEnvironment) {
				t.Helper()
				hash := common.HexToHash("0x1234").Hex()
				environment.State.Attempts[0].FailureMessage = "record lifecycle evidence: transaction " + hash + " as base/01-managed-transfer-el1 was submitted but could not be recorded: disk full"
				environment.State.FailureMessage = environment.State.Attempts[0].FailureMessage
				if err := runtime.Store.Save(*environment.State); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "service intent is resolved by suite", wantAction: lifecycle.ReconcileRetry,
			prepare: func(t *testing.T, runtime *Runtime, environment *lifecycle.RunEnvironment) {
				t.Helper()
				if err := environment.State.RecordServiceTransition(runtime.Store, lifecycle.ServiceTransition{
					Phase: string(systemSuite.PhaseBase), ServiceName: "signer", ServiceUUID: systemTestUUID(7),
					State: string(systemSuite.RestartStopIntent), At: time.Now(),
				}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "completed service transition continues", wantAction: lifecycle.ReconcileRetry,
			prepare: func(t *testing.T, runtime *Runtime, environment *lifecycle.RunEnvironment) {
				t.Helper()
				for _, state := range []systemSuite.RestartState{systemSuite.RestartStopIntent, systemSuite.RestartStopped} {
					if err := environment.State.RecordServiceTransition(runtime.Store, lifecycle.ServiceTransition{
						Phase: string(systemSuite.PhaseBase), ServiceName: "signer", ServiceUUID: systemTestUUID(7),
						State: string(state), At: time.Now(),
					}); err != nil {
						t.Fatal(err)
					}
				}
			},
		},
		{
			name: "success log completes", wantAction: lifecycle.ReconcileComplete,
			prepare: func(t *testing.T, runtime *Runtime, environment *lifecycle.RunEnvironment) {
				t.Helper()
				if err := environment.State.RecordTransaction(runtime.Store, "system-base/"+systemSuite.TransactionLabelBaseEL1Transfer, common.HexToHash("0x1234").Hex(), time.Now()); err != nil {
					t.Fatal(err)
				}
				if _, err := runtime.Writer.WriteSuiteLog("system-base-attempt-1", []byte("SUITE system-base: PASSED\n")); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			runtime, environment, _ := newSystemHarnessFixture(t, []string{"system-base"})
			markSystemStageFailed(t, runtime, environment, "system-base")
			test.prepare(t, runtime, environment)
			action, err := runtime.systemMutationReconcile("system-base", systemSuite.PhaseBase)(t.Context(), environment)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("action = %q, error = %v", action, err)
				}
				return
			}
			if err != nil || action != test.wantAction {
				t.Fatalf("action = %q, error = %v, want %q", action, err, test.wantAction)
			}
		})
	}
}

func markSystemStageFailed(t *testing.T, runtime *Runtime, environment *lifecycle.RunEnvironment, stageName string) {
	t.Helper()
	state := *environment.State
	started := time.Unix(1_700_000_000, 0).UTC()
	finished := started.Add(time.Second)
	exitCode := 1
	state.Attempts = []lifecycle.Attempt{{
		Stage: stageName, Attempt: 1, StartedAt: started, FinishedAt: &finished, ExitCode: &exitCode,
		FailureCategory: lifecycle.FailureAssertion, FailureMessage: "interrupted",
	}}
	state.Status = lifecycle.StatusFailed
	state.CurrentStage = &state.Attempts[0].Stage
	state.FailureCategory = lifecycle.FailureAssertion
	state.FailureMessage = "interrupted"
	state.UpdatedAt = finished
	if err := runtime.Store.Save(state); err != nil {
		t.Fatal(err)
	}
	environment.State = &state
}

func newSystemHarnessFixture(t *testing.T, stageOrder []string) (*Runtime, *lifecycle.RunEnvironment, *recordingSystemClient) {
	t.Helper()
	fake := kurtosis.NewFakeClient()
	enclave, err := fake.CreateEnclave(t.Context(), "system-test")
	if err != nil {
		t.Fatal(err)
	}
	client := &recordingSystemClient{FakeClient: fake}
	discovered, services := systemTestTopology()
	for _, service := range services {
		if err := fake.AddService(enclave, service); err != nil {
			t.Fatal(err)
		}
	}
	root := t.TempDir()
	writer, err := report.New(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteTopology(discovered); err != nil {
		t.Fatal(err)
	}
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: stageOrder}
	now := time.Unix(1_700_000_000, 0).UTC()
	state := lifecycle.NewCheckpoint("run-system", systemTestSHA, systemTestDigest, filepath.Join(root, "dump"), systemTestTreeID, enclave, now)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		RunID: "run-system", Enclave: enclave, Writer: writer, Store: store, Topology: &discovered,
		Dependencies: Dependencies{Client: client, Now: func() time.Time { return now }},
	}
	environment := &lifecycle.RunEnvironment{Enclave: enclave, State: &state, Values: map[string]any{}}
	return runtime, environment, client
}

func systemTestTopology() (topology.Topology, []kurtosis.Service) {
	type serviceFixture struct {
		name      string
		id        int
		privateIP string
		ports     map[string]uint16
		base      uint16
	}
	fixtures := []serviceFixture{
		{"el-1", 1, "10.0.0.1", map[string]uint16{"rpc": 8545, "ws": 8546}, 18100},
		{"el-2", 2, "10.0.0.2", map[string]uint16{"rpc": 8545, "ws": 8546}, 18200},
		{"cl-1", 3, "10.0.0.3", map[string]uint16{"http": 3500}, 18300},
		{"cl-2", 4, "10.0.0.4", map[string]uint16{"http": 3500}, 18310},
		{"vc-1", 5, "10.0.0.5", map[string]uint16{"metrics": 8080}, 18401},
		{"vc-2", 6, "10.0.0.6", map[string]uint16{"metrics": 8080}, 18402},
		{"signer", 7, "10.0.0.7", map[string]uint16{"http": 8550}, 18550},
	}
	services := make([]kurtosis.Service, 0, len(fixtures))
	byName := make(map[string]kurtosis.Service, len(fixtures))
	for _, fixture := range fixtures {
		service := kurtosis.Service{
			Name: fixture.name, UUID: systemTestUUID(fixture.id), PrivateIP: fixture.privateIP, PublicIP: "127.0.0.1",
			PrivatePorts: map[string]kurtosis.Port{}, PublicPorts: map[string]kurtosis.Port{}, Labels: map[string]string{},
		}
		index := uint16(0)
		for _, portID := range []string{"http", "metrics", "rpc", "ws"} {
			privatePort, ok := fixture.ports[portID]
			if !ok {
				continue
			}
			service.PrivatePorts[portID] = kurtosis.Port{ID: portID, Number: privatePort, TransportProtocol: "TCP"}
			service.PublicPorts[portID] = kurtosis.Port{ID: portID, Number: fixture.base + index, TransportProtocol: "TCP"}
			index++
		}
		services = append(services, service)
		byName[service.Name] = service
	}
	endpoint := func(serviceName, portID, scheme, path string) topology.Endpoint {
		service := byName[serviceName]
		privatePort := service.PrivatePorts[portID]
		publicPort := service.PublicPorts[portID]
		return topology.Endpoint{
			PortID:     portID,
			PrivateURL: fmt.Sprintf("%s://%s:%d%s", scheme, service.PrivateIP, privatePort.Number, path),
			PublicURL:  fmt.Sprintf("%s://%s:%d%s", scheme, service.PublicIP, publicPort.Number, path),
		}
	}
	identity := func(name string) topology.ServiceIdentity {
		return topology.ServiceIdentity{Name: name, UUID: byName[name].UUID}
	}
	discovered := topology.Topology{Schema: topology.TopologySchemaVersion}
	for i := 1; i <= 2; i++ {
		el := fmt.Sprintf("el-%d", i)
		cl := fmt.Sprintf("cl-%d", i)
		vc := fmt.Sprintf("vc-%d", i)
		discovered.Execution = append(discovered.Execution, topology.ExecutionNode{
			Service: identity(el), Client: "gqrl", RPC: endpoint(el, "rpc", "http", ""), WS: endpoint(el, "ws", "ws", ""),
		})
		discovered.Consensus = append(discovered.Consensus, topology.ConsensusNode{
			Service: identity(cl), Client: "qrysm", HTTP: endpoint(cl, "http", "http", ""),
		})
		discovered.Validators = append(discovered.Validators, topology.ValidatorNode{
			Service: identity(vc), Client: "qrysm", Metrics: endpoint(vc, "metrics", "http", "/metrics"), MetricsPath: "/metrics",
		})
	}
	discovered.Signer = topology.SignerNode{Service: identity("signer"), Client: "clef", HTTP: endpoint("signer", "http", "http", "")}
	return discovered, services
}

func systemTestUUID(value int) string {
	return fmt.Sprintf("%032x", value)
}

func TestSystemPublicEndpointRejectsUnknownRolePort(t *testing.T) {
	discovered, _ := systemTestTopology()
	if _, err := systemPublicEndpoint(discovered, "el-1", "metrics", "http"); err == nil || !strings.Contains(err.Error(), "no discovered public port") {
		t.Fatalf("unexpected missing-port error: %v", err)
	}
}
