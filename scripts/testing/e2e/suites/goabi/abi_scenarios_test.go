// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package goabi

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	qrlabi "github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/common"
)

func parsePortableABI(t *testing.T, definition string) qrlabi.ABI {
	t.Helper()
	parsed, err := qrlabi.JSON(strings.NewReader(definition))
	if err != nil {
		t.Fatalf("parse portable ABI: %v", err)
	}
	return parsed
}

func assertPortableABIRoundTrip(t *testing.T, parsed qrlabi.ABI, value any) any {
	t.Helper()
	packed, err := parsed.Pack("echo", value)
	if err != nil {
		t.Fatalf("pack %T: %v", value, err)
	}
	method := parsed.Methods["echo"]
	decoded, err := parsed.Unpack("echo", packed[len(method.ID):])
	if err != nil {
		t.Fatalf("unpack %T: %v", value, err)
	}
	if len(decoded) != 1 {
		t.Fatalf("unpack returned %d values, want 1", len(decoded))
	}
	if diff := cmp.Diff(value, decoded[0], abiCompareOptions...); diff != "" {
		t.Fatalf("round trip mismatch (-want +have):\n%s", diff)
	}
	return decoded[0]
}

func makePortableTupleValue(t *testing.T, tupleType qrlabi.Type, fields ...any) any {
	t.Helper()
	value := reflect.New(tupleType.GetType()).Elem()
	if value.Kind() != reflect.Struct || value.NumField() != len(fields) {
		t.Fatalf("tuple type %v has %d fields, want %d", tupleType, value.NumField(), len(fields))
	}
	for i, field := range fields {
		source := reflect.ValueOf(field)
		destination := value.Field(i)
		if !source.IsValid() || !source.Type().AssignableTo(destination.Type()) {
			t.Fatalf("tuple field %d has type %v, want %v", i, source.Type(), destination.Type())
		}
		destination.Set(source)
	}
	return value.Interface()
}

// TestPortableVM64ABISmoke is deliberately small. Exhaustive codec, malformed
// input, integer-width, topic, and public-API matrices live beside accounts/abi.
// This external consumer verifies the VM64 contract needed by the E2E suite.
func TestPortableVM64ABISmoke(t *testing.T) {
	t.Run("canonical calldata and output layout", func(t *testing.T) {
		var recipient common.Address
		for i := range recipient {
			recipient[i] = byte(i*17 + 9)
		}
		if err := checkGoABILayout(recipient); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("nested dynamic tuple array", func(t *testing.T) {
		parsed := parsePortableABI(t, `[{
			"name":"echo",
			"type":"function",
			"stateMutability":"pure",
			"inputs":[{"name":"value","type":"tuple[]","internalType":"struct Portable.Record[]","components":[
				{"name":"owner","type":"address"},
				{"name":"notes","type":"string[]"},
				{"name":"payload","type":"bytes"}
			]}],
			"outputs":[{"name":"value","type":"tuple[]","internalType":"struct Portable.Record[]","components":[
				{"name":"owner","type":"address"},
				{"name":"notes","type":"string[]"},
				{"name":"payload","type":"bytes"}
			]}]
		}]`)
		sliceType := parsed.Methods["echo"].Inputs[0].Type
		var owner common.Address
		for i := range owner {
			owner[i] = byte(255 - i)
		}
		record := makePortableTupleValue(
			t,
			*sliceType.Elem,
			owner,
			[]string{"", "nested VM64 value", string(bytes.Repeat([]byte{'x'}, 65))},
			append(bytes.Repeat([]byte{0xa5}, 64), 0x00),
		)
		value := reflect.MakeSlice(sliceType.GetType(), 1, 1)
		value.Index(0).Set(reflect.ValueOf(record))
		assertPortableABIRoundTrip(t, parsed, value.Interface())
	})

}
