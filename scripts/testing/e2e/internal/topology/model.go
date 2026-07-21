// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package topology

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"sort"
)

const TopologySchemaVersion = 1

// Topology is the deterministic, serializable discovery result consumed by
// suites and checkpoints. It contains only stable identity and resolved
// endpoints; no Kurtosis SDK context is retained across a service restart.
type Topology struct {
	Schema                int             `json:"schema"`
	NetworkID             string          `json:"network_id,omitempty"`
	FinalGenesisTimestamp string          `json:"final_genesis_timestamp,omitempty"`
	Execution             []ExecutionNode `json:"execution"`
	Consensus             []ConsensusNode `json:"consensus"`
	Validators            []ValidatorNode `json:"validators"`
	Signer                SignerNode      `json:"signer"`
	Helpers               []HelperNode    `json:"helpers,omitempty"`
}

type ServiceIdentity struct {
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

// Endpoint retains the exact Kurtosis port ID used to refresh it after a
// restart, plus both network-internal and host-reachable URLs.
type Endpoint struct {
	PortID     string `json:"port_id"`
	PrivateURL string `json:"private_url"`
	PublicURL  string `json:"public_url"`
}

type ExecutionNode struct {
	Service             ServiceIdentity `json:"service"`
	Client              string          `json:"client"`
	RPC                 Endpoint        `json:"rpc"`
	WS                  Endpoint        `json:"ws"`
	SourceRevision      string          `json:"source_revision,omitempty"`
	SourceRevisionLabel string          `json:"source_revision_label,omitempty"`
}

type ConsensusNode struct {
	Service ServiceIdentity `json:"service"`
	Client  string          `json:"client"`
	HTTP    Endpoint        `json:"http"`
}

type ValidatorNode struct {
	Service     ServiceIdentity `json:"service"`
	Client      string          `json:"client"`
	Metrics     Endpoint        `json:"metrics"`
	MetricsPath string          `json:"metrics_path"`
}

type SignerNode struct {
	Service ServiceIdentity `json:"service"`
	Client  string          `json:"client"`
	HTTP    Endpoint        `json:"http"`
}

type HelperNode struct {
	Service ServiceIdentity `json:"service"`
}

// ParseTopology strictly decodes a persisted discovery result and validates
// every identity, endpoint, and role count before it can be used for resume.
func ParseTopology(data []byte) (Topology, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var topology Topology
	if err := decoder.Decode(&topology); err != nil {
		return Topology{}, fmt.Errorf("decode topology: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return Topology{}, fmt.Errorf("decode topology: %w", err)
	}
	if err := topology.Validate(); err != nil {
		return Topology{}, err
	}
	return topology.canonical(), nil
}

// MarshalJSON canonicalizes every role by exact service name. The resulting
// bytes are stable even if Kurtosis returns services in a different order.
func (topology Topology) MarshalJSON() ([]byte, error) {
	type plain Topology
	canonical := topology.canonical()
	return json.Marshal(plain(canonical))
}

func (topology Topology) canonical() Topology {
	result := topology
	result.Execution = append([]ExecutionNode(nil), topology.Execution...)
	result.Consensus = append([]ConsensusNode(nil), topology.Consensus...)
	result.Validators = append([]ValidatorNode(nil), topology.Validators...)
	result.Helpers = append([]HelperNode(nil), topology.Helpers...)
	sort.Slice(result.Execution, func(i, j int) bool { return result.Execution[i].Service.Name < result.Execution[j].Service.Name })
	sort.Slice(result.Consensus, func(i, j int) bool { return result.Consensus[i].Service.Name < result.Consensus[j].Service.Name })
	sort.Slice(result.Validators, func(i, j int) bool { return result.Validators[i].Service.Name < result.Validators[j].Service.Name })
	sort.Slice(result.Helpers, func(i, j int) bool { return result.Helpers[i].Service.Name < result.Helpers[j].Service.Name })
	return result
}

func (topology Topology) Validate() error {
	if topology.Schema != TopologySchemaVersion {
		return fmt.Errorf("topology schema is %d, want %d", topology.Schema, TopologySchemaVersion)
	}
	if len(topology.Execution) != ExpectedExecutionCount || len(topology.Consensus) != ExpectedConsensusCount || len(topology.Validators) != ExpectedValidatorCount {
		return fmt.Errorf("topology role counts are EL=%d CL=%d VC=%d, want exactly %d/%d/%d", len(topology.Execution), len(topology.Consensus), len(topology.Validators), ExpectedExecutionCount, ExpectedConsensusCount, ExpectedValidatorCount)
	}

	seenNames := make(map[string]string)
	seenUUIDs := make(map[string]string)
	privateEndpoints := make(map[string]string)
	publicEndpoints := make(map[string]string)
	validateIdentity := func(role string, identity ServiceIdentity) error {
		if identity.Name == "" || !serviceUUIDPattern.MatchString(identity.UUID) {
			return fmt.Errorf("%s has invalid service identity %q/%q", role, identity.Name, identity.UUID)
		}
		if previous, ok := seenNames[identity.Name]; ok {
			return fmt.Errorf("service name %q is duplicated by %s and %s", identity.Name, previous, role)
		}
		if previous, ok := seenUUIDs[identity.UUID]; ok {
			return fmt.Errorf("service UUID %q is duplicated by %s and %s", identity.UUID, previous, role)
		}
		seenNames[identity.Name] = role
		seenUUIDs[identity.UUID] = role
		return nil
	}
	validateEndpoint := func(role string, endpoint Endpoint, wantScheme string) error {
		if endpoint.PortID == "" {
			return fmt.Errorf("%s has an empty port ID", role)
		}
		privateAddress, err := validateEndpointURL(endpoint.PrivateURL, wantScheme)
		if err != nil {
			return fmt.Errorf("%s private endpoint: %w", role, err)
		}
		publicAddress, err := validateEndpointURL(endpoint.PublicURL, wantScheme)
		if err != nil {
			return fmt.Errorf("%s public endpoint: %w", role, err)
		}
		if previous, ok := privateEndpoints[privateAddress]; ok {
			return fmt.Errorf("private endpoint %q is duplicated by %s and %s", privateAddress, previous, role)
		}
		if previous, ok := publicEndpoints[publicAddress]; ok {
			return fmt.Errorf("public endpoint %q is duplicated by %s and %s", publicAddress, previous, role)
		}
		privateEndpoints[privateAddress] = role
		publicEndpoints[publicAddress] = role
		return nil
	}

	revisions := make(map[string]struct{})
	revisionPresence := 0
	for i, node := range topology.Execution {
		role := fmt.Sprintf("execution[%d]", i)
		if err := validateIdentity(role, node.Service); err != nil {
			return err
		}
		if node.Client == "" || node.RPC.PortID == node.WS.PortID {
			return fmt.Errorf("%s has an empty client or duplicate RPC/WS port ID", role)
		}
		if err := validateEndpoint(role+" RPC", node.RPC, "http"); err != nil {
			return err
		}
		if err := validateEndpoint(role+" WS", node.WS, "ws"); err != nil {
			return err
		}
		if node.SourceRevision != "" {
			if !revisionPattern.MatchString(node.SourceRevision) || node.SourceRevisionLabel == "" {
				return fmt.Errorf("%s has an invalid source revision or label", role)
			}
			revisionPresence++
			revisions[node.SourceRevision] = struct{}{}
		} else if node.SourceRevisionLabel != "" {
			return fmt.Errorf("%s has a source-revision label without a revision", role)
		}
	}
	if revisionPresence != 0 && revisionPresence != len(topology.Execution) {
		return errors.New("only some execution nodes report a source revision")
	}
	if len(revisions) > 1 {
		return errors.New("execution nodes report different source revisions")
	}
	for i, node := range topology.Consensus {
		role := fmt.Sprintf("consensus[%d]", i)
		if err := validateIdentity(role, node.Service); err != nil {
			return err
		}
		if node.Client == "" {
			return fmt.Errorf("%s has an empty client", role)
		}
		if err := validateEndpoint(role+" HTTP", node.HTTP, "http"); err != nil {
			return err
		}
	}
	for i, node := range topology.Validators {
		role := fmt.Sprintf("validator[%d]", i)
		if err := validateIdentity(role, node.Service); err != nil {
			return err
		}
		if node.Client == "" || node.MetricsPath == "" || node.MetricsPath[0] != '/' {
			return fmt.Errorf("%s has an empty client or invalid metrics path", role)
		}
		if err := validateEndpoint(role+" metrics", node.Metrics, "http"); err != nil {
			return err
		}
	}
	if err := validateIdentity("signer", topology.Signer.Service); err != nil {
		return err
	}
	if topology.Signer.Client == "" {
		return errors.New("signer has an empty client")
	}
	if err := validateEndpoint("signer HTTP", topology.Signer.HTTP, "http"); err != nil {
		return err
	}
	for i, helper := range topology.Helpers {
		if err := validateIdentity(fmt.Sprintf("helper[%d]", i), helper.Service); err != nil {
			return err
		}
	}
	return nil
}

// ExactSpec produces a resume-safe explicit spec containing every discovered
// service UUID and port ID.
func (topology Topology) ExactSpec() (Spec, error) {
	if err := topology.Validate(); err != nil {
		return Spec{}, err
	}
	canonical := topology.canonical()
	spec := Spec{SourceRevisionLabel: DefaultSourceRevisionLabel}
	for _, node := range canonical.Execution {
		spec.Execution = append(spec.Execution, ExecutionSpec{Name: node.Service.Name, UUID: node.Service.UUID, Client: node.Client, RPCPortID: node.RPC.PortID, WSPortID: node.WS.PortID})
		if spec.SourceRevision == "" {
			spec.SourceRevision = node.SourceRevision
			spec.SourceRevisionLabel = node.SourceRevisionLabel
		}
	}
	for _, node := range canonical.Consensus {
		spec.Consensus = append(spec.Consensus, ConsensusSpec{Name: node.Service.Name, UUID: node.Service.UUID, Client: node.Client, HTTPPortID: node.HTTP.PortID})
	}
	for _, node := range canonical.Validators {
		spec.Validators = append(spec.Validators, ValidatorSpec{Name: node.Service.Name, UUID: node.Service.UUID, Client: node.Client, MetricsPortID: node.Metrics.PortID, MetricsPath: node.MetricsPath})
	}
	spec.Signer = SignerSpec{Name: canonical.Signer.Service.Name, UUID: canonical.Signer.Service.UUID, Client: canonical.Signer.Client, HTTPPortID: canonical.Signer.HTTP.PortID}
	for _, helper := range canonical.Helpers {
		spec.Helpers = append(spec.Helpers, HelperSpec{Name: helper.Service.Name, UUID: helper.Service.UUID})
	}
	if spec.SourceRevision == "" {
		spec.SourceRevisionLabel = DefaultSourceRevisionLabel
	}
	return spec, spec.Validate()
}

func validateEndpointURL(raw, wantScheme string) (string, error) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Scheme != wantScheme || parsed.Hostname() == "" || parsed.Port() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("invalid %s endpoint %q", wantScheme, raw)
	}
	// Endpoint uniqueness is a socket property, not a URL-scheme property.
	return parsed.Host, nil
}
