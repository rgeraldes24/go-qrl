// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
	systemSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/system"
)

// systemConfiguration maps the already validated, UUID-bearing topology into
// the importable suite. Endpoints intentionally remain empty: the SDK-backed
// controller resolves them and refreshes service contexts after every restart.
func (runtime *Runtime) systemConfiguration(rawPhase string) (systemSuite.Config, string, error) {
	if runtime.Topology == nil {
		return systemSuite.Config{}, "", errors.New("system suite requires discovered topology")
	}
	if err := runtime.Topology.Validate(); err != nil {
		return systemSuite.Config{}, "", fmt.Errorf("system suite topology: %w", err)
	}
	phase := systemSuite.Phase(rawPhase)
	var stageName string
	switch phase {
	case systemSuite.PhaseBase:
		stageName = "system-base"
	case systemSuite.PhaseSignerRestart:
		stageName = "system-signer"
	case systemSuite.PhaseParticipantRestart:
		stageName = "system-participant"
	default:
		return systemSuite.Config{}, "", fmt.Errorf("unsupported harness system phase %q", rawPhase)
	}
	configuration := systemSuite.DefaultConfig()
	configuration.Enclave = runtime.Enclave.UUID
	configuration.Phase = phase
	for i := range configuration.ELServices {
		configuration.ELServices[i] = runtime.Topology.Execution[i].Service.Name
		configuration.CLServices[i] = runtime.Topology.Consensus[i].Service.Name
		configuration.VCServices[i] = runtime.Topology.Validators[i].Service.Name
	}
	configuration.SignerService = runtime.Topology.Signer.Service.Name
	configuration.RPCURLs = [2]string{}
	configuration.CLURLs = [2]string{}
	configuration.VCMetricsURLs = [2]string{}
	configuration.SignerURL = ""
	return configuration, stageName, nil
}

// systemServiceController performs every mutation by the service's immutable
// UUID. Names are accepted only as suite role labels and are cross-checked
// against the persisted topology before the SDK is called.
type systemServiceController struct {
	runtime *Runtime
}

func (controller systemServiceController) Endpoint(ctx context.Context, serviceName, portID, scheme string) (string, error) {
	if controller.runtime == nil || controller.runtime.Topology == nil || controller.runtime.Dependencies.Client == nil {
		return "", errors.New("system service controller is not initialized")
	}
	refreshed, err := topology.RefreshAfterRestart(ctx, controller.runtime.Dependencies.Client, controller.runtime.Enclave, *controller.runtime.Topology, serviceName)
	if err != nil {
		return "", err
	}
	endpoint, err := systemPublicEndpoint(refreshed, serviceName, portID, scheme)
	if err != nil {
		return "", err
	}
	if controller.runtime.Writer == nil {
		return "", errors.New("system service controller has no artifact writer")
	}
	if err := controller.runtime.Writer.WriteTopology(refreshed); err != nil {
		return "", fmt.Errorf("persist topology after refreshing %s: %w", serviceName, err)
	}
	controller.runtime.Topology = &refreshed
	return endpoint, nil
}

func (controller systemServiceController) Stop(ctx context.Context, serviceName string) error {
	identity, err := controller.identity(ctx, serviceName)
	if err != nil {
		return err
	}
	return controller.runtime.Dependencies.Client.StopService(ctx, controller.runtime.Enclave, identity.UUID)
}

func (controller systemServiceController) Status(ctx context.Context, serviceName string) (systemSuite.ServiceStatus, error) {
	identity, err := controller.identity(ctx, serviceName)
	if err != nil {
		return "", err
	}
	live, err := controller.runtime.Dependencies.Client.Service(ctx, controller.runtime.Enclave, identity.UUID)
	if err != nil {
		return "", fmt.Errorf("read immutable service status for %q/%q: %w", identity.Name, identity.UUID, err)
	}
	switch live.Status {
	case kurtosis.ServiceStatusRunning:
		return systemSuite.ServiceRunning, nil
	case kurtosis.ServiceStatusStopped:
		return systemSuite.ServiceStopped, nil
	default:
		return "", fmt.Errorf("service %q/%q has unsafe Kurtosis status %q", identity.Name, identity.UUID, live.Status)
	}
}

func (controller systemServiceController) Start(ctx context.Context, serviceName string) error {
	identity, err := controller.identity(ctx, serviceName)
	if err != nil {
		return err
	}
	return controller.runtime.Dependencies.Client.StartService(ctx, controller.runtime.Enclave, identity.UUID)
}

func (controller systemServiceController) identity(ctx context.Context, serviceName string) (topology.ServiceIdentity, error) {
	if controller.runtime == nil || controller.runtime.Topology == nil || controller.runtime.Dependencies.Client == nil {
		return topology.ServiceIdentity{}, errors.New("system service controller is not initialized")
	}
	identity, ok := systemServiceIdentity(*controller.runtime.Topology, serviceName)
	if !ok {
		return topology.ServiceIdentity{}, fmt.Errorf("service %q is not in the discovered system topology", serviceName)
	}
	live, err := controller.runtime.Dependencies.Client.Service(ctx, controller.runtime.Enclave, identity.UUID)
	if err != nil {
		return topology.ServiceIdentity{}, fmt.Errorf("verify service %q by UUID %q: %w", serviceName, identity.UUID, err)
	}
	if live.Name != identity.Name || live.UUID != identity.UUID {
		return topology.ServiceIdentity{}, fmt.Errorf("service identity changed: got %q/%q, want %q/%q", live.Name, live.UUID, identity.Name, identity.UUID)
	}
	return identity, nil
}

type systemEvidenceRecorder struct {
	runtime     *Runtime
	environment *lifecycle.RunEnvironment
	phase       systemSuite.Phase
	stageName   string
}

func (recorder systemEvidenceRecorder) RecordRestart(ctx context.Context, evidence systemSuite.RestartEvidence) error {
	if err := recorder.validate(ctx, evidence.Phase); err != nil {
		return err
	}
	switch evidence.State {
	case systemSuite.RestartStopIntent, systemSuite.RestartStopped, systemSuite.RestartStartIntent, systemSuite.RestartStarted, systemSuite.RestartHealthy,
		systemSuite.RestartEmergencyStartIntent, systemSuite.RestartEmergencyStarted:
	default:
		return fmt.Errorf("unsupported system restart state %q", evidence.State)
	}
	identity, ok := systemServiceIdentity(*recorder.runtime.Topology, evidence.Service)
	if !ok {
		return fmt.Errorf("restart evidence names unknown service %q", evidence.Service)
	}
	return recorder.environment.State.RecordServiceTransition(recorder.runtime.Store, lifecycle.ServiceTransition{
		Phase: string(evidence.Phase), ServiceName: identity.Name, ServiceUUID: identity.UUID,
		State: string(evidence.State), At: evidence.At.UTC(),
	})
}

func (recorder systemEvidenceRecorder) RecordEndpoint(ctx context.Context, evidence systemSuite.EndpointEvidence) error {
	if err := recorder.validate(ctx, evidence.Phase); err != nil {
		return err
	}
	identity, ok := systemServiceIdentity(*recorder.runtime.Topology, evidence.Service)
	if !ok {
		return fmt.Errorf("endpoint evidence names unknown service %q", evidence.Service)
	}
	return recorder.environment.State.RecordEndpointRefresh(recorder.runtime.Store, lifecycle.EndpointRefresh{
		Phase: string(evidence.Phase), ServiceName: identity.Name, ServiceUUID: identity.UUID,
		Kind: evidence.Kind, Previous: evidence.Previous, Current: evidence.Current, At: evidence.At.UTC(),
	})
}

func (recorder systemEvidenceRecorder) RecordTransaction(ctx context.Context, evidence systemSuite.TransactionEvidence) error {
	if err := recorder.validate(ctx, evidence.Phase); err != nil {
		return err
	}
	if recorder.stageName == "" || evidence.Label == "" || evidence.Hash == ([32]byte{}) || evidence.At.IsZero() {
		return errors.New("system transaction evidence is incomplete")
	}
	return recorder.environment.State.RecordTransaction(
		recorder.runtime.Store,
		recorder.stageName+"/"+evidence.Label,
		evidence.Hash.Hex(),
		evidence.At.UTC(),
	)
}

func (recorder systemEvidenceRecorder) RecordManagedTransactionIntent(ctx context.Context, intent systemSuite.ManagedTransactionIntent) error {
	if err := recorder.validate(ctx, systemSuite.Phase(intent.Phase)); err != nil {
		return err
	}
	if recorder.stageName == "" || intent.Label == "" {
		return errors.New("system managed transaction intent is incomplete")
	}
	intent.Label = recorder.stageName + "/" + intent.Label
	return recorder.environment.State.RecordManagedTransactionIntent(recorder.runtime.Store, intent.Label, intent, intent.PreparedAt)
}

func (recorder systemEvidenceRecorder) RecordManagedTransactionInitialAttempt(ctx context.Context, label string, at time.Time) error {
	if err := recorder.validate(ctx, recorder.phase); err != nil {
		return err
	}
	return recorder.environment.State.RecordManagedTransactionInitialAttempt(recorder.runtime.Store, recorder.stageName+"/"+label, at)
}

func (recorder systemEvidenceRecorder) RecordManagedTransactionResubmit(ctx context.Context, label string, at time.Time) error {
	if err := recorder.validate(ctx, recorder.phase); err != nil {
		return err
	}
	return recorder.environment.State.RecordManagedTransactionResubmit(recorder.runtime.Store, recorder.stageName+"/"+label, at)
}

func (recorder systemEvidenceRecorder) RecordSystemObservation(ctx context.Context, label, value string, at time.Time) error {
	if err := recorder.validate(ctx, recorder.phase); err != nil {
		return err
	}
	if recorder.stageName == "" || label == "" || value == "" || at.IsZero() {
		return errors.New("system observation evidence is incomplete")
	}
	return recorder.environment.State.RecordSystemObservation(recorder.runtime.Store, recorder.stageName+"/"+label, value, at.UTC())
}

func (recorder systemEvidenceRecorder) validate(ctx context.Context, phase systemSuite.Phase) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if recorder.runtime == nil || recorder.runtime.Topology == nil || recorder.environment == nil || recorder.environment.State == nil {
		return errors.New("system evidence recorder is not initialized")
	}
	if phase != recorder.phase {
		return fmt.Errorf("system evidence phase %q does not match stage phase %q", phase, recorder.phase)
	}
	return nil
}

func systemServiceIdentity(discovered topology.Topology, name string) (topology.ServiceIdentity, bool) {
	for _, node := range discovered.Execution {
		if node.Service.Name == name {
			return node.Service, true
		}
	}
	for _, node := range discovered.Consensus {
		if node.Service.Name == name {
			return node.Service, true
		}
	}
	for _, node := range discovered.Validators {
		if node.Service.Name == name {
			return node.Service, true
		}
	}
	if discovered.Signer.Service.Name == name {
		return discovered.Signer.Service, true
	}
	return topology.ServiceIdentity{}, false
}

func systemRestartHistory(transitions []lifecycle.ServiceTransition, phase systemSuite.Phase, discovered topology.Topology) ([]systemSuite.RestartEvidence, error) {
	history := make([]systemSuite.RestartEvidence, 0)
	for _, transition := range transitions {
		if transition.Phase != string(phase) {
			continue
		}
		identity, ok := systemServiceIdentity(discovered, transition.ServiceName)
		if !ok || identity.UUID != transition.ServiceUUID {
			return nil, fmt.Errorf("restart history service identity %q/%q does not match current immutable topology", transition.ServiceName, transition.ServiceUUID)
		}
		history = append(history, systemSuite.RestartEvidence{
			Phase:   phase,
			Service: transition.ServiceName,
			State:   systemSuite.RestartState(transition.State),
			At:      transition.At,
		})
	}
	return history, nil
}

func systemManagedTransactionIntents(values map[string]lifecycle.ManagedTransactionIntent, stageName string) map[string]systemSuite.ManagedTransactionIntent {
	prefix := stageName + "/"
	result := make(map[string]systemSuite.ManagedTransactionIntent)
	for label, intent := range values {
		if !strings.HasPrefix(label, prefix) {
			continue
		}
		intent.Label = strings.TrimPrefix(intent.Label, prefix)
		result[strings.TrimPrefix(label, prefix)] = intent
	}
	return result
}

func systemManagedTransactionTimes(values map[string]time.Time, stageName string) map[string]time.Time {
	prefix := stageName + "/"
	result := make(map[string]time.Time)
	for label, at := range values {
		if strings.HasPrefix(label, prefix) {
			result[strings.TrimPrefix(label, prefix)] = at
		}
	}
	return result
}

func systemObservations(values map[string]string, stageName string) map[string]string {
	prefix := stageName + "/"
	result := make(map[string]string)
	for label, value := range values {
		if strings.HasPrefix(label, prefix) {
			result[strings.TrimPrefix(label, prefix)] = value
		}
	}
	return result
}

func systemServiceUUIDs(discovered topology.Topology) map[string]string {
	result := make(map[string]string)
	for _, node := range discovered.Execution {
		result[node.Service.Name] = node.Service.UUID
	}
	for _, node := range discovered.Consensus {
		result[node.Service.Name] = node.Service.UUID
	}
	for _, node := range discovered.Validators {
		result[node.Service.Name] = node.Service.UUID
	}
	result[discovered.Signer.Service.Name] = discovered.Signer.Service.UUID
	return result
}

func systemPublicEndpoint(discovered topology.Topology, serviceName, portID, scheme string) (string, error) {
	var endpoint string
	for _, node := range discovered.Execution {
		if node.Service.Name != serviceName {
			continue
		}
		switch portID {
		case node.RPC.PortID:
			endpoint = node.RPC.PublicURL
		case node.WS.PortID:
			endpoint = node.WS.PublicURL
		}
	}
	for _, node := range discovered.Consensus {
		if node.Service.Name == serviceName && node.HTTP.PortID == portID {
			endpoint = node.HTTP.PublicURL
		}
	}
	for _, node := range discovered.Validators {
		if node.Service.Name == serviceName && node.Metrics.PortID == portID {
			endpoint = node.Metrics.PublicURL
		}
	}
	if discovered.Signer.Service.Name == serviceName && discovered.Signer.HTTP.PortID == portID {
		endpoint = discovered.Signer.HTTP.PublicURL
	}
	if endpoint == "" {
		return "", fmt.Errorf("service %q has no discovered public port %q", serviceName, portID)
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != scheme || parsed.Hostname() == "" || parsed.Port() == "" {
		return "", fmt.Errorf("service %q port %q is not a valid %s endpoint: %q", serviceName, portID, scheme, endpoint)
	}
	// The suite adds the validator metrics path after resolving the published
	// port, matching the historical Kurtosis CLI behavior.
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

var _ systemSuite.ServiceController = systemServiceController{}
var _ systemSuite.EvidenceRecorder = systemEvidenceRecorder{}
var _ systemSuite.TransactionRecorder = systemEvidenceRecorder{}
var _ systemSuite.ManagedTransactionJournal = systemEvidenceRecorder{}
var _ systemSuite.SystemObservationRecorder = systemEvidenceRecorder{}
