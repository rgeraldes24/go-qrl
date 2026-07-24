// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	networkCommandOutputLimit = int64(4 << 20)
	networkCommandWaitDelay   = 2 * time.Second
)

type command struct {
	Path, Dir string
	Args      []string
	Env       []string
	Stdout    io.Writer
	Stderr    io.Writer
}

type commandRunner interface {
	Run(context.Context, command) error
	CombinedOutput(context.Context, command) ([]byte, error)
}

type execRunner struct{}

type commandResult struct {
	stdout []byte
	stderr []byte
}

func (execRunner) Run(ctx context.Context, specification command) error {
	_, err := runNetworkCommand(ctx, specification)
	return err
}

func (execRunner) CombinedOutput(ctx context.Context, specification command) ([]byte, error) {
	result, err := runNetworkCommand(ctx, specification)
	output := make([]byte, 0, len(result.stdout)+len(result.stderr))
	output = append(output, result.stdout...)
	output = append(output, result.stderr...)
	return output, err
}

func runNetworkCommand(ctx context.Context, specification command) (commandResult, error) {
	if ctx == nil {
		return commandResult{}, errors.New("command context is nil")
	}
	if specification.Path == "" {
		return commandResult{}, errors.New("command path is required")
	}
	cmd := exec.CommandContext(ctx, specification.Path, specification.Args...)
	cmd.Dir = specification.Dir
	cmd.Env = networkCommandEnvironment(specification.Env)
	configureNetworkCommandGroup(cmd)

	stdout := &cappedBuffer{limit: networkCommandOutputLimit}
	stderr := &cappedBuffer{limit: networkCommandOutputLimit}
	cmd.Stdout = teeOutput(stdout, specification.Stdout)
	cmd.Stderr = teeOutput(stderr, specification.Stderr)

	var canceled atomic.Bool
	cmd.Cancel = func() error {
		err := killNetworkCommandGroup(cmd.Process.Pid)
		if !errors.Is(err, os.ErrProcessDone) {
			canceled.Store(true)
		}
		return err
	}
	cmd.WaitDelay = networkCommandWaitDelay
	waitError := cmd.Run()
	result := commandResult{stdout: stdout.Bytes(), stderr: stderr.Bytes()}
	if canceled.Load() ||
		cmd.ProcessState == nil && context.Cause(ctx) != nil &&
			errors.Is(waitError, ctx.Err()) {
		return result, context.Cause(ctx)
	}
	name := filepath.Base(specification.Path)
	if cmd.ProcessState == nil && waitError != nil {
		return result, fmt.Errorf("start command %s: %w", name, waitError)
	}
	if waitError != nil {
		return result, &commandExitError{
			name: name,
			code: cmd.ProcessState.ExitCode(),
			err:  waitError,
		}
	}
	return result, nil
}

func teeOutput(capture io.Writer, stream io.Writer) io.Writer {
	if stream == nil {
		return capture
	}
	return io.MultiWriter(capture, stream)
}

func networkCommandEnvironment(explicit []string) []string {
	inherited := os.Environ()
	environment := make([]string, 0, len(inherited)+len(explicit))
	for _, entry := range inherited {
		name, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(name, "E2E_") {
			environment = append(environment, entry)
		}
	}
	return append(environment, explicit...)
}

type commandExitError struct {
	name string
	code int
	err  error
}

func (err *commandExitError) Error() string {
	return fmt.Sprintf("command %s exited with code %d", err.name, err.code)
}

func (err *commandExitError) Unwrap() error { return err.err }

type cappedBuffer struct {
	mu    sync.Mutex
	data  []byte
	limit int64
}

func (buffer *cappedBuffer) Write(data []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	if remaining := buffer.limit - int64(len(buffer.data)); remaining > 0 {
		count := min(int64(len(data)), remaining)
		buffer.data = append(buffer.data, data[:count]...)
	}
	return len(data), nil
}

func (buffer *cappedBuffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return append([]byte(nil), buffer.data...)
}
