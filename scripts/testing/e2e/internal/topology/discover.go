// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package topology

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

// Discover cross-checks the explicit role specification, the pinned package's
// serialized output, and Kurtosis's current Service models. Pass nil output
// only for a borrowed network where no package invocation result is available.
func Discover(spec Spec, output *PackageOutput, services []kurtosis.Service) (Topology, error) {
	if spec.SourceRevisionLabel == "" {
		spec.SourceRevisionLabel = DefaultSourceRevisionLabel
	}
	if err := spec.Validate(); err != nil {
		return Topology{}, err
	}
	if output != nil {
		if err := output.Validate(); err != nil {
			return Topology{}, err
		}
		if err := crossCheckPackageSpec(spec, *output); err != nil {
			return Topology{}, err
		}
	}
	serviceByName, err := indexServices(services)
	if err != nil {
		return Topology{}, err
	}

	result := Topology{Schema: TopologySchemaVersion}
	if output != nil {
		result.NetworkID = output.NetworkID
		result.FinalGenesisTimestamp = output.FinalGenesisTimestamp
	}
	for i, nodeSpec := range spec.Execution {
		service, err := requireService(serviceByName, "execution", nodeSpec.Name, nodeSpec.UUID)
		if err != nil {
			return Topology{}, err
		}
		node, err := resolveExecution(nodeSpec, spec.SourceRevision, spec.SourceRevisionLabel, service)
		if err != nil {
			return Topology{}, err
		}
		if output != nil {
			if err := crossCheckPackageExecution(i, output.Participants[i].Execution, node, service); err != nil {
				return Topology{}, err
			}
		}
		result.Execution = append(result.Execution, node)
	}
	for i, nodeSpec := range spec.Consensus {
		service, err := requireService(serviceByName, "consensus", nodeSpec.Name, nodeSpec.UUID)
		if err != nil {
			return Topology{}, err
		}
		node, err := resolveConsensus(nodeSpec, service)
		if err != nil {
			return Topology{}, err
		}
		if output != nil {
			if err := crossCheckPackageConsensus(i, output.Participants[i].Consensus, node, service); err != nil {
				return Topology{}, err
			}
		}
		result.Consensus = append(result.Consensus, node)
	}
	for i, nodeSpec := range spec.Validators {
		service, err := requireService(serviceByName, "validator", nodeSpec.Name, nodeSpec.UUID)
		if err != nil {
			return Topology{}, err
		}
		node, err := resolveValidator(nodeSpec, service)
		if err != nil {
			return Topology{}, err
		}
		if output != nil {
			if err := crossCheckPackageValidator(i, output.Participants[i].Validator, node, service); err != nil {
				return Topology{}, err
			}
		}
		result.Validators = append(result.Validators, node)
	}
	signerService, err := requireService(serviceByName, "signer", spec.Signer.Name, spec.Signer.UUID)
	if err != nil {
		return Topology{}, err
	}
	result.Signer, err = resolveSigner(spec.Signer, signerService)
	if err != nil {
		return Topology{}, err
	}
	for _, helperSpec := range sortedHelperSpecs(spec.Helpers) {
		service, err := requireService(serviceByName, "helper", helperSpec.Name, helperSpec.UUID)
		if err != nil {
			return Topology{}, err
		}
		result.Helpers = append(result.Helpers, HelperNode{Service: serviceIdentity(service)})
	}
	result = result.canonical()
	if err := result.Validate(); err != nil {
		return Topology{}, err
	}
	return result, nil
}

func DiscoverSerialized(spec Spec, serialized string, services []kurtosis.Service) (Topology, error) {
	output, err := ParsePackageOutput(serialized)
	if err != nil {
		return Topology{}, err
	}
	return Discover(spec, &output, services)
}

func indexServices(services []kurtosis.Service) (map[string]kurtosis.Service, error) {
	byName := make(map[string]kurtosis.Service, len(services))
	byUUID := make(map[string]string, len(services))
	for i, service := range services {
		if service.Name == "" {
			return nil, fmt.Errorf("Kurtosis service[%d] has an empty name", i)
		}
		if !serviceUUIDPattern.MatchString(service.UUID) {
			return nil, fmt.Errorf("Kurtosis service %q has invalid UUID %q", service.Name, service.UUID)
		}
		if _, ok := byName[service.Name]; ok {
			return nil, fmt.Errorf("Kurtosis returned duplicate service name %q", service.Name)
		}
		if previous, ok := byUUID[service.UUID]; ok {
			return nil, fmt.Errorf("Kurtosis returned duplicate service UUID %q for %q and %q", service.UUID, previous, service.Name)
		}
		byName[service.Name] = service
		byUUID[service.UUID] = service.Name
	}
	return byName, nil
}

func requireService(services map[string]kurtosis.Service, role, name, expectedUUID string) (kurtosis.Service, error) {
	service, ok := services[name]
	if !ok {
		return kurtosis.Service{}, fmt.Errorf("required %s service %q is missing", role, name)
	}
	if service.Name != name {
		return kurtosis.Service{}, fmt.Errorf("required %s service name changed: got %q, want %q", role, service.Name, name)
	}
	if expectedUUID != "" && service.UUID != expectedUUID {
		return kurtosis.Service{}, fmt.Errorf("required %s service %q UUID changed: got %q, want %q", role, name, service.UUID, expectedUUID)
	}
	return service, nil
}

func resolveExecution(spec ExecutionSpec, expectedRevision, revisionLabel string, service kurtosis.Service) (ExecutionNode, error) {
	rpc, err := resolveEndpoint(service, spec.RPCPortID, "http", "")
	if err != nil {
		return ExecutionNode{}, fmt.Errorf("execution service %q RPC: %w", service.Name, err)
	}
	ws, err := resolveEndpoint(service, spec.WSPortID, "ws", "")
	if err != nil {
		return ExecutionNode{}, fmt.Errorf("execution service %q WS: %w", service.Name, err)
	}
	revision := service.Labels[revisionLabel]
	if revision != "" && !revisionPattern.MatchString(revision) {
		return ExecutionNode{}, fmt.Errorf("execution service %q reports invalid source revision %q in label %q", service.Name, revision, revisionLabel)
	}
	if expectedRevision != "" && revision != expectedRevision {
		return ExecutionNode{}, fmt.Errorf("execution service %q source revision is %q, want %q from label %q", service.Name, revision, expectedRevision, revisionLabel)
	}
	node := ExecutionNode{Service: serviceIdentity(service), Client: spec.Client, RPC: rpc, WS: ws}
	if revision != "" {
		node.SourceRevision = revision
		node.SourceRevisionLabel = revisionLabel
	}
	return node, nil
}

func resolveConsensus(spec ConsensusSpec, service kurtosis.Service) (ConsensusNode, error) {
	httpEndpoint, err := resolveEndpoint(service, spec.HTTPPortID, "http", "")
	if err != nil {
		return ConsensusNode{}, fmt.Errorf("consensus service %q HTTP: %w", service.Name, err)
	}
	return ConsensusNode{Service: serviceIdentity(service), Client: spec.Client, HTTP: httpEndpoint}, nil
}

func resolveValidator(spec ValidatorSpec, service kurtosis.Service) (ValidatorNode, error) {
	metricsPath := spec.MetricsPath
	if metricsPath == "" {
		metricsPath = "/metrics"
	}
	metrics, err := resolveEndpoint(service, spec.MetricsPortID, "http", metricsPath)
	if err != nil {
		return ValidatorNode{}, fmt.Errorf("validator service %q metrics: %w", service.Name, err)
	}
	return ValidatorNode{Service: serviceIdentity(service), Client: spec.Client, Metrics: metrics, MetricsPath: metricsPath}, nil
}

func resolveSigner(spec SignerSpec, service kurtosis.Service) (SignerNode, error) {
	httpEndpoint, err := resolveEndpoint(service, spec.HTTPPortID, "http", "")
	if err != nil {
		return SignerNode{}, fmt.Errorf("signer service %q HTTP: %w", service.Name, err)
	}
	return SignerNode{Service: serviceIdentity(service), Client: spec.Client, HTTP: httpEndpoint}, nil
}

func resolveEndpoint(service kurtosis.Service, portID, scheme, path string) (Endpoint, error) {
	privatePort, err := requirePort(service.Name, "private", portID, service.PrivatePorts)
	if err != nil {
		return Endpoint{}, err
	}
	publicPort, err := requirePort(service.Name, "public", portID, service.PublicPorts)
	if err != nil {
		return Endpoint{}, err
	}
	if service.PrivateIP == "" || service.PublicIP == "" {
		return Endpoint{}, fmt.Errorf("service %q lacks a private or public IP", service.Name)
	}
	return Endpoint{
		PortID:     portID,
		PrivateURL: endpointURL(scheme, service.PrivateIP, privatePort.Number, path),
		PublicURL:  endpointURL(scheme, service.PublicIP, publicPort.Number, path),
	}, nil
}

func requirePort(serviceName, scope, portID string, ports map[string]kurtosis.Port) (kurtosis.Port, error) {
	port, ok := ports[portID]
	if !ok {
		return kurtosis.Port{}, fmt.Errorf("service %q is missing required %s port ID %q", serviceName, scope, portID)
	}
	if port.ID != portID {
		return kurtosis.Port{}, fmt.Errorf("service %q %s port map key %q contains port ID %q", serviceName, scope, portID, port.ID)
	}
	if port.Number == 0 {
		return kurtosis.Port{}, fmt.Errorf("service %q %s port %q has number zero", serviceName, scope, portID)
	}
	return port, nil
}

func endpointURL(scheme, host string, port uint16, path string) string {
	return (&url.URL{Scheme: scheme, Host: net.JoinHostPort(host, strconv.Itoa(int(port))), Path: path}).String()
}

func serviceIdentity(service kurtosis.Service) ServiceIdentity {
	return ServiceIdentity{Name: service.Name, UUID: service.UUID}
}

func crossCheckPackageSpec(spec Spec, output PackageOutput) error {
	for i, participant := range output.Participants {
		if participant.Execution.ServiceName != spec.Execution[i].Name || participant.Execution.ClientName != spec.Execution[i].Client {
			return fmt.Errorf("qrl-package participant[%d] execution is %q/%q, explicit spec requires %q/%q", i, participant.Execution.ServiceName, participant.Execution.ClientName, spec.Execution[i].Name, spec.Execution[i].Client)
		}
		if participant.Consensus.ServiceName != spec.Consensus[i].Name || participant.Consensus.ClientName != spec.Consensus[i].Client {
			return fmt.Errorf("qrl-package participant[%d] consensus is %q/%q, explicit spec requires %q/%q", i, participant.Consensus.ServiceName, participant.Consensus.ClientName, spec.Consensus[i].Name, spec.Consensus[i].Client)
		}
		if participant.Validator.ServiceName != spec.Validators[i].Name || participant.Validator.ClientName != spec.Validators[i].Client {
			return fmt.Errorf("qrl-package participant[%d] validator is %q/%q, explicit spec requires %q/%q", i, participant.Validator.ServiceName, participant.Validator.ClientName, spec.Validators[i].Name, spec.Validators[i].Client)
		}
		if participant.RemoteSignerType != spec.Signer.Client {
			return fmt.Errorf("qrl-package participant[%d] remote signer type is %q, explicit spec requires %q", i, participant.RemoteSignerType, spec.Signer.Client)
		}
	}
	return nil
}

func crossCheckPackageExecution(index int, context PackageExecutionContext, node ExecutionNode, service kurtosis.Service) error {
	if context.ServiceName != node.Service.Name || context.IP != service.PrivateIP {
		return fmt.Errorf("qrl-package participant[%d] execution identity %q/%q differs from Kurtosis %q/%q", index, context.ServiceName, context.IP, service.Name, service.PrivateIP)
	}
	if context.RPCURL != node.RPC.PrivateURL || context.WSURL != node.WS.PrivateURL {
		return fmt.Errorf("qrl-package participant[%d] execution endpoints %q/%q differ from Kurtosis %q/%q", index, context.RPCURL, context.WSURL, node.RPC.PrivateURL, node.WS.PrivateURL)
	}
	if context.RPCPort != service.PrivatePorts[node.RPC.PortID].Number || context.WSPort != service.PrivatePorts[node.WS.PortID].Number {
		return fmt.Errorf("qrl-package participant[%d] execution port numbers differ from Kurtosis", index)
	}
	return nil
}

func crossCheckPackageConsensus(index int, context PackageConsensusContext, node ConsensusNode, service kurtosis.Service) error {
	if context.ServiceName != node.Service.Name || context.IP != service.PrivateIP {
		return fmt.Errorf("qrl-package participant[%d] consensus identity %q/%q differs from Kurtosis %q/%q", index, context.ServiceName, context.IP, service.Name, service.PrivateIP)
	}
	if context.HTTPURL != node.HTTP.PrivateURL || context.HTTPPort != service.PrivatePorts[node.HTTP.PortID].Number {
		return fmt.Errorf("qrl-package participant[%d] consensus HTTP endpoint differs from Kurtosis", index)
	}
	return nil
}

func crossCheckPackageValidator(index int, context PackageValidatorContext, node ValidatorNode, service kurtosis.Service) error {
	if context.ServiceName != node.Service.Name || context.Metrics.Name != node.Service.Name {
		return fmt.Errorf("qrl-package participant[%d] validator identity differs from Kurtosis service %q", index, service.Name)
	}
	privatePort := service.PrivatePorts[node.Metrics.PortID]
	packageAuthority := net.JoinHostPort(service.PrivateIP, strconv.Itoa(int(privatePort.Number)))
	if context.Metrics.URL != packageAuthority || context.Metrics.Path != node.MetricsPath || node.Metrics.PrivateURL != endpointURL("http", service.PrivateIP, privatePort.Number, node.MetricsPath) {
		return fmt.Errorf("qrl-package participant[%d] validator metrics endpoint differs from Kurtosis", index)
	}
	return nil
}

// ServiceClient is the narrow part of the Kurtosis adapter needed to refresh
// a service context after restart.
type ServiceClient interface {
	Service(context.Context, lifecycle.EnclaveRef, string) (kurtosis.Service, error)
}

// RefreshAfterRestart fetches a new Service model by the previously captured
// full UUID, rebuilds its public/private endpoint, and validates the complete
// topology before returning it. The input is not mutated.
func RefreshAfterRestart(ctx context.Context, client ServiceClient, enclave lifecycle.EnclaveRef, current Topology, serviceName string) (Topology, error) {
	if err := current.Validate(); err != nil {
		return Topology{}, fmt.Errorf("current topology is invalid: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return Topology{}, err
	}
	identity, ok := current.serviceIdentity(serviceName)
	if !ok {
		return Topology{}, fmt.Errorf("service %q is not part of the topology", serviceName)
	}
	service, err := client.Service(ctx, enclave, identity.UUID)
	if err != nil {
		return Topology{}, fmt.Errorf("refresh service %q by UUID %q: %w", serviceName, identity.UUID, err)
	}
	if service.Name != identity.Name || service.UUID != identity.UUID {
		return Topology{}, fmt.Errorf("refreshed service identity changed: got %q/%q, want %q/%q", service.Name, service.UUID, identity.Name, identity.UUID)
	}

	result := current.canonical()
	refreshed := false
	for i, node := range result.Execution {
		if node.Service.Name != serviceName {
			continue
		}
		spec := ExecutionSpec{Name: node.Service.Name, UUID: node.Service.UUID, Client: node.Client, RPCPortID: node.RPC.PortID, WSPortID: node.WS.PortID}
		updated, err := resolveExecution(spec, node.SourceRevision, revisionLabel(node.SourceRevisionLabel), service)
		if err != nil {
			return Topology{}, err
		}
		result.Execution[i] = updated
		refreshed = true
	}
	for i, node := range result.Consensus {
		if node.Service.Name != serviceName {
			continue
		}
		updated, err := resolveConsensus(ConsensusSpec{Name: node.Service.Name, UUID: node.Service.UUID, Client: node.Client, HTTPPortID: node.HTTP.PortID}, service)
		if err != nil {
			return Topology{}, err
		}
		result.Consensus[i] = updated
		refreshed = true
	}
	for i, node := range result.Validators {
		if node.Service.Name != serviceName {
			continue
		}
		updated, err := resolveValidator(ValidatorSpec{Name: node.Service.Name, UUID: node.Service.UUID, Client: node.Client, MetricsPortID: node.Metrics.PortID, MetricsPath: node.MetricsPath}, service)
		if err != nil {
			return Topology{}, err
		}
		result.Validators[i] = updated
		refreshed = true
	}
	if result.Signer.Service.Name == serviceName {
		updated, err := resolveSigner(SignerSpec{Name: result.Signer.Service.Name, UUID: result.Signer.Service.UUID, Client: result.Signer.Client, HTTPPortID: result.Signer.HTTP.PortID}, service)
		if err != nil {
			return Topology{}, err
		}
		result.Signer = updated
		refreshed = true
	}
	for i, helper := range result.Helpers {
		if helper.Service.Name == serviceName {
			result.Helpers[i] = HelperNode{Service: serviceIdentity(service)}
			refreshed = true
		}
	}
	if !refreshed {
		return Topology{}, errors.New("topology service disappeared during refresh")
	}
	result = result.canonical()
	if err := result.Validate(); err != nil {
		return Topology{}, fmt.Errorf("refreshed topology is invalid: %w", err)
	}
	return result, nil
}

func (topology Topology) serviceIdentity(name string) (ServiceIdentity, bool) {
	for _, node := range topology.Execution {
		if node.Service.Name == name {
			return node.Service, true
		}
	}
	for _, node := range topology.Consensus {
		if node.Service.Name == name {
			return node.Service, true
		}
	}
	for _, node := range topology.Validators {
		if node.Service.Name == name {
			return node.Service, true
		}
	}
	if topology.Signer.Service.Name == name {
		return topology.Signer.Service, true
	}
	for _, helper := range topology.Helpers {
		if helper.Service.Name == name {
			return helper.Service, true
		}
	}
	return ServiceIdentity{}, false
}

func revisionLabel(label string) string {
	if label == "" {
		return DefaultSourceRevisionLabel
	}
	return label
}

// ServiceNames returns the exact role services in deterministic order. It is
// useful when requesting diagnostics from the Kurtosis adapter.
func (topology Topology) ServiceNames() []string {
	canonical := topology.canonical()
	names := make([]string, 0, len(canonical.Execution)+len(canonical.Consensus)+len(canonical.Validators)+1+len(canonical.Helpers))
	for _, node := range canonical.Execution {
		names = append(names, node.Service.Name)
	}
	for _, node := range canonical.Consensus {
		names = append(names, node.Service.Name)
	}
	for _, node := range canonical.Validators {
		names = append(names, node.Service.Name)
	}
	names = append(names, canonical.Signer.Service.Name)
	for _, helper := range canonical.Helpers {
		names = append(names, helper.Service.Name)
	}
	sort.Strings(names)
	return names
}

var _ ServiceClient = kurtosis.Client(nil)
