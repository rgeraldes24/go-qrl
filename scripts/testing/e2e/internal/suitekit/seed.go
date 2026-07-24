// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package suitekit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
)

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
	if before.Mode().Perm()&0o177 != 0 {
		return nil, fmt.Errorf("%s permissions %04o expose or execute the live wallet; require 0600 or stricter", seedFileVariable, before.Mode().Perm())
	}
	restored, err := wallet.RestoreFromFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s does not contain a restorable wallet seed", seedFileVariable)
	}
	return restored, nil
}
