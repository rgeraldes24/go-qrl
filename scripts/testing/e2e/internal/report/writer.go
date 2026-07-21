// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package report

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	OwnershipFilename       = "ownership.json"
	ManifestFilename        = "manifest.json"
	EffectiveConfigFilename = "effective-config.json"
	TopologyFilename        = "topology.json"
	CheckpointFilename      = "checkpoint.json"
	ResultsFilename         = "results.json"
	JUnitFilename           = "junit.xml"
	TimelineFilename        = "timeline.json"
	FinalizeFilename        = "finalize.json"
	StagesDirectory         = "stages"
	LogsDirectory           = "logs"
	ServicesDirectory       = "services"
	DiagnosticsDirectory    = "diagnostics"
	KurtosisDirectory       = "kurtosis"
	ReasonFilename          = "reason.json"
	DiagnosticIndexFilename = "collection.json"
)

var componentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Layout exposes canonical artifact paths without requiring callers to rebuild
// filenames. All fields are absolute and rooted below Root.
type Layout struct {
	Root            string
	Ownership       string
	Manifest        string
	EffectiveConfig string
	Topology        string
	Checkpoint      string
	Results         string
	JUnit           string
	Timeline        string
	Finalize        string
	Stages          string
	Logs            string
	Services        string
	Diagnostics     string
	Kurtosis        string
	Reason          string
}

type Writer struct {
	root    string
	now     func() time.Time
	mu      sync.Mutex
	clockMu sync.Mutex
}

// New creates the fixed directory layout. Existing artifacts are retained so
// resume and finalize can append evidence to the same run directory.
func New(root string) (*Writer, error) {
	return NewWithClock(root, time.Now)
}

func NewWithClock(root string, now func() time.Time) (*Writer, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("artifact root is empty")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve artifact root: %w", err)
	}
	if now == nil {
		now = time.Now
	}
	writer := &Writer{root: filepath.Clean(absolute), now: now}
	for _, directory := range []string{
		writer.root,
		filepath.Join(writer.root, StagesDirectory),
		filepath.Join(writer.root, LogsDirectory),
		filepath.Join(writer.root, ServicesDirectory),
		filepath.Join(writer.root, DiagnosticsDirectory),
		filepath.Join(writer.root, KurtosisDirectory),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return nil, fmt.Errorf("create artifact directory %s: %w", directory, err)
		}
		info, err := os.Stat(directory)
		if err != nil {
			return nil, fmt.Errorf("inspect artifact directory %s: %w", directory, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("artifact path %s is not a directory", directory)
		}
	}
	return writer, nil
}

func (writer *Writer) Root() string { return writer.root }

func (writer *Writer) currentTime() time.Time {
	writer.clockMu.Lock()
	defer writer.clockMu.Unlock()
	return writer.now().UTC()
}

func (writer *Writer) Layout() Layout {
	return Layout{
		Root:            writer.root,
		Ownership:       filepath.Join(writer.root, OwnershipFilename),
		Manifest:        filepath.Join(writer.root, ManifestFilename),
		EffectiveConfig: filepath.Join(writer.root, EffectiveConfigFilename),
		Topology:        filepath.Join(writer.root, TopologyFilename),
		Checkpoint:      filepath.Join(writer.root, CheckpointFilename),
		Results:         filepath.Join(writer.root, ResultsFilename),
		JUnit:           filepath.Join(writer.root, JUnitFilename),
		Timeline:        filepath.Join(writer.root, TimelineFilename),
		Finalize:        filepath.Join(writer.root, FinalizeFilename),
		Stages:          filepath.Join(writer.root, StagesDirectory),
		Logs:            filepath.Join(writer.root, LogsDirectory),
		Services:        filepath.Join(writer.root, ServicesDirectory),
		Diagnostics:     filepath.Join(writer.root, DiagnosticsDirectory),
		Kurtosis:        filepath.Join(writer.root, KurtosisDirectory),
		Reason:          filepath.Join(writer.root, DiagnosticsDirectory, ReasonFilename),
	}
}

func (writer *Writer) WriteOwnership(value any) error {
	return writer.writeJSON(OwnershipFilename, value)
}

func (writer *Writer) WriteEffectiveConfig(value any) error {
	return writer.writeJSON(EffectiveConfigFilename, value)
}

func (writer *Writer) WriteTopology(value any) error {
	return writer.writeJSON(TopologyFilename, value)
}

func (writer *Writer) WriteCheckpoint(value any) error {
	return writer.writeJSON(CheckpointFilename, value)
}

func (writer *Writer) WriteResults(results Results) error {
	if results.Schema == 0 {
		results.Schema = SchemaVersion
	}
	normalizeResults(&results)
	if err := results.validate(); err != nil {
		return err
	}
	return writer.writeJSON(ResultsFilename, results)
}

func (writer *Writer) WriteTimeline(timeline Timeline) error {
	if timeline.Schema == 0 {
		timeline.Schema = SchemaVersion
	}
	if timeline.Events == nil {
		timeline.Events = []TimelineEvent{}
	}
	for index := range timeline.Events {
		timeline.Events[index].At = timeline.Events[index].At.UTC()
	}
	if err := timeline.validate(); err != nil {
		return err
	}
	return writer.writeJSON(TimelineFilename, timeline)
}

func (writer *Writer) WriteFinalize(value any) error {
	return writer.writeJSON(FinalizeFilename, value)
}

func (writer *Writer) WriteStage(result StageResult) error {
	if result.Attempt < 1 {
		return fmt.Errorf("stage %q has invalid attempt %d", result.Name, result.Attempt)
	}
	if err := validateResult(result.Name, result.Status, result.FailureCategory, result.DurationMillis, result.StartedAt, result.FinishedAt, result.LogPath); err != nil {
		return err
	}
	result.StartedAt = result.StartedAt.UTC()
	result.FinishedAt = result.FinishedAt.UTC()
	payload := struct {
		Schema int `json:"schema"`
		StageResult
	}{Schema: SchemaVersion, StageResult: result}
	return writer.writeJSON(filepath.Join(StagesDirectory, result.Name+".json"), payload)
}

func normalizeResults(results *Results) {
	for index := range results.Suites {
		if results.Suites[index].Attempt == 0 {
			results.Suites[index].Attempt = 1
		}
	}
	for index := range results.SuiteHistory {
		if results.SuiteHistory[index].Attempt == 0 {
			results.SuiteHistory[index].Attempt = 1
		}
	}
	normalizeResultsShape(results)
}

func normalizeResultsShape(results *Results) {
	results.StartedAt = results.StartedAt.UTC()
	results.FinishedAt = results.FinishedAt.UTC()
	if results.Stages == nil {
		results.Stages = []StageResult{}
	}
	for index := range results.Stages {
		results.Stages[index].StartedAt = results.Stages[index].StartedAt.UTC()
		results.Stages[index].FinishedAt = results.Stages[index].FinishedAt.UTC()
	}
	if results.Suites == nil {
		results.Suites = []SuiteResult{}
	}
	for index := range results.Suites {
		results.Suites[index].StartedAt = results.Suites[index].StartedAt.UTC()
		results.Suites[index].FinishedAt = results.Suites[index].FinishedAt.UTC()
	}
	if results.SuiteHistory == nil {
		results.SuiteHistory = []SuiteResult{}
	}
	for index := range results.SuiteHistory {
		results.SuiteHistory[index].StartedAt = results.SuiteHistory[index].StartedAt.UTC()
		results.SuiteHistory[index].FinishedAt = results.SuiteHistory[index].FinishedAt.UTC()
	}
}

func (writer *Writer) WriteReason(reason DiagnosticReason) error {
	if reason.Schema == 0 {
		reason.Schema = SchemaVersion
	}
	if _, err := normalizeSchema(reason.Schema); err != nil {
		return err
	}
	if !reason.Category.valid() {
		return fmt.Errorf("diagnostic reason has invalid category %q", reason.Category)
	}
	if reason.At.IsZero() || strings.TrimSpace(reason.Message) == "" {
		return errors.New("diagnostic reason requires time and message")
	}
	if reason.Stage != "" {
		if err := validateComponent(reason.Stage); err != nil {
			return fmt.Errorf("diagnostic reason stage: %w", err)
		}
	}
	if reason.Suite != "" {
		if err := validateComponent(reason.Suite); err != nil {
			return fmt.Errorf("diagnostic reason suite: %w", err)
		}
	}
	reason.At = reason.At.UTC()
	return writer.writeJSON(filepath.Join(DiagnosticsDirectory, ReasonFilename), reason)
}

func (writer *Writer) WriteSuiteLog(name string, data []byte) (string, error) {
	return writer.writeNamedLog(LogsDirectory, name, data)
}

func (writer *Writer) WriteServiceLog(name string, data []byte) (string, error) {
	return writer.writeNamedLog(ServicesDirectory, name, data)
}

func (writer *Writer) writeNamedLog(directory, name string, data []byte) (string, error) {
	if err := validateComponent(name); err != nil {
		return "", err
	}
	relative := filepath.Join(directory, name+".log")
	if err := writer.writeBytes(relative, data, 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(relative), nil
}

// WriteKurtosisArtifact preserves a file below kurtosis/. Nested relative
// paths are supported for enclave dumps, but traversal and absolute paths are
// rejected.
func (writer *Writer) WriteKurtosisArtifact(relative string, data []byte) (string, error) {
	if err := validateRelativePath(relative); err != nil {
		return "", err
	}
	destination := filepath.Join(KurtosisDirectory, filepath.FromSlash(relative))
	if err := writer.writeBytes(destination, data, 0o644); err != nil {
		return "", err
	}
	return filepath.ToSlash(destination), nil
}

func (writer *Writer) writeJSON(relative string, value any) error {
	data, err := encodeJSON(value)
	if err != nil {
		return fmt.Errorf("encode %s: %w", filepath.ToSlash(relative), err)
	}
	return writer.writeBytes(relative, data, 0o644)
}

func (writer *Writer) writeBytes(relative string, data []byte, mode fs.FileMode) error {
	if err := validateRelativePath(filepath.ToSlash(relative)); err != nil {
		return err
	}
	path := filepath.Join(writer.root, filepath.FromSlash(filepath.ToSlash(relative)))
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if err := atomicWrite(path, data, mode); err != nil {
		return fmt.Errorf("write artifact %s: %w", filepath.ToSlash(relative), err)
	}
	return nil
}

func atomicWrite(path string, data []byte, mode fs.FileMode) (returnErr error) {
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".vm64e2e-atomic-")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	closed := false
	defer func() {
		if !closed {
			closeErr := temporary.Close()
			if returnErr == nil && closeErr != nil {
				returnErr = closeErr
			}
		}
		if removeErr := os.Remove(temporaryPath); returnErr == nil && removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			returnErr = removeErr
		}
	}()
	if err := temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	closed = true
	if err := os.Rename(temporaryPath, path); err != nil {
		return err
	}
	return syncDirectory(directory)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil && !errors.Is(err, syscall.EINVAL) && !errors.Is(err, syscall.ENOTSUP) {
		return err
	}
	return nil
}

func validateComponent(component string) error {
	if component == "." || component == ".." || !componentPattern.MatchString(component) {
		return fmt.Errorf("artifact name %q is not a safe path component", component)
	}
	return nil
}

func validateRelativePath(relative string) error {
	if relative == "" || filepath.IsAbs(relative) || strings.Contains(relative, `\`) {
		return fmt.Errorf("artifact path %q must be a non-empty relative slash-separated path", relative)
	}
	cleaned := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
	if cleaned == "." || cleaned != relative || strings.HasPrefix(cleaned, "../") {
		return fmt.Errorf("artifact path %q is not canonical or escapes the artifact root", relative)
	}
	for _, component := range strings.Split(cleaned, "/") {
		if err := validateComponent(component); err != nil {
			return err
		}
	}
	return nil
}
