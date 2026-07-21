// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package rpc

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPGetJSONIsBoundedAndBuildsEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/base/value" || request.URL.Query().Get("id") != "64" {
			t.Errorf("request URL = %s, want /base/value?id=64", request.URL)
		}
		if request.Header.Get("X-E2E") != "vm64" || request.Header.Get("Accept") != "application/json" {
			t.Errorf("request headers = %v", request.Header)
		}
		_, _ = w.Write([]byte(`{"value":64}`))
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL+"/base", HTTPOptions{Headers: http.Header{"X-E2e": {"vm64"}}})
	if err != nil {
		t.Fatal(err)
	}
	var response struct{ Value int }
	if err := client.GetJSON(t.Context(), "value?id=64", &response); err != nil {
		t.Fatal(err)
	}
	if response.Value != 64 {
		t.Fatalf("value = %d, want 64", response.Value)
	}
}

func TestHTTPRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"value":"too large"}`))
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL, HTTPOptions{MaxResponseBytes: 8})
	if err != nil {
		t.Fatal(err)
	}
	err = client.GetJSON(t.Context(), "", &map[string]any{})
	var tooLarge *ResponseTooLargeError
	if !errors.As(err, &tooLarge) || tooLarge.Limit != 8 {
		t.Fatalf("error = %#v, want response-too-large limit 8", err)
	}
}

func TestHTTPPerRequestTimeoutHonorsEarlierCallerDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	client, err := NewHTTP(server.URL, HTTPOptions{Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err = client.GetJSON(ctx, "", &map[string]any{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func TestHTTPStatusErrorAndMalformedJSON(t *testing.T) {
	t.Run("status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "not ready", http.StatusServiceUnavailable)
		}))
		defer server.Close()
		client, err := NewHTTP(server.URL, HTTPOptions{})
		if err != nil {
			t.Fatal(err)
		}
		err = client.GetJSON(t.Context(), "?token=secret", &map[string]any{})
		var status *HTTPStatusError
		if !errors.As(err, &status) || status.StatusCode != http.StatusServiceUnavailable || !strings.Contains(status.Body, "not ready") {
			t.Fatalf("error = %#v", err)
		}
		if strings.Contains(status.URL, "token") {
			t.Fatalf("status URL leaked query: %s", status.URL)
		}
	})

	t.Run("JSON", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{} trailing`))
		}))
		defer server.Close()
		client, err := NewHTTP(server.URL, HTTPOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := client.GetJSON(t.Context(), "", &map[string]any{}); err == nil {
			t.Fatal("malformed JSON succeeded")
		}
	})
}

func TestHTTPRejectsUnsafeEndpointsAndPaths(t *testing.T) {
	for _, endpoint := range []string{"", "ftp://example.invalid", "http://user:password@example.invalid", "http://example.invalid?q=1"} {
		if _, err := NewHTTP(endpoint, HTTPOptions{}); err == nil {
			t.Fatalf("NewHTTP(%q) succeeded", endpoint)
		}
	}
	client, err := NewHTTP("http://example.invalid", HTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := client.GetJSON(t.Context(), "https://other.invalid", nil); err == nil {
		t.Fatal("absolute request URL succeeded")
	}
}
