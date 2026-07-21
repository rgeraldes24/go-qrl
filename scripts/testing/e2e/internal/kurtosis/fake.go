package kurtosis

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

type FakeClient struct {
	mu              sync.Mutex
	nextUUID        uint64
	enclaves        map[string]*fakeEnclave
	Calls           []string
	PackageResult   PackageResult
	PackageError    error
	LastInvocation  PackageInvocation
	InvocationError error
	CreateError     error
	DestroyError    error
	ServiceLogData  map[string][]byte
}

type fakeEnclave struct {
	ref      lifecycle.EnclaveRef
	services map[string]fakeService
}

type fakeService struct {
	Service
	portEpoch  uint16
	execCode   int32
	execOutput string
	execError  error
}

func NewFakeClient() *FakeClient {
	return &FakeClient{
		nextUUID:       1,
		enclaves:       make(map[string]*fakeEnclave),
		PackageResult:  PackageResult{SerializedOutput: `{}`},
		ServiceLogData: make(map[string][]byte),
	}
}

func (fake *FakeClient) CreateEnclave(_ context.Context, name string) (lifecycle.EnclaveRef, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.Calls = append(fake.Calls, "create:"+name)
	if fake.CreateError != nil {
		return lifecycle.EnclaveRef{}, fake.CreateError
	}
	for _, enclave := range fake.enclaves {
		if enclave.ref.Name == name {
			return lifecycle.EnclaveRef{}, fmt.Errorf("enclave name collision: %s", name)
		}
	}
	uuid := fmt.Sprintf("%032x", fake.nextUUID)
	fake.nextUUID++
	ref := lifecycle.EnclaveRef{Name: name, UUID: uuid, Owned: true}
	fake.enclaves[uuid] = &fakeEnclave{ref: ref, services: make(map[string]fakeService)}
	return ref, nil
}

func (fake *FakeClient) GetEnclave(_ context.Context, identifier string) (lifecycle.EnclaveRef, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.Calls = append(fake.Calls, "get:"+identifier)
	for _, enclave := range fake.enclaves {
		if enclave.ref.UUID == identifier || enclave.ref.Name == identifier {
			ref := enclave.ref
			ref.Owned = false
			return ref, nil
		}
	}
	return lifecycle.EnclaveRef{}, fmt.Errorf("enclave not found: %s", identifier)
}

func (fake *FakeClient) EnclaveExists(_ context.Context, uuid string) (bool, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.Calls = append(fake.Calls, "exists:"+uuid)
	_, exists := fake.enclaves[uuid]
	return exists, nil
}

func (fake *FakeClient) RunRemotePackage(_ context.Context, ref lifecycle.EnclaveRef, run PackageRun) (PackageResult, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.Calls = append(fake.Calls, "package:"+run.Locator)
	if _, err := fake.enclave(ref); err != nil {
		return PackageResult{}, err
	}
	fake.LastInvocation = PackageInvocation{Locator: run.Locator, SerializedParams: run.SerializedParams}
	if fake.PackageError != nil {
		return PackageResult{}, fake.PackageError
	}
	return fake.PackageResult, nil
}

func (fake *FakeClient) LastPackageInvocation(_ context.Context, ref lifecycle.EnclaveRef) (PackageInvocation, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.Calls = append(fake.Calls, "last-package-invocation")
	if _, err := fake.enclave(ref); err != nil {
		return PackageInvocation{}, err
	}
	if fake.InvocationError != nil {
		return PackageInvocation{}, fake.InvocationError
	}
	if fake.LastInvocation.Locator == "" {
		return PackageInvocation{}, ErrPackageInvocationNotFound
	}
	return fake.LastInvocation, nil
}

func (fake *FakeClient) Services(_ context.Context, ref lifecycle.EnclaveRef) ([]Service, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	enclave, err := fake.enclave(ref)
	if err != nil {
		return nil, err
	}
	result := make([]Service, 0, len(enclave.services))
	for _, service := range enclave.services {
		result = append(result, cloneService(service.Service))
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func (fake *FakeClient) Service(_ context.Context, ref lifecycle.EnclaveRef, identifier string) (Service, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	enclave, err := fake.enclave(ref)
	if err != nil {
		return Service{}, err
	}
	service, _, err := findFakeService(enclave, identifier)
	if err != nil {
		return Service{}, err
	}
	return cloneService(service.Service), nil
}

func (fake *FakeClient) StartService(_ context.Context, ref lifecycle.EnclaveRef, identifier string) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	enclave, err := fake.enclave(ref)
	if err != nil {
		return err
	}
	service, name, err := findFakeService(enclave, identifier)
	if err != nil {
		return err
	}
	if service.Status == ServiceStatusRunning {
		return errors.New("service is already running")
	}
	if service.Status != ServiceStatusStopped {
		return fmt.Errorf("cannot start service with status %q", service.Status)
	}
	service.Status = ServiceStatusRunning
	service.portEpoch++
	for id, port := range service.PublicPorts {
		port.Number += service.portEpoch
		service.PublicPorts[id] = port
	}
	enclave.services[name] = service
	fake.Calls = append(fake.Calls, "start:"+name)
	return nil
}

func (fake *FakeClient) StopService(_ context.Context, ref lifecycle.EnclaveRef, identifier string) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	enclave, err := fake.enclave(ref)
	if err != nil {
		return err
	}
	service, name, err := findFakeService(enclave, identifier)
	if err != nil {
		return err
	}
	if service.Status == ServiceStatusUnknown {
		return errors.New("cannot stop service with unknown status")
	}
	service.Status = ServiceStatusStopped
	enclave.services[name] = service
	fake.Calls = append(fake.Calls, "stop:"+name)
	return nil
}

func (fake *FakeClient) RemoveService(_ context.Context, ref lifecycle.EnclaveRef, identifier string) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	enclave, err := fake.enclave(ref)
	if err != nil {
		return err
	}
	_, name, err := findFakeService(enclave, identifier)
	if err != nil {
		return err
	}
	delete(enclave.services, name)
	fake.Calls = append(fake.Calls, "remove:"+name)
	return nil
}

func (fake *FakeClient) ExecCommand(_ context.Context, ref lifecycle.EnclaveRef, identifier string, _ []string) (int32, string, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	enclave, err := fake.enclave(ref)
	if err != nil {
		return -1, "", err
	}
	service, _, err := findFakeService(enclave, identifier)
	if err != nil {
		return -1, "", err
	}
	return service.execCode, service.execOutput, service.execError
}

func (fake *FakeClient) ServiceLogs(_ context.Context, ref lifecycle.EnclaveRef, identifiers []string) (map[string][]byte, error) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if _, err := fake.enclave(ref); err != nil {
		return nil, err
	}
	result := make(map[string][]byte, len(identifiers))
	for _, identifier := range identifiers {
		result[identifier] = append([]byte(nil), fake.ServiceLogData[identifier]...)
	}
	return result, nil
}

func (fake *FakeClient) DestroyEnclave(_ context.Context, ref lifecycle.EnclaveRef) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.Calls = append(fake.Calls, "destroy:"+ref.UUID)
	if fake.DestroyError != nil {
		return fake.DestroyError
	}
	if !ref.Owned {
		return errors.New("refusing to destroy borrowed enclave")
	}
	enclave, ok := fake.enclaves[ref.UUID]
	if !ok || enclave.ref.Name != ref.Name {
		return errors.New("enclave UUID/name mismatch")
	}
	delete(fake.enclaves, ref.UUID)
	return nil
}

func (fake *FakeClient) AddService(ref lifecycle.EnclaveRef, service Service) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	enclave, err := fake.enclave(ref)
	if err != nil {
		return err
	}
	if service.Name == "" || service.UUID == "" {
		return errors.New("fake service requires name and UUID")
	}
	if service.Status == "" {
		service.Status = ServiceStatusRunning
	}
	switch service.Status {
	case ServiceStatusRunning, ServiceStatusStopped, ServiceStatusUnknown:
	default:
		return fmt.Errorf("fake service has invalid status %q", service.Status)
	}
	enclave.services[service.Name] = fakeService{Service: cloneService(service)}
	return nil
}

// SetServiceStatus lets lifecycle tests model the API container's UNKNOWN
// state without coupling status to endpoint metadata.
func (fake *FakeClient) SetServiceStatus(ref lifecycle.EnclaveRef, identifier string, status ServiceStatus) error {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	enclave, err := fake.enclave(ref)
	if err != nil {
		return err
	}
	service, name, err := findFakeService(enclave, identifier)
	if err != nil {
		return err
	}
	switch status {
	case ServiceStatusRunning, ServiceStatusStopped, ServiceStatusUnknown:
	default:
		return fmt.Errorf("invalid fake service status %q", status)
	}
	service.Status = status
	enclave.services[name] = service
	return nil
}

func (fake *FakeClient) enclave(ref lifecycle.EnclaveRef) (*fakeEnclave, error) {
	enclave, ok := fake.enclaves[ref.UUID]
	if !ok || enclave.ref.Name != ref.Name {
		return nil, errors.New("enclave UUID/name mismatch")
	}
	return enclave, nil
}

func findFakeService(enclave *fakeEnclave, identifier string) (fakeService, string, error) {
	for name, service := range enclave.services {
		if name == identifier || service.UUID == identifier {
			return service, name, nil
		}
	}
	return fakeService{}, "", fmt.Errorf("service not found: %s", identifier)
}

func cloneService(service Service) Service {
	clone := service
	clone.PrivatePorts = make(map[string]Port, len(service.PrivatePorts))
	for id, port := range service.PrivatePorts {
		clone.PrivatePorts[id] = port
	}
	clone.PublicPorts = make(map[string]Port, len(service.PublicPorts))
	for id, port := range service.PublicPorts {
		clone.PublicPorts[id] = port
	}
	clone.Labels = make(map[string]string, len(service.Labels))
	for key, value := range service.Labels {
		clone.Labels[key] = value
	}
	return clone
}

var _ Client = (*FakeClient)(nil)
