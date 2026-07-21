// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

// WebSocket performs bounded one-shot JSON-RPC exchanges. A connection is
// always closed before Call returns, including cancellation and parse errors.
type WebSocket struct {
	endpoint *url.URL
	dialer   *websocket.Dialer
	timeout  time.Duration
	maxBytes int64
	nextID   atomic.Uint64
}

type WebSocketOptions struct {
	Dialer           *websocket.Dialer
	Timeout          time.Duration
	MaxResponseBytes int64
}

func NewWebSocket(endpoint string, options WebSocketOptions) (*WebSocket, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("parse WebSocket endpoint: %w", err)
	}
	if (parsed.Scheme != "ws" && parsed.Scheme != "wss") || parsed.Host == "" || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("WebSocket endpoint must use ws or wss and contain no credentials or fragment")
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = DefaultRequestTimeout
	}
	if timeout < 0 {
		return nil, errors.New("WebSocket timeout cannot be negative")
	}
	maximum := options.MaxResponseBytes
	if maximum == 0 {
		maximum = DefaultMaxResponseSize
	}
	if maximum < 0 {
		return nil, errors.New("WebSocket maximum response size cannot be negative")
	}
	dialer := options.Dialer
	if dialer == nil {
		clone := *websocket.DefaultDialer
		dialer = &clone
	}
	return &WebSocket{endpoint: parsed, dialer: dialer, timeout: timeout, maxBytes: maximum}, nil
}

func (client *WebSocket) Call(parent context.Context, method string, params, result any) error {
	if client == nil || client.endpoint == nil {
		return errors.New("WebSocket client is nil")
	}
	if parent == nil || method == "" {
		return errors.New("WebSocket call requires context and method")
	}
	ctx, cancel := context.WithTimeout(parent, client.timeout)
	defer cancel()
	connection, response, err := client.dialer.DialContext(ctx, client.endpoint.String(), http.Header{})
	if err != nil {
		if response != nil && response.Body != nil {
			response.Body.Close()
		}
		return fmt.Errorf("dial WebSocket endpoint: %w", err)
	}
	defer connection.Close()
	connection.SetReadLimit(client.maxBytes)
	if deadline, ok := ctx.Deadline(); ok {
		if err := connection.SetWriteDeadline(deadline); err != nil {
			return err
		}
		if err := connection.SetReadDeadline(deadline); err != nil {
			return err
		}
	}
	id := client.nextID.Add(1)
	request := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := connection.WriteJSON(request); err != nil {
		return fmt.Errorf("write WebSocket JSON-RPC request: %w", err)
	}
	var reply rpcResponse
	if err := connection.ReadJSON(&reply); err != nil {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return fmt.Errorf("read WebSocket JSON-RPC response: %w", err)
	}
	if reply.JSONRPC != "2.0" || string(reply.ID) != fmt.Sprint(id) {
		return fmt.Errorf("WebSocket JSON-RPC envelope does not match request %d", id)
	}
	if reply.Error != nil {
		return reply.Error
	}
	if len(reply.Result) == 0 {
		return errors.New("WebSocket JSON-RPC response has neither result nor error")
	}
	if result != nil {
		if err := json.Unmarshal(reply.Result, result); err != nil {
			return fmt.Errorf("decode WebSocket JSON-RPC %s result: %w", method, err)
		}
	}
	return nil
}
