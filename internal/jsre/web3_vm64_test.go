// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.
//
// go-qrl is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-qrl is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.

package jsre

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/internal/jsre/deps"
)

func newEmbeddedWeb3(t *testing.T) *JSRE {
	t.Helper()

	re := New("", os.Stdout)
	t.Cleanup(func() { re.Stop(false) })

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}
	if _, err := re.Run("var Web3 = require('web3'); var web3 = new Web3();"); err != nil {
		t.Fatalf("init web3: %v", err)
	}
	return re
}

func vm64Word(hex string) string {
	return strings.Repeat("0", 128-len(hex)) + hex
}

func malformedVM64OffsetWord(lowHex string) string {
	return "01" + strings.Repeat("00", 55) + strings.Repeat("0", 16-len(lowHex)) + lowHex
}

func TestEmbeddedWeb3VM64DoesNotExposeIBAN(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	gotValue, err := re.Run(`
		[
			typeof web3.isIBAN,
			typeof web3.fromICAP,
			typeof web3.qrl.iban,
			typeof web3.qrl.sendIBANTransaction,
			typeof web3.qrl.icapNamereg
		].join(",")
	`)
	if err != nil {
		t.Fatalf("inspect IBAN/ICAP helpers: %v", err)
	}
	if got, want := gotValue.String(), "undefined,undefined,undefined,undefined,undefined"; got != want {
		t.Fatalf("legacy IBAN/ICAP helpers exposed: got %q want %q", got, want)
	}
}

func TestEmbeddedWeb3VM64ContractABIEncoding(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	addressHex := strings.Repeat("0", 126) + "ab"
	address := "Q" + addressHex
	script := fmt.Sprintf(`
		var abi = [{
			"type": "function",
			"name": "set",
			"constant": false,
			"payable": false,
			"inputs": [
				{"name": "u", "type": "uint512"},
				{"name": "b", "type": "bool"},
				{"name": "a", "type": "address"},
				{"name": "s", "type": "string"}
			],
			"outputs": []
		}];
		var c = web3.qrl.contract(abi).at(%q);
		c.set.getData("1", true, %q, "hi").slice(10);
	`, address, address)

	gotValue, err := re.Run(script)
	if err != nil {
		t.Fatalf("encode contract call: %v", err)
	}
	got := gotValue.String()
	want := strings.Join([]string{
		vm64Word("1"),
		vm64Word("1"),
		addressHex,
		vm64Word("100"),
		vm64Word("2"),
		"6869" + strings.Repeat("0", 124),
	}, "")
	if got != want {
		t.Fatalf("VM64 ABI payload mismatch:\ngot  %s\nwant %s", got, want)
	}
}

func TestEmbeddedWeb3VM64IntegerWidthEnforcement(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	address := "Q" + strings.Repeat("0", 128)
	script := fmt.Sprintf(`
		var abi = [{
			"type": "function",
			"name": "set",
			"constant": false,
			"payable": false,
			"inputs": [{"name": "u", "type": "uint256"}],
			"outputs": []
		}];
		var c = web3.qrl.contract(abi).at(%q);
		try {
			c.set.getData("0x1" + new Array(65).join("0"));
			"missing error";
		} catch (err) {
			err.message;
		}
	`, address)

	gotValue, err := re.Run(script)
	if err != nil {
		t.Fatalf("run uint256 overflow script: %v", err)
	}
	if got := gotValue.String(); !strings.Contains(got, "value exceeds uint256") {
		t.Fatalf("uint256 overflow error mismatch: %q", got)
	}
}

func TestEmbeddedWeb3VM64RejectsMalformedOffsetWords(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	address := "Q" + strings.Repeat("0", 128)
	tests := []struct {
		name     string
		payload  string
		wantPart string
	}{
		{
			name:     "offset",
			payload:  "0x" + malformedVM64OffsetWord("40") + vm64Word("1") + "ff" + strings.Repeat("0", 126),
			wantPart: "dynamic offset",
		},
		{
			name:     "length",
			payload:  "0x" + vm64Word("40") + malformedVM64OffsetWord("1") + "ff" + strings.Repeat("0", 126),
			wantPart: "dynamic length",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := fmt.Sprintf(`
				var abi = [{
					"type": "function",
					"name": "get",
					"constant": true,
					"payable": false,
					"inputs": [],
					"outputs": [{"name": "b", "type": "bytes"}]
				}];
				var c = web3.qrl.contract(abi).at(%q);
				try {
					c.get.request().format(%q);
					"missing error";
				} catch (err) {
					err.message;
				}
			`, address, test.payload)

			gotValue, err := re.Run(script)
			if err != nil {
				t.Fatalf("run malformed offset script: %v", err)
			}
			got := gotValue.String()
			if !strings.Contains(got, test.wantPart) || !strings.Contains(got, "exceeds uint64") {
				t.Fatalf("malformed offset error mismatch: %q", got)
			}
		})
	}
}

func TestEmbeddedWeb3VM64EventFilterTopics(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	addressHex := strings.Repeat("0", 126) + "cd"
	address := "Q" + addressHex
	script := fmt.Sprintf(`
		var abi = [{
			"type": "event",
			"name": "Transfer",
			"anonymous": false,
			"inputs": [
				{"name": "from", "type": "address", "indexed": true},
				{"name": "amount", "type": "uint512", "indexed": true}
			]
		}];
		var c = web3.qrl.contract(abi).at(%q);
		JSON.stringify(c.Transfer({"from": %q, "amount": "1"}).options.topics);
	`, address, address)

	gotValue, err := re.Run(script)
	if err != nil {
		t.Fatalf("encode event topics: %v", err)
	}
	var got []string
	if err := json.Unmarshal([]byte(gotValue.String()), &got); err != nil {
		t.Fatalf("decode topics json: %v", err)
	}
	eventID := crypto.Keccak256Hash([]byte("Transfer(address,uint512)")).Hex()[2:]
	want := []string{
		"0x" + vm64Word(eventID),
		"0x" + addressHex,
		"0x" + vm64Word("1"),
	}
	if len(got) != len(want) {
		t.Fatalf("topic count mismatch: got %d want %d (%v)", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("topic %d mismatch:\ngot  %s\nwant %s", i, got[i], want[i])
		}
	}
}
