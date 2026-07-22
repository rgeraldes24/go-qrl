// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

// ServiceController is the narrow service lifecycle surface needed by the
// system suite. The harness can adapt its UUID-checked Kurtosis SDK client;
// the compatibility command adapts the same SDK client around its checkpoint
// enclave and service UUIDs.
type ServiceController interface {
	Endpoint(context.Context, string, string, string) (string, error)
	Status(context.Context, string) (ServiceStatus, error)
	Stop(context.Context, string) error
	Start(context.Context, string) error
}

type ServiceStatus string

const (
	ServiceRunning ServiceStatus = "running"
	ServiceStopped ServiceStatus = "stopped"
)

// RestartState records a one-way disruptive transition. The suite does not
// retry Start or Stop calls; a resume owner must inspect the recorded state and
// the live service before deciding whether a transition is safe to continue.
type RestartState string

const (
	RestartStopIntent           RestartState = "stop-intent"
	RestartStopped              RestartState = "stopped"
	RestartStartIntent          RestartState = "start-intent"
	RestartStarted              RestartState = "started"
	RestartHealthy              RestartState = "healthy"
	RestartEmergencyStartIntent RestartState = "emergency-start-intent"
	RestartEmergencyStarted     RestartState = "emergency-started"
)

// RestartEvidence is emitted before and after every service mutation and once
// the restarted service has passed its health assertion.
type RestartEvidence struct {
	Phase   Phase        `json:"phase"`
	Service string       `json:"service"`
	State   RestartState `json:"state"`
	At      time.Time    `json:"at"`
}

// EndpointEvidence records a refreshed public endpoint after a restart.
type EndpointEvidence struct {
	Phase    Phase     `json:"phase"`
	Service  string    `json:"service"`
	Kind     string    `json:"kind"`
	Previous string    `json:"previous"`
	Current  string    `json:"current"`
	At       time.Time `json:"at"`
}

// EvidenceRecorder durably records restart progress for lifecycle resume.
// Returning an error before a mutation prevents that mutation. Returning an
// error after a mutation aborts the phase so the outer lifecycle can inspect
// the service rather than blindly replaying the transition.
type EvidenceRecorder interface {
	RecordRestart(context.Context, RestartEvidence) error
	RecordEndpoint(context.Context, EndpointEvidence) error
}

// TransactionEvidence is emitted synchronously after qrl_sendTransaction
// returns a non-zero hash and before the suite can begin any receipt or state
// wait. Label is a stable logical-phase/order identifier.
type TransactionEvidence struct {
	Phase Phase       `json:"phase"`
	Label string      `json:"label"`
	Hash  common.Hash `json:"hash"`
	At    time.Time   `json:"at"`
}

// TransactionRecorder durably records a submitted transaction before the
// suite performs any observation that may be interrupted.
type TransactionRecorder interface {
	RecordTransaction(context.Context, TransactionEvidence) error
}

// TransactionRecorderFunc adapts a function to TransactionRecorder.
type TransactionRecorderFunc func(context.Context, TransactionEvidence) error

func (recorder TransactionRecorderFunc) RecordTransaction(ctx context.Context, evidence TransactionEvidence) error {
	return recorder(ctx, evidence)
}

type ManagedAccessTuple = lifecycle.ManagedAccessTuple
type ManagedTransactionIntent = lifecycle.ManagedTransactionIntent

// ManagedTransactionJournal is a durable outbox for qrl_sendTransaction.
// The immutable intent is stored first, followed by a marker immediately
// before the initial RPC. Resume may persist one resubmit marker before one
// safe replay; a second absent reconciliation fails closed.
type ManagedTransactionJournal interface {
	RecordManagedTransactionIntent(context.Context, ManagedTransactionIntent) error
	RecordManagedTransactionInitialAttempt(context.Context, string, time.Time) error
	RecordManagedTransactionResubmit(context.Context, string, time.Time) error
}

// SystemObservationRecorder persists immutable base/fault observations and
// completed assertion milestones. The JSON value is typed and validated by
// the system suite before it is trusted on resume.
type SystemObservationRecorder interface {
	RecordSystemObservation(context.Context, string, string, time.Time) error
}

// SystemObservationRecorderFunc adapts a function to SystemObservationRecorder.
type SystemObservationRecorderFunc func(context.Context, string, string, time.Time) error

func (recorder SystemObservationRecorderFunc) RecordSystemObservation(ctx context.Context, label, value string, at time.Time) error {
	return recorder(ctx, label, value, at)
}

const (
	TransactionLabelBaseEL1Transfer        = "base/01-managed-transfer-el1"
	TransactionLabelBaseEL2Transfer        = "base/02-managed-transfer-el2"
	TransactionLabelBaseAccessListDeploy   = "base/03-access-list-deploy"
	TransactionLabelBaseAccessListWrite    = "base/04-access-list-write"
	TransactionLabelSignerOutageProbe      = "signer-restart/01-outage-probe"
	TransactionLabelSignerRecoveryTransfer = "signer-restart/02-recovery-transfer"
	TransactionLabelParticipantOffline     = "participant-restart/01-offline-transfer"
)

// Options supplies orchestration integrations. All fields are optional.
type Options struct {
	Controller          ServiceController
	Evidence            EvidenceRecorder
	TransactionRecorder TransactionRecorder
	ManagedJournal      ManagedTransactionJournal
	ObservationRecorder SystemObservationRecorder
	// RecordedTransactions and RestartHistory are durable evidence from a
	// previous attempt. They are validated before any new mutation and let the
	// suite continue without repeating a submitted transaction or completed
	// Stop/Start call.
	RecordedTransactions              map[string]string
	RestartHistory                    []RestartEvidence
	ManagedTransactionIntents         map[string]ManagedTransactionIntent
	ManagedTransactionInitialAttempts map[string]time.Time
	ManagedTransactionResubmits       map[string]time.Time
	ServiceUUIDs                      map[string]string
	RecordedObservations              map[string]string
}

type resumeState struct {
	transactions           map[string]common.Hash
	restarts               map[string][]RestartState
	managedIntents         map[string]ManagedTransactionIntent
	managedInitialAttempts map[string]time.Time
	managedResubmits       map[string]time.Time
	serviceUUIDs           map[string]string
	observations           map[string]string
}

type evidenceError struct {
	err error
}

func (e *evidenceError) Error() string { return fmt.Sprintf("record lifecycle evidence: %v", e.err) }
func (e *evidenceError) Unwrap() error { return e.err }

// Run executes exactly one configured serialized phase (or the compatibility
// all phase) without retrying service lifecycle mutations.
func Run(ctx context.Context, cfg Config, options Options) error {
	if ctx == nil {
		return fmt.Errorf("system suite context is nil")
	}
	runtimeConfig, err := cfg.internal()
	if err != nil {
		return err
	}
	controller := options.Controller
	if controller == nil {
		controller = kurtosis{enclave: runtimeConfig.enclave, runner: execRunner{}}
	}
	resume, err := validateResumeState(runtimeConfig, options.RecordedTransactions, options.RestartHistory)
	if err != nil {
		return err
	}
	resume.managedIntents = cloneManagedIntents(options.ManagedTransactionIntents)
	resume.managedInitialAttempts = cloneTimes(options.ManagedTransactionInitialAttempts)
	resume.managedResubmits = cloneTimes(options.ManagedTransactionResubmits)
	resume.serviceUUIDs = cloneStrings(options.ServiceUUIDs)
	resume.observations = cloneStrings(options.RecordedObservations)
	if err := validateManagedResumeState(runtimeConfig, &resume); err != nil {
		return err
	}
	if err := validateSystemObservationResumeState(runtimeConfig, &resume); err != nil {
		return err
	}
	return runSystemCheckWithResume(ctx, runtimeConfig, controller, options.Evidence, options.TransactionRecorder, options.ManagedJournal, options.ObservationRecorder, resume)
}

func validateResumeState(cfg config, values map[string]string, history []RestartEvidence) (resumeState, error) {
	resume := resumeState{
		transactions:           make(map[string]common.Hash, len(values)),
		restarts:               make(map[string][]RestartState),
		managedIntents:         make(map[string]ManagedTransactionIntent),
		managedInitialAttempts: make(map[string]time.Time),
		managedResubmits:       make(map[string]time.Time),
		serviceUUIDs:           make(map[string]string),
		observations:           make(map[string]string),
	}
	allowedTransactions := map[string]bool{}
	orderedTransactions := []string{}
	switch cfg.phase {
	case string(PhaseBase):
		orderedTransactions = []string{TransactionLabelBaseEL1Transfer, TransactionLabelBaseEL2Transfer, TransactionLabelBaseAccessListDeploy, TransactionLabelBaseAccessListWrite}
	case string(PhaseSignerRestart):
		orderedTransactions = []string{TransactionLabelSignerRecoveryTransfer}
	case string(PhaseParticipantRestart):
		orderedTransactions = []string{TransactionLabelParticipantOffline}
	case string(PhaseAll):
		orderedTransactions = []string{TransactionLabelBaseEL1Transfer, TransactionLabelBaseEL2Transfer, TransactionLabelBaseAccessListDeploy, TransactionLabelBaseAccessListWrite, TransactionLabelSignerRecoveryTransfer, TransactionLabelParticipantOffline}
	}
	for _, label := range orderedTransactions {
		allowedTransactions[label] = true
	}
	for label, raw := range values {
		if label == TransactionLabelSignerOutageProbe {
			return resumeState{}, fmt.Errorf("checkpoint contains %s, but the signer-outage probe must never submit a transaction", label)
		}
		if !allowedTransactions[label] {
			return resumeState{}, fmt.Errorf("recorded system transaction label %q is not valid for phase %q", label, cfg.phase)
		}
		var hash common.Hash
		if err := hash.UnmarshalText([]byte(raw)); err != nil || hash == (common.Hash{}) || hash.Hex() != strings.ToLower(raw) {
			return resumeState{}, fmt.Errorf("recorded system transaction %q has invalid canonical hash %q", label, raw)
		}
		resume.transactions[label] = hash
	}
	// Base mutations are a strict prefix. The restart phases each have one
	// successful transaction, so their map is already unambiguous.
	if cfg.phase == string(PhaseBase) {
		for index, label := range orderedTransactions {
			if _, ok := resume.transactions[label]; ok {
				for predecessor := 0; predecessor < index; predecessor++ {
					if _, exists := resume.transactions[orderedTransactions[predecessor]]; !exists {
						return resumeState{}, fmt.Errorf("recorded system transaction %q is missing predecessor %q", label, orderedTransactions[predecessor])
					}
				}
			}
		}
	}

	allowedServices := map[string]bool{}
	switch cfg.phase {
	case string(PhaseSignerRestart):
		allowedServices[cfg.signerSvc] = true
	case string(PhaseParticipantRestart):
		allowedServices[cfg.elServices[1]] = true
		allowedServices[cfg.clServices[1]] = true
		allowedServices[cfg.vcServices[1]] = true
	case string(PhaseAll):
		allowedServices[cfg.signerSvc] = true
		allowedServices[cfg.elServices[1]] = true
		allowedServices[cfg.clServices[1]] = true
		allowedServices[cfg.vcServices[1]] = true
	}
	for _, evidence := range history {
		if string(evidence.Phase) != cfg.phase || !allowedServices[evidence.Service] || evidence.At.IsZero() {
			return resumeState{}, fmt.Errorf("restart history contains invalid phase/service evidence: %+v", evidence)
		}
		states := resume.restarts[evidence.Service]
		states = append(states, evidence.State)
		if !validRestartStateSequence(states) {
			return resumeState{}, fmt.Errorf("restart history for %s is not the expected ordered prefix at %q", evidence.Service, evidence.State)
		}
		resume.restarts[evidence.Service] = states
	}
	if cfg.phase == string(PhaseParticipantRestart) {
		services := []string{cfg.elServices[1], cfg.clServices[1], cfg.vcServices[1]}
		plannedStart := false
		for _, service := range services {
			states := currentRestartGeneration(resume.restarts[service])
			if containsRestartState(states, RestartStartIntent) {
				plannedStart = true
			}
		}
		if plannedStart {
			if _, ok := resume.transactions[TransactionLabelParticipantOffline]; !ok {
				return resumeState{}, fmt.Errorf("participant restart history reached Start without the offline-window transaction checkpoint")
			}
		}
		if _, submitted := resume.transactions[TransactionLabelParticipantOffline]; submitted {
			for _, service := range services {
				if !containsRestartState(currentRestartGeneration(resume.restarts[service]), RestartStopped) {
					return resumeState{}, fmt.Errorf("offline-window transaction checkpoint exists before %s has a durable Stopped state", service)
				}
			}
		}
	}
	if cfg.phase == string(PhaseSignerRestart) {
		if _, submitted := resume.transactions[TransactionLabelSignerRecoveryTransfer]; submitted {
			if state, ok := lastRestartState(resume.restarts[cfg.signerSvc]); !ok || state != RestartHealthy {
				return resumeState{}, fmt.Errorf("signer recovery transaction checkpoint exists before the signer is Healthy")
			}
		}
	}
	return resume, nil
}

func validRestartStateSequence(states []RestartState) bool {
	if len(states) == 0 {
		return false
	}
	start := 0
	for index := 1; index < len(states); index++ {
		if states[index] != RestartStopIntent {
			continue
		}
		generation := states[start:index]
		if !validRestartGeneration(generation) || !restartGenerationSafelyRecovered(generation) {
			return false
		}
		start = index
	}
	return validRestartGeneration(states[start:])
}

func validRestartGeneration(states []RestartState) bool {
	normal := []RestartState{RestartStopIntent, RestartStopped, RestartStartIntent, RestartStarted, RestartHealthy}
	if len(states) <= len(normal) && slices.Equal(states, normal[:len(states)]) {
		return true
	}
	emergency := []RestartState{RestartEmergencyStartIntent, RestartEmergencyStarted, RestartHealthy}
	// A service can stop unexpectedly after Start returned, after Started was
	// durable, or even after it was declared Healthy while another participant
	// is still recovering. Preserve that normal prefix and append a distinct
	// emergency recovery suffix instead of rewriting history.
	for prefix := 1; prefix <= len(normal); prefix++ {
		if len(states) < prefix || !slices.Equal(states[:prefix], normal[:prefix]) {
			continue
		}
		suffix := states[prefix:]
		if len(suffix) <= len(emergency) && slices.Equal(suffix, emergency[:len(suffix)]) {
			return true
		}
	}
	return false
}

func restartGenerationSafelyRecovered(states []RestartState) bool {
	if !containsRestartState(states, RestartEmergencyStarted) {
		return false
	}
	last, ok := lastRestartState(states)
	return ok && (last == RestartEmergencyStarted || last == RestartHealthy)
}

func currentRestartGeneration(states []RestartState) []RestartState {
	start := 0
	for index := 1; index < len(states); index++ {
		if states[index] == RestartStopIntent {
			start = index
		}
	}
	return states[start:]
}

func containsRestartState(states []RestartState, want RestartState) bool {
	return slices.Contains(states, want)
}

func lastRestartState(states []RestartState) (RestartState, bool) {
	if len(states) == 0 {
		return "", false
	}
	return states[len(states)-1], true
}

func cloneManagedIntents(values map[string]ManagedTransactionIntent) map[string]ManagedTransactionIntent {
	result := make(map[string]ManagedTransactionIntent, len(values))
	for label, intent := range values {
		intent.AccessList = append([]ManagedAccessTuple(nil), intent.AccessList...)
		for index := range intent.AccessList {
			intent.AccessList[index].StorageKeys = append([]string(nil), intent.AccessList[index].StorageKeys...)
		}
		result[label] = intent
	}
	return result
}

func cloneTimes(values map[string]time.Time) map[string]time.Time {
	result := make(map[string]time.Time, len(values))
	for label, value := range values {
		result[label] = value
	}
	return result
}

func cloneStrings(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for label, value := range values {
		result[label] = value
	}
	return result
}

func validateManagedResumeState(cfg config, resume *resumeState) error {
	allowed := []string{}
	switch cfg.phase {
	case string(PhaseBase):
		allowed = []string{TransactionLabelBaseEL1Transfer, TransactionLabelBaseEL2Transfer, TransactionLabelBaseAccessListDeploy, TransactionLabelBaseAccessListWrite}
	case string(PhaseSignerRestart):
		allowed = []string{TransactionLabelSignerRecoveryTransfer}
	case string(PhaseParticipantRestart):
		allowed = []string{TransactionLabelParticipantOffline}
	case string(PhaseAll):
		allowed = []string{TransactionLabelBaseEL1Transfer, TransactionLabelBaseEL2Transfer, TransactionLabelBaseAccessListDeploy, TransactionLabelBaseAccessListWrite, TransactionLabelSignerRecoveryTransfer, TransactionLabelParticipantOffline}
	}
	position := make(map[string]int, len(allowed))
	for index, label := range allowed {
		position[label] = index
	}
	for label, intent := range resume.managedIntents {
		index, ok := position[label]
		if !ok || intent.Label != label || intent.Phase != cfg.phase || intent.Origin < 0 || intent.Origin >= len(cfg.elServices) || intent.OriginServiceName != cfg.elServices[intent.Origin] || resume.serviceUUIDs[intent.OriginServiceName] != intent.OriginServiceUUID {
			return fmt.Errorf("managed transaction intent %q does not match phase %q or immutable origin identity", label, cfg.phase)
		}
		if _, err := common.NewAddressFromString(intent.From); err != nil {
			return fmt.Errorf("managed transaction intent %q has invalid sender: %w", label, err)
		}
		if intent.To != "" {
			if _, err := common.NewAddressFromString(intent.To); err != nil {
				return fmt.Errorf("managed transaction intent %q has invalid recipient: %w", label, err)
			}
		}
		for predecessor := 0; predecessor < index; predecessor++ {
			if _, submitted := resume.transactions[allowed[predecessor]]; !submitted {
				return fmt.Errorf("managed transaction intent %q is missing submitted predecessor %q", label, allowed[predecessor])
			}
		}
	}
	for label := range resume.managedInitialAttempts {
		if _, ok := resume.managedIntents[label]; !ok {
			return fmt.Errorf("managed initial-attempt marker %q has no immutable intent", label)
		}
	}
	for label := range resume.managedResubmits {
		if _, ok := resume.managedIntents[label]; !ok {
			return fmt.Errorf("managed resubmit marker %q has no immutable intent", label)
		}
		if _, ok := resume.managedInitialAttempts[label]; !ok {
			return fmt.Errorf("managed resubmit marker %q has no initial-attempt marker", label)
		}
	}
	for label := range resume.transactions {
		if _, ok := position[label]; !ok {
			continue
		}
		if _, ok := resume.managedIntents[label]; !ok {
			return fmt.Errorf("submitted managed transaction %q has no immutable intent", label)
		}
	}
	return nil
}

func (s *systemCheck) recordRestart(ctx context.Context, service string, state RestartState) error {
	states := append([]RestartState(nil), s.resume.restarts[service]...)
	states = append(states, state)
	if !validRestartStateSequence(states) {
		return fmt.Errorf("restart transition %s/%s would make invalid history %v", service, state, states)
	}
	if s.evidence != nil {
		if err := s.evidence.RecordRestart(ctx, RestartEvidence{
			Phase: Phase(s.cfg.phase), Service: service, State: state, At: s.currentTime().UTC(),
		}); err != nil {
			return &evidenceError{err: err}
		}
	}
	if s.resume.restarts == nil {
		s.resume.restarts = make(map[string][]RestartState)
	}
	// Update the in-memory view only after the durable append succeeds. Safety
	// defers then see the exact latest transition and never append a duplicate
	// emergency intent during the same attempt.
	s.resume.restarts[service] = states
	return nil
}

func (s *systemCheck) recordSystemObservation(ctx context.Context, label, value string) error {
	if prior, exists := s.resume.observations[label]; exists {
		if prior != value {
			return fmt.Errorf("system observation %s already has different evidence", label)
		}
		return nil
	}
	if s.observations != nil {
		if err := s.observations.RecordSystemObservation(ctx, label, value, s.currentTime().UTC()); err != nil {
			return &evidenceError{err: fmt.Errorf("record system observation %s: %w", label, err)}
		}
	}
	if s.resume.observations == nil {
		s.resume.observations = make(map[string]string)
	}
	s.resume.observations[label] = value
	return nil
}

func (s *systemCheck) recordEndpoint(ctx context.Context, service, kind, previous, current string) error {
	if previous == current || s.evidence == nil {
		return nil
	}
	if err := s.evidence.RecordEndpoint(ctx, EndpointEvidence{
		Phase: Phase(s.cfg.phase), Service: service, Kind: kind, Previous: previous, Current: current, At: s.currentTime().UTC(),
	}); err != nil {
		return &evidenceError{err: err}
	}
	return nil
}

func (s *systemCheck) recordTransaction(ctx context.Context, label string, hash common.Hash) error {
	if s.transactions == nil {
		return nil
	}
	if label == "" || hash == (common.Hash{}) {
		return &evidenceError{err: fmt.Errorf("transaction label or hash is empty")}
	}
	if err := s.transactions.RecordTransaction(ctx, TransactionEvidence{
		Phase: Phase(s.cfg.phase), Label: label, Hash: hash, At: s.currentTime().UTC(),
	}); err != nil {
		return &evidenceError{err: fmt.Errorf("transaction %s as %s was submitted but could not be recorded: %w", hash, label, err)}
	}
	return nil
}

func (s *systemCheck) recordedTransaction(label string) (common.Hash, bool) {
	if s == nil {
		return common.Hash{}, false
	}
	hash, ok := s.resume.transactions[label]
	return hash, ok
}

func (s *systemCheck) restartState(service string) (RestartState, bool) {
	states := s.resume.restarts[service]
	if len(states) == 0 {
		return "", false
	}
	return states[len(states)-1], true
}

func (s *systemCheck) hasRestartHistory() bool { return len(s.resume.restarts) != 0 }
