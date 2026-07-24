// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnsureWalletCreatesAndReusesPrivateSeed(t *testing.T) {
	dir := t.TempDir()
	wallet, err := ensureWallet(dir)
	if err != nil {
		t.Fatal(err)
	}
	seedPath := filepath.Join(dir, seedName)
	seed, err := os.ReadFile(seedPath)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(seedPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0o600 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("private seed metadata = %v, %v", info, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != seedName {
		t.Fatalf("wallet directory entries = %v, want only %s", entries, seedName)
	}
	again, err := ensureWallet(dir)
	if err != nil || again != wallet {
		t.Fatalf("reused wallet = %+v, %v", again, err)
	}
	reused, _ := os.ReadFile(seedPath)
	if string(reused) != string(seed) {
		t.Fatal("wallet reuse replaced the seed")
	}
}

func TestEnsureWalletRejectsInvalidExistingSeed(t *testing.T) {
	for _, test := range []struct {
		name string
		make func(*testing.T, string)
		want string
	}{
		{
			name: "invalid contents",
			make: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("do-not-replace\n"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			want: "restore existing wallet",
		},
		{
			name: "permissive mode",
			make: func(t *testing.T, path string) {
				if err := os.WriteFile(path, []byte("do-not-replace\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "non-symlink 0600 regular file",
		},
		{
			name: "directory",
			make: func(t *testing.T, path string) {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			},
			want: "non-symlink 0600 regular file",
		},
		{
			name: "symlink",
			make: func(t *testing.T, path string) {
				target := filepath.Join(t.TempDir(), "seed")
				if err := os.WriteFile(target, []byte("do-not-replace\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(target, path); err != nil {
					t.Fatal(err)
				}
			},
			want: "non-symlink 0600 regular file",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			test.make(t, filepath.Join(dir, seedName))
			_, err := ensureWallet(dir)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("invalid-seed error = %v, want %q", err, test.want)
			}
		})
	}
}
