// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package rpc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestJSONRPCCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var got rpcRequest
		if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
			t.Error(err)
			return
		}
		if got.JSONRPC != "2.0" || got.Method != "qrl_blockNumber" || got.ID != 1 {
			t.Errorf("request = %+v", got)
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":"0x40"}`, got.ID)
	}))
	defer server.Close()
	client, err := New(server.URL, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var block string
	if err := client.Call(t.Context(), "qrl_blockNumber", nil, &block); err != nil {
		t.Fatal(err)
	}
	if block != "0x40" {
		t.Fatalf("block = %q, want 0x40", block)
	}
}

func TestJSONRPCProtocolErrors(t *testing.T) {
	for _, test := range []struct {
		name     string
		response string
		check    func(error) bool
	}{
		{name: "server error", response: `{"jsonrpc":"2.0","id":1,"error":{"code":-32000,"message":"denied"}}`, check: func(err error) bool {
			var rpcError *RPCError
			return errors.As(err, &rpcError) && rpcError.Code == -32000
		}},
		{name: "wrong version", response: `{"jsonrpc":"1.0","id":1,"result":true}`, check: func(err error) bool { return err != nil }},
		{name: "wrong ID", response: `{"jsonrpc":"2.0","id":99,"result":true}`, check: func(err error) bool { return err != nil }},
		{name: "missing result", response: `{"jsonrpc":"2.0","id":1}`, check: func(err error) bool { return err != nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(test.response))
			}))
			defer server.Close()
			client, err := New(server.URL, HTTPOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if err := client.Call(t.Context(), "test", nil, nil); !test.check(err) {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestWaitForTransactionReceiptRetriesOnlyReads(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		call := requests.Add(1)
		var got rpcRequest
		if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
			t.Error(err)
			return
		}
		if got.Method != DefaultReceiptMethod {
			t.Errorf("method = %q", got.Method)
		}
		if call == 1 {
			http.Error(w, "warming up", http.StatusServiceUnavailable)
			return
		}
		result := "null"
		if call >= 3 {
			result = `{"transactionHash":"0xabc","status":"0x1"}`
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%d,"result":%s}`, got.ID, result)
	}))
	defer server.Close()
	client, err := New(server.URL, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var receipt struct {
		TransactionHash string `json:"transactionHash"`
		Status          string `json:"status"`
	}
	if err := client.WaitForTransactionReceipt(ctx, "0xabc", time.Millisecond, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.TransactionHash != "0xabc" || receipt.Status != "0x1" || requests.Load() != 3 {
		t.Fatalf("receipt = %+v, requests = %d", receipt, requests.Load())
	}
}
