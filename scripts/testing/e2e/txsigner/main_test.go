// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
)

func TestRPCTimeoutIsBounded(t *testing.T) {
	t.Parallel()

	ctx, cancel := newRPCContext()
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("RPC context has no deadline")
	}
	remaining := time.Until(deadline)
	if remaining <= 0 || remaining > rpcTimeout {
		t.Fatalf("RPC deadline remaining = %s, want within (0, %s]", remaining, rpcTimeout)
	}
}

func TestParseRecipientVM64Boundaries(t *testing.T) {
	t.Parallel()

	if got, err := parseRecipient(""); err != nil || got != nil {
		t.Fatalf("empty recipient: got %v, err %v", got, err)
	}

	var want common.Address
	for i := range want {
		want[i] = byte(i + 1)
	}
	got, err := parseRecipient(want.Hex())
	if err != nil {
		t.Fatalf("parse full VM64 recipient: %v", err)
	}
	if got == nil || *got != want {
		t.Fatalf("recipient mismatch: got %v, want %v", got, want)
	}

	invalid := []string{
		"Q" + strings.Repeat("11", common.AddressLength-1),
		"Q" + strings.Repeat("11", common.AddressLength+1),
		"0x" + strings.Repeat("11", common.AddressLength),
		"Q" + strings.Repeat("gg", common.AddressLength),
	}
	for _, input := range invalid {
		input := input
		t.Run(input[:2], func(t *testing.T) {
			if got, err := parseRecipient(input); err == nil || got != nil {
				t.Fatalf("parseRecipient(%q) = %v, %v; want rejection", input, got, err)
			}
		})
	}
}

func TestParsePayload(t *testing.T) {
	t.Parallel()

	if got, err := parsePayload(""); err != nil || got != nil {
		t.Fatalf("empty payload: got %x, err %v", got, err)
	}
	got, err := parsePayload("0x00abff")
	if err != nil {
		t.Fatalf("valid payload: %v", err)
	}
	if !bytes.Equal(got, []byte{0, 0xab, 0xff}) {
		t.Fatalf("payload mismatch: %x", got)
	}
	for _, input := range []string{"00", "0x0", "0xzz"} {
		if _, err := parsePayload(input); err == nil {
			t.Fatalf("parsePayload(%q) succeeded, want error", input)
		}
	}
}

func TestParseAmount(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"0", "1", "340282366920938463463374607431768211456"} {
		got, err := parseAmount(input)
		if err != nil {
			t.Fatalf("parseAmount(%q): %v", input, err)
		}
		if got.String() != input {
			t.Fatalf("parseAmount(%q) = %s", input, got)
		}
	}
	for _, input := range []string{"", "-1", "+1", "1.0", "0x1"} {
		if _, err := parseAmount(input); err == nil {
			t.Fatalf("parseAmount(%q) succeeded, want error", input)
		}
	}
}

func TestFormatOutput(t *testing.T) {
	t.Parallel()

	out := []byte(`{"address":"Q00","nonce":0}`)
	if got, err := formatOutput("json", out); err != nil || got != string(out)+"\n" {
		t.Fatalf("JSON output = %q, %v", got, err)
	}
	if got, err := formatOutput("js", out); err != nil || got != "var PARAMS = "+string(out)+";\n" {
		t.Fatalf("JS output = %q, %v", got, err)
	}
	if got, err := formatOutput("yaml", out); err == nil || got != "" {
		t.Fatalf("unknown output = %q, %v; want error", got, err)
	}
}
