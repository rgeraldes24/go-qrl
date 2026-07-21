// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package rpc

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGraphQLQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		var got struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		if err := json.NewDecoder(request.Body).Decode(&got); err != nil {
			t.Error(err)
			return
		}
		if got.Query != "query Block($number: Long!) { block(number: $number) { hash } }" || got.Variables["number"] != "64" {
			t.Errorf("request = %+v", got)
		}
		_, _ = w.Write([]byte(`{"data":{"block":{"hash":"0xabc"}}}`))
	}))
	defer server.Close()
	client, err := NewGraphQL(server.URL, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Block struct {
			Hash string `json:"hash"`
		} `json:"block"`
	}
	if err := client.Query(t.Context(), "query Block($number: Long!) { block(number: $number) { hash } }", map[string]any{"number": "64"}, &result); err != nil {
		t.Fatal(err)
	}
	if result.Block.Hash != "0xabc" {
		t.Fatalf("hash = %q", result.Block.Hash)
	}
}

func TestGraphQLResponseErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"unknown block","path":["block"]}]}`))
	}))
	defer server.Close()
	client, err := NewGraphQL(server.URL, HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	err = client.Query(t.Context(), "query { block { hash } }", nil, &map[string]any{})
	var responseError *GraphQLResponseError
	if !errors.As(err, &responseError) || len(responseError.Errors) != 1 || responseError.Errors[0].Message != "unknown block" {
		t.Fatalf("error = %#v", err)
	}
}
