// Copyright 2026 The go-QRL Authors
// This file is part of the go-QRL library.
//
// The go-QRL library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-QRL library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-QRL library. If not, see <http://www.gnu.org/licenses/>.

package web3ext

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	qrljsre "github.com/theQRL/go-qrl/internal/jsre"
	"github.com/theQRL/go-qrl/internal/jsre/deps"
)

func TestQRLGetLogsFormatsVM64Topics(t *testing.T) {
	t.Parallel()

	re := qrljsre.New("", io.Discard)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}
	if _, err := re.Run(`
var capturedOptions = null;
var provider = {
  send: function(payload) {
    if (payload.method === "qrl_getLogs") {
      capturedOptions = payload.params[0];
    }
    return {jsonrpc: "2.0", id: payload.id, result: []};
  },
  sendAsync: function(payload, cb) {
    if (payload.method === "qrl_getLogs") {
      capturedOptions = payload.params[0];
    }
    cb(null, {jsonrpc: "2.0", id: payload.id, result: []});
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
`); err != nil {
		t.Fatalf("init web3: %v", err)
	}
	if err := re.Compile("qrl.js", QRLJs); err != nil {
		t.Fatalf("compile qrl.js: %v", err)
	}

	const address = "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
	value, err := re.Run(fmt.Sprintf(`
web3.qrl.getLogs({
  fromBlock: "0x1",
  toBlock: "latest",
  address: %q,
  topics: ["0xbb", null, ["0xcc", "hello"]]
});
JSON.stringify(capturedOptions);
`, address))
	if err != nil {
		t.Fatalf("run getLogs script: %v", err)
	}
	var got struct {
		FromBlock string `json:"fromBlock"`
		ToBlock   string `json:"toBlock"`
		Address   string `json:"address"`
		Topics    []any  `json:"topics"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode captured options %q: %v", value.String(), err)
	}
	if got.FromBlock != "0x1" || got.ToBlock != "latest" || got.Address != address {
		t.Fatalf("unexpected filter fields: %#v", got)
	}
	if len(got.Topics) != 3 {
		t.Fatalf("topic count mismatch: have %d want 3", len(got.Topics))
	}
	if got.Topics[0] != vm64Topic("bb") {
		t.Fatalf("short hex topic mismatch: have %#v", got.Topics[0])
	}
	if got.Topics[1] != nil {
		t.Fatalf("wildcard topic mismatch: have %#v", got.Topics[1])
	}
	orTopics, ok := got.Topics[2].([]any)
	if !ok || len(orTopics) != 2 {
		t.Fatalf("OR topic mismatch: %#v", got.Topics[2])
	}
	if orTopics[0] != vm64Topic("cc") {
		t.Fatalf("OR hex topic mismatch: have %#v", orTopics[0])
	}
	if orTopics[1] != vm64Topic("68656c6c6f") {
		t.Fatalf("string topic mismatch: have %#v", orTopics[1])
	}

	if _, err := re.Run(`web3.qrl.getLogs({topics: ["0x` + strings.Repeat("1", 129) + `"]});`); err == nil {
		t.Fatal("expected over-wide topic to be rejected")
	}
	if _, err := re.Run(`web3.qrl.getLogs({topics: ["0xb"]});`); err == nil {
		t.Fatal("expected odd-nibble topic hex to be rejected")
	}
	if _, err := re.Run(`web3.qrl.getLogs({topics: ["0xzz"]});`); err == nil {
		t.Fatal("expected invalid topic hex to be rejected")
	}
}

func vm64Topic(hex string) string {
	return "0x" + strings.Repeat("0", 128-len(hex)) + hex
}
