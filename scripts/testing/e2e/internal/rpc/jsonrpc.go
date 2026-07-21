// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync/atomic"
)

// Client is a bounded JSON-RPC 2.0 client.
type Client struct {
	http   *HTTP
	nextID atomic.Uint64
}

// RPCError is an error object returned by a JSON-RPC server.
type RPCError struct {
	Code    int64           `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("JSON-RPC error %d: %s", e.Code, e.Message)
}

func New(endpoint string, options HTTPOptions) (*Client, error) {
	transport, err := NewHTTP(endpoint, options)
	if err != nil {
		return nil, err
	}
	return NewClient(transport), nil
}

func NewClient(transport *HTTP) *Client {
	client := &Client{http: transport}
	client.nextID.Store(0)
	return client
}

// Call performs one JSON-RPC request. Calls are never automatically retried,
// because the client cannot infer whether a method changes state.
func (c *Client) Call(ctx context.Context, method string, params, result any) error {
	if c == nil || c.http == nil {
		return errors.New("JSON-RPC client is nil")
	}
	if method == "" {
		return errors.New("JSON-RPC method is required")
	}
	id := c.nextID.Add(1)
	request := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	var response rpcResponse
	if err := c.http.PostJSON(ctx, "", request, &response); err != nil {
		return err
	}
	if response.JSONRPC != "2.0" {
		return fmt.Errorf("JSON-RPC response version is %q, want 2.0", response.JSONRPC)
	}
	if string(response.ID) != strconv.FormatUint(id, 10) {
		return fmt.Errorf("JSON-RPC response ID %s does not match request ID %d", response.ID, id)
	}
	if response.Error != nil {
		return response.Error
	}
	if len(response.Result) == 0 {
		return errors.New("JSON-RPC response has neither result nor error")
	}
	if result == nil {
		return nil
	}
	if err := json.Unmarshal(response.Result, result); err != nil {
		return fmt.Errorf("decode JSON-RPC %s result: %w", method, err)
	}
	return nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      uint64 `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result"`
	Error   *RPCError       `json:"error"`
}
