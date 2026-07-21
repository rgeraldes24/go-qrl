//go:build !windows

// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package process

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagedStopKillsWholeProcessGroup(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "descendant-survived")
	script := `(trap '' TERM; sleep 0.25; printf survived > "$E2E_MARKER") & wait`
	managed, err := Start(t.Context(), ManagedCommand{
		Command: Command{
			Path: "/bin/sh", Args: []string{"-c", script}, Env: []string{"E2E_MARKER=" + marker},
			StopGrace: 20 * time.Millisecond,
		},
		LogPath: filepath.Join(directory, "process.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	stopCtx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	if err := managed.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(350 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("descendant survived managed-process stop")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
