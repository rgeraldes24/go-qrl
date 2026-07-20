// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package common

import (
	"encoding/json"
	"strings"
	"testing"
)

func FuzzAddressTextAndJSONRoundTrip(f *testing.F) {
	var upperHalfAddress Address
	for i := range upperHalfAddress {
		upperHalfAddress[i] = byte(i + 1)
	}
	for _, seed := range []string{
		"",
		"Q" + strings.Repeat("00", AddressLength),
		"Q" + strings.Repeat("11", AddressLength-1),
		"Q" + strings.Repeat("22", AddressLength+1),
		"Q" + strings.Repeat("33", 20),
		upperHalfAddress.Hex(),
		"Q" + strings.ToLower(upperHalfAddress.Hex()[1:]),
		strings.ToLower(upperHalfAddress.Hex()),
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, input string) {
		parsed, err := NewAddressFromString(input)
		if IsAddress(input) != (err == nil) {
			t.Fatalf("IsAddress(%q) = %t, parse error = %v", input, IsAddress(input), err)
		}
		if err != nil {
			return
		}

		canonical := parsed.Hex()
		if len(canonical) != 1+2*AddressLength || !IsAddress(canonical) {
			t.Fatalf("invalid canonical address %q", canonical)
		}
		reparsed, err := NewAddressFromString(canonical)
		if err != nil {
			t.Fatalf("reparse canonical address: %v", err)
		}
		if reparsed != parsed {
			t.Fatalf("text round trip changed address: have %x want %x", reparsed, parsed)
		}

		encoded, err := json.Marshal(parsed)
		if err != nil {
			t.Fatalf("marshal address: %v", err)
		}
		var decoded Address
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatalf("unmarshal canonical JSON %s: %v", encoded, err)
		}
		if decoded != parsed {
			t.Fatalf("JSON round trip changed address: have %x want %x", decoded, parsed)
		}
	})
}
