// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package app

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/network"
)

type recordingNetworks struct {
	startRequest network.StartRequest
	statusDir    string
	stopDir      string
	result       network.Result
	err          error
}

func (networks *recordingNetworks) Start(
	_ context.Context,
	request network.StartRequest,
) (network.Result, error) {
	networks.startRequest = request
	return networks.result, networks.err
}

func (networks *recordingNetworks) Status(
	_ context.Context,
	networkDir string,
) (network.Result, error) {
	networks.statusDir = networkDir
	return networks.result, networks.err
}

func (networks *recordingNetworks) Stop(
	_ context.Context,
	networkDir string,
) (network.Result, error) {
	networks.stopDir = networkDir
	return networks.result, networks.err
}

func TestHelpDescribesNetworkOnlyBoundary(t *testing.T) {
	var output bytes.Buffer
	command := New()
	command.Stdout = &output
	if err := command.Execute(t.Context(), []string{"--help"}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"network", "Ginkgo", "make live-test"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("help is missing %q:\n%s", expected, output.String())
		}
	}
}

func TestNetworkStartResolvesGenericInputs(t *testing.T) {
	root := t.TempDir()
	networkDir := filepath.Join(root, "runtime")
	networks := new(recordingNetworks)
	var output bytes.Buffer
	t.Setenv("E2E_NETWORK_START_TIMEOUT", "17m")
	t.Setenv("E2E_ENCLAVE_NAME", "e2e-example")
	t.Setenv("E2E_DOCKER_BIN", "/opt/bin/docker")
	command := &App{
		Stdout:   &output,
		Stderr:   new(bytes.Buffer),
		Getwd:    func() (string, error) { return root, nil },
		Networks: networks,
	}

	err := command.Execute(t.Context(), []string{
		"network", "start",
		"--repo-root", root,
		"--network-dir", networkDir,
	})
	if err != nil {
		t.Fatal(err)
	}

	request := networks.startRequest
	if request.RepoRoot != root || request.NetworkDir != networkDir {
		t.Fatalf("resolved roots = %+v", request)
	}
	wantBuildTool := filepath.Join(
		root, "scripts", "local_testnet", "build_network_images.sh",
	)
	if request.BuildTool != wantBuildTool {
		t.Fatalf("resolved paths = %+v", request)
	}
	if request.StartTimeout != 17*time.Minute {
		t.Fatalf("StartTimeout = %s, want 17m", request.StartTimeout)
	}
	if request.EnclaveName != "e2e-example" ||
		request.DockerBin != "/opt/bin/docker" {
		t.Fatalf("environment-backed request fields = %+v", request)
	}
	if !strings.Contains(output.String(), `"ready"`) ||
		strings.Contains(output.String(), `"state"`) {
		t.Fatalf("network result was not emitted as JSON:\n%s", output.String())
	}
}

func TestNetworkStatusAndStopUseExactDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("E2E_NETWORK_DIR", root)
	networks := &recordingNetworks{
		result: network.Result{
			Ready: true,
		},
	}

	for _, operation := range []string{"status", "stop"} {
		var output bytes.Buffer
		command := &App{
			Stdout:   &output,
			Stderr:   new(bytes.Buffer),
			Networks: networks,
		}
		if err := command.Execute(t.Context(), []string{"network", operation}); err != nil {
			t.Fatalf("network %s: %v", operation, err)
		}
		if !strings.Contains(output.String(), `"ready": true`) {
			t.Fatalf("network %s output = %s", operation, output.String())
		}
	}
	if networks.statusDir != root || networks.stopDir != root {
		t.Fatalf(
			"status directory = %q, stop directory = %q, want %q",
			networks.statusDir, networks.stopDir, root,
		)
	}
}

func TestFrameworkRejectsInvalidInputs(t *testing.T) {
	t.Run("removed suite command", func(t *testing.T) {
		err := New().Execute(t.Context(), []string{"test"})
		if got := ExitCode(err); got != 2 {
			t.Fatalf("ExitCode(%v) = %d, want 2", err, got)
		}
	})

	t.Run("unexpected argument", func(t *testing.T) {
		command := &App{
			Stdout:   new(bytes.Buffer),
			Stderr:   new(bytes.Buffer),
			Networks: new(recordingNetworks),
		}
		err := command.Execute(
			t.Context(),
			[]string{"network", "status", "unexpected"},
		)
		if got := ExitCode(err); got != 2 {
			t.Fatalf("ExitCode(%v) = %d, want 2", err, got)
		}
	})

	t.Run("unknown flag", func(t *testing.T) {
		command := &App{
			Stdout:   new(bytes.Buffer),
			Stderr:   new(bytes.Buffer),
			Networks: new(recordingNetworks),
		}
		err := command.Execute(
			t.Context(),
			[]string{"network", "status", "--unknown"},
		)
		if got := ExitCode(err); got != 2 {
			t.Fatalf("ExitCode(%v) = %d, want 2", err, got)
		}
	})

	t.Run("invalid timeout environment", func(t *testing.T) {
		t.Setenv("E2E_NETWORK_START_TIMEOUT", "eventually")
		command := &App{
			Stdout:   new(bytes.Buffer),
			Stderr:   new(bytes.Buffer),
			Networks: new(recordingNetworks),
		}
		err := command.Execute(t.Context(), []string{"network", "start"})
		if got := ExitCode(err); got != 2 {
			t.Fatalf("ExitCode(%v) = %d, want 2", err, got)
		}
	})
}

func TestExitCode(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Fatalf("ExitCode(nil) = %d", got)
	}
	if got := ExitCode(newUsageError("bad arguments")); got != 2 {
		t.Fatalf("ExitCode(usage) = %d", got)
	}
	if got := ExitCode(context.Canceled); got != 130 {
		t.Fatalf("ExitCode(canceled) = %d", got)
	}
	if got := ExitCode(errors.New("boom")); got != 1 {
		t.Fatalf("ExitCode(error) = %d", got)
	}
}
