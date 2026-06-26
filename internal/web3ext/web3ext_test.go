// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.
//
// go-qrl is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-qrl is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.

package web3ext

import (
	"strings"
	"testing"
)

func TestUnsupportedConsoleExtensionsRemoved(t *testing.T) {
	if _, ok := Modules["miner"]; ok {
		t.Fatal("miner extension should not be exported")
	}
	for _, needle := range []string{"submitTransaction", "qrl_submitTransaction"} {
		if strings.Contains(QRLJs, needle) {
			t.Fatalf("qrl extension still contains %q", needle)
		}
	}
}
