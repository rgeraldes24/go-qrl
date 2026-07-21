// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package report

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

var testTime = time.Date(2026, 7, 21, 12, 30, 0, 123456789, time.FixedZone("GST", 4*60*60))

func newTestWriter(t *testing.T) *Writer {
	t.Helper()
	writer, err := NewWithClock(filepath.Join(t.TempDir(), "artifacts"), func() time.Time { return testTime })
	if err != nil {
		t.Fatal(err)
	}
	return writer
}

func TestNewCreatesCanonicalLayout(t *testing.T) {
	writer := newTestWriter(t)
	layout := writer.Layout()
	if !filepath.IsAbs(writer.Root()) || layout.Root != writer.Root() {
		t.Fatalf("root is not canonical: writer=%q layout=%q", writer.Root(), layout.Root)
	}
	wantDirectories := []string{DiagnosticsDirectory, KurtosisDirectory, LogsDirectory, ServicesDirectory, StagesDirectory}
	entries, err := os.ReadDir(writer.Root())
	if err != nil {
		t.Fatal(err)
	}
	var gotDirectories []string
	for _, entry := range entries {
		if !entry.IsDir() {
			t.Fatalf("new layout unexpectedly contains file %q", entry.Name())
		}
		gotDirectories = append(gotDirectories, entry.Name())
	}
	if !reflect.DeepEqual(gotDirectories, wantDirectories) {
		t.Fatalf("directories = %v, want %v", gotDirectories, wantDirectories)
	}
	paths := map[string]string{
		OwnershipFilename:       layout.Ownership,
		ManifestFilename:        layout.Manifest,
		EffectiveConfigFilename: layout.EffectiveConfig,
		TopologyFilename:        layout.Topology,
		CheckpointFilename:      layout.Checkpoint,
		ResultsFilename:         layout.Results,
		JUnitFilename:           layout.JUnit,
		TimelineFilename:        layout.Timeline,
	}
	for relative, path := range paths {
		if want := filepath.Join(writer.Root(), relative); path != want {
			t.Errorf("layout path %s = %q, want %q", relative, path, want)
		}
	}
}

func TestNewRejectsInvalidRoots(t *testing.T) {
	if _, err := New("   "); err == nil {
		t.Fatal("empty root accepted")
	}
	root := t.TempDir()
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, []byte("not a directory"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := New(file); err == nil {
		t.Fatal("file root accepted")
	}
}

func TestWriterProducesCanonicalJSONAndLogs(t *testing.T) {
	writer := newTestWriter(t)
	if err := writer.WriteOwnership(map[string]any{"uuid": "abc", "schema": 1}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteEffectiveConfig(map[string]any{"word": "<vm64>", "enabled": true}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteTopology(map[string]any{"execution": []string{"el-1", "el-2"}}); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteCheckpoint(map[string]any{"completed": []string{"doctor"}}); err != nil {
		t.Fatal(err)
	}
	stage := StageResult{Name: "network-readiness", Attempt: 1, Status: StatusPassed, DurationMillis: 250}
	if err := writer.WriteStage(stage); err != nil {
		t.Fatal(err)
	}
	results := Results{
		RunID: "run-1", Status: StatusPassed, DurationMillis: 250,
		Stages: []StageResult{stage}, Suites: []SuiteResult{},
	}
	if err := writer.WriteResults(results); err != nil {
		t.Fatal(err)
	}
	recorder := NewTimelineRecorder("run-1", func() time.Time { return testTime })
	recorder.Record(TimelineEvent{Kind: "stage_finished", Stage: stage.Name, Status: StatusPassed})
	if err := writer.WriteTimeline(recorder.Snapshot()); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteReason(DiagnosticReason{
		Category: FailureInfrastructure, At: testTime, Stage: "network-readiness", Message: "engine stopped",
	}); err != nil {
		t.Fatal(err)
	}
	if path, err := writer.WriteSuiteLog("el-1-console", []byte("suite output\n")); err != nil || path != "logs/el-1-console.log" {
		t.Fatalf("suite log path=%q err=%v", path, err)
	}
	if path, err := writer.WriteServiceLog("cl-1", []byte("service output\n")); err != nil || path != "services/cl-1.log" {
		t.Fatalf("service log path=%q err=%v", path, err)
	}
	if path, err := writer.WriteKurtosisArtifact("dump/metadata.txt", []byte("dump")); err != nil || path != "kurtosis/dump/metadata.txt" {
		t.Fatalf("Kurtosis artifact path=%q err=%v", path, err)
	}

	for _, relative := range []string{
		OwnershipFilename, EffectiveConfigFilename, TopologyFilename, CheckpointFilename,
		ResultsFilename, TimelineFilename, "stages/network-readiness.json",
		"diagnostics/reason.json", "logs/el-1-console.log", "services/cl-1.log",
		"kurtosis/dump/metadata.txt",
	} {
		if _, err := os.Stat(filepath.Join(writer.Root(), filepath.FromSlash(relative))); err != nil {
			t.Errorf("missing %s: %v", relative, err)
		}
	}
	config := readTestFile(t, filepath.Join(writer.Root(), EffectiveConfigFilename))
	if !bytes.HasSuffix(config, []byte("\n")) || bytes.Contains(config, []byte(`\u003cvm64\u003e`)) || !bytes.Contains(config, []byte(`<vm64>`)) {
		t.Fatalf("effective config is not canonical readable JSON:\n%s", config)
	}
	stageData := readTestFile(t, filepath.Join(writer.Root(), StagesDirectory, "network-readiness.json"))
	var decoded map[string]any
	if err := json.Unmarshal(stageData, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["schema"] != float64(SchemaVersion) || decoded["name"] != "network-readiness" {
		t.Fatalf("unexpected stage JSON: %#v", decoded)
	}
}

func TestJSONEncodingFailureDoesNotReplaceArtifact(t *testing.T) {
	writer := newTestWriter(t)
	if err := writer.WriteTopology(map[string]int{"version": 1}); err != nil {
		t.Fatal(err)
	}
	path := writer.Layout().Topology
	before := readTestFile(t, path)
	if err := writer.WriteTopology(map[string]any{"unsupported": make(chan int)}); err == nil {
		t.Fatal("unsupported JSON value was accepted")
	}
	after := readTestFile(t, path)
	if !bytes.Equal(after, before) {
		t.Fatalf("artifact changed after encode failure\nbefore=%s\nafter=%s", before, after)
	}
	entries, err := os.ReadDir(writer.Root())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".vm64e2e-atomic-") {
			t.Fatalf("temporary artifact leaked: %s", entry.Name())
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o644 {
		t.Fatalf("artifact mode=%#o, want 0644", got)
	}
}

func TestAtomicWriteCleansTemporaryFileAfterRenameFailure(t *testing.T) {
	root := t.TempDir()
	destination := filepath.Join(root, "destination")
	if err := os.Mkdir(destination, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := atomicWrite(destination, []byte("payload"), 0o644); err == nil {
		t.Fatal("rename over a directory unexpectedly succeeded")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "destination" {
		t.Fatalf("temporary file leaked after failure: %v", entries)
	}
}

func TestArtifactNamesRejectTraversalAndAmbiguity(t *testing.T) {
	writer := newTestWriter(t)
	for _, name := range []string{"", ".", "..", "../escape", "nested/name", `nested\name`, " space", "name:port"} {
		t.Run(strings.ReplaceAll(name, "/", "_"), func(t *testing.T) {
			if _, err := writer.WriteSuiteLog(name, nil); err == nil {
				t.Fatalf("unsafe suite log name %q accepted", name)
			}
			if _, err := writer.WriteServiceLog(name, nil); err == nil {
				t.Fatalf("unsafe service log name %q accepted", name)
			}
		})
	}
	for _, path := range []string{"", "/absolute", "../escape", "a/../../escape", "a//b", "a/./b", `a\b`} {
		if _, err := writer.WriteKurtosisArtifact(path, nil); err == nil {
			t.Errorf("unsafe Kurtosis path %q accepted", path)
		}
	}
	if _, err := writer.WriteKurtosisArtifact("dump/services/el-1.log", []byte("ok")); err != nil {
		t.Fatalf("safe nested path rejected: %v", err)
	}
}

func TestResultValidationRejectsAmbiguousEvidence(t *testing.T) {
	writer := newTestWriter(t)
	valid := Results{
		RunID: "run", Status: StatusFailed,
		Stages: []StageResult{{Name: "stage", Attempt: 1, Status: StatusFailed, FailureCategory: FailureAssertion}},
		Suites: []SuiteResult{},
	}
	if err := writer.WriteResults(valid); err != nil {
		t.Fatal(err)
	}
	before := readTestFile(t, writer.Layout().Results)
	tests := []struct {
		name   string
		mutate func(*Results)
	}{
		{"schema", func(value *Results) { value.Schema = 2 }},
		{"run ID", func(value *Results) { value.RunID = "" }},
		{"status", func(value *Results) { value.Status = "unknown" }},
		{"duration", func(value *Results) { value.DurationMillis = -1 }},
		{"attempt", func(value *Results) { value.Stages[0].Attempt = 0 }},
		{"missing category", func(value *Results) { value.Stages[0].FailureCategory = "" }},
		{"unsafe name", func(value *Results) { value.Stages[0].Name = "../stage" }},
		{"unsafe log", func(value *Results) { value.Stages[0].LogPath = "../secret" }},
		{"category on pass", func(value *Results) { value.Stages[0].Status = StatusPassed }},
		{"reverse time", func(value *Results) { value.StartedAt = testTime; value.FinishedAt = testTime.Add(-time.Second) }},
		{"passed with active failed suite", func(value *Results) {
			value.Status = StatusPassed
			value.Suites = []SuiteResult{{Name: "suite", Stage: "stage", Attempt: 1, Status: StatusFailed, FailureCategory: FailureAssertion}}
		}},
		{"invalid historical suite attempt", func(value *Results) {
			value.SuiteHistory = []SuiteResult{{Name: "suite", Stage: "stage", Attempt: -1, Status: StatusPassed}}
		}},
		{"duplicate active suite", func(value *Results) {
			value.Suites = []SuiteResult{
				{Name: "suite", Stage: "stage", Attempt: 1, Status: StatusPassed},
				{Name: "suite", Stage: "stage", Attempt: 2, Status: StatusPassed},
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			candidate.Stages = append([]StageResult(nil), valid.Stages...)
			test.mutate(&candidate)
			if err := writer.WriteResults(candidate); err == nil {
				t.Fatal("invalid results accepted")
			}
			if after := readTestFile(t, writer.Layout().Results); !bytes.Equal(after, before) {
				t.Fatal("invalid results replaced valid artifact")
			}
		})
	}
}

func TestPassedResultsRequireTerminalPassingEvidence(t *testing.T) {
	base := Results{
		RunID: "passed-run", Status: StatusPassed,
		Stages: []StageResult{{Name: "stage", Attempt: 1, Status: StatusPassed}},
		Suites: []SuiteResult{{Name: "suite", Stage: "stage", Attempt: 1, Status: StatusPassed}},
	}
	for _, status := range []Status{StatusPending, StatusRunning, StatusFailed, StatusCanceled, StatusSkipped} {
		t.Run("stage-"+string(status), func(t *testing.T) {
			candidate := base
			candidate.Stages = append([]StageResult(nil), base.Stages...)
			candidate.Stages[0].Status = status
			if status == StatusFailed {
				candidate.Stages[0].FailureCategory = FailureAssertion
			}
			if status == StatusCanceled {
				candidate.Stages[0].FailureCategory = FailureCancellation
			}
			if err := newTestWriter(t).WriteResults(candidate); err == nil {
				t.Fatalf("passed aggregate accepted %s stage", status)
			}
		})
	}
	for _, status := range []Status{StatusPending, StatusRunning, StatusFailed, StatusCanceled} {
		t.Run("suite-"+string(status), func(t *testing.T) {
			candidate := base
			candidate.Suites = append([]SuiteResult(nil), base.Suites...)
			candidate.Suites[0].Status = status
			if status == StatusFailed {
				candidate.Suites[0].FailureCategory = FailureAssertion
			}
			if status == StatusCanceled {
				candidate.Suites[0].FailureCategory = FailureCancellation
			}
			if err := newTestWriter(t).WriteResults(candidate); err == nil {
				t.Fatalf("passed aggregate accepted %s active suite", status)
			}
		})
	}
	skipped := base
	skipped.Suites = append([]SuiteResult(nil), base.Suites...)
	skipped.Suites[0].Status = StatusSkipped
	skipped.Suites[0].Message = "not applicable"
	if err := newTestWriter(t).WriteResults(skipped); err != nil {
		t.Fatalf("passed aggregate rejected explicitly skipped suite: %v", err)
	}
}

func TestLoadResultsBackfillsAdditiveSuiteHistoryFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	legacy := []byte(`{
  "schema": 1,
  "run_id": "legacy-run",
  "status": "failed",
  "duration_ms": 1,
  "stages": [],
  "suites": [{"name":"go-abi","stage":"el1","status":"failed","duration_ms":1,"failure_category":"assertion_failure"}]
}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := LoadResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Suites) != 1 || results.Suites[0].Attempt != 1 {
		t.Fatalf("legacy suite attempt was not backfilled: %+v", results.Suites)
	}
	if results.SuiteHistory == nil || len(results.SuiteHistory) != 0 {
		t.Fatalf("legacy suite history = %#v, want a non-nil empty slice", results.SuiteHistory)
	}
}

func TestLoadResultsMigratesLegacyDuplicateSuiteRetries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "results.json")
	legacy := []byte(`{
  "schema": 1,
  "run_id": "legacy-retry",
  "status": "passed",
  "duration_ms": 2,
  "stages": [{"name":"el1","attempt":2,"status":"passed","duration_ms":1}],
  "suites": [
    {"name":"go-abi","stage":"el1","status":"failed","duration_ms":1,"failure_category":"assertion_failure","message":"old failure","details":"64-byte mismatch","log_path":"logs/attempt-1.log"},
    {"name":"go-abi","stage":"el1","status":"passed","duration_ms":1}
  ]
}`)
	if err := os.WriteFile(path, legacy, 0o600); err != nil {
		t.Fatal(err)
	}
	results, err := LoadResults(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Suites) != 1 || results.Suites[0].Attempt != 2 || results.Suites[0].Status != StatusPassed {
		t.Fatalf("migrated active suite = %+v", results.Suites)
	}
	if len(results.SuiteHistory) != 1 || results.SuiteHistory[0].Attempt != 1 || results.SuiteHistory[0].Status != StatusFailed || results.SuiteHistory[0].Details != "64-byte mismatch" || results.SuiteHistory[0].LogPath != "logs/attempt-1.log" {
		t.Fatalf("migrated suite history = %+v", results.SuiteHistory)
	}
}

func TestStrictArtifactLoadersRejectCorruption(t *testing.T) {
	directory := t.TempDir()
	resultsPath := filepath.Join(directory, "results.json")
	timelinePath := filepath.Join(directory, "timeline.json")
	validResults := Results{Schema: SchemaVersion, RunID: "run", Status: StatusPending, Stages: []StageResult{}, Suites: []SuiteResult{}}
	validTimeline := Timeline{Schema: SchemaVersion, RunID: "run", Events: []TimelineEvent{{Sequence: 1, At: testTime, Kind: "created"}}}
	encode := func(value any) []byte {
		t.Helper()
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return payload
	}
	if err := os.WriteFile(resultsPath, encode(validResults), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(timelinePath, encode(validTimeline), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResults(resultsPath); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTimeline(timelinePath); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resultsPath, []byte(`{"schema":1,"run_id":"run","status":"pending","stages":[],"suites":[],"unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadResults(resultsPath); err == nil {
		t.Fatal("results with an unknown field were accepted")
	}
	if err := os.WriteFile(timelinePath, append(encode(validTimeline), []byte(` {}`)...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadTimeline(timelinePath); err == nil {
		t.Fatal("timeline with trailing JSON was accepted")
	}
}

func readTestFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
