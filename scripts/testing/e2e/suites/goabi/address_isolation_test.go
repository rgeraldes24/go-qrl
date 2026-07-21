// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package goabi

import (
	"bytes"
	"testing"

	"github.com/theQRL/go-qrl/common"
)

func TestUpperHalfIsolationAddresses(t *testing.T) {
	t.Parallel()

	var from common.Address
	for i := range from {
		from[i] = byte(i*7 + 3)
	}
	first, second := upperHalfIsolationAddresses(from, 42)
	if bytes.Equal(first[:common.AddressLength/2], second[:common.AddressLength/2]) {
		t.Fatal("fixtures have equal upper halves")
	}
	if !bytes.Equal(first[common.AddressLength/2:], second[common.AddressLength/2:]) {
		t.Fatal("fixtures do not share their lower half")
	}

	firstAgain, secondAgain := upperHalfIsolationAddresses(from, 42)
	if firstAgain != first || secondAgain != second {
		t.Fatal("fixture derivation is not deterministic")
	}
	nextFirst, nextSecond := upperHalfIsolationAddresses(from, 43)
	if nextFirst == first || nextSecond == second {
		t.Fatal("advancing the sender nonce did not select fresh fixtures")
	}
}
