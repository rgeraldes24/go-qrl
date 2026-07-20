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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type validatorStatus struct {
	status                string
	executionDepositBlock uint64
}

func fetchValidatorStatus(ctx context.Context, client *http.Client, endpoint string, publicKey []byte) (validatorStatus, error) {
	query := url.Values{"public_key": {base64.StdEncoding.EncodeToString(publicKey)}}
	requestURL := strings.TrimRight(endpoint, "/") + "/qrl/v1alpha1/validator/status?" + query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return validatorStatus{}, fmt.Errorf("build validator-status request: %w", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return validatorStatus{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return validatorStatus{}, fmt.Errorf("read validator-status response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return validatorStatus{}, fmt.Errorf("validator-status HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return parseValidatorStatus(body)
}

func parseValidatorStatus(body []byte) (validatorStatus, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return validatorStatus{}, fmt.Errorf("decode validator-status JSON: %w", err)
	}
	statusRaw, ok := fields["status"]
	if !ok {
		return validatorStatus{}, fmt.Errorf("validator-status response has no status: %s", strings.TrimSpace(string(body)))
	}
	status, err := parseStatus(statusRaw)
	if err != nil {
		return validatorStatus{}, err
	}
	blockRaw, ok := fields["executionDepositBlockNumber"]
	if !ok {
		blockRaw = fields["execution_deposit_block_number"]
	}
	var block uint64
	if len(blockRaw) != 0 && string(blockRaw) != "null" {
		if block, err = parseJSONUint64(blockRaw); err != nil {
			return validatorStatus{}, fmt.Errorf("decode execution deposit block: %w", err)
		}
	}
	return validatorStatus{status: status, executionDepositBlock: block}, nil
}

func parseStatus(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.ToUpper(text)
		if _, ok := validatorStatusNumbers[text]; !ok {
			return "", fmt.Errorf("unknown validator status %q", text)
		}
		return text, nil
	}
	number, err := parseJSONUint64(raw)
	if err != nil {
		return "", fmt.Errorf("decode validator status: %w", err)
	}
	for name, value := range validatorStatusNumbers {
		if value == number {
			return name, nil
		}
	}
	return "", fmt.Errorf("unknown validator status number %d", number)
}

var validatorStatusNumbers = map[string]uint64{
	"UNKNOWN_STATUS":      0,
	"DEPOSITED":           1,
	"PENDING":             2,
	"ACTIVE":              3,
	"EXITING":             4,
	"SLASHING":            5,
	"EXITED":              6,
	"INVALID":             7,
	"PARTIALLY_DEPOSITED": 8,
}

func parseJSONUint64(raw json.RawMessage) (uint64, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strconv.ParseUint(text, 10, 64)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err != nil {
		return 0, err
	}
	return strconv.ParseUint(number.String(), 10, 64)
}

func requireUnknownValidator(ctx context.Context, client *http.Client, endpoints [2]string, publicKey []byte) error {
	for i, endpoint := range endpoints {
		status, err := fetchValidatorStatus(ctx, client, endpoint, publicKey)
		if err != nil {
			return fmt.Errorf("beacon node %d pre-deposit status: %w", i+1, err)
		}
		if status.status != "UNKNOWN_STATUS" {
			return fmt.Errorf("beacon node %d already knows deterministic validator: status=%s block=%d", i+1, status.status, status.executionDepositBlock)
		}
	}
	return nil
}

func waitForBeaconIngestion(ctx context.Context, client *http.Client, endpoints [2]string, publicKey []byte, depositBlock uint64, poll time.Duration) ([2]validatorStatus, error) {
	seen := [2]bool{}
	last := [2]string{}
	statuses := [2]validatorStatus{}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		for i, endpoint := range endpoints {
			if seen[i] {
				continue
			}
			status, err := fetchValidatorStatus(ctx, client, endpoint, publicKey)
			if err != nil {
				last[i] = err.Error()
				continue
			}
			last[i] = fmt.Sprintf("status=%s block=%d", status.status, status.executionDepositBlock)
			accepted, err := validateIngestedStatus(status, depositBlock)
			if err != nil {
				return statuses, fmt.Errorf("beacon node %d: %w", i+1, err)
			}
			if accepted {
				statuses[i] = status
				seen[i] = true
			}
		}
		if seen[0] && seen[1] {
			// Re-fetch both nodes after the first successful observations. This
			// prevents separately latched, temporarily canonical observations
			// from surviving a short execution-chain reorg.
			stable := true
			for i, endpoint := range endpoints {
				status, err := fetchValidatorStatus(ctx, client, endpoint, publicKey)
				if err != nil {
					last[i] = err.Error()
					seen[i] = false
					stable = false
					continue
				}
				last[i] = fmt.Sprintf("status=%s block=%d", status.status, status.executionDepositBlock)
				accepted, err := validateIngestedStatus(status, depositBlock)
				if err != nil {
					return statuses, fmt.Errorf("beacon node %d confirmation: %w", i+1, err)
				}
				if !accepted {
					seen[i] = false
					stable = false
					continue
				}
				statuses[i] = status
			}
			if stable {
				return statuses, nil
			}
		}
		select {
		case <-ctx.Done():
			return statuses, fmt.Errorf("wait for both beacon nodes to report DEPOSITED or stronger: node1=%q node2=%q: %w", last[0], last[1], ctx.Err())
		case <-ticker.C:
		}
	}
}

func validateIngestedStatus(status validatorStatus, depositBlock uint64) (bool, error) {
	if status.status == "INVALID" || status.status == "PARTIALLY_DEPOSITED" {
		return false, fmt.Errorf("rejected the full validator deposit: status=%s block=%d", status.status, status.executionDepositBlock)
	}
	switch status.status {
	case "DEPOSITED", "PENDING":
		if status.executionDepositBlock != depositBlock {
			return false, fmt.Errorf("reports execution deposit block %d, want %d", status.executionDepositBlock, depositBlock)
		}
		return true, nil
	case "ACTIVE":
		// Qrysm no longer populates ExecutionDepositBlockNumber in the ACTIVE
		// branch. The key was proven UNKNOWN before submission, so ACTIVE is
		// stronger evidence than the cache-level DEPOSITED state.
		return true, nil
	default:
		return false, nil
	}
}
