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
	signatureTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("Transfer(address,string,bytes,uint512)"))).Hex()
	addressTopic := "0x" + strings.Repeat("a", common.LogTopicLength*2)
	labelTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("hello"))).Hex()
	payloadTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte{0xab, 0xcd})).Hex()
	emptyPayloadTopic := common.HashToLogTopic(crypto.Keccak256Hash(nil)).Hex()
	amountWord := abiWordHex(2)

	script := fmt.Sprintf(eventCaptureProvider+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([{
  anonymous: false,
  inputs: [
    {indexed: true, name: "from", type: "address"},
    {indexed: true, name: "label", type: "string"},
    {indexed: true, name: "payload", type: "bytes"},
    {indexed: false, name: "amount", type: "uint512"}
  ],
  name: "Transfer",
  type: "event"
}]).at(%q);

var filter = contract.Transfer({from: %q, label: "hello", payload: "0xabcd"});
contract.Transfer({payload: "0x"});
var log = {
  address: %q,
  topics: [%q, %q, %q, %q],
  data: "0x%s",
  blockNumber: "0x1",
  transactionIndex: "0x0",
  logIndex: "0x0"
};
var decoded = filter.formatter(JSON.parse(JSON.stringify(log)));
var allEventsDecoded = contract.allEvents().formatter(JSON.parse(JSON.stringify(log)));

JSON.stringify({
  topics: captured[0].topics,
  emptyPayloadTopic: captured[1].topics[3],
  event: decoded.event,
  from: decoded.args.from,
  label: decoded.args.label,
  payload: decoded.args.payload,
  amount: decoded.args.amount.toString(10),
  allEventsEvent: allEventsDecoded.event,
  signatureIsTopic: web3._extend.utils.isTopic(%q),
  eventIDIsTopic: web3._extend.utils.isTopic(%q)
});
`, contractAddress, indexedAddress, contractAddress, signatureTopic, addressTopic, labelTopic, payloadTopic, amountWord, signatureTopic, "0x"+common.Bytes2Hex(crypto.Keccak256([]byte("Transfer(address,string,bytes,uint512)"))))
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run event script: %v", err)
	}
	var got struct {
		Topics            []string `json:"topics"`
		EmptyPayloadTopic string   `json:"emptyPayloadTopic"`
		Event             string   `json:"event"`
		From              string   `json:"from"`
		Label             string   `json:"label"`
		Payload           string   `json:"payload"`
		Amount            string   `json:"amount"`
		AllEventsEvent    string   `json:"allEventsEvent"`
		SignatureIsTopic  bool     `json:"signatureIsTopic"`
		EventIDIsTopic    bool     `json:"eventIDIsTopic"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode event result %q: %v", value.String(), err)
	}
	wantTopics := []string{signatureTopic, addressTopic, labelTopic, payloadTopic}
	if strings.Join(got.Topics, ",") != strings.Join(wantTopics, ",") {
		t.Fatalf("event topics mismatch:\nhave %v\nwant %v", got.Topics, wantTopics)
	}
	if got.EmptyPayloadTopic != emptyPayloadTopic {
		t.Fatalf("empty bytes topic mismatch: have %s, want %s", got.EmptyPayloadTopic, emptyPayloadTopic)
	}
	if got.Event != "Transfer" || got.AllEventsEvent != "Transfer" || got.From != indexedAddress || got.Label != labelTopic || got.Payload != payloadTopic || got.Amount != "2" {
		t.Fatalf("decoded event mismatch: %+v", got)
	}
	if !got.SignatureIsTopic || got.EventIDIsTopic {
		t.Fatalf("topic width validation mismatch: full=%t eventID=%t", got.SignatureIsTopic, got.EventIDIsTopic)
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
