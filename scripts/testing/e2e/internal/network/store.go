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
)

const maxStateSize = 1 << 20

func statePath(networkDir string) string   { return filepath.Join(networkDir, "network.json") }
func privatePath(networkDir string) string { return filepath.Join(networkDir, "private") }
func lifecyclePath(networkDir string) string {
	return filepath.Join(privatePath(networkDir), "lifecycle.json")
}

func loadLifecycle(networkDir string) (LifecycleRecord, error) {
	privateInfo, err := os.Lstat(privatePath(networkDir))
	if err != nil || privateInfo.Mode()&os.ModeSymlink != 0 || !privateInfo.IsDir() {
		return LifecycleRecord{}, errors.New("private network state must be a non-symlink directory")
	}
	path := lifecyclePath(networkDir)
	info, err := os.Lstat(path)
	if err != nil {
		return LifecycleRecord{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxStateSize {
		return LifecycleRecord{}, errors.New("lifecycle must be a bounded non-symlink regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return LifecycleRecord{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxStateSize+1))
	decoder.DisallowUnknownFields()
	var record LifecycleRecord
	if err := decoder.Decode(&record); err != nil {
		return LifecycleRecord{}, fmt.Errorf("decode lifecycle: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return LifecycleRecord{}, errors.New("lifecycle contains trailing data")
	}
	if record.NetworkDir != networkDir {
		return LifecycleRecord{}, errors.New("lifecycle belongs to another network directory")
	}
	if err := record.Validate(); err != nil {
		return LifecycleRecord{}, err
	}
	return record, nil
}

func createLifecycle(record LifecycleRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if record.Phase != LifecycleCreateIntent {
		return errors.New("a lifecycle must be created at create_intent")
	}
	return writeJSONExclusive(lifecyclePath(record.NetworkDir), record)
}

func writeLifecycle(record LifecycleRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	return writeJSONAtomic(lifecyclePath(record.NetworkDir), record)
}

func retireLifecycle(record LifecycleRecord) error {
	if err := record.Validate(); err != nil {
		return err
	}
	if record.Phase != LifecycleStopped || record.Enclave == nil {
		return errors.New("only a stopped exact-UUID lifecycle can be retired")
	}
	historyDir := filepath.Join(privatePath(record.NetworkDir), "history")
	if info, err := os.Lstat(historyDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("private network history must be a non-symlink directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err := os.Mkdir(historyDir, 0o700); err != nil {
		return err
	} else if err := syncDirectory(privatePath(record.NetworkDir)); err != nil {
		return err
	}
	archive := filepath.Join(historyDir, "lifecycle-"+record.Enclave.UUID+".json")
	if _, err := os.Lstat(archive); err == nil {
		return errors.New("completed lifecycle was already archived while still active")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(lifecyclePath(record.NetworkDir), archive); err != nil {
		return err
	}
	if err := os.Chmod(archive, 0o600); err != nil {
		return err
	}
	if err := syncDirectory(historyDir); err != nil {
		return err
	}
	return syncDirectory(privatePath(record.NetworkDir))
}

func loadState(networkDir string) (State, error) {
	privateInfo, err := os.Lstat(privatePath(networkDir))
	if err != nil || privateInfo.Mode()&os.ModeSymlink != 0 || !privateInfo.IsDir() {
		return State{}, errors.New("private network state must be a non-symlink directory")
	}
	path := statePath(networkDir)
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, fmt.Errorf("start the independent E2E network first: %s is missing", path)
		}
		return State{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxStateSize {
		return State{}, errors.New("network state must be a bounded non-symlink regular file")
	}
	file, err := os.Open(path)
	if err != nil {
		return State{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(io.LimitReader(file, maxStateSize+1))
	decoder.DisallowUnknownFields()
	var state State
	if err := decoder.Decode(&state); err != nil {
		return State{}, fmt.Errorf("decode network state: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return State{}, errors.New("network state contains trailing data")
	}
	if state.NetworkDir != networkDir {
		return State{}, fmt.Errorf("network state belongs to %s, not %s", state.NetworkDir, networkDir)
	}
	if err := state.Validate(); err != nil {
		return State{}, err
	}
	return state, nil
}

func writeState(state State) error {
	if err := state.Validate(); err != nil {
		return err
	}
	return writeJSONAtomic(statePath(state.NetworkDir), state)
}

func retireStoppedState(state State) error {
	if state.Phase != PhaseStopped {
		return errors.New("only stopped network state can be retired")
	}
	historyDir := filepath.Join(privatePath(state.NetworkDir), "history")
	if info, err := os.Lstat(historyDir); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return errors.New("private network history must be a non-symlink directory")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	} else if err := os.Mkdir(historyDir, 0o700); err != nil {
		return err
	} else if err := syncDirectory(privatePath(state.NetworkDir)); err != nil {
		return err
	}
	archive := filepath.Join(historyDir, "network-"+state.Fingerprint+".json")
	if _, err := os.Lstat(archive); err == nil {
		return errors.New("stopped network state was already archived while still active at the public path")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Rename(statePath(state.NetworkDir), archive); err != nil {
		return err
	}
	if err := os.Chmod(archive, 0o600); err != nil {
		return err
	}
	if err := syncDirectory(historyDir); err != nil {
		return err
	}
	return syncDirectory(state.NetworkDir)
}

func writeJSONAtomic(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(temporary)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(payload); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temporary, path); err != nil {
		return err
	}
	remove = false
	if err := os.Chmod(path, 0o600); err != nil {
		return err
	}
	return syncDirectory(directory)
}

// writeJSONExclusive publishes a fully synced file through a hard link. The
// destination is never overwritten, which makes lifecycle creation both
// durable and safe under concurrent creators.
func writeJSONExclusive(path string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	payload = append(payload, '\n')
	directory := filepath.Dir(path)
	file, err := os.CreateTemp(directory, "."+filepath.Base(path)+"-*")
	if err != nil {
		return err
	}
	temporary := file.Name()
	defer os.Remove(temporary)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(payload); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Link(temporary, path); err != nil {
		return err
	}
	return syncDirectory(directory)
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
