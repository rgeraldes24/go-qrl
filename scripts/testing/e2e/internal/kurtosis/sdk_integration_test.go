// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package kurtosis

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/starlark_run_config"
)

// TestSDKClientRealEngineSmoke is opt-in because it creates and destroys one
// isolated enclave in the developer's shared Kurtosis engine.
func TestSDKClientRealEngineSmoke(t *testing.T) {
	if os.Getenv("E2E_KURTOSIS_INTEGRATION") != "1" {
		t.Skip("set E2E_KURTOSIS_INTEGRATION=1 to run the real-engine SDK smoke test")
	}
	client, err := NewSDKClient()
	if err != nil {
		t.Fatal(err)
	}
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	created, err := client.CreateEnclave(ctx, "e2e-sdk-smoke-"+hex.EncodeToString(random))
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
			t.Errorf("clean up SDK smoke enclave: %v", err)
		}
	})
	discovered, err := client.GetEnclave(ctx, created.UUID)
	if err != nil || discovered.Name != created.Name || discovered.UUID != created.UUID {
		t.Fatalf("discovered=%+v created=%+v err=%v", discovered, created, err)
	}
	services, err := client.Services(ctx, created)
	if err != nil || len(services) != 0 {
		t.Fatalf("new enclave services=%d err=%v", len(services), err)
	}
	params, _ := json.Marshal(map[string]string{"sentinel": "package-invocation-identity"})
	packageRoot, err := filepath.Abs(filepath.Join("testdata", "package"))
	if err != nil {
		t.Fatal(err)
	}
	enclave, err := client.enclaveContext(ctx, created)
	if err != nil {
		t.Fatal(err)
	}
	stream, cancelRun, err := enclave.RunStarlarkPackage(ctx, packageRoot, starlark_run_config.NewRunStarlarkConfig(starlark_run_config.WithSerializedParams(string(params))))
	if err != nil {
		t.Fatal(err)
	}
	err = consumeStarlarkCompletion(stream)
	cancelRun()
	if err != nil {
		t.Fatal(err)
	}
	invocation, err := client.LastPackageInvocation(ctx, created)
	const packageID = "github.com/theqrl/go-qrl-e2e-sdk-smoke"
	if err != nil || invocation.SerializedParams != string(params) || invocation.ID != packageID {
		t.Fatalf("invocation identity: id=%q params=%q err=%v; want id=%q params=%q", invocation.ID, invocation.SerializedParams, err, packageID, params)
	}
	if invocation.ID == packageRoot {
		t.Fatalf("retained package ID %q was inferred from local package path", invocation.ID)
	}
	if err := client.DestroyEnclave(ctx, created); err != nil {
		t.Fatal(err)
	}
	destroyed = true
}
