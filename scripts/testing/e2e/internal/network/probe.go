// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const chainAdvancementWindow = 30 * time.Second

type probeRequest struct {
	RPCURL, GraphQLURL, WebSocketURL string
	Address, ExpectedChainID         string
	ExpectedGenesis                  string
	Requirements                     Requirements
}

type probeResult struct {
	ChainID, GenesisHash string
}

func probeNetwork(ctx context.Context, request probeRequest) (probeResult, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	var version string
	if err := callRPC(ctx, client, request.RPCURL, "web3_clientVersion", nil, &version); err != nil {
		return probeResult{}, err
	}
	var chainID string
	if err := callRPC(ctx, client, request.RPCURL, "qrl_chainId", nil, &chainID); err != nil {
		return probeResult{}, err
	}
	if !equalQuantity(chainID, request.ExpectedChainID) {
		return probeResult{}, fmt.Errorf("chain ID %q differs from expected %q", chainID, request.ExpectedChainID)
	}
	var blockNumber string
	if err := callRPC(ctx, client, request.RPCURL, "qrl_blockNumber", nil, &blockNumber); err != nil {
		return probeResult{}, err
	}
	if quantity(blockNumber).Sign() < 1 {
		return probeResult{}, fmt.Errorf("chain has not produced a post-genesis block: %q", blockNumber)
	}
	firstBlock := quantity(blockNumber)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	advancementDeadline := time.NewTimer(chainAdvancementWindow)
	defer advancementDeadline.Stop()
	advanced := false
	for !advanced {
		select {
		case <-ctx.Done():
			return probeResult{}, ctx.Err()
		case <-advancementDeadline.C:
			return probeResult{}, fmt.Errorf("chain did not advance beyond block %s within %s", firstBlock.String(), chainAdvancementWindow)
		case <-ticker.C:
			if err := callRPC(ctx, client, request.RPCURL, "qrl_blockNumber", nil, &blockNumber); err != nil {
				return probeResult{}, err
			}
			advanced = quantity(blockNumber).Cmp(firstBlock) > 0
		}
	}
	if request.Requirements.Signer {
		if strings.TrimSpace(request.Address) == "" {
			return probeResult{}, errors.New("signer readiness requires a wallet address")
		}
		var balance string
		if err := callRPC(ctx, client, request.RPCURL, "qrl_getBalance", []any{request.Address, "latest"}, &balance); err != nil {
			return probeResult{}, err
		}
		if quantity(balance).Sign() <= 0 {
			return probeResult{}, fmt.Errorf("E2E wallet %s has no balance", request.Address)
		}
	}
	var genesis struct {
		Hash string `json:"hash"`
	}
	if err := callRPC(ctx, client, request.RPCURL, "qrl_getBlockByNumber", []any{"0x0", false}, &genesis); err != nil {
		return probeResult{}, err
	}
	if genesis.Hash == "" {
		return probeResult{}, errors.New("genesis response has no hash")
	}
	if request.ExpectedGenesis != "" && genesis.Hash != request.ExpectedGenesis {
		return probeResult{}, fmt.Errorf("genesis hash changed: got %q, want %q", genesis.Hash, request.ExpectedGenesis)
	}
	if request.Requirements.GraphQL {
		var graphQL struct {
			Data struct {
				ChainID string `json:"chainID"`
			} `json:"data"`
			Errors []any `json:"errors"`
		}
		if err := postJSON(ctx, client, request.GraphQLURL, map[string]any{"query": "{ chainID }"}, &graphQL); err != nil {
			return probeResult{}, fmt.Errorf("GraphQL readiness: %w", err)
		}
		if len(graphQL.Errors) != 0 || !equalQuantity(graphQL.Data.ChainID, request.ExpectedChainID) {
			return probeResult{}, fmt.Errorf("GraphQL chain ID %q differs from expected %q", graphQL.Data.ChainID, request.ExpectedChainID)
		}
	}
	if request.Requirements.WebSocket {
		if err := probeWebSocket(ctx, request.WebSocketURL, version); err != nil {
			return probeResult{}, err
		}
	}
	return probeResult{ChainID: canonicalQuantity(chainID), GenesisHash: genesis.Hash}, nil
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

func callRPC(ctx context.Context, client *http.Client, url, method string, params []any, destination any) error {
	var response rpcResponse
	if params == nil {
		params = []any{}
	}
	if err := postJSON(ctx, client, url, map[string]any{"jsonrpc": "2.0", "id": 1, "method": method, "params": params}, &response); err != nil {
		return fmt.Errorf("%s: %w", method, err)
	}
	if response.Error != nil {
		return fmt.Errorf("%s returned RPC error %d: %s", method, response.Error.Code, response.Error.Message)
	}
	if len(response.Result) == 0 {
		return fmt.Errorf("%s returned no result", method)
	}
	if err := json.Unmarshal(response.Result, destination); err != nil {
		return fmt.Errorf("decode %s result: %w", method, err)
	}
	return nil
}

func postJSON(ctx context.Context, client *http.Client, url string, value, destination any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("HTTP %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1<<20))
	return decoder.Decode(destination)
}

func probeWebSocket(ctx context.Context, url, expectedVersion string) error {
	dialer := *websocket.DefaultDialer
	dialer.HandshakeTimeout = 5 * time.Second
	connection, response, err := dialer.DialContext(ctx, url, http.Header{"Origin": []string{"http://localhost"}})
	if err != nil {
		if response != nil {
			return fmt.Errorf("WebSocket handshake failed with %s: %w", response.Status, err)
		}
		return fmt.Errorf("WebSocket handshake: %w", err)
	}
	defer connection.Close()
	deadline := time.Now().Add(5 * time.Second)
	_ = connection.SetReadDeadline(deadline)
	_ = connection.SetWriteDeadline(deadline)
	if err := connection.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": 1, "method": "web3_clientVersion", "params": []any{}}); err != nil {
		return err
	}
	var responseBody rpcResponse
	if err := connection.ReadJSON(&responseBody); err != nil {
		return err
	}
	var version string
	if responseBody.Error != nil || json.Unmarshal(responseBody.Result, &version) != nil || version != expectedVersion {
		return errors.New("WebSocket client identity differs from HTTP RPC")
	}
	return nil
}

func quantity(value string) *big.Int {
	base := 10
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "0x") {
		base, value = 16, value[2:]
	}
	parsed, ok := new(big.Int).SetString(value, base)
	if !ok {
		return new(big.Int).SetInt64(-1)
	}
	return parsed
}

func equalQuantity(left, right string) bool {
	parsedLeft, parsedRight := quantity(left), quantity(right)
	return parsedLeft.Sign() >= 0 && parsedRight.Sign() >= 0 && parsedLeft.Cmp(parsedRight) == 0
}
func canonicalQuantity(value string) string {
	parsed := quantity(value)
	if parsed.Sign() < 0 {
		return ""
	}
	return "0x" + parsed.Text(16)
}
