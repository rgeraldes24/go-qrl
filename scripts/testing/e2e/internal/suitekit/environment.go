// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package suitekit provides the common, suite-facing boundary for a live E2E
// invocation. The suite authenticates a separately managed network directly;
// no intermediate test orchestrator injects network identity or endpoints.
package suitekit

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/network"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/source"
)

const (
	DefaultNetworkDir = "/tmp/go-qrl-e2e-network"

	networkDirVariable   = "E2E_NETWORK_DIR"
	repoRootVariable     = "E2E_REPO_ROOT"
	rpcURLVariable       = "RPC URL"
	graphqlURLVariable   = "GraphQL URL"
	webSocketURLVariable = "WebSocket URL"
	seedFileVariable     = "seed file"
)

// Environment is the authenticated network contract used by one live suite.
// SeedFile is a path to private state; the wallet seed itself never leaves the
// network directory or appears in this value.
type Environment struct {
	RPCURL       string
	GraphQLURL   string
	WebSocketURL string
	SeedFile     string
}

type liveEnvironmentDependencies struct {
	getenv       func(string) string
	getwd        func() (string, error)
	findRepoRoot func(string) (string, error)
	authenticate func(context.Context, string, string, network.Requirements) (network.Environment, error)
	acquireLease func(string) (*network.MutationLease, error)
}

// PrepareLiveEnvironment acquires the independently managed network's
// exclusive mutation lease before authenticating it. The caller must close the
// returned lease. Only the requested optional network surfaces are inspected
// and exposed.
func PrepareLiveEnvironment(
	ctx context.Context,
	requirements network.Requirements,
) (Environment, *network.MutationLease, error) {
	manager := network.NewManager()
	dependencies := liveEnvironmentDependencies{
		getenv:       os.Getenv,
		getwd:        os.Getwd,
		findRepoRoot: source.FindRepoRoot,
		authenticate: manager.Authenticate,
		acquireLease: network.AcquireMutationLease,
	}
	return resolveLiveEnvironment(ctx, requirements, dependencies)
}

func resolveLiveEnvironment(
	ctx context.Context,
	requirements network.Requirements,
	dependencies liveEnvironmentDependencies,
) (Environment, *network.MutationLease, error) {
	if ctx == nil {
		return Environment{}, nil, errors.New("prepare live E2E environment: context is nil")
	}
	if dependencies.getenv == nil || dependencies.getwd == nil ||
		dependencies.findRepoRoot == nil || dependencies.authenticate == nil ||
		dependencies.acquireLease == nil {
		return Environment{}, nil, errors.New("prepare live E2E environment: dependencies are incomplete")
	}

	var workingDirectory string
	resolvePath := func(name, value string) (string, error) {
		if value != strings.TrimSpace(value) {
			return "", fmt.Errorf("%s must not contain leading or trailing whitespace", name)
		}
		if value == "" {
			return "", fmt.Errorf("%s is empty", name)
		}
		if filepath.IsAbs(value) {
			return filepath.Clean(value), nil
		}
		if workingDirectory == "" {
			var err error
			workingDirectory, err = dependencies.getwd()
			if err != nil {
				return "", fmt.Errorf("resolve working directory: %w", err)
			}
		}
		absolute, err := filepath.Abs(filepath.Join(workingDirectory, value))
		if err != nil {
			return "", fmt.Errorf("resolve %s: %w", name, err)
		}
		return filepath.Clean(absolute), nil
	}
	closeLease := func(lease *network.MutationLease, cause error) (Environment, *network.MutationLease, error) {
		return Environment{}, nil, errors.Join(cause, lease.Close())
	}

	networkValue := dependencies.getenv(networkDirVariable)
	if networkValue == "" {
		networkValue = DefaultNetworkDir
	}
	networkDir, err := resolvePath(networkDirVariable, networkValue)
	if err != nil {
		return Environment{}, nil, err
	}
	lease, err := dependencies.acquireLease(networkDir)
	if err != nil {
		return Environment{}, nil, err
	}
	if lease == nil {
		return Environment{}, nil, errors.New("prepare live E2E environment: mutation lease is nil")
	}

	repoRootValue := dependencies.getenv(repoRootVariable)
	var repoRoot string
	if repoRootValue == "" {
		if workingDirectory == "" {
			workingDirectory, err = dependencies.getwd()
			if err != nil {
				return closeLease(lease, fmt.Errorf("resolve working directory: %w", err))
			}
		}
		repoRoot, err = dependencies.findRepoRoot(workingDirectory)
		if err == nil {
			repoRoot, err = resolvePath(repoRootVariable, repoRoot)
		}
	} else {
		repoRoot, err = resolvePath(repoRootVariable, repoRootValue)
	}
	if err != nil {
		return closeLease(lease, err)
	}

	authenticated, err := dependencies.authenticate(ctx, repoRoot, lease.NetworkDir(), requirements)
	if err != nil {
		return closeLease(lease, fmt.Errorf("authenticate live E2E network: %w", err))
	}
	if authenticated.NetworkDir != lease.NetworkDir() {
		return closeLease(
			lease,
			errors.New("authenticated network directory changed while acquiring its mutation lease"),
		)
	}

	missing := make([]string, 0, 4)
	if strings.TrimSpace(authenticated.RPCURL) == "" {
		missing = append(missing, rpcURLVariable)
	}
	if requirements.Signer && strings.TrimSpace(authenticated.SeedFile) == "" {
		missing = append(missing, seedFileVariable)
	}
	if requirements.GraphQL && strings.TrimSpace(authenticated.GraphQLURL) == "" {
		missing = append(missing, graphqlURLVariable)
	}
	if requirements.WebSocket && strings.TrimSpace(authenticated.WebSocketURL) == "" {
		missing = append(missing, webSocketURLVariable)
	}
	if len(missing) != 0 {
		return closeLease(
			lease,
			fmt.Errorf("authenticated live E2E network omitted %s", strings.Join(missing, ", ")),
		)
	}

	environment := Environment{
		RPCURL: authenticated.RPCURL,
	}
	if requirements.Signer {
		environment.SeedFile = authenticated.SeedFile
	}
	if requirements.GraphQL {
		environment.GraphQLURL = authenticated.GraphQLURL
	}
	if requirements.WebSocket {
		environment.WebSocketURL = authenticated.WebSocketURL
	}
	return environment, lease, nil
}
