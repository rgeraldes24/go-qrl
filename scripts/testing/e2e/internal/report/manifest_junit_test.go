// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestManifestIsSortedHashedAndRepeatable(t *testing.T) {
	writer := newTestWriter(t)
	if _, err := writer.WriteSuiteLog("b", []byte("bravo")); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.WriteSuiteLog("a", []byte("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(writer.Root(), ".vm64e2e-atomic-orphan"), []byte("incomplete"), 0o600); err != nil {
		t.Fatal(err)
	}
	metadata := ManifestMetadata{
		RunID: "run-42", SourceSHA: strings.Repeat("a", 40),
		ConfigurationDigest: strings.Repeat("b", 64), GeneratedAt: testTime,
	}
	manifest, err := writer.WriteManifest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{"logs/a.log", "logs/b.log"}
	var gotPaths []string
	for _, artifact := range manifest.Artifacts {
		gotPaths = append(gotPaths, artifact.Path)
	}
	if !reflect.DeepEqual(gotPaths, wantPaths) {
		t.Fatalf("manifest paths=%v, want %v", gotPaths, wantPaths)
	}
	wantDigest := sha256.Sum256([]byte("alpha"))
	if manifest.Artifacts[0].SizeBytes != 5 || manifest.Artifacts[0].SHA256 != hex.EncodeToString(wantDigest[:]) {
		t.Fatalf("unexpected alpha metadata: %#v", manifest.Artifacts[0])
	}
	first := readTestFile(t, writer.Layout().Manifest)
	secondManifest, err := writer.WriteManifest(metadata)
	if err != nil {
		t.Fatal(err)
	}
	second := readTestFile(t, writer.Layout().Manifest)
	if !bytes.Equal(first, second) || !reflect.DeepEqual(manifest, secondManifest) {
		t.Fatalf("manifest changed without an artifact change\nfirst=%s\nsecond=%s", first, second)
	}
	if bytes.Contains(first, []byte(ManifestFilename)) || bytes.Contains(first, []byte("atomic-orphan")) {
		t.Fatalf("manifest inventories itself or an atomic temporary file: %s", first)
	}
}

func TestManifestRejectsSymlinkArtifacts(t *testing.T) {
	writer := newTestWriter(t)
	target := filepath.Join(writer.Root(), "target.log")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(writer.Root(), "services", "link.log")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := writer.WriteManifest(ManifestMetadata{RunID: "run", GeneratedAt: testTime}); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("symlink artifact error=%v", err)
	}
}

func TestWriteManifestUsesClockWhenTimestampOmitted(t *testing.T) {
	writer := newTestWriter(t)
	manifest, err := writer.WriteManifest(ManifestMetadata{RunID: "run"})
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.GeneratedAt.Equal(testTime.UTC()) {
		t.Fatalf("generated_at=%s, want %s", manifest.GeneratedAt, testTime.UTC())
	}
}

func TestManifestRejectsDanglingResultLogReferences(t *testing.T) {
	for _, test := range []struct {
		name  string
		write func(*Writer) error
	}{
		{
			name: "aggregate",
			write: func(writer *Writer) error {
				return writer.WriteResults(Results{
					RunID: "run", Status: StatusPassed,
					Stages: []StageResult{{Name: "readiness", Attempt: 1, Status: StatusPassed, LogPath: "logs/missing.log"}},
					Suites: []SuiteResult{},
				})
			},
		},
		{
			name: "per-stage",
			write: func(writer *Writer) error {
				return writer.WriteStage(StageResult{Name: "readiness", Attempt: 1, Status: StatusPassed, LogPath: "logs/missing.log"})
			},
		},
		{
			name: "suite",
			write: func(writer *Writer) error {
				return writer.WriteResults(Results{
					RunID: "run", Status: StatusPassed, Stages: []StageResult{},
					Suites: []SuiteResult{{Name: "go-abi", Stage: "el1", Status: StatusPassed, LogPath: "logs/missing.log"}},
				})
			},
		},
		{
			name: "suite-history",
			write: func(writer *Writer) error {
				return writer.WriteResults(Results{
					RunID: "run", Status: StatusPassed, Stages: []StageResult{}, Suites: []SuiteResult{},
					SuiteHistory: []SuiteResult{{
						Name: "go-abi", Stage: "el1", Attempt: 1, Status: StatusFailed,
						FailureCategory: FailureAssertion, LogPath: "logs/missing-history.log",
					}},
				})
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			writer := newTestWriter(t)
			if _, err := writer.WriteManifest(ManifestMetadata{RunID: "run", GeneratedAt: testTime.Add(-time.Minute)}); err != nil {
				t.Fatal(err)
			}
			if err := test.write(writer); err != nil {
				t.Fatal(err)
			}
			if _, err := writer.WriteManifest(ManifestMetadata{RunID: "run", GeneratedAt: testTime}); err == nil || !strings.Contains(err.Error(), "not a manifest-inventoried regular artifact") {
				t.Fatalf("dangling log reference error = %v", err)
			}
			if _, err := os.Stat(writer.Layout().Manifest); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("failed reseal retained the prior manifest: %v", err)
			}
		})
	}
}

func TestJUnitContainsStageAndSuiteGranularity(t *testing.T) {
	writer := newTestWriter(t)
	results := Results{
		RunID: "run-junit", Status: StatusFailed, StartedAt: testTime, FinishedAt: testTime.Add(3750 * time.Millisecond), DurationMillis: 3750,
		Stages: []StageResult{
			{Name: "doctor", Attempt: 1, Status: StatusPassed, DurationMillis: 1000},
			{Name: "readiness", Attempt: 1, Status: StatusFailed, DurationMillis: 2000, FailureCategory: FailureTimeout, Message: "deadline"},
		},
		Suites: []SuiteResult{
			{Name: "abi", Stage: "el-1", Status: StatusFailed, DurationMillis: 500, FailureCategory: FailureAssertion, Message: "bad <topic> & key", Details: "wanted 64 bytes", LogPath: "logs/abi.log"},
			{Name: "clef", Stage: "el-1", Status: StatusSkipped, Message: "not configured"},
			{Name: "freshsync", Stage: "sync", Status: StatusCanceled, DurationMillis: 250, FailureCategory: FailureCancellation, Message: "interrupted"},
		},
	}
	if err := writer.WriteResults(results); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteJUnit(results); err != nil {
		t.Fatal(err)
	}
	data := readTestFile(t, writer.Layout().JUnit)
	if !bytes.HasPrefix(data, []byte(xml.Header)) || !bytes.HasSuffix(data, []byte("\n")) {
		t.Fatalf("JUnit lacks canonical XML framing: %q", data)
	}
	if bytes.Contains(data, []byte("bad <topic>")) || !bytes.Contains(data, []byte("bad &lt;topic&gt; &amp; key")) {
		t.Fatalf("JUnit did not escape failure text: %s", data)
	}
	var document junitSuites
	if err := xml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Tests != 5 || document.Failures != 1 || document.Errors != 2 || document.Skipped != 1 || document.Time != "3.750" {
		t.Fatalf("unexpected JUnit totals: %#v", document)
	}
	if len(document.Suites) != 2 || document.Suites[0].Name != "stages" || document.Suites[1].Name != "suites" {
		t.Fatalf("unexpected JUnit groups: %#v", document.Suites)
	}
	if document.Suites[0].Tests != 2 || document.Suites[0].Errors != 1 || document.Suites[0].Time != "3.000" {
		t.Fatalf("unexpected stage suite: %#v", document.Suites[0])
	}
	abi := document.Suites[1].Cases[0]
	if abi.Failure == nil || abi.Failure.Type != string(FailureAssertion) || abi.Failure.Body != "wanted 64 bytes" || abi.SystemOut != "artifact log: logs/abi.log" {
		t.Fatalf("assertion failure lost detail: %#v", abi)
	}
	if got := document.Timestamp; got != testTime.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("timestamp=%q, want UTC timestamp", got)
	}
	var decoded Results
	if err := json.Unmarshal(readTestFile(t, writer.Layout().Results), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Schema != SchemaVersion || len(decoded.Stages) != 2 || len(decoded.Suites) != 3 {
		t.Fatalf("unexpected aggregate results: %#v", decoded)
	}
}

func TestJUnitSuiteHistoryIsVisibleButNonGating(t *testing.T) {
	writer := newTestWriter(t)
	results := Results{
		RunID: "run-resumed", Status: StatusPassed,
		StartedAt: testTime, FinishedAt: testTime.Add(3 * time.Second), DurationMillis: 3000,
		Stages: []StageResult{{Name: "el1", Attempt: 2, Status: StatusPassed, DurationMillis: 1000}},
		Suites: []SuiteResult{{Name: "go-abi", Stage: "el1", Attempt: 2, Status: StatusPassed, DurationMillis: 500}},
		SuiteHistory: []SuiteResult{{
			Name: "go-abi", Stage: "el1", Attempt: 1, Status: StatusFailed,
			DurationMillis: 1500, FailureCategory: FailureAssertion,
			Message: "64-byte address mismatch", Details: "wanted full-width topic", LogPath: "logs/go-abi-attempt-1.log",
		}},
	}
	if err := writer.WriteJUnit(results); err != nil {
		t.Fatal(err)
	}
	data := readTestFile(t, writer.Layout().JUnit)
	var document junitSuites
	if err := xml.Unmarshal(data, &document); err != nil {
		t.Fatal(err)
	}
	if document.Tests != 3 || document.Failures != 0 || document.Errors != 0 || document.Skipped != 1 {
		t.Fatalf("resumed JUnit totals are gating on history: %#v", document)
	}
	if len(document.Suites) != 3 || document.Suites[2].Name != "suite-history" || len(document.Suites[2].Cases) != 1 {
		t.Fatalf("historical JUnit group = %#v", document.Suites)
	}
	historical := document.Suites[2].Cases[0]
	if historical.Name != "go-abi [attempt 1]" || historical.Skipped == nil || historical.Failure != nil || historical.Error != nil {
		t.Fatalf("historical case is not explicitly non-gating: %#v", historical)
	}
	for _, evidence := range []string{
		"historical status: failed", "historical failure category: assertion_failure",
		"historical message: 64-byte address mismatch", "historical details: wanted full-width topic",
		"artifact log: logs/go-abi-attempt-1.log",
	} {
		if !strings.Contains(historical.SystemOut, evidence) {
			t.Errorf("historical JUnit case lost %q: %#v", evidence, historical)
		}
	}
}

func TestDiagnosticReasonSupportsEveryRequiredCategory(t *testing.T) {
	writer := newTestWriter(t)
	categories := []FailureCategory{
		FailureAssertion, FailureTimeout, FailureCancellation, FailureProcessExit,
		FailureSDK, FailureInfrastructure, FailureCleanup,
	}
	for _, category := range categories {
		reason := DiagnosticReason{Category: category, At: testTime, Stage: "stage", Suite: "suite", Message: "failed", Details: map[string]string{"z": "last", "a": "first"}}
		if err := writer.WriteReason(reason); err != nil {
			t.Fatalf("category %s: %v", category, err)
		}
		var decoded DiagnosticReason
		if err := json.Unmarshal(readTestFile(t, writer.Layout().Reason), &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.Schema != SchemaVersion || decoded.Category != category || !decoded.At.Equal(testTime.UTC()) {
			t.Fatalf("reason=%#v", decoded)
		}
	}
	before := readTestFile(t, writer.Layout().Reason)
	for name, reason := range map[string]DiagnosticReason{
		"category": {Category: "unknown", At: testTime, Message: "failed"},
		"time":     {Category: FailureSDK, Message: "failed"},
		"message":  {Category: FailureSDK, At: testTime},
		"stage":    {Category: FailureSDK, At: testTime, Message: "failed", Stage: "../bad"},
	} {
		t.Run(name, func(t *testing.T) {
			if err := writer.WriteReason(reason); err == nil {
				t.Fatal("invalid reason accepted")
			}
			if after := readTestFile(t, writer.Layout().Reason); !bytes.Equal(after, before) {
				t.Fatal("invalid reason replaced valid reason")
			}
		})
	}
}

func TestTimelineRecorderIsConcurrentAndSnapshotsAreIsolated(t *testing.T) {
	recorder := NewTimelineRecorder("run-timeline", func() time.Time { return testTime })
	fields := map[string]any{"mutable": "original"}
	recorder.Record(TimelineEvent{Kind: "created", Fields: fields})
	fields["mutable"] = "changed"

	const workers = 64
	var wait sync.WaitGroup
	wait.Add(workers)
	for index := 0; index < workers; index++ {
		index := index
		go func() {
			defer wait.Done()
			recorder.Record(TimelineEvent{Kind: "worker", Message: string(rune('A' + index%26))})
		}()
	}
	wait.Wait()
	snapshot := recorder.Snapshot()
	if len(snapshot.Events) != workers+1 {
		t.Fatalf("events=%d, want %d", len(snapshot.Events), workers+1)
	}
	for index, event := range snapshot.Events {
		if event.Sequence != uint64(index+1) || !event.At.Equal(testTime.UTC()) {
			t.Fatalf("event %d=%#v", index, event)
		}
	}
	if snapshot.Events[0].Fields["mutable"] != "original" {
		t.Fatalf("record retained caller map: %#v", snapshot.Events[0].Fields)
	}
	snapshot.Events[0].Fields["mutable"] = "snapshot mutation"
	if got := recorder.Snapshot().Events[0].Fields["mutable"]; got != "original" {
		t.Fatalf("snapshot exposed recorder map: %v", got)
	}

	writer := newTestWriter(t)
	if err := writer.WriteTimeline(recorder.Snapshot()); err != nil {
		t.Fatal(err)
	}
	invalid := Timeline{RunID: "run", Events: []TimelineEvent{{Sequence: 2, At: testTime, Kind: "a"}, {Sequence: 1, At: testTime, Kind: "b"}}}
	if err := writer.WriteTimeline(invalid); err == nil {
		t.Fatal("non-increasing timeline accepted")
	}
}

func TestResumeTimelineRecorderAppendsWithoutRenumbering(t *testing.T) {
	previous := Timeline{
		Schema: SchemaVersion,
		RunID:  "run-resume",
		Events: []TimelineEvent{
			{Sequence: 1, At: testTime, Kind: "created"},
			{Sequence: 2, At: testTime.Add(time.Second), Kind: "stage-finished", Stage: "fixture", Status: StatusPassed},
		},
	}
	recorder, err := ResumeTimelineRecorder(previous, func() time.Time { return testTime.Add(2 * time.Second) })
	if err != nil {
		t.Fatal(err)
	}
	if sequence := recorder.Record(TimelineEvent{Kind: "resumed"}); sequence != 3 {
		t.Fatalf("resumed sequence = %d, want 3", sequence)
	}
	got := recorder.Snapshot()
	if len(got.Events) != 3 || got.Events[0].Kind != "created" || got.Events[2].Sequence != 3 {
		t.Fatalf("resumed timeline = %#v", got)
	}
	got.Events[0].Kind = "mutated"
	if recorder.Snapshot().Events[0].Kind != "created" {
		t.Fatal("resumed timeline snapshot aliases recorder state")
	}

	invalid := previous
	invalid.Events = append([]TimelineEvent(nil), previous.Events...)
	invalid.Events[1].Sequence = 1
	if _, err := ResumeTimelineRecorder(invalid, nil); err == nil {
		t.Fatal("non-increasing durable timeline was accepted")
	}
}
