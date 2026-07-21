// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
)

type resumeEvidenceDumper struct{}

func (resumeEvidenceDumper) Dump(context.Context, string, string) ([]byte, error) {
	return []byte("resume evidence dump\n"), nil
}

func TestSuccessfulResumeSupersedesFailedSuiteEvidence(t *testing.T) {
	started := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	failedAt := started.Add(time.Second)
	passedAt := failedAt.Add(time.Second)
	exitFailure, exitSuccess := 1, 0
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	enclave := lifecycle.EnclaveRef{Name: "resume-suite-history", UUID: "00000000000000000000000000000001", Owned: true}
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: []string{"el1"}, Now: func() time.Time { return passedAt }}
	state := lifecycle.NewCheckpoint("run-suite-history", harnessLifecycleSHA, harnessLifecycleDigest, writer.Root(), harnessLifecycleTreeID, enclave, started)
	state.Attempts = []lifecycle.Attempt{
		{Stage: "el1", Attempt: 1, StartedAt: started, FinishedAt: &failedAt, ExitCode: &exitFailure, FailureCategory: lifecycle.FailureAssertion, FailureMessage: "old assertion"},
		{Stage: "el1", Attempt: 2, StartedAt: failedAt, FinishedAt: &passedAt, ExitCode: &exitSuccess},
	}
	state.Completed = []string{"el1"}
	state.UpdatedAt = passedAt
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		RunID: "run-suite-history", Enclave: enclave, Writer: writer, Store: store, StartedAt: started,
		Dependencies: Dependencies{Now: func() time.Time { return passedAt }},
		SuiteResults: []report.SuiteResult{{
			Name: "go-abi", Stage: "el1", Attempt: 1, Status: report.StatusFailed,
			FailureCategory: report.FailureAssertion, Message: "old assertion", Details: "wanted 64 bytes",
		}},
		Timeline: report.NewTimelineRecorder("run-suite-history", func() time.Time { return passedAt }),
	}
	runtime.recordSuiteResult(report.SuiteResult{Name: "go-abi", Stage: "el1", Status: report.StatusPassed})
	if err := runtime.writeAggregate([]lifecycle.Stage{{Name: "el1"}}, report.StatusPassed); err != nil {
		t.Fatal(err)
	}
	results, err := report.LoadResults(writer.Layout().Results)
	if err != nil {
		t.Fatal(err)
	}
	if results.Status != report.StatusPassed || len(results.Suites) != 1 || results.Suites[0].Status != report.StatusPassed || results.Suites[0].Attempt != 2 {
		t.Fatalf("active resumed suite evidence = %+v", results)
	}
	if len(results.SuiteHistory) != 1 || results.SuiteHistory[0].Status != report.StatusFailed || results.SuiteHistory[0].Attempt != 1 || results.SuiteHistory[0].Details != "wanted 64 bytes" {
		t.Fatalf("historical resumed suite evidence = %+v", results.SuiteHistory)
	}
}

func TestResumeRestoresActiveAndHistoricalSuiteEvidence(t *testing.T) {
	previous := report.Results{
		Suites:       []report.SuiteResult{{Name: "go-abi", Stage: "el1", Attempt: 2, Status: report.StatusPassed}},
		SuiteHistory: []report.SuiteResult{{Name: "go-abi", Stage: "el1", Attempt: 1, Status: report.StatusFailed, FailureCategory: report.FailureAssertion, Details: "old evidence"}},
	}
	runtime := &Runtime{}
	runtime.restoreSuiteEvidence(previous)
	previous.Suites[0].Name = "mutated"
	previous.SuiteHistory[0].Details = "mutated"
	if len(runtime.SuiteResults) != 1 || runtime.SuiteResults[0].Name != "go-abi" {
		t.Fatalf("active suite evidence was not independently restored: %+v", runtime.SuiteResults)
	}
	if len(runtime.SuiteHistory) != 1 || runtime.SuiteHistory[0].Details != "old evidence" {
		t.Fatalf("historical suite evidence was not independently restored: %+v", runtime.SuiteHistory)
	}
}

func TestResumeRepairsCompletedStageAfterInterruptedEvidenceCallback(t *testing.T) {
	fixture := newOwnedResumeFixture(t, "")
	state, err := fixture.store.Load()
	if err != nil {
		t.Fatal(err)
	}
	started := state.CreatedAt
	finished := started
	exitFailure, exitSuccess := 1, 0
	state.Attempts = nil
	for _, stageName := range fixture.store.StageOrder {
		attemptStarted := finished.Add(time.Second)
		attemptFinished := attemptStarted.Add(time.Second)
		if stageName == fixture.store.StageOrder[len(fixture.store.StageOrder)-1] {
			failedAt := attemptFinished
			state.Attempts = append(state.Attempts, lifecycle.Attempt{
				Stage: stageName, Attempt: 1, StartedAt: attemptStarted, FinishedAt: &failedAt,
				ExitCode: &exitFailure, FailureCategory: lifecycle.FailureAssertion,
				FailureMessage: "suite failed before retry",
			})
			attemptStarted = failedAt.Add(time.Second)
			attemptFinished = attemptStarted.Add(time.Second)
			state.Attempts = append(state.Attempts, lifecycle.Attempt{
				Stage: stageName, Attempt: 2, StartedAt: attemptStarted, FinishedAt: &attemptFinished, ExitCode: &exitSuccess,
			})
		} else {
			state.Attempts = append(state.Attempts, lifecycle.Attempt{
				Stage: stageName, Attempt: 1, StartedAt: attemptStarted, FinishedAt: &attemptFinished, ExitCode: &exitSuccess,
			})
		}
		finished = attemptFinished
	}
	state.Completed = append([]string(nil), fixture.store.StageOrder...)
	state.Status = lifecycle.StatusRunning
	state.CurrentStage = nil
	state.FailureCategory = lifecycle.FailureNone
	state.FailureMessage = ""
	state.UpdatedAt = finished
	if err := fixture.store.Save(state); err != nil {
		t.Fatal(err)
	}
	writer, err := report.New(filepath.Dir(fixture.options.CheckpointPath))
	if err != nil {
		t.Fatal(err)
	}
	completedStage := fixture.store.StageOrder[len(fixture.store.StageOrder)-1]
	if err := writer.WriteResults(report.Results{
		RunID: "run-resume", Status: report.StatusRunning, StartedAt: started,
		Stages: []report.StageResult{},
		Suites: []report.SuiteResult{{
			Name: "interrupted-suite", Stage: completedStage, Attempt: 1, Status: report.StatusFailed,
			FailureCategory: report.FailureAssertion, Message: "stale active failure",
		}},
	}); err != nil {
		t.Fatal(err)
	}
	oldManifest, err := writer.WriteManifest(report.ManifestMetadata{RunID: "run-resume", GeneratedAt: started})
	if err != nil {
		t.Fatal(err)
	}
	fixture.dependencies.Dumper = resumeEvidenceDumper{}
	outcome, err := Resume(t.Context(), fixture.options, fixture.dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if outcome.Status != lifecycle.StatusCompleteAfterResume {
		t.Fatalf("resume outcome = %+v", outcome)
	}
	results, err := report.LoadResults(writer.Layout().Results)
	if err != nil {
		t.Fatal(err)
	}
	if results.Status != report.StatusPassed || len(results.Suites) != 0 {
		t.Fatalf("completed-stage resume retained contradictory active evidence: %+v", results)
	}
	if len(results.SuiteHistory) != 1 || results.SuiteHistory[0].Name != "interrupted-suite" || results.SuiteHistory[0].Status != report.StatusFailed {
		t.Fatalf("completed-stage resume lost historical failure: %+v", results.SuiteHistory)
	}
	newManifest, err := os.ReadFile(writer.Layout().Manifest)
	if err != nil {
		t.Fatal(err)
	}
	var resealed report.Manifest
	if err := json.Unmarshal(newManifest, &resealed); err != nil {
		t.Fatal(err)
	}
	if resealed.GeneratedAt.Equal(oldManifest.GeneratedAt) {
		t.Fatalf("resume did not replace the prior artifact seal: %+v", resealed)
	}
}

func TestReconciledResumeRetiresUnreplayedFailedSuite(t *testing.T) {
	started := time.Date(2026, 7, 21, 13, 0, 0, 0, time.UTC)
	failedAt := started.Add(time.Second)
	reconciledAt := failedAt.Add(time.Second)
	exitFailure, exitSuccess := 1, 0
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	enclave := lifecycle.EnclaveRef{Name: "resume-reconciled-history", UUID: "00000000000000000000000000000001", Owned: true}
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: []string{"system-base"}, Now: func() time.Time { return reconciledAt }}
	state := lifecycle.NewCheckpoint("run-reconciled-history", harnessLifecycleSHA, harnessLifecycleDigest, writer.Root(), harnessLifecycleTreeID, enclave, started)
	state.Attempts = []lifecycle.Attempt{
		{Stage: "system-base", Attempt: 1, StartedAt: started, FinishedAt: &failedAt, ExitCode: &exitFailure, FailureCategory: lifecycle.FailureTimeout, FailureMessage: "receipt wait timed out"},
		{Stage: "system-base", Attempt: 2, StartedAt: reconciledAt, FinishedAt: &reconciledAt, ExitCode: &exitSuccess, Reconciled: true},
	}
	state.Completed = []string{"system-base"}
	state.UpdatedAt = reconciledAt
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	runtime := &Runtime{
		RunID: "run-reconciled-history", Enclave: enclave, Writer: writer, Store: store, StartedAt: started,
		Dependencies: Dependencies{Now: func() time.Time { return reconciledAt }},
		SuiteResults: []report.SuiteResult{{
			Name: "system-base", Stage: "system-base", Attempt: 1, Status: report.StatusFailed,
			FailureCategory: report.FailureTimeout, Message: "receipt wait timed out",
		}},
		Timeline: report.NewTimelineRecorder("run-reconciled-history", func() time.Time { return reconciledAt }),
	}
	stages := []lifecycle.Stage{{Name: "system-base"}}
	if err := runtime.writeProgress(stages, state.Attempts[1]); err != nil {
		t.Fatal(err)
	}
	if err := runtime.writeAggregate(stages, report.StatusPassed); err != nil {
		t.Fatal(err)
	}
	results, err := report.LoadResults(writer.Layout().Results)
	if err != nil {
		t.Fatal(err)
	}
	if len(results.Suites) != 0 {
		t.Fatalf("reconciled pass retained active failed suites: %+v", results.Suites)
	}
	if len(results.SuiteHistory) != 1 || results.SuiteHistory[0].Status != report.StatusFailed || results.SuiteHistory[0].Attempt != 1 {
		t.Fatalf("reconciled historical evidence = %+v", results.SuiteHistory)
	}
}
