// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	systemSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/system"
)

func TestSystemStageTimeoutExceedsSuiteTimeout(t *testing.T) {
	suiteTimeout := systemSuite.DefaultConfig().Timeout
	expected := map[string]time.Duration{
		"system-base":        systemBaseTimeout,
		"system-signer":      systemSignerTimeout,
		"system-participant": systemParticipantTimeout,
	}
	stageSets := map[string][]lifecycle.Stage{
		"owned":               ownedStages(&Runtime{}),
		"borrowed-disruptive": borrowedStages(&Runtime{}, true),
	}
	for name, stages := range stageSets {
		t.Run(name, func(t *testing.T) {
			if got := assertSystemStageTimeouts(t, stages, suiteTimeout, expected); len(got) != len(expected) {
				t.Fatalf("checked system stages %v, want all three serialized phases", got)
			}
		})
	}
}

func TestSystemStageRefusesTruncatedGlobalBudget(t *testing.T) {
	now := time.Date(2026, 7, 22, 9, 0, 0, 0, time.UTC)
	var systemBase lifecycle.Stage
	for _, stage := range ownedStages(&Runtime{}) {
		if stage.Name == "system-base" {
			systemBase = stage
			break
		}
	}
	if systemBase.Name == "" {
		t.Fatal("owned stages have no system-base phase")
	}
	ran := false
	systemBase.Run = func(context.Context, *lifecycle.RunEnvironment) error {
		ran = true
		return nil
	}
	root := t.TempDir()
	store := lifecycle.Store{
		Path:       filepath.Join(root, "checkpoint.json"),
		StageOrder: []string{systemBase.Name},
		Now:        func() time.Time { return now },
	}
	enclave := lifecycle.EnclaveRef{Name: "vm64-system-budget", UUID: "00000000000000000000000000000001", Owned: true}
	state := lifecycle.NewCheckpoint("run-system-budget", harnessLifecycleSHA, harnessLifecycleDigest, root, harnessLifecycleTreeID, enclave, now)
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	cleanupReserve := time.Hour
	runner := lifecycle.Runner{
		Store: store, Stages: []lifecycle.Stage{systemBase},
		GlobalDeadline: now.Add(cleanupReserve + systemBase.Timeout - time.Second),
		CleanupReserve: cleanupReserve, AllowDisruptive: true,
		Now: func() time.Time { return now },
	}
	err := runner.Run(context.Background(), &lifecycle.RunEnvironment{Enclave: enclave})
	if err == nil || !strings.Contains(err.Error(), "refusing stage system-base") {
		t.Fatalf("truncated system-stage budget error = %v", err)
	}
	if ran {
		t.Fatal("system-base callback ran with less than its full wrapper budget")
	}
}

func TestBorrowedNonDisruptiveStagesExcludeSystemPhases(t *testing.T) {
	for _, stage := range borrowedStages(&Runtime{}, false) {
		if strings.HasPrefix(stage.Name, "system-") {
			t.Fatalf("non-disruptive borrowed stages include %q", stage.Name)
		}
	}
}

func assertSystemStageTimeouts(t *testing.T, stages []lifecycle.Stage, suiteTimeout time.Duration, expected map[string]time.Duration) []string {
	t.Helper()
	var checked []string
	for _, stage := range stages {
		want, ok := expected[stage.Name]
		if !ok {
			continue
		}
		checked = append(checked, stage.Name)
		if stage.Timeout != want {
			t.Errorf("stage %s timeout = %s, want %s", stage.Name, stage.Timeout, want)
		}
		if stage.Timeout <= suiteTimeout {
			t.Errorf("stage %s timeout %s must exceed system suite timeout %s", stage.Name, stage.Timeout, suiteTimeout)
		}
		if stage.MinimumRuntime != stage.Timeout {
			t.Errorf("stage %s minimum runtime %s must equal wrapper timeout %s so the global deadline cannot truncate it", stage.Name, stage.MinimumRuntime, stage.Timeout)
		}
	}
	return checked
}
