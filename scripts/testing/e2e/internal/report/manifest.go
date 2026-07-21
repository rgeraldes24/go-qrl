// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package report

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// WriteManifest inventories the artifact directory and atomically writes
// manifest.json. Call it after all other artifacts have been finalized.
func (writer *Writer) WriteManifest(metadata ManifestMetadata) (Manifest, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if err := writer.invalidateManifestLocked(); err != nil {
		return Manifest{}, err
	}

	manifest := Manifest{
		Schema:              SchemaVersion,
		RunID:               metadata.RunID,
		SourceSHA:           metadata.SourceSHA,
		ConfigurationDigest: metadata.ConfigurationDigest,
		GeneratedAt:         metadata.GeneratedAt.UTC(),
		Artifacts:           []Artifact{},
	}
	if manifest.RunID == "" {
		return Manifest{}, fmt.Errorf("manifest run ID is empty")
	}
	if metadata.GeneratedAt.IsZero() {
		manifest.GeneratedAt = writer.currentTime()
	}
	err := filepath.WalkDir(writer.root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == writer.root || entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(writer.root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == ManifestFilename || strings.HasPrefix(entry.Name(), ".vm64e2e-atomic-") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact %s is not a regular file", relative)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		digest := sha256.New()
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		manifest.Artifacts = append(manifest.Artifacts, Artifact{
			Path: relative, SizeBytes: info.Size(), SHA256: hex.EncodeToString(digest.Sum(nil)),
		})
		return nil
	})
	if err != nil {
		return Manifest{}, fmt.Errorf("inventory artifacts: %w", err)
	}
	slices.SortFunc(manifest.Artifacts, func(left, right Artifact) int {
		return strings.Compare(left.Path, right.Path)
	})
	if err := writer.validateResultLogReferences(manifest); err != nil {
		return Manifest{}, err
	}
	data, err := encodeJSON(manifest)
	if err != nil {
		return Manifest{}, err
	}
	if err := atomicWrite(filepath.Join(writer.root, ManifestFilename), data, 0o644); err != nil {
		return Manifest{}, fmt.Errorf("write artifact %s: %w", ManifestFilename, err)
	}
	return manifest, nil
}

// InvalidateManifest removes the prior artifact seal before any caller mutates
// an existing evidence bundle. Until WriteManifest succeeds again, consumers
// can no longer mistake pre-mutation checksums for a valid current inventory.
func (writer *Writer) InvalidateManifest() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	return writer.invalidateManifestLocked()
}

func (writer *Writer) invalidateManifestLocked() error {
	path := writer.Layout().Manifest
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("invalidate artifact manifest: %w", err)
	}
	if err := syncDirectory(writer.root); err != nil {
		return fmt.Errorf("sync invalidated artifact manifest: %w", err)
	}
	return nil
}

func (writer *Writer) validateResultLogReferences(manifest Manifest) error {
	inventory := make(map[string]struct{}, len(manifest.Artifacts))
	for _, artifact := range manifest.Artifacts {
		inventory[artifact.Path] = struct{}{}
	}
	validate := func(kind, name, relative string) error {
		if relative == "" {
			return nil
		}
		if _, exists := inventory[relative]; !exists {
			return fmt.Errorf("%s %q log path %q is not a manifest-inventoried regular artifact", kind, name, relative)
		}
		return nil
	}

	results, err := LoadResults(writer.Layout().Results)
	if err == nil {
		for _, stage := range results.Stages {
			if err := validate("stage", stage.Name, stage.LogPath); err != nil {
				return err
			}
		}
		for _, suite := range results.Suites {
			if err := validate("suite", suite.Name, suite.LogPath); err != nil {
				return err
			}
		}
		for _, suite := range results.SuiteHistory {
			if err := validate("historical suite", suite.Name, suite.LogPath); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("validate result log references: %w", err)
	}

	entries, err := os.ReadDir(writer.Layout().Stages)
	if err != nil {
		return fmt.Errorf("read stage artifacts: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var payload struct {
			Schema int `json:"schema"`
			StageResult
		}
		path := filepath.Join(writer.Layout().Stages, entry.Name())
		if err := loadStrictJSON(path, &payload); err != nil {
			return fmt.Errorf("load stage artifact %s: %w", entry.Name(), err)
		}
		if payload.Schema != SchemaVersion {
			return fmt.Errorf("stage artifact %s has schema %d, want %d", entry.Name(), payload.Schema, SchemaVersion)
		}
		if err := validate("stage", payload.Name, payload.LogPath); err != nil {
			return err
		}
	}
	return nil
}

func encodeJSON(value any) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return nil, fmt.Errorf("encode JSON: %w", err)
	}
	return buffer.Bytes(), nil
}
