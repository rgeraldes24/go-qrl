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

func TestQRLGetLogsFormatsVM64Values(t *testing.T) {
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
    return {jsonrpc: "2.0", id: payload.id, result: [{
      blockNumber: "0x2",
      transactionIndex: "0x1",
      logIndex: "0x0",
      data: "0x",
      topics: []
    }]};
  },
  sendAsync: function(payload, cb) {
    cb(null, this.send(payload));
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
	hashHex := strings.Repeat("ab", 32)
	fullTopic := strings.Repeat("cd", 64)
	value, err := re.Run(fmt.Sprintf(`
var logs = web3.qrl.getLogs({
  fromBlock: 1,
  toBlock: "latest",
  address: %q,
  topics: ["0xbb", null, ["0xcc", "hello"], "0x%s", "0x%s"]
});
JSON.stringify({options: capturedOptions, logs: logs});
`, address, hashHex, fullTopic))
	if err != nil {
		t.Fatalf("run getLogs script: %v", err)
	}
	var got struct {
		Options struct {
			FromBlock string `json:"fromBlock"`
			ToBlock   string `json:"toBlock"`
			Address   string `json:"address"`
			Topics    []any  `json:"topics"`
		} `json:"options"`
		Logs []struct {
			BlockNumber      int `json:"blockNumber"`
			TransactionIndex int `json:"transactionIndex"`
			LogIndex         int `json:"logIndex"`
		} `json:"logs"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode getLogs result %q: %v", value.String(), err)
	}
	if got.Options.FromBlock != "0x1" || got.Options.ToBlock != "latest" || got.Options.Address != address {
		t.Fatalf("unexpected filter fields: %#v", got.Options)
	}
	if len(got.Options.Topics) != 5 {
		t.Fatalf("topic count mismatch: have %d want 5", len(got.Options.Topics))
	}
	if got.Options.Topics[0] != vm64Topic("bb") || got.Options.Topics[1] != nil {
		t.Fatalf("short or wildcard topic mismatch: %#v", got.Options.Topics[:2])
	}
	orTopics, ok := got.Options.Topics[2].([]any)
	if !ok || len(orTopics) != 2 || orTopics[0] != vm64Topic("cc") || orTopics[1] != vm64Topic("68656c6c6f") {
		t.Fatalf("OR topic mismatch: %#v", got.Options.Topics[2])
	}
	if got.Options.Topics[3] != vm64HashTopic(hashHex) {
		t.Fatalf("hash topic mismatch: %#v", got.Options.Topics[3])
	}
	if got.Options.Topics[4] != "0x"+fullTopic {
		t.Fatalf("full topic mismatch: %#v", got.Options.Topics[4])
	}
	if len(got.Logs) != 1 || got.Logs[0].BlockNumber != 2 || got.Logs[0].TransactionIndex != 1 || got.Logs[0].LogIndex != 0 {
		t.Fatalf("output log formatting mismatch: %#v", got.Logs)
	}
}

func vm64Topic(hex string) string {
	return "0x" + strings.Repeat("0", 128-len(hex)) + hex
}

func vm64HashTopic(hex string) string {
	return "0x" + hex + strings.Repeat("0", 128-len(hex))
}
