// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package harness composes the VM64 E2E lifecycle from project-owned adapters
// and importable suites.
package harness

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/config"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/provision"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
	freshSyncSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/freshsync"
	systemSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/system"
)

const EffectiveConfigurationSchema = 1

type EffectiveConfiguration struct {
	Schema       int                    `json:"schema"`
	RunID        string                 `json:"run_id"`
	Config       config.RunConfig       `json:"config"`
	Preparation  *provision.Preparation `json:"preparation,omitempty"`
	TopologySpec topology.Spec          `json:"topology_spec"`
}

type ProcessRunner func(context.Context, process.Command) (process.Result, error)

type SystemSuiteRunner func(context.Context, systemSuite.Config, systemSuite.Options) error

type FreshSyncSuiteRunner func(context.Context, freshSyncSuite.Config, freshSyncSuite.Options) error

type PackageNetworkMetadata struct {
	NetworkID             string `json:"network_id"`
	FinalGenesisTimestamp string `json:"final_genesis_timestamp"`
	GenesisValidatorsRoot string `json:"genesis_validators_root"`
	GenesisForkVersion    string `json:"genesis_fork_version"`
}

type PackageMetadataReader func(context.Context, topology.Topology) (PackageNetworkMetadata, error)

type OwnershipUUIDCapture func(string, string, string) (lifecycle.OwnershipRecord, error)

type Dependencies struct {
	Client               kurtosis.Client
	Process              ProcessRunner
	System               SystemSuiteRunner
	FreshSync            FreshSyncSuiteRunner
	PackageMetadata      PackageMetadataReader
	CaptureOwnershipUUID OwnershipUUIDCapture
	Dumper               interface {
		Dump(context.Context, string, string) ([]byte, error)
	}
	Now    func() time.Time
	Logger *slog.Logger
	Output io.Writer
}

func (dependencies *Dependencies) normalize() error {
	if dependencies.Client == nil {
		return errors.New("Kurtosis client is required")
	}
	if dependencies.Process == nil {
		dependencies.Process = process.Run
	}
	if dependencies.System == nil {
		dependencies.System = systemSuite.Run
	}
	if dependencies.FreshSync == nil {
		dependencies.FreshSync = freshSyncSuite.Run
	}
	if dependencies.PackageMetadata == nil {
		dependencies.PackageMetadata = readPackageNetworkMetadata
	}
	if dependencies.CaptureOwnershipUUID == nil {
		dependencies.CaptureOwnershipUUID = lifecycle.CaptureOwnershipUUID
	}
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	if dependencies.Output == nil {
		dependencies.Output = io.Discard
	}
	return nil
}

type Runtime struct {
	RunID         string
	Config        config.RunConfig
	Enclave       lifecycle.EnclaveRef
	Writer        *report.Writer
	Store         lifecycle.Store
	Dependencies  Dependencies
	Preparation   *provision.Preparation
	PackageResult *kurtosis.PackageResult
	Topology      *topology.Topology
	TopologySpec  topology.Spec
	StartedAt     time.Time
	SuiteResults  []report.SuiteResult
	SuiteHistory  []report.SuiteResult
	Timeline      *report.TimelineRecorder
}

func sameSuite(left, right report.SuiteResult) bool {
	return left.Name == right.Name && left.Stage == right.Stage
}

func (runtime *Runtime) restoreSuiteEvidence(results report.Results) {
	runtime.SuiteResults = append([]report.SuiteResult(nil), results.Suites...)
	runtime.SuiteHistory = append([]report.SuiteResult(nil), results.SuiteHistory...)
}

// recordSuiteResult makes the newest attempt for a stage/name pair active and
// moves the prior active attempt into immutable history. This keeps aggregate
// pass/fail consumers focused on the current attempt without discarding the
// failure evidence that caused a resume.
func (runtime *Runtime) recordSuiteResult(result report.SuiteResult) {
	maxAttempt := 0
	for _, historical := range runtime.SuiteHistory {
		if sameSuite(historical, result) && historical.Attempt > maxAttempt {
			maxAttempt = historical.Attempt
		}
	}
	for index, active := range runtime.SuiteResults {
		if !sameSuite(active, result) {
			continue
		}
		if active.Attempt > maxAttempt {
			maxAttempt = active.Attempt
		}
		runtime.SuiteHistory = append(runtime.SuiteHistory, active)
		result.Attempt = maxAttempt + 1
		runtime.SuiteResults[index] = result
		return
	}
	if result.Attempt <= maxAttempt {
		result.Attempt = maxAttempt + 1
	}
	if result.Attempt < 1 {
		result.Attempt = 1
	}
	runtime.SuiteResults = append(runtime.SuiteResults, result)
}

// retireFailedSuiteResults handles a stage that completes on resume without
// producing a replacement suite record, including reconciliation from durable
// external state. Its old failure remains in suite_history, not in the active
// suite set of a successful aggregate.
func (runtime *Runtime) retireFailedSuiteResults(stage string) {
	active := runtime.SuiteResults[:0]
	for _, result := range runtime.SuiteResults {
		if result.Stage == stage && (result.Status == report.StatusFailed || result.Status == report.StatusCanceled) {
			runtime.SuiteHistory = append(runtime.SuiteHistory, result)
			continue
		}
		active = append(active, result)
	}
	runtime.SuiteResults = active
}

func (runtime *Runtime) effectiveConfiguration() EffectiveConfiguration {
	return EffectiveConfiguration{
		Schema: EffectiveConfigurationSchema, RunID: runtime.RunID, Config: runtime.Config,
		Preparation: runtime.Preparation, TopologySpec: runtime.TopologySpec,
	}
}

func (runtime *Runtime) writeEffectiveConfiguration() error {
	return runtime.Writer.WriteEffectiveConfig(runtime.effectiveConfiguration())
}

func loadEffectiveConfiguration(path string) (EffectiveConfiguration, error) {
	file, err := os.Open(path)
	if err != nil {
		return EffectiveConfiguration{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var effective EffectiveConfiguration
	if err := decoder.Decode(&effective); err != nil {
		return EffectiveConfiguration{}, fmt.Errorf("decode effective configuration: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return EffectiveConfiguration{}, errors.New("effective configuration contains trailing data")
	}
	if effective.Schema != EffectiveConfigurationSchema || effective.RunID == "" {
		return EffectiveConfiguration{}, errors.New("effective configuration has an invalid schema or run ID")
	}
	return effective, nil
}

func (runtime *Runtime) restoreArtifacts() error {
	effective, err := loadEffectiveConfiguration(runtime.Writer.Layout().EffectiveConfig)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err == nil {
		runtime.RunID = effective.RunID
		if runtime.Config.RepoRoot == "" {
			runtime.Config = effective.Config
		}
		runtime.Preparation = effective.Preparation
		runtime.TopologySpec = effective.TopologySpec
	}
	packagePath := filepath.Join(runtime.Writer.Layout().Kurtosis, "package-output.json")
	if payload, readErr := os.ReadFile(packagePath); readErr == nil {
		runtime.PackageResult = &kurtosis.PackageResult{SerializedOutput: string(payload)}
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	if payload, readErr := os.ReadFile(runtime.Writer.Layout().Topology); readErr == nil {
		discovered, parseErr := topology.ParseTopology(payload)
		if parseErr != nil {
			return parseErr
		}
		runtime.Topology = &discovered
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return readErr
	}
	return nil
}

func stageOrder(stages []lifecycle.Stage) []string {
	result := make([]string, 0, len(stages))
	for _, stage := range stages {
		result = append(result, stage.Name)
	}
	return result
}
