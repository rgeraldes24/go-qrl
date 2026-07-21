// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/config"
	finalizer "github.com/theQRL/go-qrl/scripts/testing/e2e/internal/finalize"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/source"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
)

type Outcome struct {
	RunID      string
	ResultsDir string
	Checkpoint string
	Ownership  string
	Status     lifecycle.Status
}

const (
	enclaveCreationTimeout = 5 * time.Minute
	enclaveCreationMinimum = 10 * time.Second
)

func RunOwned(ctx context.Context, runID string, runConfig config.RunConfig, dependencies Dependencies) (Outcome, error) {
	if err := dependencies.normalize(); err != nil {
		return Outcome{}, err
	}
	if runConfig.Mode != config.ModeRun {
		return Outcome{}, fmt.Errorf("owned lifecycle mode is %q, want run", runConfig.Mode)
	}
	if err := prepareConfig(&runConfig, dependencies.Now()); err != nil {
		return Outcome{}, err
	}
	writer, err := report.New(runConfig.ResultsDir)
	if err != nil {
		return Outcome{}, err
	}
	layout := writer.Layout()
	if runConfig.CheckpointPath == "" {
		runConfig.CheckpointPath = layout.Checkpoint
	}
	if runConfig.OwnershipPath == "" {
		runConfig.OwnershipPath = layout.Ownership
	}
	creationContext, cancelCreation, err := enclaveCreationContext(ctx, runConfig, dependencies.Now())
	if err != nil {
		return Outcome{}, err
	}
	defer cancelCreation()
	if _, err := lifecycle.NewOwnership(runConfig.OwnershipPath, runID, runConfig.EnclaveName, dependencies.Now()); err != nil {
		return Outcome{}, err
	}
	if record, err := lifecycle.LoadOwnership(runConfig.OwnershipPath); err == nil {
		_ = writer.WriteOwnership(record)
	}
	enclave, err := dependencies.Client.CreateEnclave(creationContext, runConfig.EnclaveName)
	cancelCreation()
	if err != nil {
		preserveErr := lifecycle.MarkOwnershipPreserved(runConfig.OwnershipPath, "enclave creation returned an error; ownership is ambiguous")
		if record, loadErr := lifecycle.LoadOwnership(runConfig.OwnershipPath); loadErr == nil {
			preserveErr = errors.Join(preserveErr, writer.WriteOwnership(record))
		} else {
			preserveErr = errors.Join(preserveErr, loadErr)
		}
		return Outcome{RunID: runID, ResultsDir: runConfig.ResultsDir, Checkpoint: runConfig.CheckpointPath, Ownership: runConfig.OwnershipPath}, errors.Join(err, preserveErr)
	}
	if _, err := dependencies.CaptureOwnershipUUID(runConfig.OwnershipPath, runConfig.EnclaveName, enclave.UUID); err != nil {
		captureErr := fmt.Errorf("capture enclave UUID: %w", err)
		preserveErr := lifecycle.MarkOwnershipPreserved(runConfig.OwnershipPath, "enclave was created but its UUID could not be durably captured; refusing automatic cleanup")
		if record, loadErr := lifecycle.LoadOwnership(runConfig.OwnershipPath); loadErr == nil {
			preserveErr = errors.Join(preserveErr, writer.WriteOwnership(record))
		}
		return Outcome{RunID: runID, ResultsDir: runConfig.ResultsDir, Checkpoint: runConfig.CheckpointPath, Ownership: runConfig.OwnershipPath}, errors.Join(captureErr, preserveErr)
	}
	if record, err := lifecycle.LoadOwnership(runConfig.OwnershipPath); err == nil {
		_ = writer.WriteOwnership(record)
	}
	enclave.Owned = true
	runtime := &Runtime{
		RunID: runID, Config: runConfig, Enclave: enclave, Writer: writer, Dependencies: dependencies,
		TopologySpec: topology.DefaultSpec(""), StartedAt: dependencies.Now(),
		Timeline: report.NewTimelineRecorder(runID, dependencies.Now),
	}
	stages := ownedStages(runtime)
	runtime.Store = lifecycle.Store{Path: runConfig.CheckpointPath, StageOrder: stageOrder(stages), Now: dependencies.Now}
	if err := initializeCheckpoint(ctx, runtime, stages); err != nil {
		preserveErr := lifecycle.MarkOwnershipPreserved(runConfig.OwnershipPath, "checkpoint initialization failed after enclave creation")
		return outcome(runtime, lifecycle.StatusFailed), errors.Join(err, preserveErr)
	}
	if err := runtime.writeEffectiveConfiguration(); err != nil {
		preserveErr := lifecycle.MarkOwnershipPreserved(runConfig.OwnershipPath, "effective configuration could not be persisted")
		return outcome(runtime, lifecycle.StatusFailed), errors.Join(err, preserveErr)
	}
	return execute(ctx, runtime, stages, false)
}

func TestBorrowed(ctx context.Context, runID string, runConfig config.RunConfig, dependencies Dependencies) (Outcome, error) {
	if err := dependencies.normalize(); err != nil {
		return Outcome{}, err
	}
	if runConfig.Mode != config.ModeTest {
		return Outcome{}, fmt.Errorf("borrowed lifecycle mode is %q, want test", runConfig.Mode)
	}
	if err := prepareConfig(&runConfig, dependencies.Now()); err != nil {
		return Outcome{}, err
	}
	writer, err := report.New(runConfig.ResultsDir)
	if err != nil {
		return Outcome{}, err
	}
	if runConfig.CheckpointPath == "" {
		runConfig.CheckpointPath = writer.Layout().Checkpoint
	}
	enclave, err := dependencies.Client.GetEnclave(ctx, runConfig.EnclaveIdentifier)
	if err != nil {
		return Outcome{}, err
	}
	enclave.Owned = false
	runtime := &Runtime{
		RunID: runID, Config: runConfig, Enclave: enclave, Writer: writer, Dependencies: dependencies,
		TopologySpec: topology.DefaultSpec(""), StartedAt: dependencies.Now(),
		Timeline: report.NewTimelineRecorder(runID, dependencies.Now),
	}
	stages := borrowedStages(runtime, runConfig.AllowDisruptive)
	runtime.Store = lifecycle.Store{Path: runConfig.CheckpointPath, StageOrder: stageOrder(stages), Now: dependencies.Now}
	if err := initializeCheckpoint(ctx, runtime, stages); err != nil {
		return outcome(runtime, lifecycle.StatusFailed), err
	}
	if err := runtime.writeEffectiveConfiguration(); err != nil {
		return outcome(runtime, lifecycle.StatusFailed), err
	}
	return execute(ctx, runtime, stages, false)
}

type ResumeOptions struct {
	CheckpointPath string
	SourceSHA      string
	RepoRoot       string
	GlobalDeadline time.Time
	TreeID         string
}

func Resume(ctx context.Context, options ResumeOptions, dependencies Dependencies) (Outcome, error) {
	if err := dependencies.normalize(); err != nil {
		return Outcome{}, err
	}
	if options.CheckpointPath == "" {
		return Outcome{}, errors.New("resume requires a checkpoint path")
	}
	resultsDir := filepath.Dir(options.CheckpointPath)
	writer, err := report.New(resultsDir)
	if err != nil {
		return Outcome{}, err
	}
	effective, err := loadEffectiveConfiguration(writer.Layout().EffectiveConfig)
	if err != nil {
		return Outcome{}, err
	}
	runConfig := effective.Config
	runConfig.Mode = config.ModeResume
	runConfig.CheckpointPath = options.CheckpointPath
	if options.RepoRoot != "" {
		runConfig.RepoRoot = options.RepoRoot
	}
	if !options.GlobalDeadline.IsZero() {
		runConfig.GlobalDeadline = options.GlobalDeadline
	}
	if options.SourceSHA != "" {
		runConfig.SourceSHA = options.SourceSHA
	}
	if err := prepareConfig(&runConfig, dependencies.Now()); err != nil {
		return Outcome{}, err
	}
	runtime := &Runtime{
		RunID: effective.RunID, Config: runConfig, Writer: writer, Dependencies: dependencies,
		Preparation: effective.Preparation, TopologySpec: effective.TopologySpec, StartedAt: dependencies.Now(),
		Timeline: report.NewTimelineRecorder(effective.RunID, dependencies.Now),
	}
	owned := effective.Config.Mode == config.ModeRun
	var stages []lifecycle.Stage
	// Enclave identity must be loaded before stage callbacks are built.
	if owned {
		stages = ownedStages(runtime)
	} else {
		stages = borrowedStages(runtime, effective.Config.AllowDisruptive)
	}
	runtime.Store = lifecycle.Store{Path: options.CheckpointPath, StageOrder: stageOrder(stages), Now: dependencies.Now}
	state, err := runtime.Store.Load()
	if err != nil {
		return Outcome{}, err
	}
	runtime.Enclave = state.Enclave
	runtime.Enclave.Owned = owned
	runtime.StartedAt = state.CreatedAt
	if previous, loadErr := report.LoadResults(writer.Layout().Results); loadErr == nil {
		if previous.RunID != runtime.RunID {
			return outcome(runtime, state.Status), errors.New("persisted results run ID does not match effective configuration")
		}
		runtime.restoreSuiteEvidence(previous)
		if !previous.StartedAt.IsZero() {
			runtime.StartedAt = previous.StartedAt
		}
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return outcome(runtime, state.Status), fmt.Errorf("restore prior results: %w", loadErr)
	}
	// A stage success is checkpointed before OnAttemptFinished publishes its
	// aggregate evidence. If the runner is interrupted in that window, resume
	// skips the completed stage, so repair stale active failures while both the
	// checkpoint and prior results are available.
	for _, completedStage := range state.Completed {
		runtime.retireFailedSuiteResults(completedStage)
	}
	if previous, loadErr := report.LoadTimeline(writer.Layout().Timeline); loadErr == nil {
		if previous.RunID != runtime.RunID {
			return outcome(runtime, state.Status), errors.New("persisted timeline run ID does not match effective configuration")
		}
		runtime.Timeline, err = report.ResumeTimelineRecorder(previous, dependencies.Now)
		if err != nil {
			return outcome(runtime, state.Status), fmt.Errorf("restore prior timeline: %w", err)
		}
	} else if !errors.Is(loadErr, os.ErrNotExist) {
		return outcome(runtime, state.Status), fmt.Errorf("restore prior timeline: %w", loadErr)
	}
	var ownership lifecycle.OwnershipRecord
	if owned {
		ownership, err = lifecycle.LoadOwnership(writer.Layout().Ownership)
		if err != nil || ownership.UUID == nil || *ownership.UUID != runtime.Enclave.UUID || ownership.RequestedName != runtime.Enclave.Name {
			return outcome(runtime, state.Status), errors.Join(errors.New("resume ownership does not match checkpoint enclave"), err)
		}
	}
	cleanupCanResumeWithoutEnclave := owned && (ownership.DestroyRequestedAt != nil || ownership.DestroyedAt != nil)
	if cleanupCanResumeWithoutEnclave {
		if len(state.Completed) != len(stages) || state.CurrentStage != nil {
			return outcome(runtime, state.Status), errors.New("resume ownership records cleanup in progress before every stage completed")
		}
	} else {
		current, err := dependencies.Client.GetEnclave(ctx, runtime.Enclave.UUID)
		if err != nil || current.UUID != runtime.Enclave.UUID || current.Name != runtime.Enclave.Name {
			return outcome(runtime, state.Status), errors.Join(errors.New("resume enclave identity is unavailable or changed"), err)
		}
	}
	if err := runtime.restoreArtifacts(); err != nil {
		return outcome(runtime, state.Status), err
	}
	digest, err := runtime.Config.Digest()
	if err != nil {
		return outcome(runtime, state.Status), err
	}
	treeID := options.TreeID
	if treeID == "" {
		treeID, err = source.TreeID(ctx, nil, runtime.Config.RepoRoot)
		if err != nil {
			return outcome(runtime, state.Status), err
		}
	}
	failedStage := ""
	if state.CurrentStage != nil {
		failedStage = *state.CurrentStage
	}
	if err := runtime.Writer.InvalidateManifest(); err != nil {
		return outcome(runtime, state.Status), err
	}
	lock, err := runtime.Store.Acquire(true)
	if err != nil {
		return outcome(runtime, state.Status), err
	}
	defer lock.Close()
	if err := state.PrepareResume(runtime.Store, runtime.Config.SourceSHA, digest, treeID, failedStage, dependencies.Now()); err != nil {
		return outcome(runtime, state.Status), err
	}
	return executeLocked(ctx, runtime, stages, true)
}

func initializeCheckpoint(ctx context.Context, runtime *Runtime, stages []lifecycle.Stage) error {
	digest, err := runtime.Config.Digest()
	if err != nil {
		return err
	}
	treeID, err := source.TreeID(ctx, nil, runtime.Config.RepoRoot)
	if err != nil {
		return err
	}
	state := lifecycle.NewCheckpoint(runtime.RunID, runtime.Config.SourceSHA, digest, runtime.Config.ResultsDir, treeID, runtime.Enclave, runtime.Dependencies.Now())
	return runtime.Store.Create(state)
}

func execute(ctx context.Context, runtime *Runtime, stages []lifecycle.Stage, resumed bool) (Outcome, error) {
	lock, err := runtime.Store.Acquire(resumed)
	if err != nil {
		return outcome(runtime, lifecycle.StatusFailed), err
	}
	defer lock.Close()
	return executeLocked(ctx, runtime, stages, resumed)
}

func executeLocked(ctx context.Context, runtime *Runtime, stages []lifecycle.Stage, resumed bool) (Outcome, error) {
	runner := lifecycle.Runner{
		Store: runtime.Store, Stages: stages, GlobalDeadline: runtime.Config.GlobalDeadline,
		CleanupReserve: runtime.Config.CleanupReserve, AllowDisruptive: runtime.Enclave.Owned || runtime.Config.AllowDisruptive,
		Now: runtime.Dependencies.Now, Classify: classifyFailure,
		OnAttemptFinished: func(attempt lifecycle.Attempt) error { return runtime.writeProgress(stages, attempt) },
	}
	environment := &lifecycle.RunEnvironment{Enclave: runtime.Enclave, Values: map[string]any{"runtime": runtime}}
	runErr := runner.Run(ctx, environment)
	if runErr != nil {
		return runtime.fail(ctx, stages, runErr)
	}
	if !runtime.Enclave.Owned {
		state, err := runtime.Store.Load()
		if err == nil {
			err = state.MarkComplete(runtime.Store, runtime.Dependencies.Now())
		}
		if err != nil {
			return runtime.fail(ctx, stages, err)
		}
		if state, loadErr := runtime.Store.Load(); loadErr == nil {
			return runtime.finish(stages, state.Status, nil)
		}
		return runtime.finish(stages, lifecycle.StatusCompleteClean, nil)
	}
	finalizeContext, cancelFinalize := runtime.cleanupContext(ctx)
	finalizeResult, finalizeErr := finalizer.Run(finalizeContext, finalizer.Options{
		OwnershipPath: runtime.Writer.Layout().Ownership, Writer: runtime.Writer,
		Client: runtime.Dependencies.Client, Dumper: runtime.Dependencies.Dumper,
		DestroyPreserved: true, Now: runtime.Dependencies.Now,
	})
	cancelFinalize()
	if finalizeErr != nil {
		var checkpointErr error
		if finalizeResult.Destroyed {
			if state, loadErr := runtime.Store.Load(); loadErr == nil {
				checkpointErr = state.MarkCleanedAfterFailure(runtime.Store, runtime.Dependencies.Now())
			} else {
				checkpointErr = loadErr
			}
		}
		return runtime.failAfterCleanup(stages, errors.Join(finalizeErr, checkpointErr))
	}
	state, err := runtime.Store.Load()
	if err == nil {
		err = state.MarkComplete(runtime.Store, runtime.Dependencies.Now())
	}
	if err != nil {
		return runtime.failAfterCleanup(stages, err)
	}
	status := lifecycle.StatusCompleteClean
	if resumed {
		status = lifecycle.StatusCompleteAfterResume
	}
	return runtime.finish(stages, status, nil)
}

func (runtime *Runtime) fail(ctx context.Context, stages []lifecycle.Stage, cause error) (Outcome, error) {
	state, _ := runtime.Store.Load()
	stageName := ""
	if state.CurrentStage != nil {
		stageName = *state.CurrentStage
	}
	category := classifyFailure(cause)
	reasonErr := runtime.Writer.WriteReason(report.DiagnosticReason{
		Category: reportCategory(category), At: runtime.Dependencies.Now(), Stage: stageName, Message: cause.Error(),
	})
	if runtime.Enclave.Owned {
		if runtime.Config.PreserveOnFailure {
			diagnosticContext, cancelDiagnostics := runtime.cleanupContext(ctx)
			diagnosticErr := runtime.collectDiagnostics(diagnosticContext)
			cancelDiagnostics()
			preserveErr := lifecycle.MarkOwnershipPreserved(runtime.Writer.Layout().Ownership, cause.Error())
			if record, loadErr := lifecycle.LoadOwnership(runtime.Writer.Layout().Ownership); loadErr == nil {
				preserveErr = errors.Join(preserveErr, runtime.Writer.WriteOwnership(record))
			} else {
				preserveErr = errors.Join(preserveErr, loadErr)
			}
			return runtime.finish(stages, lifecycle.StatusFailed, errors.Join(cause, reasonErr, diagnosticErr, preserveErr))
		}
		cleanupContext, cancelCleanup := runtime.cleanupContext(ctx)
		result, cleanupErr := finalizer.Run(cleanupContext, finalizer.Options{
			OwnershipPath: runtime.Writer.Layout().Ownership, Writer: runtime.Writer,
			Client: runtime.Dependencies.Client, Dumper: runtime.Dependencies.Dumper,
			DestroyPreserved: true, Now: runtime.Dependencies.Now,
		})
		cancelCleanup()
		var checkpointErr error
		if result.Destroyed {
			if loaded, loadErr := runtime.Store.Load(); loadErr == nil {
				checkpointErr = loaded.MarkCleanedAfterFailure(runtime.Store, runtime.Dependencies.Now())
			} else {
				checkpointErr = loadErr
			}
		}
		cause = errors.Join(cause, reasonErr, cleanupErr, checkpointErr)
	} else {
		diagnosticContext, cancelDiagnostics := runtime.cleanupContext(ctx)
		diagnosticErr := runtime.collectDiagnostics(diagnosticContext)
		cancelDiagnostics()
		cause = errors.Join(cause, reasonErr, diagnosticErr)
	}
	return runtime.finish(stages, lifecycle.StatusFailed, cause)
}

func (runtime *Runtime) failAfterCleanup(stages []lifecycle.Stage, cause error) (Outcome, error) {
	reasonErr := runtime.Writer.WriteReason(report.DiagnosticReason{Category: report.FailureCleanup, At: runtime.Dependencies.Now(), Message: cause.Error()})
	return runtime.finish(stages, lifecycle.StatusCleanedAfterFailure, errors.Join(cause, reasonErr))
}

func (runtime *Runtime) collectDiagnostics(ctx context.Context) error {
	if runtime.Topology == nil {
		return nil
	}
	tasks := make([]report.DiagnosticTask, 0, len(runtime.Topology.ServiceNames()))
	for _, name := range runtime.Topology.ServiceNames() {
		name := name
		tasks = append(tasks, report.DiagnosticTask{Name: "service-" + safeDiagnosticName(name), Collect: func(ctx context.Context) ([]byte, error) {
			logs, err := runtime.Dependencies.Client.ServiceLogs(ctx, runtime.Enclave, []string{name})
			return logs[name], err
		}})
	}
	_, err := runtime.Writer.CollectDiagnostics(ctx, tasks)
	return err
}

func (runtime *Runtime) writeProgress(stages []lifecycle.Stage, attempt lifecycle.Attempt) error {
	status := report.StatusPassed
	category := report.FailureCategory("")
	if attempt.ExitCode == nil {
		status = report.StatusRunning
	} else if *attempt.ExitCode != 0 {
		status = report.StatusFailed
		category = reportCategory(attempt.FailureCategory)
	}
	finished := time.Time{}
	duration := int64(0)
	if attempt.FinishedAt != nil {
		finished = *attempt.FinishedAt
		duration = finished.Sub(attempt.StartedAt).Milliseconds()
	}
	stageResult := report.StageResult{
		Name: attempt.Stage, Attempt: attempt.Attempt, Status: status, StartedAt: attempt.StartedAt,
		FinishedAt: finished, DurationMillis: duration, FailureCategory: category,
		Message: attempt.FailureMessage, LogPath: runtime.stageLogPath(attempt),
	}
	if attempt.Reconciled {
		stageResult.Details = "external state proved the stage already completed; execution was not replayed"
	}
	if err := runtime.Writer.WriteStage(stageResult); err != nil {
		return err
	}
	runtime.Timeline.Record(report.TimelineEvent{Kind: "stage-finished", Stage: attempt.Stage, Status: status, Message: attempt.FailureMessage, Fields: map[string]any{"attempt": attempt.Attempt, "reconciled": attempt.Reconciled}})
	if status == report.StatusPassed {
		runtime.retireFailedSuiteResults(attempt.Stage)
	}
	return runtime.writeAggregate(stages, report.StatusRunning)
}

func (runtime *Runtime) writeAggregate(stages []lifecycle.Stage, overall report.Status) error {
	state, err := runtime.Store.Load()
	if err != nil {
		return err
	}
	latest := make(map[string]lifecycle.Attempt)
	for _, attempt := range state.Attempts {
		latest[attempt.Stage] = attempt
	}
	stageResults := make([]report.StageResult, 0, len(stages))
	for _, stage := range stages {
		attempt, ok := latest[stage.Name]
		if !ok {
			stageResults = append(stageResults, report.StageResult{Name: stage.Name, Attempt: 1, Status: report.StatusPending})
			continue
		}
		status := report.StatusRunning
		category := report.FailureCategory("")
		finished := time.Time{}
		duration := int64(0)
		if attempt.ExitCode != nil {
			finished = *attempt.FinishedAt
			duration = finished.Sub(attempt.StartedAt).Milliseconds()
			if *attempt.ExitCode == 0 {
				status = report.StatusPassed
			} else {
				status = report.StatusFailed
				category = reportCategory(attempt.FailureCategory)
			}
		}
		stageResults = append(stageResults, report.StageResult{
			Name: attempt.Stage, Attempt: attempt.Attempt, Status: status, StartedAt: attempt.StartedAt,
			FinishedAt: finished, DurationMillis: duration, FailureCategory: category,
			Message: attempt.FailureMessage, LogPath: runtime.stageLogPath(attempt),
		})
	}
	finished := time.Time{}
	if overall == report.StatusPassed || overall == report.StatusFailed || overall == report.StatusCanceled {
		finished = runtime.Dependencies.Now()
	}
	results := report.Results{
		RunID: runtime.RunID, Status: overall, StartedAt: runtime.StartedAt, FinishedAt: finished,
		DurationMillis: durationMillis(runtime.StartedAt, finished), Stages: stageResults,
		Suites: runtime.SuiteResults, SuiteHistory: runtime.SuiteHistory,
	}
	if err := runtime.Writer.WriteResults(results); err != nil {
		return err
	}
	if err := runtime.Writer.WriteJUnit(results); err != nil {
		return err
	}
	return runtime.Writer.WriteTimeline(runtime.Timeline.Snapshot())
}

// stageLogPath returns only evidence that was durably written for this exact
// attempt. Lifecycle-only stages intentionally omit LogPath rather than
// advertising the conventional filename when no log artifact exists.
func (runtime *Runtime) stageLogPath(attempt lifecycle.Attempt) string {
	if attempt.Reconciled {
		return ""
	}
	candidates := []string{filepath.Join(report.LogsDirectory, fmt.Sprintf("%s-attempt-%d.log", attempt.Stage, attempt.Attempt))}
	if attempt.Stage == "validate" {
		candidates = append(candidates, filepath.Join(report.LogsDirectory, "doctor.log"))
	}
	for _, relative := range candidates {
		info, err := os.Lstat(filepath.Join(runtime.Writer.Root(), relative))
		if err == nil && info.Mode().IsRegular() {
			return filepath.ToSlash(relative)
		}
	}
	return ""
}

func (runtime *Runtime) finish(stages []lifecycle.Stage, status lifecycle.Status, cause error) (Outcome, error) {
	overall := report.StatusPassed
	if cause != nil {
		overall = report.StatusFailed
		if errors.Is(cause, context.Canceled) {
			overall = report.StatusCanceled
		}
	}
	writeErr := runtime.writeAggregate(stages, overall)
	digest, _ := runtime.Config.Digest()
	_, manifestErr := runtime.Writer.WriteManifest(report.ManifestMetadata{RunID: runtime.RunID, SourceSHA: runtime.Config.SourceSHA, ConfigurationDigest: digest, GeneratedAt: runtime.Dependencies.Now()})
	return outcome(runtime, status), errors.Join(cause, writeErr, manifestErr)
}

func prepareConfig(runConfig *config.RunConfig, now time.Time) error {
	if err := runConfig.Normalize(); err != nil {
		return err
	}
	if runConfig.NetworkParams != "" {
		payload, err := os.ReadFile(runConfig.NetworkParams)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		runConfig.NetworkParamsSHA256 = hex.EncodeToString(digest[:])
	}
	return runConfig.Validate(now)
}

func enclaveCreationContext(parent context.Context, runConfig config.RunConfig, now time.Time) (context.Context, context.CancelFunc, error) {
	deadline := now.Add(enclaveCreationTimeout)
	preCleanupDeadline := runConfig.GlobalDeadline.Add(-runConfig.CleanupReserve)
	if deadline.After(preCleanupDeadline) {
		deadline = preCleanupDeadline
	}
	if parentDeadline, ok := parent.Deadline(); ok && deadline.After(parentDeadline) {
		deadline = parentDeadline
	}
	if deadline.Sub(now) < enclaveCreationMinimum {
		return nil, nil, fmt.Errorf("refusing enclave creation: only %s remains before cleanup, need %s", deadline.Sub(now), enclaveCreationMinimum)
	}
	bounded, cancel := context.WithDeadline(parent, deadline)
	return bounded, cancel, nil
}

func (runtime *Runtime) cleanupContext(parent context.Context) (context.Context, context.CancelFunc) {
	base := context.WithoutCancel(parent)
	if runtime.Config.GlobalDeadline.IsZero() {
		return context.WithCancel(base)
	}
	return context.WithDeadline(base, runtime.Config.GlobalDeadline)
}

func classifyFailure(err error) lifecycle.FailureCategory {
	if errors.Is(err, context.DeadlineExceeded) {
		return lifecycle.FailureTimeout
	}
	if errors.Is(err, context.Canceled) {
		return lifecycle.FailureCancellation
	}
	type exitCoder interface{ ExitCode() int }
	var coded exitCoder
	if errors.As(err, &coded) {
		return lifecycle.FailureProcessExit
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "kurtosis") || strings.Contains(text, "starlark") || strings.Contains(text, "enclave") {
		return lifecycle.FailureSDK
	}
	return lifecycle.FailureAssertion
}

func reportCategory(category lifecycle.FailureCategory) report.FailureCategory {
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

func safeDiagnosticName(name string) string {
	var result strings.Builder
	for _, character := range name {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._-", character) {
			result.WriteRune(character)
		} else {
			result.WriteByte('_')
		}
	}
	return result.String()
}

func durationMillis(started, finished time.Time) int64 {
	if started.IsZero() || finished.IsZero() {
		return 0
	}
	return finished.Sub(started).Milliseconds()
}

func outcome(runtime *Runtime, status lifecycle.Status) Outcome {
	return Outcome{RunID: runtime.RunID, ResultsDir: runtime.Writer.Root(), Checkpoint: runtime.Store.Path, Ownership: runtime.Writer.Layout().Ownership, Status: status}
}
