// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package source identifies and validates the checkout used by an E2E lifecycle.
package source

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Runner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
}

// FindRepoRoot walks upward from start until it finds the go-qrl module and
// its E2E directory. The returned path is absolute and clean; callers that
// require a canonical existing directory perform their own symlink checks.
func FindRepoRoot(start string) (string, error) {
	current, err := filepath.Abs(start)
	if err != nil {
		return "", fmt.Errorf("resolve repository search path: %w", err)
	}
	for {
		goModule := filepath.Join(current, "go.mod")
		e2eDirectory := filepath.Join(current, "scripts", "testing", "e2e")
		if moduleInfo, moduleErr := os.Stat(goModule); moduleErr == nil && moduleInfo.Mode().IsRegular() {
			if e2eInfo, e2eErr := os.Stat(e2eDirectory); e2eErr == nil && e2eInfo.IsDir() {
				return filepath.Clean(current), nil
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("could not find go-qrl repository root from %s", start)
		}
		current = parent
	}
}

func Commit(ctx context.Context, runner Runner, repoRoot string) (string, error) {
	if runner == nil {
		runner = ExecRunner{}
	}
	output, err := runner.Output(ctx, "git", "-C", repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve source commit: %w", err)
	}
	commit := strings.TrimSpace(string(output))
	if len(commit) != 40 {
		return "", fmt.Errorf("resolved source commit %q is not exact", commit)
	}
	return commit, nil
}

// ValidateE2EOnlyTreeDrift accepts an existing network only when every
// tracked, staged, and untracked working-tree change is below
// scripts/testing/e2e.
// Rename detection is disabled so moving a runtime file into the allowed tree
// still exposes the deletion outside it.
func ValidateE2EOnlyTreeDrift(ctx context.Context, runner Runner, repoRoot string) error {
	if runner == nil {
		runner = ExecRunner{}
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return err
	}
	const excluded = ":(exclude)scripts/testing/e2e/**"
	checks := []struct {
		description string
		arguments   []string
	}{
		{
			description: "tracked or staged",
			arguments:   []string{"-C", root, "diff", "--binary", "--no-renames", "HEAD", "--", ".", excluded},
		},
		{
			description: "untracked",
			arguments:   []string{"-C", root, "ls-files", "--others", "--exclude-standard", "-z", "--", ".", excluded},
		},
	}
	for _, check := range checks {
		payload, runErr := runner.Output(ctx, "git", check.arguments...)
		if runErr != nil {
			return fmt.Errorf("audit %s checkout changes: %w", check.description, runErr)
		}
		if len(payload) != 0 {
			return fmt.Errorf("checkout has %s changes outside scripts/testing/e2e", check.description)
		}
	}
	return nil
}
