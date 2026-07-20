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

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEventEmitterArtifactsInSync(t *testing.T) {
	const compilerCommit = "f2e6ae7a59e8dafc23a2f34164fdd26180cec2dd"
	fixtureDir := filepath.Join("..", "tests", "fixtures")
	abiBytes, err := os.ReadFile(filepath.Join(fixtureDir, "EventEmitter.abi"))
	if err != nil {
		t.Fatal(err)
	}
	binBytes, err := os.ReadFile(filepath.Join(fixtureDir, "EventEmitter.bin"))
	if err != nil {
		t.Fatal(err)
	}
	jsBytes, err := os.ReadFile(filepath.Join(fixtureDir, "emitter.js"))
	if err != nil {
		t.Fatal(err)
	}

	abiText := strings.TrimSpace(string(abiBytes))
	binText := strings.TrimSpace(string(binBytes))
	jsText := string(jsBytes)
	compilerLine := "// Compiler: github.com/cyyber/hyperion@" + compilerCommit + "\n"
	if strings.Count(jsText, compilerLine) != 1 {
		t.Fatalf("emitter.js does not identify the pinned Hyperion compiler %s", compilerCommit)
	}

	// abigen deliberately removes spaces while embedding ABI JSON. In
	// particular, this normalizes internalType strings such as "struct X".
	wantBindingABI := strings.ReplaceAll(abiText, " ", "")
	if EventEmitterMetaData.ABI != wantBindingABI {
		t.Fatal("generated binding ABI differs from EventEmitter.abi; run go generate ./scripts/local_testnet/goabi")
	}
	if normalizeHex(EventEmitterMetaData.Bin) != normalizeHex(binText) {
		t.Fatal("generated binding bytecode differs from EventEmitter.bin; run go generate ./scripts/local_testnet/goabi")
	}

	abiLine := "  abi: " + abiText + ",\n"
	binLine := "  bin: \"0x" + binText + "\"\n"
	if strings.Count(jsText, abiLine) != 1 || strings.Count(jsText, binLine) != 1 {
		t.Fatal("emitter.js differs from EventEmitter.abi/EventEmitter.bin; run go run generate_emitter_js.go in tests/fixtures")
	}
}

func TestEventEmitterStorageLayoutUsesVM64Slots(t *testing.T) {
	fixture := filepath.Join("..", "tests", "fixtures", "EventEmitter_storage.json")
	encoded, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	var layout struct {
		Storage []struct {
			Label string `json:"label"`
			Slot  string `json:"slot"`
			Type  string `json:"type"`
		} `json:"storage"`
		Types map[string]struct {
			NumberOfBytes string `json:"numberOfBytes"`
		} `json:"types"`
	}
	if err := json.Unmarshal(encoded, &layout); err != nil {
		t.Fatalf("decode storage layout: %v", err)
	}
	want := []struct {
		label    string
		typeName string
	}{
		{label: "storedAmount", typeName: "t_uint512"},
		{label: "storedDelta", typeName: "t_int512"},
		{label: "storedTag", typeName: "t_bytes64"},
		{label: "storedRecipient", typeName: "t_address"},
		{label: "storedPayload", typeName: "t_bytes_storage"},
		{label: "storedNote", typeName: "t_string_storage"},
		{label: "storedEnabled", typeName: "t_bool"},
	}
	if len(layout.Storage) != len(want) {
		t.Fatalf("storage entry count = %d, want %d", len(layout.Storage), len(want))
	}
	for i, expected := range want {
		entry := layout.Storage[i]
		if entry.Label != expected.label || entry.Slot != strconv.Itoa(i) || entry.Type != expected.typeName {
			t.Fatalf("storage entry %d = %+v, want label=%s slot=%d type=%s", i, entry, expected.label, i, expected.typeName)
		}
	}
	for _, typeName := range []string{"t_uint512", "t_int512", "t_bytes64", "t_address", "t_bytes_storage", "t_string_storage"} {
		if got := layout.Types[typeName].NumberOfBytes; got != "64" {
			t.Fatalf("%s width = %q, want 64", typeName, got)
		}
	}
}
