// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package bind

import (
	"testing"

	"github.com/theQRL/go-qrl/accounts/abi"
)

func TestBindTopicTypeCompositeValuesUseHash(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		typ  string
	}{
		{name: "string", typ: "string"},
		{name: "bytes", typ: "bytes"},
		{name: "slice", typ: "uint512[]"},
		{name: "array", typ: "address[2]"},
		{name: "tuple", typ: "tuple"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			kind, err := abi.NewType(test.typ, "", nil)
			if err != nil {
				t.Fatalf("create ABI type %s: %v", test.typ, err)
			}
			if got := bindTopicType(kind, nil); got != "common.Hash" {
				t.Fatalf("bindTopicType(%s) = %s, want common.Hash", test.typ, got)
			}
		})
	}
}
