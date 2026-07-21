// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// RunMode identifies the ownership and mutation contract of a vm64e2e command.
type RunMode string

const (
	ModeDoctor   RunMode = "doctor"
	ModeRun      RunMode = "run"
	ModeTest     RunMode = "test"
	ModeResume   RunMode = "resume"
	ModeFinalize RunMode = "finalize"
)

const (
	DefaultCleanupReserve  = time.Hour
	DefaultGlobalRuntime   = 6 * time.Hour
	DefaultPackageLocator  = "github.com/rgeraldes24/qrl-package"
	DefaultPackageRevision = "1f31cd03dbe2061225701ea79d956cfeceaf91db"
)

var shaPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// RunConfig contains inputs that affect execution or the evidence produced by
// a run. Its canonical digest is stored in checkpoints to reject incompatible
// resumes.
type RunConfig struct {
	Mode                RunMode       `json:"mode"`
	SourceSHA           string        `json:"source_sha"`
	PackageLocator      string        `json:"package_locator"`
	PackageRevision     string        `json:"package_revision"`
	RepoRoot            string        `json:"repo_root"`
	NetworkParams       string        `json:"network_params"`
	NetworkParamsSHA256 string        `json:"network_params_sha256"`
	ResultsDir          string        `json:"results_dir"`
	CheckpointPath      string        `json:"checkpoint_path"`
	OwnershipPath       string        `json:"ownership_path"`
	EnclaveName         string        `json:"enclave_name,omitempty"`
	EnclaveIdentifier   string        `json:"enclave_identifier,omitempty"`
	GlobalDeadline      time.Time     `json:"global_deadline"`
	CleanupReserve      time.Duration `json:"cleanup_reserve"`
	PreserveOnFailure   bool          `json:"preserve_on_failure"`
	AllowDisruptive     bool          `json:"allow_disruptive"`
	CI                  bool          `json:"ci"`
}

// New supplies the safety-oriented defaults used by the command layer.
func New(mode RunMode, now time.Time) RunConfig {
	return RunConfig{
		Mode:              mode,
		PackageLocator:    DefaultPackageLocator,
		PackageRevision:   DefaultPackageRevision,
		GlobalDeadline:    now.Add(DefaultGlobalRuntime),
		CleanupReserve:    DefaultCleanupReserve,
		PreserveOnFailure: true,
	}
}

func (c *RunConfig) Normalize() error {
	if c.RepoRoot != "" {
		root, err := filepath.Abs(c.RepoRoot)
		if err != nil {
			return fmt.Errorf("resolve repository root: %w", err)
		}
		c.RepoRoot = filepath.Clean(root)
	}
	for target, value := range map[*string]string{
		&c.NetworkParams:  c.NetworkParams,
		&c.ResultsDir:     c.ResultsDir,
		&c.CheckpointPath: c.CheckpointPath,
		&c.OwnershipPath:  c.OwnershipPath,
	} {
		if value == "" {
			continue
		}
		absolute, err := filepath.Abs(value)
		if err != nil {
			return fmt.Errorf("resolve path %q: %w", value, err)
		}
		*target = filepath.Clean(absolute)
	}
	return nil
}

func (c RunConfig) Validate(now time.Time) error {
	if !validMode(c.Mode) {
		return fmt.Errorf("invalid run mode %q", c.Mode)
	}
	if c.SourceSHA != "" && !shaPattern.MatchString(c.SourceSHA) {
		return errors.New("source SHA must be exactly 40 lowercase hexadecimal characters")
	}
	if c.NetworkParamsSHA256 != "" && !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(c.NetworkParamsSHA256) {
		return errors.New("network parameters SHA-256 is invalid")
	}
	if c.CleanupReserve <= 0 {
		return errors.New("cleanup reserve must be positive")
	}
	if !c.GlobalDeadline.IsZero() && !c.GlobalDeadline.After(now.Add(c.CleanupReserve)) {
		return errors.New("global deadline does not leave the cleanup reserve")
	}
	if strings.TrimSpace(c.ResultsDir) == "" {
		return errors.New("results directory is required")
	}
	switch c.Mode {
	case ModeRun:
		if c.EnclaveName == "" {
			return errors.New("owned runs require an enclave name")
		}
		if c.PackageLocator == "" || c.PackageRevision == "" {
			return errors.New("owned runs require a pinned package locator and revision")
		}
		if !shaPattern.MatchString(c.PackageRevision) {
			return errors.New("package revision must be an exact 40-character commit")
		}
	case ModeTest:
		if c.EnclaveIdentifier == "" {
			return errors.New("borrowed-network tests require an enclave identifier")
		}
	case ModeResume:
		if c.CheckpointPath == "" {
			return errors.New("resume requires a checkpoint path")
		}
	case ModeFinalize:
		if c.OwnershipPath == "" {
			return errors.New("finalize requires an ownership path")
		}
	}
	return nil
}

func (c RunConfig) Digest() (string, error) {
	type digestConfig struct {
		SourceSHA           string        `json:"source_sha"`
		PackageLocator      string        `json:"package_locator"`
		PackageRevision     string        `json:"package_revision"`
		NetworkParams       string        `json:"network_params"`
		NetworkParamsSHA256 string        `json:"network_params_sha256"`
		CleanupReserve      time.Duration `json:"cleanup_reserve"`
		AllowDisruptive     bool          `json:"allow_disruptive"`
		CI                  bool          `json:"ci"`
	}
	payload, err := json.Marshal(digestConfig{
		SourceSHA:           c.SourceSHA,
		PackageLocator:      c.PackageLocator,
		PackageRevision:     c.PackageRevision,
		NetworkParams:       c.NetworkParams,
		NetworkParamsSHA256: c.NetworkParamsSHA256,
		CleanupReserve:      c.CleanupReserve,
		AllowDisruptive:     c.AllowDisruptive,
		CI:                  c.CI,
	})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

func validMode(mode RunMode) bool {
	switch mode {
	case ModeDoctor, ModeRun, ModeTest, ModeResume, ModeFinalize:
		return true
	default:
		return false
	}
}
