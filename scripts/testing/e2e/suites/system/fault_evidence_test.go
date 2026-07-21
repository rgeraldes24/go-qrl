// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/rpc"
)

func TestSignerAccountBaselineObservationRoundTrip(t *testing.T) {
	want := [2]managedAccountState{
		{head: 10, nonce: 4, pendingNonce: 4, recipientBalance: big.NewInt(99)},
		{head: 11, nonce: 4, pendingNonce: 4, recipientBalance: big.NewInt(99)},
	}
	payload, err := json.Marshal(managedAccountBaselineToEvidence(want))
	if err != nil {
		t.Fatal(err)
	}
	check := &systemCheck{resume: resumeState{observations: map[string]string{signerAccountBaselineObservation: string(payload)}}}
	got, found, err := check.recordedManagedAccountBaseline()
	if err != nil || !found || !reflect.DeepEqual(got, want) {
		t.Fatalf("round trip = %+v, found=%t, error=%v; want %+v", got, found, err, want)
	}

	corrupt := managedAccountBaselineToEvidence(want)
	corrupt.EL[1].PendingNonce++
	if _, err := managedAccountBaselineFromEvidence(corrupt); err == nil {
		t.Fatal("divergent signer baseline was accepted")
	}
}

func TestParticipantFaultAndRecoveryBaselineObservationRoundTrip(t *testing.T) {
	preFault := testValidatorDutySnapshots(100, 100)
	execution := executionFinalityStatus{
		safeNumber: 12, safeHash: common.HexToHash("0x12"),
		finalizedNumber: 10, finalizedHash: common.HexToHash("0x10"),
	}
	evidence := participantFaultBaselineToEvidence(3, 96, execution, preFault)
	epoch, head, gotExecution, gotValidators, err := participantFaultBaselineFromEvidence(evidence)
	if err != nil || epoch != 3 || head != 96 || !reflect.DeepEqual(gotExecution, execution) || !reflect.DeepEqual(gotValidators, preFault) {
		t.Fatalf("participant baseline round trip = epoch:%d head:%d execution:%+v validators:%+v error:%v", epoch, head, gotExecution, gotValidators, err)
	}

	observed := testValidatorDutySnapshots(100, 200)
	observed[0].successfulAttestations++
	observed[1].successfulAttestations = 0
	observed[1].successfulProposals = 0
	baseline, err := restartedValidatorBaseline(preFault, observed)
	if err != nil {
		t.Fatal(err)
	}
	recovery := participantRecoveryBaselineEvidence{
		Version: 1, PreFault: validatorSnapshotsToEvidence(preFault),
		Baseline: validatorSnapshotsToEvidence(baseline), Observed: validatorSnapshotsToEvidence(observed),
	}
	raw, err := json.Marshal(recovery)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateSystemObservationValue(participantRecoveryBaselineObservation, string(raw)); err != nil {
		t.Fatalf("valid recovery baseline rejected: %v", err)
	}
	recovery.Baseline[0].SuccessfulAttestations++
	raw, _ = json.Marshal(recovery)
	if err := validateSystemObservationValue(participantRecoveryBaselineObservation, string(raw)); err == nil {
		t.Fatal("recovery baseline inconsistent with pre-fault/observed evidence was accepted")
	}
}

func TestSystemObservationResumeValidationRejectsMissingOrderedProof(t *testing.T) {
	cfg := config{
		phase: string(PhaseSignerRestart), signerSvc: "signer",
		elServices: [2]string{"el1", "el2"}, clServices: [2]string{"cl1", "cl2"}, vcServices: [2]string{"vc1", "vc2"},
	}
	resume := resumeState{restarts: map[string][]RestartState{"signer": {RestartStopIntent, RestartStopped}}}
	if err := validateSystemObservationResumeState(cfg, &resume); err == nil || !strings.Contains(err.Error(), "pre-outage") {
		t.Fatalf("missing signer baseline error = %v", err)
	}

	baseline := [2]managedAccountState{
		{head: 10, nonce: 1, pendingNonce: 1, recipientBalance: big.NewInt(2)},
		{head: 10, nonce: 1, pendingNonce: 1, recipientBalance: big.NewInt(2)},
	}
	raw, _ := json.Marshal(managedAccountBaselineToEvidence(baseline))
	resume.observations = map[string]string{signerAccountBaselineObservation: string(raw)}
	resume.restarts["signer"] = []RestartState{RestartStopIntent, RestartStopped, RestartStartIntent}
	if err := validateSystemObservationResumeState(cfg, &resume); err == nil || !strings.Contains(err.Error(), "outage assertion") {
		t.Fatalf("missing outage assertion error = %v", err)
	}
}

func TestRestartHistoryAcceptsEmergencyRecoveryButDoesNotMaskPlannedState(t *testing.T) {
	valid := [][]RestartState{
		{RestartStopIntent},
		{RestartStopIntent, RestartStopped},
		{RestartStopIntent, RestartStopped, RestartStartIntent, RestartStarted, RestartHealthy},
		{RestartStopIntent, RestartEmergencyStartIntent, RestartEmergencyStarted},
		{RestartStopIntent, RestartStopped, RestartEmergencyStartIntent, RestartEmergencyStarted, RestartHealthy},
		{RestartStopIntent, RestartStopped, RestartStartIntent, RestartEmergencyStartIntent, RestartEmergencyStarted},
		{RestartStopIntent, RestartStopped, RestartStartIntent, RestartStarted, RestartEmergencyStartIntent, RestartEmergencyStarted, RestartHealthy},
		{RestartStopIntent, RestartStopped, RestartStartIntent, RestartStarted, RestartHealthy, RestartEmergencyStartIntent, RestartEmergencyStarted, RestartHealthy},
		{RestartStopIntent, RestartStopped, RestartEmergencyStartIntent, RestartEmergencyStarted, RestartStopIntent},
		{RestartStopIntent, RestartStopped, RestartEmergencyStartIntent, RestartEmergencyStarted, RestartStopIntent, RestartStopped},
		{RestartStopIntent, RestartEmergencyStartIntent, RestartEmergencyStarted, RestartHealthy, RestartStopIntent, RestartStopped},
	}
	for _, states := range valid {
		if !validRestartStateSequence(states) {
			t.Fatalf("valid restart sequence rejected: %v", states)
		}
	}
	for _, states := range [][]RestartState{
		{RestartStopped},
		{RestartStopIntent, RestartStarted},
		{RestartStopIntent, RestartStopped, RestartEmergencyStarted},
		{RestartStopIntent, RestartStopped, RestartEmergencyStartIntent, RestartStarted},
		{RestartStopIntent, RestartStopped, RestartStartIntent, RestartStarted, RestartHealthy, RestartStopIntent},
		{RestartStopIntent, RestartStopped, RestartEmergencyStartIntent, RestartStopIntent},
	} {
		if validRestartStateSequence(states) {
			t.Fatalf("invalid restart sequence accepted: %v", states)
		}
	}
}

func TestCurrentRestartGenerationExcludesSafetyRecoveredHistory(t *testing.T) {
	history := []RestartState{
		RestartStopIntent, RestartStopped, RestartEmergencyStartIntent, RestartEmergencyStarted,
		RestartStopIntent, RestartStopped,
	}
	want := []RestartState{RestartStopIntent, RestartStopped}
	if got := currentRestartGeneration(history); !reflect.DeepEqual(got, want) {
		t.Fatalf("current restart generation = %v, want %v", got, want)
	}
}

func TestSafetyRecoveredGenerationCanReenterBeforeSignerMilestone(t *testing.T) {
	baseline := [2]managedAccountState{
		{head: 10, nonce: 1, pendingNonce: 1, recipientBalance: big.NewInt(2)},
		{head: 10, nonce: 1, pendingNonce: 1, recipientBalance: big.NewInt(2)},
	}
	raw, _ := json.Marshal(managedAccountBaselineToEvidence(baseline))
	resume := resumeState{
		restarts: map[string][]RestartState{"signer": {
			RestartStopIntent, RestartStopped, RestartStartIntent, RestartEmergencyStartIntent, RestartEmergencyStarted,
			RestartStopIntent, RestartStopped,
		}},
		observations: map[string]string{signerAccountBaselineObservation: string(raw)},
	}
	cfg := config{phase: string(PhaseSignerRestart), signerSvc: "signer"}
	if err := validateSystemObservationResumeState(cfg, &resume); err != nil {
		t.Fatalf("new signer fault generation rejected because of an old planned start: %v", err)
	}
}

func TestRecoverServiceUsesDistinctEmergencyTransitions(t *testing.T) {
	controller := &statusController{status: ServiceStopped}
	recorder := new(recordingEvidence)
	check := &systemCheck{
		cfg: config{phase: string(PhaseSignerRestart)}, k: controller, evidence: recorder,
		resume: resumeState{restarts: map[string][]RestartState{"signer": {RestartStopIntent, RestartStopped}}},
	}
	if err := check.recoverService("signer"); err != nil {
		t.Fatal(err)
	}
	if controller.starts != 1 {
		t.Fatalf("emergency starts = %d, want 1", controller.starts)
	}
	requireRestartStates(t, recorder.restarts, []RestartState{RestartEmergencyStartIntent, RestartEmergencyStarted})
}

func TestRecoverServiceDoesNotDuplicateStartAfterLostStopResponse(t *testing.T) {
	controller := &statusController{status: ServiceRunning}
	recorder := new(recordingEvidence)
	check := &systemCheck{
		cfg: config{phase: string(PhaseSignerRestart)}, k: controller, evidence: recorder,
		resume: resumeState{restarts: map[string][]RestartState{"signer": {RestartStopIntent}}},
	}
	if err := check.recoverService("signer"); err != nil {
		t.Fatal(err)
	}
	if controller.starts != 0 {
		t.Fatalf("already-running service received %d emergency Start calls", controller.starts)
	}
	requireRestartStates(t, recorder.restarts, []RestartState{RestartEmergencyStartIntent, RestartEmergencyStarted})
}

func TestRecoverServiceResumesExistingEmergencyIntentAfterTransientStatusError(t *testing.T) {
	controller := &statusController{status: ServiceStopped, statusErrors: []error{errors.New("temporary SDK failure"), nil}}
	recorder := new(recordingEvidence)
	check := &systemCheck{
		cfg: config{phase: string(PhaseSignerRestart)}, k: controller, evidence: recorder,
		resume: resumeState{restarts: map[string][]RestartState{"signer": {
			RestartStopIntent, RestartStopped, RestartEmergencyStartIntent,
		}}},
	}
	if err := check.resolveEmergencyStartIntent(t.Context(), "signer"); err == nil {
		t.Fatal("transient status failure unexpectedly resolved the emergency intent")
	}
	if err := check.recoverService("signer"); err != nil {
		t.Fatal(err)
	}
	if controller.starts != 1 {
		t.Fatalf("emergency starts = %d, want 1", controller.starts)
	}
	requireRestartStates(t, recorder.restarts, []RestartState{RestartEmergencyStarted})
}

func TestRecoverServiceAfterDurableStartedOrHealthy(t *testing.T) {
	for _, history := range [][]RestartState{
		{RestartStopIntent, RestartStopped, RestartStartIntent, RestartStarted},
		{RestartStopIntent, RestartStopped, RestartStartIntent, RestartStarted, RestartHealthy},
	} {
		t.Run(string(history[len(history)-1]), func(t *testing.T) {
			controller := &statusController{status: ServiceStopped}
			recorder := new(recordingEvidence)
			check := &systemCheck{
				cfg: config{phase: string(PhaseSignerRestart)}, k: controller, evidence: recorder,
				resume: resumeState{restarts: map[string][]RestartState{"signer": append([]RestartState(nil), history...)}},
			}
			recovered, err := check.ensureServiceRunning(t.Context(), "signer")
			if err != nil || !recovered || controller.starts != 1 {
				t.Fatalf("recover %v = recovered:%t starts:%d error:%v", history, recovered, controller.starts, err)
			}
			requireRestartStates(t, recorder.restarts, []RestartState{RestartEmergencyStartIntent, RestartEmergencyStarted})
		})
	}
}

func TestReenterFaultAfterEmergencyAppendsNewGenerationBeforeStop(t *testing.T) {
	controller := &statusController{status: ServiceRunning}
	recorder := new(recordingEvidence)
	check := &systemCheck{cfg: config{phase: string(PhaseSignerRestart)}, k: controller, evidence: recorder}
	stopped, err := check.reenterFaultAfterEmergency(t.Context(), "signer")
	if err != nil {
		t.Fatal(err)
	}
	if !stopped || controller.stops != 1 || controller.starts != 0 {
		t.Fatalf("re-entry mutation = stopped:%t stops:%d starts:%d", stopped, controller.stops, controller.starts)
	}
	requireRestartStates(t, recorder.restarts, []RestartState{RestartStopIntent, RestartStopped})
}

func TestReenterFaultAfterEmergencyMarksAmbiguousStopForSafetyRecovery(t *testing.T) {
	controller := &statusController{status: ServiceRunning, stopErr: errors.New("response lost")}
	recorder := new(recordingEvidence)
	check := &systemCheck{cfg: config{phase: string(PhaseSignerRestart)}, k: controller, evidence: recorder}
	stopped, err := check.reenterFaultAfterEmergency(t.Context(), "signer")
	if err == nil || !stopped || controller.stops != 1 || controller.starts != 0 {
		t.Fatalf("ambiguous re-entry = stopped:%t error:%v stops:%d starts:%d", stopped, err, controller.stops, controller.starts)
	}
	requireRestartStates(t, recorder.restarts, []RestartState{RestartStopIntent})
}

func TestReenterFaultAfterEmergencyFailsClosedBeforeMutation(t *testing.T) {
	for _, test := range []struct {
		name       string
		controller *statusController
		recorder   *recordingEvidence
	}{
		{name: "service not running", controller: &statusController{status: ServiceStopped}, recorder: new(recordingEvidence)},
		{name: "intent checkpoint failure", controller: &statusController{status: ServiceRunning}, recorder: &recordingEvidence{err: errors.New("disk full")}},
	} {
		t.Run(test.name, func(t *testing.T) {
			check := &systemCheck{cfg: config{phase: string(PhaseSignerRestart)}, k: test.controller, evidence: test.recorder}
			stopped, err := check.reenterFaultAfterEmergency(t.Context(), "signer")
			if err == nil || stopped || test.controller.stops != 0 || test.controller.starts != 0 {
				t.Fatalf("fail-closed re-entry = stopped:%t error:%v stops:%d starts:%d", stopped, err, test.controller.stops, test.controller.starts)
			}
		})
	}
}

func TestSignerBaselineCheckpointFailurePreventsStopMutation(t *testing.T) {
	server := rpc.NewServer()
	if err := server.RegisterName("qrl", new(testSignerOutageExecutionAPI)); err != nil {
		t.Fatal(err)
	}
	client := qrlclient.NewClient(rpc.DialInProc(server))
	t.Cleanup(func() { client.Close(); server.Stop() })
	recordFailure := errors.New("disk full")
	controller := &statusController{status: ServiceRunning}
	check := &systemCheck{
		cfg: config{
			phase: string(PhaseSignerRestart), signerSvc: "signer", signerAddress: common.Address{common.AddressLength - 1: 1},
			recipient: common.Address{common.AddressLength - 1: 2}, pollInterval: time.Millisecond,
		},
		k: controller, clients: [2]*qrlclient.Client{client, client},
		observations: SystemObservationRecorderFunc(func(context.Context, string, string, time.Time) error { return recordFailure }),
		resume:       resumeState{observations: make(map[string]string)},
	}
	if err := check.restartSigner(t.Context()); !errors.Is(err, recordFailure) {
		t.Fatalf("restart error = %v, want %v", err, recordFailure)
	}
	if controller.stops != 0 || controller.starts != 0 {
		t.Fatalf("checkpoint failure mutated signer: stops=%d starts=%d", controller.stops, controller.starts)
	}
}

func testValidatorDutySnapshots(firstStart, secondStart float64) validatorDutySnapshots {
	result := validatorDutySnapshots{}
	for index, start := range []float64{firstStart, secondStart} {
		keys := make(map[string]struct{}, expectedValidatorsPerClient)
		for key := 0; key < expectedValidatorsPerClient; key++ {
			keys[fmt.Sprintf("0x%02x%04x", index, key)] = struct{}{}
		}
		result[index] = validatorDutySnapshot{
			reportedValidators: expectedValidatorsPerClient, activeValidators: expectedValidatorsPerClient,
			attestedValidators: expectedValidatorsPerClient, activePubkeys: keys, processStartTime: start,
			successfulAttestations: 100, successfulProposals: 3,
		}
	}
	return result
}
