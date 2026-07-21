// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package process

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunCapturesBoundedRedactedOutputWithoutLoggingInvocationSecrets(t *testing.T) {
	const secret = "temporary-password"
	var structured bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&structured, &slog.HandlerOptions{Level: slog.LevelDebug}))
	result, err := Run(t.Context(), Command{
		Path: "/bin/sh", Args: []string{"-c", `printf '%s' "$E2E_SECRET"; printf 'stderr-data' >&2`},
		Env: []string{"E2E_SECRET=" + secret}, Name: "helper-" + secret,
		Secrets: []string{secret}, Logger: logger, MaxOutputBytes: 12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Stdout) != "[REDACTED]" || string(result.Stderr) != "stderr-data" {
		t.Fatalf("stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
	if result.StdoutTruncated || result.StderrTruncated {
		t.Fatalf("unexpected truncation: %+v", result)
	}
	if strings.Contains(structured.String(), secret) || !strings.Contains(structured.String(), "helper-[REDACTED]") {
		t.Fatalf("structured log leaked or omitted redaction: %s", structured.String())
	}
}

func TestRunOutputLimitAndExitError(t *testing.T) {
	result, err := Run(t.Context(), Command{
		Path: "/bin/sh", Args: []string{"-c", "printf 123456789; exit 7"}, MaxOutputBytes: 4,
	})
	var exitError *ExitError
	if !errors.As(err, &exitError) || exitError.ExitCode() != 7 {
		t.Fatalf("error = %#v, want exit code 7", err)
	}
	if string(result.Stdout) != "1234" || !result.StdoutTruncated || result.ExitCode != 7 {
		t.Fatalf("result = %+v", result)
	}
}

func TestRunCancellationKillsCommand(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := Run(ctx, Command{Path: "/bin/sh", Args: []string{"-c", "sleep 30"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestManagedProcessPreservesRestrictedRedactedLog(t *testing.T) {
	const secret = "seed-phrase"
	logPath := filepath.Join(t.TempDir(), "nested", "managed.log")
	first, err := Start(t.Context(), ManagedCommand{
		Command: Command{Path: "/bin/sh", Args: []string{"-c", `printf 'first:%s\n' "$E2E_SECRET"`}, Env: []string{"E2E_SECRET=" + secret}, Secrets: []string{secret}},
		LogPath: logPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	second, err := Start(t.Context(), ManagedCommand{
		Command: Command{Path: "/bin/sh", Args: []string{"-c", "printf 'second\\n'"}},
		LogPath: logPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Wait(t.Context()); err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), secret) || string(payload) != "first:[REDACTED]\nsecond\n" {
		t.Fatalf("log = %q", payload)
	}
	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("log mode = %o, want 600", info.Mode().Perm())
	}
}

func TestRedactingWriterHandlesChunkBoundaries(t *testing.T) {
	var destination bytes.Buffer
	writer := newRedactingWriter(&destination, []string{"cross-boundary-secret"})
	for _, chunk := range []string{"before cross-", "boundary-", "secret after"} {
		if _, err := writer.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if got, want := destination.String(), "before [REDACTED] after"; got != want {
		t.Fatalf("redacted output = %q, want %q", got, want)
	}
}

func TestManagedContextCancellationIsReported(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	managed, err := Start(ctx, ManagedCommand{
		Command: Command{Path: "/bin/sh", Args: []string{"-c", "sleep 30"}, StopGrace: 10 * time.Millisecond},
		LogPath: filepath.Join(t.TempDir(), "cancel.log"),
	})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	waitCtx, waitCancel := context.WithTimeout(t.Context(), time.Second)
	defer waitCancel()
	if err := managed.Wait(waitCtx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait error = %v, want context canceled", err)
	}
}
