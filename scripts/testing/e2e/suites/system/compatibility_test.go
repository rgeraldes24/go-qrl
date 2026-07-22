// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	kurtosisapi "github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

func TestOpenCompatibilityCheckpointWiresDurableEvidenceAndFullUUIDController(t *testing.T) {
	t.Parallel()
	runtime, cfg, fake, serviceUUIDs, now := newCompatibilityFixture(t, PhaseSignerRestart)

	if runtime.Enclave.Name != "compatibility" || runtime.Enclave.UUID == "" || runtime.Enclave.Owned {
		t.Fatalf("runtime enclave = %+v, want exact borrowed full identity", runtime.Enclave)
	}
	if runtime.Options.ManagedJournal == nil || runtime.Options.TransactionRecorder == nil || runtime.Options.Evidence == nil || runtime.Options.ObservationRecorder == nil {
		t.Fatalf("compatibility options did not wire every durable recorder: %+v", runtime.Options)
	}
	if runtime.Options.ServiceUUIDs[cfg.ELServices[0]] != serviceUUIDs[cfg.ELServices[0]] {
		t.Fatalf("EL1 UUID = %q, want %q", runtime.Options.ServiceUUIDs[cfg.ELServices[0]], serviceUUIDs[cfg.ELServices[0]])
	}

	endpoint, err := runtime.Options.Controller.Endpoint(t.Context(), cfg.ELServices[0], "rpc", "http")
	if err != nil || endpoint != "http://127.0.0.1:18000" {
		t.Fatalf("EL1 endpoint = %q, %v", endpoint, err)
	}
	if err := runtime.Options.Controller.Stop(t.Context(), cfg.SignerService); err != nil {
		t.Fatal(err)
	}
	stopped, err := fake.Service(t.Context(), runtime.Enclave, serviceUUIDs[cfg.SignerService])
	if err != nil || stopped.Status != kurtosisapi.ServiceStatusStopped {
		t.Fatalf("signer after UUID stop = %+v, %v", stopped, err)
	}
	if err := runtime.Options.Controller.Start(t.Context(), cfg.SignerService); err != nil {
		t.Fatal(err)
	}

	preparedAt := now.Add(time.Second)
	intent := ManagedTransactionIntent{
		Phase: string(PhaseSignerRestart), Label: TransactionLabelSignerRecoveryTransfer, Origin: 0,
		OriginServiceName: cfg.ELServices[0], OriginServiceUUID: serviceUUIDs[cfg.ELServices[0]],
		ChainID: "0x1", From: managedAddressString(&cfg.SignerAddress), To: managedAddressString(&cfg.Recipient), Value: "0x1", Input: "0x",
		AccessList: []ManagedAccessTuple{}, Nonce: 7, StartBlock: 11,
		StartBlockHash: "0x" + strings.Repeat("ab", 32), PreparedAt: preparedAt,
	}
	if err := runtime.Options.ManagedJournal.RecordManagedTransactionIntent(t.Context(), intent); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Options.ManagedJournal.RecordManagedTransactionInitialAttempt(t.Context(), intent.Label, preparedAt.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	hash := common.HexToHash("0x" + strings.Repeat("cd", 32))
	if err := runtime.Options.TransactionRecorder.RecordTransaction(t.Context(), TransactionEvidence{
		Phase: PhaseSignerRestart, Label: intent.Label, Hash: hash, At: preparedAt.Add(2 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Options.Evidence.RecordRestart(t.Context(), RestartEvidence{
		Phase: PhaseSignerRestart, Service: cfg.SignerService, State: RestartStopIntent, At: preparedAt.Add(3 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Options.Evidence.RecordEndpoint(t.Context(), EndpointEvidence{
		Phase: PhaseSignerRestart, Service: cfg.SignerService, Kind: "http",
		Previous: "http://127.0.0.1:18550", Current: "http://127.0.0.1:18551", At: preparedAt.Add(4 * time.Second),
	}); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Options.ObservationRecorder.RecordSystemObservation(
		t.Context(), "compatibility-observation", `{"version":1}`, preparedAt.Add(5*time.Second),
	); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenCompatibilityCheckpoint(t.Context(), cfg.Checkpoint, cfg, fake)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Options.RecordedTransactions[intent.Label] != hash.Hex() {
		t.Fatalf("reopened transaction = %q, want %s", reopened.Options.RecordedTransactions[intent.Label], hash)
	}
	gotIntent, ok := reopened.Options.ManagedTransactionIntents[intent.Label]
	if !ok || gotIntent.Label != intent.Label || gotIntent.OriginServiceUUID != serviceUUIDs[cfg.ELServices[0]] {
		t.Fatalf("reopened managed intent = %+v, present=%t", gotIntent, ok)
	}
	if _, ok := reopened.Options.ManagedTransactionInitialAttempts[intent.Label]; !ok {
		t.Fatal("reopened checkpoint lost the initial-attempt marker")
	}
	if len(reopened.Options.RestartHistory) != 1 || reopened.Options.RestartHistory[0].Service != cfg.SignerService {
		t.Fatalf("reopened restart history = %+v", reopened.Options.RestartHistory)
	}
	if reopened.Options.RecordedObservations["compatibility-observation"] != `{"version":1}` {
		t.Fatalf("reopened observations = %+v", reopened.Options.RecordedObservations)
	}
}

func TestOpenCompatibilityCheckpointRejectsReorderedLegacyStages(t *testing.T) {
	t.Parallel()
	runtime, cfg, fake, _, _ := newCompatibilityFixture(t, PhaseBase)
	runtime.State.Completed[1] = "network-start"
	payload, err := json.MarshalIndent(runtime.State, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	payload = append(payload, '\n')
	if err := os.WriteFile(cfg.Checkpoint, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := OpenCompatibilityCheckpoint(t.Context(), cfg.Checkpoint, cfg, fake); err == nil || !strings.Contains(err.Error(), "ordered prefix") {
		t.Fatalf("reordered legacy stage error = %v", err)
	}
}

func TestOpenCompatibilityCheckpointRejectsIdentityDrift(t *testing.T) {
	t.Parallel()
	runtime, cfg, fake, serviceUUIDs, _ := newCompatibilityFixture(t, PhaseBase)

	wrongPhase := cfg
	wrongPhase.Phase = PhaseSignerRestart
	if _, err := OpenCompatibilityCheckpoint(t.Context(), cfg.Checkpoint, wrongPhase, fake); err == nil || !strings.Contains(err.Error(), "active stage") {
		t.Fatalf("active-stage mismatch error = %v", err)
	}
	duplicateService := cfg
	duplicateService.ELServices[1] = duplicateService.ELServices[0]
	if _, err := OpenCompatibilityCheckpoint(t.Context(), cfg.Checkpoint, duplicateService, fake); err == nil || !strings.Contains(err.Error(), "repeats required service name") {
		t.Fatalf("duplicate required service error = %v", err)
	}

	wrong := cfg
	wrong.Enclave = "another-enclave"
	if _, err := OpenCompatibilityCheckpoint(t.Context(), cfg.Checkpoint, wrong, fake); err == nil || !strings.Contains(err.Error(), "does not match checkpoint identity") {
		t.Fatalf("configured enclave mismatch error = %v", err)
	}

	if err := fake.AddService(runtime.Enclave, kurtosisapi.Service{
		Name: cfg.ELServices[0], UUID: strings.Repeat("f", 32), Status: kurtosisapi.ServiceStatusRunning,
		PublicIP: "127.0.0.1", PublicPorts: map[string]kurtosisapi.Port{"rpc": {ID: "rpc", Number: 19000}},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.Options.Controller.Endpoint(t.Context(), cfg.ELServices[0], "rpc", "http"); err == nil {
		t.Fatal("controller accepted a service name whose captured UUID disappeared")
	}
	if serviceUUIDs[cfg.ELServices[0]] == strings.Repeat("f", 32) {
		t.Fatal("test fixture did not exercise UUID replacement")
	}
}

func TestParseConfigRetainsCompatibilityCheckpoint(t *testing.T) {
	t.Parallel()
	cfg, err := ParseConfig([]string{"-phase", "base", "-checkpoint", "/tmp/system-checkpoint.json"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Checkpoint != "/tmp/system-checkpoint.json" {
		t.Fatalf("checkpoint = %q", cfg.Checkpoint)
	}
}

func TestCompatibilityRejectsPhaseAllWithoutCheckpointMutation(t *testing.T) {
	if _, err := compatibilityStageName(PhaseAll); err == nil || !strings.Contains(err.Error(), "no single checkpoint stage") {
		t.Fatalf("PhaseAll compatibility error = %v", err)
	}
}

func TestCompatibilityCheckpointRoundTripsAutomaticWithdrawalObservation(t *testing.T) {
	t.Parallel()
	runtime, cfg, fake, _, now := newCompatibilityFixture(t, PhaseBase)
	label := automaticWithdrawalObservationLabel(common.HexToHash("0x42"))
	value := `{"version":1,"block_number":42}`
	if err := runtime.Options.ObservationRecorder.RecordSystemObservation(
		t.Context(), label, value, now.Add(time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenCompatibilityCheckpoint(t.Context(), cfg.Checkpoint, cfg, fake)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.Options.RecordedObservations[label] != value {
		t.Fatalf("reopened automatic-withdrawal observations = %+v", reopened.Options.RecordedObservations)
	}
	wantKey := "system-base/" + label
	if reopened.State.SystemObservations[wantKey] != value {
		t.Fatalf("checkpoint automatic-withdrawal observations = %+v", reopened.State.SystemObservations)
	}
}

func newCompatibilityFixture(t *testing.T, phase Phase) (*CompatibilityRuntime, Config, *kurtosisapi.FakeClient, map[string]string, time.Time) {
	t.Helper()
	now := time.Date(2026, 7, 21, 9, 0, 0, 0, time.UTC)
	fake := kurtosisapi.NewFakeClient()
	enclave, err := fake.CreateEnclave(t.Context(), "compatibility")
	if err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Enclave = enclave.UUID
	cfg.Phase = phase
	cfg.Checkpoint = t.TempDir() + "/checkpoint.json"

	serviceUUIDs := make(map[string]string)
	for index, name := range compatibilityServiceNames(cfg) {
		uuid := fmt.Sprintf("%032x", index+100)
		serviceUUIDs[name] = uuid
		portID := "http"
		if name == cfg.ELServices[0] || name == cfg.ELServices[1] {
			portID = "rpc"
		} else if name == cfg.VCServices[0] || name == cfg.VCServices[1] {
			portID = "metrics"
		}
		port := uint16(18000 + index)
		if err := fake.AddService(enclave, kurtosisapi.Service{
			Name: name, UUID: uuid, Status: kurtosisapi.ServiceStatusRunning, PublicIP: "127.0.0.1",
			PublicPorts: map[string]kurtosisapi.Port{portID: {ID: portID, Number: port}},
		}); err != nil {
			t.Fatal(err)
		}
	}

	stageName, err := compatibilityStageName(phase)
	if err != nil {
		t.Fatal(err)
	}
	stageIndex := slicesIndex(compatibilityLegacyStageOrder, stageName)
	if stageIndex < 0 {
		t.Fatalf("compatibility stage %q is missing from fixed lifecycle order", stageName)
	}
	stateEnclave := enclave
	stateEnclave.Owned = false
	state := lifecycle.NewCheckpoint("compatibility-run", strings.Repeat("a", 40), "", t.TempDir(), strings.Repeat("b", 64), stateEnclave, now)
	state.Completed = append([]string(nil), compatibilityLegacyStageOrder[:stageIndex]...)
	state.Attempts = make([]lifecycle.Attempt, 0, stageIndex+1)
	for index, name := range state.Completed {
		finished := now.Add(time.Duration(index+1) * time.Second)
		exitCode := 0
		state.Attempts = append(state.Attempts, lifecycle.Attempt{
			Stage: name, Attempt: 1, StartedAt: finished.Add(-time.Second), FinishedAt: &finished, ExitCode: &exitCode,
		})
	}
	current := stageName
	state.Attempts = append(state.Attempts, lifecycle.Attempt{Stage: current, Attempt: 1, StartedAt: now.Add(time.Duration(stageIndex+1) * time.Second)})
	state.CurrentStage = &current
	state.UpdatedAt = state.Attempts[len(state.Attempts)-1].StartedAt
	storeOrder := append(append([]string(nil), state.Completed...), current)
	store := lifecycle.Store{Path: cfg.Checkpoint, StageOrder: storeOrder}
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	runtime, err := OpenCompatibilityCheckpoint(t.Context(), cfg.Checkpoint, cfg, fake)
	if err != nil {
		t.Fatal(err)
	}
	return runtime, cfg, fake, serviceUUIDs, now.Add(time.Duration(stageIndex+1) * time.Second)
}

func slicesIndex(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}
