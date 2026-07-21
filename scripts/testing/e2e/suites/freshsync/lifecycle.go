// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package freshsync

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"time"

	kurtosisapi "github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

var (
	serviceUUIDPattern   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	serviceDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

const TemporaryServiceCreationIntentLabel = "vm64e2e.freshsync.creation-intent"

var legacyLifecycleStageOrder = []string{
	"fixture",
	"host-preflight",
	"network-start",
	"el1",
	"el2",
	"deposit",
	"system-base",
	"system-signer",
	"system-participant",
	"fresh-snap",
	"fresh-full",
	"cleanup",
}

// TemporaryService is the immutable identity of a service created by this
// suite. Name is retained for diagnostics; UUID is the authority for cleanup.
type TemporaryService struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

func (service TemporaryService) Validate() error {
	if service.Name == "" {
		return errors.New("temporary service name is empty")
	}
	if !serviceUUIDPattern.MatchString(service.UUID) {
		return fmt.Errorf("temporary service UUID %q is not a full 32-character lowercase UUID", service.UUID)
	}
	return nil
}

// TemporaryServiceRecorder is invoked synchronously after Kurtosis has
// created a service and the SDK has resolved its full immutable UUID. Run does
// not begin a sync wait until the callback succeeds.
type TemporaryServiceRecorder interface {
	RecordTemporaryService(context.Context, TemporaryService) error
}

// TemporaryServiceCreationRecorder writes the creation intent before the
// Kurtosis service-add call. Implementations must make the write durable before
// returning so a process killed after add can recover the exact labeled
// service.
type TemporaryServiceCreationRecorder interface {
	RecordTemporaryServiceCreationIntent(context.Context, lifecycle.TemporaryServiceCreationIntent) error
}

// TemporaryServiceReconciler rotates durable current-service identities only
// after Run has proved the prior UUID absent or removed. Recorders that may be
// used across resumed attempts should implement this optional interface.
type TemporaryServiceReconciler interface {
	ReconcileTemporaryServices(context.Context, []TemporaryService) error
}

type TemporaryServiceCreationReconciler interface {
	ReconcileTemporaryServiceCreationIntents(context.Context, []lifecycle.TemporaryServiceCreationIntent) error
}

type TemporaryServiceRecorderFunc func(context.Context, TemporaryService) error

func (record TemporaryServiceRecorderFunc) RecordTemporaryService(ctx context.Context, service TemporaryService) error {
	return record(ctx, service)
}

// TransactionRecorder persists the post-catch-up VM64 transfer hash before
// the suite begins receipt/finality verification.
type TransactionRecorder interface {
	RecordTransaction(context.Context, string, string) error
}

type TransactionRecorderFunc func(context.Context, string, string) error

func (record TransactionRecorderFunc) RecordTransaction(ctx context.Context, label, hash string) error {
	return record(ctx, label, hash)
}

// ManagedTransactionRecorder persists every irreversible boundary around a
// qrl_sendTransaction call. Intent is immutable request evidence; the two
// attempt markers are monotonic and must be written before their respective
// RPC calls.
type ManagedTransactionRecorder interface {
	RecordManagedTransactionIntent(context.Context, string, lifecycle.ManagedTransactionIntent) error
	RecordManagedTransactionInitialAttempt(context.Context, string) error
	RecordManagedTransactionResubmit(context.Context, string) error
}

// CheckpointRecorder persists temporary identities in a lifecycle checkpoint.
// The caller supplies the canonical Store so validation uses the same stage
// order as the lifecycle runner.
type CheckpointRecorder struct {
	Store lifecycle.Store
	Now   func() time.Time
}

func (recorder CheckpointRecorder) RecordTemporaryService(ctx context.Context, service TemporaryService) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := service.Validate(); err != nil {
		return err
	}
	state, err := recorder.Store.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	if recorder.Now != nil {
		now = recorder.Now()
	}
	return state.RecordTemporaryService(recorder.Store, service.Name, service.UUID, now)
}

func (recorder CheckpointRecorder) RecordTemporaryServiceCreationIntent(ctx context.Context, intent lifecycle.TemporaryServiceCreationIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := recorder.Store.Load()
	if err != nil {
		return err
	}
	return state.RecordTemporaryServiceCreationIntent(recorder.Store, intent, recorder.now())
}

func (recorder CheckpointRecorder) ReconcileTemporaryServices(ctx context.Context, services []TemporaryService) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := recorder.Store.Load()
	if err != nil {
		return err
	}
	next := state
	next.TemporaryServices = cloneServiceUUIDMap(state.TemporaryServices)
	next.TemporaryServiceCreationIntents = cloneCreationIntentMap(state.TemporaryServiceCreationIntents)
	for _, service := range services {
		if err := service.Validate(); err != nil {
			return err
		}
		if current, exists := state.TemporaryServices[service.Name]; exists && current != service.UUID {
			return fmt.Errorf("checkpoint temporary service %s changed from reconciled UUID %s to %s", service.Name, service.UUID, current)
		}
		delete(next.TemporaryServices, service.Name)
		delete(next.TemporaryServiceCreationIntents, service.Name)
	}
	next.UpdatedAt = recorder.now()
	return recorder.Store.Save(next)
}

func (recorder CheckpointRecorder) ReconcileTemporaryServiceCreationIntents(ctx context.Context, intents []lifecycle.TemporaryServiceCreationIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := recorder.Store.Load()
	if err != nil {
		return err
	}
	next := state
	next.TemporaryServiceCreationIntents = cloneCreationIntentMap(state.TemporaryServiceCreationIntents)
	for _, intent := range intents {
		current, exists := state.TemporaryServiceCreationIntents[intent.Name]
		if exists && current != intent {
			return fmt.Errorf("checkpoint temporary service %s has a different creation intent", intent.Name)
		}
		if uuid := state.TemporaryServices[intent.Name]; uuid != "" {
			return fmt.Errorf("checkpoint temporary service %s still has bound UUID %s", intent.Name, uuid)
		}
		delete(next.TemporaryServiceCreationIntents, intent.Name)
	}
	next.UpdatedAt = recorder.now()
	return recorder.Store.Save(next)
}

// TemporaryServiceCreationState is used by compatibility callers that pass a
// CheckpointRecorder but do not separately thread the additive intent map.
func (recorder CheckpointRecorder) TemporaryServiceCreationState(ctx context.Context) (map[string]lifecycle.TemporaryServiceCreationIntent, map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	state, err := recorder.Store.Load()
	if err != nil {
		return nil, nil, err
	}
	return cloneCreationIntentMap(state.TemporaryServiceCreationIntents), cloneServiceUUIDMap(state.TemporaryServices), nil
}

func (recorder CheckpointRecorder) ActiveFreshSyncStage(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	state, err := recorder.Store.Load()
	if err != nil {
		return "", err
	}
	return activeFreshSyncCheckpointStage(state)
}

func (recorder CheckpointRecorder) RecordTransaction(ctx context.Context, label, hash string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := recorder.Store.Load()
	if err != nil {
		return err
	}
	now := time.Now()
	if recorder.Now != nil {
		now = recorder.Now()
	}
	return state.RecordTransaction(recorder.Store, label, hash, now)
}

func (recorder CheckpointRecorder) RecordManagedTransactionIntent(ctx context.Context, label string, intent lifecycle.ManagedTransactionIntent) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := recorder.Store.Load()
	if err != nil {
		return err
	}
	return state.RecordManagedTransactionIntent(recorder.Store, label, intent, recorder.now())
}

func (recorder CheckpointRecorder) RecordManagedTransactionInitialAttempt(ctx context.Context, label string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := recorder.Store.Load()
	if err != nil {
		return err
	}
	return state.RecordManagedTransactionInitialAttempt(recorder.Store, label, recorder.now())
}

func (recorder CheckpointRecorder) RecordManagedTransactionResubmit(ctx context.Context, label string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	state, err := recorder.Store.Load()
	if err != nil {
		return err
	}
	return state.RecordManagedTransactionResubmit(recorder.Store, label, recorder.now())
}

func (recorder CheckpointRecorder) now() time.Time {
	if recorder.Now != nil {
		return recorder.Now().UTC()
	}
	return time.Now().UTC()
}

// Options supplies orchestration dependencies without leaking them into the
// protocol assertions. The CLI config-cloning fallback remains intentional:
// the SDK client is used for immutable identity, endpoints, and UUID cleanup.
type Options struct {
	Client                            kurtosisapi.Client
	Enclave                           lifecycle.EnclaveRef
	Recorder                          TemporaryServiceRecorder
	RecordedServices                  map[string]string
	RecordedServiceCreationIntents    map[string]lifecycle.TemporaryServiceCreationIntent
	TransactionRecorder               TransactionRecorder
	ManagedTransactionRecorder        ManagedTransactionRecorder
	RecordedTransactions              map[string]string
	RecordedManagedTransactionIntents map[string]lifecycle.ManagedTransactionIntent
	ManagedTransactionInitialAttempts map[string]time.Time
	ManagedTransactionResubmits       map[string]time.Time
	Now                               func() time.Time
}

type TemporaryServiceCreationRecovery struct {
	Absent           []TemporaryService
	AbandonedIntents []lifecycle.TemporaryServiceCreationIntent
	Bound            []TemporaryService
	Reusable         []TemporaryService
	LegacyRemovals   []TemporaryService
}

func cloneServiceUUIDMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneCreationIntentMap(values map[string]lifecycle.TemporaryServiceCreationIntent) map[string]lifecycle.TemporaryServiceCreationIntent {
	cloned := make(map[string]lifecycle.TemporaryServiceCreationIntent, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func validateCreationIntent(name string, intent lifecycle.TemporaryServiceCreationIntent, enclave lifecycle.EnclaveRef) error {
	if intent.Name != name || name == "" {
		return fmt.Errorf("temporary service creation intent name mismatch: key=%q intent=%q", name, intent.Name)
	}
	if intent.EnclaveUUID != enclave.UUID {
		return fmt.Errorf("temporary service %s creation intent enclave changed: intent=%s current=%s", name, intent.EnclaveUUID, enclave.UUID)
	}
	if !serviceDigestPattern.MatchString(intent.ConfigDigest) || !serviceDigestPattern.MatchString(intent.Marker) || intent.PreparedAt.IsZero() {
		return fmt.Errorf("temporary service %s creation intent is incomplete or invalid", name)
	}
	_, offset := intent.PreparedAt.Zone()
	if offset != 0 {
		return fmt.Errorf("temporary service %s creation intent preparation time is not UTC", name)
	}
	return nil
}

// RecoverTemporaryServiceCreations inspects the full service set without
// mutating it. A service is reusable only when its exact name, full UUID (when
// already bound), and durable creation marker all agree. All requested names
// are validated before the caller persists a binding or removes anything.
func RecoverTemporaryServiceCreations(
	ctx context.Context,
	client kurtosisapi.Client,
	enclave lifecycle.EnclaveRef,
	recorded map[string]string,
	intents map[string]lifecycle.TemporaryServiceCreationIntent,
	names ...string,
) (TemporaryServiceCreationRecovery, error) {
	if client == nil {
		return TemporaryServiceCreationRecovery{}, errors.New("Kurtosis client is required for temporary-service creation recovery")
	}
	if err := enclave.Validate(); err != nil {
		return TemporaryServiceCreationRecovery{}, err
	}
	services, err := client.Services(ctx, enclave)
	if err != nil {
		return TemporaryServiceCreationRecovery{}, fmt.Errorf("discover temporary services for creation recovery: %w", err)
	}
	byName := make(map[string][]kurtosisapi.Service, len(services))
	for _, service := range services {
		byName[service.Name] = append(byName[service.Name], service)
	}
	var result TemporaryServiceCreationRecovery
	for _, name := range names {
		matches := byName[name]
		if len(matches) > 1 {
			return result, fmt.Errorf("temporary service %s creation recovery is ambiguous: discovered %d services with that name", name, len(matches))
		}
		intent, hasIntent := intents[name]
		if hasIntent {
			if err := validateCreationIntent(name, intent, enclave); err != nil {
				return result, err
			}
		}
		recordedUUID := recorded[name]
		if recordedUUID != "" {
			if err := (TemporaryService{Name: name, UUID: recordedUUID}).Validate(); err != nil {
				return result, err
			}
		}
		if len(matches) == 0 {
			if recordedUUID != "" {
				result.Absent = append(result.Absent, TemporaryService{Name: name, UUID: recordedUUID})
			}
			if hasIntent {
				result.AbandonedIntents = append(result.AbandonedIntents, intent)
			}
			continue
		}
		current := matches[0]
		identity := TemporaryService{Name: current.Name, UUID: current.UUID}
		if err := identity.Validate(); err != nil {
			return result, err
		}
		if !hasIntent {
			if recordedUUID == "" {
				return result, fmt.Errorf("temporary service %s exists as %s without a durable creation intent or UUID record; preserving it", name, current.UUID)
			}
			if current.UUID != recordedUUID {
				return result, fmt.Errorf("temporary service %s UUID changed: current=%s recorded=%s; preserving it", name, current.UUID, recordedUUID)
			}
			result.LegacyRemovals = append(result.LegacyRemovals, identity)
			continue
		}
		if marker := current.Labels[TemporaryServiceCreationIntentLabel]; marker != intent.Marker {
			return result, fmt.Errorf("temporary service %s creation marker changed: current=%q intent=%q; preserving it", name, marker, intent.Marker)
		}
		if recordedUUID != "" && current.UUID != recordedUUID {
			return result, fmt.Errorf("temporary service %s UUID changed: current=%s recorded=%s; preserving it", name, current.UUID, recordedUUID)
		}
		if recordedUUID == "" {
			result.Bound = append(result.Bound, identity)
		}
		result.Reusable = append(result.Reusable, identity)
	}
	return result, nil
}

func serviceConfigDigest(cfg rawServiceConfig) (string, error) {
	encoded, err := cfg.marshal()
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:]), nil
}

func newTemporaryServiceCreationIntent(name string, enclave lifecycle.EnclaveRef, cfg rawServiceConfig, now time.Time) (lifecycle.TemporaryServiceCreationIntent, error) {
	digest, err := serviceConfigDigest(cfg)
	if err != nil {
		return lifecycle.TemporaryServiceCreationIntent{}, fmt.Errorf("digest temporary service %s config: %w", name, err)
	}
	markerBytes := make([]byte, sha256.Size)
	if _, err := io.ReadFull(rand.Reader, markerBytes); err != nil {
		return lifecycle.TemporaryServiceCreationIntent{}, fmt.Errorf("generate temporary service %s creation marker: %w", name, err)
	}
	return lifecycle.TemporaryServiceCreationIntent{
		Name: name, EnclaveUUID: enclave.UUID, ConfigDigest: digest,
		Marker: fmt.Sprintf("%x", markerBytes), PreparedAt: now.UTC(),
	}, nil
}

func configWithCreationIntent(cfg rawServiceConfig, intent lifecycle.TemporaryServiceCreationIntent) (rawServiceConfig, error) {
	cloned := make(rawServiceConfig, len(cfg)+1)
	for key, value := range cfg {
		cloned[key] = slices.Clone(value)
	}
	labels := make(map[string]string)
	if raw, exists := cloned["labels"]; exists {
		if err := json.Unmarshal(raw, &labels); err != nil {
			return nil, fmt.Errorf("decode service config labels: %w", err)
		}
	}
	if labels == nil {
		labels = make(map[string]string)
	}
	if prior := labels[TemporaryServiceCreationIntentLabel]; prior != "" && prior != intent.Marker {
		return nil, fmt.Errorf("service config already contains reserved creation-intent label %q", prior)
	}
	labels[TemporaryServiceCreationIntentLabel] = intent.Marker
	if err := cloned.set("labels", labels); err != nil {
		return nil, err
	}
	return cloned, nil
}

// PersistTemporaryServiceCreationRecovery applies only checkpoint mutations;
// the caller performs any UUID-verified external removals separately.
func PersistTemporaryServiceCreationRecovery(ctx context.Context, recorder TemporaryServiceRecorder, result TemporaryServiceCreationRecovery) error {
	for _, identity := range result.Bound {
		if recorder == nil {
			return errors.New("temporary service creation recovery requires a UUID recorder")
		}
		if err := recorder.RecordTemporaryService(ctx, identity); err != nil {
			return fmt.Errorf("persist recovered temporary service %s/%s: %w", identity.Name, identity.UUID, err)
		}
	}
	if len(result.Absent) != 0 {
		reconciler, ok := recorder.(TemporaryServiceReconciler)
		if !ok {
			return errors.New("absent temporary services require a recorder that can reconcile UUIDs")
		}
		if err := reconciler.ReconcileTemporaryServices(ctx, result.Absent); err != nil {
			return fmt.Errorf("persist absent temporary-service reconciliation: %w", err)
		}
	}
	if len(result.AbandonedIntents) != 0 {
		reconciler, ok := recorder.(TemporaryServiceCreationReconciler)
		if !ok {
			return errors.New("absent temporary-service creations require an intent reconciler")
		}
		if err := reconciler.ReconcileTemporaryServiceCreationIntents(ctx, result.AbandonedIntents); err != nil {
			return fmt.Errorf("persist absent temporary-service creation reconciliation: %w", err)
		}
	}
	return nil
}

// ReconcileResult describes UUID-verified leftovers removed before a retry.
type ReconcileResult struct {
	Absent  []TemporaryService `json:"absent,omitempty"`
	Removed []TemporaryService `json:"removed,omitempty"`
}

// ReconcileTemporaryServices makes a fresh-sync retry safe. A service is
// removed only when the currently discovered full UUID exactly matches the
// durable record. An existing unrecorded or mismatched service is preserved
// and returned as an error.
func ReconcileTemporaryServices(ctx context.Context, client kurtosisapi.Client, enclave lifecycle.EnclaveRef, recorded map[string]string, names ...string) (ReconcileResult, error) {
	if client == nil {
		return ReconcileResult{}, errors.New("Kurtosis client is required for temporary-service reconciliation")
	}
	if err := enclave.Validate(); err != nil {
		return ReconcileResult{}, err
	}
	services, err := client.Services(ctx, enclave)
	if err != nil {
		return ReconcileResult{}, fmt.Errorf("discover temporary services: %w", err)
	}
	byName := make(map[string]kurtosisapi.Service, len(services))
	for _, service := range services {
		byName[service.Name] = service
	}
	var result ReconcileResult
	var removals []TemporaryService
	for _, name := range names {
		current, exists := byName[name]
		if !exists {
			if uuid := recorded[name]; uuid != "" {
				identity := TemporaryService{Name: name, UUID: uuid}
				if err := identity.Validate(); err != nil {
					return result, err
				}
				result.Absent = append(result.Absent, identity)
			}
			continue
		}
		expectedUUID := recorded[name]
		if expectedUUID == "" {
			return result, fmt.Errorf("temporary service %s exists as %s without a durable UUID record; preserving it", name, current.UUID)
		}
		identity := TemporaryService{Name: name, UUID: expectedUUID}
		if err := identity.Validate(); err != nil {
			return result, err
		}
		if current.UUID != expectedUUID {
			return result, fmt.Errorf("temporary service %s UUID changed: current=%s recorded=%s; preserving it", name, current.UUID, expectedUUID)
		}
		removals = append(removals, identity)
	}
	// Validate the complete reconciliation set before the first mutation so a
	// later UUID mismatch cannot cause a partially cleaned retry boundary.
	for _, identity := range removals {
		if err := removeTemporaryService(ctx, client, enclave, identity); err != nil {
			return result, err
		}
		result.Removed = append(result.Removed, identity)
	}
	return result, nil
}

// CleanupTemporaryServices removes identities in reverse creation order and
// verifies every name/UUID pair immediately before deletion.
func CleanupTemporaryServices(ctx context.Context, client kurtosisapi.Client, enclave lifecycle.EnclaveRef, services []TemporaryService) error {
	var cleanupErrors []error
	for index := len(services) - 1; index >= 0; index-- {
		if err := removeTemporaryService(ctx, client, enclave, services[index]); err != nil {
			cleanupErrors = append(cleanupErrors, err)
		}
	}
	return errors.Join(cleanupErrors...)
}

func removeTemporaryService(ctx context.Context, client kurtosisapi.Client, enclave lifecycle.EnclaveRef, identity TemporaryService) error {
	if client == nil {
		return errors.New("Kurtosis client is required for UUID-safe cleanup")
	}
	if err := identity.Validate(); err != nil {
		return err
	}
	current, err := client.Service(ctx, enclave, identity.UUID)
	if err != nil {
		return fmt.Errorf("resolve temporary service %s/%s before removal: %w", identity.Name, identity.UUID, err)
	}
	if current.Name != identity.Name || current.UUID != identity.UUID {
		return fmt.Errorf("temporary service identity changed: current=%s/%s recorded=%s/%s; preserving it", current.Name, current.UUID, identity.Name, identity.UUID)
	}
	if err := client.RemoveService(ctx, enclave, identity.UUID); err != nil {
		return fmt.Errorf("remove temporary service %s/%s: %w", identity.Name, identity.UUID, err)
	}
	return nil
}

func resolveIntendedTemporaryService(ctx context.Context, client kurtosisapi.Client, enclave lifecycle.EnclaveRef, intent lifecycle.TemporaryServiceCreationIntent) (TemporaryService, error) {
	if client == nil {
		return TemporaryService{}, errors.New("Kurtosis client is required to capture a temporary service UUID")
	}
	if err := validateCreationIntent(intent.Name, intent, enclave); err != nil {
		return TemporaryService{}, err
	}
	services, err := client.Services(ctx, enclave)
	if err != nil {
		return TemporaryService{}, fmt.Errorf("resolve newly created temporary service %s: %w", intent.Name, err)
	}
	var matches []kurtosisapi.Service
	for _, service := range services {
		if service.Name == intent.Name {
			matches = append(matches, service)
		}
	}
	if len(matches) != 1 {
		return TemporaryService{}, fmt.Errorf("resolve newly created temporary service %s: discovered %d exact-name matches", intent.Name, len(matches))
	}
	service := matches[0]
	identity := TemporaryService{Name: service.Name, UUID: service.UUID}
	if err := identity.Validate(); err != nil {
		return TemporaryService{}, err
	}
	if marker := service.Labels[TemporaryServiceCreationIntentLabel]; marker != intent.Marker {
		return TemporaryService{}, fmt.Errorf("created service %s marker changed: got %q, want %q", identity.Name, marker, intent.Marker)
	}
	return identity, nil
}

func captureTemporaryService(ctx context.Context, client kurtosisapi.Client, enclave lifecycle.EnclaveRef, intent lifecycle.TemporaryServiceCreationIntent, recorder TemporaryServiceRecorder) (TemporaryService, error) {
	identity, err := resolveIntendedTemporaryService(ctx, client, enclave, intent)
	if err != nil {
		return TemporaryService{}, err
	}
	if recorder != nil {
		if err := recorder.RecordTemporaryService(ctx, identity); err != nil {
			return identity, fmt.Errorf("persist temporary service %s/%s: %w", identity.Name, identity.UUID, err)
		}
	}
	return identity, nil
}

func refreshPublicEndpoint(ctx context.Context, client kurtosisapi.Client, enclave lifecycle.EnclaveRef, identity TemporaryService, portID, scheme string) (string, error) {
	current, err := client.Service(ctx, enclave, identity.UUID)
	if err != nil {
		return "", err
	}
	if current.Name != identity.Name || current.UUID != identity.UUID {
		return "", fmt.Errorf("temporary service identity changed while refreshing endpoint: got %s/%s, want %s/%s", current.Name, current.UUID, identity.Name, identity.UUID)
	}
	endpoint, ok := current.PublicEndpoint(portID, scheme)
	if !ok {
		return "", fmt.Errorf("temporary service %s has no public %s port %q", identity.Name, scheme, portID)
	}
	return endpoint, nil
}

// OpenCheckpoint builds the validation store needed by the compatibility
// command. Importing harnesses should prefer passing their canonical Store to
// CheckpointRecorder directly.
func OpenCheckpoint(path string) (lifecycle.Checkpoint, lifecycle.Store, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return lifecycle.Checkpoint{}, lifecycle.Store{}, err
	}
	var unchecked lifecycle.Checkpoint
	if err := json.Unmarshal(payload, &unchecked); err != nil {
		return lifecycle.Checkpoint{}, lifecycle.Store{}, fmt.Errorf("decode checkpoint %s: %w", path, err)
	}
	store := lifecycle.Store{Path: path, StageOrder: slices.Clone(legacyLifecycleStageOrder)}
	state, err := store.Load()
	if err != nil {
		return lifecycle.Checkpoint{}, lifecycle.Store{}, err
	}
	if _, err := activeFreshSyncCheckpointStage(state); err != nil {
		return lifecycle.Checkpoint{}, lifecycle.Store{}, err
	}
	return state, store, nil
}

func activeFreshSyncCheckpointStage(state lifecycle.Checkpoint) (string, error) {
	if state.Status != lifecycle.StatusRunning || state.CurrentStage == nil {
		return "", errors.New("fresh-sync checkpoint must be running an active fresh-snap or fresh-full stage")
	}
	stage := *state.CurrentStage
	if stage != "fresh-snap" && stage != "fresh-full" {
		return "", fmt.Errorf("fresh-sync checkpoint current stage is %q, want fresh-snap or fresh-full", stage)
	}
	if len(state.Attempts) == 0 {
		return "", errors.New("fresh-sync checkpoint has no active attempt")
	}
	last := state.Attempts[len(state.Attempts)-1]
	if last.Stage != stage || last.FinishedAt != nil || last.ExitCode != nil {
		return "", fmt.Errorf("fresh-sync checkpoint final attempt does not match active unfinished stage %s", stage)
	}
	return stage, nil
}
