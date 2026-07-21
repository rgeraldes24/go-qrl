// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package source fingerprints the exact checkout used by an E2E lifecycle.
package source

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
)

type Runner interface {
	Output(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).Output()
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

// TreeID is compatible with the legacy Python schema-v1 implementation. It
// includes HEAD, tracked changes, and the bytes or symlink target of every
// untracked file so resume cannot silently switch implementation mid-run.
func TreeID(ctx context.Context, runner Runner, repoRoot string) (string, error) {
	if runner == nil {
		runner = ExecRunner{}
	}
	root, err := filepath.Abs(repoRoot)
	if err != nil {
		return "", err
	}
	commands := []struct {
		label string
		args  []string
	}{
		{"HEAD\x00", []string{"-C", root, "rev-parse", "HEAD"}},
		{"STATUS\x00", []string{"-C", root, "status", "--porcelain=v1", "-z", "--untracked-files=all"}},
		{"DIFF\x00", []string{"-C", root, "diff", "--binary", "HEAD"}},
	}
	digest := sha256.New()
	for _, command := range commands {
		payload, runErr := runner.Output(ctx, "git", command.args...)
		if runErr != nil {
			return "", fmt.Errorf("fingerprint Git %s: %w", strings.TrimSuffix(command.label, "\x00"), runErr)
		}
		digest.Write([]byte(command.label))
		digest.Write(payload)
	}
	untrackedRaw, err := runner.Output(ctx, "git", "-C", root, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return "", fmt.Errorf("list untracked files: %w", err)
	}
	untracked := strings.Split(string(untrackedRaw), "\x00")
	untracked = slices.DeleteFunc(untracked, func(value string) bool { return value == "" })
	slices.Sort(untracked)
	for _, relative := range untracked {
		if filepath.IsAbs(relative) || strings.HasPrefix(filepath.Clean(relative), ".."+string(filepath.Separator)) {
			return "", errors.New("Git returned an unsafe untracked path")
		}
		path := filepath.Join(root, relative)
		digest.Write([]byte("UNTRACKED\x00"))
		digest.Write([]byte(relative))
		digest.Write([]byte{0})
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			digest.Write([]byte("SYMLINK\x00"))
			digest.Write([]byte(target))
			continue
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("untracked path %s is not a regular file or symlink", relative)
		}
		payload, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		digest.Write(payload)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}
