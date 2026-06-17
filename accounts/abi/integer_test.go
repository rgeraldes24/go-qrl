// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.

package abi

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
)

func TestABIIntegerTypeSizes(t *testing.T) {
	t.Parallel()

	var valid []string
	for bits := 8; bits <= abiSlotBits; bits += 8 {
		valid = append(valid, fmt.Sprintf("uint%d", bits), fmt.Sprintf("int%d", bits))
	}
	for _, typ := range valid {
		if _, err := NewType(typ, "", nil); err != nil {
			t.Fatalf("NewType(%q): %v", typ, err)
		}
	}
	invalid := []string{
		"uint0", "int0", "uint7", "int7",
		fmt.Sprintf("uint%d", abiSlotBits+1),
		fmt.Sprintf("int%d", abiSlotBits+1),
		fmt.Sprintf("uint%d", abiSlotBits+8),
		fmt.Sprintf("int%d", abiSlotBits+8),
	}
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

func TestABIVM64IntegerGoldenEncoding(t *testing.T) {
	t.Parallel()

	uint512Ty, err := NewType("uint512", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	int512Ty, err := NewType("int512", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	uint256Ty, err := NewType("uint256", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	int256Ty, err := NewType("int256", "", nil)
	if err != nil {
		t.Fatal(err)
	}

	maxUint512 := new(big.Int).Sub(twoPow(512), big.NewInt(1))
	minInt512 := new(big.Int).Neg(twoPow(511))
	maxUint256 := new(big.Int).Sub(twoPow(256), big.NewInt(1))
	minInt256 := new(big.Int).Neg(twoPow(255))

	args := Arguments{
		{Type: uint512Ty},
		{Type: int512Ty},
		{Type: uint256Ty},
		{Type: int256Ty},
	}
	packed, err := args.Pack(maxUint512, minInt512, maxUint256, minInt256)
	if err != nil {
		t.Fatalf("pack VM64 integer goldens: %v", err)
	}

	want := append([]byte{}, bytes.Repeat([]byte{0xff}, abiSlotBytes)...)
	want = append(want, append([]byte{0x80}, bytes.Repeat([]byte{0}, abiSlotBytes-1)...)...)
	want = append(want, append(bytes.Repeat([]byte{0}, 32), bytes.Repeat([]byte{0xff}, 32)...)...)
	want = append(want, append(bytes.Repeat([]byte{0xff}, 32), append([]byte{0x80}, bytes.Repeat([]byte{0}, 31)...)...)...)
	if !bytes.Equal(packed, want) {
		t.Fatalf("VM64 integer ABI encoding mismatch:\ngot  %x\nwant %x", packed, want)
	}

	decoded, err := args.Unpack(packed)
	if err != nil {
		t.Fatalf("unpack VM64 integer goldens: %v", err)
	}
	for i, want := range []*big.Int{maxUint512, minInt512, maxUint256, minInt256} {
		if got := decoded[i].(*big.Int); got.Cmp(want) != 0 {
			t.Fatalf("decoded integer %d = %s, want %s", i, got, want)
		}
	}
}

func TestABIIntegerDeclaredWidthsAbove256(t *testing.T) {
	t.Parallel()

	for _, bits := range []int{264, 504} {
		bits := bits
		t.Run(fmt.Sprintf("%d", bits), func(t *testing.T) {
			t.Parallel()

			uintTy, err := NewType(fmt.Sprintf("uint%d", bits), "", nil)
			if err != nil {
				t.Fatalf("uint%d type: %v", bits, err)
			}
			intTy, err := NewType(fmt.Sprintf("int%d", bits), "", nil)
			if err != nil {
				t.Fatalf("int%d type: %v", bits, err)
			}

			args := Arguments{{Type: uintTy}, {Type: intTy}}
			maxUint := new(big.Int).Sub(twoPow(bits), big.NewInt(1))
			maxInt := new(big.Int).Sub(twoPow(bits-1), big.NewInt(1))
			minInt := new(big.Int).Neg(twoPow(bits - 1))

			packed, err := args.Pack(maxUint, minInt)
			if err != nil {
				t.Fatalf("pack uint%d/int%d boundaries: %v", bits, bits, err)
			}
			decoded, err := args.Unpack(packed)
			if err != nil {
				t.Fatalf("unpack uint%d/int%d boundaries: %v", bits, bits, err)
			}
			if got := decoded[0].(*big.Int); got.Cmp(maxUint) != 0 {
				t.Fatalf("uint%d max = %s, want %s", bits, got, maxUint)
			}
			if got := decoded[1].(*big.Int); got.Cmp(minInt) != 0 {
				t.Fatalf("int%d min = %s, want %s", bits, got, minInt)
			}

			if _, err := args.Pack(twoPow(bits), minInt); err == nil {
				t.Fatalf("pack uint%d overflow succeeded", bits)
			}
			if _, err := args.Pack(maxUint, new(big.Int).Add(maxInt, big.NewInt(1))); err == nil {
				t.Fatalf("pack int%d overflow succeeded", bits)
			}
			if _, err := args.Pack(maxUint, new(big.Int).Sub(minInt, big.NewInt(1))); err == nil {
				t.Fatalf("pack int%d underflow succeeded", bits)
			}

			if _, err := (Arguments{{Type: uintTy}}).Unpack(common.LeftPadBytes(twoPow(bits).Bytes(), abiSlotBytes)); err == nil {
				t.Fatalf("unpack uint%d with non-zero high bits succeeded", bits)
			}
			if _, err := (Arguments{{Type: intTy}}).Unpack(common.LeftPadBytes(twoPow(bits-1).Bytes(), abiSlotBytes)); err == nil {
				t.Fatalf("unpack int%d without required sign extension succeeded", bits)
			}
		})
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
