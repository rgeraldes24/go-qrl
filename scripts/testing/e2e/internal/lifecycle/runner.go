package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"
)

type ResumePolicy string

const (
	RetrySafe          ResumePolicy = "retry_safe"
	InspectBeforeRetry ResumePolicy = "inspect_before_retry"
	RetryByUUID        ResumePolicy = "retry_by_uuid"
)

// ReconcileAction tells the runner whether external state proves that a failed
// stage already completed or whether it is safe to execute the stage again.
// Returning an error rejects resume for that stage.
type ReconcileAction string

const (
	ReconcileComplete ReconcileAction = "complete"
	ReconcileRetry    ReconcileAction = "retry"
)

type RunEnvironment struct {
	Enclave EnclaveRef
	State   *Checkpoint
	Values  map[string]any
}

type Stage struct {
	Name           string
	Timeout        time.Duration
	MinimumRuntime time.Duration
	ResumePolicy   ResumePolicy
	Disruptive     bool
	Run            func(context.Context, *RunEnvironment) error
	Reconcile      func(context.Context, *RunEnvironment) (ReconcileAction, error)
}

type Runner struct {
	Store             Store
	Stages            []Stage
	GlobalDeadline    time.Time
	CleanupReserve    time.Duration
	AllowDisruptive   bool
	Now               func() time.Time
	Classify          func(error) FailureCategory
	OnAttemptFinished func(Attempt) error
}

func (r *Runner) Run(ctx context.Context, environment *RunEnvironment) error {
	state, err := r.Store.Load()
	if err != nil {
		return err
	}
	environment.State = &state
	for _, stage := range r.Stages {
		if state.StageComplete(stage.Name) {
			continue
		}
		if stage.Disruptive && !environment.Enclave.Owned && !r.AllowDisruptive {
			return fmt.Errorf("stage %s is disruptive and borrowed-network mutation was not authorized", stage.Name)
		}
		attemptCount := 0
		for _, attempt := range state.Attempts {
			if attempt.Stage == stage.Name {
				attemptCount++
			}
		}
		if attemptCount > 0 && (stage.ResumePolicy == InspectBeforeRetry || stage.ResumePolicy == RetryByUUID) {
			if stage.Reconcile == nil {
				return fmt.Errorf("stage %s requires reconciliation before retry", stage.Name)
			}
			action, err := stage.Reconcile(ctx, environment)
			if err != nil {
				return fmt.Errorf("reconcile stage %s: %w", stage.Name, err)
			}
			switch action {
			case ReconcileComplete:
				if err := r.recordReconciledCompletion(stage, environment, &state); err != nil {
					return err
				}
				continue
			case ReconcileRetry:
				// The reconciler proved that replaying the stage is safe.
			default:
				return fmt.Errorf("reconcile stage %s returned invalid action %q", stage.Name, action)
			}
		}
		if err := r.runStage(ctx, stage, environment, &state); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) recordReconciledCompletion(stage Stage, environment *RunEnvironment, state *Checkpoint) error {
	now := r.now()
	attemptNumber := 1
	for _, attempt := range state.Attempts {
		if attempt.Stage == stage.Name {
			attemptNumber++
		}
	}
	exitCode := 0
	attempt := Attempt{
		Stage:      stage.Name,
		Attempt:    attemptNumber,
		StartedAt:  now,
		FinishedAt: &now,
		ExitCode:   &exitCode,
		Reconciled: true,
	}
	next := *state
	next.Attempts = append(slices.Clone(state.Attempts), attempt)
	next.Completed = append(slices.Clone(state.Completed), stage.Name)
	next.CurrentStage = nil
	next.Resumed = true
	next.Status = StatusRunning
	next.FailureCategory = FailureNone
	next.FailureMessage = ""
	next.UpdatedAt = now
	if err := r.Store.Save(next); err != nil {
		return fmt.Errorf("checkpoint reconciled stage %s: %w", stage.Name, err)
	}
	*state = next
	environment.State = state
	if r.OnAttemptFinished != nil {
		if err := r.OnAttemptFinished(attempt); err != nil {
			return fmt.Errorf("record reconciled stage %s evidence: %w", stage.Name, err)
		}
	}
	return nil
}

func (r *Runner) runStage(parent context.Context, stage Stage, environment *RunEnvironment, state *Checkpoint) error {
	if stage.Name == "" || stage.Run == nil || stage.Timeout <= 0 || stage.MinimumRuntime <= 0 {
		return fmt.Errorf("stage %q is not fully configured", stage.Name)
	}
	now := r.now()
	latestEnd := r.GlobalDeadline.Add(-r.CleanupReserve)
	stageEnd := now.Add(stage.Timeout)
	if stageEnd.After(latestEnd) {
		stageEnd = latestEnd
	}
	if stageEnd.Sub(now) < stage.MinimumRuntime {
		return fmt.Errorf("refusing stage %s: only %s remains before cleanup reserve, need %s", stage.Name, stageEnd.Sub(now), stage.MinimumRuntime)
	}
	startedState := *state
	if startedState.Status == StatusFailed {
		startedState.Status = StatusRunning
		startedState.CurrentStage = nil
		startedState.Resumed = true
	}
	attemptNumber := 1
	for _, attempt := range startedState.Attempts {
		if attempt.Stage == stage.Name {
			attemptNumber++
		}
	}
	attempt := Attempt{Stage: stage.Name, Attempt: attemptNumber, StartedAt: now}
	startedState.Attempts = append(slices.Clone(state.Attempts), attempt)
	startedState.CurrentStage = &startedState.Attempts[len(startedState.Attempts)-1].Stage
	startedState.Status = StatusRunning
	startedState.UpdatedAt = now
	if err := r.Store.Save(startedState); err != nil {
		return fmt.Errorf("checkpoint stage %s start: %w", stage.Name, err)
	}
	*state = startedState
	environment.State = state
	stageContext, cancel := context.WithDeadline(parent, stageEnd)
	err := stage.Run(stageContext, environment)
	cancel()
	finished := r.now()
	resultState := *state
	resultState.Attempts = slices.Clone(state.Attempts)
	index := len(resultState.Attempts) - 1
	resultState.Attempts[index].FinishedAt = &finished
	exitCode := 0
	if err != nil {
		exitCode = parseExitCode(err)
		category := r.classify(err)
		resultState.Attempts[index].FailureCategory = category
		resultState.Attempts[index].FailureMessage = err.Error()
		resultState.FailureCategory = category
		resultState.FailureMessage = err.Error()
		resultState.Status = StatusFailed
		resultState.CurrentStage = &resultState.Attempts[index].Stage
	} else {
		resultState.Completed = append(slices.Clone(state.Completed), stage.Name)
		resultState.CurrentStage = nil
		resultState.FailureCategory = FailureNone
		resultState.FailureMessage = ""
		resultState.Status = StatusRunning
	}
	resultState.Attempts[index].ExitCode = pointerTo(exitCode)
	resultState.UpdatedAt = finished
	if saveErr := r.Store.Save(resultState); saveErr != nil {
		return fmt.Errorf("checkpoint stage %s result after exit %d: %w", stage.Name, exitCode, saveErr)
	}
	*state = resultState
	environment.State = state
	if r.OnAttemptFinished != nil {
		if callbackErr := r.OnAttemptFinished(resultState.Attempts[index]); callbackErr != nil {
			return fmt.Errorf("record stage %s evidence: %w", stage.Name, callbackErr)
		}
	}
	if err != nil {
		return fmt.Errorf("stage %s: %w", stage.Name, err)
	}
	return nil
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now().UTC()
	}
	return time.Now().UTC()
}

func (r *Runner) classify(err error) FailureCategory {
	if r.Classify != nil {
		return r.Classify(err)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return FailureTimeout
	}
	if errors.Is(err, context.Canceled) {
		return FailureCancellation
	}
	type exitCoder interface{ ExitCode() int }
	var coded exitCoder
	if errors.As(err, &coded) {
		return FailureProcessExit
	}
	return FailureAssertion
}

func (state *Checkpoint) RecordTransaction(store Store, label, hash string, now time.Time) error {
	if label == "" || hash == "" {
		return errors.New("transaction label and hash are required")
	}
	if prior, exists := state.Transactions[label]; exists && prior != hash {
		return fmt.Errorf("transaction %s is already recorded as %s", label, prior)
	}
	if prepared, exists := state.PreparedTransactions[label]; exists && prepared.Hash != hash {
		return fmt.Errorf("transaction %s hash %s differs from prepared hash %s", label, hash, prepared.Hash)
	}
	next := *state
	next.Transactions = cloneStringMap(state.Transactions)
	next.Transactions[label] = hash
	next.UpdatedAt = now.UTC()
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func (state *Checkpoint) RecordPreparedTransaction(store Store, label, hash, raw string, now time.Time) error {
	prepared := PreparedTransaction{Hash: hash, Raw: raw}
	if label == "" || validatePreparedTransaction(prepared) != nil {
		return errors.New("prepared transaction label, hash, or raw bytes are invalid")
	}
	if prior, exists := state.PreparedTransactions[label]; exists && prior != prepared {
		return fmt.Errorf("prepared transaction %s is already journaled with different bytes", label)
	}
	if submitted, exists := state.Transactions[label]; exists && submitted != hash {
		return fmt.Errorf("prepared transaction %s hash %s differs from submitted hash %s", label, hash, submitted)
	}
	next := *state
	next.PreparedTransactions = clonePreparedTransactionMap(state.PreparedTransactions)
	next.PreparedTransactions[label] = prepared
	next.UpdatedAt = now.UTC()
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func (state *Checkpoint) RecordManagedTransactionIntent(store Store, label string, intent ManagedTransactionIntent, now time.Time) error {
	if err := validateManagedTransactionIntent(label, intent); err != nil {
		return fmt.Errorf("managed transaction intent is invalid: %w", err)
	}
	if prior, exists := state.ManagedTransactionIntents[label]; exists && !reflect.DeepEqual(prior, intent) {
		return fmt.Errorf("managed transaction intent %s is already journaled with different arguments", label)
	}
	next := *state
	next.ManagedTransactionIntents = cloneManagedTransactionIntentMap(state.ManagedTransactionIntents)
	next.ManagedTransactionIntents[label] = cloneManagedTransactionIntent(intent)
	next.UpdatedAt = now.UTC()
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func (state *Checkpoint) RecordManagedTransactionInitialAttempt(store Store, label string, now time.Time) error {
	intent, exists := state.ManagedTransactionIntents[label]
	if !exists {
		return fmt.Errorf("managed transaction intent %s is not journaled", label)
	}
	started := now.UTC()
	if started.IsZero() || started.Before(intent.PreparedAt) {
		return errors.New("managed transaction initial-attempt time is invalid")
	}
	if prior, exists := state.ManagedTransactionInitialAttempts[label]; exists {
		return fmt.Errorf("managed transaction initial attempt %s already started at %s", label, prior)
	}
	if _, submitted := state.Transactions[label]; submitted {
		return fmt.Errorf("managed transaction %s is already submitted", label)
	}
	next := *state
	next.ManagedTransactionInitialAttempts = cloneTimeMap(state.ManagedTransactionInitialAttempts)
	next.ManagedTransactionInitialAttempts[label] = started
	next.UpdatedAt = started
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func (state *Checkpoint) RecordManagedTransactionResubmit(store Store, label string, now time.Time) error {
	if _, exists := state.ManagedTransactionIntents[label]; !exists {
		return fmt.Errorf("managed transaction intent %s is not journaled", label)
	}
	initial, exists := state.ManagedTransactionInitialAttempts[label]
	if !exists {
		return fmt.Errorf("managed transaction initial attempt %s is not journaled", label)
	}
	started := now.UTC()
	if started.IsZero() || started.Before(initial) {
		return errors.New("managed transaction resubmit time is invalid")
	}
	if prior, exists := state.ManagedTransactionResubmits[label]; exists {
		return fmt.Errorf("managed transaction resubmit %s already started at %s", label, prior)
	}
	if _, submitted := state.Transactions[label]; submitted {
		return fmt.Errorf("managed transaction %s is already submitted", label)
	}
	next := *state
	next.ManagedTransactionResubmits = cloneTimeMap(state.ManagedTransactionResubmits)
	next.ManagedTransactionResubmits[label] = started
	next.UpdatedAt = started
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

// RecordSystemObservation durably stores immutable assertion input or a
// completed assertion milestone. Values are JSON so callers can evolve their
// own typed evidence without widening the checkpoint schema for every check.
func (state *Checkpoint) RecordSystemObservation(store Store, label, value string, now time.Time) error {
	if label == "" || value == "" || !json.Valid([]byte(value)) {
		return errors.New("system observation label or JSON value is invalid")
	}
	if prior, exists := state.SystemObservations[label]; exists {
		if prior != value {
			return fmt.Errorf("system observation %s is already recorded with different evidence", label)
		}
		return nil
	}
	next := *state
	next.SystemObservations = cloneStringMap(state.SystemObservations)
	next.SystemObservations[label] = value
	next.UpdatedAt = now.UTC()
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func cloneStringMap(values map[string]string) map[string]string {
	cloned := make(map[string]string, len(values)+1)
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func clonePreparedTransactionMap(values map[string]PreparedTransaction) map[string]PreparedTransaction {
	cloned := make(map[string]PreparedTransaction, len(values)+1)
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneManagedTransactionIntentMap(values map[string]ManagedTransactionIntent) map[string]ManagedTransactionIntent {
	cloned := make(map[string]ManagedTransactionIntent, len(values)+1)
	for key, value := range values {
		cloned[key] = cloneManagedTransactionIntent(value)
	}
	return cloned
}

func cloneManagedTransactionIntent(value ManagedTransactionIntent) ManagedTransactionIntent {
	value.AccessList = slices.Clone(value.AccessList)
	for index := range value.AccessList {
		value.AccessList[index].StorageKeys = slices.Clone(value.AccessList[index].StorageKeys)
	}
	return value
}

func cloneTimeMap(values map[string]time.Time) map[string]time.Time {
	cloned := make(map[string]time.Time, len(values)+1)
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func cloneTemporaryServiceCreationIntentMap(values map[string]TemporaryServiceCreationIntent) map[string]TemporaryServiceCreationIntent {
	cloned := make(map[string]TemporaryServiceCreationIntent, len(values)+1)
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

// RecordTemporaryServiceCreationIntent durably journals the immutable
// service-add identity before the external Kurtosis mutation. Repeating the
// exact write is idempotent; changing any field is rejected.
func (state *Checkpoint) RecordTemporaryServiceCreationIntent(store Store, intent TemporaryServiceCreationIntent, now time.Time) error {
	if intent.Name == "" || !uuidPattern.MatchString(intent.EnclaveUUID) || !digestPattern.MatchString(intent.ConfigDigest) || !digestPattern.MatchString(intent.Marker) || intent.PreparedAt.IsZero() {
		return errors.New("temporary service creation intent is invalid")
	}
	intent.PreparedAt = intent.PreparedAt.UTC()
	if prior, exists := state.TemporaryServiceCreationIntents[intent.Name]; exists {
		if prior != intent {
			return fmt.Errorf("temporary service %s already has a different creation intent", intent.Name)
		}
		return nil
	}
	next := *state
	next.TemporaryServiceCreationIntents = cloneTemporaryServiceCreationIntentMap(state.TemporaryServiceCreationIntents)
	next.TemporaryServiceCreationIntents[intent.Name] = intent
	next.UpdatedAt = now.UTC()
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func (state *Checkpoint) RecordTemporaryService(store Store, name, uuid string, now time.Time) error {
	if name == "" || !uuidPattern.MatchString(uuid) {
		return errors.New("temporary service name or UUID is invalid")
	}
	if prior, exists := state.TemporaryServices[name]; exists && prior != uuid {
		return fmt.Errorf("temporary service %s is already recorded as %s", name, prior)
	}
	next := *state
	next.TemporaryServices = cloneStringMap(state.TemporaryServices)
	next.TemporaryServices[name] = uuid
	next.UpdatedAt = now.UTC()
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func (state *Checkpoint) RecordServiceTransition(store Store, transition ServiceTransition) error {
	if transition.Phase == "" || transition.ServiceName == "" || !uuidPattern.MatchString(transition.ServiceUUID) || transition.State == "" || transition.At.IsZero() {
		return errors.New("service transition is incomplete or invalid")
	}
	if len(state.ServiceTransitions) > 0 {
		last := state.ServiceTransitions[len(state.ServiceTransitions)-1]
		if last.Phase == transition.Phase && last.ServiceName == transition.ServiceName && last.State == transition.State {
			return errors.New("duplicate consecutive service transition")
		}
	}
	next := *state
	next.ServiceTransitions = append(slices.Clone(state.ServiceTransitions), transition)
	next.UpdatedAt = transition.At.UTC()
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func (state *Checkpoint) RecordEndpointRefresh(store Store, refresh EndpointRefresh) error {
	if refresh.Phase == "" || refresh.ServiceName == "" || !uuidPattern.MatchString(refresh.ServiceUUID) || refresh.Kind == "" || refresh.Current == "" || refresh.At.IsZero() {
		return errors.New("endpoint refresh is incomplete or invalid")
	}
	next := *state
	next.EndpointRefreshes = append(slices.Clone(state.EndpointRefreshes), refresh)
	next.UpdatedAt = refresh.At.UTC()
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func (state *Checkpoint) PrepareResume(store Store, sourceSHA, configurationDigest, treeID, reconciledStage string, now time.Time) error {
	if state.Status == StatusCompleteClean || state.Status == StatusCompleteAfterResume || state.Status == StatusCleanedAfterFailure {
		return fmt.Errorf("checkpoint is terminal: %s", state.Status)
	}
	if state.SourceSHA != sourceSHA {
		return fmt.Errorf("checkpoint source %s does not match checkout %s", state.SourceSHA, sourceSHA)
	}
	if !digestPattern.MatchString(configurationDigest) || !digestPattern.MatchString(treeID) {
		return errors.New("resume configuration digest or tree ID is invalid")
	}
	if state.ConfigurationDigest != "" && state.ConfigurationDigest != configurationDigest {
		return fmt.Errorf("checkpoint configuration %s does not match requested configuration %s", state.ConfigurationDigest, configurationDigest)
	}
	next := *state
	next.ConfigurationDigest = configurationDigest
	timestamp := now.UTC()
	next.Attempts = slices.Clone(state.Attempts)
	if len(next.Attempts) > 0 {
		last := &next.Attempts[len(next.Attempts)-1]
		if last.FinishedAt == nil {
			last.FinishedAt = &timestamp
			last.ExitCode = pointerTo(255)
			last.FailureCategory = FailureCancellation
			last.FailureMessage = "previous runner stopped before recording the stage result"
		}
	}
	next.ResumeTreeIDs = append(slices.Clone(state.ResumeTreeIDs), treeID)
	next.ResumeHistory = append(slices.Clone(state.ResumeHistory), ResumeEvent{
		At: timestamp, TreeID: treeID, ConfigurationDigest: configurationDigest, ReconciledStage: reconciledStage,
	})
	next.Resumed = true
	next.Status = StatusRunning
	next.CurrentStage = nil
	next.UpdatedAt = timestamp
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func (state *Checkpoint) MarkComplete(store Store, now time.Time) error {
	if len(state.Completed) != len(store.StageOrder) {
		return fmt.Errorf("cannot complete lifecycle with %d of %d stages", len(state.Completed), len(store.StageOrder))
	}
	next := *state
	next.CurrentStage = nil
	next.FailureCategory = FailureNone
	next.FailureMessage = ""
	if state.Resumed || hasFailedOrRetriedAttempt(state.Attempts) {
		next.Status = StatusCompleteAfterResume
	} else {
		next.Status = StatusCompleteClean
	}
	next.UpdatedAt = now.UTC()
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func (state *Checkpoint) MarkCleanedAfterFailure(store Store, now time.Time) error {
	if state.Status == StatusCompleteClean || state.Status == StatusCompleteAfterResume || state.Status == StatusCleanedAfterFailure {
		return fmt.Errorf("cannot mark terminal checkpoint cleaned: %s", state.Status)
	}
	next := *state
	next.Status = StatusCleanedAfterFailure
	next.UpdatedAt = now.UTC()
	if err := store.Save(next); err != nil {
		return err
	}
	*state = next
	return nil
}

func hasFailedOrRetriedAttempt(attempts []Attempt) bool {
	counts := make(map[string]int)
	for _, attempt := range attempts {
		counts[attempt.Stage]++
		if attempt.ExitCode != nil && *attempt.ExitCode != 0 {
			return true
		}
	}
	for _, count := range counts {
		if count > 1 {
			return true
		}
	}
	return false
}
