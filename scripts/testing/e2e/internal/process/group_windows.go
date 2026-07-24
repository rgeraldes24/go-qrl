//go:build windows

// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package process

import (
	"errors"
	"os"
	"os/exec"
)

// Windows does not expose Unix process groups through os/exec. This fallback
// terminates the direct process; Windows CI should run helpers in a Job Object
// before relying on descendant-process guarantees.
func configureProcessGroup(_ *exec.Cmd) {}

func killProcessGroup(pid int) error {
	if pid <= 0 {
		return os.ErrProcessDone
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	err = process.Kill()
	if errors.Is(err, os.ErrProcessDone) {
		return os.ErrProcessDone
	}
	return err
}
