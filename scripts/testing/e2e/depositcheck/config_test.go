// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
)

func TestParseConfigRequiresGeneratorImage(t *testing.T) {
	if _, err := parseConfig(nil); err == nil || !strings.Contains(err.Error(), "generator-image") {
		t.Fatalf("parseConfig error = %v, want missing generator image", err)
	}
}

func TestParseConfigVM64Defaults(t *testing.T) {
	cfg, err := parseConfig([]string{"-generator-image", "local/generator:test"})
	if err != nil {
		t.Fatal(err)
	}
	if !upperHalfNonzero(cfg.withdrawal) {
		t.Fatal("default withdrawal address does not exercise the upper 32 bytes")
	}
	if got := len(cfg.withdrawal.Bytes()); got != common.AddressLength {
		t.Fatalf("withdrawal address is %d bytes, want %d", got, common.AddressLength)
	}
	if got := len(cfg.depositContract.Bytes()); got != common.AddressLength {
		t.Fatalf("deposit contract address is %d bytes, want %d", got, common.AddressLength)
	}
}

func TestParseConfigRejectsLegacyWidthCoverageAndBadFork(t *testing.T) {
	var lowerHalfOnly common.Address
	lowerHalfOnly[common.AddressLength-1] = 1
	if _, err := parseConfig([]string{
		"-generator-image", "local/generator:test",
		"-withdrawal-address", lowerHalfOnly.Hex(),
	}); err == nil || !strings.Contains(err.Error(), "nonzero upper") {
		t.Fatalf("lower-half-only withdrawal error = %v", err)
	}
	if _, err := parseConfig([]string{
		"-generator-image", "local/generator:test",
		"-fork-version", "0xnothex!",
	}); err == nil || !strings.Contains(err.Error(), "4-byte hex") {
		t.Fatalf("invalid fork-version error = %v", err)
	}
}

func TestConfigRejectsDuplicateServicesAndEndpoints(t *testing.T) {
	for _, args := range [][]string{
		{"-generator-image", "local/generator:test", "-el1-service", "same", "-el2-service", "same"},
		{"-generator-image", "local/generator:test", "-cl1-service", "same", "-cl2-service", "same"},
	} {
		if _, err := parseConfig(args); err == nil || !strings.Contains(err.Error(), "must be distinct") {
			t.Errorf("parseConfig(%q) duplicate-service error = %v", args, err)
		}
	}

	cfg, err := parseConfig([]string{
		"-generator-image", "local/generator:test",
		"-rpc1", "http://127.0.0.1:8545",
		"-rpc2", "HTTP://127.0.0.1:8545/",
		"-cl1", "http://127.0.0.1:3500",
		"-cl2", "http://127.0.0.1:3501",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cfg.resolveEndpoints(t.Context(), nil); err == nil || !strings.Contains(err.Error(), "execution endpoints must be distinct") {
		t.Fatalf("duplicate endpoint error = %v", err)
	}
}

func TestParsePortOutput(t *testing.T) {
	for _, test := range []struct {
		name, input, scheme, want string
	}{
		{name: "host port", input: "127.0.0.1:8545\n", scheme: "http", want: "http://127.0.0.1:8545"},
		{name: "last nonempty", input: "noise\nhttp://127.0.0.1:3500/\n", scheme: "http", want: "http://127.0.0.1:3500"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePortOutput(test.input, test.scheme)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("parsePortOutput = %q, want %q", got, test.want)
			}
		})
	}
	if _, err := parsePortOutput("\n", "http"); err == nil {
		t.Fatal("empty port output unexpectedly accepted")
	}
}
