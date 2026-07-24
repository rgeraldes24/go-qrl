// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMutationLeaseIsNetworkScopedPrivateAndIdempotent(t *testing.T) {
	networkDir := t.TempDir()
	privateDir := filepath.Join(networkDir, "private")
	if err := os.Mkdir(privateDir, 0o700); err != nil {
		t.Fatal(err)
	}

	first, err := AcquireMutationLease(networkDir)
	if err != nil {
		t.Fatal(err)
	}
	canonicalNetworkDir, err := filepath.EvalSymlinks(networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if first.NetworkDir() != canonicalNetworkDir {
		t.Fatalf("lease network directory = %s, want %s", first.NetworkDir(), canonicalNetworkDir)
	}
	lockPath := filepath.Join(privateDir, "mutation.lock")
	info, err := os.Lstat(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("mutation lease mode = %v, want regular 0600", info.Mode())
	}

	secondResult := make(chan error, 1)
	go func() {
		second, err := AcquireMutationLease(networkDir)
		if second != nil {
			_ = second.Close()
		}
		secondResult <- err
	}()
	select {
	case err := <-secondResult:
		if err == nil || !strings.Contains(err.Error(), "already in progress") {
			t.Fatalf("concurrent mutation lease error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent mutation lease acquisition did not fail fast")
	}

	var closeGroup sync.WaitGroup
	closeResults := make(chan error, 2)
	for range 2 {
		closeGroup.Add(1)
		go func() {
			defer closeGroup.Done()
			closeResults <- first.Close()
		}()
	}
	closeGroup.Wait()
	close(closeResults)
	for err := range closeResults {
		if err != nil {
			t.Fatalf("concurrent mutation lease release: %v", err)
		}
	}

	reopened, err := AcquireMutationLease(networkDir)
	if err != nil {
		t.Fatalf("reacquire released mutation lease: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMutationLeaseRejectsUntrustedPaths(t *testing.T) {
	t.Run("relative network", func(t *testing.T) {
		if lease, err := AcquireMutationLease("network"); err == nil {
			_ = lease.Close()
			t.Fatal("relative network directory was accepted")
		}
	})

	t.Run("symlink private directory", func(t *testing.T) {
		networkDir := t.TempDir()
		target := t.TempDir()
		if err := os.Symlink(target, filepath.Join(networkDir, "private")); err != nil {
			t.Skipf("create test symlink: %v", err)
		}
		if lease, err := AcquireMutationLease(networkDir); err == nil {
			_ = lease.Close()
			t.Fatal("symlink private network directory was accepted")
		} else if !strings.Contains(err.Error(), "non-symlink directory") {
			t.Fatalf("symlink private directory error = %v", err)
		}
	})

	for name, arrange := range map[string]func(*testing.T, string){
		"symlink lock": func(t *testing.T, lockPath string) {
			target := filepath.Join(t.TempDir(), "target.lock")
			if err := os.WriteFile(target, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, lockPath); err != nil {
				t.Skipf("create test symlink: %v", err)
			}
		},
		"directory lock": func(t *testing.T, lockPath string) {
			if err := os.Mkdir(lockPath, 0o700); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			networkDir := t.TempDir()
			privateDir := filepath.Join(networkDir, "private")
			if err := os.Mkdir(privateDir, 0o700); err != nil {
				t.Fatal(err)
			}
			arrange(t, filepath.Join(privateDir, "mutation.lock"))
			if lease, err := AcquireMutationLease(networkDir); err == nil {
				_ = lease.Close()
				t.Fatal("untrusted mutation lock path was accepted")
			} else if !strings.Contains(err.Error(), "non-symlink regular file") {
				t.Fatalf("untrusted mutation lock error = %v", err)
			}
		})
	}
}
