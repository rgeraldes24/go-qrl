// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package rpc contains bounded HTTP, JSON-RPC, and GraphQL transports used by
// the E2E harness. It deliberately performs no implicit retries.
package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	DefaultRequestTimeout  = 15 * time.Second
	DefaultMaxResponseSize = int64(4 << 20)
	maxErrorBodySize       = 4096
)

// HTTPOptions configures an HTTP transport. Timeout and MaxResponseBytes use
// conservative defaults when zero. Headers are cloned during construction.
type HTTPOptions struct {
	Client           *http.Client
	Timeout          time.Duration
	MaxResponseBytes int64
	Headers          http.Header
}

// HTTP is a bounded JSON-over-HTTP transport.
type HTTP struct {
	base             *url.URL
	client           *http.Client
	timeout          time.Duration
	maxResponseBytes int64
	headers          http.Header
}

// HTTPStatusError describes a non-2xx response. Body is capped and intended
// only for diagnostics.
type HTTPStatusError struct {
	Method     string
	URL        string
	StatusCode int
	Status     string
	Body       string
}

func (e *HTTPStatusError) Error() string {
	if e.Body == "" {
		return fmt.Sprintf("%s %s: HTTP %s", e.Method, e.URL, e.Status)
	}
	return fmt.Sprintf("%s %s: HTTP %s: %s", e.Method, e.URL, e.Status, e.Body)
}

// ResponseTooLargeError reports that a server exceeded the configured limit.
type ResponseTooLargeError struct {
	Limit int64
}

func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("HTTP response exceeds %d bytes", e.Limit)
}

func NewHTTP(baseURL string, options HTTPOptions) (*HTTP, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("parse HTTP endpoint: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, fmt.Errorf("HTTP endpoint scheme must be http or https, got %q", parsed.Scheme)
	}
	if parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("HTTP endpoint must contain a host and no credentials, query, or fragment")
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = DefaultRequestTimeout
	}
	if timeout < 0 {
		return nil, errors.New("HTTP request timeout cannot be negative")
	}
	maxResponseBytes := options.MaxResponseBytes
	if maxResponseBytes == 0 {
		maxResponseBytes = DefaultMaxResponseSize
	}
	if maxResponseBytes < 0 {
		return nil, errors.New("maximum HTTP response size cannot be negative")
	}
	client := options.Client
	if client == nil {
		client = &http.Client{}
	}
	return &HTTP{
		base:             parsed,
		client:           client,
		timeout:          timeout,
		maxResponseBytes: maxResponseBytes,
		headers:          options.Headers.Clone(),
	}, nil
}

// GetJSON performs a bounded GET and decodes exactly one JSON value.
func (c *HTTP) GetJSON(ctx context.Context, requestPath string, out any) error {
	return c.DoJSON(ctx, http.MethodGet, requestPath, nil, out)
}

// PostJSON performs a bounded POST and decodes exactly one JSON value.
func (c *HTTP) PostJSON(ctx context.Context, requestPath string, in, out any) error {
	return c.DoJSON(ctx, http.MethodPost, requestPath, in, out)
}

// DoJSON performs one request. It does not retry, even for idempotent methods.
func (c *HTTP) DoJSON(parent context.Context, method, requestPath string, in, out any) error {
	if c == nil {
		return errors.New("HTTP transport is nil")
	}
	if parent == nil {
		return errors.New("HTTP request context is nil")
	}
	endpoint, err := c.endpoint(requestPath)
	if err != nil {
		return err
	}
	var body io.Reader
	if in != nil {
		encoded, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("encode %s request: %w", method, err)
		}
		body = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(parent, c.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), body)
	if err != nil {
		return fmt.Errorf("build %s request: %w", method, err)
	}
	for key, values := range c.headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, safeURL(endpoint), err)
	}
	defer resp.Body.Close()
	if resp.ContentLength > c.maxResponseBytes {
		return &ResponseTooLargeError{Limit: c.maxResponseBytes}
	}
	payload, err := io.ReadAll(io.LimitReader(resp.Body, c.maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("read %s %s response: %w", method, safeURL(endpoint), err)
	}
	if int64(len(payload)) > c.maxResponseBytes {
		return &ResponseTooLargeError{Limit: c.maxResponseBytes}
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		errorBody := strings.TrimSpace(string(payload))
		if len(errorBody) > maxErrorBodySize {
			errorBody = errorBody[:maxErrorBodySize] + "..."
		}
		return &HTTPStatusError{
			Method: method, URL: safeURL(endpoint), StatusCode: resp.StatusCode,
			Status: resp.Status, Body: errorBody,
		}
	}
	if out == nil || len(bytes.TrimSpace(payload)) == 0 {
		return nil
	}
	if err := json.Unmarshal(payload, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, safeURL(endpoint), err)
	}
	return nil
}

func safeURL(endpoint *url.URL) string {
	clone := *endpoint
	clone.User = nil
	clone.RawQuery = ""
	clone.ForceQuery = false
	return clone.String()
}

func (c *HTTP) endpoint(requestPath string) (*url.URL, error) {
	relative, err := url.Parse(requestPath)
	if err != nil {
		return nil, fmt.Errorf("parse request path: %w", err)
	}
	if relative.IsAbs() || relative.Host != "" || relative.User != nil {
		return nil, errors.New("request path must not override the configured endpoint")
	}
	if relative.Fragment != "" {
		return nil, errors.New("request path must not contain a fragment")
	}
	endpoint := *c.base
	baseEscaped := strings.TrimRight(c.base.EscapedPath(), "/")
	endpoint.Path = strings.TrimRight(endpoint.Path, "/")
	requestPath = strings.TrimLeft(relative.Path, "/")
	if requestPath != "" {
		endpoint.Path += "/" + requestPath
	}
	requestEscaped := strings.TrimLeft(relative.EscapedPath(), "/")
	if requestEscaped != "" {
		endpoint.RawPath = baseEscaped + "/" + requestEscaped
	} else {
		endpoint.RawPath = baseEscaped
	}
	endpoint.RawQuery = relative.RawQuery
	return &endpoint, nil
}
