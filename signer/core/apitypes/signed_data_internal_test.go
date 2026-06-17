// Copyright 2019 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package apitypes

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/crypto"
)

func TestBytesPadding(t *testing.T) {
	t.Parallel()
	tests := []struct {
		Type   string
		Input  []byte
		Output []byte // nil => error
	}{
		{
			// Fail on wrong length
			Type:   "bytes20",
			Input:  []byte{},
			Output: nil,
		},
		{
			Type:   "bytes1",
			Input:  []byte{1},
			Output: common.RightPadBytes([]byte{1}, typedDataWordBytes),
		},
		{
			Type:   "bytes1",
			Input:  []byte{1, 2},
			Output: nil,
		},
		{
			Type:   "bytes7",
			Input:  []byte{1, 2, 3, 4, 5, 6, 7},
			Output: common.RightPadBytes([]byte{1, 2, 3, 4, 5, 6, 7}, typedDataWordBytes),
		},
		{
			Type:   "bytes32",
			Input:  []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
			Output: common.RightPadBytes([]byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32}, typedDataWordBytes),
		},
		{
			Type:   "bytes32",
			Input:  []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33},
			Output: nil,
		},
		{
			Type:   "bytes64",
			Input:  bytes.Repeat([]byte{0xff}, typedDataWordBytes),
			Output: bytes.Repeat([]byte{0xff}, typedDataWordBytes),
		},
		{
			Type:   "bytes64",
			Input:  bytes.Repeat([]byte{0xff}, typedDataWordBytes+1),
			Output: nil,
		},
	}

	d := TypedData{}
	for i, test := range tests {
		val, err := d.EncodePrimitiveValue(test.Type, test.Input, 1)
		if test.Output == nil {
			if err == nil {
				t.Errorf("test %d: expected error, got no error (result %x)", i, val)
			}
		} else {
			if err != nil {
				t.Errorf("test %d: expected no error, got %v", i, err)
			}
			if len(val) != typedDataWordBytes {
				t.Errorf("test %d: expected len %d, got %d", i, typedDataWordBytes, len(val))
			}
			if !bytes.Equal(val, test.Output) {
				t.Errorf("test %d: expected %x, got %x", i, test.Output, val)
			}
		}
	}
}

func TestParseAddress(t *testing.T) {
	t.Parallel()
	// Hyperion typed data primitive encoding uses one 64-byte slot for QRL addresses.
	validAddr64 := [common.AddressLength]byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10,
		0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
		0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F, 0x20,
		0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28,
		0x29, 0x2A, 0x2B, 0x2C, 0x2D, 0x2E, 0x2F, 0x30,
		0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38,
		0x39, 0x3A, 0x3B, 0x3C, 0x3D, 0x3E, 0x3F, 0x40,
	}
	okOutput := common.FromHex("0x0102030405060708090A0B0C0D0E0F10" +
		"1112131415161718191A1B1C1D1E1F20" +
		"2122232425262728292A2B2C2D2E2F30" +
		"3132333435363738393A3B3C3D3E3F40")
	tests := []struct {
		Input  any
		Output []byte // nil => error
	}{
		{
			Input:  validAddr64,
			Output: okOutput,
		},
		{
			Input:  "Q0102030405060708090A0B0C0D0E0F101112131415161718191A1B1C1D1E1F202122232425262728292A2B2C2D2E2F303132333435363738393A3B3C3D3E3F40",
			Output: okOutput,
		},
		{
			Input:  validAddr64[:],
			Output: okOutput,
		},
		// Various error-cases:
		{Input: "Q01"}, // too short string
		{Input: ""},
		{Input: [32]byte{}},       // wrong fixed-size array length
		{Input: [20]byte{}},       // old 20-byte form no longer accepted
		{Input: make([]byte, 63)}, // too short slice
		{Input: make([]byte, 65)}, // too long slice
		{Input: nil},
	}

	d := TypedData{}
	for i, test := range tests {
		val, err := d.EncodePrimitiveValue("address", test.Input, 1)
		if test.Output == nil {
			if err == nil {
				t.Errorf("test %d: expected error, got no error (result %x)", i, val)
			}
			continue
		}
		if err != nil {
			t.Errorf("test %d: expected no error, got %v", i, err)
		}
		if have, want := len(val), 64; have != want {
			t.Errorf("test %d: have len %d, want %d", i, have, want)
		}
		if !bytes.Equal(val, test.Output) {
			t.Errorf("test %d: want %x, have %x", i, test.Output, val)
		}
	}
}

func TestParseBytes(t *testing.T) {
	t.Parallel()
	for i, tt := range []struct {
		v   any
		exp []byte
	}{
		{"0x", []byte{}},
		{"0x1234", []byte{0x12, 0x34}},
		{[]byte{12, 34}, []byte{12, 34}},
		{hexutil.Bytes([]byte{12, 34}), []byte{12, 34}},
		{"1234", nil},    // not a proper hex-string
		{"0x01233", nil}, // nibbles should be rejected
		{"not a hex string", nil},
		{15, nil},
		{nil, nil},
		{[2]byte{12, 34}, []byte{12, 34}},
		{[8]byte{12, 34, 56, 78, 90, 12, 34, 56}, []byte{12, 34, 56, 78, 90, 12, 34, 56}},
		{[16]byte{12, 34, 56, 78, 90, 12, 34, 56, 12, 34, 56, 78, 90, 12, 34, 56}, []byte{12, 34, 56, 78, 90, 12, 34, 56, 12, 34, 56, 78, 90, 12, 34, 56}},
	} {
		out, ok := parseBytes(tt.v)
		if tt.exp == nil {
			if ok || out != nil {
				t.Errorf("test %d: expected !ok, got ok = %v with out = %x", i, ok, out)
			}
			continue
		}
		if !ok {
			t.Errorf("test %d: expected ok got !ok", i)
		}
		if !bytes.Equal(out, tt.exp) {
			t.Errorf("test %d: expected %x got %x", i, tt.exp, out)
		}
	}
}

func TestParseInteger(t *testing.T) {
	t.Parallel()
	maxUint512 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1))
	overflowUint512 := new(big.Int).Lsh(big.NewInt(1), 512)
	maxInt512 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 511), big.NewInt(1))
	overflowInt512 := new(big.Int).Lsh(big.NewInt(1), 511)
	minInt512 := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 511))

	for i, tt := range []struct {
		t   string
		v   any
		exp *big.Int
	}{
		{"uint32", "-123", nil},
		{"int32", "-123", big.NewInt(-123)},
		{"int32", big.NewInt(-124), big.NewInt(-124)},
		{"uint32", "0xff", big.NewInt(0xff)},
		{"int8", "0xffff", nil},
		{"int8", "0x7f", big.NewInt(0x7f)},
		{"int8", "0x80", nil},
		{"uint512", maxUint512.String(), maxUint512},
		{"uint512", overflowUint512.String(), nil},
		{"int512", maxInt512.String(), maxInt512},
		{"int512", minInt512.String(), minInt512},
		{"int512", overflowInt512.String(), nil},
		{"uint", maxUint512.String(), maxUint512},
	} {
		res, err := parseInteger(tt.t, tt.v)
		if tt.exp == nil && res == nil {
			continue
		}
		if tt.exp == nil && res != nil {
			t.Errorf("test %d, got %v, expected nil", i, res)
			continue
		}
		if tt.exp != nil && res == nil {
			t.Errorf("test %d, got '%v', expected %v", i, err, tt.exp)
			continue
		}
		if tt.exp.Cmp(res) != 0 {
			t.Errorf("test %d, got %v expected %v", i, res, tt.exp)
		}
	}
}

func TestHyperionTypedDataVM64PrimitiveEncoding(t *testing.T) {
	t.Parallel()

	d := TypedData{}
	trueWord, err := d.EncodePrimitiveValue("bool", true, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(trueWord) != typedDataWordBytes || trueWord[typedDataWordBytes-1] != 1 {
		t.Fatalf("bool true encoding mismatch: %x", trueWord)
	}

	maxUint512 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1))
	uintWord, err := d.EncodePrimitiveValue("uint512", maxUint512, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(uintWord, bytes.Repeat([]byte{0xff}, typedDataWordBytes)) {
		t.Fatalf("uint512 max encoding mismatch: %x", uintWord)
	}

	intWord, err := d.EncodePrimitiveValue("int512", big.NewInt(-1), 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(intWord, bytes.Repeat([]byte{0xff}, typedDataWordBytes)) {
		t.Fatalf("int512 -1 encoding mismatch: %x", intWord)
	}

	if _, err := d.EncodePrimitiveValue("uint256", new(big.Int).Lsh(big.NewInt(1), 256), 1); err == nil {
		t.Fatalf("expected uint256 overflow to fail")
	}

	stringWord, err := d.EncodePrimitiveValue("string", "vm64", 1)
	if err != nil {
		t.Fatal(err)
	}
	wantHashWord := common.RightPadBytes(crypto.Keccak256([]byte("vm64")), typedDataWordBytes)
	if !bytes.Equal(stringWord, wantHashWord) {
		t.Fatalf("string hash word mismatch:\ngot  %x\nwant %x", stringWord, wantHashWord)
	}
}

func TestHyperionTypedDataVM64EncodeData(t *testing.T) {
	t.Parallel()

	maxUint512 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1))
	td := TypedData{
		Types: Types{
			"EIP712Domain": {
				{Name: "chainId", Type: "uint512"},
			},
			"Message": {
				{Name: "amount", Type: "uint512"},
				{Name: "delta", Type: "int512"},
				{Name: "payload", Type: "bytes64"},
			},
		},
		PrimaryType: "Message",
		Domain: TypedDataDomain{
			ChainId: math.NewHexOrDecimal512(1),
		},
		Message: TypedDataMessage{
			"amount":  maxUint512,
			"delta":   big.NewInt(-1),
			"payload": bytes.Repeat([]byte{0xab}, typedDataWordBytes),
		},
	}

	encoded, err := td.EncodeData(td.PrimaryType, td.Message, 0)
	if err != nil {
		t.Fatal(err)
	}
	if have, want := len(encoded), 4*typedDataWordBytes; have != want {
		t.Fatalf("encoded length mismatch: have %d want %d", have, want)
	}
	if !bytes.Equal(encoded[typedDataWordBytes:2*typedDataWordBytes], bytes.Repeat([]byte{0xff}, typedDataWordBytes)) {
		t.Fatalf("uint512 field was not encoded as one full VM64 word")
	}
	if !bytes.Equal(encoded[2*typedDataWordBytes:3*typedDataWordBytes], bytes.Repeat([]byte{0xff}, typedDataWordBytes)) {
		t.Fatalf("int512 field was not encoded as one full VM64 word")
	}
	if !bytes.Equal(encoded[3*typedDataWordBytes:4*typedDataWordBytes], bytes.Repeat([]byte{0xab}, typedDataWordBytes)) {
		t.Fatalf("bytes64 field was not encoded as one full VM64 word")
	}
}

func TestConvertStringDataToSlice(t *testing.T) {
	t.Parallel()
	slice := []string{"a", "b", "c"}
	var it any = slice
	_, err := convertDataToSlice(it)
	if err != nil {
		t.Fatal(err)
	}
}

func TestConvertUint512DataToSlice(t *testing.T) {
	t.Parallel()
	slice := []*math.HexOrDecimal512{
		math.NewHexOrDecimal512(1),
		math.NewHexOrDecimal512(2),
		math.NewHexOrDecimal512(3),
	}
	var it any = slice
	_, err := convertDataToSlice(it)
	if err != nil {
		t.Fatal(err)
	}
}

func TestConvertAddressDataToSlice(t *testing.T) {
	t.Parallel()
	addr1, _ := common.NewAddressFromString("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000001")
	addr2, _ := common.NewAddressFromString("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000002")
	addr3, _ := common.NewAddressFromString("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000003")

	slice := []common.Address{addr1, addr2, addr3}
	var it any = slice
	_, err := convertDataToSlice(it)
	if err != nil {
		t.Fatal(err)
	}
}
