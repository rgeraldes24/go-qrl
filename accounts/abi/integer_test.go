// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.

package abi

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
)

func TestABIIntegerTypeSizes(t *testing.T) {
	t.Parallel()

	valid := []string{"uint8", "int8", "uint72", "int72", "uint256", "int256", "uint512", "int512"}
	for _, typ := range valid {
		if _, err := NewType(typ, "", nil); err != nil {
			t.Fatalf("NewType(%q): %v", typ, err)
		}
	}
	invalid := []string{"uint0", "int0", "uint7", "int7", "uint513", "int513", "uint520", "int520"}
	for _, typ := range invalid {
		if _, err := NewType(typ, "", nil); err == nil {
			t.Fatalf("NewType(%q) succeeded, want error", typ)
		}
	}
}

func TestABIIntegerPackDeclaredWidths(t *testing.T) {
	t.Parallel()

	parsed, err := JSON(strings.NewReader(`[
		{
			"name": "f",
			"type": "function",
			"inputs": [
				{"name": "u256", "type": "uint256"},
				{"name": "i256", "type": "int256"},
				{"name": "u512", "type": "uint512"},
				{"name": "i512", "type": "int512"}
			]
		}
	]`))
	if err != nil {
		t.Fatalf("parse ABI: %v", err)
	}

	maxUint256 := new(big.Int).Sub(twoPow(256), big.NewInt(1))
	maxInt256 := new(big.Int).Sub(twoPow(255), big.NewInt(1))
	minInt256 := new(big.Int).Neg(twoPow(255))
	maxUint512 := new(big.Int).Sub(twoPow(512), big.NewInt(1))
	maxInt512 := new(big.Int).Sub(twoPow(511), big.NewInt(1))
	minInt512 := new(big.Int).Neg(twoPow(511))

	if _, err := parsed.Pack("f", maxUint256, minInt256, maxUint512, minInt512); err != nil {
		t.Fatalf("pack max/min in-range integers: %v", err)
	}
	if _, err := parsed.Pack("f", new(big.Int).Add(maxUint256, big.NewInt(1)), minInt256, maxUint512, minInt512); err == nil {
		t.Fatalf("pack uint256 overflow succeeded")
	}
	if _, err := parsed.Pack("f", maxUint256, new(big.Int).Add(maxInt256, big.NewInt(1)), maxUint512, minInt512); err == nil {
		t.Fatalf("pack int256 overflow succeeded")
	}
	if _, err := parsed.Pack("f", maxUint256, new(big.Int).Sub(minInt256, big.NewInt(1)), maxUint512, minInt512); err == nil {
		t.Fatalf("pack int256 underflow succeeded")
	}
	if _, err := parsed.Pack("f", maxUint256, minInt256, new(big.Int).Add(maxUint512, big.NewInt(1)), minInt512); err == nil {
		t.Fatalf("pack uint512 overflow succeeded")
	}
	if _, err := parsed.Pack("f", maxUint256, minInt256, maxUint512, new(big.Int).Add(maxInt512, big.NewInt(1))); err == nil {
		t.Fatalf("pack int512 overflow succeeded")
	}
	if _, err := parsed.Pack("f", maxUint256, minInt256, maxUint512, new(big.Int).Sub(minInt512, big.NewInt(1))); err == nil {
		t.Fatalf("pack int512 underflow succeeded")
	}
}

func TestABIIntegerUnpackDeclaredWidths(t *testing.T) {
	t.Parallel()

	uint256Ty, err := NewType("uint256", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	int256Ty, err := NewType("int256", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	uint512Ty, err := NewType("uint512", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	int512Ty, err := NewType("int512", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	uint256Args := Arguments{{Type: uint256Ty}}
	int256Args := Arguments{{Type: int256Ty}}
	uint512Args := Arguments{{Type: uint512Ty}}
	int512Args := Arguments{{Type: int512Ty}}

	if _, err := uint256Args.Unpack(common.LeftPadBytes(twoPow(256).Bytes(), abiSlotBytes)); err == nil {
		t.Fatalf("unpack uint256 with non-zero high bits succeeded")
	}
	if _, err := int256Args.Unpack(common.LeftPadBytes(twoPow(255).Bytes(), abiSlotBytes)); err == nil {
		t.Fatalf("unpack int256 without required sign extension succeeded")
	}

	validMinInt256 := append(bytes.Repeat([]byte{0xff}, 32), append([]byte{0x80}, bytes.Repeat([]byte{0}, 31)...)...)
	decoded, err := int256Args.Unpack(validMinInt256)
	if err != nil {
		t.Fatalf("unpack sign-extended int256 min: %v", err)
	}
	if got, want := decoded[0].(*big.Int), new(big.Int).Neg(twoPow(255)); got.Cmp(want) != 0 {
		t.Fatalf("int256 min = %s, want %s", got, want)
	}

	maxUint512Word := bytes.Repeat([]byte{0xff}, abiSlotBytes)
	decoded, err = uint512Args.Unpack(maxUint512Word)
	if err != nil {
		t.Fatalf("unpack uint512 max: %v", err)
	}
	if got, want := decoded[0].(*big.Int), new(big.Int).Sub(twoPow(512), big.NewInt(1)); got.Cmp(want) != 0 {
		t.Fatalf("uint512 max = %s, want %s", got, want)
	}

	decoded, err = int512Args.Unpack(append([]byte{0x80}, bytes.Repeat([]byte{0}, abiSlotBytes-1)...))
	if err != nil {
		t.Fatalf("unpack int512 min: %v", err)
	}
	if got, want := decoded[0].(*big.Int), new(big.Int).Neg(twoPow(511)); got.Cmp(want) != 0 {
		t.Fatalf("int512 min = %s, want %s", got, want)
	}
}
