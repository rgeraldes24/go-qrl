// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package apitypes

import (
	"testing"
)

func TestIsPrimitive(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{
		"address", "bool", "string", "bytes",
		"int", "int8", "int256", "int512", "uint", "uint8", "uint256", "uint512",
		"bytes1", "bytes32", "bytes64",
		"int[]", "uint[]", "uint512[]",
	} {
		if !isPrimitiveTypeValid(typ) {
			t.Errorf("expected %q to be a valid primitive type", typ)
		}
	}
	for _, typ := range []string{
		"int0", "uint0", "int7", "uint513", "uint008",
		"bytes0", "bytes65", "bytes064", "function",
		"uint256[2]", "uint256[", "uint256[]x",
	} {
		if isPrimitiveTypeValid(typ) {
			t.Errorf("expected %q to be rejected", typ)
		}
	}
}

func TestValidateDomainType(t *testing.T) {
	t.Parallel()
	valid := [][]Type{
		{{Name: "name", Type: "string"}},
		{
			{Name: "name", Type: "string"},
			{Name: "chainId", Type: "uint256"},
			{Name: "salt", Type: "bytes32"},
		},
		qrlTypedDataDomain,
	}
	for _, fields := range valid {
		if err := validateDomainType(fields); err != nil {
			t.Errorf("valid domain fields %v: %v", fields, err)
		}
	}

	invalid := [][]Type{
		nil,
		{{Name: "name", Type: "bytes32"}},
		{
			{Name: "version", Type: "string"},
			{Name: "name", Type: "string"},
		},
		{
			{Name: "name", Type: "string"},
			{Name: "name", Type: "string"},
		},
		{{Name: "application", Type: "string"}},
	}
	for _, fields := range invalid {
		if err := validateDomainType(fields); err == nil {
			t.Errorf("invalid domain fields %v were accepted", fields)
		}
	}
}
