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
	"encoding/hex"
	"encoding/json"
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/accounts/abi/bind"
)

func TestGeneratedBindingProjectionAndFullABIs(t *testing.T) {
	fixtureDir := filepath.Join("..", "..", "testdata", "contracts")
	projection := readArtifactFile(t, fixtureDir, "EventEmitterBindingSmoke.abi")
	fullEmitter := readArtifactFile(t, fixtureDir, "EventEmitter.abi")

	var compactProjection bytes.Buffer
	if err := json.Compact(&compactProjection, projection); err != nil {
		t.Fatalf("compact generated-binding ABI projection: %v", err)
	}
	if EventEmitterBindingSmokeMetaData.ABI != compactProjection.String() {
		t.Fatal("generated smoke binding differs from EventEmitterBindingSmoke.abi; run go -C scripts/testing/e2e generate ./suites/goabi")
	}
	emitterBytecode := strings.TrimSpace(string(readArtifactFile(t, fixtureDir, "EventEmitter.bin")))
	if !bytes.Equal(decodeArtifactHex(t, EventEmitterBindingSmokeMetaData.Bin), decodeArtifactHex(t, emitterBytecode)) {
		t.Fatal("generated smoke binding bytecode differs from EventEmitter.bin; run go -C scripts/testing/e2e generate ./suites/goabi")
	}
	requireABIProjection(t, projection, fullEmitter)

	for _, name := range []string{"EventEmitter", "AdvancedABI"} {
		t.Run(name+"FullBindingSyntax", func(t *testing.T) {
			abiJSON := string(readArtifactFile(t, fixtureDir, name+".abi"))
			bytecode := strings.TrimSpace(string(readArtifactFile(t, fixtureDir, name+".bin")))
			generated, err := bind.Bind(
				[]string{name},
				[]string{abiJSON},
				[]string{bytecode},
				nil,
				"goabiartifact",
				nil,
				nil,
			)
			if err != nil {
				t.Fatalf("generate full %s binding: %v", name, err)
			}
			if _, err := format.Source([]byte(generated)); err != nil {
				t.Fatalf("format full %s binding: %v", name, err)
			}
		})
	}
}

func decodeArtifactHex(t *testing.T, value string) []byte {
	t.Helper()
	value = strings.TrimPrefix(strings.TrimSpace(value), "0x")
	decoded, err := hex.DecodeString(value)
	if err != nil {
		t.Fatalf("decode artifact bytecode: %v", err)
	}
	return decoded
}

func readArtifactFile(t *testing.T, directory, name string) []byte {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join(directory, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return contents
}

func requireABIProjection(t *testing.T, projection, full []byte) {
	t.Helper()
	var projectedEntries, fullEntries []json.RawMessage
	if err := json.Unmarshal(projection, &projectedEntries); err != nil {
		t.Fatalf("decode ABI projection: %v", err)
	}
	if err := json.Unmarshal(full, &fullEntries); err != nil {
		t.Fatalf("decode full ABI: %v", err)
	}
	fullSet := make(map[string]bool, len(fullEntries))
	for _, entry := range fullEntries {
		fullSet[compactJSON(t, entry)] = true
	}
	for _, entry := range projectedEntries {
		if encoded := compactJSON(t, entry); !fullSet[encoded] {
			t.Fatalf("generated binding ABI entry is not an exact EventEmitter.abi projection: %s", encoded)
		}
	}
}

func compactJSON(t *testing.T, encoded []byte) string {
	t.Helper()
	var compact bytes.Buffer
	if err := json.Compact(&compact, encoded); err != nil {
		t.Fatalf("compact JSON: %v", err)
	}
	return compact.String()
}
