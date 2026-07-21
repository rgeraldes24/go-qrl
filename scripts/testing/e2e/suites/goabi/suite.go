// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Package goabi runs Go ABI/qrlclient E2E checks against a VM64 network.
package goabi

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

// Config identifies the endpoint and deterministic fixtures exercised by the
// suite. GraphQLURL and WebSocketURL are optional; all other fields are
// required. A zero Timeout uses the historical 15-minute suite deadline.
type Config struct {
	RPCURL       string
	GraphQLURL   string
	WebSocketURL string
	SeedHex      string
	BinHex       string
	Timeout      time.Duration
}

const defaultTimeout = 15 * time.Minute

// Validate reports configuration errors without connecting to the network.
func (cfg Config) Validate() error {
	if strings.TrimSpace(cfg.RPCURL) == "" {
		return fmt.Errorf("RPC URL is required")
	}
	if strings.TrimSpace(cfg.SeedHex) == "" {
		return fmt.Errorf("wallet seed is required")
	}
	if strings.TrimSpace(cfg.BinHex) == "" {
		return fmt.Errorf("EventEmitter deployment bytecode is required")
	}
	if cfg.Timeout < 0 {
		return fmt.Errorf("timeout must not be negative")
	}
	return nil
}

// Run executes the Go ABI suite in its established order. The order is part of
// the test contract because every state-changing assertion is mined before the
// next one begins, giving retries and diagnostics an unambiguous last action.
func Run(parent context.Context, cfg Config) error {
	return RunWithOptions(parent, cfg, Options{})
}

// RunWithOptions executes the same suite as Run and durably records submitted
// transaction hashes when a recorder is configured.
func RunWithOptions(parent context.Context, cfg Config, options Options) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = defaultTimeout
	}

	// This suite intentionally serializes multiple mined transactions so each
	// assertion has an unambiguous historical state root. Leave enough room for
	// transient missed slots without turning a stalled chain into an endless CI
	// job.
	ctx, cancel := context.WithTimeout(parent, cfg.Timeout)
	defer cancel()
	run, err := newSuiteRun(options)
	if err != nil {
		return err
	}

	w, err := wallet.RestoreFromSeedHex(strings.TrimPrefix(cfg.SeedHex, "0x"))
	if err != nil {
		return fmt.Errorf("restore wallet: %w", err)
	}
	from := common.Address(w.GetAddress())

	client, err := qrlclient.Dial(cfg.RPCURL)
	if err != nil {
		return fmt.Errorf("dial %s: %w", cfg.RPCURL, err)
	}
	defer client.Close()
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("read Go ABI transaction chain ID: %w", err)
	}
	if err := run.setTransactionIdentity(from, chainID); err != nil {
		return err
	}

	if err := checkGoABILayout(from); err != nil {
		return err
	}
	if err := checkLiveEventRoundTrip(ctx, run, client, w, from, cfg.BinHex, cfg.GraphQLURL); err != nil {
		return err
	}
	if err := checkStorageAPIs(ctx, run, cfg.GraphQLURL, client, w, from); err != nil {
		return err
	}
	if err := checkAddressUpperHalfIsolation(ctx, run, client, w, from); err != nil {
		return err
	}
	if err := checkLiveVM64Opcodes(ctx, run, client, w, from); err != nil {
		return err
	}
	if err := checkLivePrecompiles(ctx, client, from); err != nil {
		return err
	}
	if cfg.GraphQLURL != "" {
		if err := checkGraphQLSendRawTransaction(ctx, run, cfg.GraphQLURL, client, w, from); err != nil {
			return err
		}
	}
	if err := checkWebSocketSubscriptions(ctx, run, cfg.WebSocketURL, client, w, from); err != nil {
		return err
	}
	return nil
}
