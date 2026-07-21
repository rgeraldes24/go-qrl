package kurtosis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/starlark_run_config"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

const realEngineSmokeEnvironment = "VM64_E2E_REAL_ENGINE_SMOKE"

const addRealEngineSmokeServiceScript = `def run(plan, args):
    plan.add_service(
        name=args["service_name"],
        config=ServiceConfig(
            image="alpine:3.20",
            entrypoint=["/bin/sh", "-c", "while true; do sleep 3600; done"],
        ),
    )
`

// TestSDKClientRealEngineSmoke proves the SDK boundary against the pinned local
// engine before image preparation starts. It is opt-in so the ordinary unit
// suite remains hermetic and does not mutate a developer's Kurtosis engine.
func TestSDKClientRealEngineSmoke(t *testing.T) {
	if os.Getenv(realEngineSmokeEnvironment) != "1" {
		t.Skip("set " + realEngineSmokeEnvironment + "=1 to run the real-engine SDK smoke test")
	}

	client, err := NewSDKClient()
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	name := "vm64-sdk-smoke-" + hex.EncodeToString(random)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	created, err := client.CreateEnclave(ctx, name)
	if err != nil {
		t.Fatal(err)
	}
	destroyed := false
	t.Cleanup(func() {
		if destroyed {
			return
		}
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		if err := client.DestroyEnclave(cleanupCtx, created); err != nil {
			t.Errorf("clean up SDK smoke enclave %s: %v", created.UUID, err)
		}
	})

	discovered, err := client.GetEnclave(ctx, created.UUID)
	if err != nil {
		t.Fatal(err)
	}
	if discovered.Name != created.Name || discovered.UUID != created.UUID {
		t.Fatalf("discovered enclave = %+v, created = %+v", discovered, created)
	}
	if exists, err := client.EnclaveExists(ctx, created.UUID); err != nil || !exists {
		t.Fatalf("created enclave existence = %t, %v", exists, err)
	}
	services, err := client.Services(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	if len(services) != 0 {
		t.Fatalf("new smoke enclave unexpectedly contains %d services", len(services))
	}
	if err := verifyRealEnginePackageInvocation(ctx, client, created); err != nil {
		t.Fatal(err)
	}

	const serviceName = "status-smoke"
	if err := client.mutateCanonicalService(ctx, created, serviceName, addRealEngineSmokeServiceScript); err != nil {
		t.Fatal(err)
	}
	service, err := waitForRealEngineServiceStatus(ctx, client, created, serviceName, ServiceStatusRunning)
	if err != nil {
		t.Fatal(err)
	}
	if service.Name != serviceName || service.UUID == "" {
		t.Fatalf("created smoke service has invalid identity: %+v", service)
	}
	serviceUUID := service.UUID
	if err := client.StopService(ctx, created, serviceUUID); err != nil {
		t.Fatal(err)
	}
	stopped, err := waitForRealEngineServiceStatus(ctx, client, created, serviceUUID, ServiceStatusStopped)
	if err != nil {
		t.Fatal(err)
	}
	if stopped.Name != serviceName || stopped.UUID != serviceUUID {
		t.Fatalf("stopped service identity changed: %+v", stopped)
	}
	if err := client.StartService(ctx, created, serviceUUID); err != nil {
		t.Fatal(err)
	}
	restarted, err := waitForRealEngineServiceStatus(ctx, client, created, serviceUUID, ServiceStatusRunning)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Name != serviceName || restarted.UUID != serviceUUID {
		t.Fatalf("restarted service identity changed: %+v", restarted)
	}
	if err := client.DestroyEnclave(ctx, created); err != nil {
		t.Fatal(err)
	}
	if err := waitForRealEngineEnclaveAbsence(ctx, client, created.UUID); err != nil {
		t.Fatal(err)
	}
	destroyed = true
}

func verifyRealEnginePackageInvocation(ctx context.Context, client *SDKClient, enclave lifecycle.EnclaveRef) error {
	const (
		packageID = "github.com/theqrl/go-qrl-vm64-sdk-smoke"
		sentinel  = "full-width-package-recovery"
	)
	packageRoot, err := filepath.Abs(filepath.Join("testdata", "package"))
	if err != nil {
		return fmt.Errorf("resolve SDK smoke package: %w", err)
	}
	params, err := json.Marshal(map[string]string{"sentinel": sentinel})
	if err != nil {
		return err
	}
	enclaveContext, err := client.enclaveContext(ctx, enclave)
	if err != nil {
		return err
	}
	configuration := starlark_run_config.NewRunStarlarkConfig(
		starlark_run_config.WithSerializedParams(string(params)),
	)
	stream, cancel, err := enclaveContext.RunStarlarkPackage(ctx, packageRoot, configuration)
	if err != nil {
		return fmt.Errorf("run local SDK smoke package: %w", err)
	}
	defer cancel()
	result, err := consumeStarlarkStream(stream)
	if err != nil {
		return err
	}
	var output map[string]string
	if err := json.Unmarshal([]byte(result.SerializedOutput), &output); err != nil {
		return fmt.Errorf("decode SDK smoke package output %q: %w", result.SerializedOutput, err)
	}
	if output["sentinel"] != sentinel {
		return fmt.Errorf("SDK smoke package output %q does not contain the expected sentinel", result.SerializedOutput)
	}

	invocation, err := client.LastPackageInvocation(ctx, enclave)
	if err != nil {
		return fmt.Errorf("read durable SDK package invocation: %w", err)
	}
	if invocation.Locator != packageID {
		return fmt.Errorf("durable SDK package locator %q, want %q", invocation.Locator, packageID)
	}
	var recorded map[string]string
	if err := json.Unmarshal([]byte(invocation.SerializedParams), &recorded); err != nil {
		return fmt.Errorf("decode durable SDK package parameters %q: %w", invocation.SerializedParams, err)
	}
	if recorded["sentinel"] != sentinel {
		return fmt.Errorf("durable SDK package parameters %q do not contain the expected sentinel", invocation.SerializedParams)
	}
	return nil
}

func waitForRealEngineEnclaveAbsence(ctx context.Context, client *SDKClient, uuid string) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		exists, err := client.EnclaveExists(ctx, uuid)
		if err == nil && !exists {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for enclave %s absence (last existence error %v): %w", uuid, err, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForRealEngineServiceStatus(ctx context.Context, client *SDKClient, enclave lifecycle.EnclaveRef, identifier string, want ServiceStatus) (Service, error) {
	var last Service
	var lastErr error
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		service, err := client.Service(ctx, enclave, identifier)
		if err == nil {
			last = service
			if service.Status == want {
				return service, nil
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return Service{}, fmt.Errorf("wait for service %q status %q (last status %q, last error %v): %w", identifier, want, last.Status, lastErr, ctx.Err())
		case <-ticker.C:
		}
	}
}
