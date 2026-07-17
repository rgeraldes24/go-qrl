// Copyright 2023 The go-ethereum Authors
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

import "testing"

func TestIsPrimitive(t *testing.T) {
	t.Parallel()
	// Expected positives
	for i, tc := range []string{
		"int24", "int24[]", "int24[2]", "int24[2][3]", "uint88", "uint88[]", "uint", "uint[]", "uint[2]", "int256", "int256[]",
		"uint96", "uint96[]", "int96", "int96[]", "int512", "uint512[]",
		"bytes17[]", "bytes17", "bytes33[]", "bytes64", "bytes64[2]", "address[2]", "bool[4]", "string[5]", "bytes[2]",
	} {
		if !isPrimitiveTypeValid(tc) {
			t.Errorf("test %d: expected '%v' to be a valid primitive", i, tc)
		}
	}
	// Expected negatives
	for i, tc := range []string{
		"int257", "int257[]", "uint88 ", "uint88 []", "uint257", "uint-1[]",
		"uint0", "uint0[]", "int95", "int95[]", "uint1", "uint1[]", "bytes65[]", "bytess",
	} {
		if isPrimitiveTypeValid(tc) {
			t.Errorf("test %d: expected '%v' to not be a valid primitive", i, tc)
		}
	}
}

func TestTypeIsArray(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{"int24[]", "int24[2]", "int24[2][2][2]"} {
		if !(&Type{Type: typ}).isArray() {
			t.Errorf("expected %q to be an array", typ)
		}
	}
	for _, typ := range []string{"int24", "uint88", "bytes64"} {
		if (&Type{Type: typ}).isArray() {
			t.Errorf("expected %q not to be an array", typ)
		}
	}
}

func TestTypeName(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		typ  string
		want string
	}{
		{"int24[]", "int24"},
		{"int24[2][2][2]", "int24"},
		{"bytes64[2]", "bytes64"},
		{"uint512", "uint512"},
	} {
		if got := (&Type{Type: test.typ}).typeName(); got != test.want {
			t.Errorf("typeName(%q) = %q, want %q", test.typ, got, test.want)
		}
	}
}
