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
	"encoding/json"
	"math/big"
	"reflect"
	"testing"

	qrlabi "github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/common"
	qrlmath "github.com/theQRL/go-qrl/common/math"
)

type portableABIArgument struct {
	Name         string                `json:"name"`
	Type         string                `json:"type"`
	InternalType string                `json:"internalType,omitempty"`
	Components   []portableABIArgument `json:"components,omitempty"`
	Indexed      bool                  `json:"indexed,omitempty"`
}

type portableABIEntry struct {
	Name            string                `json:"name,omitempty"`
	Type            string                `json:"type"`
	StateMutability string                `json:"stateMutability,omitempty"`
	Inputs          []portableABIArgument `json:"inputs,omitempty"`
	Outputs         []portableABIArgument `json:"outputs,omitempty"`
	Anonymous       bool                  `json:"anonymous,omitempty"`
}

func parsePortableABI(t *testing.T, entries ...portableABIEntry) qrlabi.ABI {
	t.Helper()
	encoded, err := json.Marshal(entries)
	if err != nil {
		t.Fatalf("marshal portable ABI: %v", err)
	}
	parsed, err := qrlabi.JSON(bytes.NewReader(encoded))
	if err != nil {
		t.Fatalf("parse portable ABI %s: %v", encoded, err)
	}
	return parsed
}

func singleValuePortableABI(t *testing.T, argument portableABIArgument) qrlabi.ABI {
	t.Helper()
	argument.Name = "value"
	return parsePortableABI(t, portableABIEntry{
		Name:            "echo",
		Type:            "function",
		StateMutability: "pure",
		Inputs:          []portableABIArgument{argument},
		Outputs:         []portableABIArgument{argument},
	})
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
	if len(decoded) != 1 || !portableABIValuesEqual(reflect.ValueOf(decoded[0]), reflect.ValueOf(value)) {
		t.Fatalf("round trip mismatch\nhave %#v\nwant %#v", decoded, value)
	}
	return decoded[0]
}

// portableABIValuesEqual and makePortableTupleValue are also used by the
// generated-binding smoke tests. They intentionally compare big.Int values by
// value while retaining exact Go types for every other ABI value.
func portableABIValuesEqual(have, want reflect.Value) bool {
	if !have.IsValid() || !want.IsValid() {
		return have.IsValid() == want.IsValid()
	}
	if have.Type() != want.Type() {
		return false
	}
	if have.Kind() == reflect.Ptr && have.Type().Elem() == reflect.TypeFor[big.Int]() {
		if have.IsNil() || want.IsNil() {
			return have.IsNil() == want.IsNil()
		}
		return have.Interface().(*big.Int).Cmp(want.Interface().(*big.Int)) == 0
	}
	switch have.Kind() {
	case reflect.Array, reflect.Slice:
		if have.Kind() == reflect.Slice && (have.IsNil() != want.IsNil()) {
			return false
		}
		if have.Len() != want.Len() {
			return false
		}
		for i := range have.Len() {
			if !portableABIValuesEqual(have.Index(i), want.Index(i)) {
				return false
			}
		}
		return true
	case reflect.Struct:
		for i := range have.NumField() {
			if !portableABIValuesEqual(have.Field(i), want.Field(i)) {
				return false
			}
		}
		return true
	case reflect.Interface, reflect.Ptr:
		if have.IsNil() || want.IsNil() {
			return have.IsNil() == want.IsNil()
		}
		return portableABIValuesEqual(have.Elem(), want.Elem())
	default:
		return reflect.DeepEqual(have.Interface(), want.Interface())
	}
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
	t.Run("64-byte word and address", func(t *testing.T) {
		parsed := parsePortableABI(t, portableABIEntry{
			Name:            "inspect",
			Type:            "function",
			StateMutability: "pure",
			Inputs: []portableABIArgument{
				{Name: "amount", Type: "uint512"},
				{Name: "recipient", Type: "address"},
			},
			Outputs: []portableABIArgument{
				{Name: "amount", Type: "uint512"},
				{Name: "recipient", Type: "address"},
			},
		})
		amount := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 511), big.NewInt(0x42))
		var recipient common.Address
		for i := range recipient {
			recipient[i] = byte(i*17 + 9)
		}

		calldata, err := parsed.Pack("inspect", amount, recipient)
		if err != nil {
			t.Fatal(err)
		}
		method := parsed.Methods["inspect"]
		body := calldata[len(method.ID):]
		if len(body) != 2*64 {
			t.Fatalf("static VM64 body = %d bytes, want 128", len(body))
		}
		if want := qrlmath.U512Bytes(amount); !bytes.Equal(body[:64], want) {
			t.Fatalf("uint512 word = %x, want %x", body[:64], want)
		}
		if !bytes.Equal(body[64:], recipient[:]) {
			t.Fatalf("address word = %x, want %x", body[64:], recipient)
		}
		values, err := parsed.Unpack("inspect", body)
		if err != nil {
			t.Fatal(err)
		}
		if values[0].(*big.Int).Cmp(amount) != 0 || values[1].(common.Address) != recipient {
			t.Fatalf("decoded values = %#v, want %s and %s", values, amount, recipient)
		}
	})

	t.Run("nested dynamic tuple array", func(t *testing.T) {
		parsed := singleValuePortableABI(t, portableABIArgument{
			Type:         "tuple[]",
			InternalType: "struct Portable.Record[]",
			Components: []portableABIArgument{
				{Name: "owner", Type: "address"},
				{Name: "notes", Type: "string[]"},
				{Name: "payload", Type: "bytes"},
			},
		})
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
