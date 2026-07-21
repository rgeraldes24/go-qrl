// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package goabi

import (
	"strings"
	"testing"
	"time"
)

func TestConfigValidate(t *testing.T) {
	t.Parallel()
	valid := Config{RPCURL: "http://127.0.0.1:8545", SeedHex: "01", BinHex: "60"}
	tests := []struct {
		name string
		cfg  Config
		want string
	}{
		{name: "valid", cfg: valid},
		{name: "missing RPC", cfg: Config{SeedHex: valid.SeedHex, BinHex: valid.BinHex}, want: "RPC URL"},
		{name: "missing seed", cfg: Config{RPCURL: valid.RPCURL, BinHex: valid.BinHex}, want: "wallet seed"},
		{name: "missing bytecode", cfg: Config{RPCURL: valid.RPCURL, SeedHex: valid.SeedHex}, want: "deployment bytecode"},
		{name: "negative timeout", cfg: Config{RPCURL: valid.RPCURL, SeedHex: valid.SeedHex, BinHex: valid.BinHex, Timeout: -time.Second}, want: "timeout"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := test.cfg.Validate()
			if test.want == "" && err != nil {
				t.Fatalf("Validate() error = %v", err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("Validate() error = %v, want substring %q", err, test.want)
			}
		})
	}
}
