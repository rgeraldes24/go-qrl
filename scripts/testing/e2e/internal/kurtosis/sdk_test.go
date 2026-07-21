package kurtosis

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_rpc_api_bindings"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/binding_constructors"
	"google.golang.org/grpc"
)

type serviceInfoAPIFunc func(context.Context, *kurtosis_core_rpc_api_bindings.GetServicesArgs, ...grpc.CallOption) (*kurtosis_core_rpc_api_bindings.GetServicesResponse, error)

func (function serviceInfoAPIFunc) GetServices(ctx context.Context, args *kurtosis_core_rpc_api_bindings.GetServicesArgs, options ...grpc.CallOption) (*kurtosis_core_rpc_api_bindings.GetServicesResponse, error) {
	return function(ctx, args, options...)
}

func TestContextBoundHonorsCancellationOfContextlessOperation(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()

	result, err := contextBound(ctx, func() (string, error) {
		close(started)
		<-release
		return "late result", nil
	})
	if !errors.Is(err, context.Canceled) || result != "" {
		t.Fatalf("contextBound result = %q, error = %v", result, err)
	}
}

func TestContextBoundReturnsCompletedResult(t *testing.T) {
	wantErr := errors.New("operation failed")
	result, err := contextBound(context.Background(), func() (string, error) {
		return "result", wantErr
	})
	if result != "result" || !errors.Is(err, wantErr) {
		t.Fatalf("contextBound result = %q, error = %v", result, err)
	}
}

func TestFetchServiceInfosPassesIdentifiersAndContext(t *testing.T) {
	started := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	api := serviceInfoAPIFunc(func(callContext context.Context, args *kurtosis_core_rpc_api_bindings.GetServicesArgs, _ ...grpc.CallOption) (*kurtosis_core_rpc_api_bindings.GetServicesResponse, error) {
		if !args.GetServiceIdentifiers()["immutable-service-uuid"] {
			t.Errorf("GetServices identifiers = %v", args.GetServiceIdentifiers())
		}
		close(started)
		<-callContext.Done()
		return nil, callContext.Err()
	})
	go func() {
		<-started
		cancel()
	}()

	infos, err := fetchServiceInfos(ctx, api, map[string]bool{"immutable-service-uuid": true})
	if !errors.Is(err, context.Canceled) || infos != nil {
		t.Fatalf("fetchServiceInfos = %v, %v", infos, err)
	}
}

func TestConvertServiceInfoPreservesExplicitStatus(t *testing.T) {
	for _, test := range []struct {
		name       string
		apiStatus  kurtosis_core_rpc_api_bindings.ServiceStatus
		wantStatus ServiceStatus
		publicIP   string
	}{
		{name: "running without published endpoint", apiStatus: kurtosis_core_rpc_api_bindings.ServiceStatus_RUNNING, wantStatus: ServiceStatusRunning},
		{name: "stopped with stale endpoint metadata", apiStatus: kurtosis_core_rpc_api_bindings.ServiceStatus_STOPPED, wantStatus: ServiceStatusStopped, publicIP: "127.0.0.1"},
		{name: "unknown", apiStatus: kurtosis_core_rpc_api_bindings.ServiceStatus_UNKNOWN, wantStatus: ServiceStatusUnknown},
	} {
		t.Run(test.name, func(t *testing.T) {
			info := &kurtosis_core_rpc_api_bindings.ServiceInfo{
				Name: "service", ServiceUuid: "11111111111111111111111111111111", ServiceStatus: test.apiStatus,
				PrivateIpAddr: "10.0.0.1", MaybePublicIpAddr: test.publicIP,
				PrivatePorts: map[string]*kurtosis_core_rpc_api_bindings.Port{
					"rpc": {Number: 8545, TransportProtocol: kurtosis_core_rpc_api_bindings.Port_TCP},
				},
				MaybePublicPorts: map[string]*kurtosis_core_rpc_api_bindings.Port{
					"rpc": {Number: 18545, TransportProtocol: kurtosis_core_rpc_api_bindings.Port_TCP},
				},
			}
			service, err := convertServiceInfo(info)
			if err != nil {
				t.Fatal(err)
			}
			if service.Status != test.wantStatus || service.PublicIP != test.publicIP || service.PrivatePorts["rpc"].Number != 8545 {
				t.Fatalf("converted service = %+v, want status %q public IP %q", service, test.wantStatus, test.publicIP)
			}
		})
	}
}

func TestConvertServiceInfoRejectsUnsupportedStatus(t *testing.T) {
	_, err := convertServiceInfo(&kurtosis_core_rpc_api_bindings.ServiceInfo{
		Name: "service", ServiceUuid: "11111111111111111111111111111111",
		ServiceStatus: kurtosis_core_rpc_api_bindings.ServiceStatus(99),
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported service status 99") {
		t.Fatalf("convertServiceInfo error = %v", err)
	}
}

func TestConsumeStarlarkStreamReturnsSerializedOutput(t *testing.T) {
	stream := make(chan *kurtosis_core_rpc_api_bindings.StarlarkRunResponseLine, 1)
	stream <- binding_constructors.NewStarlarkRunResponseLineFromRunSuccessEvent(`{"rpc":"http://127.0.0.1"}`, time.Second)
	close(stream)

	result, err := consumeStarlarkStream(stream)
	if err != nil {
		t.Fatal(err)
	}
	if result.SerializedOutput != `{"rpc":"http://127.0.0.1"}` {
		t.Fatalf("serialized output = %q", result.SerializedOutput)
	}
}

func TestConsumeStarlarkStreamRejectsFailedAndIncompleteRuns(t *testing.T) {
	t.Run("failed terminal event", func(t *testing.T) {
		stream := make(chan *kurtosis_core_rpc_api_bindings.StarlarkRunResponseLine, 1)
		stream <- binding_constructors.NewStarlarkRunResponseLineFromRunFailureEvent()
		close(stream)
		if _, err := consumeStarlarkStream(stream); err == nil || !strings.Contains(err.Error(), "Starlark run failed") {
			t.Fatalf("consume error = %v", err)
		}
	})

	t.Run("stream without terminal event", func(t *testing.T) {
		stream := make(chan *kurtosis_core_rpc_api_bindings.StarlarkRunResponseLine, 1)
		stream <- binding_constructors.NewStarlarkRunResponseLineFromSinglelineProgressInfo("creating service", 1, 2)
		close(stream)
		if _, err := consumeStarlarkStream(stream); err == nil || !strings.Contains(err.Error(), "without a terminal event") {
			t.Fatalf("consume error = %v", err)
		}
	})
}
