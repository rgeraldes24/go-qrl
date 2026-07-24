// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
)

func TestOwnershipStoreRejectsUntrustedFiles(t *testing.T) {
	tests := map[string]func(*testing.T, string, []byte){
		"symlink": func(t *testing.T, path string, valid []byte) {
			target := filepath.Join(t.TempDir(), "ownership-target.json")
			if err := os.WriteFile(target, valid, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, path); err != nil {
				t.Fatal(err)
			}
		},
		"unknown field": func(t *testing.T, path string, valid []byte) {
			var value map[string]any
			if err := json.Unmarshal(valid, &value); err != nil {
				t.Fatal(err)
			}
			value["unexpected"] = true
			writeTestJSON(t, path, value)
		},
		"trailing data": func(t *testing.T, path string, valid []byte) {
			if err := os.WriteFile(path, append(valid, []byte("{}\n")...), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"corrupt data": func(t *testing.T, path string, _ []byte) {
			if err := os.WriteFile(path, []byte("{not-json\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"oversized": func(t *testing.T, path string, _ []byte) {
			if err := os.WriteFile(path, []byte(strings.Repeat("x", maxStateSize+1)), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, arrange := range tests {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			record := fixtureOwnership(t, dir)
			valid, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			arrange(t, ownershipPath(dir), valid)
			if loaded, err := loadOwnership(dir); err == nil {
				t.Fatalf("untrusted ownership loaded: %+v", loaded)
			}
		})
	}
}

func TestAtomicJSONWriteIsPrivateAndNeverFollowsDestinationSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "record.json")
	target := filepath.Join(dir, "target.json")
	const targetContents = "do-not-overwrite\n"
	if err := os.WriteFile(target, []byte(targetContents), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(path, map[string]any{"generation": 1}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 {
		t.Fatalf("atomic result metadata = %v, %v", info, err)
	}
	unchanged, err := os.ReadFile(target)
	if err != nil || string(unchanged) != targetContents {
		t.Fatalf("symlink target changed to %q, %v", unchanged, err)
	}
	if temporary, err := filepath.Glob(filepath.Join(dir, ".record.json-*")); err != nil || len(temporary) != 0 {
		t.Fatalf("temporary files after atomic write = %v, %v", temporary, err)
	}

	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeJSONAtomic(path, map[string]any{"unsupported": make(chan int)}); err == nil {
		t.Fatal("unsupported JSON value unexpectedly replaced durable state")
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(before) {
		t.Fatalf("failed atomic write changed durable state: before=%q after=%q err=%v", before, after, err)
	}
}

func TestExclusivePrivateWriteDoesNotReplaceExistingSecret(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret")
	if err := writeExclusive(path, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := writeExclusive(path, []byte("second\n")); err == nil {
		t.Fatal("exclusive write unexpectedly replaced existing secret")
	}
	contents, err := os.ReadFile(path)
	if err != nil || string(contents) != "first\n" {
		t.Fatalf("exclusive content = %q, %v", contents, err)
	}
}

func TestNetworkDirectoryRejectsSymlinks(t *testing.T) {
	realDirectory := t.TempDir()
	rootLink := filepath.Join(t.TempDir(), "network")
	if err := os.Symlink(realDirectory, rootLink); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureNetworkDirectory(rootLink); err == nil {
		t.Fatal("symlink network directory was accepted")
	}

	networkDir := t.TempDir()
	privateTarget := t.TempDir()
	if err := os.Symlink(privateTarget, privatePath(networkDir)); err != nil {
		t.Fatal(err)
	}
	if _, err := ensureNetworkDirectory(networkDir); err == nil {
		t.Fatal("symlink private directory was accepted")
	}
}

func fixtureOwnership(t *testing.T, networkDir string) OwnershipRecord {
	t.Helper()
	if err := os.MkdirAll(privatePath(networkDir), 0o700); err != nil {
		t.Fatal(err)
	}
	enclave := kurtosis.EnclaveRef{Name: "e2e", UUID: strings.Repeat("a", 32), Owned: true}
	record := OwnershipRecord{
		NetworkDir: networkDir,
		Name:       enclave.Name,
		UUID:       enclave.UUID,
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	return record
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
