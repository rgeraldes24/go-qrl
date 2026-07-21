// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package freshsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	kurtosisapi "github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

const (
	testEnclaveUUID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	testELUUID      = "11111111111111111111111111111111"
	testCLUUID      = "22222222222222222222222222222222"
)

func TestAddTemporaryServiceRecordsFullUUIDBeforeReturning(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	var events []string
	runner := &serviceAddingRunner{
		fake: fake, enclave: enclave,
		events: &events,
		services: map[string]kurtosisapi.Service{
			"fresh-el": {Name: "fresh-el", UUID: testELUUID},
		},
	}
	recorder := &temporaryLifecycleRecorderStub{events: &events}
	client := serviceTrackingClient{Client: fake, events: &events}
	check := freshSyncCheck{
		cfg: Config{FreshELService: "fresh-el"},
		k:   cliKurtosis{enclave: enclave.UUID, runner: runner}, client: client, enclave: enclave,
		recorder: recorder,
	}
	if err := check.addTemporaryService(t.Context(), "fresh-el", rawServiceConfig{}); err != nil {
		t.Fatal(err)
	}
	want := TemporaryService{Name: "fresh-el", UUID: testELUUID}
	if !slices.Equal(recorder.services, []TemporaryService{want}) || check.freshEL != want || !slices.Equal(check.addedServices, []TemporaryService{want}) {
		t.Fatalf("recorded=%+v freshEL=%+v added=%+v, want %+v", recorder.services, check.freshEL, check.addedServices, want)
	}
	if !slices.Equal(events, []string{"intent", "services", "add", "services", "uuid"}) {
		t.Fatalf("creation event order = %v, want durable intent, absence proof, add, exact lookup, UUID bind", events)
	}
	if len(recorder.intents) != 1 || recorder.intents[0].Name != want.Name || recorder.intents[0].EnclaveUUID != enclave.UUID {
		t.Fatalf("creation intent = %+v", recorder.intents)
	}
}

func TestRecorderFailureRemovesOnlyCapturedUUID(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	runner := &serviceAddingRunner{
		fake: fake, enclave: enclave,
		services: map[string]kurtosisapi.Service{
			"fresh-el": {Name: "fresh-el", UUID: testELUUID},
		},
	}
	check := freshSyncCheck{
		cfg: Config{FreshELService: "fresh-el"},
		k:   cliKurtosis{enclave: enclave.UUID, runner: runner}, client: fake, enclave: enclave,
		recorder: &temporaryLifecycleRecorderStub{serviceErr: errors.New("disk full")},
	}
	err := check.addTemporaryService(t.Context(), "fresh-el", rawServiceConfig{})
	if err == nil || !strings.Contains(err.Error(), "persist temporary service") {
		t.Fatalf("error = %v, want recorder failure", err)
	}
	if _, lookupErr := fake.Service(t.Context(), enclave, "fresh-el"); lookupErr == nil {
		t.Fatal("service remained after its UUID could not be persisted")
	}
	if len(check.addedServices) != 0 {
		t.Fatalf("removed service still tracked for cleanup: %+v", check.addedServices)
	}
}

func TestCreationIntentFailurePreventsServiceAdd(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	runner := &serviceAddingRunner{
		fake: fake, enclave: enclave,
		services: map[string]kurtosisapi.Service{"fresh-el": {Name: "fresh-el", UUID: testELUUID}},
	}
	check := freshSyncCheck{
		cfg: Config{FreshELService: "fresh-el"},
		k:   cliKurtosis{enclave: enclave.UUID, runner: runner}, client: fake, enclave: enclave,
		recorder: &temporaryLifecycleRecorderStub{intentErr: errors.New("intent disk full")},
	}
	err := check.addTemporaryService(t.Context(), "fresh-el", rawServiceConfig{})
	if err == nil || !strings.Contains(err.Error(), "persist temporary service fresh-el creation intent") {
		t.Fatalf("creation intent error = %v", err)
	}
	if runner.addCalls != 0 {
		t.Fatalf("service add ran %d times after intent persistence failed", runner.addCalls)
	}
	if _, err := fake.Service(t.Context(), enclave, "fresh-el"); err == nil {
		t.Fatal("service exists despite failed creation-intent journal")
	}
}

func TestLostAddResponseStillCapturesCreatedServiceUUID(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	runner := &serviceAddingRunner{
		fake: fake, enclave: enclave, addErr: context.Canceled,
		services: map[string]kurtosisapi.Service{
			"fresh-el": {Name: "fresh-el", UUID: testELUUID},
		},
	}
	recorder := new(temporaryLifecycleRecorderStub)
	check := freshSyncCheck{
		cfg: Config{FreshELService: "fresh-el"},
		k:   cliKurtosis{enclave: enclave.UUID, runner: runner}, client: fake, enclave: enclave,
		recorder: recorder,
	}
	err := check.addTemporaryService(t.Context(), "fresh-el", rawServiceConfig{})
	if err != nil {
		t.Fatalf("exact committed add was not recovered from lost response: %v", err)
	}
	want := TemporaryService{Name: "fresh-el", UUID: testELUUID}
	if !slices.Equal(recorder.services, []TemporaryService{want}) || !slices.Equal(check.addedServices, []TemporaryService{want}) {
		t.Fatalf("recorded=%+v added=%+v, want %+v", recorder.services, check.addedServices, want)
	}
}

func TestHardInterruptedAddResumesByBindingExactIntendedService(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	now := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	store, recorder := newTemporaryServiceCheckpoint(t, enclave, now)
	runner := &serviceAddingRunner{
		fake: fake, enclave: enclave, panicAfterAdd: true,
		services: map[string]kurtosisapi.Service{
			"fresh-el": {Name: "fresh-el", UUID: testELUUID},
		},
	}
	check := freshSyncCheck{
		cfg: Config{FreshELService: "fresh-el"},
		k:   cliKurtosis{enclave: enclave.UUID, runner: runner}, client: fake, enclave: enclave,
		recorder: recorder, now: func() time.Time { return now },
	}
	panicked := func() (panicked bool) {
		defer func() { panicked = recover() != nil }()
		_ = check.addTemporaryService(t.Context(), "fresh-el", rawServiceConfig{})
		return false
	}()
	if !panicked {
		t.Fatal("hard interruption was not injected")
	}
	interrupted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	intent, ok := interrupted.TemporaryServiceCreationIntents["fresh-el"]
	if !ok || interrupted.TemporaryServices["fresh-el"] != "" {
		t.Fatalf("hard-interrupted checkpoint = intents %+v services %+v", interrupted.TemporaryServiceCreationIntents, interrupted.TemporaryServices)
	}
	recovery, err := RecoverTemporaryServiceCreations(
		t.Context(), fake, enclave, interrupted.TemporaryServices, interrupted.TemporaryServiceCreationIntents, "fresh-el",
	)
	if err != nil {
		t.Fatal(err)
	}
	want := TemporaryService{Name: "fresh-el", UUID: testELUUID}
	if !slices.Equal(recovery.Bound, []TemporaryService{want}) || !slices.Equal(recovery.Reusable, []TemporaryService{want}) {
		t.Fatalf("hard-interruption recovery = %+v", recovery)
	}
	if err := PersistTemporaryServiceCreationRecovery(t.Context(), recorder, recovery); err != nil {
		t.Fatal(err)
	}
	bound, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if bound.TemporaryServices["fresh-el"] != testELUUID || bound.TemporaryServiceCreationIntents["fresh-el"] != intent {
		t.Fatalf("bound checkpoint = intents %+v services %+v", bound.TemporaryServiceCreationIntents, bound.TemporaryServices)
	}
	runner.panicAfterAdd = false
	resumed := freshSyncCheck{
		cfg: Config{FreshELService: "fresh-el"},
		k:   cliKurtosis{enclave: enclave.UUID, runner: runner}, client: fake, enclave: enclave,
		recorder: recorder, now: func() time.Time { return now },
		creationIntents:   bound.TemporaryServiceCreationIntents,
		recoveredServices: map[string]TemporaryService{"fresh-el": want},
	}
	if err := resumed.addTemporaryService(t.Context(), "fresh-el", rawServiceConfig{}); err != nil {
		t.Fatalf("same-checkpoint resume: %v", err)
	}
	if runner.addCalls != 1 || resumed.freshEL != want {
		t.Fatalf("resume replayed add or lost identity: calls=%d freshEL=%+v", runner.addCalls, resumed.freshEL)
	}
}

func TestCreationRecoveryFailsClosedOnMarkerUUIDAndAmbiguity(t *testing.T) {
	for _, test := range []struct {
		name     string
		service  kurtosisapi.Service
		recorded string
		client   func(kurtosisapi.Client, lifecycle.EnclaveRef, kurtosisapi.Service) kurtosisapi.Client
		want     string
	}{
		{
			name: "marker mismatch", service: kurtosisapi.Service{Name: "fresh-el", UUID: testELUUID, Labels: map[string]string{TemporaryServiceCreationIntentLabel: strings.Repeat("9", 64)}},
			want: "creation marker changed",
		},
		{
			name: "UUID mismatch", service: kurtosisapi.Service{Name: "fresh-el", UUID: testELUUID}, recorded: testCLUUID,
			want: "UUID changed",
		},
		{
			name: "ambiguous exact name", service: kurtosisapi.Service{Name: "fresh-el", UUID: testELUUID},
			client: func(client kurtosisapi.Client, _ lifecycle.EnclaveRef, service kurtosisapi.Service) kurtosisapi.Client {
				return duplicateServiceClient{Client: client, duplicate: kurtosisapi.Service{Name: service.Name, UUID: testCLUUID, Labels: service.Labels}}
			},
			want: "ambiguous",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake, enclave := freshSyncFake(t)
			intent, err := newTemporaryServiceCreationIntent("fresh-el", enclave, rawServiceConfig{}, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			if test.service.Labels == nil {
				test.service.Labels = map[string]string{TemporaryServiceCreationIntentLabel: intent.Marker}
			}
			if err := fake.AddService(enclave, test.service); err != nil {
				t.Fatal(err)
			}
			client := kurtosisapi.Client(fake)
			if test.client != nil {
				client = test.client(client, enclave, test.service)
			}
			_, err = RecoverTemporaryServiceCreations(
				t.Context(), client, enclave, map[string]string{"fresh-el": test.recorded},
				map[string]lifecycle.TemporaryServiceCreationIntent{"fresh-el": intent}, "fresh-el",
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("recovery error = %v, want %q", err, test.want)
			}
			if _, err := fake.Service(t.Context(), enclave, "fresh-el"); err != nil {
				t.Fatalf("mismatched service was mutated: %v", err)
			}
		})
	}
}

func TestRecoveredServiceRejectsConfigDriftWithoutReadding(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	original := rawServiceConfig{"image": json.RawMessage(`"go-qrl:original"`)}
	intent, err := newTemporaryServiceCreationIntent("fresh-el", enclave, original, time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	service := kurtosisapi.Service{Name: "fresh-el", UUID: testELUUID, Labels: map[string]string{TemporaryServiceCreationIntentLabel: intent.Marker}}
	if err := fake.AddService(enclave, service); err != nil {
		t.Fatal(err)
	}
	runner := &serviceAddingRunner{fake: fake, enclave: enclave}
	check := freshSyncCheck{
		cfg: Config{FreshELService: "fresh-el"}, k: cliKurtosis{enclave: enclave.UUID, runner: runner},
		client: fake, enclave: enclave,
		creationIntents:   map[string]lifecycle.TemporaryServiceCreationIntent{"fresh-el": intent},
		recoveredServices: map[string]TemporaryService{"fresh-el": {Name: "fresh-el", UUID: testELUUID}},
	}
	drifted := rawServiceConfig{"image": json.RawMessage(`"go-qrl:drifted"`)}
	if err := check.addTemporaryService(t.Context(), "fresh-el", drifted); err == nil || !strings.Contains(err.Error(), "resumed config changed") {
		t.Fatalf("config drift error = %v", err)
	}
	if runner.addCalls != 0 {
		t.Fatalf("config drift replayed service add %d times", runner.addCalls)
	}
}

func TestReconcileTemporaryServicesRequiresRecordedUUID(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	if err := fake.AddService(enclave, kurtosisapi.Service{Name: "fresh-el", UUID: testELUUID}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileTemporaryServices(t.Context(), fake, enclave, map[string]string{}, "fresh-el"); err == nil || !strings.Contains(err.Error(), "without a durable UUID record") {
		t.Fatalf("unrecorded service error = %v", err)
	}
	if _, err := ReconcileTemporaryServices(t.Context(), fake, enclave, map[string]string{"fresh-el": testCLUUID}, "fresh-el"); err == nil || !strings.Contains(err.Error(), "UUID changed") {
		t.Fatalf("mismatched service error = %v", err)
	}
	if _, err := fake.Service(t.Context(), enclave, "fresh-el"); err != nil {
		t.Fatalf("mismatched service was not preserved: %v", err)
	}
	result, err := ReconcileTemporaryServices(t.Context(), fake, enclave, map[string]string{"fresh-el": testELUUID}, "fresh-el")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(result.Removed, []TemporaryService{{Name: "fresh-el", UUID: testELUUID}}) {
		t.Fatalf("removed = %+v", result.Removed)
	}
}

func TestReconcileValidatesAllUUIDsBeforeRemovingAnyService(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	for _, service := range []kurtosisapi.Service{{Name: "fresh-el", UUID: testELUUID}, {Name: "fresh-cl", UUID: testCLUUID}} {
		if err := fake.AddService(enclave, service); err != nil {
			t.Fatal(err)
		}
	}
	_, err := ReconcileTemporaryServices(t.Context(), fake, enclave, map[string]string{
		"fresh-el": testELUUID,
		"fresh-cl": "33333333333333333333333333333333",
	}, "fresh-el", "fresh-cl")
	if err == nil || !strings.Contains(err.Error(), "UUID changed") {
		t.Fatalf("reconcile error = %v", err)
	}
	for _, name := range []string{"fresh-el", "fresh-cl"} {
		if _, err := fake.Service(t.Context(), enclave, name); err != nil {
			t.Fatalf("%s was removed before complete UUID validation: %v", name, err)
		}
	}
}

func TestCleanupTemporaryServicesUsesReverseUUIDOrder(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	for _, service := range []kurtosisapi.Service{{Name: "fresh-el", UUID: testELUUID}, {Name: "fresh-cl", UUID: testCLUUID}} {
		if err := fake.AddService(enclave, service); err != nil {
			t.Fatal(err)
		}
	}
	services := []TemporaryService{{Name: "fresh-el", UUID: testELUUID}, {Name: "fresh-cl", UUID: testCLUUID}}
	if err := CleanupTemporaryServices(t.Context(), fake, enclave, services); err != nil {
		t.Fatal(err)
	}
	var removes []string
	for _, call := range fake.Calls {
		if strings.HasPrefix(call, "remove:") {
			removes = append(removes, call)
		}
	}
	if !slices.Equal(removes, []string{"remove:fresh-cl", "remove:fresh-el"}) {
		t.Fatalf("remove calls = %v", removes)
	}
}

func TestExplicitFailureCleanupOwnsWholeRecoveredPairBeforeCLResume(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	cfg := Config{FreshELService: "fresh-el", FreshCLService: "fresh-cl"}
	now := time.Date(2026, 7, 21, 15, 0, 0, 0, time.UTC)
	intents := make(map[string]lifecycle.TemporaryServiceCreationIntent, 2)
	recorded := map[string]string{"fresh-el": testELUUID, "fresh-cl": testCLUUID}
	for index, service := range []kurtosisapi.Service{
		{Name: "fresh-el", UUID: testELUUID},
		{Name: "fresh-cl", UUID: testCLUUID},
	} {
		serviceConfig := rawServiceConfig{"image": json.RawMessage(fmt.Sprintf(`"service-%d"`, index))}
		intent, err := newTemporaryServiceCreationIntent(service.Name, enclave, serviceConfig, now)
		if err != nil {
			t.Fatal(err)
		}
		service.Labels = map[string]string{TemporaryServiceCreationIntentLabel: intent.Marker}
		if err := fake.AddService(enclave, service); err != nil {
			t.Fatal(err)
		}
		intents[service.Name] = intent
	}

	recovery, err := RecoverTemporaryServiceCreations(
		t.Context(), fake, enclave, recorded, intents, cfg.FreshELService, cfg.FreshCLService,
	)
	if err != nil {
		t.Fatal(err)
	}
	recovered := make(map[string]TemporaryService, len(recovery.Reusable))
	for _, identity := range recovery.Reusable {
		recovered[identity.Name] = identity
	}
	owned, err := orderedRecoveredTemporaryServices(cfg, recovered)
	if err != nil {
		t.Fatal(err)
	}
	wantOwned := []TemporaryService{{Name: "fresh-el", UUID: testELUUID}, {Name: "fresh-cl", UUID: testCLUUID}}
	if !slices.Equal(owned, wantOwned) {
		t.Fatalf("recovered cleanup ownership = %+v, want EL then CL %+v", owned, wantOwned)
	}

	// Model an explicit-cleanup failure after the recovered EL was adopted but
	// before the run reached addTemporaryService for the recovered CL. Cleanup
	// must still remove the complete pair, in reverse dependency order.
	check := freshSyncCheck{client: fake, enclave: enclave, addedServices: owned}
	if err := check.cleanup(t.Context()); err != nil {
		t.Fatal(err)
	}
	var removes []string
	for _, call := range fake.Calls {
		if strings.HasPrefix(call, "remove:") {
			removes = append(removes, call)
		}
	}
	if !slices.Equal(removes, []string{"remove:fresh-cl", "remove:fresh-el"}) {
		t.Fatalf("recovered-pair cleanup calls = %v", removes)
	}

	next, err := RecoverTemporaryServiceCreations(
		t.Context(), fake, enclave, recorded, intents, cfg.FreshELService, cfg.FreshCLService,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Reusable) != 0 || len(next.Bound) != 0 || len(next.Absent) != 2 || len(next.AbandonedIntents) != 2 {
		t.Fatalf("next same-checkpoint recovery remains bound to an orphaned CL: %+v", next)
	}
}

func TestRefreshPublicEndpointReadsCurrentServiceContext(t *testing.T) {
	fake, enclave := freshSyncFake(t)
	service := kurtosisapi.Service{
		Name: "fresh-el", UUID: testELUUID, PublicIP: "127.0.0.1",
		PublicPorts: map[string]kurtosisapi.Port{"rpc": {ID: "rpc", Number: 8545}},
	}
	if err := fake.AddService(enclave, service); err != nil {
		t.Fatal(err)
	}
	identity := TemporaryService{Name: service.Name, UUID: service.UUID}
	before, err := refreshPublicEndpoint(t.Context(), fake, enclave, identity, "rpc", "http")
	if err != nil {
		t.Fatal(err)
	}
	if err := fake.StopService(t.Context(), enclave, identity.UUID); err != nil {
		t.Fatal(err)
	}
	if err := fake.StartService(t.Context(), enclave, identity.UUID); err != nil {
		t.Fatal(err)
	}
	after, err := refreshPublicEndpoint(t.Context(), fake, enclave, identity, "rpc", "http")
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatalf("endpoint was not refreshed after restart: %q", after)
	}
}

func TestCheckpointRecorderPersistsTemporaryUUID(t *testing.T) {
	now := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	enclave := lifecycle.EnclaveRef{Name: "vm64", UUID: testEnclaveUUID, Owned: true}
	state := lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), t.TempDir(), strings.Repeat("c", 64), enclave, now)
	store := lifecycle.Store{Path: t.TempDir() + "/checkpoint.json", StageOrder: []string{"fresh-snap"}, Now: func() time.Time { return now }}
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	recorder := CheckpointRecorder{Store: store, Now: func() time.Time { return now.Add(time.Second) }}
	service := TemporaryService{Name: "fresh-el", UUID: testELUUID}
	if err := recorder.RecordTemporaryService(t.Context(), service); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TemporaryServices[service.Name] != service.UUID {
		t.Fatalf("temporary services = %v", loaded.TemporaryServices)
	}
	if err := recorder.ReconcileTemporaryServices(t.Context(), []TemporaryService{service}); err != nil {
		t.Fatal(err)
	}
	replacement := TemporaryService{Name: service.Name, UUID: testCLUUID}
	if err := recorder.RecordTemporaryService(t.Context(), replacement); err != nil {
		t.Fatalf("record replacement UUID after safe reconciliation: %v", err)
	}
	loaded, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.TemporaryServices[replacement.Name] != replacement.UUID {
		t.Fatalf("replacement temporary services = %v", loaded.TemporaryServices)
	}
	transactionHash := "0x" + strings.Repeat("12", 32)
	if err := recorder.RecordTransaction(t.Context(), "fresh-snap-transfer", transactionHash); err != nil {
		t.Fatal(err)
	}
	loaded, err = store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Transactions["fresh-snap-transfer"] != transactionHash {
		t.Fatalf("recorded transactions = %v", loaded.Transactions)
	}
}

func TestOpenCheckpointRejectsReorderedLegacyStageHistory(t *testing.T) {
	now := time.Date(2026, 7, 21, 14, 0, 0, 0, time.UTC)
	path := filepath.Join(t.TempDir(), "checkpoint.json")
	enclave := lifecycle.EnclaveRef{Name: "vm64", UUID: testEnclaveUUID, Owned: true}
	state := lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), t.TempDir(), strings.Repeat("c", 64), enclave, now)
	finished := now.Add(time.Second)
	exitCode := 0
	state.Completed = slices.Clone(legacyLifecycleStageOrder[:9])
	for _, stage := range state.Completed {
		state.Attempts = append(state.Attempts, lifecycle.Attempt{
			Stage: stage, Attempt: 1, StartedAt: now, FinishedAt: &finished, ExitCode: &exitCode,
		})
	}
	active := "fresh-snap"
	state.CurrentStage = &active
	state.Attempts = append(state.Attempts, lifecycle.Attempt{Stage: active, Attempt: 1, StartedAt: now})
	state.UpdatedAt = finished
	store := lifecycle.Store{Path: path, StageOrder: slices.Clone(legacyLifecycleStageOrder)}
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenCheckpoint(path); err != nil {
		t.Fatalf("open correctly ordered legacy checkpoint: %v", err)
	}
	state.Completed[1], state.Completed[2] = state.Completed[2], state.Completed[1]
	state.Attempts[1].Stage, state.Attempts[2].Stage = state.Attempts[2].Stage, state.Attempts[1].Stage
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := OpenCheckpoint(path); err == nil || !strings.Contains(err.Error(), "completed checkpoint stages are not an exact ordered prefix") {
		t.Fatalf("reordered legacy checkpoint error = %v", err)
	}
}

func TestActiveFreshSyncCheckpointRequiresMatchingUnfinishedAttempt(t *testing.T) {
	active := "fresh-snap"
	state := lifecycle.Checkpoint{
		Status: lifecycle.StatusRunning, CurrentStage: &active,
		Attempts: []lifecycle.Attempt{{Stage: active, Attempt: 1, StartedAt: time.Now().UTC()}},
	}
	if stage, err := activeFreshSyncCheckpointStage(state); err != nil || stage != active {
		t.Fatalf("valid active stage = %q/%v", stage, err)
	}
	for _, test := range []struct {
		name   string
		mutate func(*lifecycle.Checkpoint)
		want   string
	}{
		{name: "not running", mutate: func(state *lifecycle.Checkpoint) { state.Status = lifecycle.StatusFailed }, want: "must be running"},
		{name: "wrong stage", mutate: func(state *lifecycle.Checkpoint) { wrong := "system-base"; state.CurrentStage = &wrong }, want: "want fresh-snap or fresh-full"},
		{name: "finished", mutate: func(state *lifecycle.Checkpoint) {
			finished := time.Now().UTC()
			code := 0
			state.Attempts[0].FinishedAt = &finished
			state.Attempts[0].ExitCode = &code
		}, want: "active unfinished"},
	} {
		t.Run(test.name, func(t *testing.T) {
			copy := state
			copy.Attempts = slices.Clone(state.Attempts)
			test.mutate(&copy)
			if _, err := activeFreshSyncCheckpointStage(copy); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("active-stage error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRecordedTransferIsReusedWithoutSubmission(t *testing.T) {
	want := "0x" + strings.Repeat("ab", 32)
	check := freshSyncCheck{cfg: Config{SyncMode: "snap"}, recordedTransaction: want}
	hash, err := check.transferForVerification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if hash.Hex() != want {
		t.Fatalf("hash = %s, want %s", hash, want)
	}
	check.recordedTransaction = "0x1234"
	if _, err := check.transferForVerification(t.Context()); err == nil || !strings.Contains(err.Error(), "has 2 bytes") {
		t.Fatalf("short recorded hash error = %v", err)
	}
}

func freshSyncFake(t *testing.T) (*kurtosisapi.FakeClient, lifecycle.EnclaveRef) {
	t.Helper()
	fake := kurtosisapi.NewFakeClient()
	enclave, err := fake.CreateEnclave(t.Context(), "vm64")
	if err != nil {
		t.Fatal(err)
	}
	if enclave.UUID != "00000000000000000000000000000001" {
		t.Fatalf("unexpected fake enclave UUID %s", enclave.UUID)
	}
	return fake, enclave
}

func newTemporaryServiceCheckpoint(t *testing.T, enclave lifecycle.EnclaveRef, now time.Time) (lifecycle.Store, CheckpointRecorder) {
	t.Helper()
	directory := t.TempDir()
	store := lifecycle.Store{Path: directory + "/checkpoint.json", StageOrder: []string{"fresh-snap"}, Now: func() time.Time { return now }}
	state := lifecycle.NewCheckpoint("run", strings.Repeat("a", 40), strings.Repeat("b", 64), directory+"/dump", strings.Repeat("c", 64), enclave, now)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	return store, CheckpointRecorder{Store: store, Now: func() time.Time { return now }}
}

type temporaryLifecycleRecorderStub struct {
	events     *[]string
	intents    []lifecycle.TemporaryServiceCreationIntent
	services   []TemporaryService
	intentErr  error
	serviceErr error
}

func (recorder *temporaryLifecycleRecorderStub) RecordTemporaryServiceCreationIntent(_ context.Context, intent lifecycle.TemporaryServiceCreationIntent) error {
	if recorder.events != nil {
		*recorder.events = append(*recorder.events, "intent")
	}
	if recorder.intentErr != nil {
		return recorder.intentErr
	}
	recorder.intents = append(recorder.intents, intent)
	return nil
}

func (recorder *temporaryLifecycleRecorderStub) RecordTemporaryService(_ context.Context, service TemporaryService) error {
	if recorder.events != nil {
		*recorder.events = append(*recorder.events, "uuid")
	}
	if recorder.serviceErr != nil {
		return recorder.serviceErr
	}
	recorder.services = append(recorder.services, service)
	return nil
}

type serviceTrackingClient struct {
	kurtosisapi.Client
	events *[]string
}

func (client serviceTrackingClient) Services(ctx context.Context, enclave lifecycle.EnclaveRef) ([]kurtosisapi.Service, error) {
	*client.events = append(*client.events, "services")
	return client.Client.Services(ctx, enclave)
}

type duplicateServiceClient struct {
	kurtosisapi.Client
	duplicate kurtosisapi.Service
}

func (client duplicateServiceClient) Services(ctx context.Context, enclave lifecycle.EnclaveRef) ([]kurtosisapi.Service, error) {
	services, err := client.Client.Services(ctx, enclave)
	if err != nil {
		return nil, err
	}
	return append(services, client.duplicate), nil
}

type serviceAddingRunner struct {
	fake          *kurtosisapi.FakeClient
	enclave       lifecycle.EnclaveRef
	services      map[string]kurtosisapi.Service
	addErr        error
	panicAfterAdd bool
	events        *[]string
	addCalls      int
}

func (runner *serviceAddingRunner) run(_ context.Context, input []byte, args ...string) (string, error) {
	if len(args) >= 4 && slices.Equal(args[:2], []string{"service", "add"}) {
		runner.addCalls++
		if runner.events != nil {
			*runner.events = append(*runner.events, "add")
		}
		service, ok := runner.services[args[3]]
		if !ok {
			return "", errors.New("unexpected service")
		}
		var cfg rawServiceConfig
		if err := json.Unmarshal(input, &cfg); err != nil {
			return "", err
		}
		if raw := cfg["labels"]; raw != nil {
			if err := json.Unmarshal(raw, &service.Labels); err != nil {
				return "", err
			}
		}
		if err := runner.fake.AddService(runner.enclave, service); err != nil {
			return "", err
		}
		if runner.panicAfterAdd {
			panic("hard interruption after Kurtosis committed service add")
		}
		return "created", runner.addErr
	}
	return "", errors.New("unexpected command")
}
