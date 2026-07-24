// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package app implements the standalone E2E network command without calling
// os.Exit. Ginkgo owns suite execution; this command only manages the network.
package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/network"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/source"
	"github.com/urfave/cli/v2"
)

const DefaultNetworkDir = "/tmp/go-qrl-e2e-network"

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
	command := &cli.App{
		Name:      "e2e",
		Usage:     "manage the separately running E2E network",
		UsageText: "e2e network <start|status|stop> [options]",
		Description: "Suite execution is provided by the pinned Ginkgo tool " +
			"through make live-test. Network commands never execute tests, " +
			"and tests never start or stop the network.",
		Writer:         app.Stdout,
		ErrWriter:      app.Stderr,
		ExitErrHandler: func(*cli.Context, error) {},
		Commands: []*cli.Command{
			app.networkCommand(),
		},
	}
	return command.RunContext(ctx, append([]string{command.Name}, arguments...))
}

func (app *App) networkCommand() *cli.Command {
	return &cli.Command{
		Name:      "network",
		Usage:     "start, inspect, or stop a separately managed network",
		UsageText: "e2e network <start|status|stop> [options]",
		Subcommands: []*cli.Command{
			{
				Name:   "start",
				Usage:  "start or authenticate the test network",
				Flags:  startFlags(),
				Action: app.networkStart,
			},
			{
				Name:   "status",
				Usage:  "authenticate and inspect the test network",
				Flags:  []cli.Flag{networkDirFlag()},
				Action: app.networkStatus,
			},
			{
				Name:   "stop",
				Usage:  "stop the owned test network",
				Flags:  []cli.Flag{networkDirFlag()},
				Action: app.networkStop,
			},
		},
	}
}

func startFlags() []cli.Flag {
	return []cli.Flag{
		networkDirFlag(),
		&cli.PathFlag{
			Name:  "repo-root",
			Usage: "go-qrl checkout root; discovered when omitted",
		},
		&cli.PathFlag{
			Name:    "build-tool",
			EnvVars: []string{"E2E_NETWORK_BUILD_TOOL"},
			Usage:   "pinned network-image build tool",
		},
		&cli.StringFlag{
			Name:    "enclave",
			EnvVars: []string{"E2E_ENCLAVE_NAME"},
			Usage:   "optional enclave name",
		},
		&cli.PathFlag{
			Name:    "docker-bin",
			Value:   "docker",
			EnvVars: []string{"E2E_DOCKER_BIN"},
			Usage:   "Docker command path",
		},
		&cli.DurationFlag{
			Name:    "start-timeout",
			Value:   150 * time.Minute,
			EnvVars: []string{"E2E_NETWORK_START_TIMEOUT"},
			Usage:   "network provisioning and readiness budget",
		},
	}
}

func networkDirFlag() cli.Flag {
	return &cli.PathFlag{
		Name:    "network-dir",
		Value:   DefaultNetworkDir,
		EnvVars: []string{"E2E_NETWORK_DIR"},
		Usage:   "durable E2E network directory",
	}
}

func (app *App) networkStart(ctx *cli.Context) error {
	if err := rejectArguments(ctx); err != nil {
		return err
	}

	root := ctx.Path("repo-root")
	var err error
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

	buildTool := ctx.Path("build-tool")
	if buildTool == "" {
		buildTool = filepath.Join(root, "scripts", "local_testnet", "build_network_images.sh")
	}
	networkDir, err := absolutePath(ctx.Path("network-dir"))
	if err != nil {
		return err
	}
	buildTool, err = absolutePath(buildTool)
	if err != nil {
		return err
	}

	result, runErr := app.Networks.Start(ctx.Context, network.StartRequest{
		RepoRoot:     root,
		NetworkDir:   networkDir,
		EnclaveName:  ctx.String("enclave"),
		BuildTool:    buildTool,
		DockerBin:    ctx.Path("docker-bin"),
		StartTimeout: ctx.Duration("start-timeout"),
	})
	return errors.Join(runErr, writeJSON(app.Stdout, result))
}

func (app *App) networkStatus(ctx *cli.Context) error {
	if err := rejectArguments(ctx); err != nil {
		return err
	}
	networkDir, err := absolutePath(ctx.Path("network-dir"))
	if err != nil {
		return err
	}
	result, statusErr := app.Networks.Status(ctx.Context, networkDir)
	return errors.Join(statusErr, writeJSON(app.Stdout, result))
}

func (app *App) networkStop(ctx *cli.Context) error {
	if err := rejectArguments(ctx); err != nil {
		return err
	}
	networkDir, err := absolutePath(ctx.Path("network-dir"))
	if err != nil {
		return err
	}
	result, stopErr := app.Networks.Stop(ctx.Context, networkDir)
	return errors.Join(stopErr, writeJSON(app.Stdout, result))
}

func rejectArguments(ctx *cli.Context) error {
	if ctx.Args().Len() == 0 {
		return nil
	}
	return cli.Exit(
		fmt.Sprintf("unexpected positional arguments: %v", ctx.Args().Slice()),
		2,
	)
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

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exited cli.ExitCoder
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
