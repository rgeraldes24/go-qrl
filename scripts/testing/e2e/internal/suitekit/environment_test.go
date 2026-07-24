// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package suitekit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/network"
)

func TestResolveLiveEnvironmentDiscoversRootAndUsesTypedRequirements(t *testing.T) {
	requestedNetwork, canonicalNetwork := testNetworkDirectory(t)
	workingDirectory := filepath.Join(t.TempDir(), "repo", "scripts", "testing", "e2e", "suites", "goabi")
	repoRoot := filepath.Clean(filepath.Join(workingDirectory, "../../../../.."))
	authenticated := validNetworkEnvironment(canonicalNetwork)
	var observedRequirements network.Requirements
	var observedRoot, observedNetwork string

	environment, lease, err := resolveLiveEnvironment(
		context.Background(),
		network.Requirements{GraphQL: true},
		liveEnvironmentDependencies{
			getenv: mapGetenv(map[string]string{networkDirVariable: requestedNetwork}),
			getwd:  func() (string, error) { return workingDirectory, nil },
			findRepoRoot: func(start string) (string, error) {
				if start != workingDirectory {
					t.Fatalf("repository search started at %s, want %s", start, workingDirectory)
				}
				return repoRoot, nil
			},
			authenticate: func(
				_ context.Context,
				root,
				networkDir string,
				requirements network.Requirements,
			) (network.Environment, error) {
				observedRoot = root
				observedNetwork = networkDir
				observedRequirements = requirements
				return authenticated, nil
			},
			acquireLease: network.AcquireMutationLease,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := lease.Close(); err != nil {
			t.Error(err)
		}
	})

	if observedRoot != repoRoot || observedNetwork != canonicalNetwork {
		t.Fatalf(
			"Authenticate(root, network) = (%s, %s), want (%s, %s)",
			observedRoot,
			observedNetwork,
			repoRoot,
			canonicalNetwork,
		)
	}
	if observedRequirements != (network.Requirements{GraphQL: true}) {
		t.Fatalf("network requirements = %+v", observedRequirements)
	}
	if environment.RPCURL != authenticated.RPCURL {
		t.Fatalf("resolved environment = %+v", environment)
	}
	if environment.GraphQLURL != authenticated.GraphQLURL ||
		environment.SeedFile != "" ||
		environment.WebSocketURL != "" {
		t.Fatalf("least-privilege environment = %+v", environment)
	}
}

func TestResolveLiveEnvironmentLocksBeforeAuthentication(t *testing.T) {
	workingDirectory := t.TempDir()
	requestedNetwork := filepath.Join(workingDirectory, "runtime", "network")
	if err := os.MkdirAll(filepath.Join(requestedNetwork, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	canonicalNetwork, err := filepath.EvalSymlinks(requestedNetwork)
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{
		networkDirVariable: "runtime/network",
		repoRootVariable:   "checkout",
	}
	wantRoot := filepath.Join(workingDirectory, "checkout")
	authenticated := validNetworkEnvironment(canonicalNetwork)
	authenticateCalls := 0
	dependencies := liveEnvironmentDependencies{
		getenv: mapGetenv(values),
		getwd:  func() (string, error) { return workingDirectory, nil },
		findRepoRoot: func(string) (string, error) {
			t.Fatal("explicit repository root triggered discovery")
			return "", nil
		},
		authenticate: func(
			_ context.Context,
			root,
			networkDir string,
			requirements network.Requirements,
		) (network.Environment, error) {
			authenticateCalls++
			if root != wantRoot || networkDir != canonicalNetwork {
				t.Fatalf(
					"Authenticate paths = (%s, %s), want (%s, %s)",
					root,
					networkDir,
					wantRoot,
					canonicalNetwork,
				)
			}
			if requirements != network.FullRequirements() {
				t.Fatalf("Authenticate requirements = %+v", requirements)
			}
			if competing, err := network.AcquireMutationLease(requestedNetwork); err == nil {
				_ = competing.Close()
				t.Fatal("network mutation lease was not held before authentication")
			} else if !strings.Contains(err.Error(), "already in progress") {
				t.Fatalf("competing mutation lease error = %v", err)
			}
			return authenticated, nil
		},
		acquireLease: network.AcquireMutationLease,
	}

	_, lease, err := resolveLiveEnvironment(
		context.Background(),
		network.FullRequirements(),
		dependencies,
	)
	if err != nil {
		t.Fatal(err)
	}
	if lease.NetworkDir() != canonicalNetwork {
		t.Fatalf("lease network = %s, want %s", lease.NetworkDir(), canonicalNetwork)
	}

	if competingEnvironment, competingLease, err := resolveLiveEnvironment(
		context.Background(),
		network.FullRequirements(),
		dependencies,
	); err == nil {
		_ = competingLease.Close()
		t.Fatalf("second suite invocation bypassed network lease: %+v", competingEnvironment)
	} else if !strings.Contains(err.Error(), "already in progress") {
		t.Fatalf("competing mutation lease error = %v", err)
	}
	if authenticateCalls != 1 {
		t.Fatalf("authentication calls = %d, want 1", authenticateCalls)
	}

	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := network.AcquireMutationLease(requestedNetwork)
	if err != nil {
		t.Fatalf("reacquire released network mutation lease: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLiveEnvironmentRejectsInvalidRequestsWithoutLeasing(t *testing.T) {
	tests := []struct {
		name   string
		ctx    context.Context
		mutate func(*liveEnvironmentDependencies)
		want   string
	}{
		{name: "nil context", want: "context is nil"},
		{
			name: "whitespace network path",
			ctx:  context.Background(),
			mutate: func(dependencies *liveEnvironmentDependencies) {
				dependencies.getenv = mapGetenv(map[string]string{networkDirVariable: " /tmp/network"})
			},
			want: "whitespace",
		},
		{
			name: "incomplete dependencies",
			ctx:  context.Background(),
			mutate: func(dependencies *liveEnvironmentDependencies) {
				dependencies.authenticate = nil
			},
			want: "dependencies are incomplete",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dependencies := liveEnvironmentDependencies{
				getenv:       func(string) string { return "" },
				getwd:        func() (string, error) { return "/work", nil },
				findRepoRoot: func(string) (string, error) { return "/repo", nil },
				authenticate: func(context.Context, string, string, network.Requirements) (network.Environment, error) {
					t.Fatal("authentication must not run")
					return network.Environment{}, nil
				},
				acquireLease: func(string) (*network.MutationLease, error) {
					t.Fatal("mutation lease must not be acquired")
					return nil, nil
				},
			}
			if test.mutate != nil {
				test.mutate(&dependencies)
			}
			_, _, err := resolveLiveEnvironment(test.ctx, network.Requirements{}, dependencies)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("resolveLiveEnvironment error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveLiveEnvironmentRequiresRequestedSurfacesAndReleasesLease(t *testing.T) {
	authenticationError := errors.New("network unavailable")
	tests := []struct {
		name         string
		requirements network.Requirements
		mutate       func(*network.Environment) error
		want         string
	}{
		{
			name:   "authentication failure",
			mutate: func(*network.Environment) error { return authenticationError },
			want:   "authenticate live E2E network",
		},
		{
			name:   "RPC",
			mutate: func(value *network.Environment) error { value.RPCURL = ""; return nil },
			want:   rpcURLVariable,
		},
		{
			name: "network directory changed",
			mutate: func(value *network.Environment) error {
				value.NetworkDir = filepath.Join(value.NetworkDir, "other")
				return nil
			},
			want: "changed while acquiring",
		},
		{
			name:         "signer",
			requirements: network.Requirements{Signer: true},
			mutate:       func(value *network.Environment) error { value.SeedFile = ""; return nil },
			want:         seedFileVariable,
		},
		{
			name:         "GraphQL",
			requirements: network.Requirements{GraphQL: true},
			mutate:       func(value *network.Environment) error { value.GraphQLURL = ""; return nil },
			want:         graphqlURLVariable,
		},
		{
			name:         "WebSocket",
			requirements: network.Requirements{WebSocket: true},
			mutate:       func(value *network.Environment) error { value.WebSocketURL = ""; return nil },
			want:         webSocketURLVariable,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			requestedNetwork, canonicalNetwork := testNetworkDirectory(t)
			authenticated := validNetworkEnvironment(canonicalNetwork)
			dependencies := liveEnvironmentDependencies{
				getenv: mapGetenv(map[string]string{
					networkDirVariable: requestedNetwork,
					repoRootVariable:   t.TempDir(),
				}),
				getwd:        os.Getwd,
				findRepoRoot: func(string) (string, error) { return "", errors.New("unexpected discovery") },
				authenticate: func(context.Context, string, string, network.Requirements) (network.Environment, error) {
					if err := test.mutate(&authenticated); err != nil {
						return network.Environment{}, err
					}
					return authenticated, nil
				},
				acquireLease: network.AcquireMutationLease,
			}
			_, lease, err := resolveLiveEnvironment(
				context.Background(),
				test.requirements,
				dependencies,
			)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				if lease != nil {
					_ = lease.Close()
				}
				t.Fatalf("resolveLiveEnvironment error = %v, want %q", err, test.want)
			}
			reopened, reopenErr := network.AcquireMutationLease(requestedNetwork)
			if reopenErr != nil {
				t.Fatalf("failure leaked network mutation lease: %v", reopenErr)
			}
			if err := reopened.Close(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func testNetworkDirectory(t *testing.T) (string, string) {
	t.Helper()
	requested := t.TempDir()
	if err := os.Mkdir(filepath.Join(requested, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(requested)
	if err != nil {
		t.Fatal(err)
	}
	return requested, canonical
}

func validNetworkEnvironment(networkDir string) network.Environment {
	return network.Environment{
		NetworkDir:   networkDir,
		RPCURL:       "http://127.0.0.1:18545",
		GraphQLURL:   "http://127.0.0.1:18545/graphql",
		WebSocketURL: "ws://127.0.0.1:18546",
		SeedFile:     filepath.Join(networkDir, "private", "wallet.seed"),
	}
}

func mapGetenv(values map[string]string) func(string) string {
	return func(name string) string {
		return values[name]
	}
}
