// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/rpc"
)

const (
	probeChainID = "0x539"
	probeGenesis = "0xaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	probeVersion = "gqrl/e2e-test"
)

type probeScenario struct {
	chainID, genesis, httpVersion, graphQLChainID, webSocketVersion string
	balance                                                         string
	freezeBlocks                                                    bool
}

type probeObservations struct {
	mu              sync.Mutex
	methods         []string
	blockCalls      int
	balanceAddress  string
	graphQLQuery    string
	webSocketMethod string
}

type probeSnapshot struct {
	methods         []string
	blockCalls      int
	balanceAddress  string
	graphQLQuery    string
	webSocketMethod string
}

func (observations *probeObservations) recordMethod(method string) int {
	observations.mu.Lock()
	defer observations.mu.Unlock()
	observations.methods = append(observations.methods, method)
	if method == "qrl_blockNumber" {
		observations.blockCalls++
	}
	return observations.blockCalls
}

func (observations *probeObservations) snapshot() probeSnapshot {
	observations.mu.Lock()
	defer observations.mu.Unlock()
	return probeSnapshot{
		methods: append([]string(nil), observations.methods...), blockCalls: observations.blockCalls,
		balanceAddress: observations.balanceAddress, graphQLQuery: observations.graphQLQuery,
		webSocketMethod: observations.webSocketMethod,
	}
}

func TestProbeNetworkAuthenticatesEveryEndpointAndAdvancingFundedChain(t *testing.T) {
	server, observations := newProbeServer(t, probeScenario{})
	defer server.Close()
	address := "Q" + strings.Repeat("b", 128)
	if len(address) != 129 {
		t.Fatalf("test address length = %d", len(address))
	}

	result, err := probeNetwork(context.Background(), probeRequest{
		RPCURL: server.URL, GraphQLURL: server.URL + "/graphql", WebSocketURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/ws",
		Address: address, ExpectedChainID: probeChainID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ChainID != probeChainID || result.GenesisHash != probeGenesis {
		t.Fatalf("probe result = %+v", result)
	}
	got := observations.snapshot()
	if got.blockCalls < 2 {
		t.Fatalf("block height observations = %d; want at least two", got.blockCalls)
	}
	for _, method := range []string{"web3_clientVersion", "qrl_chainId", "qrl_blockNumber", "qrl_getBalance", "qrl_getBlockByNumber"} {
		if !containsString(got.methods, method) {
			t.Fatalf("HTTP RPC method %q was not observed: %v", method, got.methods)
		}
	}
	if got.balanceAddress != address || !addressPattern.MatchString(got.balanceAddress) {
		t.Fatalf("funded address = %q; want exact 64-byte Q address", got.balanceAddress)
	}
	if got.graphQLQuery != "{ chainID }" {
		t.Fatalf("GraphQL query = %q", got.graphQLQuery)
	}
	if got.webSocketMethod != "web3_clientVersion" {
		t.Fatalf("WebSocket method = %q", got.webSocketMethod)
	}
}

func TestProbeNetworkRejectsIdentityMismatch(t *testing.T) {
	tests := []struct {
		name     string
		scenario probeScenario
		request  probeRequest
		want     string
	}{
		{name: "chain", request: probeRequest{ExpectedChainID: "0x540"}, want: "chain ID"},
		{name: "genesis", request: probeRequest{ExpectedGenesis: "0x" + strings.Repeat("c", 64)}, want: "genesis hash changed"},
		{name: "GraphQL chain", scenario: probeScenario{graphQLChainID: "0x540"}, want: "GraphQL chain ID"},
		{name: "WebSocket version", scenario: probeScenario{webSocketVersion: "different/version"}, want: "WebSocket client identity"},
		{name: "zero balance", scenario: probeScenario{balance: "0x0"}, want: "has no balance"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, _ := newProbeServer(t, test.scenario)
			defer server.Close()
			request := test.request
			request.RPCURL = server.URL
			request.GraphQLURL = server.URL + "/graphql"
			request.WebSocketURL = "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"
			request.Address = "Q" + strings.Repeat("d", 128)
			if request.ExpectedChainID == "" {
				request.ExpectedChainID = probeChainID
			}
			_, err := probeNetwork(context.Background(), request)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("mismatch error = %v; want %q", err, test.want)
			}
		})
	}
}

func TestProbeNetworkNonAdvancingChainHonorsCallerDeadline(t *testing.T) {
	server, observations := newProbeServer(t, probeScenario{freezeBlocks: true})
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := probeNetwork(ctx, probeRequest{
		RPCURL: server.URL, GraphQLURL: server.URL + "/graphql", WebSocketURL: "ws" + strings.TrimPrefix(server.URL, "http") + "/ws",
		Address: "Q" + strings.Repeat("e", 128), ExpectedChainID: probeChainID,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("non-advancing probe error = %v; want caller deadline", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("caller deadline took %s instead of bypassing the %s advancement timeout", elapsed, chainAdvancementWindow)
	}
	if got := observations.snapshot().blockCalls; got != 1 {
		t.Fatalf("non-advancing block observations before caller deadline = %d; want 1", got)
	}
}

func TestChainAdvancementWindowCoversSeveralBlockSlots(t *testing.T) {
	if chainAdvancementWindow != 30*time.Second {
		t.Fatalf("chain advancement window = %s; want 30s", chainAdvancementWindow)
	}
}

type probeWeb3Service struct {
	version      string
	observations *probeObservations
	webSocket    bool
}

func (service *probeWeb3Service) ClientVersion() string {
	if service.webSocket {
		service.observations.mu.Lock()
		service.observations.webSocketMethod = "web3_clientVersion"
		service.observations.mu.Unlock()
	} else {
		service.observations.recordMethod("web3_clientVersion")
	}
	return service.version
}

type probeQRLService struct {
	scenario     probeScenario
	observations *probeObservations
}

func (service *probeQRLService) ChainId() string {
	service.observations.recordMethod("qrl_chainId")
	return service.scenario.chainID
}

func (service *probeQRLService) BlockNumber() string {
	blockCalls := service.observations.recordMethod("qrl_blockNumber")
	if blockCalls == 1 || service.scenario.freezeBlocks {
		return "0x1"
	}
	return "0x2"
}

func (service *probeQRLService) GetBalance(address, _ string) string {
	service.observations.recordMethod("qrl_getBalance")
	service.observations.mu.Lock()
	service.observations.balanceAddress = address
	service.observations.mu.Unlock()
	return service.scenario.balance
}

func (service *probeQRLService) GetBlockByNumber(_ string, _ bool) map[string]string {
	service.observations.recordMethod("qrl_getBlockByNumber")
	return map[string]string{"hash": service.scenario.genesis}
}

func newProbeServer(t *testing.T, scenario probeScenario) (*httptest.Server, *probeObservations) {
	t.Helper()
	if scenario.chainID == "" {
		scenario.chainID = probeChainID
	}
	if scenario.genesis == "" {
		scenario.genesis = probeGenesis
	}
	if scenario.httpVersion == "" {
		scenario.httpVersion = probeVersion
	}
	if scenario.graphQLChainID == "" {
		scenario.graphQLChainID = scenario.chainID
	}
	if scenario.webSocketVersion == "" {
		scenario.webSocketVersion = scenario.httpVersion
	}
	if scenario.balance == "" {
		scenario.balance = "0x1"
	}
	observations := new(probeObservations)
	httpRPC := rpc.NewServer()
	if err := httpRPC.RegisterName("web3", &probeWeb3Service{
		version: scenario.httpVersion, observations: observations,
	}); err != nil {
		t.Fatal(err)
	}
	if err := httpRPC.RegisterName("qrl", &probeQRLService{
		scenario: scenario, observations: observations,
	}); err != nil {
		t.Fatal(err)
	}
	webSocketRPC := rpc.NewServer()
	if err := webSocketRPC.RegisterName("web3", &probeWeb3Service{
		version: scenario.webSocketVersion, observations: observations, webSocket: true,
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(httpRPC.Stop)
	t.Cleanup(webSocketRPC.Stop)

	mux := http.NewServeMux()
	mux.Handle("/graphql", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		observations.mu.Lock()
		observations.graphQLQuery = body.Query
		observations.mu.Unlock()
		writeProbeJSON(writer, map[string]any{"data": map[string]any{"chainID": scenario.graphQLChainID}})
	}))
	mux.Handle("/ws", webSocketRPC.WebsocketHandler([]string{"http://localhost"}))
	mux.Handle("/", httpRPC)
	return httptest.NewServer(mux), observations
}

func writeProbeJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
