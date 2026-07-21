// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package process

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ManagedCommand extends Command with an append-only log path. Output is
// redacted before it reaches either the log or Output.
type ManagedCommand struct {
	Command
	LogPath string
	Output  io.Writer
}

// Managed is a long-running process whose entire process group is terminated
// on Stop or when the context passed to Start ends.
type Managed struct {
	cmd     commandHandle
	name    string
	pid     int
	logPath string
	logger  *slog.Logger
	grace   time.Duration
	output  *redactingWriter
	logFile *os.File
	done    chan struct{}

	mu               sync.Mutex
	waitError        error
	terminationCause error
	manualStop       bool
	terminateOnce    sync.Once
	terminationDone  chan struct{}
}

// commandHandle is the subset of exec.Cmd retained by Managed. Keeping the
// concrete command behind this small wrapper makes state access explicit.
type commandHandle struct {
	wait func() error
}

func Start(ctx context.Context, specification ManagedCommand) (*Managed, error) {
	if ctx == nil {
		return nil, errors.New("managed-process context is nil")
	}
	if specification.LogPath == "" {
		return nil, errors.New("managed process requires a log path")
	}
	cmd, name, err := buildCommand(specification.Command)
	if err != nil {
		return nil, err
	}
	grace := specification.StopGrace
	if grace == 0 {
		grace = DefaultStopGrace
	}
	if grace < 0 {
		return nil, errors.New("managed-process stop grace cannot be negative")
	}
	if err := os.MkdirAll(filepath.Dir(specification.LogPath), 0o700); err != nil {
		return nil, fmt.Errorf("create managed-process log directory: %w", err)
	}
	logFile, err := os.OpenFile(specification.LogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open managed-process log: %w", err)
	}
	if err := logFile.Chmod(0o600); err != nil {
		logFile.Close()
		return nil, fmt.Errorf("restrict managed-process log permissions: %w", err)
	}
	destination := io.Writer(logFile)
	if specification.Output != nil {
		destination = io.MultiWriter(destination, specification.Output)
	}
	redacted := newRedactingWriter(destination, specification.Secrets)
	cmd.Stdout = redacted
	cmd.Stderr = redacted

	log(specification.Logger, slog.LevelDebug, "starting managed process", "command", name, "log_path", redactString(specification.LogPath, specification.Secrets))
	if err := cmd.Start(); err != nil {
		_ = redacted.Close()
		_ = logFile.Close()
		return nil, fmt.Errorf("start managed process %s: %w", name, err)
	}
	managed := &Managed{
		cmd: commandHandle{wait: cmd.Wait}, name: name, pid: cmd.Process.Pid, logPath: specification.LogPath,
		logger: specification.Logger, grace: grace, output: redacted, logFile: logFile,
		done: make(chan struct{}), terminationDone: make(chan struct{}),
	}
	go managed.reap()
	go func() {
		select {
		case <-ctx.Done():
			select {
			case <-managed.done:
				return
			default:
			}
			managed.requestTermination(context.Cause(ctx), false)
		case <-managed.done:
		}
	}()
	return managed, nil
}

func (process *Managed) PID() int              { return process.pid }
func (process *Managed) LogPath() string       { return process.logPath }
func (process *Managed) Done() <-chan struct{} { return process.done }

// Wait waits for natural exit, cancellation, or a preceding Stop call.
func (process *Managed) Wait(ctx context.Context) error {
	if process == nil {
		return errors.New("managed process is nil")
	}
	if ctx == nil {
		return errors.New("managed-process wait context is nil")
	}
	select {
	case <-process.done:
		return process.resultError()
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// Stop sends a graceful signal to the whole process group, waits StopGrace,
// then sends a kill signal if any group member remains. If ctx expires first,
// a kill signal is sent immediately.
func (process *Managed) Stop(ctx context.Context) error {
	if process == nil {
		return errors.New("managed process is nil")
	}
	if ctx == nil {
		return errors.New("managed-process stop context is nil")
	}
	process.requestTermination(nil, true)
	for {
		select {
		case <-ctx.Done():
			_ = killProcessGroup(process.pid)
			return context.Cause(ctx)
		case <-process.done:
			select {
			case <-process.terminationDone:
				return process.resultError()
			case <-ctx.Done():
				_ = killProcessGroup(process.pid)
				return context.Cause(ctx)
			}
		case <-process.terminationDone:
			select {
			case <-process.done:
				return process.resultError()
			case <-ctx.Done():
				_ = killProcessGroup(process.pid)
				return context.Cause(ctx)
			}
		}
	}
}

func (process *Managed) requestTermination(cause error, manual bool) {
	process.mu.Lock()
	if cause != nil && process.terminationCause == nil {
		process.terminationCause = cause
	}
	if manual {
		process.manualStop = true
	}
	process.mu.Unlock()
	process.terminateOnce.Do(func() {
		go process.terminateGroup()
	})
}

func (process *Managed) terminateGroup() {
	defer close(process.terminationDone)
	log(process.logger, slog.LevelDebug, "stopping managed process", "command", process.name, "pid", process.pid)
	_ = terminateProcessGroup(process.pid)
	if !processGroupAlive(process.pid) {
		return
	}
	timer := time.NewTimer(process.grace)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer timer.Stop()
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if !processGroupAlive(process.pid) {
				return
			}
		case <-timer.C:
			_ = killProcessGroup(process.pid)
			return
		}
	}
}

func (process *Managed) reap() {
	waitError := process.cmd.wait()
	outputError := process.output.Close()
	logError := process.logFile.Close()
	if waitError == nil {
		waitError = errors.Join(outputError, logError)
	}
	process.mu.Lock()
	process.waitError = waitError
	process.mu.Unlock()
	close(process.done)
}

func (process *Managed) resultError() error {
	process.mu.Lock()
	defer process.mu.Unlock()
	if process.terminationCause != nil {
		return process.terminationCause
	}
	if process.manualStop {
		return nil
	}
	if process.waitError == nil {
		return nil
	}
	code := -1
	var exitCoder interface{ ExitCode() int }
	if errors.As(process.waitError, &exitCoder) {
		code = exitCoder.ExitCode()
	}
	return &ExitError{Name: process.name, Code: code, err: process.waitError}
}
