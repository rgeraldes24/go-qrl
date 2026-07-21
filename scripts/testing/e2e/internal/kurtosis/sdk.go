package kurtosis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/kurtosis-tech/kurtosis/api/golang/core/kurtosis_core_rpc_api_bindings"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/binding_constructors"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/enclaves"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/services"
	"github.com/kurtosis-tech/kurtosis/api/golang/core/lib/starlark_run_config"
	"github.com/kurtosis-tech/kurtosis/api/golang/engine/lib/kurtosis_context"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

const (
	startServiceScript = `def run(plan, args):
    plan.start_service(name=args["service_name"])
`
	stopServiceScript = `def run(plan, args):
    plan.stop_service(name=args["service_name"])
`
	removeServiceScript = `def run(plan, args):
    plan.remove_service(name=args["service_name"])
`
	maxAPIContainerResponseBytes = 100 * 1024 * 1024
)

type serviceInfoAPI interface {
	GetServices(context.Context, *kurtosis_core_rpc_api_bindings.GetServicesArgs, ...grpc.CallOption) (*kurtosis_core_rpc_api_bindings.GetServicesResponse, error)
}

type SDKClient struct {
	context *kurtosis_context.KurtosisContext
}

func NewSDKClient() (*SDKClient, error) {
	ctx, err := kurtosis_context.NewKurtosisContextFromLocalEngine()
	if err != nil {
		return nil, err
	}
	return &SDKClient{context: ctx}, nil
}

func (client *SDKClient) CreateEnclave(ctx context.Context, name string) (lifecycle.EnclaveRef, error) {
	enclave, err := client.context.CreateEnclave(ctx, name)
	if err != nil {
		return lifecycle.EnclaveRef{}, err
	}
	ref := lifecycle.EnclaveRef{Name: enclave.GetEnclaveName(), UUID: string(enclave.GetEnclaveUuid()), Owned: true}
	if err := ref.Validate(); err != nil {
		return lifecycle.EnclaveRef{}, fmt.Errorf("Kurtosis returned invalid enclave identity: %w", err)
	}
	return ref, nil
}

func (client *SDKClient) GetEnclave(ctx context.Context, identifier string) (lifecycle.EnclaveRef, error) {
	info, err := client.context.GetEnclave(ctx, identifier)
	if err != nil {
		return lifecycle.EnclaveRef{}, err
	}
	ref := lifecycle.EnclaveRef{Name: info.GetName(), UUID: info.GetEnclaveUuid()}
	if err := ref.Validate(); err != nil {
		return lifecycle.EnclaveRef{}, fmt.Errorf("Kurtosis returned invalid enclave identity: %w", err)
	}
	return ref, nil
}

func (client *SDKClient) EnclaveExists(ctx context.Context, uuid string) (bool, error) {
	if err := (&lifecycle.EnclaveRef{Name: "identity-check", UUID: uuid}).Validate(); err != nil {
		return false, err
	}
	enclaves, err := client.context.GetEnclaves(ctx)
	if err != nil {
		return false, err
	}
	_, exists := enclaves.GetEnclavesByUuid()[uuid]
	return exists, nil
}

func (client *SDKClient) RunRemotePackage(ctx context.Context, ref lifecycle.EnclaveRef, run PackageRun) (PackageResult, error) {
	enclave, err := client.enclaveContext(ctx, ref)
	if err != nil {
		return PackageResult{}, err
	}
	configuration := starlark_run_config.NewRunStarlarkConfig(
		starlark_run_config.WithSerializedParams(run.SerializedParams),
	)
	stream, cancel, err := enclave.RunStarlarkRemotePackage(ctx, run.Locator, configuration)
	if err != nil {
		return PackageResult{}, err
	}
	defer cancel()
	return consumeStarlarkStream(stream)
}

func (client *SDKClient) LastPackageInvocation(ctx context.Context, ref lifecycle.EnclaveRef) (PackageInvocation, error) {
	enclave, err := client.enclaveContext(ctx, ref)
	if err != nil {
		return PackageInvocation{}, err
	}
	last, err := enclave.GetStarlarkRun(ctx)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return PackageInvocation{}, ErrPackageInvocationNotFound
		}
		return PackageInvocation{}, err
	}
	params := last.GetInitialSerializedParams()
	if params == "" {
		params = last.GetSerializedParams()
	}
	invocation := PackageInvocation{Locator: last.GetPackageId(), SerializedParams: params}
	if invocation.Locator == "" && invocation.SerializedParams == "" {
		return PackageInvocation{}, ErrPackageInvocationNotFound
	}
	if invocation.Locator == "" || invocation.SerializedParams == "" {
		return PackageInvocation{}, errors.New("Kurtosis did not retain a complete package invocation")
	}
	return invocation, nil
}

func (client *SDKClient) Services(ctx context.Context, ref lifecycle.EnclaveRef) ([]Service, error) {
	infos, err := client.serviceInfos(ctx, ref, map[string]bool{})
	if err != nil {
		return nil, err
	}
	result := make([]Service, 0, len(infos))
	for _, info := range infos {
		service, err := convertServiceInfo(info)
		if err != nil {
			return nil, err
		}
		result = append(result, service)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (client *SDKClient) Service(ctx context.Context, ref lifecycle.EnclaveRef, identifier string) (Service, error) {
	infos, err := client.serviceInfos(ctx, ref, map[string]bool{identifier: true})
	if err != nil {
		return Service{}, err
	}
	info, found := infos[identifier]
	if !found {
		for _, candidate := range infos {
			if candidate != nil && (candidate.GetName() == identifier || candidate.GetServiceUuid() == identifier || candidate.GetShortenedUuid() == identifier) {
				info = candidate
				found = true
				break
			}
		}
	}
	if !found {
		return Service{}, fmt.Errorf("Kurtosis did not return service %q", identifier)
	}
	return convertServiceInfo(info)
}

// serviceInfos deliberately calls the API container's GetServices RPC rather
// than EnclaveContext.GetServiceContext(s). The higher-level helpers discard
// ServiceInfo.ServiceStatus, which made stopped services indistinguishable
// from services that merely lacked a published endpoint.
func (client *SDKClient) serviceInfos(ctx context.Context, ref lifecycle.EnclaveRef, identifiers map[string]bool) (map[string]*kurtosis_core_rpc_api_bindings.ServiceInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// This preserves the SDK's remote-context port forwarding and the existing
	// immutable enclave name/UUID validation before dialing the API container.
	if _, err := client.enclaveContext(ctx, ref); err != nil {
		return nil, err
	}
	info, err := client.context.GetEnclave(ctx, ref.UUID)
	if err != nil {
		return nil, err
	}
	if info.GetEnclaveUuid() != ref.UUID || info.GetName() != ref.Name {
		return nil, fmt.Errorf("enclave identity changed: got %s/%s, want %s/%s", info.GetName(), info.GetEnclaveUuid(), ref.Name, ref.UUID)
	}
	host := info.GetApiContainerHostMachineInfo()
	if host == nil || host.GetIpOnHostMachine() == "" || host.GetGrpcPortOnHostMachine() == 0 {
		return nil, fmt.Errorf("enclave %s/%s has no API-container host endpoint", ref.Name, ref.UUID)
	}
	endpoint := net.JoinHostPort(host.GetIpOnHostMachine(), strconv.FormatUint(uint64(host.GetGrpcPortOnHostMachine()), 10))
	connection, err := grpc.NewClient(
		endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxAPIContainerResponseBytes)),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to Kurtosis API container %s: %w", endpoint, err)
	}
	defer connection.Close()
	return fetchServiceInfos(ctx, kurtosis_core_rpc_api_bindings.NewApiContainerServiceClient(connection), identifiers)
}

func fetchServiceInfos(ctx context.Context, api serviceInfoAPI, identifiers map[string]bool) (map[string]*kurtosis_core_rpc_api_bindings.ServiceInfo, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	response, err := api.GetServices(ctx, binding_constructors.NewGetServicesArgs(identifiers))
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if response == nil {
		return nil, errors.New("Kurtosis GetServices returned a nil response")
	}
	return response.GetServiceInfo(), nil
}

func (client *SDKClient) StartService(ctx context.Context, ref lifecycle.EnclaveRef, identifier string) error {
	return client.mutateService(ctx, ref, identifier, startServiceScript)
}

func (client *SDKClient) StopService(ctx context.Context, ref lifecycle.EnclaveRef, identifier string) error {
	return client.mutateService(ctx, ref, identifier, stopServiceScript)
}

func (client *SDKClient) RemoveService(ctx context.Context, ref lifecycle.EnclaveRef, identifier string) error {
	service, err := client.Service(ctx, ref, identifier)
	if err != nil {
		return err
	}
	if err := client.mutateCanonicalService(ctx, ref, service.Name, removeServiceScript); err != nil {
		return err
	}
	services, err := client.Services(ctx, ref)
	if err != nil {
		return err
	}
	for _, candidate := range services {
		if candidate.Name == service.Name || candidate.UUID == service.UUID {
			return fmt.Errorf("service %s still exists after removal", service.Name)
		}
	}
	return nil
}

func (client *SDKClient) ExecCommand(ctx context.Context, ref lifecycle.EnclaveRef, identifier string, command []string) (int32, string, error) {
	if err := ctx.Err(); err != nil {
		return -1, "", err
	}
	enclave, err := client.enclaveContext(ctx, ref)
	if err != nil {
		return -1, "", err
	}
	service, err := enclave.GetServiceContext(identifier)
	if err != nil {
		return -1, "", err
	}
	exitCode, output, err := service.ExecCommand(command)
	if err != nil {
		return exitCode, output, err
	}
	if err := ctx.Err(); err != nil {
		return exitCode, output, err
	}
	return exitCode, output, nil
}

func (client *SDKClient) ServiceLogs(ctx context.Context, ref lifecycle.EnclaveRef, identifiers []string) (map[string][]byte, error) {
	if len(identifiers) == 0 {
		return map[string][]byte{}, nil
	}
	requested := make(map[services.ServiceUUID]bool, len(identifiers))
	names := make(map[services.ServiceUUID]string, len(identifiers))
	for _, identifier := range identifiers {
		service, err := client.Service(ctx, ref, identifier)
		if err != nil {
			return nil, err
		}
		uuid := services.ServiceUUID(service.UUID)
		requested[uuid] = true
		names[uuid] = service.Name
	}
	stream, cancel, err := client.context.GetServiceLogs(ctx, ref.UUID, requested, false, true, 0, nil)
	if err != nil {
		return nil, err
	}
	defer cancel()
	result := make(map[string][]byte, len(requested))
	received := false
	for batch := range stream {
		received = true
		if missing := batch.GetNotFoundServiceUuids(); len(missing) > 0 {
			return nil, fmt.Errorf("Kurtosis did not find service UUIDs: %v", missing)
		}
		for uuid, lines := range batch.GetServiceLogsByServiceUuids() {
			var buffer bytes.Buffer
			for _, line := range lines {
				buffer.WriteString(line.GetContent())
				buffer.WriteByte('\n')
			}
			result[names[uuid]] = append(result[names[uuid]], buffer.Bytes()...)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !received {
		return nil, errors.New("Kurtosis service-log stream closed without a response")
	}
	return result, nil
}

func (client *SDKClient) DestroyEnclave(ctx context.Context, ref lifecycle.EnclaveRef) error {
	if !ref.Owned {
		return errors.New("refusing to destroy an enclave not marked as owned")
	}
	if err := ref.Validate(); err != nil {
		return err
	}
	return client.context.DestroyEnclave(ctx, ref.UUID)
}

func (client *SDKClient) enclaveContext(ctx context.Context, ref lifecycle.EnclaveRef) (*enclaves.EnclaveContext, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	current, err := client.context.GetEnclaveContext(ctx, ref.UUID)
	if err != nil {
		return nil, err
	}
	if string(current.GetEnclaveUuid()) != ref.UUID || current.GetEnclaveName() != ref.Name {
		return nil, fmt.Errorf("enclave identity changed: got %s/%s, want %s/%s", current.GetEnclaveName(), current.GetEnclaveUuid(), ref.Name, ref.UUID)
	}
	return current, nil
}

func (client *SDKClient) mutateService(ctx context.Context, ref lifecycle.EnclaveRef, identifier, script string) error {
	service, err := client.Service(ctx, ref, identifier)
	if err != nil {
		return err
	}
	return client.mutateCanonicalService(ctx, ref, service.Name, script)
}

func (client *SDKClient) mutateCanonicalService(ctx context.Context, ref lifecycle.EnclaveRef, name, script string) error {
	enclave, err := client.enclaveContext(ctx, ref)
	if err != nil {
		return err
	}
	params, err := json.Marshal(map[string]string{"service_name": name})
	if err != nil {
		return err
	}
	configuration := starlark_run_config.NewRunStarlarkConfig(starlark_run_config.WithSerializedParams(string(params)))
	stream, cancel, err := enclave.RunStarlarkScript(ctx, script, configuration)
	if err != nil {
		return err
	}
	defer cancel()
	_, err = consumeStarlarkStream(stream)
	return err
}

func consumeStarlarkStream(stream <-chan *kurtosis_core_rpc_api_bindings.StarlarkRunResponseLine) (PackageResult, error) {
	var transcript []string
	for line := range stream {
		transcript = append(transcript, line.String())
		if finished := line.GetRunFinishedEvent(); finished != nil {
			if !finished.GetIsRunSuccessful() {
				return PackageResult{}, fmt.Errorf("Starlark run failed: %s", strings.Join(transcript, "\n"))
			}
			return PackageResult{SerializedOutput: finished.GetSerializedOutput()}, nil
		}
	}
	return PackageResult{}, fmt.Errorf("Starlark response stream closed without a terminal event: %s", strings.Join(transcript, "\n"))
}

// contextBound adapts Kurtosis SDK helpers that internally issue RPCs with a
// background context. The buffered result channel lets the caller honor its
// cleanup deadline even if the upstream helper does not return until later.
func contextBound[T any](ctx context.Context, operation func() (T, error)) (T, error) {
	var zero T
	if err := ctx.Err(); err != nil {
		return zero, err
	}
	type outcome struct {
		value T
		err   error
	}
	completed := make(chan outcome, 1)
	go func() {
		value, err := operation()
		completed <- outcome{value: value, err: err}
	}()
	select {
	case <-ctx.Done():
		return zero, ctx.Err()
	case result := <-completed:
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		return result.value, result.err
	}
}

func convertServiceInfo(info *kurtosis_core_rpc_api_bindings.ServiceInfo) (Service, error) {
	if info == nil {
		return Service{}, errors.New("Kurtosis GetServices returned nil service info")
	}
	status, err := convertServiceStatus(info.GetServiceStatus())
	if err != nil {
		return Service{}, fmt.Errorf("service %q/%q: %w", info.GetName(), info.GetServiceUuid(), err)
	}
	return Service{
		Name:         info.GetName(),
		UUID:         info.GetServiceUuid(),
		Status:       status,
		PrivateIP:    info.GetPrivateIpAddr(),
		PublicIP:     info.GetMaybePublicIpAddr(),
		PrivatePorts: convertAPIPorts(info.GetPrivatePorts()),
		PublicPorts:  convertAPIPorts(info.GetMaybePublicPorts()),
		Labels:       info.GetLabels(),
	}, nil
}

func convertServiceStatus(status kurtosis_core_rpc_api_bindings.ServiceStatus) (ServiceStatus, error) {
	switch status {
	case kurtosis_core_rpc_api_bindings.ServiceStatus_RUNNING:
		return ServiceStatusRunning, nil
	case kurtosis_core_rpc_api_bindings.ServiceStatus_STOPPED:
		return ServiceStatusStopped, nil
	case kurtosis_core_rpc_api_bindings.ServiceStatus_UNKNOWN:
		return ServiceStatusUnknown, nil
	default:
		return "", fmt.Errorf("Kurtosis returned unsupported service status %d", status)
	}
}

func convertAPIPorts(ports map[string]*kurtosis_core_rpc_api_bindings.Port) map[string]Port {
	result := make(map[string]Port, len(ports))
	for id, port := range ports {
		result[id] = Port{
			ID:                  id,
			Number:              uint16(port.GetNumber()),
			TransportProtocol:   fmt.Sprint(port.GetTransportProtocol()),
			ApplicationProtocol: port.GetMaybeApplicationProtocol(),
		}
	}
	return result
}

var _ Client = (*SDKClient)(nil)
