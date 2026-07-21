// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
)

func TestReadPackageNetworkMetadataCrossChecksBothELsAndCLs(t *testing.T) {
	rpcOne := packageMetadataRPCServer(t, "3151908")
	rpcTwo := packageMetadataRPCServer(t, "3151908")
	beaconOne := packageMetadataBeaconServer(t, "1784625026", "0x1234", "0x01020304")
	beaconTwo := packageMetadataBeaconServer(t, "1784625026", "0x1234", "0x01020304")
	discovered := packageMetadataTopology(rpcOne.URL, rpcTwo.URL, beaconOne.URL, beaconTwo.URL)

	metadata, err := readPackageNetworkMetadata(t.Context(), discovered)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.NetworkID != "3151908" || metadata.FinalGenesisTimestamp != "1784625026" || metadata.GenesisValidatorsRoot != "0x1234" || metadata.GenesisForkVersion != "0x01020304" {
		t.Fatalf("package metadata = %+v", metadata)
	}
}

func TestReadPackageNetworkMetadataRejectsNodeDisagreement(t *testing.T) {
	t.Run("network ID", func(t *testing.T) {
		rpcOne := packageMetadataRPCServer(t, "3151908")
		rpcTwo := packageMetadataRPCServer(t, "3151909")
		beacon := packageMetadataBeaconServer(t, "1784625026", "0x1234", "0x01020304")
		_, err := readPackageNetworkMetadata(t.Context(), packageMetadataTopology(rpcOne.URL, rpcTwo.URL, beacon.URL, beacon.URL))
		if err == nil || !strings.Contains(err.Error(), "network IDs differ") {
			t.Fatalf("network disagreement error = %v", err)
		}
	})
	t.Run("genesis", func(t *testing.T) {
		rpc := packageMetadataRPCServer(t, "3151908")
		beaconOne := packageMetadataBeaconServer(t, "1784625026", "0x1234", "0x01020304")
		beaconTwo := packageMetadataBeaconServer(t, "1784625027", "0x5678", "0x01020304")
		_, err := readPackageNetworkMetadata(t.Context(), packageMetadataTopology(rpc.URL, rpc.URL, beaconOne.URL, beaconTwo.URL))
		if err == nil || !strings.Contains(err.Error(), "different genesis metadata") {
			t.Fatalf("genesis disagreement error = %v", err)
		}
	})
}

func packageMetadataTopology(rpcOne, rpcTwo, beaconOne, beaconTwo string) topology.Topology {
	return topology.Topology{
		Execution: []topology.ExecutionNode{
			{RPC: topology.Endpoint{PublicURL: rpcOne}},
			{RPC: topology.Endpoint{PublicURL: rpcTwo}},
		},
		Consensus: []topology.ConsensusNode{
			{HTTP: topology.Endpoint{PublicURL: beaconOne}},
			{HTTP: topology.Endpoint{PublicURL: beaconTwo}},
		},
	}
}

func packageMetadataRPCServer(t *testing.T, networkID string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      json.RawMessage `json:"id"`
			Method  string          `json:"method"`
		}
		if request.Method != http.MethodPost || json.NewDecoder(request.Body).Decode(&body) != nil || body.JSONRPC != "2.0" || body.Method != "net_version" {
			http.Error(writer, "bad JSON-RPC request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"jsonrpc":"2.0","id":%s,"result":%q}`, body.ID, networkID)
	}))
	t.Cleanup(server.Close)
	return server
}

func packageMetadataBeaconServer(t *testing.T, timestamp, root, fork string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/qrl/v1/beacon/genesis" {
			http.Error(writer, "bad beacon request", http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(writer, `{"data":{"genesis_time":%q,"genesis_validators_root":%q,"genesis_fork_version":%q}}`, timestamp, root, fork)
	}))
	t.Cleanup(server.Close)
	return server
}
