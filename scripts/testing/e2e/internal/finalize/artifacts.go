// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package finalize

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/config"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
)

const (
	interruptedStageName = "interrupted-lifecycle"
	standaloneStageName  = "standalone-finalize"
)

// ArtifactOptions supplies the result of an independent cleanup attempt. This
// path is intentionally separate from Run because the harness also calls Run
// during a clean, successful lifecycle.
type ArtifactOptions struct {
	OwnershipPath string
	Writer        *report.Writer
	Result        Result
	FinalizeError error
	Now           func() time.Time
}

type StandaloneArtifact struct {
	Schema          int           `json:"schema"`
	At              time.Time     `json:"at"`
	CleanupStatus   string        `json:"cleanup_status"`
	LifecycleStatus report.Status `json:"lifecycle_status"`
	Result          Result        `json:"result"`
	Error           string        `json:"error,omitempty"`
}

type checkpointRepair struct {
	Exists         bool
	OriginalStatus lifecycle.Status
	Status         lifecycle.Status
	Terminalized   bool
	AttemptClosed  bool
	StageOrder     []string
	State          lifecycle.Checkpoint
}

// CompleteStandaloneArtifacts repairs the machine-readable evidence bundle
// after an independent finalizer invocation. It never promotes an incomplete
// run to passed, and it never replaces an existing primary reason.
func CompleteStandaloneArtifacts(options ArtifactOptions) error {
	if options.Writer == nil || options.OwnershipPath == "" {
		return errors.New("standalone artifacts require writer and ownership path")
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	finishedAt := now().UTC()
	record, err := lifecycle.LoadOwnership(options.OwnershipPath)
	if err != nil {
		return fmt.Errorf("load ownership for standalone artifacts: %w", err)
	}
	timelineRecorder, err := resumeStandaloneTimeline(options.Writer.Layout().Timeline, record.RunID, finishedAt)
	if err != nil {
		return fmt.Errorf("load existing timeline: %w", err)
	}

	results, err := report.LoadResults(options.Writer.Layout().Results)
	resultsExist := err == nil
	if errors.Is(err, os.ErrNotExist) {
		results = report.Results{}
		err = nil
	}
	if err != nil {
		return fmt.Errorf("load existing results: %w", err)
	}
	if resultsExist && results.RunID != record.RunID {
		return fmt.Errorf("existing results run ID %q does not match ownership run ID %q", results.RunID, record.RunID)
	}
	cleanupComplete := options.Result.Destroyed || options.Result.AlreadyClean
	repair, checkpointErr := inspectCheckpoint(options.Writer.Layout().Checkpoint, record.RunID)
	if err := options.Writer.InvalidateManifest(); err != nil {
		return err
	}
	if checkpointErr == nil && cleanupComplete {
		repair, checkpointErr = terminalizeCleanedCheckpoint(options.Writer.Layout().Checkpoint, repair, finishedAt)
	}
	checkpointValid := checkpointErr == nil && repair.Exists
	checkpointSuccessful := checkpointValid && successfulCheckpointStatus(repair.Status)
	existingPassed := resultsExist && results.Status == report.StatusPassed
	passedEvidence := existingPassed
	if repair.Exists {
		passedEvidence = checkpointSuccessful && (existingPassed || !resultsExist)
	}
	preservePassed := passedEvidence && options.FinalizeError == nil && cleanupComplete

	reason, reasonExists, reasonErr := readOptionalJSON[report.DiagnosticReason](options.Writer.Layout().Reason)
	if reasonErr != nil {
		// Existing primary evidence is immutable even if a future schema cannot
		// be decoded here. Use a synthetic in-memory reason for aggregate output.
		reason = standaloneReason(options.Result, options.FinalizeError, finishedAt)
		reason.Details["reason_read_error"] = reasonErr.Error()
	}
	if !resultsExist {
		results = report.Results{
			RunID: record.RunID, StartedAt: record.CreatedAt.UTC(),
			Stages: []report.StageResult{}, Suites: []report.SuiteResult{},
		}
	}
	if checkpointValid && (!resultsExist || results.Status != report.StatusPassed) {
		results.Stages = mergeMissingStages(results.Stages, checkpointStageResults(repair))
	}
	if preservePassed && !resultsExist {
		results.Status = report.StatusPassed
		results.FinishedAt = finishedAt
		results.DurationMillis = nonnegativeDurationMillis(results.StartedAt, finishedAt)
	}
	if !reasonExists && !preservePassed {
		reason = standaloneReason(options.Result, options.FinalizeError, finishedAt)
		if checkpointValid && repair.State.FailureMessage != "" {
			reason.Category = reportFailureCategory(repair.State.FailureCategory)
			reason.Message = repair.State.FailureMessage
			if repair.State.CurrentStage != nil {
				reason.Stage = *repair.State.CurrentStage
			}
		}
		if checkpointErr != nil {
			reason.Details["checkpoint_error"] = checkpointErr.Error()
		}
	}
	if !preservePassed {
		if results.Status != report.StatusCanceled {
			results.Status = report.StatusFailed
		}
		if results.StartedAt.IsZero() {
			results.StartedAt = record.CreatedAt.UTC()
		}
		results.FinishedAt = finishedAt
		results.DurationMillis = nonnegativeDurationMillis(results.StartedAt, finishedAt)
		if !hasFailureEvidence(results) && options.FinalizeError == nil {
			primary := reason
			if primary.Message == "" {
				primary = standaloneReason(options.Result, nil, finishedAt)
			}
			results.Stages = upsertStage(results.Stages, report.StageResult{
				Name: interruptedStageName, Attempt: 1, Status: report.StatusFailed,
				StartedAt: results.StartedAt, FinishedAt: finishedAt,
				DurationMillis: results.DurationMillis, FailureCategory: primary.Category,
				Message: primary.Message,
			})
		}
		results.Stages = upsertStage(results.Stages, standaloneStageResult(options.Result, options.FinalizeError, finishedAt))
	}
	artifact := standaloneArtifact(options.Result, options.FinalizeError, results.Status, finishedAt)
	standaloneStage := standaloneStageResult(options.Result, options.FinalizeError, finishedAt)
	timelineFields := map[string]any{
		"cleanup_status":             artifact.CleanupStatus,
		"lifecycle_status":           results.Status,
		"checkpoint_exists":          repair.Exists,
		"checkpoint_terminalized":    repair.Terminalized,
		"interrupted_attempt_closed": repair.AttemptClosed,
	}
	if repair.Exists {
		timelineFields["checkpoint_status"] = repair.Status
	}
	if checkpointErr != nil {
		timelineFields["checkpoint_error"] = checkpointErr.Error()
	}
	timelineRecorder.Record(report.TimelineEvent{
		At: finishedAt, Kind: standaloneStageName, Status: standaloneTimelineStatus(options.Result, options.FinalizeError),
		Message: standaloneStage.Message, Fields: timelineFields,
	})

	var writeErrors []error
	writeErrors = append(writeErrors, checkpointErr)
	if !reasonExists && !preservePassed {
		writeErrors = append(writeErrors, options.Writer.WriteReason(reason))
	}
	writeErrors = append(writeErrors, options.Writer.WriteFinalize(artifact))
	writeErrors = append(writeErrors, options.Writer.WriteResults(results))
	writeErrors = append(writeErrors, options.Writer.WriteJUnit(results))
	writeErrors = append(writeErrors, options.Writer.WriteTimeline(timelineRecorder.Snapshot()))
	metadata := standaloneManifestMetadata(options.Writer, record, finishedAt)
	_, manifestErr := options.Writer.WriteManifest(metadata)
	writeErrors = append(writeErrors, manifestErr)
	return errors.Join(writeErrors...)
}

func standaloneArtifact(result Result, finalizeErr error, lifecycleStatus report.Status, now time.Time) StandaloneArtifact {
	artifact := StandaloneArtifact{
		Schema: report.SchemaVersion, At: now, CleanupStatus: "preserved", LifecycleStatus: lifecycleStatus, Result: result,
	}
	switch {
	case finalizeErr != nil && result.Destroyed:
		artifact.CleanupStatus = "destroyed_with_errors"
		artifact.Error = finalizeErr.Error()
	case finalizeErr != nil && result.AlreadyClean:
		artifact.CleanupStatus = "already_clean_with_errors"
		artifact.Error = finalizeErr.Error()
	case finalizeErr != nil:
		artifact.CleanupStatus = "failed"
		artifact.Error = finalizeErr.Error()
	case result.Destroyed:
		artifact.CleanupStatus = "destroyed"
	case result.AlreadyClean:
		artifact.CleanupStatus = "already_clean"
	case result.Preserved:
		artifact.CleanupStatus = "preserved"
	default:
		artifact.CleanupStatus = "not_completed"
	}
	return artifact
}

func standaloneReason(result Result, finalizeErr error, now time.Time) report.DiagnosticReason {
	reason := report.DiagnosticReason{
		Category: report.FailureInfrastructure,
		At:       now,
		Message:  "lifecycle ended before a durable root-cause reason was recorded; standalone finalization recovered its owned resources",
		Details:  map[string]string{"standalone_finalize": "true"},
	}
	if result.Preserved && finalizeErr == nil {
		reason.Message = "lifecycle ended before a durable root-cause reason was recorded; standalone finalization preserved its owned resources"
	}
	if finalizeErr != nil {
		reason.Message = "standalone finalization encountered an error after the lifecycle ended without a durable root-cause reason: " + finalizeErr.Error()
		reason.Details["finalize_error"] = finalizeErr.Error()
		if !result.Destroyed && !result.AlreadyClean {
			reason.Category = report.FailureCleanup
		}
	}
	return reason
}

func standaloneStageResult(result Result, finalizeErr error, now time.Time) report.StageResult {
	stage := report.StageResult{
		Name: standaloneStageName, Attempt: 1, StartedAt: now, FinishedAt: now,
		Status: report.StatusPassed, Message: "standalone finalization completed owned-enclave cleanup",
	}
	if result.AlreadyClean {
		stage.Message = "owned enclave was already clean"
	}
	if result.Preserved && finalizeErr == nil {
		stage.Status = report.StatusSkipped
		stage.Message = "owned enclave remains preserved by policy"
	}
	if finalizeErr != nil {
		stage.Status = report.StatusFailed
		stage.FailureCategory = report.FailureInfrastructure
		stage.Message = finalizeErr.Error()
		if !result.Destroyed && !result.AlreadyClean {
			stage.FailureCategory = report.FailureCleanup
		}
	}
	return stage
}

func standaloneTimelineStatus(result Result, finalizeErr error) report.Status {
	switch {
	case finalizeErr != nil:
		return report.StatusFailed
	case result.Preserved:
		return report.StatusSkipped
	case result.Destroyed || result.AlreadyClean:
		return report.StatusPassed
	default:
		return report.StatusFailed
	}
}

func resumeStandaloneTimeline(path, runID string, now time.Time) (*report.TimelineRecorder, error) {
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		return report.NewTimelineRecorder(runID, func() time.Time { return now }), nil
	} else if err != nil {
		return nil, err
	}
	timeline, err := report.LoadTimeline(path)
	if err != nil {
		return nil, err
	}
	if timeline.RunID != runID {
		return nil, fmt.Errorf("timeline run ID %q does not match ownership run ID %q", timeline.RunID, runID)
	}
	return report.ResumeTimelineRecorder(timeline, func() time.Time { return now })
}

func inspectCheckpoint(path, runID string) (checkpointRepair, error) {
	raw, exists, err := readOptionalJSON[lifecycle.Checkpoint](path)
	repair := checkpointRepair{Exists: exists, OriginalStatus: raw.Status, Status: raw.Status}
	if err != nil {
		return repair, fmt.Errorf("decode checkpoint: %w", err)
	}
	if !exists {
		return repair, nil
	}
	stageOrder, err := inferCheckpointStageOrder(raw)
	if err != nil {
		return repair, fmt.Errorf("infer checkpoint stage order: %w", err)
	}
	store := lifecycle.Store{Path: path, StageOrder: stageOrder}
	state, err := store.Load()
	if err != nil {
		return repair, err
	}
	if state.RunID != "" && state.RunID != runID {
		return repair, fmt.Errorf("checkpoint run ID %q does not match ownership run ID %q", state.RunID, runID)
	}
	repair.StageOrder = stageOrder
	repair.State = state
	repair.Status = state.Status
	return repair, nil
}

func inferCheckpointStageOrder(state lifecycle.Checkpoint) ([]string, error) {
	order := make([]string, 0, len(state.Completed)+1)
	seen := make(map[string]struct{}, len(state.Completed)+1)
	expectedIndex := 0
	for index, attempt := range state.Attempts {
		if attempt.Stage == "" {
			return nil, fmt.Errorf("attempt %d has an empty stage", index)
		}
		if expectedIndex == len(order) {
			if _, exists := seen[attempt.Stage]; exists {
				return nil, fmt.Errorf("attempt %d revisits completed stage %q", index, attempt.Stage)
			}
			order = append(order, attempt.Stage)
			seen[attempt.Stage] = struct{}{}
		}
		if expectedIndex >= len(order) || attempt.Stage != order[expectedIndex] {
			return nil, fmt.Errorf("attempt %d for stage %q is out of order", index, attempt.Stage)
		}
		if attempt.FinishedAt != nil && attempt.ExitCode != nil && *attempt.ExitCode == 0 {
			expectedIndex++
		}
	}
	if expectedIndex != len(state.Completed) {
		return nil, fmt.Errorf("%d successful attempts do not match %d completed stages", expectedIndex, len(state.Completed))
	}
	for index, completed := range state.Completed {
		if index >= len(order) || completed != order[index] {
			return nil, errors.New("completed checkpoint stages are not an exact inferred prefix")
		}
	}
	if state.CurrentStage != nil {
		if *state.CurrentStage == "" {
			return nil, errors.New("checkpoint current stage is empty")
		}
		if expectedIndex == len(order) {
			if _, exists := seen[*state.CurrentStage]; exists {
				return nil, fmt.Errorf("current stage %q revisits a completed stage", *state.CurrentStage)
			}
			order = append(order, *state.CurrentStage)
		} else if order[expectedIndex] != *state.CurrentStage {
			return nil, fmt.Errorf("current stage %q does not match inferred stage %q", *state.CurrentStage, order[expectedIndex])
		}
	}
	return order, nil
}

func terminalizeCleanedCheckpoint(path string, repair checkpointRepair, now time.Time) (checkpointRepair, error) {
	if !repair.Exists || terminalCheckpointStatus(repair.Status) {
		return repair, nil
	}
	state := repair.State
	attemptClosed := false
	if len(state.Attempts) != 0 {
		last := &state.Attempts[len(state.Attempts)-1]
		if last.FinishedAt == nil {
			finishedAt := now.UTC()
			exitCode := 255
			last.FinishedAt = &finishedAt
			last.ExitCode = &exitCode
			last.FailureCategory = lifecycle.FailureCancellation
			last.FailureMessage = "standalone finalization closed an interrupted attempt after runner termination"
			state.FailureCategory = last.FailureCategory
			state.FailureMessage = last.FailureMessage
			attemptClosed = true
		}
	}
	store := lifecycle.Store{Path: path, StageOrder: repair.StageOrder, Now: func() time.Time { return now }}
	if err := state.MarkCleanedAfterFailure(store, now); err != nil {
		return repair, fmt.Errorf("terminalize cleaned checkpoint: %w", err)
	}
	repair.State = state
	repair.Status = state.Status
	repair.Terminalized = true
	repair.AttemptClosed = attemptClosed
	return repair, nil
}

func terminalCheckpointStatus(status lifecycle.Status) bool {
	return successfulCheckpointStatus(status) || status == lifecycle.StatusCleanedAfterFailure
}

func successfulCheckpointStatus(status lifecycle.Status) bool {
	return status == lifecycle.StatusCompleteClean || status == lifecycle.StatusCompleteAfterResume
}

func checkpointStageResults(repair checkpointRepair) []report.StageResult {
	latest := make(map[string]lifecycle.Attempt, len(repair.StageOrder))
	for _, attempt := range repair.State.Attempts {
		latest[attempt.Stage] = attempt
	}
	results := make([]report.StageResult, 0, len(latest))
	for _, name := range repair.StageOrder {
		attempt, exists := latest[name]
		if !exists {
			continue
		}
		result := report.StageResult{
			Name: name, Attempt: attempt.Attempt, Status: report.StatusRunning,
			StartedAt: attempt.StartedAt.UTC(), Message: attempt.FailureMessage,
		}
		if attempt.FinishedAt != nil {
			result.FinishedAt = attempt.FinishedAt.UTC()
			result.DurationMillis = nonnegativeDurationMillis(result.StartedAt, result.FinishedAt)
			if attempt.ExitCode != nil && *attempt.ExitCode == 0 {
				result.Status = report.StatusPassed
			} else {
				result.FailureCategory = reportFailureCategory(attempt.FailureCategory)
				if result.FailureCategory == report.FailureCancellation {
					result.Status = report.StatusCanceled
				} else {
					result.Status = report.StatusFailed
				}
			}
		}
		results = append(results, result)
	}
	return results
}

func reportFailureCategory(category lifecycle.FailureCategory) report.FailureCategory {
	switch category {
	case lifecycle.FailureTimeout:
		return report.FailureTimeout
	case lifecycle.FailureCancellation:
		return report.FailureCancellation
	case lifecycle.FailureProcessExit:
		return report.FailureProcessExit
	case lifecycle.FailureSDK:
		return report.FailureSDK
	case lifecycle.FailureInfrastructure:
		return report.FailureInfrastructure
	case lifecycle.FailureCleanup:
		return report.FailureCleanup
	default:
		return report.FailureAssertion
	}
}

func mergeMissingStages(existing, additional []report.StageResult) []report.StageResult {
	seen := make(map[string]struct{}, len(existing))
	for _, stage := range existing {
		seen[stage.Name] = struct{}{}
	}
	for _, stage := range additional {
		if _, exists := seen[stage.Name]; exists {
			continue
		}
		existing = append(existing, stage)
		seen[stage.Name] = struct{}{}
	}
	return existing
}

func hasFailureEvidence(results report.Results) bool {
	for _, stage := range results.Stages {
		if stage.Status == report.StatusFailed || stage.Status == report.StatusCanceled {
			return true
		}
	}
	for _, suite := range results.Suites {
		if suite.Status == report.StatusFailed || suite.Status == report.StatusCanceled {
			return true
		}
	}
	return false
}

func upsertStage(stages []report.StageResult, stage report.StageResult) []report.StageResult {
	for index := range stages {
		if stages[index].Name == stage.Name {
			stages[index] = stage
			return stages
		}
	}
	return append(stages, stage)
}

func nonnegativeDurationMillis(started, finished time.Time) int64 {
	if started.IsZero() || finished.IsZero() || finished.Before(started) {
		return 0
	}
	return finished.Sub(started).Milliseconds()
}

func standaloneManifestMetadata(writer *report.Writer, record lifecycle.OwnershipRecord, now time.Time) report.ManifestMetadata {
	metadata := report.ManifestMetadata{RunID: record.RunID, GeneratedAt: now}
	if existing, ok, err := readOptionalJSON[report.Manifest](writer.Layout().Manifest); err == nil && ok && existing.RunID == record.RunID {
		metadata.SourceSHA = existing.SourceSHA
		metadata.ConfigurationDigest = existing.ConfigurationDigest
	}
	if effective, ok, err := readOptionalJSON[struct {
		RunID  string           `json:"run_id"`
		Config config.RunConfig `json:"config"`
	}](writer.Layout().EffectiveConfig); err == nil && ok && effective.RunID == record.RunID {
		metadata.SourceSHA = effective.Config.SourceSHA
		if digest, digestErr := effective.Config.Digest(); digestErr == nil {
			metadata.ConfigurationDigest = digest
		}
	}
	return metadata
}

func readOptionalJSON[T any](path string) (T, bool, error) {
	var value T
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return value, false, nil
	}
	if err != nil {
		return value, false, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(&value); err != nil {
		return value, true, err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return value, true, errors.New("JSON artifact contains trailing data")
	}
	return value, true, nil
}
