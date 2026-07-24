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

	"github.com/theQRL/go-qrl/rpc"
)

const (
	chainAdvancementWindow = 30 * time.Second
	probeRequestTimeout    = 5 * time.Second
)

type probeRequest struct {
	RPCURL, GraphQLURL, WebSocketURL string
	Address, ExpectedChainID         string
	ExpectedGenesis                  string
}

type probeResult struct {
	ChainID, GenesisHash string
}

func probeNetwork(ctx context.Context, request probeRequest) (probeResult, error) {
	httpClient := &http.Client{Timeout: probeRequestTimeout}
	client, err := rpc.DialOptions(ctx, request.RPCURL, rpc.WithHTTPClient(httpClient))
	if err != nil {
		return probeResult{}, fmt.Errorf("dial HTTP RPC: %w", err)
	}
	defer client.Close()
	var version string
	if err := callRPC(ctx, client, "web3_clientVersion", &version); err != nil {
		return probeResult{}, err
	}
	var chainID string
	if err := callRPC(ctx, client, "qrl_chainId", &chainID); err != nil {
		return probeResult{}, err
	}
	if !equalQuantity(chainID, request.ExpectedChainID) {
		return probeResult{}, fmt.Errorf("chain ID %q differs from expected %q", chainID, request.ExpectedChainID)
	}
	var blockNumber string
	if err := callRPC(ctx, client, "qrl_blockNumber", &blockNumber); err != nil {
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
			if err := callRPC(ctx, client, "qrl_blockNumber", &blockNumber); err != nil {
				return probeResult{}, err
			}
			advanced = quantity(blockNumber).Cmp(firstBlock) > 0
		}
	}
	if strings.TrimSpace(request.Address) == "" {
		return probeResult{}, errors.New("signer readiness requires a wallet address")
	}
	var balance string
	if err := callRPC(ctx, client, "qrl_getBalance", &balance, request.Address, "latest"); err != nil {
		return probeResult{}, err
	}
	if quantity(balance).Sign() <= 0 {
		return probeResult{}, fmt.Errorf("E2E wallet %s has no balance", request.Address)
	}
	var genesis struct {
		Hash string `json:"hash"`
	}
	if err := callRPC(ctx, client, "qrl_getBlockByNumber", &genesis, "0x0", false); err != nil {
		return probeResult{}, err
	}
	if genesis.Hash == "" {
		return probeResult{}, errors.New("genesis response has no hash")
	}
	if request.ExpectedGenesis != "" && genesis.Hash != request.ExpectedGenesis {
		return probeResult{}, fmt.Errorf("genesis hash changed: got %q, want %q", genesis.Hash, request.ExpectedGenesis)
	}
	var graphQL struct {
		Data struct {
			ChainID string `json:"chainID"`
		} `json:"data"`
		Errors []any `json:"errors"`
	}
	if err := postJSON(ctx, httpClient, request.GraphQLURL, map[string]any{"query": "{ chainID }"}, &graphQL); err != nil {
		return probeResult{}, fmt.Errorf("GraphQL readiness: %w", err)
	}
	if len(graphQL.Errors) != 0 || !equalQuantity(graphQL.Data.ChainID, request.ExpectedChainID) {
		return probeResult{}, fmt.Errorf("GraphQL chain ID %q differs from expected %q", graphQL.Data.ChainID, request.ExpectedChainID)
	}
	if err := probeWebSocket(ctx, request.WebSocketURL, version); err != nil {
		return probeResult{}, err
	}
	return probeResult{ChainID: canonicalQuantity(chainID), GenesisHash: genesis.Hash}, nil
}

func callRPC(ctx context.Context, client *rpc.Client, method string, destination any, arguments ...any) error {
	if err := client.CallContext(ctx, destination, method, arguments...); err != nil {
		return fmt.Errorf("%s: %w", method, err)
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
	probeCtx, cancel := context.WithTimeout(ctx, probeRequestTimeout)
	defer cancel()
	client, err := rpc.DialOptions(
		probeCtx,
		url,
		rpc.WithHeader("Origin", "http://localhost"),
	)
	if err != nil {
		return fmt.Errorf("WebSocket handshake: %w", err)
	}
	defer client.Close()
	var version string
	if err := callRPC(probeCtx, client, "web3_clientVersion", &version); err != nil {
		return fmt.Errorf("WebSocket client identity: %w", err)
	}
	if version != expectedVersion {
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
