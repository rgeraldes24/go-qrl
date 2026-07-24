// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package app implements the standalone E2E network command without calling
// os.Exit. Ginkgo owns suite execution; this command only manages the network.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/network"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/source"
)

const (
	DefaultNetworkDir   = "/tmp/go-qrl-e2e-network"
	defaultStartTimeout = 150 * time.Minute
	rootUsage           = `Usage:
  e2e network <start|status|stop> [options]

Manage the separately running E2E network. Suite execution is provided by the
pinned Ginkgo tool through make live-test. Network commands never execute
tests, and tests never start or stop the network.

Commands:
  start   Start or authenticate the test network.
  status  Authenticate and inspect the test network.
  stop    Stop the exact owned test network.
`
)

type App struct {
	Stdout   io.Writer
	Stderr   io.Writer
	Getwd    func() (string, error)
	Networks network.Controller
}

func New() *App {
	return &App{
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getwd:  os.Getwd,
	}
}

func (app *App) Execute(ctx context.Context, arguments []string) error {
	app.normalize()
	if len(arguments) == 0 || isHelp(arguments[0]) {
		fmt.Fprint(app.Stdout, rootUsage)
		return nil
	}
	if arguments[0] != "network" {
		return newUsageError("unknown command %q", arguments[0])
	}
	if len(arguments) == 1 || isHelp(arguments[1]) {
		fmt.Fprint(app.Stdout, rootUsage)
		return nil
	}
	switch arguments[1] {
	case "start":
		return app.networkStart(ctx, arguments[2:])
	case "status", "stop":
		return app.networkOperation(ctx, arguments[1], arguments[2:])
	default:
		return newUsageError("unknown network command %q", arguments[1])
	}
}

func (app *App) networkStart(ctx context.Context, arguments []string) error {
	timeout, err := durationFromEnvironment("E2E_NETWORK_START_TIMEOUT", defaultStartTimeout)
	if err != nil {
		return newUsageError("%v", err)
	}
	var (
		networkDir  = environmentValue("E2E_NETWORK_DIR", DefaultNetworkDir)
		root        string
		buildTool   = environmentValue("E2E_NETWORK_BUILD_TOOL", "")
		enclaveName = environmentValue("E2E_ENCLAVE_NAME", "")
		dockerBin   = environmentValue("E2E_DOCKER_BIN", "docker")
	)
	flags := app.newFlagSet("e2e network start")
	flags.StringVar(&networkDir, "network-dir", networkDir, "durable E2E network directory")
	flags.StringVar(&root, "repo-root", "", "go-qrl checkout root; discovered when omitted")
	flags.StringVar(&buildTool, "build-tool", buildTool, "pinned network-image build tool")
	flags.StringVar(&enclaveName, "enclave", enclaveName, "optional enclave name")
	flags.StringVar(&dockerBin, "docker-bin", dockerBin, "Docker command path")
	flags.DurationVar(&timeout, "start-timeout", timeout, "network provisioning and readiness budget")
	help, err := parseFlags(flags, arguments)
	if err != nil || help {
		return err
	}

	if root == "" {
		root, err = app.Getwd()
		if err != nil {
			return err
		}
		root, err = source.FindRepoRoot(root)
		if err != nil {
			return err
		}
	}
	root, err = absolutePath(root)
	if err != nil {
		return err
	}
	if buildTool == "" {
		buildTool = filepath.Join(root, "scripts", "local_testnet", "build_network_images.sh")
	}
	networkDir, err = absolutePath(networkDir)
	if err != nil {
		return err
	}
	buildTool, err = absolutePath(buildTool)
	if err != nil {
		return err
	}

	result, runErr := app.Networks.Start(ctx, network.StartRequest{
		RepoRoot:     root,
		NetworkDir:   networkDir,
		EnclaveName:  enclaveName,
		BuildTool:    buildTool,
		DockerBin:    dockerBin,
		StartTimeout: timeout,
	})
	return errors.Join(runErr, writeJSON(app.Stdout, result))
}

func (app *App) networkOperation(ctx context.Context, operation string, arguments []string) error {
	networkDir := environmentValue("E2E_NETWORK_DIR", DefaultNetworkDir)
	flags := app.newFlagSet("e2e network " + operation)
	flags.StringVar(&networkDir, "network-dir", networkDir, "durable E2E network directory")
	help, err := parseFlags(flags, arguments)
	if err != nil || help {
		return err
	}
	networkDir, err = absolutePath(networkDir)
	if err != nil {
		return err
	}
	var result network.Result
	if operation == "status" {
		result, err = app.Networks.Status(ctx, networkDir)
	} else {
		result, err = app.Networks.Stop(ctx, networkDir)
	}
	return errors.Join(err, writeJSON(app.Stdout, result))
}

func (app *App) newFlagSet(name string) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(app.Stderr)
	flags.Usage = func() {
		fmt.Fprintf(app.Stdout, "Usage:\n  %s [options]\n\nOptions:\n", name)
		flags.SetOutput(app.Stdout)
		flags.PrintDefaults()
		flags.SetOutput(app.Stderr)
	}
	return flags
}

func parseFlags(flags *flag.FlagSet, arguments []string) (bool, error) {
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return true, nil
		}
		return false, &exitError{code: 2, err: err}
	}
	if flags.NArg() != 0 {
		return false, newUsageError("unexpected positional arguments: %v", flags.Args())
	}
	return false, nil
}

func isHelp(argument string) bool {
	return argument == "-h" || argument == "--help" || argument == "help"
}

func environmentValue(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func durationFromEnvironment(name string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(name)
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s=%q is not a duration: %w", name, value, err)
	}
	return duration, nil
}

func absolutePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(absolute), nil
}

func (app *App) normalize() {
	if app.Stdout == nil {
		app.Stdout = io.Discard
	}
	if app.Stderr == nil {
		app.Stderr = io.Discard
	}
	if app.Getwd == nil {
		app.Getwd = os.Getwd
	}
	if app.Networks == nil {
		manager := network.NewManager()
		manager.Stdout, manager.Stderr = app.Stdout, app.Stderr
		app.Networks = manager
	}
}

func writeJSON(destination io.Writer, value any) error {
	encoder := json.NewEncoder(destination)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	return encoder.Encode(value)
}

type exitError struct {
	code int
	err  error
}

func (err *exitError) Error() string { return err.err.Error() }
func (err *exitError) Unwrap() error { return err.err }
func (err *exitError) ExitCode() int { return err.code }

func newUsageError(format string, arguments ...any) error {
	return &exitError{code: 2, err: fmt.Errorf(format, arguments...)}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exited interface{ ExitCode() int }
	if errors.As(err, &exited) {
		if code := exited.ExitCode(); code > 0 && code < 256 {
			return code
		}
	}
	if errors.Is(err, context.Canceled) {
		return 130
	}
	return 1
}
