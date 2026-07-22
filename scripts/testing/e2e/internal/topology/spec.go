// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package topology discovers and validates the fixed two-participant VM64 E2E
// network. Role assignment is always explicit: service names and Kurtosis port
// IDs come from a Spec, never from fuzzy service-name matching.
package topology

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
)

const (
	ExpectedExecutionCount = 2
	ExpectedConsensusCount = 2
	ExpectedValidatorCount = 2

	DefaultSourceRevisionLabel = "commit"
	ephemeralGenesisHelperName = "run-generate-genesis"
)

var (
	serviceUUIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	revisionPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// Spec is the explicit contract between the pinned package and the E2E
// runner. UUIDs are optional when initially discovering a network and should
// be populated when persisting a topology for resume. A non-empty UUID is an
// exact identity assertion, not a hint.
type Spec struct {
	Execution           []ExecutionSpec `json:"execution"`
	Consensus           []ConsensusSpec `json:"consensus"`
	Validators          []ValidatorSpec `json:"validators"`
	Signer              SignerSpec      `json:"signer"`
	Helpers             []HelperSpec    `json:"helpers,omitempty"`
	SourceRevision      string          `json:"source_revision,omitempty"`
	SourceRevisionLabel string          `json:"source_revision_label,omitempty"`
}

type ExecutionSpec struct {
	Name      string `json:"name"`
	UUID      string `json:"uuid,omitempty"`
	Client    string `json:"client"`
	RPCPortID string `json:"rpc_port_id"`
	WSPortID  string `json:"ws_port_id"`
}

type ConsensusSpec struct {
	Name       string `json:"name"`
	UUID       string `json:"uuid,omitempty"`
	Client     string `json:"client"`
	HTTPPortID string `json:"http_port_id"`
}

type ValidatorSpec struct {
	Name          string `json:"name"`
	UUID          string `json:"uuid,omitempty"`
	Client        string `json:"client"`
	MetricsPortID string `json:"metrics_port_id"`
	MetricsPath   string `json:"metrics_path,omitempty"`
}

type SignerSpec struct {
	Name       string `json:"name"`
	UUID       string `json:"uuid,omitempty"`
	Client     string `json:"client"`
	HTTPPortID string `json:"http_port_id"`
}

// HelperSpec models a package service with no endpoint consumed by a protocol
// suite. Persistent helpers are required; explicitly ephemeral helpers may be
// absent after their successful one-shot package task completes.
type HelperSpec struct {
	Name     string `json:"name"`
	UUID     string `json:"uuid,omitempty"`
	Optional bool   `json:"optional,omitempty"`
}

func (helper HelperSpec) optional() bool {
	// Checkpoints created before HelperSpec had an Optional field still contain
	// this package-scoped helper. qrl-package removes it after genesis succeeds,
	// so its exact historical name is also the compatibility marker.
	return helper.Optional || helper.Name == ephemeralGenesisHelperName
}

// DefaultSpec returns the exact topology emitted by the pinned qrl-package
// configuration in scripts/local_testnet/network_params.yaml.
func DefaultSpec(sourceRevision string) Spec {
	return Spec{
		Execution: []ExecutionSpec{
			{Name: "el-1-gqrl-qrysm", Client: "gqrl", RPCPortID: "rpc", WSPortID: "ws"},
			{Name: "el-2-gqrl-qrysm", Client: "gqrl", RPCPortID: "rpc", WSPortID: "ws"},
		},
		Consensus: []ConsensusSpec{
			{Name: "cl-1-qrysm-gqrl", Client: "qrysm", HTTPPortID: "http"},
			{Name: "cl-2-qrysm-gqrl", Client: "qrysm", HTTPPortID: "http"},
		},
		Validators: []ValidatorSpec{
			{Name: "vc-1-gqrl-qrysm", Client: "qrysm", MetricsPortID: "metrics", MetricsPath: "/metrics"},
			{Name: "vc-2-gqrl-qrysm", Client: "qrysm", MetricsPortID: "metrics", MetricsPath: "/metrics"},
		},
		Signer: SignerSpec{Name: "signer-clef", Client: "clef", HTTPPortID: "http"},
		Helpers: []HelperSpec{
			{Name: "clef-keystore-generation-el-clef-keystore"},
			{Name: ephemeralGenesisHelperName, Optional: true},
			{Name: "validator-key-generation-cl-validator-keystore"},
		},
		SourceRevision:      sourceRevision,
		SourceRevisionLabel: DefaultSourceRevisionLabel,
	}
}

// ParseSpec strictly decodes an explicit topology specification. Unlike the
// package-output parser, unknown fields are rejected because a misspelled
// service or port field would silently weaken the discovery contract.
func ParseSpec(data []byte) (Spec, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var spec Spec
	if err := decoder.Decode(&spec); err != nil {
		return Spec{}, fmt.Errorf("decode topology spec: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Spec{}, fmt.Errorf("decode topology spec: %w", err)
	}
	if spec.SourceRevisionLabel == "" {
		spec.SourceRevisionLabel = DefaultSourceRevisionLabel
	}
	if err := spec.Validate(); err != nil {
		return Spec{}, err
	}
	return spec, nil
}

func (spec Spec) Validate() error {
	if len(spec.Execution) != ExpectedExecutionCount {
		return fmt.Errorf("topology spec has %d execution services, want exactly %d", len(spec.Execution), ExpectedExecutionCount)
	}
	if len(spec.Consensus) != ExpectedConsensusCount {
		return fmt.Errorf("topology spec has %d consensus services, want exactly %d", len(spec.Consensus), ExpectedConsensusCount)
	}
	if len(spec.Validators) != ExpectedValidatorCount {
		return fmt.Errorf("topology spec has %d validator services, want exactly %d", len(spec.Validators), ExpectedValidatorCount)
	}
	if spec.SourceRevision != "" && !revisionPattern.MatchString(spec.SourceRevision) {
		return errors.New("topology source revision must be an exact 40-character lowercase commit")
	}
	if spec.SourceRevisionLabel == "" {
		return errors.New("topology source-revision label is empty")
	}

	type identity struct {
		role string
		name string
		uuid string
	}
	identities := make([]identity, 0, len(spec.Execution)+len(spec.Consensus)+len(spec.Validators)+1+len(spec.Helpers))
	for i, node := range spec.Execution {
		role := fmt.Sprintf("execution[%d]", i)
		if err := validateSpecService(role, node.Name, node.UUID, node.Client); err != nil {
			return err
		}
		if node.RPCPortID == "" || node.WSPortID == "" {
			return fmt.Errorf("%s must declare RPC and WS port IDs", role)
		}
		if node.RPCPortID == node.WSPortID {
			return fmt.Errorf("%s reuses port ID %q for RPC and WS", role, node.RPCPortID)
		}
		identities = append(identities, identity{role, node.Name, node.UUID})
	}
	for i, node := range spec.Consensus {
		role := fmt.Sprintf("consensus[%d]", i)
		if err := validateSpecService(role, node.Name, node.UUID, node.Client); err != nil {
			return err
		}
		if node.HTTPPortID == "" {
			return fmt.Errorf("%s must declare an HTTP port ID", role)
		}
		identities = append(identities, identity{role, node.Name, node.UUID})
	}
	for i, node := range spec.Validators {
		role := fmt.Sprintf("validator[%d]", i)
		if err := validateSpecService(role, node.Name, node.UUID, node.Client); err != nil {
			return err
		}
		if node.MetricsPortID == "" {
			return fmt.Errorf("%s must declare a metrics port ID", role)
		}
		if node.MetricsPath != "" && !strings.HasPrefix(node.MetricsPath, "/") {
			return fmt.Errorf("%s metrics path %q is not absolute", role, node.MetricsPath)
		}
		identities = append(identities, identity{role, node.Name, node.UUID})
	}
	if err := validateSpecService("signer", spec.Signer.Name, spec.Signer.UUID, spec.Signer.Client); err != nil {
		return err
	}
	if spec.Signer.HTTPPortID == "" {
		return errors.New("signer must declare an HTTP port ID")
	}
	identities = append(identities, identity{"signer", spec.Signer.Name, spec.Signer.UUID})
	for i, helper := range spec.Helpers {
		role := fmt.Sprintf("helper[%d]", i)
		if err := validateSpecService(role, helper.Name, helper.UUID, "helper"); err != nil {
			return err
		}
		if helper.Optional && helper.Name != ephemeralGenesisHelperName {
			return fmt.Errorf("%s service %q cannot be optional; only %q is an allowlisted one-shot helper", role, helper.Name, ephemeralGenesisHelperName)
		}
		identities = append(identities, identity{role, helper.Name, helper.UUID})
	}

	seenNames := make(map[string]string, len(identities))
	seenUUIDs := make(map[string]string, len(identities))
	for _, item := range identities {
		if previous, ok := seenNames[item.name]; ok {
			return fmt.Errorf("topology service name %q is duplicated by %s and %s", item.name, previous, item.role)
		}
		seenNames[item.name] = item.role
		if item.uuid == "" {
			continue
		}
		if previous, ok := seenUUIDs[item.uuid]; ok {
			return fmt.Errorf("topology service UUID %q is duplicated by %s and %s", item.uuid, previous, item.role)
		}
		seenUUIDs[item.uuid] = item.role
	}
	return nil
}

func validateSpecService(role, name, uuid, client string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s service name is empty", role)
	}
	if strings.TrimSpace(client) == "" {
		return fmt.Errorf("%s client is empty", role)
	}
	if uuid != "" && !serviceUUIDPattern.MatchString(uuid) {
		return fmt.Errorf("%s service UUID %q is not a full 32-character lowercase UUID", role, uuid)
	}
	return nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err == io.EOF {
		return nil
	} else if err != nil {
		return err
	}
	return errors.New("input contains more than one JSON value")
}

func sortedHelperSpecs(helpers []HelperSpec) []HelperSpec {
	result := append([]HelperSpec(nil), helpers...)
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}
