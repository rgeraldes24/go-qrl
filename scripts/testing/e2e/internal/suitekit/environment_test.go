// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package suitekit

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/network"
)

func TestResolveLiveEnvironmentAuthenticatesWhileHoldingLease(t *testing.T) {
	networkDir := testNetworkDirectory(t)
	repoRoot := t.TempDir()
	want := validNetworkEnvironment(networkDir)
	calls := 0
	dependencies := liveEnvironmentDependencies{
		getenv: mapGetenv(map[string]string{
			networkDirVariable: networkDir,
			repoRootVariable:   repoRoot,
		}),
		authenticate: func(_ context.Context, root, requestedNetwork string) (network.Environment, error) {
			calls++
			if root != repoRoot || requestedNetwork != networkDir {
				t.Fatalf("authenticate paths = (%q, %q)", root, requestedNetwork)
			}
			if competing, err := network.AcquireMutationLease(networkDir); err == nil {
				_ = competing.Close()
				t.Fatal("network lease was not held during authentication")
			}
			return want, nil
		},
		acquireLease: network.AcquireMutationLease,
	}

	got, lease, err := resolveLiveEnvironment(context.Background(), dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 || lease.NetworkDir() != networkDir {
		t.Fatalf("calls = %d, lease = %q", calls, lease.NetworkDir())
	}
	if !reflect.DeepEqual(got, Environment{
		RPCURL:       want.RPCURL,
		GraphQLURL:   want.GraphQLURL,
		WebSocketURL: want.WebSocketURL,
		SeedFile:     want.SeedFile,
	}) {
		t.Fatalf("environment = %+v", got)
	}
	if _, competing, err := resolveLiveEnvironment(context.Background(), dependencies); err == nil {
		_ = competing.Close()
		t.Fatal("concurrent suite acquired the same network")
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := network.AcquireMutationLease(networkDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestResolveLiveEnvironmentRequiresExplicitAbsolutePaths(t *testing.T) {
	valid := map[string]string{
		networkDirVariable: "/tmp/network",
		repoRootVariable:   "/tmp/repository",
	}
	tests := []struct {
		name, variable, value, want string
		ctx                         context.Context
	}{
		{name: "nil context", ctx: nil, want: "context is nil"},
		{name: "missing network", ctx: context.Background(), variable: networkDirVariable, value: "", want: "absolute path"},
		{name: "relative repository", ctx: context.Background(), variable: repoRootVariable, value: "repository", want: "absolute path"},
		{name: "whitespace network", ctx: context.Background(), variable: networkDirVariable, value: " /tmp/network", want: "whitespace"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			values := map[string]string{
				networkDirVariable: valid[networkDirVariable],
				repoRootVariable:   valid[repoRootVariable],
			}
			if test.variable != "" {
				values[test.variable] = test.value
			}
			dependencies := liveEnvironmentDependencies{
				getenv: mapGetenv(values),
				authenticate: func(context.Context, string, string) (network.Environment, error) {
					t.Fatal("authentication must not run")
					return network.Environment{}, nil
				},
				acquireLease: func(string) (*network.MutationLease, error) {
					t.Fatal("lease must not be acquired")
					return nil, nil
				},
			}
			if _, _, err := resolveLiveEnvironment(test.ctx, dependencies); err == nil ||
				!strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveLiveEnvironmentReleasesLeaseOnAuthenticationFailure(t *testing.T) {
	networkDir := testNetworkDirectory(t)
	dependencies := liveEnvironmentDependencies{
		getenv: mapGetenv(map[string]string{
			networkDirVariable: networkDir,
			repoRootVariable:   t.TempDir(),
		}),
		authenticate: func(context.Context, string, string) (network.Environment, error) {
			return network.Environment{}, errors.New("network unavailable")
		},
		acquireLease: network.AcquireMutationLease,
	}
	if _, _, err := resolveLiveEnvironment(context.Background(), dependencies); err == nil ||
		!strings.Contains(err.Error(), "authenticate live E2E network") {
		t.Fatalf("authentication error = %v", err)
	}
	reopened, err := network.AcquireMutationLease(networkDir)
	if err != nil {
		t.Fatalf("failure leaked network lease: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

func testNetworkDirectory(t *testing.T) string {
	t.Helper()
	requested := t.TempDir()
	if err := os.Mkdir(filepath.Join(requested, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(requested)
	if err != nil {
		t.Fatal(err)
	}
	return canonical
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
	return func(name string) string { return values[name] }
}
