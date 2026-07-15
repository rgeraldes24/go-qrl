// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package apitypes

import (
	"strings"
	"testing"
)

func TestIsPrimitive(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{
		"address", "bool", "string", "bytes",
		"int8", "int256", "int512", "uint8", "uint256", "uint512",
		"bytes1", "bytes32", "bytes64",
		"uint512[]",
	} {
		if !isPrimitiveTypeValid(typ) {
			t.Errorf("expected %q to be a valid primitive type", typ)
		}
	}
	for _, typ := range []string{
		"int", "uint", "int0", "uint0", "int7", "uint513", "uint008",
		"bytes0", "bytes65", "bytes064", "function",
		"uint256[2]", "uint256[", "uint256[]x",
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
		if err := types.validate(); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("type name %q: expected reserved-name error, got %v", name, err)
		}
	}
}

func TestTypedDataAcceptsPrimitivePrefixedCustomType(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"bytesEnvelope", "intention", "uintConfig"} {
		types := Types{
			TypedDataDomainType: append([]Type(nil), qrlTypedDataDomain...),
			name:                {{Name: "value", Type: "bool"}},
		}
		if err := types.validate(); err != nil {
			t.Errorf("type name %q: %v", name, err)
		}
	}
}
