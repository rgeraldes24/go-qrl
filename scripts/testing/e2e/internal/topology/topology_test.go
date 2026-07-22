// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package topology

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

const testRevision = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestDiscoverCrossChecksPackageAndServices(t *testing.T) {
	spec, output, services := validFixture()
	topology, err := Discover(spec, &output, services)
	if err != nil {
		t.Fatal(err)
	}
	if topology.Schema != TopologySchemaVersion || topology.NetworkID != "3151908" || topology.FinalGenesisTimestamp != "1784625026" {
		t.Fatalf("unexpected topology metadata: %+v", topology)
	}
	if len(topology.Execution) != 2 || topology.Execution[0].Service.Name != "el-1" || topology.Execution[1].Service.Name != "el-2" {
		t.Fatalf("execution topology is not canonical: %+v", topology.Execution)
	}
	for _, node := range topology.Execution {
		if node.SourceRevision != testRevision || node.SourceRevisionLabel != "commit" {
			t.Fatalf("source revision was not retained: %+v", node)
		}
	}
	if got := topology.Validators[0].Metrics.PublicURL; got != "http://127.0.0.1:18401/metrics" {
		t.Fatalf("validator metrics URL = %q", got)
	}
	if len(topology.Helpers) != 2 || topology.Helpers[0].Service.Name != "genesis-helper" {
		t.Fatalf("helpers were not retained canonically: %+v", topology.Helpers)
	}

	exact, err := topology.ExactSpec()
	if err != nil {
		t.Fatal(err)
	}
	if exact.Execution[0].UUID == "" || exact.Signer.UUID == "" || exact.Helpers[0].UUID == "" {
		t.Fatalf("exact spec omitted UUIDs: %+v", exact)
	}
	rediscovered, err := Discover(exact, &output, services)
	if err != nil {
		t.Fatalf("discover from exact resume spec: %v", err)
	}
	firstJSON, err := json.Marshal(topology)
	if err != nil {
		t.Fatal(err)
	}
	reversed := append([]kurtosis.Service(nil), services...)
	slices.Reverse(reversed)
	secondTopology, err := Discover(spec, &output, reversed)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(secondTopology)
	if err != nil {
		t.Fatal(err)
	}
	thirdJSON, _ := json.Marshal(rediscovered)
	if string(firstJSON) != string(secondJSON) || string(firstJSON) != string(thirdJSON) {
		t.Fatalf("topology JSON is nondeterministic:\n%s\n%s\n%s", firstJSON, secondJSON, thirdJSON)
	}
	parsedTopology, err := ParseTopology(firstJSON)
	if err != nil {
		t.Fatal(err)
	}
	parsedJSON, _ := json.Marshal(parsedTopology)
	if string(parsedJSON) != string(firstJSON) {
		t.Fatalf("topology JSON round trip changed:\n%s\n%s", firstJSON, parsedJSON)
	}
	wantNames := []string{"cl-1", "cl-2", "el-1", "el-2", "genesis-helper", "key-helper", "signer", "vc-1", "vc-2"}
	if got := topology.ServiceNames(); !slices.Equal(got, wantNames) {
		t.Fatalf("service names = %v, want %v", got, wantNames)
	}
}

func TestDiscoverTreatsCompletedGenesisHelperAsOptionalAcrossCheckpointVersions(t *testing.T) {
	baseSpec, output, baseServices := validFixture()
	tests := []struct {
		name     string
		helper   HelperSpec
		present  bool
		wantSeen bool
	}{
		{name: "legacy checkpoint missing helper", helper: HelperSpec{Name: ephemeralGenesisHelperName}},
		{name: "new checkpoint missing helper", helper: HelperSpec{Name: ephemeralGenesisHelperName, Optional: true}},
		{name: "running helper is retained", helper: HelperSpec{Name: ephemeralGenesisHelperName, Optional: true}, present: true, wantSeen: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := cloneSpec(baseSpec)
			spec.Helpers = append(spec.Helpers, test.helper)
			services := cloneServices(baseServices)
			if test.present {
				services = append(services, testService(ephemeralGenesisHelperName, 10, "10.0.0.10", nil, 0, false))
			}
			discovered, err := Discover(spec, &output, services)
			if err != nil {
				t.Fatal(err)
			}
			seen := false
			for _, helper := range discovered.Helpers {
				seen = seen || helper.Service.Name == ephemeralGenesisHelperName
			}
			if seen != test.wantSeen {
				t.Fatalf("ephemeral genesis helper retained=%t, want %t", seen, test.wantSeen)
			}
		})
	}
}

func TestParsePinnedPackageOutput(t *testing.T) {
	_, output, _ := validFixture()
	raw, err := json.Marshal(struct {
		PackageOutput
		NetworkParams map[string]any `json:"network_params"`
	}{output, map[string]any{"unknown_pinned_field": true}})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParsePackageOutput(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(parsed.Participants) != 2 || parsed.Participants[1].Execution.ServiceName != "el-2" {
		t.Fatalf("unexpected parsed package output: %+v", parsed)
	}

	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"empty", "", "decode qrl-package"},
		{"trailing", string(raw) + `{}`, "more than one JSON value"},
		{"one participant", `{"all_participants":[],"network_id":"1","final_genesis_timestamp":"2"}`, "want exactly 2"},
		{"numeric network malformed", `{"all_participants":[{},{}],"network_id":"abc","final_genesis_timestamp":"2"}`, "not an unsigned integer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParsePackageOutput(test.raw)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParsePackageOutput error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseSpecIsStrictAndDefaultsRevisionLabel(t *testing.T) {
	spec, _, _ := validFixture()
	spec.SourceRevisionLabel = ""
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := ParseSpec(raw)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.SourceRevisionLabel != DefaultSourceRevisionLabel {
		t.Fatalf("revision label = %q", parsed.SourceRevisionLabel)
	}
	withUnknown := strings.TrimSuffix(string(raw), "}") + `,"servce":"typo"}`
	if _, err := ParseSpec([]byte(withUnknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown spec field error = %v", err)
	}
	if _, err := ParseSpec(append(raw, []byte(` {}`)...)); err == nil || !strings.Contains(err.Error(), "more than one JSON value") {
		t.Fatalf("trailing spec error = %v", err)
	}
}

func TestSpecValidationRejectsMissingAndDuplicateRoles(t *testing.T) {
	base, _, _ := validFixture()
	tests := []struct {
		name string
		edit func(*Spec)
		want string
	}{
		{"missing execution", func(spec *Spec) { spec.Execution = spec.Execution[:1] }, "execution services"},
		{"extra consensus", func(spec *Spec) { spec.Consensus = append(spec.Consensus, spec.Consensus[0]) }, "consensus services"},
		{"missing validator", func(spec *Spec) { spec.Validators = nil }, "validator services"},
		{"duplicate name", func(spec *Spec) { spec.Consensus[0].Name = spec.Execution[0].Name }, "service name"},
		{"duplicate UUID", func(spec *Spec) { spec.Execution[0].UUID = id(1); spec.Execution[1].UUID = id(1) }, "service UUID"},
		{"invalid UUID", func(spec *Spec) { spec.Signer.UUID = "short" }, "full 32-character"},
		{"duplicate role port", func(spec *Spec) { spec.Execution[0].WSPortID = spec.Execution[0].RPCPortID }, "reuses port ID"},
		{"missing signer port", func(spec *Spec) { spec.Signer.HTTPPortID = "" }, "HTTP port ID"},
		{"relative metrics path", func(spec *Spec) { spec.Validators[0].MetricsPath = "metrics" }, "not absolute"},
		{"persistent key helper optional", func(spec *Spec) { spec.Helpers[0].Optional = true }, "cannot be optional"},
		{"persistent genesis helper optional", func(spec *Spec) { spec.Helpers[1].Optional = true }, "cannot be optional"},
		{"bad revision", func(spec *Spec) { spec.SourceRevision = "abc" }, "40-character"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := cloneSpec(base)
			test.edit(&spec)
			err := spec.Validate()
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDiscoverRejectsMissingDuplicateAndMismatchedServices(t *testing.T) {
	baseSpec, baseOutput, baseServices := validFixture()
	tests := []struct {
		name       string
		editSpec   func(*Spec)
		editOutput func(*PackageOutput)
		edit       func([]kurtosis.Service) []kurtosis.Service
		want       string
	}{
		{"missing EL", nil, nil, func(services []kurtosis.Service) []kurtosis.Service { return removeService(services, "el-1") }, "required execution service"},
		{"missing helper", nil, nil, func(services []kurtosis.Service) []kurtosis.Service { return removeService(services, "key-helper") }, "required helper service"},
		{"duplicate name", nil, nil, func(services []kurtosis.Service) []kurtosis.Service {
			services[1].Name = services[0].Name
			return services
		}, "duplicate service name"},
		{"duplicate UUID", nil, nil, func(services []kurtosis.Service) []kurtosis.Service {
			services[1].UUID = services[0].UUID
			return services
		}, "duplicate service UUID"},
		{"expected UUID changed", func(spec *Spec) { spec.Execution[0].UUID = id(99) }, nil, nil, "UUID changed"},
		{"missing private RPC", nil, nil, func(services []kurtosis.Service) []kurtosis.Service {
			delete(serviceNamed(services, "el-1").PrivatePorts, "rpc")
			return services
		}, "missing required private port"},
		{"missing public WS", nil, nil, func(services []kurtosis.Service) []kurtosis.Service {
			delete(serviceNamed(services, "el-1").PublicPorts, "ws")
			return services
		}, "missing required public port"},
		{"missing CL HTTP", nil, nil, func(services []kurtosis.Service) []kurtosis.Service {
			delete(serviceNamed(services, "cl-1").PrivatePorts, "http")
			return services
		}, "missing required private port"},
		{"missing VC metrics", nil, nil, func(services []kurtosis.Service) []kurtosis.Service {
			delete(serviceNamed(services, "vc-1").PublicPorts, "metrics")
			return services
		}, "missing required public port"},
		{"missing signer HTTP", nil, nil, func(services []kurtosis.Service) []kurtosis.Service {
			delete(serviceNamed(services, "signer").PrivatePorts, "http")
			return services
		}, "missing required private port"},
		{"port identity mismatch", nil, nil, func(services []kurtosis.Service) []kurtosis.Service {
			p := serviceNamed(services, "el-1").PrivatePorts["rpc"]
			p.ID = "wrong"
			serviceNamed(services, "el-1").PrivatePorts["rpc"] = p
			return services
		}, "contains port ID"},
		{"duplicate node endpoint", nil, nil, func(services []kurtosis.Service) []kurtosis.Service {
			one, two := serviceNamed(services, "el-1"), serviceNamed(services, "el-2")
			p := two.PublicPorts["rpc"]
			p.Number = one.PublicPorts["rpc"].Number
			two.PublicPorts["rpc"] = p
			return services
		}, "public endpoint"},
		{"source revision mismatch", func(spec *Spec) { spec.SourceRevision = strings.Repeat("b", 40) }, nil, nil, "source revision"},
		{"source revision absent from one EL", nil, nil, func(services []kurtosis.Service) []kurtosis.Service {
			delete(serviceNamed(services, "el-2").Labels, "commit")
			return services
		}, "source revision"},
		{"package service mismatch", nil, func(output *PackageOutput) { output.Participants[0].Execution.ServiceName = "el-other" }, nil, "explicit spec requires"},
		{"package private endpoint mismatch", nil, func(output *PackageOutput) {
			output.Participants[0].Execution.RPCURL = "http://10.0.0.1:9999"
			output.Participants[0].Execution.RPCPort = 9999
		}, nil, "differ from Kurtosis"},
		{"package validator endpoint mismatch", nil, func(output *PackageOutput) { output.Participants[0].Validator.Metrics.URL = "10.0.0.9:8080" }, nil, "validator metrics endpoint differs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := cloneSpec(baseSpec)
			output := baseOutput
			output.Participants = append([]PackageParticipant(nil), baseOutput.Participants...)
			services := cloneServices(baseServices)
			if test.editSpec != nil {
				test.editSpec(&spec)
			}
			if test.editOutput != nil {
				test.editOutput(&output)
			}
			if test.edit != nil {
				services = test.edit(services)
			}
			_, err := Discover(spec, &output, services)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Discover error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestDiscoverAllowsBorrowedNetworkWithoutPackageOutput(t *testing.T) {
	spec, _, services := validFixture()
	topology, err := Discover(spec, nil, services)
	if err != nil {
		t.Fatal(err)
	}
	if topology.NetworkID != "" || topology.FinalGenesisTimestamp != "" {
		t.Fatalf("borrowed topology invented package metadata: %+v", topology)
	}
}

func TestRecoverPackageOutputFromExactServices(t *testing.T) {
	spec, want, services := validFixture()
	recovered, err := RecoverPackageOutput(spec, services, want.NetworkID, want.FinalGenesisTimestamp)
	if err != nil {
		t.Fatal(err)
	}
	wantJSON, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	recoveredJSON, err := json.Marshal(recovered)
	if err != nil {
		t.Fatal(err)
	}
	if string(recoveredJSON) != string(wantJSON) {
		t.Fatalf("recovered package output differs:\n got %s\nwant %s", recoveredJSON, wantJSON)
	}
	if _, err := Discover(spec, &recovered, services); err != nil {
		t.Fatalf("recovered output does not cross-check: %v", err)
	}
}

func TestRecoverPackageOutputFailsClosed(t *testing.T) {
	spec, output, services := validFixture()
	tests := []struct {
		name      string
		networkID string
		genesis   string
		edit      func([]kurtosis.Service)
		want      string
	}{
		{name: "invalid network", networkID: "not-a-number", genesis: output.FinalGenesisTimestamp, want: "network_id"},
		{name: "invalid genesis", networkID: output.NetworkID, genesis: "", want: "final_genesis_timestamp"},
		{name: "missing service", networkID: output.NetworkID, genesis: output.FinalGenesisTimestamp, edit: func(items []kurtosis.Service) { items[0].Name = "wrong" }, want: "required execution service"},
		{name: "wrong source", networkID: output.NetworkID, genesis: output.FinalGenesisTimestamp, edit: func(items []kurtosis.Service) { items[0].Labels["commit"] = strings.Repeat("b", 40) }, want: "source revision"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current := cloneServices(services)
			if test.edit != nil {
				test.edit(current)
			}
			_, err := RecoverPackageOutput(spec, current, test.networkID, test.genesis)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("RecoverPackageOutput error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestRefreshAfterRestartReplacesEndpointWithoutMutatingOriginal(t *testing.T) {
	spec, output, services := validFixture()
	topology, err := Discover(spec, &output, services)
	if err != nil {
		t.Fatal(err)
	}
	fake := kurtosis.NewFakeClient()
	ref, err := fake.CreateEnclave(context.Background(), "topology-refresh")
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range services {
		if err := fake.AddService(ref, service); err != nil {
			t.Fatal(err)
		}
	}
	oldURL := topology.Execution[1].RPC.PublicURL
	if err := fake.StopService(context.Background(), ref, "el-2"); err != nil {
		t.Fatal(err)
	}
	if err := fake.StartService(context.Background(), ref, "el-2"); err != nil {
		t.Fatal(err)
	}
	refreshed, err := RefreshAfterRestart(context.Background(), fake, ref, topology, "el-2")
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Execution[1].RPC.PublicURL == oldURL {
		t.Fatalf("RPC endpoint did not refresh: %q", oldURL)
	}
	if topology.Execution[1].RPC.PublicURL != oldURL {
		t.Fatal("RefreshAfterRestart mutated the input topology")
	}
	if refreshed.Execution[1].Service != topology.Execution[1].Service || refreshed.Execution[1].RPC.PrivateURL != topology.Execution[1].RPC.PrivateURL {
		t.Fatalf("restart changed stable identity/private endpoint: %+v", refreshed.Execution[1])
	}
}

func TestRefreshAfterRestartFailsClosed(t *testing.T) {
	spec, output, services := validFixture()
	topology, err := Discover(spec, &output, services)
	if err != nil {
		t.Fatal(err)
	}
	service := *serviceNamed(cloneServices(services), "el-1")
	tests := []struct {
		name    string
		service kurtosis.Service
		target  string
		client  ServiceClient
		want    string
	}{
		{"unknown topology service", service, "other", staticServiceClient{service: service}, "not part of"},
		{"UUID changed", func() kurtosis.Service { s := service; s.UUID = id(99); return s }(), "el-1", staticServiceClient{service: func() kurtosis.Service { s := service; s.UUID = id(99); return s }()}, "identity changed"},
		{"required port disappeared", func() kurtosis.Service { s := cloneService(service); delete(s.PublicPorts, "rpc"); return s }(), "el-1", staticServiceClient{service: func() kurtosis.Service { s := cloneService(service); delete(s.PublicPorts, "rpc"); return s }()}, "missing required public port"},
		{"endpoint collides", func() kurtosis.Service {
			s := cloneService(service)
			other := serviceNamed(services, "el-2")
			p := s.PublicPorts["rpc"]
			p.Number = other.PublicPorts["rpc"].Number
			s.PublicPorts["rpc"] = p
			return s
		}(), "el-1", nil, "public endpoint"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client := test.client
			if client == nil {
				client = staticServiceClient{service: test.service}
			}
			_, err := RefreshAfterRestart(context.Background(), client, lifecycle.EnclaveRef{Name: "e", UUID: id(88)}, topology, test.target)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("RefreshAfterRestart error = %v, want %q", err, test.want)
			}
		})
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := RefreshAfterRestart(cancelled, staticServiceClient{service: service}, lifecycle.EnclaveRef{Name: "e", UUID: id(88)}, topology, "el-1"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled refresh error = %v", err)
	}
}

type staticServiceClient struct {
	service kurtosis.Service
	err     error
}

func (client staticServiceClient) Service(context.Context, lifecycle.EnclaveRef, string) (kurtosis.Service, error) {
	return client.service, client.err
}

func validFixture() (Spec, PackageOutput, []kurtosis.Service) {
	spec := Spec{
		Execution: []ExecutionSpec{
			{Name: "el-1", Client: "gqrl", RPCPortID: "rpc", WSPortID: "ws"},
			{Name: "el-2", Client: "gqrl", RPCPortID: "rpc", WSPortID: "ws"},
		},
		Consensus: []ConsensusSpec{
			{Name: "cl-1", Client: "qrysm", HTTPPortID: "http"},
			{Name: "cl-2", Client: "qrysm", HTTPPortID: "http"},
		},
		Validators: []ValidatorSpec{
			{Name: "vc-1", Client: "qrysm", MetricsPortID: "metrics", MetricsPath: "/metrics"},
			{Name: "vc-2", Client: "qrysm", MetricsPortID: "metrics", MetricsPath: "/metrics"},
		},
		Signer:              SignerSpec{Name: "signer", Client: "clef", HTTPPortID: "http"},
		Helpers:             []HelperSpec{{Name: "key-helper"}, {Name: "genesis-helper"}},
		SourceRevision:      testRevision,
		SourceRevisionLabel: "commit",
	}
	services := []kurtosis.Service{
		testService("el-1", 1, "10.0.0.1", map[string]uint16{"rpc": 8545, "ws": 8546}, 18100, true),
		testService("el-2", 2, "10.0.0.2", map[string]uint16{"rpc": 8545, "ws": 8546}, 18200, true),
		testService("cl-1", 3, "10.0.0.3", map[string]uint16{"http": 3500}, 18300, false),
		testService("cl-2", 4, "10.0.0.4", map[string]uint16{"http": 3500}, 18310, false),
		testService("vc-1", 5, "10.0.0.5", map[string]uint16{"metrics": 8080}, 18401, false),
		testService("vc-2", 6, "10.0.0.6", map[string]uint16{"metrics": 8080}, 18402, false),
		testService("signer", 7, "10.0.0.7", map[string]uint16{"http": 8550}, 18550, false),
		testService("key-helper", 8, "10.0.0.8", nil, 0, false),
		testService("genesis-helper", 9, "10.0.0.9", nil, 0, false),
	}
	output := PackageOutput{NetworkID: "3151908", FinalGenesisTimestamp: "1784625026"}
	for i := 0; i < 2; i++ {
		elIP := "10.0.0." + string(rune('1'+i))
		clIP := "10.0.0." + string(rune('3'+i))
		vcIP := "10.0.0." + string(rune('5'+i))
		output.Participants = append(output.Participants, PackageParticipant{
			ExecutionType:    "gqrl",
			ConsensusType:    "qrysm",
			ValidatorType:    "qrysm",
			RemoteSignerType: "clef",
			Execution:        PackageExecutionContext{ClientName: "gqrl", IP: elIP, RPCPort: 8545, WSPort: 8546, RPCURL: "http://" + elIP + ":8545", WSURL: "ws://" + elIP + ":8546", ServiceName: "el-" + string(rune('1'+i))},
			Consensus:        PackageConsensusContext{ClientName: "qrysm", IP: clIP, HTTPPort: 3500, HTTPURL: "http://" + clIP + ":3500", ServiceName: "cl-" + string(rune('1'+i))},
			Validator:        PackageValidatorContext{ClientName: "qrysm", ServiceName: "vc-" + string(rune('1'+i)), Metrics: PackageMetricsInfo{Name: "vc-" + string(rune('1'+i)), Path: "/metrics", URL: vcIP + ":8080"}},
		})
	}
	return spec, output, services
}

func testService(name string, numericID int, privateIP string, private map[string]uint16, publicBase uint16, source bool) kurtosis.Service {
	service := kurtosis.Service{Name: name, UUID: id(numericID), PrivateIP: privateIP, PublicIP: "127.0.0.1", PrivatePorts: map[string]kurtosis.Port{}, PublicPorts: map[string]kurtosis.Port{}, Labels: map[string]string{}}
	keys := make([]string, 0, len(private))
	for key := range private {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for i, key := range keys {
		service.PrivatePorts[key] = kurtosis.Port{ID: key, Number: private[key], TransportProtocol: "TCP"}
		service.PublicPorts[key] = kurtosis.Port{ID: key, Number: publicBase + uint16(i), TransportProtocol: "TCP"}
	}
	if source {
		service.Labels["commit"] = testRevision
	}
	return service
}

func id(value int) string {
	const hex = "0123456789abcdef"
	result := make([]byte, 32)
	for i := range result {
		result[i] = '0'
	}
	for index := len(result) - 1; value > 0; index-- {
		result[index] = hex[value&15]
		value >>= 4
	}
	return string(result)
}

func cloneSpec(spec Spec) Spec {
	clone := spec
	clone.Execution = append([]ExecutionSpec(nil), spec.Execution...)
	clone.Consensus = append([]ConsensusSpec(nil), spec.Consensus...)
	clone.Validators = append([]ValidatorSpec(nil), spec.Validators...)
	clone.Helpers = append([]HelperSpec(nil), spec.Helpers...)
	return clone
}

func cloneServices(services []kurtosis.Service) []kurtosis.Service {
	result := make([]kurtosis.Service, len(services))
	for i, service := range services {
		result[i] = cloneService(service)
	}
	return result
}

func cloneService(service kurtosis.Service) kurtosis.Service {
	clone := service
	clone.PrivatePorts = make(map[string]kurtosis.Port, len(service.PrivatePorts))
	for key, port := range service.PrivatePorts {
		clone.PrivatePorts[key] = port
	}
	clone.PublicPorts = make(map[string]kurtosis.Port, len(service.PublicPorts))
	for key, port := range service.PublicPorts {
		clone.PublicPorts[key] = port
	}
	clone.Labels = make(map[string]string, len(service.Labels))
	for key, value := range service.Labels {
		clone.Labels[key] = value
	}
	return clone
}

func serviceNamed(services []kurtosis.Service, name string) *kurtosis.Service {
	for i := range services {
		if services[i].Name == name {
			return &services[i]
		}
	}
	panic("missing fixture service " + name)
}

func removeService(services []kurtosis.Service, name string) []kurtosis.Service {
	for i, service := range services {
		if service.Name == name {
			return append(services[:i], services[i+1:]...)
		}
	}
	return services
}
