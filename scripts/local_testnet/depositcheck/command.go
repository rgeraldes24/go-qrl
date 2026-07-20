// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

type commandRunner interface {
	run(context.Context, string, ...string) (string, error)
}

type execRunner struct{}

func (execRunner) run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func (cfg *config) resolveEndpoints(ctx context.Context, runner commandRunner) error {
	for i := range cfg.rpcURLs {
		if cfg.rpcURLs[i] == "" {
			endpoint, err := kurtosisEndpoint(ctx, runner, cfg.enclave, cfg.elServices[i], "rpc", "http")
			if err != nil {
				return fmt.Errorf("resolve %s RPC: %w", cfg.elServices[i], err)
			}
			cfg.rpcURLs[i] = endpoint
		}
		if cfg.clURLs[i] == "" {
			endpoint, err := kurtosisEndpoint(ctx, runner, cfg.enclave, cfg.clServices[i], "http", "http")
			if err != nil {
				return fmt.Errorf("resolve %s HTTP: %w", cfg.clServices[i], err)
			}
			cfg.clURLs[i] = endpoint
		}
	}
	if sameEndpoint(cfg.rpcURLs[0], cfg.rpcURLs[1]) {
		return fmt.Errorf("execution endpoints must be distinct, got %q twice", cfg.rpcURLs[0])
	}
	if sameEndpoint(cfg.clURLs[0], cfg.clURLs[1]) {
		return fmt.Errorf("consensus endpoints must be distinct, got %q twice", cfg.clURLs[0])
	}
	return nil
}

func sameEndpoint(left, right string) bool {
	normalize := func(raw string) string {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil {
			return strings.TrimRight(strings.ToLower(strings.TrimSpace(raw)), "/")
		}
		parsed.Scheme = strings.ToLower(parsed.Scheme)
		parsed.Host = strings.ToLower(parsed.Host)
		parsed.Path = strings.TrimRight(parsed.Path, "/")
		return parsed.String()
	}
	return normalize(left) == normalize(right)
}

func kurtosisEndpoint(ctx context.Context, runner commandRunner, enclave, service, portID, scheme string) (string, error) {
	out, err := runner.run(ctx, "kurtosis", "port", "print", enclave, service, portID, "--format", "ip,number")
	if err != nil {
		return "", err
	}
	return parsePortOutput(out, scheme)
}

func parsePortOutput(output, scheme string) (string, error) {
	var endpoint string
	for _, line := range strings.Split(output, "\n") {
		if value := strings.TrimSpace(line); value != "" {
			endpoint = value
		}
	}
	if endpoint == "" {
		return "", fmt.Errorf("empty Kurtosis port output")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = scheme + "://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid Kurtosis port output %q", endpoint)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
