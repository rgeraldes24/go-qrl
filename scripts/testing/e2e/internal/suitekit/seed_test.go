// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package suitekit

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testSeed = "010000f29f58aff0b00de2844f7e20bd9eeaacc379150043beeb328335817512b29fbb7184da84a092f842b2a06d72a24a5d28"

func TestReadWalletRestoresSupportedSeedFiles(t *testing.T) {
	path := writeSeedFile(t, "0x"+testSeed+"\n", 0o600)
	if _, err := readWallet(path); err != nil {
		t.Fatalf("prefixed seed: %v", err)
	}

	readOnly := writeSeedFile(t, testSeed, 0o400)
	if _, err := readWallet(readOnly); err != nil {
		t.Fatalf("owner-read-only seed: %v", err)
	}
}

func TestReadWalletRejectsUnsafeFileKinds(t *testing.T) {
	directory := t.TempDir()
	if _, err := readWallet(directory); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("directory error = %v", err)
	}

	if runtime.GOOS != "windows" {
		target := writeSeedFile(t, testSeed, 0o600)
		link := filepath.Join(t.TempDir(), "seed-link")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		if _, err := readWallet(link); err == nil || !strings.Contains(err.Error(), "symlink") {
			t.Fatalf("symlink error = %v", err)
		}
	}

	if _, err := readWallet("relative.seed"); err == nil || !strings.Contains(err.Error(), "clean absolute") {
		t.Fatalf("relative path error = %v", err)
	}
}

func TestReadWalletRejectsUnsafePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not enforced on Windows")
	}
	for _, permissions := range []os.FileMode{0o640, 0o604, 0o700} {
		t.Run(permissions.String(), func(t *testing.T) {
			path := writeSeedFile(t, testSeed, permissions)
			if _, err := readWallet(path); err == nil || !strings.Contains(err.Error(), "permissions") {
				t.Fatalf("permissions %04o error = %v", permissions, err)
			}
		})
	}
}

func TestReadWalletRejectsOversizedAndInvalidContents(t *testing.T) {
	oversized := writeSeedFile(t, strings.Repeat("a", maxSeedFileSize+1), 0o600)
	if _, err := readWallet(oversized); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized error = %v", err)
	}

	tests := []struct {
		name, contents, want string
	}{
		{name: "empty", contents: "\n", want: "non-empty hexadecimal"},
		{name: "nonhex", contents: "not-a-seed", want: "non-empty hexadecimal"},
		{name: "multiple lines", contents: testSeed + "\n" + testSeed, want: "non-empty hexadecimal"},
		{name: "wrong length", contents: "00", want: "restorable wallet seed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeSeedFile(t, test.contents, 0o600)
			_, err := readWallet(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("readWallet error = %v, want %q", err, test.want)
			}
		})
	}
}

func writeSeedFile(t *testing.T, contents string, permissions os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wallet.seed")
	if err := os.WriteFile(path, []byte(contents), permissions); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, permissions); err != nil {
		t.Fatal(err)
	}
	return path
}
