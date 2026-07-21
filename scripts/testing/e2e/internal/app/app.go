// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package app implements the vm64e2e command without embedding process exit
// calls, so every subcommand can be exercised by unit tests.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/config"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/doctor"
	finalizer "github.com/theQRL/go-qrl/scripts/testing/e2e/internal/finalize"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/harness"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/source"
)

type ClientFactory func() (kurtosis.Client, error)

const defaultFinalizeTimeout = 18 * time.Minute

type App struct {
	Stdout        io.Writer
	Stderr        io.Writer
	Now           func() time.Time
	ClientFactory ClientFactory
	Dependencies  harness.Dependencies
	DoctorRunner  doctor.Runner
}

func New() *App {
	return &App{
		Stdout: os.Stdout, Stderr: os.Stderr, Now: time.Now,
		ClientFactory: func() (kurtosis.Client, error) { return kurtosis.NewSDKClient() },
	}
}

func (app *App) Execute(ctx context.Context, arguments []string) error {
	if app.Stdout == nil {
		app.Stdout = io.Discard
	}
	if app.Stderr == nil {
		app.Stderr = io.Discard
	}
	if app.Now == nil {
		app.Now = time.Now
	}
	if len(arguments) == 0 {
		return usageError("expected doctor, run, test, resume, or finalize")
	}
	switch arguments[0] {
	case "doctor":
		return app.doctor(ctx, arguments[1:])
	case "run":
		return app.run(ctx, arguments[1:])
	case "test":
		return app.test(ctx, arguments[1:])
	case "resume":
		return app.resume(ctx, arguments[1:])
	case "finalize":
		return app.finalize(ctx, arguments[1:])
	case "help", "-h", "--help":
		_, _ = io.WriteString(app.Stdout, usage())
		return nil
	default:
		return usageError("unknown command " + arguments[0])
	}
}

func (app *App) doctor(ctx context.Context, arguments []string) error {
	common, err := app.parseCommon("doctor", config.ModeDoctor, arguments, false)
	if err != nil {
		return err
	}
	result, err := app.environmentReport(ctx, common.config)
	if err != nil {
		return err
	}
	if err := writeJSON(app.Stdout, result); err != nil {
		return err
	}
	return result.Validate()
}

func (app *App) environmentReport(ctx context.Context, runConfig config.RunConfig) (doctor.Report, error) {
	pinned, err := pinnedImages(filepath.Join(runConfig.RepoRoot, "scripts", "local_testnet", "images.lock.env"))
	if err != nil {
		return doctor.Report{}, err
	}
	binaries := []string{}
	if executable, executableErr := os.Executable(); executableErr == nil {
		binaries = append(binaries, executable)
	}
	return doctor.Run(ctx, app.DoctorRunner, doctor.Options{
		RepoRoot: runConfig.RepoRoot, SourceSHA: runConfig.SourceSHA,
		NetworkParams: runConfig.NetworkParams, RequireEngine: true, PinnedImages: pinned,
		BuiltBinaries: binaries,
	}), nil
}

func (app *App) run(ctx context.Context, arguments []string) error {
	common, err := app.parseCommon("run", config.ModeRun, arguments, true)
	if err != nil {
		return err
	}
	if common.config.EnclaveName == "" {
		common.config.EnclaveName = "vm64-e2e-" + common.runID
	}
	runContext, cancelRun := context.WithDeadline(ctx, common.config.GlobalDeadline)
	defer cancelRun()
	preflightDeadline := common.config.GlobalDeadline.Add(-common.config.CleanupReserve)
	preflightContext, cancelPreflight := context.WithDeadline(runContext, preflightDeadline)
	preflight, err := app.environmentReport(preflightContext, common.config)
	cancelPreflight()
	if err != nil {
		return err
	}
	if err := preflight.Validate(); err != nil {
		return fmt.Errorf("preflight validation before enclave creation: %w", err)
	}
	client, dependencies, err := app.clientDependencies()
	if err != nil {
		return err
	}
	dependencies.Client = client
	outcome, runErr := harness.RunOwned(runContext, common.runID, common.config, dependencies)
	if err := writeJSON(app.Stdout, outcome); err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}

func (app *App) test(ctx context.Context, arguments []string) error {
	common, err := app.parseCommon("test", config.ModeTest, arguments, false)
	if err != nil {
		return err
	}
	if common.config.EnclaveIdentifier == "" {
		return usageError("test requires --enclave")
	}
	client, dependencies, err := app.clientDependencies()
	if err != nil {
		return err
	}
	dependencies.Client = client
	outcome, runErr := harness.TestBorrowed(ctx, common.runID, common.config, dependencies)
	if err := writeJSON(app.Stdout, outcome); err != nil {
		return errors.Join(runErr, err)
	}
	return runErr
}

func (app *App) resume(ctx context.Context, arguments []string) error {
	set := flag.NewFlagSet("resume", flag.ContinueOnError)
	set.SetOutput(app.Stderr)
	checkpoint := set.String("checkpoint", "", "schema-v1 checkpoint to resume")
	repoRoot := set.String("repo-root", "", "go-qrl checkout root")
	sourceSHA := set.String("source-sha", "", "exact checkout commit")
	globalTimeout := set.Duration("global-timeout", config.DefaultGlobalRuntime, "new total lifecycle budget including the cleanup reserve")
	if err := set.Parse(arguments); err != nil {
		return usageError(err.Error())
	}
	if set.NArg() != 0 || *checkpoint == "" {
		return usageError("resume requires --checkpoint and no positional arguments")
	}
	root, err := resolveRepoRoot(*repoRoot)
	if err != nil {
		return err
	}
	if *sourceSHA == "" {
		*sourceSHA, err = source.Commit(ctx, nil, root)
		if err != nil {
			return err
		}
	}
	client, dependencies, err := app.clientDependencies()
	if err != nil {
		return err
	}
	dependencies.Client = client
	outcome, resumeErr := harness.Resume(ctx, harness.ResumeOptions{
		CheckpointPath: *checkpoint, SourceSHA: *sourceSHA,
		RepoRoot:       root,
		GlobalDeadline: app.Now().Add(*globalTimeout),
	}, dependencies)
	if err := writeJSON(app.Stdout, outcome); err != nil {
		return errors.Join(resumeErr, err)
	}
	return resumeErr
}

func (app *App) finalize(ctx context.Context, arguments []string) error {
	set := flag.NewFlagSet("finalize", flag.ContinueOnError)
	set.SetOutput(app.Stderr)
	ownership := set.String("ownership", "", "ownership.json to finalize")
	resultsDir := set.String("results", "", "artifact directory")
	destroyPreserved := set.Bool("destroy-preserved", false, "explicitly clean a previously preserved enclave")
	finalizeTimeout := set.Duration("timeout", defaultFinalizeTimeout, "total diagnostics, cleanup, and artifact-repair budget")
	if err := set.Parse(arguments); err != nil {
		return usageError(err.Error())
	}
	if set.NArg() != 0 || *ownership == "" {
		return usageError("finalize requires --ownership and no positional arguments")
	}
	if *finalizeTimeout <= finalizer.DefaultDestroyReserve {
		return usageError(fmt.Sprintf("finalize --timeout must exceed the %s destroy reserve", finalizer.DefaultDestroyReserve))
	}
	if *resultsDir == "" {
		*resultsDir = filepath.Dir(*ownership)
	}
	writer, err := report.New(*resultsDir)
	if err != nil {
		return err
	}
	client, dependencies, clientErr := app.clientDependencies()
	result := finalizer.Result{}
	finalizeErr := error(nil)
	if clientErr != nil {
		finalizeErr = fmt.Errorf("construct Kurtosis client for finalization: %w", clientErr)
	} else {
		finalizeContext, cancelFinalize := context.WithTimeout(ctx, *finalizeTimeout)
		result, finalizeErr = finalizer.Run(finalizeContext, finalizer.Options{
			OwnershipPath: *ownership, Writer: writer, Client: client,
			Dumper: dependencies.Dumper, DestroyPreserved: *destroyPreserved, Now: app.Now,
		})
		cancelFinalize()
	}
	artifactErr := finalizer.CompleteStandaloneArtifacts(finalizer.ArtifactOptions{
		OwnershipPath: *ownership, Writer: writer, Result: result,
		FinalizeError: finalizeErr, Now: app.Now,
	})
	if err := writeJSON(app.Stdout, result); err != nil {
		return errors.Join(finalizeErr, artifactErr, err)
	}
	return errors.Join(finalizeErr, artifactErr)
}

type commonOptions struct {
	config config.RunConfig
	runID  string
}

func (app *App) parseCommon(name string, mode config.RunMode, arguments []string, owned bool) (commonOptions, error) {
	now := app.Now()
	runConfig := config.New(mode, now)
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(app.Stderr)
	repoRoot := set.String("repo-root", "", "go-qrl checkout root")
	set.StringVar(&runConfig.SourceSHA, "source-sha", "", "exact source commit")
	set.StringVar(&runConfig.NetworkParams, "network-params", "", "local-testnet parameter file")
	set.StringVar(&runConfig.ResultsDir, "results", "", "artifact directory")
	set.StringVar(&runConfig.EnclaveName, "enclave", "", "new enclave name")
	if !owned {
		set.StringVar(&runConfig.EnclaveIdentifier, "enclave-id", "", "existing enclave name or full UUID")
	}
	set.StringVar(&runConfig.PackageLocator, "package", config.DefaultPackageLocator, "pinned qrl-package repository")
	set.StringVar(&runConfig.PackageRevision, "package-revision", config.DefaultPackageRevision, "exact qrl-package commit")
	globalTimeout := set.Duration("global-timeout", config.DefaultGlobalRuntime, "total lifecycle budget including the cleanup reserve")
	set.DurationVar(&runConfig.CleanupReserve, "cleanup-reserve", config.DefaultCleanupReserve, "reserved finalization budget")
	set.BoolVar(&runConfig.PreserveOnFailure, "preserve-on-failure", true, "preserve an owned failed enclave for resume")
	set.BoolVar(&runConfig.AllowDisruptive, "allow-disruptive", false, "allow state-changing suites against a borrowed network")
	set.BoolVar(&runConfig.CI, "ci", false, "enable clean, source-pinned CI preparation")
	if err := set.Parse(arguments); err != nil {
		return commonOptions{}, usageError(err.Error())
	}
	if set.NArg() != 0 {
		return commonOptions{}, usageError("unexpected positional arguments")
	}
	root, err := resolveRepoRoot(*repoRoot)
	if err != nil {
		return commonOptions{}, err
	}
	runConfig.RepoRoot = root
	if runConfig.SourceSHA == "" {
		runConfig.SourceSHA, err = source.Commit(context.Background(), nil, root)
		if err != nil {
			return commonOptions{}, err
		}
	}
	if runConfig.NetworkParams == "" {
		runConfig.NetworkParams = filepath.Join(root, "scripts", "local_testnet", "network_params.yaml")
	}
	runID, err := newRunID(now)
	if err != nil {
		return commonOptions{}, err
	}
	if runConfig.ResultsDir == "" {
		runConfig.ResultsDir = filepath.Join(os.TempDir(), "vm64e2e-"+runID)
	}
	runConfig.GlobalDeadline = now.Add(*globalTimeout)
	if mode == config.ModeTest {
		// --enclave is the convenient spelling for borrowed networks too.
		if runConfig.EnclaveIdentifier == "" {
			runConfig.EnclaveIdentifier = runConfig.EnclaveName
		}
		runConfig.EnclaveName = ""
	}
	return commonOptions{config: runConfig, runID: runID}, nil
}

func (app *App) clientDependencies() (kurtosis.Client, harness.Dependencies, error) {
	if app.ClientFactory == nil {
		return nil, harness.Dependencies{}, errors.New("Kurtosis client factory is nil")
	}
	client, err := app.ClientFactory()
	if err != nil {
		return nil, harness.Dependencies{}, err
	}
	dependencies := app.Dependencies
	dependencies.Now = app.Now
	dependencies.Output = app.Stdout
	if dependencies.Logger == nil {
		dependencies.Logger = slog.New(slog.NewJSONHandler(app.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	}
	return client, dependencies, nil
}

func resolveRepoRoot(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		payload, readErr := os.ReadFile(filepath.Join(current, "go.mod"))
		if readErr == nil && strings.Contains(string(payload), "module github.com/theQRL/go-qrl\n") {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", errors.New("could not find the go-qrl repository root")
		}
		current = parent
	}
}

func newRunID(now time.Time) (string, error) {
	random := make([]byte, 6)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return now.UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(random), nil
}

func writeJSON(writer io.Writer, value any) error {
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type UsageError struct{ Message string }

func (err *UsageError) Error() string { return err.Message + "\n\n" + usage() }

func usageError(message string) error { return &UsageError{Message: message} }

func usage() string {
	return `Usage: vm64e2e <command> [options]

Commands:
  doctor    validate exact tools, engine, images, and configuration
  run       create and own an enclave, run every suite, diagnose, and clean up
  test      test a borrowed enclave; state-changing suites require --allow-disruptive
  resume    reconcile external state and continue at the failed checkpoint stage
  finalize  independently collect diagnostics and clean up by captured full UUID
`
}

func pinnedImages(path string) ([]string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var images []string
	for _, line := range strings.Split(string(payload), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "PINNED_") || !strings.Contains(line, "_IMAGE=") {
			continue
		}
		_, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = strings.Trim(value, "'\"")
		images = append(images, value)
	}
	return images, nil
}
