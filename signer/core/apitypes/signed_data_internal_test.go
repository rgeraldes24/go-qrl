// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package apitypes

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/crypto"
)

func TestFixedBytesEncodingVM64(t *testing.T) {
	t.Parallel()
	codec := new(TypedData)
	for _, test := range []struct {
		typ   string
		input []byte
		valid bool
	}{
		{typ: "bytes1", input: []byte{1}, valid: true},
		{typ: "bytes32", input: bytes.Repeat([]byte{2}, 32), valid: true},
		{typ: "bytes64", input: bytes.Repeat([]byte{3}, 64), valid: true},
		{typ: "bytes20", input: nil},
		{typ: "bytes1", input: []byte{1, 2}},
		{typ: "bytes65", input: bytes.Repeat([]byte{4}, 65)},
	} {
		encoded, err := codec.EncodePrimitiveValue(test.typ, test.input, 1)
		if !test.valid {
			if err == nil {
				t.Errorf("%s: expected rejection, got %x", test.typ, encoded)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", test.typ, err)
			continue
		}
		if len(encoded) != uint512.WordBytes {
			t.Errorf("%s: encoded length %d, want %d", test.typ, len(encoded), uint512.WordBytes)
		}
		want := make([]byte, uint512.WordBytes)
		copy(want, test.input)
		if !bytes.Equal(encoded, want) {
			t.Errorf("%s: have %x, want %x", test.typ, encoded, want)
		}
	}
}

func TestAddressEncodingVM64(t *testing.T) {
	t.Parallel()
	codec := new(TypedData)
	var address common.Address
	for i := range address {
		address[i] = byte(i + 1)
	}
	for _, input := range []any{address, address[:], address.Hex()} {
		encoded, err := codec.EncodePrimitiveValue("address", input, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(encoded, address[:]) {
			t.Fatalf("have %x, want %x", encoded, address)
		}
	}
	for _, input := range []any{"Q01", [32]byte{}, make([]byte, 63), make([]byte, 65), nil} {
		if _, err := codec.EncodePrimitiveValue("address", input, 1); err == nil {
			t.Errorf("expected address input %T(%v) to be rejected", input, input)
		}
	}
}

func TestDynamicHashEncodingIsLeftAligned(t *testing.T) {
	t.Parallel()
	encoded, err := new(TypedData).EncodePrimitiveValue("string", "hello", 1)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, uint512.WordBytes)
	copy(want, crypto.Keccak256([]byte("hello")))
	if !bytes.Equal(encoded, want) {
		t.Fatalf("have %x, want %x", encoded, want)
	}
	if !bytes.Equal(encoded[common.HashLength:], make([]byte, common.HashLength)) {
		t.Fatal("hash word is not zero-padded on the right")
	}
}

func TestIntegerEncodingVM64(t *testing.T) {
	t.Parallel()
	codec := new(TypedData)
	max256 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	max512 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1))
	tests := []struct {
		typ   string
		value any
		want  []byte
	}{
		{typ: "uint256", value: max256, want: append(make([]byte, 32), bytes.Repeat([]byte{0xff}, 32)...)},
		{typ: "uint512", value: max512, want: bytes.Repeat([]byte{0xff}, 64)},
		{typ: "int8", value: "-1", want: bytes.Repeat([]byte{0xff}, 64)},
		{typ: "int8", value: "-128", want: append(bytes.Repeat([]byte{0xff}, 63), 0x80)},
	}
	for _, test := range tests {
		encoded, err := codec.EncodePrimitiveValue(test.typ, test.value, 1)
		if err != nil {
			t.Errorf("%s: %v", test.typ, err)
			continue
		}
		if !bytes.Equal(encoded, test.want) {
			t.Errorf("%s: have %x, want %x", test.typ, encoded, test.want)
		}
	}
	for _, test := range []struct {
		typ   string
		value any
	}{
		{typ: "uint256", value: new(big.Int).Lsh(big.NewInt(1), 256)},
		{typ: "uint512", value: new(big.Int).Lsh(big.NewInt(1), 512)},
		{typ: "int8", value: -129},
		{typ: "int8", value: 128},
		{typ: "uint8", value: -1},
		{typ: "uint", value: 1},
	} {
		if _, err := codec.EncodePrimitiveValue(test.typ, test.value, 1); err == nil {
			t.Errorf("expected %s(%v) to be rejected", test.typ, test.value)
		}
	}
}

func TestParseTypedDataBytes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		input any
		want  []byte
	}{
		{input: "0x", want: []byte{}},
		{input: "0x1234", want: []byte{0x12, 0x34}},
		{input: []byte{12, 34}, want: []byte{12, 34}},
		{input: hexutil.Bytes{12, 34}, want: []byte{12, 34}},
		{input: [2]byte{12, 34}, want: []byte{12, 34}},
		{input: "1234"},
		{input: "0x01233"},
		{input: 15},
		{input: nil},
	} {
		got, ok := parseBytes(test.input)
		if test.want == nil {
			if ok || got != nil {
				t.Errorf("input %v: expected rejection, got %x", test.input, got)
			}
			continue
		}
		if !ok || !bytes.Equal(got, test.want) {
			t.Errorf("input %v: have %x, want %x", test.input, got, test.want)
		}
	}
}

func TestTypedDataArrayConversion(t *testing.T) {
	t.Parallel()
	for _, input := range []any{
		[]string{"a", "b"},
		[2]uint64{1, 2},
		[]common.Address{{1}, {2}},
	} {
		if _, err := convertDataToSlice(input); err != nil {
			t.Errorf("%T: %v", input, err)
		}
	}
	if _, err := convertDataToSlice("not an array"); err == nil {
		t.Fatal("expected scalar array conversion to fail")
	}
}

func TestNestedArrayEncodingVM64(t *testing.T) {
	t.Parallel()
	typedData := &TypedData{
		Types: Types{
			TypedDataDomainType: append([]Type(nil), qrlTypedDataDomain...),
			"Matrix":            {{Name: "values", Type: "uint16[2][]"}},
		},
		PrimaryType: "Matrix",
		Domain: TypedDataDomain{
			Name:              "matrix",
			Version:           "1",
			ChainId:           math.NewHexOrDecimal256(1),
			VerifyingContract: common.Address{}.Hex(),
			Salt:              hexutil.Encode(make([]byte, common.HashLength)),
		},
		Message: TypedDataMessage{
			"values": []any{
				[]any{"1", "2"},
				[]any{"3", "4"},
			},
		},
	}
	encoded, err := typedData.EncodeData("Matrix", typedData.Message, 1)
	if err != nil {
		t.Fatal(err)
	}
	word := func(value byte) []byte {
		result := make([]byte, uint512.WordBytes)
		result[len(result)-1] = value
		return result
	}
	innerOne := encodeTypedDataHashWord(crypto.Keccak256(append(word(1), word(2)...)))
	innerTwo := encodeTypedDataHashWord(crypto.Keccak256(append(word(3), word(4)...)))
	wantArray := encodeTypedDataHashWord(crypto.Keccak256(append(innerOne, innerTwo...)))
	want := append(encodeTypedDataHashWord(crypto.Keccak256([]byte("Matrix(uint16[2][] values)"))), wantArray...)
	if !bytes.Equal(encoded, want) {
		t.Fatalf("nested array encoding:\n have %x\n want %x", encoded, want)
	}
}
