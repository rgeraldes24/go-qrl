// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

type statusController struct {
	status       ServiceStatus
	err          error
	statusErrors []error
	stopErr      error
	starts       int
	stops        int
}

func (controller *statusController) Endpoint(context.Context, string, string, string) (string, error) {
	return "", errors.New("unused")
}
func (controller *statusController) Status(context.Context, string) (ServiceStatus, error) {
	if len(controller.statusErrors) != 0 {
		err := controller.statusErrors[0]
		controller.statusErrors = controller.statusErrors[1:]
		return controller.status, err
	}
	return controller.status, controller.err
}
func (controller *statusController) Stop(context.Context, string) error {
	controller.stops++
	return controller.stopErr
}
func (controller *statusController) Start(context.Context, string) error {
	controller.starts++
	return nil
}

func TestPublicConfigPreservesSerializedPhaseAndTopology(t *testing.T) {
	cfg, err := ParseConfig([]string{
		"-phase", "participant-restart",
		"-enclave", "owned-enclave",
		"-rpc1", "http://127.0.0.1:18545",
		"-rpc2", "http://127.0.0.1:28545",
		"-cl1", "http://127.0.0.1:13500",
		"-cl2", "http://127.0.0.1:23500",
		"-vc1-metrics", "http://127.0.0.1:18080/metrics",
		"-vc2-metrics", "http://127.0.0.1:28080/metrics",
		"-signer", "http://127.0.0.1:18550",
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Phase != PhaseParticipantRestart || cfg.Enclave != "owned-enclave" {
		t.Fatalf("unexpected public phase/topology: %+v", cfg)
	}
	runtimeConfig, err := cfg.internal()
	if err != nil {
		t.Fatal(err)
	}
	if runtimeConfig.phase != string(PhaseParticipantRestart) || runtimeConfig.rpcURLs != cfg.RPCURLs || runtimeConfig.signerAddress != cfg.SignerAddress {
		t.Fatalf("public configuration did not survive internal conversion: %+v", runtimeConfig)
	}

	cfg.Phase = Phase("replay-everything")
	if _, err := cfg.internal(); err == nil {
		t.Fatal("invalid public phase was accepted")
	}
}

func TestDefaultConfigReturnsIndependentValidatedValues(t *testing.T) {
	first := DefaultConfig()
	second := DefaultConfig()
	if second.Phase != PhaseBase {
		t.Fatalf("default phase = %q, want concrete phase %q", second.Phase, PhaseBase)
	}
	first.ELServices[0] = "changed"
	if second.ELServices[0] == first.ELServices[0] {
		t.Fatal("DefaultConfig returned aliased service storage")
	}
	if _, err := second.internal(); err != nil {
		t.Fatalf("default public configuration is invalid: %v", err)
	}
}

type recordingEvidence struct {
	restarts  []RestartEvidence
	endpoints []EndpointEvidence
	err       error
}

func (r *recordingEvidence) RecordRestart(_ context.Context, evidence RestartEvidence) error {
	if r.err != nil {
		return r.err
	}
	r.restarts = append(r.restarts, evidence)
	return nil
}

func (r *recordingEvidence) RecordEndpoint(_ context.Context, evidence EndpointEvidence) error {
	if r.err != nil {
		return r.err
	}
	r.endpoints = append(r.endpoints, evidence)
	return nil
}

func TestEvidenceRecordsPhaseServiceAndEndpointRefresh(t *testing.T) {
	fixed := time.Unix(1_700_000_000, 0)
	recorder := new(recordingEvidence)
	check := &systemCheck{
		cfg:      config{phase: string(PhaseSignerRestart)},
		evidence: recorder,
		now:      func() time.Time { return fixed },
	}
	if err := check.recordRestart(t.Context(), "signer-clef", RestartStopIntent); err != nil {
		t.Fatal(err)
	}
	if err := check.recordEndpoint(t.Context(), "signer-clef", "signer-http", "http://old", "http://new"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.restarts) != 1 || recorder.restarts[0].Phase != PhaseSignerRestart || recorder.restarts[0].Service != "signer-clef" || !recorder.restarts[0].At.Equal(fixed) {
		t.Fatalf("unexpected restart evidence: %+v", recorder.restarts)
	}
	if len(recorder.endpoints) != 1 || recorder.endpoints[0].Previous != "http://old" || recorder.endpoints[0].Current != "http://new" || !recorder.endpoints[0].At.Equal(fixed) {
		t.Fatalf("unexpected endpoint evidence: %+v", recorder.endpoints)
	}
	if err := check.recordEndpoint(t.Context(), "signer-clef", "signer-http", "http://new", "http://new"); err != nil {
		t.Fatal(err)
	}
	if len(recorder.endpoints) != 1 {
		t.Fatalf("unchanged endpoint produced evidence: %+v", recorder.endpoints)
	}
}

func TestWaitForDoesNotRetryEvidenceFailure(t *testing.T) {
	want := errors.New("checkpoint unavailable")
	attempts := 0
	err := waitFor(t.Context(), time.Second, time.Millisecond, "durable evidence", func(context.Context) (bool, error) {
		attempts++
		return false, &evidenceError{err: want}
	})
	if !errors.Is(err, want) {
		t.Fatalf("waitFor error = %v, want evidence failure", err)
	}
	if attempts != 1 {
		t.Fatalf("evidence failure was retried %d times", attempts)
	}
}

func TestResolveRestartIntentUsesContainerStateAndPreservesUnknown(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		resolve    func(*systemCheck) error
		status     ServiceStatus
		statusErr  error
		wantStarts int
		wantStops  int
		wantState  RestartState
		history    []RestartState
		wantError  bool
	}{
		{name: "stop intent already applied", status: ServiceStopped, history: []RestartState{RestartStopIntent}, resolve: func(check *systemCheck) error { return check.resolveStopIntent(t.Context(), "service") }, wantState: RestartStopped},
		{name: "stop intent not applied", status: ServiceRunning, history: []RestartState{RestartStopIntent}, resolve: func(check *systemCheck) error { return check.resolveStopIntent(t.Context(), "service") }, wantStops: 1, wantState: RestartStopped},
		{name: "start intent already applied but not readiness-probed", status: ServiceRunning, history: []RestartState{RestartStopIntent, RestartStopped, RestartStartIntent}, resolve: func(check *systemCheck) error { return check.resolveStartIntent(t.Context(), "service") }, wantState: RestartStarted},
		{name: "start intent not applied", status: ServiceStopped, history: []RestartState{RestartStopIntent, RestartStopped, RestartStartIntent}, resolve: func(check *systemCheck) error { return check.resolveStartIntent(t.Context(), "service") }, wantStarts: 1, wantState: RestartStarted},
		{name: "transient status failure remains unknown", statusErr: errors.New("temporary SDK failure"), history: []RestartState{RestartStopIntent, RestartStopped, RestartStartIntent}, resolve: func(check *systemCheck) error { return check.resolveStartIntent(t.Context(), "service") }, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			controller := &statusController{status: test.status, err: test.statusErr}
			evidence := new(recordingEvidence)
			check := &systemCheck{
				cfg: config{phase: string(PhaseSignerRestart)}, k: controller, evidence: evidence,
				resume: resumeState{restarts: map[string][]RestartState{"service": append([]RestartState(nil), test.history...)}},
			}
			err := test.resolve(check)
			if (err != nil) != test.wantError {
				t.Fatalf("resolve error = %v, wantError=%t", err, test.wantError)
			}
			if controller.starts != test.wantStarts || controller.stops != test.wantStops {
				t.Fatalf("mutations = start:%d stop:%d, want start:%d stop:%d", controller.starts, controller.stops, test.wantStarts, test.wantStops)
			}
			if test.wantError {
				if len(evidence.restarts) != 0 {
					t.Fatalf("unknown status advanced evidence: %+v", evidence.restarts)
				}
			} else if len(evidence.restarts) != 1 || evidence.restarts[0].State != test.wantState {
				t.Fatalf("restart evidence = %+v, want %s", evidence.restarts, test.wantState)
			}
		})
	}
}

func restartStates(records []RestartEvidence) []RestartState {
	states := make([]RestartState, len(records))
	for i, record := range records {
		states[i] = record.State
	}
	return states
}

func requireRestartStates(t *testing.T, records []RestartEvidence, want []RestartState) {
	t.Helper()
	if got := restartStates(records); !reflect.DeepEqual(got, want) {
		t.Fatalf("restart evidence states = %v, want %v", got, want)
	}
}
