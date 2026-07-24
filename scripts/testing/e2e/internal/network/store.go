// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/google/renameio"
)

const maxStateSize = 1 << 20

func statePath(networkDir string) string   { return filepath.Join(networkDir, "network.json") }
func privatePath(networkDir string) string { return filepath.Join(networkDir, "private") }
func ownershipPath(networkDir string) string {
	return filepath.Join(privatePath(networkDir), "ownership.json")
}

func loadOwnership(networkDir string) (OwnershipRecord, error) {
	if err := validatePrivateDirectory(networkDir); err != nil {
		return OwnershipRecord{}, err
	}
	record, err := loadJSON[OwnershipRecord](ownershipPath(networkDir), "ownership")
	if err != nil {
		return OwnershipRecord{}, err
	}
	if record.NetworkDir != networkDir {
		return OwnershipRecord{}, errors.New("ownership belongs to another network directory")
	}
	if err := record.Validate(); err != nil {
		return OwnershipRecord{}, err
	}
	return record, nil
}

func createOwnership(record OwnershipRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if record.UUID != "" {
		return errors.New("ownership must begin as a creation intent")
	}
	return writeJSONExclusive(ownershipPath(record.NetworkDir), record)
}

func captureOwnership(record OwnershipRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if record.UUID == "" {
		return errors.New("captured ownership has no exact enclave UUID")
	}
	return writeJSONAtomic(ownershipPath(record.NetworkDir), record)
}

func removeOwnership(record OwnershipRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if err := os.Remove(ownershipPath(record.NetworkDir)); err != nil {
		return err
	}
	return syncDirectory(privatePath(record.NetworkDir))
}

func loadState(networkDir string) (State, error) {
	if err := validatePrivateDirectory(networkDir); err != nil {
		return State{}, err
	}
	path := statePath(networkDir)
	state, err := loadJSON[State](path, "network state")
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, fmt.Errorf("start the independent E2E network first: %s is missing", path)
		}
		return State{}, err
	}
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

func writeState(networkDir string, state State) error {
	if err := state.Validate(); err != nil {
		return err
	}
	return writeJSONAtomic(statePath(networkDir), state)
}

func removeState(networkDir string) error {
	if err := os.Remove(statePath(networkDir)); err != nil {
		return err
	}
	return syncDirectory(networkDir)
}

func validatePrivateDirectory(networkDir string) error {
	info, err := os.Lstat(privatePath(networkDir))
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("private network state must be a non-symlink directory")
	}
	return nil
}

func loadJSON[T any](path, description string) (T, error) {
	var value T
	info, err := os.Lstat(path)
	if err != nil {
		return value, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxStateSize {
		return value, fmt.Errorf("%s must be a bounded non-symlink regular file", description)
	}
	file, err := os.Open(path)
	if err != nil {
		return value, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxStateSize+1))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, fmt.Errorf("decode %s: %w", description, err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return value, fmt.Errorf("%s contains trailing data", description)
	}
	return value, nil
}

func writeJSONAtomic(path string, value any) error {
	payload, err := jsonPayload(value)
	if err != nil {
		return err
	}
	if err := renameio.WriteFile(path, payload, 0o600); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
}

func writeJSONExclusive(path string, value any) error {
	payload, err := jsonPayload(value)
	if err != nil {
		return err
	}
	return writeExclusive(path, payload)
}

func jsonPayload(value any) ([]byte, error) {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	payload = append(payload, '\n')
	return payload, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func canonicalExistingDirectory(path, description string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be an absolute path", description)
	}
	clean := filepath.Clean(path)
	info, err := os.Lstat(clean)
	if err != nil {
		return "", err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", fmt.Errorf("%s must be a non-symlink directory", description)
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	return filepath.Clean(resolved), nil
}

func ensureNetworkDirectory(path string) (string, error) {
	if path == "" || !filepath.IsAbs(path) {
		return "", errors.New("network directory must be an absolute path")
	}
	clean := filepath.Clean(path)
	createdNetworkDirectory := false
	if info, err := os.Lstat(clean); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.New("network directory must be a non-symlink directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	} else if err := os.MkdirAll(clean, 0o700); err != nil {
		return "", err
	} else {
		createdNetworkDirectory = true
		if err := syncDirectory(filepath.Dir(clean)); err != nil {
			return "", fmt.Errorf("sync network directory parent: %w", err)
		}
	}
	resolved, err := filepath.EvalSymlinks(clean)
	if err != nil {
		return "", err
	}
	clean = filepath.Clean(resolved)
	if err := os.Chmod(clean, 0o700); err != nil {
		return "", err
	}
	privateDir := privatePath(clean)
	createdPrivateDirectory := false
	if info, err := os.Lstat(privateDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", errors.New("private network state must be a non-symlink directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	} else if err := os.Mkdir(privateDir, 0o700); err != nil {
		return "", err
	} else {
		createdPrivateDirectory = true
	}
	if err := os.Chmod(privateDir, 0o700); err != nil {
		return "", err
	}
	if createdPrivateDirectory {
		if err := syncDirectory(clean); err != nil {
			return "", fmt.Errorf("sync network directory after private state creation: %w", err)
		}
	}
	if createdNetworkDirectory {
		// The parent entry was synced before resolving symlinked ancestors. Sync
		// the canonical parent as well so paths such as /tmp remain durable.
		if err := syncDirectory(filepath.Dir(clean)); err != nil {
			return "", fmt.Errorf("sync canonical network directory parent: %w", err)
		}
	}
	return clean, nil
}
