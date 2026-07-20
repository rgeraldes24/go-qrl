// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

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

type config struct {
	enclave        string
	elServices     [2]string
	clServices     [2]string
	rpcURLs        [2]string
	clURLs         [2]string
	generatorImage string

	depositContract common.Address
	withdrawal      common.Address
	fundingSeed     string
	forkVersion     string

	timeout      time.Duration
	pollInterval time.Duration
}

func parseConfig(args []string) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("depositcheck", flag.ContinueOnError)
	fs.StringVar(&cfg.enclave, "enclave", "local-testnet", "Kurtosis enclave name")
	fs.StringVar(&cfg.elServices[0], "el1-service", "el-1-gqrl-qrysm", "first execution service")
	fs.StringVar(&cfg.elServices[1], "el2-service", "el-2-gqrl-qrysm", "second execution service")
	fs.StringVar(&cfg.clServices[0], "cl1-service", "cl-1-qrysm-gqrl", "first beacon service")
	fs.StringVar(&cfg.clServices[1], "cl2-service", "cl-2-qrysm-gqrl", "second beacon service")
	fs.StringVar(&cfg.rpcURLs[0], "rpc1", "", "first execution HTTP RPC URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.rpcURLs[1], "rpc2", "", "second execution HTTP RPC URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.clURLs[0], "cl1", "", "first beacon HTTP URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.clURLs[1], "cl2", "", "second beacon HTTP URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.generatorImage, "generator-image", "", "exact local Qrysm genesis-generator image used to create validator deposit data")

	contract := fs.String("deposit-contract", defaultDepositContract, "VM64 deposit contract address")
	withdrawal := fs.String("withdrawal-address", defaultWithdrawal, "64-byte execution withdrawal address")
	fs.StringVar(&cfg.fundingSeed, "funding-seed", defaultFundingSeed, "hex ML-DSA-87 seed for the prefunded transaction sender")
	fs.StringVar(&cfg.forkVersion, "fork-version", defaultForkVersion, "expected deposit-data fork version")
	fs.DurationVar(&cfg.timeout, "timeout", 15*time.Minute, "timeout for transaction inclusion and beacon ingestion")
	fs.DurationVar(&cfg.pollInterval, "poll", 2*time.Second, "poll interval")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if cfg.elServices[0] == cfg.elServices[1] {
		return config{}, fmt.Errorf("execution service names must be distinct, got %q twice", cfg.elServices[0])
	}
	if cfg.clServices[0] == cfg.clServices[1] {
		return config{}, fmt.Errorf("consensus service names must be distinct, got %q twice", cfg.clServices[0])
	}
	if strings.TrimSpace(cfg.generatorImage) == "" {
		return config{}, fmt.Errorf("-generator-image is required; pass the exact source-pinned image used by the topology")
	}
	var err error
	if cfg.depositContract, err = common.NewAddressFromString(*contract); err != nil {
		return config{}, fmt.Errorf("invalid deposit contract address: %w", err)
	}
	if cfg.withdrawal, err = common.NewAddressFromString(*withdrawal); err != nil {
		return config{}, fmt.Errorf("invalid withdrawal address: %w", err)
	}
	if !upperHalfNonzero(cfg.withdrawal) {
		return config{}, fmt.Errorf("withdrawal address must have a nonzero upper 32-byte half")
	}
	if strings.TrimSpace(cfg.fundingSeed) == "" {
		return config{}, fmt.Errorf("funding seed is required")
	}
	if len(cfg.forkVersion) != 10 || !strings.HasPrefix(cfg.forkVersion, "0x") {
		return config{}, fmt.Errorf("fork version %q must be 0x-prefixed 4-byte hex", cfg.forkVersion)
	}
	if _, err := hex.DecodeString(cfg.forkVersion[2:]); err != nil {
		return config{}, fmt.Errorf("fork version %q must be 0x-prefixed 4-byte hex: %w", cfg.forkVersion, err)
	}
	if cfg.timeout <= 0 {
		return config{}, fmt.Errorf("timeout must be positive")
	}
	if cfg.pollInterval <= 0 {
		return config{}, fmt.Errorf("poll interval must be positive")
	}
	for i := range cfg.rpcURLs {
		if cfg.rpcURLs[i] == "" && cfg.enclave == "" {
			return config{}, fmt.Errorf("enclave is required when execution endpoints are unresolved")
		}
		if cfg.clURLs[i] == "" && cfg.enclave == "" {
			return config{}, fmt.Errorf("enclave is required when beacon endpoints are unresolved")
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
