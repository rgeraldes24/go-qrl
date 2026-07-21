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
// You should have received a copy of the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package jsre

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto"
)

func TestEmbeddedWeb3EventsUseVM64Topics(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	indexedAddress := "Q" + strings.Repeat("a", common.AddressLength*2)
	signatureTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("Transfer(address,uint512)"))).Hex()
	addressTopic := "0x" + strings.Repeat("a", common.LogTopicLength*2)
	amountWord := abiWordHex(2)

	script := fmt.Sprintf(eventCaptureProvider+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([{
  anonymous: false,
  inputs: [
    {indexed: true, name: "from", type: "address"},
    {indexed: false, name: "amount", type: "uint512"}
  ],
  name: "Transfer",
  type: "event"
}]).at(%q);

var filter = contract.Transfer({from: %q});
var log = {
  address: %q,
  topics: [%q, %q],
  data: "0x%s",
  blockNumber: "0x1",
  transactionIndex: "0x0",
  logIndex: "0x0"
};
var decoded = filter.formatter(JSON.parse(JSON.stringify(log)));
var allEventsDecoded = contract.allEvents().formatter(JSON.parse(JSON.stringify(log)));

JSON.stringify({
  topics: captured[0].topics,
  event: decoded.event,
  from: decoded.args.from,
  amount: decoded.args.amount.toString(10),
  allEventsEvent: allEventsDecoded.event,
  signatureIsTopic: web3._extend.utils.isTopic(%q),
  eventIDIsTopic: web3._extend.utils.isTopic(%q)
});
`, contractAddress, indexedAddress, contractAddress, signatureTopic, addressTopic, amountWord, signatureTopic, "0x"+common.Bytes2Hex(crypto.Keccak256([]byte("Transfer(address,uint512)"))))
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run event script: %v", err)
	}
	var got struct {
		Topics           []string `json:"topics"`
		Event            string   `json:"event"`
		From             string   `json:"from"`
		Amount           string   `json:"amount"`
		AllEventsEvent   string   `json:"allEventsEvent"`
		SignatureIsTopic bool     `json:"signatureIsTopic"`
		EventIDIsTopic   bool     `json:"eventIDIsTopic"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode event result %q: %v", value.String(), err)
	}
	wantTopics := []string{signatureTopic, addressTopic}
	if strings.Join(got.Topics, ",") != strings.Join(wantTopics, ",") {
		t.Fatalf("event topics mismatch:\nhave %v\nwant %v", got.Topics, wantTopics)
	}
	if got.Event != "Transfer" || got.AllEventsEvent != "Transfer" || got.From != indexedAddress || got.Amount != "2" {
		t.Fatalf("decoded event mismatch: %+v", got)
	}
	if !got.SignatureIsTopic || got.EventIDIsTopic {
		t.Fatalf("topic width validation mismatch: full=%t eventID=%t", got.SignatureIsTopic, got.EventIDIsTopic)
	}
}

func TestEmbeddedWeb3EventTopicAlignment(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	fixedBytes32 := strings.Repeat("ab", 32)
	fixedBytes64 := strings.Repeat("cd", common.LogTopicLength)

	script := fmt.Sprintf(eventCaptureProvider+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([{
  anonymous: false,
  inputs: [{indexed: true, name: "value", type: "uint512"}],
  name: "Unsigned",
  type: "event"
}, {
  anonymous: false,
  inputs: [{indexed: true, name: "value", type: "int512"}],
  name: "Signed",
  type: "event"
}, {
  anonymous: false,
  inputs: [{indexed: true, name: "value", type: "bool"}],
  name: "Flag",
  type: "event"
}, {
  anonymous: false,
  inputs: [{indexed: true, name: "value", type: "bytes32"}],
  name: "FixedBytes32",
  type: "event"
}, {
  anonymous: false,
  inputs: [{indexed: true, name: "value", type: "bytes64"}],
  name: "FixedBytes64",
  type: "event"
}]).at(%q);

contract.Unsigned({value: 2});
contract.Signed({value: 2});
contract.Signed({value: -2});
contract.Flag({value: true});
contract.Flag({value: false});
contract.FixedBytes32({value: "0x%s"});
contract.FixedBytes64({value: "0x%s"});

JSON.stringify(captured.map(function (filter) {
  return filter.topics[1];
}));
`, contractAddress, fixedBytes32, fixedBytes64)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run event topic alignment script: %v", err)
	}
	var got []string
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode event topics %q: %v", value.String(), err)
	}
	want := []string{
		"0x" + abiWordHex(2),
		"0x" + abiWordHex(2),
		"0x" + strings.Repeat("f", common.LogTopicLength*2-1) + "e",
		"0x" + abiWordHex(1),
		"0x" + abiWordHex(0),
		"0x" + fixedBytes32 + strings.Repeat("0", (common.LogTopicLength-32)*2),
		"0x" + fixedBytes64,
	}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("event topic alignment mismatch:\nhave %v\nwant %v", got, want)
	}
}

const eventCaptureProvider = `
var captured = [];
var provider = {
  send: function(payload) {
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    if (payload.method === "qrl_newFilter") {
      captured.push(payload.params[0]);
    }
    cb(null, {jsonrpc: "2.0", id: payload.id, result: "0x1"});
  }
};
`
