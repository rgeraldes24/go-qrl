package rpc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

func TestWebSocketCallClosesConnection(t *testing.T) {
	closed := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer func() {
			connection.Close()
			closed <- struct{}{}
		}()
		var call rpcRequest
		if err := connection.ReadJSON(&call); err != nil {
			return
		}
		_ = connection.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": call.ID, "result": "go-qrl/vm64"})
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer server.Close()
	client, err := NewWebSocket("ws"+strings.TrimPrefix(server.URL, "http"), WebSocketOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var version string
	if err := client.Call(context.Background(), "web3_clientVersion", []any{}, &version); err != nil {
		t.Fatal(err)
	}
	if version != "go-qrl/vm64" {
		t.Fatalf("version = %q", version)
	}
	<-closed
}

func TestWebSocketRejectsRPCError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		connection, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(writer, request, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		_, payload, err := connection.ReadMessage()
		if err != nil {
			return
		}
		var call map[string]json.RawMessage
		if err := json.Unmarshal(payload, &call); err != nil {
			return
		}
		_ = connection.WriteJSON(map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(call["id"]), "error": map[string]any{"code": -32601, "message": "missing"}})
	}))
	defer server.Close()
	client, err := NewWebSocket("ws"+strings.TrimPrefix(server.URL, "http"), WebSocketOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Call(context.Background(), "missing", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "-32601") {
		t.Fatalf("RPC error = %v", err)
	}
}
