// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package topology maps explicit qrl-package service roles to exact Kurtosis
// service UUIDs and host-reachable endpoints.
package topology

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
)

var (
	uuidPattern               = regexp.MustCompile(`^[0-9a-f]{32}$`)
	rolePattern               = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
	persistentParticipantName = regexp.MustCompile(`^(?:el|cl|vc)-[0-9]+-`)
)

type Spec struct {
	Execution   ExecutionSpec
	Required    []ServiceSpec
	GraphQLPath string
}

type ServiceSpec struct {
	Role string
	Name string
}

type ExecutionSpec struct {
	ServiceSpec
	RPCPortID string
	WSPortID  string
}

func (spec Spec) Validate() error {
	if err := validateSpec(spec.Execution.ServiceSpec); err != nil {
		return fmt.Errorf("execution service: %w", err)
	}
	if spec.Execution.RPCPortID == "" || spec.Execution.WSPortID == "" || spec.Execution.RPCPortID == spec.Execution.WSPortID {
		return errors.New("execution service must declare distinct RPC and WebSocket port IDs")
	}
	if len(spec.Required) == 0 {
		return errors.New("topology must declare required services")
	}
	seenRoles := map[string]struct{}{spec.Execution.Role: {}}
	seenNames := map[string]struct{}{spec.Execution.Name: {}}
	for _, service := range spec.Required {
		if err := validateSpec(service); err != nil {
			return err
		}
		if _, exists := seenRoles[service.Role]; exists {
			return fmt.Errorf("topology role %q is duplicated", service.Role)
		}
		if _, exists := seenNames[service.Name]; exists {
			return fmt.Errorf("topology service %q is duplicated", service.Name)
		}
		seenRoles[service.Role], seenNames[service.Name] = struct{}{}, struct{}{}
	}
	if !strings.HasPrefix(spec.GraphQLPath, "/") {
		return errors.New("topology GraphQL path is invalid")
	}
	return nil
}

func validateSpec(spec ServiceSpec) error {
	if !rolePattern.MatchString(spec.Role) || strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("invalid service role/name %q/%q", spec.Role, spec.Name)
	}
	return nil
}

type Snapshot struct {
	Execution ServiceIdentity   `json:"execution"`
	Required  []ServiceIdentity `json:"required"`
	RPC       Endpoint          `json:"rpc"`
	WebSocket Endpoint          `json:"websocket"`
	GraphQL   string            `json:"graphql"`
}

type ServiceIdentity struct {
	Role  string `json:"role"`
	Name  string `json:"name"`
	UUID  string `json:"uuid"`
	Image string `json:"image"`
}

type Endpoint struct {
	PortID string `json:"port_id"`
	URL    string `json:"url"`
}

func Discover(spec Spec, services []kurtosis.Service) (Snapshot, error) {
	if err := spec.Validate(); err != nil {
		return Snapshot{}, err
	}
	byName := make(map[string]kurtosis.Service, len(services))
	uuids := make(map[string]string, len(services))
	for _, service := range services {
		if service.Name == "" || !uuidPattern.MatchString(service.UUID) {
			return Snapshot{}, fmt.Errorf("Kurtosis returned invalid service identity %q/%q", service.Name, service.UUID)
		}
		if previous, exists := uuids[service.UUID]; exists {
			return Snapshot{}, fmt.Errorf("Kurtosis service UUID %q is shared by %q and %q", service.UUID, previous, service.Name)
		}
		if _, exists := byName[service.Name]; exists {
			return Snapshot{}, fmt.Errorf("Kurtosis returned duplicate service name %q", service.Name)
		}
		byName[service.Name], uuids[service.UUID] = service, service.Name
	}
	if err := rejectUndeclaredParticipants(specServiceNames(spec), services); err != nil {
		return Snapshot{}, err
	}
	execution, err := requireRunning(byName, spec.Execution.ServiceSpec)
	if err != nil {
		return Snapshot{}, err
	}
	rpc, ok := execution.PublicEndpoint(spec.Execution.RPCPortID, "http")
	if !ok {
		return Snapshot{}, fmt.Errorf("execution service %q lacks public RPC port %q", execution.Name, spec.Execution.RPCPortID)
	}
	ws, ok := execution.PublicEndpoint(spec.Execution.WSPortID, "ws")
	if !ok {
		return Snapshot{}, fmt.Errorf("execution service %q lacks public WebSocket port %q", execution.Name, spec.Execution.WSPortID)
	}
	snapshot := Snapshot{
		Execution: identity(spec.Execution.ServiceSpec, execution),
		RPC:       Endpoint{PortID: spec.Execution.RPCPortID, URL: rpc},
		WebSocket: Endpoint{PortID: spec.Execution.WSPortID, URL: ws},
		GraphQL:   strings.TrimRight(rpc, "/") + spec.GraphQLPath,
	}
	for _, serviceSpec := range spec.Required {
		service, err := requireRunning(byName, serviceSpec)
		if err != nil {
			return Snapshot{}, err
		}
		snapshot.Required = append(snapshot.Required, identity(serviceSpec, service))
	}
	sort.Slice(snapshot.Required, func(i, j int) bool { return snapshot.Required[i].Role < snapshot.Required[j].Role })
	if err := snapshot.Validate(); err != nil {
		return Snapshot{}, err
	}
	return snapshot, nil
}

// VerifySnapshot re-authenticates every persisted service name/UUID/image,
// port ID, and resolved public endpoint.
func VerifySnapshot(expected Snapshot, services []kurtosis.Service) error {
	if err := expected.Validate(); err != nil {
		return err
	}
	byName := make(map[string]kurtosis.Service, len(services))
	for _, service := range services {
		if _, exists := byName[service.Name]; exists {
			return fmt.Errorf("Kurtosis returned duplicate service name %q", service.Name)
		}
		byName[service.Name] = service
	}
	if err := rejectUndeclaredParticipants(snapshotServiceNames(expected), services); err != nil {
		return err
	}
	verify := func(identity ServiceIdentity) error {
		service, exists := byName[identity.Name]
		if !exists {
			return fmt.Errorf("required %s service %q is missing", identity.Role, identity.Name)
		}
		if service.UUID != identity.UUID || service.Image != identity.Image || service.Status != kurtosis.ServiceStatusRunning {
			return fmt.Errorf("required %s service identity changed: got %q/%q/%q/%s, want %q/%q/%q/RUNNING", identity.Role, service.Name, service.UUID, service.Image, service.Status, identity.Name, identity.UUID, identity.Image)
		}
		return nil
	}
	if err := verify(expected.Execution); err != nil {
		return err
	}
	for _, identity := range expected.Required {
		if err := verify(identity); err != nil {
			return err
		}
	}
	execution := byName[expected.Execution.Name]
	rpc, ok := execution.PublicEndpoint(expected.RPC.PortID, "http")
	if !ok || rpc != expected.RPC.URL {
		return fmt.Errorf("execution RPC endpoint changed: got %q, want %q", rpc, expected.RPC.URL)
	}
	ws, ok := execution.PublicEndpoint(expected.WebSocket.PortID, "ws")
	if !ok || ws != expected.WebSocket.URL {
		return fmt.Errorf("execution WebSocket endpoint changed: got %q, want %q", ws, expected.WebSocket.URL)
	}
	return nil
}

func specServiceNames(spec Spec) map[string]struct{} {
	names := map[string]struct{}{spec.Execution.Name: {}}
	for _, service := range spec.Required {
		names[service.Name] = struct{}{}
	}
	return names
}

func snapshotServiceNames(snapshot Snapshot) map[string]struct{} {
	names := map[string]struct{}{snapshot.Execution.Name: {}}
	for _, service := range snapshot.Required {
		names[service.Name] = struct{}{}
	}
	return names
}

func rejectUndeclaredParticipants(declared map[string]struct{}, services []kurtosis.Service) error {
	for _, service := range services {
		if persistentParticipantName.MatchString(service.Name) {
			if _, ok := declared[service.Name]; !ok {
				return fmt.Errorf("undeclared persistent participant service %q is present", service.Name)
			}
		}
	}
	return nil
}

func (snapshot Snapshot) Validate() error {
	if err := validateIdentity(snapshot.Execution); err != nil {
		return fmt.Errorf("execution identity: %w", err)
	}
	seenRoles := map[string]struct{}{snapshot.Execution.Role: {}}
	seenNames := map[string]struct{}{snapshot.Execution.Name: {}}
	seenUUIDs := map[string]struct{}{snapshot.Execution.UUID: {}}
	for _, service := range snapshot.Required {
		if err := validateIdentity(service); err != nil {
			return err
		}
		if _, ok := seenRoles[service.Role]; ok {
			return fmt.Errorf("topology role %q is duplicated", service.Role)
		}
		if _, ok := seenNames[service.Name]; ok {
			return fmt.Errorf("topology service %q is duplicated", service.Name)
		}
		if _, ok := seenUUIDs[service.UUID]; ok {
			return fmt.Errorf("topology UUID %q is duplicated", service.UUID)
		}
		seenRoles[service.Role], seenNames[service.Name], seenUUIDs[service.UUID] = struct{}{}, struct{}{}, struct{}{}
	}
	if err := validateURL(snapshot.RPC.URL, "http"); err != nil {
		return fmt.Errorf("RPC endpoint: %w", err)
	}
	if err := validateURL(snapshot.WebSocket.URL, "ws"); err != nil {
		return fmt.Errorf("WebSocket endpoint: %w", err)
	}
	if err := validateURL(snapshot.GraphQL, "http"); err != nil {
		return fmt.Errorf("GraphQL endpoint: %w", err)
	}
	if snapshot.RPC.PortID == "" || snapshot.WebSocket.PortID == "" || snapshot.RPC.PortID == snapshot.WebSocket.PortID {
		return errors.New("topology endpoint port IDs are incomplete or duplicated")
	}
	return nil
}

func requireRunning(services map[string]kurtosis.Service, spec ServiceSpec) (kurtosis.Service, error) {
	service, exists := services[spec.Name]
	if !exists {
		return kurtosis.Service{}, fmt.Errorf("required %s service %q is missing", spec.Role, spec.Name)
	}
	if service.Status != kurtosis.ServiceStatusRunning {
		return kurtosis.Service{}, fmt.Errorf("required %s service %q is %s, want RUNNING", spec.Role, spec.Name, service.Status)
	}
	if service.Image == "" {
		return kurtosis.Service{}, fmt.Errorf("required %s service %q has no image identity", spec.Role, spec.Name)
	}
	return service, nil
}

func identity(spec ServiceSpec, service kurtosis.Service) ServiceIdentity {
	return ServiceIdentity{Role: spec.Role, Name: service.Name, UUID: service.UUID, Image: service.Image}
}

func validateIdentity(identity ServiceIdentity) error {
	if !rolePattern.MatchString(identity.Role) || identity.Name == "" || !uuidPattern.MatchString(identity.UUID) || identity.Image == "" {
		return fmt.Errorf("invalid service identity %+v", identity)
	}
	return nil
}

func validateURL(raw, scheme string) error {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != scheme || parsed.Hostname() == "" || parsed.Port() == "" || parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("invalid %s URL %q", scheme, raw)
	}
	return nil
}
