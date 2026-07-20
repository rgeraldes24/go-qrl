// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"flag"
	"net"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/p2p/qnode"
	"github.com/urfave/cli/v2"
)

func TestProtocolSuiteCommandsRejectMissingFixtures(t *testing.T) {
	t.Parallel()
	key, err := crypto.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	node := qnode.NewV4(&key.PublicKey, net.ParseIP("127.0.0.1"), 30303, 30303)
	missing := filepath.Join(t.TempDir(), "missing")

	tests := []struct {
		name string
		run  func(*cli.Context) error
	}{
		{name: "qrl", run: rlpxQRLTest},
		{name: "snap", run: rlpxSnapTest},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			flags := flag.NewFlagSet(test.name, flag.ContinueOnError)
			if err := flags.Parse([]string{node.String(), missing + ".rlp", missing + ".json"}); err != nil {
				t.Fatal(err)
			}
			ctx := cli.NewContext(nil, flags, nil)
			if err := test.run(ctx); err == nil || !strings.Contains(err.Error(), "missing.json") {
				t.Fatalf("suite action error = %v, want missing fixture error", err)
			}
		})
	}
}
