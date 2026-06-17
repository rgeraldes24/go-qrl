// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.
//
// go-qrl is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-qrl is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.

package abi

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
)

func TestVM64ABIUint512Int512Golden(t *testing.T) {
	t.Parallel()

	parsed, err := JSON(strings.NewReader(`[
		{
			"name": "f",
			"type": "function",
			"inputs": [
				{"name": "u", "type": "uint512"},
				{"name": "min", "type": "int512"},
				{"name": "neg", "type": "int512"}
			],
			"outputs": [
				{"name": "u", "type": "uint512"},
				{"name": "min", "type": "int512"},
				{"name": "neg", "type": "int512"}
			]
		}
	]`))
	if err != nil {
		t.Fatalf("parse ABI: %v", err)
	}

	maxUint512 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1))
	minInt512 := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 511))
	negOne := big.NewInt(-1)
	maxWord := strings.Repeat("f", 128)
	minWord := "8" + strings.Repeat("0", 127)
	wantPayload := common.FromHex(maxWord + minWord + maxWord)

	packed, err := parsed.Pack("f", maxUint512, minInt512, negOne)
	if err != nil {
		t.Fatalf("pack uint512/int512: %v", err)
	}
	if !bytes.Equal(packed[4:], wantPayload) {
		t.Fatalf("packed payload mismatch:\ngot  %x\nwant %x", packed[4:], wantPayload)
	}

	values, err := parsed.Methods["f"].Outputs.UnpackValues(wantPayload)
	if err != nil {
		t.Fatalf("unpack uint512/int512: %v", err)
	}
	if got := values[0].(*big.Int); got.Cmp(maxUint512) != 0 {
		t.Fatalf("uint512 unpack mismatch: got %s want %s", got, maxUint512)
	}
	if got := values[1].(*big.Int); got.Cmp(minInt512) != 0 {
		t.Fatalf("int512 min unpack mismatch: got %s want %s", got, minInt512)
	}
	if got := values[2].(*big.Int); got.Cmp(negOne) != 0 {
		t.Fatalf("int512 -1 unpack mismatch: got %s want %s", got, negOne)
	}
}

func TestVM64ABITypedTopicGolden(t *testing.T) {
	t.Parallel()

	uint512Ty, err := NewType("uint512", "", nil)
	if err != nil {
		t.Fatalf("uint512 type: %v", err)
	}
	int512Ty, err := NewType("int512", "", nil)
	if err != nil {
		t.Fatalf("int512 type: %v", err)
	}
	fields := Arguments{
		{Name: "u", Type: uint512Ty, Indexed: true},
		{Name: "i", Type: int512Ty, Indexed: true},
	}

	maxUint512 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1))
	minInt512 := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 511))
	maxWord := strings.Repeat("f", 128)
	minWord := "8" + strings.Repeat("0", 127)
	topics := []common.LogTopic{
		common.BytesToLogTopic(common.FromHex(maxWord)),
		common.BytesToLogTopic(common.FromHex(minWord)),
	}

	out := make(map[string]any)
	if err := ParseTopicsIntoMap(out, fields, topics); err != nil {
		t.Fatalf("parse topics: %v", err)
	}
	if got := out["u"].(*big.Int); got.Cmp(maxUint512) != 0 {
		t.Fatalf("uint512 topic mismatch: got %s want %s", got, maxUint512)
	}
	if got := out["i"].(*big.Int); got.Cmp(minInt512) != 0 {
		t.Fatalf("int512 topic mismatch: got %s want %s", got, minInt512)
	}

	generated, err := MakeTopics([]any{maxUint512}, []any{minInt512})
	if err != nil {
		t.Fatalf("make topics: %v", err)
	}
	if got := generated[0][0]; got != topics[0] {
		t.Fatalf("uint512 generated topic mismatch:\ngot  %x\nwant %x", got, topics[0])
	}
	if got := generated[1][0]; got != topics[1] {
		t.Fatalf("int512 generated topic mismatch:\ngot  %x\nwant %x", got, topics[1])
	}
}
