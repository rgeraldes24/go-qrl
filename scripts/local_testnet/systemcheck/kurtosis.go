// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

type commandRunner interface {
	run(context.Context, ...string) (string, error)
}

type execRunner struct{}

func (execRunner) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kurtosis", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("kurtosis %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

type kurtosis struct {
	enclave string
	runner  commandRunner
}

func (k kurtosis) endpoint(ctx context.Context, service, portID, scheme string) (string, error) {
	out, err := k.runner.run(ctx, "port", "print", k.enclave, service, portID, "--format", "ip,number")
	if err != nil {
		return "", err
	}
	return parsePortOutput(out, scheme)
}

func (k kurtosis) stop(ctx context.Context, service string) error {
	_, err := k.runner.run(ctx, "service", "stop", k.enclave, service)
	return err
}

func (k kurtosis) start(ctx context.Context, service string) error {
	_, err := k.runner.run(ctx, "service", "start", k.enclave, service)
	return err
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
	if err != nil {
		return "", fmt.Errorf("invalid Kurtosis port output %q: %w", endpoint, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid Kurtosis port output %q", endpoint)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (cfg *config) resolveEndpoints(ctx context.Context, k kurtosis) error {
	for i := range cfg.rpcURLs {
		if cfg.rpcURLs[i] == "" {
			endpoint, err := k.endpoint(ctx, cfg.elServices[i], "rpc", "http")
			if err != nil {
				return fmt.Errorf("resolve %s RPC: %w", cfg.elServices[i], err)
			}
			cfg.rpcURLs[i] = endpoint
		}
		if cfg.clURLs[i] == "" {
			endpoint, err := k.endpoint(ctx, cfg.clServices[i], "http", "http")
			if err != nil {
				return fmt.Errorf("resolve %s HTTP: %w", cfg.clServices[i], err)
			}
			cfg.clURLs[i] = endpoint
		}
		if cfg.vcMetricsURLs[i] == "" {
			endpoint, err := k.endpoint(ctx, cfg.vcServices[i], "metrics", "http")
			if err != nil {
				return fmt.Errorf("resolve %s metrics: %w", cfg.vcServices[i], err)
			}
			cfg.vcMetricsURLs[i] = endpoint + "/metrics"
		}
	}
	if cfg.signerURL == "" {
		endpoint, err := k.endpoint(ctx, cfg.signerSvc, "http", "http")
		if err != nil {
			return fmt.Errorf("resolve %s HTTP: %w", cfg.signerSvc, err)
		}
		cfg.signerURL = endpoint
	}
	return nil
}
