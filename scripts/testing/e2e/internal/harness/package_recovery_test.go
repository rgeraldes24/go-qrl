// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/config"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/provision"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
)

func TestPackageReconcileRecoversAcceptedInvocationWithoutReplay(t *testing.T) {
	runtime, fake, run := newPackageRecoveryRuntime(t)
	if _, err := runtime.ensurePackageInvocationIntent(run); err != nil {
		t.Fatal(err)
	}
	fake.LastInvocation = kurtosis.PackageInvocation{
		Locator:          run.Locator,
		SerializedParams: `{"network_params":{"preset":"mainnet"},"participants":[]}`,
	}

	action, err := runtime.packageReconcile(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if action != lifecycle.ReconcileComplete || runtime.PackageResult == nil {
		t.Fatalf("package reconciliation = %q, result = %+v", action, runtime.PackageResult)
	}
	output, err := topology.ParsePackageOutput(runtime.PackageResult.SerializedOutput)
	if err != nil {
		t.Fatalf("recovered package output: %v", err)
	}
	if output.NetworkID != "3151908" || output.FinalGenesisTimestamp != "1784625026" {
		t.Fatalf("recovered metadata = %+v", output)
	}
	if _, err := os.Stat(filepath.Join(runtime.Writer.Layout().Kurtosis, "package-recovery.json")); err != nil {
		t.Fatalf("package recovery evidence: %v", err)
	}
	if _, err := os.Stat(filepath.Join(runtime.Writer.Layout().Kurtosis, "package-output.json")); err != nil {
		t.Fatalf("recovered package output artifact: %v", err)
	}
	for _, call := range fake.Calls {
		if strings.HasPrefix(call, "package:") {
			t.Fatalf("package was replayed during response-loss recovery: %v", fake.Calls)
		}
	}

	// A second reconciliation uses the durable output and remains observational.
	if action, err := runtime.packageReconcile(t.Context(), nil); err != nil || action != lifecycle.ReconcileComplete {
		t.Fatalf("second reconciliation = %q, %v", action, err)
	}
}

func TestPackageReconcileAcceptedInvocationWithZeroServicesNeverReplays(t *testing.T) {
	runtime, fake, run := newPackageRecoveryRuntime(t)
	if _, err := runtime.ensurePackageInvocationIntent(run); err != nil {
		t.Fatal(err)
	}
	removePackageRecoveryServices(t, fake, runtime.Enclave)
	fake.LastInvocation = kurtosis.PackageInvocation{
		Locator:          run.Locator,
		SerializedParams: `{"network_params":{"preset":"mainnet"},"participants":[]}`,
	}

	action, err := runtime.packageReconcile(t.Context(), nil)
	if err == nil || !strings.Contains(err.Error(), "exact package invocation was accepted") || action != "" {
		t.Fatalf("accepted zero-service reconciliation = %q, %v", action, err)
	}
	for _, call := range fake.Calls {
		if strings.HasPrefix(call, "package:") {
			t.Fatalf("accepted package invocation was replayed: %v", fake.Calls)
		}
	}
}

func TestPackageReconcileRetriesZeroServicesOnlyWhenInvocationIsProvenAbsent(t *testing.T) {
	runtime, fake, run := newPackageRecoveryRuntime(t)
	if _, err := runtime.ensurePackageInvocationIntent(run); err != nil {
		t.Fatal(err)
	}
	removePackageRecoveryServices(t, fake, runtime.Enclave)

	action, err := runtime.packageReconcile(t.Context(), nil)
	if err != nil || action != lifecycle.ReconcileRetry {
		t.Fatalf("proven-absent package reconciliation = %q, %v", action, err)
	}

	fake.InvocationError = errors.New("API container unavailable")
	action, err = runtime.packageReconcile(t.Context(), nil)
	if err == nil || !strings.Contains(err.Error(), "inspect retained package invocation") || action != "" {
		t.Fatalf("ambiguous package reconciliation = %q, %v", action, err)
	}
}

func TestPackagePreparationEnvironmentPreservesRequestedPins(t *testing.T) {
	wantRepository := "github.com/example/qrl-package"
	wantRevision := strings.Repeat("7", 40)
	environment := packagePreparationEnvironment(config.RunConfig{
		PackageLocator: wantRepository, PackageRevision: wantRevision,
	})
	if environment["QRL_PACKAGE_REPO"] != wantRepository || environment["QRL_PKG_VERSION"] != wantRevision || len(environment) != 2 {
		t.Fatalf("package preparation environment = %v", environment)
	}
}

func TestPackageRecoveryRequiresPreCallIntent(t *testing.T) {
	runtime, fake, run := newPackageRecoveryRuntime(t)
	fake.LastInvocation = kurtosis.PackageInvocation{Locator: run.Locator, SerializedParams: run.SerializedParams}
	metadataCalls := 0
	runtime.Dependencies.PackageMetadata = func(_ context.Context, _ topology.Topology) (PackageNetworkMetadata, error) {
		metadataCalls++
		return PackageNetworkMetadata{}, nil
	}

	action, err := runtime.packageReconcile(t.Context(), nil)
	if err == nil || !errors.Is(err, os.ErrNotExist) || action != "" {
		t.Fatalf("reconciliation without intent = %q, %v", action, err)
	}
	if metadataCalls != 0 || runtime.PackageResult != nil {
		t.Fatalf("missing intent observed metadata %d times or produced %+v", metadataCalls, runtime.PackageResult)
	}
	if _, err := os.Stat(filepath.Join(runtime.Writer.Layout().Kurtosis, "package-invocation.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("recovery retroactively created package intent: %v", err)
	}
}

func TestPackageInvocationComparisonIsSemanticAndFailsClosed(t *testing.T) {
	expected := kurtosis.PackageRun{
		Locator:          "github.com/theqrl/qrl-package@" + harnessLifecycleSHA,
		SerializedParams: "participants: []\nnetwork_params:\n  preset: mainnet\n",
	}
	if err := verifyPackageInvocation(expected, kurtosis.PackageInvocation{
		Locator:          expected.Locator,
		SerializedParams: `{"network_params":{"preset":"mainnet"},"participants":[]}`,
	}); err != nil {
		t.Fatalf("semantically identical parameters rejected: %v", err)
	}
	tests := []struct {
		name       string
		invocation kurtosis.PackageInvocation
		want       string
	}{
		{name: "locator", invocation: kurtosis.PackageInvocation{Locator: "wrong", SerializedParams: expected.SerializedParams}, want: "locator"},
		{name: "parameters", invocation: kurtosis.PackageInvocation{Locator: expected.Locator, SerializedParams: `{"participants":[{}]}`}, want: "parameters differ"},
		{name: "malformed", invocation: kurtosis.PackageInvocation{Locator: expected.Locator, SerializedParams: "[invalid"}, want: "normalize retained"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := verifyPackageInvocation(expected, test.invocation)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("verifyPackageInvocation error = %v, want %q", err, test.want)
			}
		})
	}
}

func newPackageRecoveryRuntime(t *testing.T) (*Runtime, *kurtosis.FakeClient, kurtosis.PackageRun) {
	t.Helper()
	root := t.TempDir()
	writer, err := report.New(root)
	if err != nil {
		t.Fatal(err)
	}
	params := []byte("participants: []\nnetwork_params:\n  preset: mainnet\n")
	paramsPath := filepath.Join(root, "effective.yaml")
	if err := os.WriteFile(paramsPath, params, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(params)
	preparation := &provision.Preparation{
		QRLPackage:      provision.Source{Repository: "github.com/theqrl/qrl-package", Revision: harnessLifecycleSHA},
		EffectiveParams: provision.EffectiveParams{Path: paramsPath, SHA256: hex.EncodeToString(digest[:])},
	}
	fake := kurtosis.NewFakeClient()
	enclave, err := fake.CreateEnclave(t.Context(), "package-response-loss")
	if err != nil {
		t.Fatal(err)
	}
	spec := topology.DefaultSpec(harnessLifecycleSHA)
	for _, service := range packageRecoveryServices(spec) {
		if err := fake.AddService(enclave, service); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 7, 21, 10, 0, 0, 0, time.UTC)
	runtime := &Runtime{
		RunID: "package-response-loss", Enclave: enclave, Writer: writer,
		Preparation: preparation, TopologySpec: spec,
		Dependencies: Dependencies{
			Client: fake, Now: func() time.Time { return now },
			PackageMetadata: func(_ context.Context, discovered topology.Topology) (PackageNetworkMetadata, error) {
				if err := discovered.Validate(); err != nil {
					return PackageNetworkMetadata{}, err
				}
				return PackageNetworkMetadata{
					NetworkID: "3151908", FinalGenesisTimestamp: "1784625026",
					GenesisValidatorsRoot: "0x1234", GenesisForkVersion: "0x01020304",
				}, nil
			},
		},
	}
	run := kurtosis.PackageRun{Locator: preparation.PackageLocator(), SerializedParams: string(params)}
	return runtime, fake, run
}

func removePackageRecoveryServices(t *testing.T, fake *kurtosis.FakeClient, enclave lifecycle.EnclaveRef) {
	t.Helper()
	services, err := fake.Services(t.Context(), enclave)
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		if err := fake.RemoveService(t.Context(), enclave, service.UUID); err != nil {
			t.Fatal(err)
		}
	}
}

func packageRecoveryServices(spec topology.Spec) []kurtosis.Service {
	services := make([]kurtosis.Service, 0, 10)
	add := func(name string, ports map[string]uint16, source bool) {
		index := len(services) + 1
		service := kurtosis.Service{
			Name: name, UUID: fmt.Sprintf("%032x", index), Status: kurtosis.ServiceStatusRunning,
			PrivateIP: fmt.Sprintf("10.0.0.%d", index), PublicIP: "127.0.0.1",
			PrivatePorts: map[string]kurtosis.Port{}, PublicPorts: map[string]kurtosis.Port{}, Labels: map[string]string{},
		}
		portIndex := 0
		for id, number := range ports {
			service.PrivatePorts[id] = kurtosis.Port{ID: id, Number: number, TransportProtocol: "TCP"}
			service.PublicPorts[id] = kurtosis.Port{ID: id, Number: uint16(18000 + index*10 + portIndex), TransportProtocol: "TCP"}
			portIndex++
		}
		if source {
			service.Labels[spec.SourceRevisionLabel] = spec.SourceRevision
		}
		services = append(services, service)
	}
	for _, item := range spec.Execution {
		add(item.Name, map[string]uint16{item.RPCPortID: 8545, item.WSPortID: 8546}, true)
	}
	for _, item := range spec.Consensus {
		add(item.Name, map[string]uint16{item.HTTPPortID: 3500}, false)
	}
	for _, item := range spec.Validators {
		add(item.Name, map[string]uint16{item.MetricsPortID: 8080}, false)
	}
	add(spec.Signer.Name, map[string]uint16{spec.Signer.HTTPPortID: 8550}, false)
	for _, item := range spec.Helpers {
		add(item.Name, nil, false)
	}
	return services
}
