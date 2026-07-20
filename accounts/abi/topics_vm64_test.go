// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package abi

import (
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto"
)

func TestParseTopicsCompositeHashVM64(t *testing.T) {
	t.Parallel()

	stringType, err := NewType("string", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := Arguments{{Name: "value", Type: stringType, Indexed: true}}
	want := crypto.Keccak256Hash([]byte("vm64 indexed topic"))
	topic := common.HashToLogTopic(want)

	out := new(struct{ Value common.Hash })
	if err := ParseTopics(out, fields, []common.LogTopic{topic}); err != nil {
		t.Fatalf("parse canonical indexed hash: %v", err)
	}
	if out.Value != want {
		t.Fatalf("decoded hash = %s, want %s", out.Value, want)
	}

	nonCanonical := topic
	nonCanonical[common.HashLength] = 1
	if err := ParseTopics(out, fields, []common.LogTopic{nonCanonical}); err == nil || !strings.Contains(err.Error(), "improperly encoded") {
		t.Fatalf("non-canonical indexed hash error = %v, want encoding error", err)
	}
}

func TestParseTopicsDestinationMismatchReturnsError(t *testing.T) {
	t.Parallel()

	stringType, err := NewType("string", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	fields := Arguments{{Name: "value", Type: stringType, Indexed: true}}
	topic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("vm64 indexed topic")))
	out := new(struct{ Value common.LogTopic })

	if err := ParseTopics(out, fields, []common.LogTopic{topic}); err == nil || !strings.Contains(err.Error(), "cannot unmarshal indexed string") {
		t.Fatalf("destination mismatch error = %v, want typed unmarshal error", err)
	}
}
