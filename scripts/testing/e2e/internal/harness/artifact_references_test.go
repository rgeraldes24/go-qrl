// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
)

func TestSealedStageLogReferencesExistAndAreInventoried(t *testing.T) {
	writer, err := report.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	stages := ownedStages(&Runtime{})
	stageNames := make([]string, 0, len(stages))
	for _, stage := range stages {
		stageNames = append(stageNames, stage.Name)
	}

	started := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	enclave := lifecycle.EnclaveRef{Name: "vm64-artifact-test", UUID: "00000000000000000000000000000001", Owned: true}
	store := lifecycle.Store{Path: writer.Layout().Checkpoint, StageOrder: stageNames, Now: func() time.Time { return started }}
	state := lifecycle.NewCheckpoint("run-artifact-references", harnessLifecycleSHA, harnessLifecycleDigest, writer.Root(), harnessLifecycleTreeID, enclave, started)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	for index, name := range stageNames {
		attemptStarted := started.Add(time.Duration(index) * time.Second)
		finished := attemptStarted.Add(time.Second)
		exitCode := 0
		state.Attempts = append(state.Attempts, lifecycle.Attempt{
			Stage: name, Attempt: 1, StartedAt: attemptStarted, FinishedAt: &finished, ExitCode: &exitCode,
		})
		state.Completed = append(state.Completed, name)
		state.UpdatedAt = finished
	}
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}

	expectedLogs := map[string]string{
		"validate":           "logs/doctor.log",
		"fixture":            "logs/fixture-attempt-1.log",
		"host-preflight":     "logs/host-preflight-attempt-1.log",
		"el1":                "logs/el1-attempt-1.log",
		"el2":                "logs/el2-attempt-1.log",
		"deposit":            "logs/deposit-attempt-1.log",
		"system-base":        "logs/system-base-attempt-1.log",
		"system-signer":      "logs/system-signer-attempt-1.log",
		"system-participant": "logs/system-participant-attempt-1.log",
		"fresh-snap":         "logs/fresh-snap-attempt-1.log",
		"fresh-full":         "logs/fresh-full-attempt-1.log",
	}
	for stageName, relative := range expectedLogs {
		logName := relative[len(report.LogsDirectory)+1 : len(relative)-len(filepath.Ext(relative))]
		if _, err := writer.WriteSuiteLog(logName, []byte("evidence for "+stageName+"\n")); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := writer.WriteKurtosisArtifact("package-output.json", []byte("{}\n")); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteTopology(map[string]any{"execution": []string{"el1", "el2"}}); err != nil {
		t.Fatal(err)
	}

	runtime := &Runtime{
		RunID: "run-artifact-references", Enclave: enclave, Writer: writer, Store: store, StartedAt: started,
		Dependencies: Dependencies{Now: func() time.Time { return started.Add(time.Hour) }},
		Timeline:     report.NewTimelineRecorder("run-artifact-references", func() time.Time { return started.Add(time.Hour) }),
	}
	for _, attempt := range state.Attempts {
		if err := runtime.writeProgress(stages, attempt); err != nil {
			t.Fatal(err)
		}
	}
	if err := runtime.writeAggregate(stages, report.StatusPassed); err != nil {
		t.Fatal(err)
	}
	manifest, err := writer.WriteManifest(report.ManifestMetadata{RunID: runtime.RunID, GeneratedAt: started.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}

	results, err := report.LoadResults(writer.Layout().Results)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := make(map[string]report.Artifact, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		artifacts[artifact.Path] = artifact
	}
	for _, result := range results.Stages {
		if want := expectedLogs[result.Name]; result.LogPath != want {
			t.Errorf("stage %s log path = %q, want %q", result.Name, result.LogPath, want)
		}
		assertInventoriedLogReference(t, writer, artifacts, result.Name, result.LogPath)

		payload, err := os.ReadFile(filepath.Join(writer.Layout().Stages, result.Name+".json"))
		if err != nil {
			t.Fatal(err)
		}
		var stageArtifact struct {
			Schema int `json:"schema"`
			report.StageResult
		}
		if err := json.Unmarshal(payload, &stageArtifact); err != nil {
			t.Fatal(err)
		}
		if stageArtifact.LogPath != result.LogPath {
			t.Errorf("stage artifact %s log path = %q, aggregate = %q", result.Name, stageArtifact.LogPath, result.LogPath)
		}
		assertInventoriedLogReference(t, writer, artifacts, result.Name+" stage artifact", stageArtifact.LogPath)
	}
}

func assertInventoriedLogReference(t *testing.T, writer *report.Writer, artifacts map[string]report.Artifact, name, relative string) {
	t.Helper()
	if relative == "" {
		return
	}
	payload, err := os.ReadFile(filepath.Join(writer.Root(), filepath.FromSlash(relative)))
	if err != nil {
		t.Fatalf("%s references missing log %q: %v", name, relative, err)
	}
	artifact, exists := artifacts[relative]
	if !exists {
		t.Fatalf("%s log %q is absent from manifest", name, relative)
	}
	digest := sha256.Sum256(payload)
	if artifact.SHA256 != hex.EncodeToString(digest[:]) || artifact.SizeBytes != int64(len(payload)) {
		t.Fatalf("%s log %q manifest entry = %+v", name, relative, artifact)
	}
}
