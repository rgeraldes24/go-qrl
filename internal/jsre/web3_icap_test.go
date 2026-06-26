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
	"os"
	"testing"

	"github.com/theQRL/go-qrl/internal/jsre/deps"
)

func TestEmbeddedWeb3UnsupportedICAPIBANAPIsRemoved(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	value, err := re.Run(`
var Web3 = require("web3");
var web3 = new Web3();
JSON.stringify({
  isIBAN: typeof web3.isIBAN,
  fromICAP: typeof web3.fromICAP,
  sendIBANTransaction: typeof web3.qrl.sendIBANTransaction,
  icapNamereg: typeof web3.qrl.icapNamereg
});
`)
	if err != nil {
		t.Fatalf("run ICAP/IBAN surface script: %v", err)
	}
	var got struct {
		IsIBAN              string `json:"isIBAN"`
		FromICAP            string `json:"fromICAP"`
		SendIBANTransaction string `json:"sendIBANTransaction"`
		IcapNamereg         string `json:"icapNamereg"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	if got.IsIBAN != "undefined" || got.FromICAP != "undefined" ||
		got.SendIBANTransaction != "undefined" || got.IcapNamereg != "undefined" {
		t.Fatalf("unsupported ICAP/IBAN APIs should be absent, got %#v", got)
	}
}
