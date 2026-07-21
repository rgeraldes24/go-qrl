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

package freshsync

import (
	"bytes"
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
)

type commandRunner interface {
	run(context.Context, []byte, ...string) (string, error)
}

type execRunner struct{}

func (execRunner) run(ctx context.Context, input []byte, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kurtosis", args...)
	if input != nil {
		cmd.Stdin = bytes.NewReader(input)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("kurtosis %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// cliKurtosis is the deliberately small compatibility boundary retained for
// lossless service-config inspection and cloning. The Kurtosis Go SDK does not
// currently expose every inspected service-config field needed by fresh-sync.
type cliKurtosis struct {
	enclave string
	runner  commandRunner
}

func (k cliKurtosis) inspect(ctx context.Context, service string) (rawServiceConfig, error) {
	out, err := k.runner.run(ctx, nil, "service", "inspect", k.enclave, service, "--output", "json")
	if err != nil {
		return nil, err
	}
	cfg, err := parseServiceConfig(out)
	if err != nil {
		return nil, fmt.Errorf("parse inspected config for %s: %w", service, err)
	}
	return cfg, nil
}

func (k cliKurtosis) add(ctx context.Context, service string, cfg rawServiceConfig) error {
	encoded, err := cfg.marshal()
	if err != nil {
		return fmt.Errorf("encode config for %s: %w", service, err)
	}
	_, err = k.runner.run(ctx, encoded, "service", "add", k.enclave, service, "--json-service-config", "-")
	return err
}

func (k cliKurtosis) remove(ctx context.Context, service string) error {
	_, err := k.runner.run(ctx, nil, "service", "rm", k.enclave, service)
	return err
}

func (k cliKurtosis) logs(ctx context.Context, service string) (string, error) {
	return k.runner.run(ctx, nil, "service", "logs", k.enclave, service, "--all")
}

func (k cliKurtosis) endpoint(ctx context.Context, service, portID, scheme string) (string, error) {
	out, err := k.runner.run(ctx, nil, "port", "print", k.enclave, service, portID, "--format", "ip,number")
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
	if err != nil {
		return "", fmt.Errorf("invalid Kurtosis port output %q: %w", endpoint, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid Kurtosis port output %q", endpoint)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}
