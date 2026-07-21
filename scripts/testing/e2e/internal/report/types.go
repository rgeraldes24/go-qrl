// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package report writes deterministic, machine-readable evidence for VM64 E2E
// runs. It deliberately contains no lifecycle or Kurtosis dependencies so the
// runner, finalizer, and suite packages can all use it without import cycles.
package report

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"
	"time"
)

const SchemaVersion = 1

// Status is the lifecycle state of a stage, suite, or complete run.
type Status string

const (
	StatusPending  Status = "pending"
	StatusRunning  Status = "running"
	StatusPassed   Status = "passed"
	StatusFailed   Status = "failed"
	StatusSkipped  Status = "skipped"
	StatusCanceled Status = "canceled"
)

func (status Status) valid() bool {
	switch status {
	case StatusPending, StatusRunning, StatusPassed, StatusFailed, StatusSkipped, StatusCanceled:
		return true
	default:
		return false
	}
}

// FailureCategory is intentionally aligned with diagnostics/reason.json from
// the VM64 E2E refactor plan.
type FailureCategory string

const (
	FailureAssertion      FailureCategory = "assertion_failure"
	FailureTimeout        FailureCategory = "timeout"
	FailureCancellation   FailureCategory = "cancellation"
	FailureProcessExit    FailureCategory = "process_exit"
	FailureSDK            FailureCategory = "sdk_error"
	FailureInfrastructure FailureCategory = "infrastructure_failure"
	FailureCleanup        FailureCategory = "cleanup_failure"
)

func (category FailureCategory) valid() bool {
	switch category {
	case FailureAssertion, FailureTimeout, FailureCancellation, FailureProcessExit,
		FailureSDK, FailureInfrastructure, FailureCleanup:
		return true
	default:
		return false
	}
}

// ManifestMetadata is supplied when the final artifact inventory is emitted.
// GeneratedAt may be left zero to use the writer's clock.
type ManifestMetadata struct {
	RunID               string    `json:"run_id"`
	SourceSHA           string    `json:"source_sha,omitempty"`
	ConfigurationDigest string    `json:"configuration_digest,omitempty"`
	GeneratedAt         time.Time `json:"generated_at"`
}

// Artifact identifies a regular file by a root-relative slash-separated path.
type Artifact struct {
	Path      string `json:"path"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// Manifest inventories all regular artifacts except manifest.json itself.
// Excluding the manifest avoids an impossible self-referential checksum.
type Manifest struct {
	Schema              int        `json:"schema"`
	RunID               string     `json:"run_id"`
	SourceSHA           string     `json:"source_sha,omitempty"`
	ConfigurationDigest string     `json:"configuration_digest,omitempty"`
	GeneratedAt         time.Time  `json:"generated_at"`
	Artifacts           []Artifact `json:"artifacts"`
}

// StageResult is both the per-stage JSON payload and the stage-granularity
// input to results.json and junit.xml.
type StageResult struct {
	Name            string          `json:"name"`
	Attempt         int             `json:"attempt"`
	Status          Status          `json:"status"`
	StartedAt       time.Time       `json:"started_at,omitzero"`
	FinishedAt      time.Time       `json:"finished_at,omitzero"`
	DurationMillis  int64           `json:"duration_ms"`
	FailureCategory FailureCategory `json:"failure_category,omitempty"`
	Message         string          `json:"message,omitempty"`
	Details         string          `json:"details,omitempty"`
	LogPath         string          `json:"log_path,omitempty"`
}

// SuiteResult is the suite-granularity input to results.json and junit.xml.
type SuiteResult struct {
	Name            string          `json:"name"`
	Stage           string          `json:"stage,omitempty"`
	Attempt         int             `json:"attempt"`
	Status          Status          `json:"status"`
	StartedAt       time.Time       `json:"started_at,omitzero"`
	FinishedAt      time.Time       `json:"finished_at,omitzero"`
	DurationMillis  int64           `json:"duration_ms"`
	FailureCategory FailureCategory `json:"failure_category,omitempty"`
	Message         string          `json:"message,omitempty"`
	Details         string          `json:"details,omitempty"`
	LogPath         string          `json:"log_path,omitempty"`
}

// Results is the single aggregate result consumed by CI summaries.
type Results struct {
	Schema         int           `json:"schema"`
	RunID          string        `json:"run_id"`
	Status         Status        `json:"status"`
	StartedAt      time.Time     `json:"started_at,omitzero"`
	FinishedAt     time.Time     `json:"finished_at,omitzero"`
	DurationMillis int64         `json:"duration_ms"`
	Stages         []StageResult `json:"stages"`
	Suites         []SuiteResult `json:"suites"`
	SuiteHistory   []SuiteResult `json:"suite_history"`
}

// TimelineEvent records a meaningful lifecycle transition. Sequence is
// assigned by TimelineRecorder and makes events with equal timestamps stable.
type TimelineEvent struct {
	Sequence uint64         `json:"sequence"`
	At       time.Time      `json:"at"`
	Kind     string         `json:"kind"`
	Stage    string         `json:"stage,omitempty"`
	Suite    string         `json:"suite,omitempty"`
	Status   Status         `json:"status,omitempty"`
	Message  string         `json:"message,omitempty"`
	Fields   map[string]any `json:"fields,omitempty"`
}

type Timeline struct {
	Schema int             `json:"schema"`
	RunID  string          `json:"run_id"`
	Events []TimelineEvent `json:"events"`
}

// TimelineRecorder is safe for concurrent suite and diagnostic goroutines.
// It records call order under a mutex rather than sorting wall-clock values.
type TimelineRecorder struct {
	mu     sync.Mutex
	runID  string
	now    func() time.Time
	next   uint64
	events []TimelineEvent
}

func NewTimelineRecorder(runID string, now func() time.Time) *TimelineRecorder {
	if now == nil {
		now = time.Now
	}
	return &TimelineRecorder{runID: runID, now: now, next: 1}
}

// ResumeTimelineRecorder restores a validated durable timeline and continues
// sequence allocation without rewriting or reordering earlier events.
func ResumeTimelineRecorder(timeline Timeline, now func() time.Time) (*TimelineRecorder, error) {
	if err := timeline.validate(); err != nil {
		return nil, err
	}
	if now == nil {
		now = time.Now
	}
	events := slices.Clone(timeline.Events)
	for index := range events {
		events[index].Fields = maps.Clone(events[index].Fields)
	}
	next := uint64(1)
	if len(events) != 0 {
		next = events[len(events)-1].Sequence + 1
	}
	return &TimelineRecorder{runID: timeline.RunID, now: now, next: next, events: events}, nil
}

func (recorder *TimelineRecorder) Record(event TimelineEvent) uint64 {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	event.Sequence = recorder.next
	recorder.next++
	if event.At.IsZero() {
		event.At = recorder.now().UTC()
	} else {
		event.At = event.At.UTC()
	}
	event.Fields = maps.Clone(event.Fields)
	recorder.events = append(recorder.events, event)
	return event.Sequence
}

func (recorder *TimelineRecorder) Snapshot() Timeline {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()
	events := slices.Clone(recorder.events)
	for index := range events {
		events[index].Fields = maps.Clone(events[index].Fields)
	}
	return Timeline{Schema: SchemaVersion, RunID: recorder.runID, Events: events}
}

// DiagnosticReason is the stable primary-failure record. Collection errors
// live in diagnostics/collection.json and do not overwrite this root cause.
type DiagnosticReason struct {
	Schema   int               `json:"schema"`
	Category FailureCategory   `json:"category"`
	At       time.Time         `json:"at"`
	Stage    string            `json:"stage,omitempty"`
	Suite    string            `json:"suite,omitempty"`
	Message  string            `json:"message"`
	Details  map[string]string `json:"details,omitempty"`
}

func normalizeSchema(schema int) (int, error) {
	if schema == 0 {
		return SchemaVersion, nil
	}
	if schema != SchemaVersion {
		return 0, fmt.Errorf("report schema is %d, want %d", schema, SchemaVersion)
	}
	return schema, nil
}

func validateResult(name string, status Status, category FailureCategory, duration int64, started, finished time.Time, logPath string) error {
	if err := validateComponent(name); err != nil {
		return err
	}
	if !status.valid() {
		return fmt.Errorf("result %q has invalid status %q", name, status)
	}
	if duration < 0 {
		return fmt.Errorf("result %q has a negative duration", name)
	}
	if !started.IsZero() && !finished.IsZero() && finished.Before(started) {
		return fmt.Errorf("result %q finishes before it starts", name)
	}
	if category != "" && !category.valid() {
		return fmt.Errorf("result %q has invalid failure category %q", name, category)
	}
	if (status == StatusFailed || status == StatusCanceled) && !category.valid() {
		return fmt.Errorf("result %q must classify its failure", name)
	}
	if status != StatusFailed && status != StatusCanceled && category != "" {
		return fmt.Errorf("result %q has a failure category while status is %q", name, status)
	}
	if logPath != "" {
		if err := validateRelativePath(logPath); err != nil {
			return fmt.Errorf("result %q log path: %w", name, err)
		}
	}
	return nil
}

func (results Results) validate() error {
	if _, err := normalizeSchema(results.Schema); err != nil {
		return err
	}
	if results.RunID == "" {
		return errors.New("results run ID is empty")
	}
	if !results.Status.valid() {
		return fmt.Errorf("results status %q is invalid", results.Status)
	}
	if results.DurationMillis < 0 {
		return errors.New("results duration is negative")
	}
	if !results.StartedAt.IsZero() && !results.FinishedAt.IsZero() && results.FinishedAt.Before(results.StartedAt) {
		return errors.New("results finish before they start")
	}
	for _, stage := range results.Stages {
		if stage.Attempt < 1 {
			return fmt.Errorf("stage %q has invalid attempt %d", stage.Name, stage.Attempt)
		}
		if err := validateResult(stage.Name, stage.Status, stage.FailureCategory, stage.DurationMillis, stage.StartedAt, stage.FinishedAt, stage.LogPath); err != nil {
			return err
		}
		if results.Status == StatusPassed && stage.Status != StatusPassed {
			return fmt.Errorf("passed results retain non-passing stage %q with status %q", stage.Name, stage.Status)
		}
	}
	type suiteKey struct{ stage, name string }
	type suiteEvidenceKey struct {
		suiteKey
		attempt int
	}
	activeSuites := make(map[suiteKey]struct{}, len(results.Suites))
	evidence := make(map[suiteEvidenceKey]struct{}, len(results.Suites)+len(results.SuiteHistory))
	for _, suite := range results.Suites {
		if suite.Attempt < 1 {
			return fmt.Errorf("suite %q has invalid attempt %d", suite.Name, suite.Attempt)
		}
		if err := validateResult(suite.Name, suite.Status, suite.FailureCategory, suite.DurationMillis, suite.StartedAt, suite.FinishedAt, suite.LogPath); err != nil {
			return err
		}
		if suite.Stage != "" {
			if err := validateComponent(suite.Stage); err != nil {
				return fmt.Errorf("suite %q stage: %w", suite.Name, err)
			}
		}
		key := suiteKey{stage: suite.Stage, name: suite.Name}
		if _, exists := activeSuites[key]; exists {
			return fmt.Errorf("duplicate active suite %q in stage %q", suite.Name, suite.Stage)
		}
		activeSuites[key] = struct{}{}
		evidenceKey := suiteEvidenceKey{suiteKey: key, attempt: suite.Attempt}
		if _, exists := evidence[evidenceKey]; exists {
			return fmt.Errorf("duplicate suite evidence %q stage %q attempt %d", suite.Name, suite.Stage, suite.Attempt)
		}
		evidence[evidenceKey] = struct{}{}
		if results.Status == StatusPassed && suite.Status != StatusPassed && suite.Status != StatusSkipped {
			return fmt.Errorf("passed results retain non-passing active suite %q attempt %d with status %q", suite.Name, suite.Attempt, suite.Status)
		}
	}
	for _, suite := range results.SuiteHistory {
		if suite.Attempt < 1 {
			return fmt.Errorf("historical suite %q has invalid attempt %d", suite.Name, suite.Attempt)
		}
		if err := validateResult(suite.Name, suite.Status, suite.FailureCategory, suite.DurationMillis, suite.StartedAt, suite.FinishedAt, suite.LogPath); err != nil {
			return fmt.Errorf("historical suite attempt: %w", err)
		}
		if suite.Stage != "" {
			if err := validateComponent(suite.Stage); err != nil {
				return fmt.Errorf("historical suite %q stage: %w", suite.Name, err)
			}
		}
		evidenceKey := suiteEvidenceKey{suiteKey: suiteKey{stage: suite.Stage, name: suite.Name}, attempt: suite.Attempt}
		if _, exists := evidence[evidenceKey]; exists {
			return fmt.Errorf("duplicate suite evidence %q stage %q attempt %d", suite.Name, suite.Stage, suite.Attempt)
		}
		evidence[evidenceKey] = struct{}{}
	}
	return nil
}

func (timeline Timeline) validate() error {
	if _, err := normalizeSchema(timeline.Schema); err != nil {
		return err
	}
	if timeline.RunID == "" {
		return errors.New("timeline run ID is empty")
	}
	var previous uint64
	for index, event := range timeline.Events {
		if event.Sequence == 0 || event.Sequence <= previous {
			return fmt.Errorf("timeline event %d has non-increasing sequence %d", index, event.Sequence)
		}
		if event.At.IsZero() || event.Kind == "" {
			return fmt.Errorf("timeline event %d is missing time or kind", index)
		}
		if event.Status != "" && !event.Status.valid() {
			return fmt.Errorf("timeline event %d has invalid status %q", index, event.Status)
		}
		previous = event.Sequence
	}
	return nil
}
