// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gofrs/flock"
)

// MutationLease prevents network lifecycle commands and live suites from
// mutating one network concurrently. The lease is anchored in private network
// state, so every invocation contends for the same lock.
type MutationLease struct {
	networkDir string
	lock       *flock.Flock
}

// AcquireMutationLease takes the network's non-blocking exclusive mutation
// lease. The network directory must already exist.
func AcquireMutationLease(networkDir string) (*MutationLease, error) {
	canonical, err := canonicalExistingDirectory(networkDir, "network directory")
	if err != nil {
		return nil, err
	}
	return acquireMutationLease(canonical)
}

func acquireMutationLease(networkDir string) (*MutationLease, error) {
	privateDir := privatePath(networkDir)
	privateInfo, err := os.Lstat(privateDir)
	if err != nil {
		return nil, fmt.Errorf("inspect private network state: %w", err)
	}
	if privateInfo.Mode()&os.ModeSymlink != 0 || !privateInfo.IsDir() {
		return nil, errors.New("private network state must be a non-symlink directory")
	}
	path := filepath.Join(privatePath(networkDir), "mutation.lock")
	if err := validateMutationLockPath(path, true); err != nil {
		return nil, err
	}
	fileLock := flock.New(path)
	locked, err := fileLock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("acquire network mutation lease: %w", err)
	}
	if !locked {
		_ = fileLock.Close()
		return nil, errors.New("network mutation is already in progress")
	}
	closeOnError := func(cause error) (*MutationLease, error) {
		return nil, errors.Join(cause, fileLock.Close())
	}
	if err := validateMutationLockPath(path, false); err != nil {
		return closeOnError(err)
	}
	return &MutationLease{networkDir: networkDir, lock: fileLock}, nil
}

// NetworkDir returns the canonical network directory protected by the lease.
func (lease *MutationLease) NetworkDir() string {
	if lease == nil {
		return ""
	}
	return lease.networkDir
}

// Close releases the lease. Repeated and concurrent calls are safe.
func (lease *MutationLease) Close() error {
	if lease == nil {
		return nil
	}
	if lease.lock == nil {
		return nil
	}
	return lease.lock.Close()
}

func validateMutationLockPath(path string, allowMissing bool) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) && allowMissing {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("network mutation lock must be a non-symlink regular file")
	}
	return nil
}
