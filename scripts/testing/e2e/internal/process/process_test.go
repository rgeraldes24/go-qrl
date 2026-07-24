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
	"runtime"
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

func TestRunFiltersInheritedEnvironmentBeforeApplyingExplicitValues(t *testing.T) {
	t.Setenv("E2E_INHERITED_SECRET", "must-not-reach-child")
	t.Setenv("UNRELATED_INHERITED", "preserved")
	result, err := Run(t.Context(), Command{
		Path:              "/bin/sh",
		Args:              []string{"-c", `printf '%s|%s|%s' "$E2E_INHERITED_SECRET" "$E2E_EXPLICIT" "$UNRELATED_INHERITED"`},
		Env:               []string{"E2E_EXPLICIT=declared"},
		EnvRemovePrefixes: []string{"E2E_"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(result.Stdout), "|declared|preserved"; got != want {
		t.Fatalf("filtered environment = %q, want %q", got, want)
	}
}

func TestRunPreservesStdinAndStreamingWritersAfterRedaction(t *testing.T) {
	const secret = "stdin-secret"
	var streamedStdout, streamedStderr bytes.Buffer
	result, err := Run(t.Context(), Command{
		Path:   "/bin/sh",
		Args:   []string{"-c", `read value; printf 'out:%s' "$value"; printf 'err:%s' "$value" >&2`},
		Stdin:  strings.NewReader("value-" + secret + "\n"),
		Stdout: &streamedStdout,
		Stderr: &streamedStderr,
		Secrets: []string{
			secret,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(result.Stdout), "out:value-[REDACTED]"; got != want {
		t.Fatalf("captured stdout = %q, want %q", got, want)
	}
	if got, want := string(result.Stderr), "err:value-[REDACTED]"; got != want {
		t.Fatalf("captured stderr = %q, want %q", got, want)
	}
	if streamedStdout.String() != string(result.Stdout) ||
		streamedStderr.String() != string(result.Stderr) {
		t.Fatalf(
			"streamed stdout/stderr = %q/%q, captured = %q/%q",
			streamedStdout.String(),
			streamedStderr.String(),
			result.Stdout,
			result.Stderr,
		)
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

func TestRunReturnsCancellationCauseBeforeStart(t *testing.T) {
	cause := errors.New("planned cancellation")
	ctx, cancel := context.WithCancelCause(t.Context())
	cancel(cause)
	result, err := Run(ctx, Command{
		Path: "/bin/sh",
		Args: []string{"-c", "exit 0"},
	})
	if !errors.Is(err, cause) {
		t.Fatalf("error = %v, want cancellation cause", err)
	}
	if result.ExitCode != -1 {
		t.Fatalf("exit code = %d, want -1", result.ExitCode)
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

func TestRunCancellationKillsProcessGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix process groups")
	}
	marker := filepath.Join(t.TempDir(), "descendant-survived")
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	_, err := Run(ctx, Command{
		Path: "/bin/sh",
		Args: []string{
			"-c",
			`(sleep 0.25; printf survived > "$E2E_MARKER") & wait`,
		},
		Env: []string{"E2E_MARKER=" + marker},
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	time.Sleep(350 * time.Millisecond)
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("descendant survived command cancellation")
	} else if !os.IsNotExist(err) {
		t.Fatal(err)
	}
}
