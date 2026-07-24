// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package suitekit

import (
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
)

const maxSeedFileSize = 64 << 10

func readWallet(path string) (wallet.Wallet, error) {
	if strings.ContainsRune(path, '\x00') || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, fmt.Errorf("%s must be a clean absolute path", seedFileVariable)
	}
	before, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("inspect %s: %w", seedFileVariable, err)
	}
	if before.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("%s must not be a symlink", seedFileVariable)
	}
	if !before.Mode().IsRegular() {
		return nil, fmt.Errorf("%s must be a regular file", seedFileVariable)
	}
	if before.Size() > maxSeedFileSize {
		return nil, fmt.Errorf("%s exceeds %d bytes", seedFileVariable, maxSeedFileSize)
	}
	if before.Mode().Perm()&0o177 != 0 {
		return nil, fmt.Errorf("%s permissions %04o expose or execute the live wallet; require 0600 or stricter", seedFileVariable, before.Mode().Perm())
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", seedFileVariable, err)
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("inspect opened %s: %w", seedFileVariable, err)
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) {
		return nil, fmt.Errorf("%s changed while it was being opened", seedFileVariable)
	}
	if opened.Mode().Perm()&0o177 != 0 {
		return nil, fmt.Errorf("%s permissions %04o expose or execute the live wallet; require 0600 or stricter", seedFileVariable, opened.Mode().Perm())
	}
	after, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("reinspect %s: %w", seedFileVariable, err)
	}
	if after.Mode()&os.ModeSymlink != 0 || !after.Mode().IsRegular() || !os.SameFile(before, after) || !os.SameFile(after, opened) {
		return nil, fmt.Errorf("%s changed while it was being opened", seedFileVariable)
	}
	raw, err := io.ReadAll(io.LimitReader(file, maxSeedFileSize+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", seedFileVariable, err)
	}
	if len(raw) > maxSeedFileSize {
		return nil, fmt.Errorf("%s exceeds %d bytes", seedFileVariable, maxSeedFileSize)
	}

	seed := strings.TrimSpace(string(raw))
	seed = strings.TrimPrefix(seed, "0x")
	decoded, err := hex.DecodeString(seed)
	if err != nil || len(decoded) == 0 {
		return nil, fmt.Errorf("%s must contain one non-empty hexadecimal seed", seedFileVariable)
	}
	restored, err := wallet.RestoreFromSeedHex(seed)
	if err != nil {
		return nil, fmt.Errorf("%s does not contain a restorable wallet seed", seedFileVariable)
	}
	return restored, nil
}
