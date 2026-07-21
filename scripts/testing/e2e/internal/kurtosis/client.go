// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package kurtosis

import (
	"context"
	"errors"
	"net"
	"strconv"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

// ErrPackageInvocationNotFound is returned only when Kurtosis proves that an
// enclave has no retained Starlark package invocation. Callers may treat this
// as evidence that a pre-journaled package call was not accepted; every other
// lookup error remains ambiguous and must fail closed.
var ErrPackageInvocationNotFound = errors.New("Kurtosis package invocation not found")

type PackageRun struct {
	Locator          string
	SerializedParams string
}

type PackageResult struct {
	SerializedOutput string `json:"serialized_output"`
}

// PackageInvocation is the immutable package identity retained by Kurtosis for
// the most recent Starlark run in an enclave. It lets a resumed harness prove
// that existing services came from the exact package and parameters it
// journaled before making the external call.
type PackageInvocation struct {
	Locator          string `json:"locator"`
	SerializedParams string `json:"serialized_params"`
}

type Port struct {
	ID                  string `json:"id"`
	Number              uint16 `json:"number"`
	TransportProtocol   string `json:"transport_protocol"`
	ApplicationProtocol string `json:"application_protocol,omitempty"`
}

// ServiceStatus is the lifecycle state reported by the Kurtosis API container.
// It is deliberately independent of published endpoint metadata: a running
// service may have no public ports, and endpoint publication may lag a restart.
type ServiceStatus string

const (
	ServiceStatusRunning ServiceStatus = "RUNNING"
	ServiceStatusStopped ServiceStatus = "STOPPED"
	ServiceStatusUnknown ServiceStatus = "UNKNOWN"
)

type Service struct {
	Name         string            `json:"name"`
	UUID         string            `json:"uuid"`
	Status       ServiceStatus     `json:"status"`
	PrivateIP    string            `json:"private_ip"`
	PublicIP     string            `json:"public_ip,omitempty"`
	PrivatePorts map[string]Port   `json:"private_ports"`
	PublicPorts  map[string]Port   `json:"public_ports"`
	Labels       map[string]string `json:"labels,omitempty"`
}

func (service Service) PublicEndpoint(portID, scheme string) (string, bool) {
	port, ok := service.PublicPorts[portID]
	if !ok || service.PublicIP == "" {
		return "", false
	}
	return scheme + "://" + net.JoinHostPort(service.PublicIP, strconv.FormatUint(uint64(port.Number), 10)), true
}

// Client is intentionally narrower than the Kurtosis SDK. It keeps SDK types
// out of suite code and makes lifecycle behavior testable with a project fake.
type Client interface {
	CreateEnclave(context.Context, string) (lifecycle.EnclaveRef, error)
	GetEnclave(context.Context, string) (lifecycle.EnclaveRef, error)
	EnclaveExists(context.Context, string) (bool, error)
	RunRemotePackage(context.Context, lifecycle.EnclaveRef, PackageRun) (PackageResult, error)
	LastPackageInvocation(context.Context, lifecycle.EnclaveRef) (PackageInvocation, error)
	Services(context.Context, lifecycle.EnclaveRef) ([]Service, error)
	Service(context.Context, lifecycle.EnclaveRef, string) (Service, error)
	StartService(context.Context, lifecycle.EnclaveRef, string) error
	StopService(context.Context, lifecycle.EnclaveRef, string) error
	RemoveService(context.Context, lifecycle.EnclaveRef, string) error
	ExecCommand(context.Context, lifecycle.EnclaveRef, string, []string) (int32, string, error)
	ServiceLogs(context.Context, lifecycle.EnclaveRef, []string) (map[string][]byte, error)
	DestroyEnclave(context.Context, lifecycle.EnclaveRef) error
}
