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
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/common/math"
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
			Output: []byte{1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			Type:   "bytes1",
			Input:  []byte{1, 2},
			Output: nil,
		},
		{
			Type:   "bytes7",
			Input:  []byte{1, 2, 3, 4, 5, 6, 7},
			Output: []byte{1, 2, 3, 4, 5, 6, 7, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		},
		{
			Type:   "bytes32",
			Input:  []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
			Output: []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32},
		},
		{
			Type:   "bytes32",
			Input:  []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24, 25, 26, 27, 28, 29, 30, 31, 32, 33},
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
			if len(val) != 32 {
				t.Errorf("test %d: expected len 32, got %d", i, len(val))
			}
			if !bytes.Equal(val, test.Output) {
				t.Errorf("test %d: expected %x, got %x", i, test.Output, val)
			}
		}
	}
}

func TestParseAddress(t *testing.T) {
	t.Parallel()
	addr := common.Address{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
		0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10,
		0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
		0x19, 0x1A, 0x1B, 0x1C, 0x1D, 0x1E, 0x1F, 0x20,
		0x21, 0x22, 0x23, 0x24, 0x25, 0x26, 0x27, 0x28,
		0x29, 0x2A, 0x2B, 0x2C, 0x2D, 0x2E, 0x2F, 0x30,
		0x31, 0x32, 0x33, 0x34, 0x35, 0x36, 0x37, 0x38,
		0x39, 0x3A, 0x3B, 0x3C, 0x3D, 0x3E, 0x3F, 0x40,
	}
	tests := []struct {
		Input  any
		Output []byte // nil => error
	}{
		{
			Input:  [common.AddressLength]byte(addr),
			Output: addr.Bytes(),
		},
		{
			Input:  addr.Hex(),
			Output: addr.Bytes(),
		},
		{
			Input:  addr.Bytes(),
			Output: addr.Bytes(),
		},
		{
			Input:  addr,
			Output: addr.Bytes(),
		},
		// Various error-cases:
		{Input: [20]byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x14}},
		{Input: "Q" + strings.Repeat("01", common.AddressLength-1)},
		{Input: []byte{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0A, 0x0B, 0x0C, 0x0D, 0x0E, 0x0F, 0x10, 0x11, 0x12, 0x13, 0x14}},
		{Input: "Q" + strings.Repeat("00", common.AddressLength-1)},
		{Input: "Q01"}, // too short string
		{Input: ""},
		{Input: [32]byte{}},       // too short fixed-size array
		{Input: [65]byte{}},       // too long fixed-size array
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
		if have, want := len(val), common.AddressLength; have != want {
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

func TestConvertStringDataToSlice(t *testing.T) {
	t.Parallel()
	slice := []string{"a", "b", "c"}
	var it any = slice
	_, err := convertDataToSlice(it)
	if err != nil {
		t.Fatal(err)
	}
}

func TestConvertUint256DataToSlice(t *testing.T) {
	t.Parallel()
	slice := []*math.HexOrDecimal256{
		math.NewHexOrDecimal256(1),
		math.NewHexOrDecimal256(2),
		math.NewHexOrDecimal256(3),
	}
	var it any = slice
	_, err := convertDataToSlice(it)
	if err != nil {
		t.Fatal(err)
	}
}

func TestConvertAddressDataToSlice(t *testing.T) {
	t.Parallel()
	addr1 := common.BytesToAddress([]byte{1})
	addr2 := common.BytesToAddress([]byte{2})
	addr3 := common.BytesToAddress([]byte{3})

	slice := []common.Address{addr1, addr2, addr3}
	var it any = slice
	_, err := convertDataToSlice(it)
	if err != nil {
		t.Fatal(err)
	}
}
