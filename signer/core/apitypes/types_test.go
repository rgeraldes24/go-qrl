// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package apitypes

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsPrimitive(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{
		"address", "bool", "string", "bytes",
		"int8", "int256", "int512", "uint8", "uint256", "uint512",
		"bytes1", "bytes32", "bytes64",
		"uint512[]", "bytes64[2]", "address[][3]",
	} {
		if !isPrimitiveTypeValid(typ) {
			t.Errorf("expected %q to be a valid primitive type", typ)
		}
	}
	for _, typ := range []string{
		"int", "uint", "int0", "uint0", "int7", "uint513", "uint008",
		"bytes0", "bytes65", "bytes064", "function",
		"uint256[0]", "uint256[01]", "uint256[", "uint256[]x",
	} {
		if isPrimitiveTypeValid(typ) {
			t.Errorf("expected %q to be rejected", typ)
		}
	}
}

func TestTypedDataRejectsReservedCustomType(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"address", "bytes32", "function", "int", "uint512"} {
		types := Types{
			TypedDataDomainType: append([]Type(nil), qrlTypedDataDomain...),
			name:                {{Name: "nested", Type: "bool"}},
		}
		if err := validateTypedDataTypes(types); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("type name %q: expected reserved-name error, got %v", name, err)
		}
	}
}

func TestTypedDataJSONRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"types":{},"primaryType":"Message","domain":{},"message":{},"unexpected":true}`,
		`{"types":{},"primaryType":"Message","domain":{"unexpected":true},"message":{}}`,
	} {
		var typedData TypedData
		if err := json.Unmarshal([]byte(input), &typedData); err == nil {
			t.Fatalf("unknown field accepted in %s", input)
		}
	}
}

func TestTypedDataJSONRejectsDuplicateKeys(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"types":{},"types":{},"primaryType":"Message","domain":{},"message":{}}`,
		`{"types":{},"primaryType":"Message","domain":{"name":"one","name":"two"},"message":{}}`,
		`{"types":{},"primaryType":"Message","domain":{},"message":{"value":1,"value":2}}`,
	} {
		var typedData TypedData
		if err := json.Unmarshal([]byte(input), &typedData); err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("duplicate key accepted in %s: %v", input, err)
		}
	}
}
