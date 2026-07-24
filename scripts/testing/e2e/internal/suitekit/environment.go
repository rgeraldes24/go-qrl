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
)

const (
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
	authenticate func(context.Context, string, string) (network.Environment, error)
	acquireLease func(string) (*network.MutationLease, error)
}

// PrepareLiveEnvironment acquires the independently managed network's
// exclusive mutation lease before authenticating every endpoint. The caller
// must close the returned lease.
func PrepareLiveEnvironment(ctx context.Context) (Environment, *network.MutationLease, error) {
	manager := network.NewManager()
	dependencies := liveEnvironmentDependencies{
		getenv:       os.Getenv,
		authenticate: manager.Authenticate,
		acquireLease: network.AcquireMutationLease,
	}
	return resolveLiveEnvironment(ctx, dependencies)
}

func resolveLiveEnvironment(
	ctx context.Context,
	dependencies liveEnvironmentDependencies,
) (Environment, *network.MutationLease, error) {
	if ctx == nil {
		return Environment{}, nil, errors.New("prepare live E2E environment: context is nil")
	}
	if dependencies.getenv == nil || dependencies.authenticate == nil ||
		dependencies.acquireLease == nil {
		return Environment{}, nil, errors.New("prepare live E2E environment: dependencies are incomplete")
	}
	resolvePath := func(name string) (string, error) {
		value := dependencies.getenv(name)
		if value != strings.TrimSpace(value) {
			return "", fmt.Errorf("%s must not contain leading or trailing whitespace", name)
		}
		if value == "" || !filepath.IsAbs(value) {
			return "", fmt.Errorf("%s must be an absolute path", name)
		}
		return filepath.Clean(value), nil
	}
	closeLease := func(lease *network.MutationLease, cause error) (Environment, *network.MutationLease, error) {
		return Environment{}, nil, errors.Join(cause, lease.Close())
	}

	networkDir, err := resolvePath(networkDirVariable)
	if err != nil {
		return Environment{}, nil, err
	}
	repoRoot, err := resolvePath(repoRootVariable)
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

	authenticated, err := dependencies.authenticate(ctx, repoRoot, lease.NetworkDir())
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
	if strings.TrimSpace(authenticated.SeedFile) == "" {
		missing = append(missing, seedFileVariable)
	}
	if strings.TrimSpace(authenticated.GraphQLURL) == "" {
		missing = append(missing, graphqlURLVariable)
	}
	if strings.TrimSpace(authenticated.WebSocketURL) == "" {
		missing = append(missing, webSocketURLVariable)
	}
	if len(missing) != 0 {
		return closeLease(
			lease,
			fmt.Errorf("authenticated live E2E network omitted %s", strings.Join(missing, ", ")),
		)
	}

	return Environment{
		RPCURL:       authenticated.RPCURL,
		GraphQLURL:   authenticated.GraphQLURL,
		WebSocketURL: authenticated.WebSocketURL,
		SeedFile:     authenticated.SeedFile,
	}, lease, nil
}
