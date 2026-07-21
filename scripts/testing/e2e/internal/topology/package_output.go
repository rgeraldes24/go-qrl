// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package topology

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
)

// PackageOutput is the typed subset of the pinned qrl-package 1f31cd03 output
// that carries topology identity. The package also emits genesis and prefunded
// account data; those fields are intentionally ignored here.
type PackageOutput struct {
	Participants          []PackageParticipant `json:"all_participants"`
	NetworkID             string               `json:"network_id"`
	FinalGenesisTimestamp string               `json:"final_genesis_timestamp"`
}

type PackageParticipant struct {
	ExecutionType    string                  `json:"el_type"`
	ConsensusType    string                  `json:"cl_type"`
	ValidatorType    string                  `json:"vc_type"`
	RemoteSignerType string                  `json:"remote_signer_type"`
	Execution        PackageExecutionContext `json:"el_context"`
	Consensus        PackageConsensusContext `json:"cl_context"`
	Validator        PackageValidatorContext `json:"vc_context"`
}

type PackageExecutionContext struct {
	ClientName  string `json:"client_name"`
	IP          string `json:"ip_addr"`
	RPCPort     uint16 `json:"rpc_port_num"`
	WSPort      uint16 `json:"ws_port_num"`
	RPCURL      string `json:"rpc_http_url"`
	WSURL       string `json:"ws_url"`
	ServiceName string `json:"service_name"`
}

type PackageConsensusContext struct {
	ClientName  string `json:"client_name"`
	IP          string `json:"ip_addr"`
	HTTPPort    uint16 `json:"http_port"`
	HTTPURL     string `json:"beacon_http_url"`
	ServiceName string `json:"beacon_service_name"`
}

type PackageValidatorContext struct {
	ClientName  string             `json:"client_name"`
	ServiceName string             `json:"service_name"`
	Metrics     PackageMetricsInfo `json:"metrics_info"`
}

type PackageMetricsInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
	URL  string `json:"url"`
}

func ParsePackageOutput(serialized string) (PackageOutput, error) {
	decoder := json.NewDecoder(strings.NewReader(serialized))
	var output PackageOutput
	if err := decoder.Decode(&output); err != nil {
		return PackageOutput{}, fmt.Errorf("decode qrl-package serialized output: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return PackageOutput{}, fmt.Errorf("decode qrl-package serialized output: %w", err)
	}
	if err := output.Validate(); err != nil {
		return PackageOutput{}, err
	}
	return output, nil
}

func (output PackageOutput) Validate() error {
	if len(output.Participants) != ExpectedExecutionCount {
		return fmt.Errorf("qrl-package output has %d participants, want exactly %d", len(output.Participants), ExpectedExecutionCount)
	}
	if strings.TrimSpace(output.NetworkID) == "" {
		return errors.New("qrl-package output network_id is empty")
	}
	if _, err := strconv.ParseUint(output.NetworkID, 10, 64); err != nil {
		return fmt.Errorf("qrl-package output network_id %q is not an unsigned integer", output.NetworkID)
	}
	if strings.TrimSpace(output.FinalGenesisTimestamp) == "" {
		return errors.New("qrl-package output final_genesis_timestamp is empty")
	}
	if _, err := strconv.ParseUint(output.FinalGenesisTimestamp, 10, 64); err != nil {
		return fmt.Errorf("qrl-package output final_genesis_timestamp %q is not an unsigned integer", output.FinalGenesisTimestamp)
	}
	seen := make(map[string]string, ExpectedExecutionCount*3)
	for i, participant := range output.Participants {
		prefix := fmt.Sprintf("qrl-package participant[%d]", i)
		if participant.ExecutionType == "" || participant.ConsensusType == "" || participant.ValidatorType == "" || participant.RemoteSignerType == "" {
			return fmt.Errorf("%s is missing an EL, CL, VC, or remote-signer type", prefix)
		}
		if participant.Execution.ClientName == "" || participant.Execution.ServiceName == "" || participant.Execution.IP == "" || participant.Execution.RPCPort == 0 || participant.Execution.WSPort == 0 {
			return fmt.Errorf("%s has an incomplete execution context", prefix)
		}
		if participant.ExecutionType != participant.Execution.ClientName {
			return fmt.Errorf("%s execution type %q differs from context client %q", prefix, participant.ExecutionType, participant.Execution.ClientName)
		}
		if err := validatePackageURL(participant.Execution.RPCURL, "http", participant.Execution.IP, participant.Execution.RPCPort, ""); err != nil {
			return fmt.Errorf("%s execution RPC URL: %w", prefix, err)
		}
		if err := validatePackageURL(participant.Execution.WSURL, "ws", participant.Execution.IP, participant.Execution.WSPort, ""); err != nil {
			return fmt.Errorf("%s execution WS URL: %w", prefix, err)
		}
		if participant.Consensus.ClientName == "" || participant.Consensus.ServiceName == "" || participant.Consensus.IP == "" || participant.Consensus.HTTPPort == 0 {
			return fmt.Errorf("%s has an incomplete consensus context", prefix)
		}
		if participant.ConsensusType != participant.Consensus.ClientName {
			return fmt.Errorf("%s consensus type %q differs from context client %q", prefix, participant.ConsensusType, participant.Consensus.ClientName)
		}
		if err := validatePackageURL(participant.Consensus.HTTPURL, "http", participant.Consensus.IP, participant.Consensus.HTTPPort, ""); err != nil {
			return fmt.Errorf("%s consensus HTTP URL: %w", prefix, err)
		}
		metrics := participant.Validator.Metrics
		if participant.Validator.ClientName == "" || participant.Validator.ServiceName == "" || metrics.Name == "" || metrics.URL == "" || metrics.Path == "" {
			return fmt.Errorf("%s has an incomplete validator context", prefix)
		}
		if participant.ValidatorType != participant.Validator.ClientName {
			return fmt.Errorf("%s validator type %q differs from context client %q", prefix, participant.ValidatorType, participant.Validator.ClientName)
		}
		if participant.Validator.ServiceName != metrics.Name {
			return fmt.Errorf("%s validator service %q differs from metrics service %q", prefix, participant.Validator.ServiceName, metrics.Name)
		}
		if !strings.HasPrefix(metrics.Path, "/") {
			return fmt.Errorf("%s validator metrics path %q is not absolute", prefix, metrics.Path)
		}
		for role, name := range map[string]string{
			"execution": participant.Execution.ServiceName,
			"consensus": participant.Consensus.ServiceName,
			"validator": participant.Validator.ServiceName,
		} {
			if previous, ok := seen[name]; ok {
				return fmt.Errorf("qrl-package service name %q is duplicated by %s and %s %s", name, previous, prefix, role)
			}
			seen[name] = prefix + " " + role
		}
	}
	return nil
}

// RecoverPackageOutput reconstructs the topology-bearing subset of the pinned
// package output from Kurtosis's current, exact service models and live network
// metadata. It is used only after a response-loss boundary where Kurtosis
// accepted the journaled package invocation but the terminal serialized output
// could not be persisted locally.
func RecoverPackageOutput(spec Spec, services []kurtosis.Service, networkID, finalGenesisTimestamp string) (PackageOutput, error) {
	if _, err := Discover(spec, nil, services); err != nil {
		return PackageOutput{}, fmt.Errorf("recover package output topology: %w", err)
	}
	byName, err := indexServices(services)
	if err != nil {
		return PackageOutput{}, err
	}
	output := PackageOutput{NetworkID: networkID, FinalGenesisTimestamp: finalGenesisTimestamp}
	for i := 0; i < ExpectedExecutionCount; i++ {
		execution := byName[spec.Execution[i].Name]
		consensus := byName[spec.Consensus[i].Name]
		validator := byName[spec.Validators[i].Name]
		rpcPort := execution.PrivatePorts[spec.Execution[i].RPCPortID]
		wsPort := execution.PrivatePorts[spec.Execution[i].WSPortID]
		httpPort := consensus.PrivatePorts[spec.Consensus[i].HTTPPortID]
		metricsPort := validator.PrivatePorts[spec.Validators[i].MetricsPortID]
		metricsPath := spec.Validators[i].MetricsPath
		if metricsPath == "" {
			metricsPath = "/metrics"
		}
		output.Participants = append(output.Participants, PackageParticipant{
			ExecutionType:    spec.Execution[i].Client,
			ConsensusType:    spec.Consensus[i].Client,
			ValidatorType:    spec.Validators[i].Client,
			RemoteSignerType: spec.Signer.Client,
			Execution: PackageExecutionContext{
				ClientName: spec.Execution[i].Client, IP: execution.PrivateIP,
				RPCPort: rpcPort.Number, WSPort: wsPort.Number,
				RPCURL:      endpointURL("http", execution.PrivateIP, rpcPort.Number, ""),
				WSURL:       endpointURL("ws", execution.PrivateIP, wsPort.Number, ""),
				ServiceName: execution.Name,
			},
			Consensus: PackageConsensusContext{
				ClientName: spec.Consensus[i].Client, IP: consensus.PrivateIP,
				HTTPPort:    httpPort.Number,
				HTTPURL:     endpointURL("http", consensus.PrivateIP, httpPort.Number, ""),
				ServiceName: consensus.Name,
			},
			Validator: PackageValidatorContext{
				ClientName: spec.Validators[i].Client, ServiceName: validator.Name,
				Metrics: PackageMetricsInfo{
					Name: validator.Name, Path: metricsPath,
					URL: net.JoinHostPort(validator.PrivateIP, strconv.Itoa(int(metricsPort.Number))),
				},
			},
		})
	}
	if err := output.Validate(); err != nil {
		return PackageOutput{}, fmt.Errorf("validate recovered package output: %w", err)
	}
	if _, err := Discover(spec, &output, services); err != nil {
		return PackageOutput{}, fmt.Errorf("cross-check recovered package output: %w", err)
	}
	return output, nil
}

func validatePackageURL(raw, scheme, host string, port uint16, path string) error {
	parsed, err := url.Parse(raw)
	if err != nil {
		return err
	}
	if parsed.Scheme != scheme || parsed.Hostname() != host || parsed.Port() != strconv.Itoa(int(port)) || parsed.EscapedPath() != path || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("got %q, want %s://%s:%d%s", raw, scheme, host, port, path)
	}
	return nil
}
