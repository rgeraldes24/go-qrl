// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package kurtosis

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_rpc_api_bindings"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/binding_constructors"
)

func TestConvertServiceInfoPreservesLifecycleStatus(t *testing.T) {
	info := &kurtosis_core_rpc_api_bindings.ServiceInfo{
		Name: "execution", ServiceUuid: "11111111111111111111111111111111",
		ServiceStatus:     kurtosis_core_rpc_api_bindings.ServiceStatus_STOPPED,
		MaybePublicIpAddr: "127.0.0.1",
		MaybePublicPorts:  map[string]*kurtosis_core_rpc_api_bindings.Port{"rpc": {Number: 18545, TransportProtocol: kurtosis_core_rpc_api_bindings.Port_TCP}},
	}
	service, err := convertServiceInfo(info)
	if err != nil {
		t.Fatal(err)
	}
	if service.Status != ServiceStatusStopped || service.PublicPorts["rpc"].Number != 18545 {
		t.Fatalf("service = %+v", service)
	}
}

func TestPackageInvocationFromStarlarkRun(t *testing.T) {
	emptyParams := ""
	initialParams := `{"source":"initial"}`
	pristineWithUnknown := &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
		PackageId:               pristineScriptPackageID,
		Parallelism:             pristineScriptDefaultParallelism,
		InitialSerializedParams: &emptyParams,
	}
	pristineWithUnknown.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 0x01})
	tests := []struct {
		name    string
		run     *kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse
		want    PackageInvocation
		wantErr error
		errText string
	}{
		{
			name: "pristine enclave has no prior invocation",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId:               pristineScriptPackageID,
				Parallelism:             pristineScriptDefaultParallelism,
				RestartPolicy:           kurtosis_core_rpc_api_bindings.RestartPolicy_NEVER,
				InitialSerializedParams: &emptyParams,
			},
			wantErr: ErrPackageInvocationNotFound,
		},
		{
			name: "initial parameters are authoritative",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId:               "github.com/theqrl/qrl-package",
				SerializedParams:        `{"source":"latest"}`,
				InitialSerializedParams: &initialParams,
			},
			want: PackageInvocation{
				ID:               "github.com/theqrl/qrl-package",
				SerializedParams: initialParams,
			},
		},
		{
			name: "serialized parameters support older engines",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId:        "github.com/theqrl/qrl-package",
				SerializedParams: `{"source":"serialized"}`,
			},
			want: PackageInvocation{
				ID:               "github.com/theqrl/qrl-package",
				SerializedParams: `{"source":"serialized"}`,
			},
		},
		{
			name:    "nil response fails closed",
			errText: "no package invocation metadata",
		},
		{
			name:    "zero-value response is incomplete",
			run:     &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{},
			errText: "complete package invocation",
		},
		{
			name: "real package without parameters is incomplete",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId: "github.com/theqrl/qrl-package",
			},
			errText: "complete package invocation",
		},
		{
			name: "default record without explicit initial parameters is incomplete",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId:   pristineScriptPackageID,
				Parallelism: pristineScriptDefaultParallelism,
			},
			errText: "complete package invocation",
		},
		{
			name: "default identifier with a script is incomplete",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId:               pristineScriptPackageID,
				SerializedScript:        "main()",
				Parallelism:             pristineScriptDefaultParallelism,
				InitialSerializedParams: &emptyParams,
			},
			errText: "complete package invocation",
		},
		{
			name: "default identifier with a relative main is incomplete",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId:               pristineScriptPackageID,
				Parallelism:             pristineScriptDefaultParallelism,
				RelativePathToMainFile:  "main.star",
				InitialSerializedParams: &emptyParams,
			},
			errText: "complete package invocation",
		},
		{
			name: "default identifier with a main function is incomplete",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId:               pristineScriptPackageID,
				Parallelism:             pristineScriptDefaultParallelism,
				MainFunctionName:        "run",
				InitialSerializedParams: &emptyParams,
			},
			errText: "complete package invocation",
		},
		{
			name: "default identifier with an experimental feature is incomplete",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId:               pristineScriptPackageID,
				Parallelism:             pristineScriptDefaultParallelism,
				ExperimentalFeatures:    []kurtosis_core_rpc_api_bindings.KurtosisFeatureFlag{kurtosis_core_rpc_api_bindings.KurtosisFeatureFlag_NO_INSTRUCTIONS_CACHING},
				InitialSerializedParams: &emptyParams,
			},
			errText: "complete package invocation",
		},
		{
			name: "default identifier with a restart policy is incomplete",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId:               pristineScriptPackageID,
				Parallelism:             pristineScriptDefaultParallelism,
				RestartPolicy:           kurtosis_core_rpc_api_bindings.RestartPolicy_ALWAYS,
				InitialSerializedParams: &emptyParams,
			},
			errText: "complete package invocation",
		},
		{
			name:    "default identifier with unknown metadata is incomplete",
			run:     pristineWithUnknown,
			errText: "complete package invocation",
		},
		{
			name: "parameters without an identifier are incomplete",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				SerializedParams: `{"source":"serialized"}`,
			},
			errText: "complete package invocation",
		},
		{
			name: "unexpected pristine defaults fail closed",
			run: &kurtosis_core_rpc_api_bindings.GetStarlarkRunResponse{
				PackageId:               pristineScriptPackageID,
				Parallelism:             pristineScriptDefaultParallelism + 1,
				InitialSerializedParams: &emptyParams,
			},
			errText: "complete package invocation",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := packageInvocationFromStarlarkRun(test.run)
			if test.wantErr != nil {
				if !errors.Is(err, test.wantErr) {
					t.Fatalf("error = %v, want %v", err, test.wantErr)
				}
				return
			}
			if test.errText != "" {
				if err == nil || !strings.Contains(err.Error(), test.errText) {
					t.Fatalf("error = %v, want text %q", err, test.errText)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("invocation = %+v, want %+v", got, test.want)
			}
		})
	}
}

func TestConsumeStarlarkCompletionSuppressesSecretBearingTranscript(t *testing.T) {
	const secret = "seed-that-must-never-reach-errors"
	stream := make(chan *kurtosis_core_rpc_api_bindings.StarlarkRunResponseLine, 2)
	stream <- binding_constructors.NewStarlarkRunResponseLineFromSinglelineProgressInfo(secret, 1, 2)
	stream <- binding_constructors.NewStarlarkRunResponseLineFromRunFailureEvent()
	close(stream)
	err := consumeStarlarkCompletion(stream)
	if err == nil || strings.Contains(err.Error(), secret) {
		t.Fatalf("secret-bearing error = %v", err)
	}

	incomplete := make(chan *kurtosis_core_rpc_api_bindings.StarlarkRunResponseLine, 1)
	incomplete <- binding_constructors.NewStarlarkRunResponseLineFromSinglelineProgressInfo(secret, 1, 2)
	close(incomplete)
	err = consumeStarlarkCompletion(incomplete)
	if err == nil || strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "without a terminal event") {
		t.Fatalf("incomplete error = %v", err)
	}

	success := make(chan *kurtosis_core_rpc_api_bindings.StarlarkRunResponseLine, 1)
	success <- binding_constructors.NewStarlarkRunResponseLineFromRunSuccessEvent(secret, time.Second)
	close(success)
	if err := consumeStarlarkCompletion(success); err != nil {
		t.Fatal(err)
	}
}
