// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package process runs E2E helper commands without exposing arguments or
// environment values in structured logs. Every started command owns a process
// group so cancellation can terminate descendants as well as the group leader.
package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

const (
	DefaultMaxOutputBytes = int64(4 << 20)
	DefaultStopGrace      = 2 * time.Second
)

// Command describes a process. Args and Env are deliberately never logged.
// Env entries are appended to the host environment, so later duplicate keys
// override inherited values in the usual os/exec manner.
type Command struct {
	Path           string
	Args           []string
	Dir            string
	Env            []string
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
	Name           string
	Logger         *slog.Logger
	Secrets        []string
	MaxOutputBytes int64
	StopGrace      time.Duration
}

type Result struct {
	ExitCode        int
	Stdout          []byte
	Stderr          []byte
	StdoutTruncated bool
	StderrTruncated bool
	StartedAt       time.Time
	FinishedAt      time.Time
}

// ExitError reports a non-zero process exit without echoing arguments or
// environment variables.
type ExitError struct {
	Name string
	Code int
	err  error
}

func (e *ExitError) Error() string {
	return fmt.Sprintf("command %s exited with code %d", e.Name, e.Code)
}

func (e *ExitError) Unwrap() error { return e.err }
func (e *ExitError) ExitCode() int { return e.Code }

// Run executes a one-shot command and captures bounded, redacted output.
func Run(ctx context.Context, specification Command) (Result, error) {
	if ctx == nil {
		return Result{ExitCode: -1}, errors.New("command context is nil")
	}
	cmd, name, err := buildCommand(specification)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	limit := specification.MaxOutputBytes
	if limit == 0 {
		limit = DefaultMaxOutputBytes
	}
	if limit < 0 {
		return Result{ExitCode: -1}, errors.New("maximum command output cannot be negative")
	}
	stdout := &cappedBuffer{limit: limit}
	stderr := &cappedBuffer{limit: limit}
	stdoutTarget := io.Writer(stdout)
	stderrTarget := io.Writer(stderr)
	if specification.Stdout != nil {
		stdoutTarget = io.MultiWriter(stdoutTarget, specification.Stdout)
	}
	if specification.Stderr != nil {
		stderrTarget = io.MultiWriter(stderrTarget, specification.Stderr)
	}
	redactedStdout := newRedactingWriter(stdoutTarget, specification.Secrets)
	redactedStderr := newRedactingWriter(stderrTarget, specification.Secrets)
	cmd.Stdout = redactedStdout
	cmd.Stderr = redactedStderr

	result := Result{ExitCode: -1, StartedAt: time.Now().UTC()}
	log(specification.Logger, slog.LevelDebug, "starting command", "command", name)
	if err := cmd.Start(); err != nil {
		_ = redactedStdout.Close()
		_ = redactedStderr.Close()
		result.FinishedAt = time.Now().UTC()
		return result, fmt.Errorf("start command %s: %w", name, err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()

	var waitError error
	select {
	case waitError = <-waited:
	case <-ctx.Done():
		_ = killProcessGroup(cmd.Process.Pid)
		waitError = <-waited
		_ = redactedStdout.Close()
		_ = redactedStderr.Close()
		result = finishResult(result, cmd, stdout, stderr)
		log(specification.Logger, slog.LevelDebug, "command canceled", "command", name, "exit_code", result.ExitCode)
		return result, context.Cause(ctx)
	}
	stdoutCloseError := redactedStdout.Close()
	stderrCloseError := redactedStderr.Close()
	result = finishResult(result, cmd, stdout, stderr)
	if waitError == nil {
		waitError = errors.Join(stdoutCloseError, stderrCloseError)
	}
	if waitError != nil {
		exitError := &ExitError{Name: name, Code: result.ExitCode, err: waitError}
		log(specification.Logger, slog.LevelDebug, "command exited", "command", name, "exit_code", result.ExitCode)
		return result, exitError
	}
	log(specification.Logger, slog.LevelDebug, "command completed", "command", name, "exit_code", result.ExitCode)
	return result, nil
}

func buildCommand(specification Command) (*exec.Cmd, string, error) {
	if specification.Path == "" {
		return nil, "", errors.New("command path is required")
	}
	name := specification.Name
	if name == "" {
		name = filepath.Base(specification.Path)
	}
	name = redactString(name, specification.Secrets)
	cmd := exec.Command(specification.Path, specification.Args...)
	cmd.Dir = specification.Dir
	cmd.Env = append(os.Environ(), specification.Env...)
	cmd.Stdin = specification.Stdin
	configureProcessGroup(cmd)
	return cmd, name, nil
}

func finishResult(result Result, cmd *exec.Cmd, stdout, stderr *cappedBuffer) Result {
	result.FinishedAt = time.Now().UTC()
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	result.Stdout = stdout.Bytes()
	result.Stderr = stderr.Bytes()
	result.StdoutTruncated = stdout.Truncated()
	result.StderrTruncated = stderr.Truncated()
	return result
}

func log(logger *slog.Logger, level slog.Level, message string, args ...any) {
	if logger != nil {
		logger.Log(context.Background(), level, message, args...)
	}
}

type cappedBuffer struct {
	mu        sync.Mutex
	data      []byte
	limit     int64
	truncated bool
}

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	remaining := buffer.limit - int64(len(buffer.data))
	if remaining > 0 {
		count := int64(len(data))
		if count > remaining {
			count = remaining
		}
		buffer.data = append(buffer.data, data[:int(count)]...)
	}
	if int64(len(data)) > remaining {
		buffer.truncated = true
	}
	return len(data), nil
}

func (buffer *cappedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.data...)
}

func (buffer *cappedBuffer) Truncated() bool {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.truncated
}
