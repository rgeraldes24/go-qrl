package kurtosis

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestFakeRetainsAcceptedPackageInvocationAcrossResponseFailure(t *testing.T) {
	fake := NewFakeClient()
	ref, err := fake.CreateEnclave(t.Context(), "package-response-loss")
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("response stream lost")
	fake.PackageError = injected
	run := PackageRun{Locator: "github.com/theqrl/qrl-package@" + strings.Repeat("a", 40), SerializedParams: "participants: []\n"}
	if _, err := fake.RunRemotePackage(t.Context(), ref, run); !errors.Is(err, injected) {
		t.Fatalf("RunRemotePackage error = %v", err)
	}
	last, err := fake.LastPackageInvocation(t.Context(), ref)
	if err != nil {
		t.Fatal(err)
	}
	if last.Locator != run.Locator || last.SerializedParams != run.SerializedParams {
		t.Fatalf("last package invocation = %+v, want %+v", last, run)
	}
}

func TestFakeDistinguishesNoPackageInvocation(t *testing.T) {
	fake := NewFakeClient()
	ref, err := fake.CreateEnclave(t.Context(), "package-not-run")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.LastPackageInvocation(t.Context(), ref); !errors.Is(err, ErrPackageInvocationNotFound) {
		t.Fatalf("LastPackageInvocation error = %v, want ErrPackageInvocationNotFound", err)
	}
}

func TestFakeEnclaveCollisionAndUUIDSafety(t *testing.T) {
	fake := NewFakeClient()
	ref, err := fake.CreateEnclave(context.Background(), "vm64")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fake.CreateEnclave(context.Background(), "vm64"); err == nil {
		t.Fatal("duplicate enclave name was accepted")
	}
	if exists, err := fake.EnclaveExists(context.Background(), ref.UUID); err != nil || !exists {
		t.Fatalf("created enclave existence = %t, %v", exists, err)
	}
	wrong := ref
	wrong.UUID = "ffffffffffffffffffffffffffffffff"
	if err := fake.DestroyEnclave(context.Background(), wrong); err == nil {
		t.Fatal("UUID mismatch was accepted")
	}
	borrowed := ref
	borrowed.Owned = false
	if err := fake.DestroyEnclave(context.Background(), borrowed); err == nil {
		t.Fatal("borrowed enclave cleanup was accepted")
	}
	if err := fake.DestroyEnclave(context.Background(), ref); err != nil {
		t.Fatal(err)
	}
	if exists, err := fake.EnclaveExists(context.Background(), ref.UUID); err != nil || exists {
		t.Fatalf("destroyed enclave existence = %t, %v", exists, err)
	}
}

func TestFakeRefreshesEndpointAfterRestart(t *testing.T) {
	fake := NewFakeClient()
	ref, err := fake.CreateEnclave(context.Background(), "vm64")
	if err != nil {
		t.Fatal(err)
	}
	service := Service{
		Name: "el-1", UUID: "11111111111111111111111111111111", PublicIP: "127.0.0.1",
		PublicPorts: map[string]Port{"rpc": {ID: "rpc", Number: 8545}},
	}
	if err := fake.AddService(ref, service); err != nil {
		t.Fatal(err)
	}
	if err := fake.StopService(context.Background(), ref, service.Name); err != nil {
		t.Fatal(err)
	}
	stopped, err := fake.Service(context.Background(), ref, service.Name)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Status != ServiceStatusStopped || stopped.PublicIP == "" || len(stopped.PublicPorts) == 0 {
		t.Fatalf("stopped fake service = %+v; status must be independent of endpoint metadata", stopped)
	}
	if err := fake.StartService(context.Background(), ref, service.Name); err != nil {
		t.Fatal(err)
	}
	refreshed, err := fake.Service(context.Background(), ref, service.Name)
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.PublicPorts["rpc"].Number == service.PublicPorts["rpc"].Number {
		t.Fatal("restart did not change the fake public endpoint")
	}
	if refreshed.Status != ServiceStatusRunning {
		t.Fatalf("restarted status = %q", refreshed.Status)
	}
}

func TestFakeModelsUnknownServiceStatus(t *testing.T) {
	fake := NewFakeClient()
	ref, err := fake.CreateEnclave(context.Background(), "vm64")
	if err != nil {
		t.Fatal(err)
	}
	service := Service{Name: "el-1", UUID: "11111111111111111111111111111111"}
	if err := fake.AddService(ref, service); err != nil {
		t.Fatal(err)
	}
	if err := fake.SetServiceStatus(ref, service.UUID, ServiceStatusUnknown); err != nil {
		t.Fatal(err)
	}
	unknown, err := fake.Service(context.Background(), ref, service.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if unknown.Status != ServiceStatusUnknown {
		t.Fatalf("status = %q", unknown.Status)
	}
	if err := fake.StartService(context.Background(), ref, service.UUID); err == nil {
		t.Fatal("unknown service status was treated as stopped")
	}
	if err := fake.StopService(context.Background(), ref, service.UUID); err == nil {
		t.Fatal("unknown service status was treated as running")
	}
}

func TestPublicEndpointBracketsIPv6(t *testing.T) {
	service := Service{
		PublicIP:    "2001:db8::1",
		PublicPorts: map[string]Port{"rpc": {ID: "rpc", Number: 8545}},
	}
	endpoint, ok := service.PublicEndpoint("rpc", "http")
	if !ok || endpoint != "http://[2001:db8::1]:8545" {
		t.Fatalf("endpoint = %q, %t", endpoint, ok)
	}
}
