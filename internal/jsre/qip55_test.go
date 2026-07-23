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
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/internal/jsre/deps"
)

func TestEmbeddedWeb3QIP55Checksum(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}
	if _, err := re.Run("var Web3 = require('web3'); var web3 = new Web3();"); err != nil {
		t.Fatalf("init web3: %v", err)
	}

	lower := "Q" + strings.Repeat("0", 88) + "b26f2b342aab24bcf63ea218c6a9274d30ab9a15"
	addr, err := common.NewAddressFromString(lower)
	if err != nil {
		t.Fatal(err)
	}
	want := addr.Hex()

	gotValue, err := re.Run("web3.toChecksumAddress('" + lower + "')")
	if err != nil {
		t.Fatalf("toChecksumAddress: %v", err)
	}
	if got := gotValue.String(); got != want {
		t.Fatalf("toChecksumAddress mismatch: got %s want %s", got, want)
	}

	validValue, err := re.Run("web3.isChecksumAddress('" + want + "')")
	if err != nil {
		t.Fatalf("isChecksumAddress(valid): %v", err)
	}
	if !validValue.ToBoolean() {
		t.Fatal("canonical QIP-55 address was rejected")
	}

	invalidValue, err := re.Run("web3.isChecksumAddress('" + lower + "')")
	if err != nil {
		t.Fatalf("isChecksumAddress(lower): %v", err)
	}
	if invalidValue.ToBoolean() {
		t.Fatal("lowercase compatibility address passed strict checksum validation")
	}
}

func TestEmbeddedWeb3RejectsLowercaseQPrefix(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)

	plain := "Q" + strings.Repeat("0", 88) + "b26f2b342aab24bcf63ea218c6a9274d30ab9a15"
	checksummed := common.MustParseAddress(plain).Hex()
	lowerPrefix := "q" + plain[1:]

	script := fmt.Sprintf(`
currentOutput = "0x0";
var formatterError = "";
try {
  web3.qrl.getBalance(%q);
} catch (err) {
  formatterError = err.message;
}
var rejectedBeforeProvider = lastPayload === null;
web3.qrl.getBalance(%q);
var plainPayload = lastPayload.params[0];
web3.qrl.getBalance(%q);
var checksummedPayload = lastPayload.params[0];
JSON.stringify({
  plain: web3.isAddress(%q),
  checksummed: web3.isAddress(%q),
  lowercasePrefix: web3.isAddress(%q),
  strictLowercasePrefix: web3._extend.utils.isStrictAddress(%q),
  rejectedBeforeProvider: rejectedBeforeProvider,
  plainPayload: plainPayload,
  checksummedPayload: checksummedPayload,
  formatterError: formatterError
});
`, lowerPrefix, plain, checksummed, plain, checksummed, lowerPrefix, lowerPrefix)

	var result struct {
		Plain                  bool   `json:"plain"`
		Checksummed            bool   `json:"checksummed"`
		LowercasePrefix        bool   `json:"lowercasePrefix"`
		StrictLowercasePrefix  bool   `json:"strictLowercasePrefix"`
		RejectedBeforeProvider bool   `json:"rejectedBeforeProvider"`
		PlainPayload           string `json:"plainPayload"`
		ChecksummedPayload     string `json:"checksummedPayload"`
		FormatterError         string `json:"formatterError"`
	}
	runWeb3JSON(t, re, web3CallProvider, script, &result)

	if !result.Plain {
		t.Fatal("full-width uppercase-Q address was rejected")
	}
	if !result.Checksummed {
		t.Fatal("canonical QIP-55 address was rejected")
	}
	if result.LowercasePrefix || result.StrictLowercasePrefix {
		t.Fatal("lowercase-q address was accepted")
	}
	if result.FormatterError == "" {
		t.Fatal("input address formatter accepted lowercase-q address")
	}
	if !result.RejectedBeforeProvider {
		t.Fatal("provider received a request for lowercase-q address")
	}
	if result.PlainPayload != plain || result.ChecksummedPayload != checksummed {
		t.Fatal("input address formatter changed a valid uppercase-Q address")
	}
}
