// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	kurtosisapi "github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

var compatibilityUUIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Keep this exact order synchronized with lifecycle_state.py. The legacy
// driver owns these stage names; deriving an order from untrusted checkpoint
// contents would make an out-of-order file self-validating.
var compatibilityLegacyStageOrder = []string{
	"fixture", "host-preflight", "network-start", "el1", "el2", "deposit",
	"system-base", "system-signer", "system-participant", "fresh-snap", "fresh-full", "cleanup",
}

// CompatibilityRuntime adapts the retained systemcheck command to the same
// additive schema-v1 checkpoint used by the Python lifecycle and the Go
// runner. All external mutations remain scoped to the checkpoint's full
// enclave and service UUIDs.
type CompatibilityRuntime struct {
	Enclave lifecycle.EnclaveRef
	State   *lifecycle.Checkpoint
	Store   lifecycle.Store
	Options Options
}

// OpenCompatibilityCheckpoint validates a schema-v1 lifecycle checkpoint,
// resolves the exact live enclave and service UUIDs, and wires every durable
// system-suite evidence interface to that checkpoint.
func OpenCompatibilityCheckpoint(ctx context.Context, path string, cfg Config, client kurtosisapi.Client) (*CompatibilityRuntime, error) {
	if ctx == nil {
		return nil, errors.New("system compatibility checkpoint context is nil")
	}
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("system compatibility checkpoint path is empty")
	}
	if client == nil {
		return nil, errors.New("system compatibility checkpoint Kurtosis client is nil")
	}
	state, store, err := loadCompatibilityCheckpoint(path)
	if err != nil {
		return nil, err
	}
	if cfg.Enclave != state.Enclave.Name && cfg.Enclave != state.Enclave.UUID {
		return nil, fmt.Errorf("configured enclave %q does not match checkpoint identity %s/%s", cfg.Enclave, state.Enclave.Name, state.Enclave.UUID)
	}
	stageName, err := compatibilityStageName(cfg.Phase)
	if err != nil {
		return nil, err
	}
	if err := validateCompatibilityStage(state, stageName); err != nil {
		return nil, err
	}
	live, err := client.GetEnclave(ctx, state.Enclave.UUID)
	if err != nil {
		return nil, fmt.Errorf("resolve checkpoint enclave by full UUID %s: %w", state.Enclave.UUID, err)
	}
	if live.Name != state.Enclave.Name || live.UUID != state.Enclave.UUID {
		return nil, fmt.Errorf("checkpoint enclave identity changed: got %s/%s, want %s/%s", live.Name, live.UUID, state.Enclave.Name, state.Enclave.UUID)
	}
	// Ownership is a cleanup concern. The compatibility system command mutates
	// only services and must never gain enclave-destruction authority.
	live.Owned = false

	identities, serviceUUIDs, err := resolveCompatibilityServices(ctx, client, live, compatibilityServiceNames(cfg))
	if err != nil {
		return nil, err
	}
	recorder := &compatibilityRecorder{
		store: store, state: &state, phase: cfg.Phase, stageName: stageName, serviceUUIDs: serviceUUIDs,
	}
	restarts, err := compatibilityRestartHistory(state.ServiceTransitions, cfg.Phase, serviceUUIDs)
	if err != nil {
		return nil, err
	}
	runtime := &CompatibilityRuntime{Enclave: live, State: recorder.state, Store: store}
	runtime.Options = Options{
		Controller:                        &compatibilityController{client: client, enclave: live, serviceUUIDs: identities},
		Evidence:                          recorder,
		TransactionRecorder:               recorder,
		ManagedJournal:                    recorder,
		ObservationRecorder:               recorder,
		RecordedTransactions:              compatibilityStringValues(state.Transactions, stageName),
		RestartHistory:                    restarts,
		ManagedTransactionIntents:         compatibilityManagedIntents(state.ManagedTransactionIntents, stageName),
		ManagedTransactionInitialAttempts: compatibilityTimeValues(state.ManagedTransactionInitialAttempts, stageName),
		ManagedTransactionResubmits:       compatibilityTimeValues(state.ManagedTransactionResubmits, stageName),
		ServiceUUIDs:                      serviceUUIDs,
		RecordedObservations:              compatibilityStringValues(state.SystemObservations, stageName),
	}
	return runtime, nil
}

func validateCompatibilityStage(state lifecycle.Checkpoint, stageName string) error {
	if state.Status != lifecycle.StatusRunning {
		return fmt.Errorf("checkpoint status %q does not own a running system phase", state.Status)
	}
	if state.CurrentStage == nil || *state.CurrentStage != stageName {
		current := ""
		if state.CurrentStage != nil {
			current = *state.CurrentStage
		}
		return fmt.Errorf("checkpoint active stage %q does not match requested system stage %q", current, stageName)
	}
	if len(state.Attempts) == 0 {
		return fmt.Errorf("checkpoint system stage %q has no active attempt", stageName)
	}
	active := state.Attempts[len(state.Attempts)-1]
	if active.Stage != stageName || active.FinishedAt != nil || active.ExitCode != nil {
		return fmt.Errorf("checkpoint system stage %q is not the final unfinished attempt", stageName)
	}
	return nil
}

func loadCompatibilityCheckpoint(path string) (lifecycle.Checkpoint, lifecycle.Store, error) {
	store := lifecycle.Store{Path: path, StageOrder: slices.Clone(compatibilityLegacyStageOrder)}
	state, err := store.Load()
	if err != nil {
		return lifecycle.Checkpoint{}, lifecycle.Store{}, err
	}
	return state, store, nil
}

func compatibilityServiceNames(cfg Config) []string {
	return []string{
		cfg.ELServices[0], cfg.ELServices[1],
		cfg.CLServices[0], cfg.CLServices[1],
		cfg.VCServices[0], cfg.VCServices[1],
		cfg.SignerService,
	}
}

func resolveCompatibilityServices(ctx context.Context, client kurtosisapi.Client, enclave lifecycle.EnclaveRef, required []string) (map[string]string, map[string]string, error) {
	services, err := client.Services(ctx, enclave)
	if err != nil {
		return nil, nil, fmt.Errorf("list checkpoint enclave services: %w", err)
	}
	byName := make(map[string]kurtosisapi.Service, len(services))
	for _, service := range services {
		if service.Name == "" || !compatibilityUUIDPattern.MatchString(service.UUID) {
			return nil, nil, fmt.Errorf("Kurtosis returned invalid service identity %q/%q", service.Name, service.UUID)
		}
		if prior, exists := byName[service.Name]; exists && prior.UUID != service.UUID {
			return nil, nil, fmt.Errorf("Kurtosis returned ambiguous service name %q", service.Name)
		}
		byName[service.Name] = service
	}
	identities := make(map[string]string, len(required))
	serviceUUIDs := make(map[string]string, len(required))
	seenNames := make(map[string]bool, len(required))
	seenUUIDs := make(map[string]string, len(required))
	for _, name := range required {
		if name == "" {
			return nil, nil, errors.New("system configuration contains an empty required service name")
		}
		if seenNames[name] {
			return nil, nil, fmt.Errorf("system configuration repeats required service name %q", name)
		}
		seenNames[name] = true
		service, ok := byName[name]
		if !ok {
			return nil, nil, fmt.Errorf("checkpoint enclave has no required service %q", name)
		}
		if prior, exists := seenUUIDs[service.UUID]; exists && prior != name {
			return nil, nil, fmt.Errorf("required services %q and %q share UUID %s", prior, name, service.UUID)
		}
		seenUUIDs[service.UUID] = name
		identities[name] = service.UUID
		serviceUUIDs[name] = service.UUID
	}
	return identities, serviceUUIDs, nil
}

func compatibilityStageName(phase Phase) (string, error) {
	switch phase {
	case PhaseBase:
		return "system-base", nil
	case PhaseSignerRestart:
		return "system-signer", nil
	case PhaseParticipantRestart:
		return "system-participant", nil
	case PhaseAll:
		return "", errors.New("system compatibility phase all has no single checkpoint stage; run base, signer-restart, and participant-restart at their respective lifecycle stages")
	default:
		return "", fmt.Errorf("unsupported system compatibility phase %q", phase)
	}
}

func compatibilityPrefix(stageName string) string { return stageName + "/" }

func compatibilityStringValues(values map[string]string, stageName string) map[string]string {
	prefix := compatibilityPrefix(stageName)
	result := make(map[string]string)
	for label, value := range values {
		if strings.HasPrefix(label, prefix) {
			result[strings.TrimPrefix(label, prefix)] = value
		}
	}
	return result
}

func compatibilityTimeValues(values map[string]time.Time, stageName string) map[string]time.Time {
	prefix := compatibilityPrefix(stageName)
	result := make(map[string]time.Time)
	for label, value := range values {
		if strings.HasPrefix(label, prefix) {
			result[strings.TrimPrefix(label, prefix)] = value
		}
	}
	return result
}

func compatibilityManagedIntents(values map[string]lifecycle.ManagedTransactionIntent, stageName string) map[string]ManagedTransactionIntent {
	prefix := compatibilityPrefix(stageName)
	result := make(map[string]ManagedTransactionIntent)
	for label, intent := range values {
		if !strings.HasPrefix(label, prefix) {
			continue
		}
		short := strings.TrimPrefix(label, prefix)
		intent.Label = strings.TrimPrefix(intent.Label, prefix)
		result[short] = intent
	}
	return result
}

func compatibilityRestartHistory(values []lifecycle.ServiceTransition, phase Phase, serviceUUIDs map[string]string) ([]RestartEvidence, error) {
	result := make([]RestartEvidence, 0)
	for _, transition := range values {
		if transition.Phase != string(phase) {
			continue
		}
		if serviceUUIDs[transition.ServiceName] != transition.ServiceUUID || transition.ServiceUUID == "" {
			return nil, fmt.Errorf("checkpoint transition service identity %s/%s does not match the live topology", transition.ServiceName, transition.ServiceUUID)
		}
		result = append(result, RestartEvidence{
			Phase: phase, Service: transition.ServiceName, State: RestartState(transition.State), At: transition.At,
		})
	}
	return result, nil
}

type compatibilityController struct {
	client       kurtosisapi.Client
	enclave      lifecycle.EnclaveRef
	serviceUUIDs map[string]string
}

func (controller *compatibilityController) current(ctx context.Context, name string) (kurtosisapi.Service, error) {
	uuid := controller.serviceUUIDs[name]
	if uuid == "" {
		return kurtosisapi.Service{}, fmt.Errorf("service %q is not part of the checkpoint topology", name)
	}
	service, err := controller.client.Service(ctx, controller.enclave, uuid)
	if err != nil {
		return kurtosisapi.Service{}, err
	}
	if service.Name != name || service.UUID != uuid {
		return kurtosisapi.Service{}, fmt.Errorf("service identity changed: got %s/%s, want %s/%s", service.Name, service.UUID, name, uuid)
	}
	return service, nil
}

func (controller *compatibilityController) Endpoint(ctx context.Context, serviceName, portID, scheme string) (string, error) {
	service, err := controller.current(ctx, serviceName)
	if err != nil {
		return "", err
	}
	endpoint, ok := service.PublicEndpoint(portID, scheme)
	if !ok {
		return "", fmt.Errorf("service %s/%s has no public port %q", service.Name, service.UUID, portID)
	}
	return endpoint, nil
}

func (controller *compatibilityController) Status(ctx context.Context, serviceName string) (ServiceStatus, error) {
	service, err := controller.current(ctx, serviceName)
	if err != nil {
		return "", err
	}
	switch service.Status {
	case kurtosisapi.ServiceStatusRunning:
		return ServiceRunning, nil
	case kurtosisapi.ServiceStatusStopped:
		return ServiceStopped, nil
	default:
		return "", fmt.Errorf("service %s/%s has unsafe Kurtosis status %q", service.Name, service.UUID, service.Status)
	}
}

func (controller *compatibilityController) Stop(ctx context.Context, serviceName string) error {
	service, err := controller.current(ctx, serviceName)
	if err != nil {
		return err
	}
	return controller.client.StopService(ctx, controller.enclave, service.UUID)
}

func (controller *compatibilityController) Start(ctx context.Context, serviceName string) error {
	service, err := controller.current(ctx, serviceName)
	if err != nil {
		return err
	}
	return controller.client.StartService(ctx, controller.enclave, service.UUID)
}

type compatibilityRecorder struct {
	store        lifecycle.Store
	state        *lifecycle.Checkpoint
	phase        Phase
	stageName    string
	serviceUUIDs map[string]string
}

func (recorder *compatibilityRecorder) validate(ctx context.Context, phase Phase) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if recorder == nil || recorder.state == nil || recorder.stageName == "" {
		return errors.New("system compatibility recorder is not initialized")
	}
	if phase != recorder.phase {
		return fmt.Errorf("system evidence phase %q does not match checkpoint phase %q", phase, recorder.phase)
	}
	return nil
}

func (recorder *compatibilityRecorder) label(label string) string {
	return compatibilityPrefix(recorder.stageName) + label
}

func (recorder *compatibilityRecorder) RecordRestart(ctx context.Context, evidence RestartEvidence) error {
	if err := recorder.validate(ctx, evidence.Phase); err != nil {
		return err
	}
	uuid := recorder.serviceUUIDs[evidence.Service]
	if uuid == "" || evidence.At.IsZero() {
		return errors.New("system restart evidence has no immutable service identity or timestamp")
	}
	return recorder.state.RecordServiceTransition(recorder.store, lifecycle.ServiceTransition{
		Phase: string(evidence.Phase), ServiceName: evidence.Service, ServiceUUID: uuid,
		State: string(evidence.State), At: evidence.At.UTC(),
	})
}

func (recorder *compatibilityRecorder) RecordEndpoint(ctx context.Context, evidence EndpointEvidence) error {
	if err := recorder.validate(ctx, evidence.Phase); err != nil {
		return err
	}
	uuid := recorder.serviceUUIDs[evidence.Service]
	if uuid == "" || evidence.At.IsZero() {
		return errors.New("system endpoint evidence has no immutable service identity or timestamp")
	}
	return recorder.state.RecordEndpointRefresh(recorder.store, lifecycle.EndpointRefresh{
		Phase: string(evidence.Phase), ServiceName: evidence.Service, ServiceUUID: uuid,
		Kind: evidence.Kind, Previous: evidence.Previous, Current: evidence.Current, At: evidence.At.UTC(),
	})
}

func (recorder *compatibilityRecorder) RecordTransaction(ctx context.Context, evidence TransactionEvidence) error {
	if err := recorder.validate(ctx, evidence.Phase); err != nil {
		return err
	}
	if evidence.Label == "" || evidence.Hash == ([32]byte{}) || evidence.At.IsZero() {
		return errors.New("system transaction evidence is incomplete")
	}
	return recorder.state.RecordTransaction(recorder.store, recorder.label(evidence.Label), evidence.Hash.Hex(), evidence.At.UTC())
}

func (recorder *compatibilityRecorder) RecordManagedTransactionIntent(ctx context.Context, intent ManagedTransactionIntent) error {
	if err := recorder.validate(ctx, Phase(intent.Phase)); err != nil {
		return err
	}
	if intent.Label == "" || intent.PreparedAt.IsZero() {
		return errors.New("system managed transaction intent is incomplete")
	}
	intent.Label = recorder.label(intent.Label)
	return recorder.state.RecordManagedTransactionIntent(recorder.store, intent.Label, intent, intent.PreparedAt.UTC())
}

func (recorder *compatibilityRecorder) RecordManagedTransactionInitialAttempt(ctx context.Context, label string, at time.Time) error {
	if err := recorder.validate(ctx, recorder.phase); err != nil {
		return err
	}
	return recorder.state.RecordManagedTransactionInitialAttempt(recorder.store, recorder.label(label), at.UTC())
}

func (recorder *compatibilityRecorder) RecordManagedTransactionResubmit(ctx context.Context, label string, at time.Time) error {
	if err := recorder.validate(ctx, recorder.phase); err != nil {
		return err
	}
	return recorder.state.RecordManagedTransactionResubmit(recorder.store, recorder.label(label), at.UTC())
}

func (recorder *compatibilityRecorder) RecordSystemObservation(ctx context.Context, label, value string, at time.Time) error {
	if err := recorder.validate(ctx, recorder.phase); err != nil {
		return err
	}
	return recorder.state.RecordSystemObservation(recorder.store, recorder.label(label), value, at.UTC())
}

var _ ServiceController = (*compatibilityController)(nil)
var _ EvidenceRecorder = (*compatibilityRecorder)(nil)
var _ TransactionRecorder = (*compatibilityRecorder)(nil)
var _ ManagedTransactionJournal = (*compatibilityRecorder)(nil)
var _ SystemObservationRecorder = (*compatibilityRecorder)(nil)
