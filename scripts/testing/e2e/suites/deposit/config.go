// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package deposit

import (
	"encoding/hex"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/common"
)

const (
	defaultDepositContract = "Q42424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242"
	defaultWithdrawal      = "Qa5aedb928f8300de98c66bb4bb66b9bb137e9a17e9d41039d98a664671b7c8a34bf63d49800d4ff8f4fd28aef583920e018988d994651b6f0f5966b1dbe11a8b"
	defaultFundingSeed     = "010000f29f58aff0b00de2844f7e20bd9eeaacc379150043beeb328335817512b29fbb7184da84a092f842b2a06d72a24a5d28"
	defaultForkVersion     = "0x10000038"
)

// Config describes the two execution and beacon endpoints exercised by the
// deposit lifecycle suite. Empty endpoint URLs are resolved from the matching
// Kurtosis service names when Run starts.
type Config struct {
	Enclave        string
	ELServices     [2]string
	CLServices     [2]string
	RPCURLs        [2]string
	CLURLs         [2]string
	GeneratorImage string

	DepositContract common.Address
	Withdrawal      common.Address
	FundingSeed     string
	ForkVersion     string

	Timeout      time.Duration
	PollInterval time.Duration
}

// ParseConfig parses and validates the legacy depositcheck command-line
// arguments. It remains exported so both the compatibility command and the
// lifecycle runner use exactly the same defaults and validation.
func ParseConfig(args []string) (Config, error) {
	var cfg Config
	fs := flag.NewFlagSet("depositcheck", flag.ContinueOnError)
	fs.StringVar(&cfg.Enclave, "enclave", "local-testnet", "Kurtosis enclave name")
	fs.StringVar(&cfg.ELServices[0], "el1-service", "el-1-gqrl-qrysm", "first execution service")
	fs.StringVar(&cfg.ELServices[1], "el2-service", "el-2-gqrl-qrysm", "second execution service")
	fs.StringVar(&cfg.CLServices[0], "cl1-service", "cl-1-qrysm-gqrl", "first beacon service")
	fs.StringVar(&cfg.CLServices[1], "cl2-service", "cl-2-qrysm-gqrl", "second beacon service")
	fs.StringVar(&cfg.RPCURLs[0], "rpc1", "", "first execution HTTP RPC URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.RPCURLs[1], "rpc2", "", "second execution HTTP RPC URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.CLURLs[0], "cl1", "", "first beacon HTTP URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.CLURLs[1], "cl2", "", "second beacon HTTP URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.GeneratorImage, "generator-image", "", "exact local Qrysm genesis-generator image used to create validator deposit data")

	contract := fs.String("deposit-contract", defaultDepositContract, "VM64 deposit contract address")
	withdrawal := fs.String("withdrawal-address", defaultWithdrawal, "64-byte execution withdrawal address")
	fs.StringVar(&cfg.FundingSeed, "funding-seed", defaultFundingSeed, "hex ML-DSA-87 seed for the prefunded transaction sender")
	fs.StringVar(&cfg.ForkVersion, "fork-version", defaultForkVersion, "expected deposit-data fork version")
	fs.DurationVar(&cfg.Timeout, "timeout", 15*time.Minute, "timeout for transaction inclusion and beacon ingestion")
	fs.DurationVar(&cfg.PollInterval, "poll", 2*time.Second, "poll interval")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if cfg.ELServices[0] == cfg.ELServices[1] {
		return Config{}, fmt.Errorf("execution service names must be distinct, got %q twice", cfg.ELServices[0])
	}
	if cfg.CLServices[0] == cfg.CLServices[1] {
		return Config{}, fmt.Errorf("consensus service names must be distinct, got %q twice", cfg.CLServices[0])
	}
	if strings.TrimSpace(cfg.GeneratorImage) == "" {
		return Config{}, fmt.Errorf("-generator-image is required; pass the exact source-pinned image used by the topology")
	}
	var err error
	if cfg.DepositContract, err = common.NewAddressFromString(*contract); err != nil {
		return Config{}, fmt.Errorf("invalid deposit contract address: %w", err)
	}
	if cfg.Withdrawal, err = common.NewAddressFromString(*withdrawal); err != nil {
		return Config{}, fmt.Errorf("invalid withdrawal address: %w", err)
	}
	if !upperHalfNonzero(cfg.Withdrawal) {
		return Config{}, fmt.Errorf("withdrawal address must have a nonzero upper 32-byte half")
	}
	if strings.TrimSpace(cfg.FundingSeed) == "" {
		return Config{}, fmt.Errorf("funding seed is required")
	}
	if len(cfg.ForkVersion) != 10 || !strings.HasPrefix(cfg.ForkVersion, "0x") {
		return Config{}, fmt.Errorf("fork version %q must be 0x-prefixed 4-byte hex", cfg.ForkVersion)
	}
	if _, err := hex.DecodeString(cfg.ForkVersion[2:]); err != nil {
		return Config{}, fmt.Errorf("fork version %q must be 0x-prefixed 4-byte hex: %w", cfg.ForkVersion, err)
	}
	if cfg.Timeout <= 0 {
		return Config{}, fmt.Errorf("timeout must be positive")
	}
	if cfg.PollInterval <= 0 {
		return Config{}, fmt.Errorf("poll interval must be positive")
	}
	for i := range cfg.RPCURLs {
		if cfg.RPCURLs[i] == "" && cfg.Enclave == "" {
			return Config{}, fmt.Errorf("enclave is required when execution endpoints are unresolved")
		}
		if cfg.CLURLs[i] == "" && cfg.Enclave == "" {
			return Config{}, fmt.Errorf("enclave is required when beacon endpoints are unresolved")
		}
	}
	return cfg, nil
}

func upperHalfNonzero(address common.Address) bool {
	for _, value := range address[:common.AddressLength/2] {
		if value != 0 {
			return true
		}
	}
	return false
}
