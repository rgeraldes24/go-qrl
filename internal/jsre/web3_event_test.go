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

func TestEmbeddedWeb3EventTopicAlignment(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	highBitWord := "8" + strings.Repeat("0", common.LogTopicLength*2-1)
	negativeTwoWord := strings.Repeat("f", common.LogTopicLength*2-1) + "e"
	fixedBytes1 := "ab"
	fixedBytes32 := strings.Repeat("bc", 32)
	fixedBytes33 := strings.Repeat("cd", 33)
	fixedBytes64 := strings.Repeat("de", common.LogTopicLength)

	tests := []struct {
		name        string
		typ         string
		value       string
		wantTopics  []string
		wantDecoded string
	}{
		{
			name:        "uint512 high bit",
			typ:         "uint512",
			value:       `"0x` + highBitWord + `"`,
			wantTopics:  []string{"0x" + highBitWord},
			wantDecoded: highBitWord,
		},
		{
			name:        "positive int512",
			typ:         "int512",
			value:       "2",
			wantTopics:  []string{"0x" + abiWordHex(2)},
			wantDecoded: "2",
		},
		{
			name:        "negative int512 sign extension",
			typ:         "int512",
			value:       "-2",
			wantTopics:  []string{"0x" + negativeTwoWord},
			wantDecoded: "-2",
		},
		{
			name:        "bool true",
			typ:         "bool",
			value:       "true",
			wantTopics:  []string{"0x" + abiWordHex(1)},
			wantDecoded: "true",
		},
		{
			name:        "bool false",
			typ:         "bool",
			value:       "false",
			wantTopics:  []string{"0x" + abiWordHex(0)},
			wantDecoded: "false",
		},
		{
			name:        "bytes1 left alignment",
			typ:         "bytes1",
			value:       `"0x` + fixedBytes1 + `"`,
			wantTopics:  []string{"0x" + fixedBytes1 + strings.Repeat("0", (common.LogTopicLength-1)*2)},
			wantDecoded: "0x" + fixedBytes1,
		},
		{
			name:        "bytes32 left alignment",
			typ:         "bytes32",
			value:       `"0x` + fixedBytes32 + `"`,
			wantTopics:  []string{"0x" + fixedBytes32 + strings.Repeat("0", (common.LogTopicLength-32)*2)},
			wantDecoded: "0x" + fixedBytes32,
		},
		{
			name:        "bytes33 left alignment",
			typ:         "bytes33",
			value:       `"0x` + fixedBytes33 + `"`,
			wantTopics:  []string{"0x" + fixedBytes33 + strings.Repeat("0", (common.LogTopicLength-33)*2)},
			wantDecoded: "0x" + fixedBytes33,
		},
		{
			name:        "bytes64 full width",
			typ:         "bytes64",
			value:       `"0x` + fixedBytes64 + `"`,
			wantTopics:  []string{"0x" + fixedBytes64},
			wantDecoded: "0x" + fixedBytes64,
		},
		{
			name:       "OR filter array",
			typ:        "uint512",
			value:      "[2, 3]",
			wantTopics: []string{"0x" + abiWordHex(2), "0x" + abiWordHex(3)},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := fmt.Sprintf(eventCaptureProvider+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([{
  anonymous: false,
  inputs: [{indexed: true, name: "value", type: %q}],
  name: "Value",
  type: "event"
}]).at(%q);

var filter = contract.Value({value: %s});
var indexedTopic = captured[0].topics[1];
var topics = indexedTopic instanceof Array ? indexedTopic : [indexedTopic];
var decodedValue = null;
if (!(indexedTopic instanceof Array)) {
  var decoded = filter.formatter({
    address: %q,
    topics: [captured[0].topics[0], indexedTopic],
    data: "0x",
    blockNumber: "0x1",
    transactionIndex: "0x0",
    logIndex: "0x0"
  });
  decodedValue = decoded.args.value.toString(16);
}

JSON.stringify({topics: topics, decoded: decodedValue});
`, test.typ, contractAddress, test.value, contractAddress)
			value, err := re.Run(script)
			if err != nil {
				t.Fatalf("run event topic alignment script: %v", err)
			}
			var got struct {
				Topics  []string `json:"topics"`
				Decoded *string  `json:"decoded"`
			}
			if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
				t.Fatalf("decode event topic result %q: %v", value.String(), err)
			}
			if strings.Join(got.Topics, ",") != strings.Join(test.wantTopics, ",") {
				t.Fatalf("event topics mismatch:\nhave %v\nwant %v", got.Topics, test.wantTopics)
			}
			if test.wantDecoded == "" {
				if got.Decoded != nil {
					t.Fatalf("OR-filter topic unexpectedly decoded as %q", *got.Decoded)
				}
				return
			}
			if got.Decoded == nil || *got.Decoded != test.wantDecoded {
				t.Fatalf("decoded event value mismatch: have %v, want %q", got.Decoded, test.wantDecoded)
			}
		})
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
