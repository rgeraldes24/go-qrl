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

func TestEmbeddedWeb3UnsupportedAPIsRemoved(t *testing.T) {
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

	value, err := re.Run(`JSON.stringify({
		db: typeof web3.db,
		miner: typeof web3.miner,
		personal: typeof web3.personal,
		shh: typeof web3.shh,
		versionQrl: typeof web3.version.qrl,
		qrlProtocolVersion: typeof web3.qrl.protocolVersion,
		namereg: typeof web3.qrl.namereg,
		compileHyperion: typeof web3.qrl.compile === "undefined" ? "undefined" : typeof web3.qrl.compile.hyperion
	})`)
	if err != nil {
		t.Fatalf("inspect unsupported APIs: %v", err)
	}

	var got map[string]string
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode unsupported API inspection: %v", err)
	}
	for name, typ := range got {
		if typ != "undefined" {
			t.Fatalf("%s is still exported as %s", name, typ)
		}
	}
}
