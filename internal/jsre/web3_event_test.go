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
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/internal/jsre/deps"
)

func TestEmbeddedWeb3RawFilterTopicFormatting(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3ForEvents(t)

	eventID := crypto.Keccak256([]byte("Transfer(address,uint512,string)"))
	expectedEventTopic := common.HashToLogTopic(common.BytesToHash(eventID)).Hex()
	expectedAllEventsTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("Ping(address,uint512,string)"))).Hex()
	upperAllEventsTopic := "0X" + strings.ToUpper(expectedAllEventsTopic[2:])
	indexedAddress := "Q" + strings.Repeat("a", common.AddressLength*2)
	expectedIndexedAddressTopic := "0x" + strings.Repeat("a", common.AddressLength*2)
	var indexedValueTopic common.LogTopic
	indexedValueTopic[common.LogTopicLength-1] = 2
	expectedIndexedValueTopic := indexedValueTopic.Hex()
	expectedIndexedLabelTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("hello"))).Hex()
	expectedRawStringFilterValue := "0x" + common.Bytes2Hex([]byte("hello"))
	expectedDecodedAddress := common.MustParseAddress(indexedAddress).Hex()
	address := "Q" + strings.Repeat("0", common.AddressLength*2)

	script := fmt.Sprintf(eventCaptureProviderJS+`
var Web3 = require("web3");
var web3 = new Web3(provider);

var rawTopic = %q;
web3.qrl.filter({
  fromBlock: "0x0",
  toBlock: "latest",
  address: %q,
  topics: [rawTopic, null, [rawTopic, "hello"]]
});

var contractAbi = [{
  anonymous: false,
  inputs: [
    {indexed: true, name: "from", type: "address"},
    {indexed: true, name: "value", type: "uint512"},
    {indexed: true, name: "label", type: "string"}
  ],
  name: "Transfer",
  type: "event"
}];
web3.qrl.contract(contractAbi).at(%q).Transfer({from: %q, value: 2, label: "hello"});

var allEventsAbi = [{
  anonymous: false,
  inputs: [
    {indexed: true, name: "from", type: "address"},
    {indexed: true, name: "value", type: "uint512"},
    {indexed: true, name: "label", type: "string"}
  ],
  name: "Ping",
  type: "event"
}];
var allEventsFilter = web3.qrl.contract(allEventsAbi).at(%q).allEvents();

var allEventsDecoded = allEventsFilter.formatter({
  address: %q,
  topics: [%q, %q, %q, %q],
  data: "0x",
  blockNumber: "0x1",
  transactionIndex: "0x0",
  logIndex: "0x0"
});

JSON.stringify({
  captured: captured[0].topics,
  eventCaptured: captured[1].topics,
  allEventsDecodedEvent: allEventsDecoded.event,
  allEventsDecodedFrom: allEventsDecoded.args.from,
  allEventsDecodedFromIsChecksum: web3.isChecksumAddress(allEventsDecoded.args.from),
  allEventsDecodedValue: allEventsDecoded.args.value.toString(10),
  allEventsDecodedLabel: allEventsDecoded.args.label
});
`, expectedEventTopic, address, address, indexedAddress, address, address, upperAllEventsTopic, expectedIndexedAddressTopic, expectedIndexedValueTopic, expectedIndexedLabelTopic)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run raw topic script: %v", err)
	}
	var got struct {
		Captured              []any  `json:"captured"`
		EventCaptured         []any  `json:"eventCaptured"`
		AllEventsDecodedEvent string `json:"allEventsDecodedEvent"`
		AllEventsDecodedFrom  string `json:"allEventsDecodedFrom"`
		AllEventsFromChecksum bool   `json:"allEventsDecodedFromIsChecksum"`
		AllEventsDecodedValue string `json:"allEventsDecodedValue"`
		AllEventsDecodedLabel string `json:"allEventsDecodedLabel"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	expectedRawTopics := []any{expectedEventTopic, nil, []any{expectedEventTopic, expectedRawStringFilterValue}}
	if !reflect.DeepEqual(got.Captured, expectedRawTopics) {
		t.Fatalf("captured filter topic mismatch: have %#v want %#v", got.Captured, expectedRawTopics)
	}
	expectedEventTopics := []any{expectedEventTopic, expectedIndexedAddressTopic, expectedIndexedValueTopic, expectedIndexedLabelTopic}
	if !reflect.DeepEqual(got.EventCaptured, expectedEventTopics) {
		t.Fatalf("captured event topic mismatch: have %#v want %#v", got.EventCaptured, expectedEventTopics)
	}
	if got.AllEventsDecodedEvent != "Ping" {
		t.Fatalf("allEvents should decode VM64 signature topic, have event %q", got.AllEventsDecodedEvent)
	}
	if got.AllEventsDecodedFrom != expectedDecodedAddress || !got.AllEventsFromChecksum {
		t.Fatalf("allEvents decoded address is not canonical: have %q want %q", got.AllEventsDecodedFrom, expectedDecodedAddress)
	}
	if got.AllEventsDecodedValue != "2" {
		t.Fatalf("allEvents decoded value mismatch: have %q want 2", got.AllEventsDecodedValue)
	}
	if got.AllEventsDecodedLabel != expectedIndexedLabelTopic {
		t.Fatalf("allEvents decoded label topic mismatch: have %q want %q", got.AllEventsDecodedLabel, expectedIndexedLabelTopic)
	}

	eventIDHex := "0x" + common.Bytes2Hex(eventID)
	if len(eventIDHex) != 66 {
		t.Fatalf("event ID should be 32 bytes, have %q", eventIDHex)
	}

	upperCaseTopic := "0x" + strings.Repeat("AA", common.LogTopicLength)
	mixedCaseTopic := "0x" + strings.Repeat("Aa", common.LogTopicLength)
	upperPrefixTopic := "0X" + strings.Repeat("aa", common.LogTopicLength)

	assertEventWeb3IsTopic(t, re, eventIDHex, false, "event ID")
	assertEventWeb3IsTopic(t, re, expectedEventTopic, true, "full-width VM64 event topic")
	assertEventWeb3IsTopic(t, re, upperCaseTopic, true, "uppercase 64-byte topic")
	assertEventWeb3IsTopic(t, re, mixedCaseTopic, true, "mixed-case 64-byte topic")
	assertEventWeb3IsTopic(t, re, upperPrefixTopic, true, "uppercase 0X-prefixed topic")
}

func TestEmbeddedWeb3CompositeEventFiltersRequirePrecomputedTopics(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3ForEvents(t)
	address := "Q" + strings.Repeat("0", common.AddressLength*2)
	owner := "Q" + strings.Repeat("a", common.AddressLength*2)
	tupleTopic := "0x" + strings.Repeat("ab", common.LogTopicLength)
	arrayTopic := "0x" + strings.Repeat("cd", common.LogTopicLength)
	tupleSignatureTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("Composite((uint512,address))"))).Hex()
	arraySignatureTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("Values(uint512[2])"))).Hex()

	script := fmt.Sprintf(eventCaptureProviderJS+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([
  {
    anonymous: false,
    inputs: [{indexed: true, name: "value", type: "tuple", components: [{name: "count", type: "uint512"}, {name: "owner", type: "address"}]}],
    name: "Composite",
    type: "event"
  },
  {
    anonymous: false,
    inputs: [{indexed: true, name: "value", type: "uint512[2]"}],
    name: "Values",
    type: "event"
  }
]).at(%q);

function rejects(fn) {
  try {
    fn();
    return false;
  } catch (err) {
    return true;
  }
}

var rejectsTupleValue = rejects(function() { contract.Composite({value: {count: 1, owner: %q}}); });
var rejectsArrayValue = rejects(function() { contract.Values({value: [1, 2]}); });
contract.Composite({value: %q});
contract.Values({value: %q});
contract.Composite({value: [%q, %q]});
contract.Composite({value: [%q, null]});

var decoded = contract.allEvents().formatter({
  address: %q,
  topics: [%q, %q],
  data: "0x",
  blockNumber: "0x1",
  transactionIndex: "0x0",
  logIndex: "0x0"
});

JSON.stringify({
  captured: captured.slice(0, 2).map(function (options) { return options.topics; }),
  compositeOrTopics: captured[2].topics,
  compositeWildcardTopics: captured[3].topics,
  rejectsTupleValue: rejectsTupleValue,
  rejectsArrayValue: rejectsArrayValue,
  decodedEvent: decoded.event,
  decodedTupleTopic: decoded.args.value
});
`, address, owner, tupleTopic, arrayTopic, tupleTopic, arrayTopic, tupleTopic, address, tupleSignatureTopic, tupleTopic)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run composite event script: %v", err)
	}
	var got struct {
		Captured          [][]string `json:"captured"`
		CompositeOrTopics []any      `json:"compositeOrTopics"`
		WildcardTopics    []any      `json:"compositeWildcardTopics"`
		RejectsTupleValue bool       `json:"rejectsTupleValue"`
		RejectsArrayValue bool       `json:"rejectsArrayValue"`
		DecodedEvent      string     `json:"decodedEvent"`
		DecodedTupleTopic string     `json:"decodedTupleTopic"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode composite event result %q: %v", value.String(), err)
	}
	wantCaptured := [][]string{{tupleSignatureTopic, tupleTopic}, {arraySignatureTopic, arrayTopic}}
	if len(got.Captured) < len(wantCaptured) {
		t.Fatalf("missing composite event filters: have %#v want at least %#v", got.Captured, wantCaptured)
	}
	if !reflect.DeepEqual(got.Captured[:2], wantCaptured) {
		t.Fatalf("composite event topics mismatch: have %#v want %#v", got.Captured[:2], wantCaptured)
	}
	wantOrTopics := []any{tupleSignatureTopic, []any{tupleTopic, arrayTopic}}
	if !reflect.DeepEqual(got.CompositeOrTopics, wantOrTopics) {
		t.Fatalf("composite OR topics mismatch: have %#v want %#v", got.CompositeOrTopics, wantOrTopics)
	}
	wantWildcardTopics := []any{tupleSignatureTopic, nil}
	if !reflect.DeepEqual(got.WildcardTopics, wantWildcardTopics) {
		t.Fatalf("composite wildcard topics mismatch: have %#v want %#v", got.WildcardTopics, wantWildcardTopics)
	}
	if !got.RejectsTupleValue || !got.RejectsArrayValue {
		t.Fatalf("composite event values should require precomputed topics: tuple=%t array=%t", got.RejectsTupleValue, got.RejectsArrayValue)
	}
	if got.DecodedEvent != "Composite" || got.DecodedTupleTopic != tupleTopic {
		t.Fatalf("decoded composite event mismatch: event=%q topic=%q", got.DecodedEvent, got.DecodedTupleTopic)
	}
}

func TestEmbeddedWeb3AllEventsToleratesTopiclessLogs(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3ForEvents(t)
	address := "Q" + strings.Repeat("0", common.AddressLength*2)

	script := fmt.Sprintf(eventEchoProviderJS+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([{
  anonymous: false,
  inputs: [{indexed: true, name: "value", type: "uint512"}],
  name: "Ping",
  type: "event"
}]).at(%q);

// LOG0 emits logs with no topics at all; the allEvents formatter must pass
// them through undecoded instead of throwing and aborting the whole batch.
var decoded = contract.allEvents().formatter({
  address: %q,
  topics: [],
  data: "0x",
  blockNumber: "0x1",
  transactionIndex: "0x0",
  logIndex: "0x0"
});

JSON.stringify({address: decoded.address, data: decoded.data, topicCount: decoded.topics.length});
`, address, address)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run topicless log script: %v", err)
	}
	var got struct {
		Address    string `json:"address"`
		Data       string `json:"data"`
		TopicCount int    `json:"topicCount"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode topicless log result %q: %v", value.String(), err)
	}
	if got.Address != address || got.Data != "0x" || got.TopicCount != 0 {
		t.Fatalf("topicless log should pass through unchanged: %+v", got)
	}
}

func TestEmbeddedWeb3RejectsMalformedIndexedBytes(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3ForEvents(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	wantTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte{0xab, 0xcd})).Hex()

	script := fmt.Sprintf(eventEchoProviderJS+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([
  {anonymous: false, inputs: [{indexed: true, name: "payload", type: "bytes"}], name: "Blob", type: "event"}
]).at(%q);

function rejects(fn) {
  try { fn(); return false; } catch (err) { return true; }
}

JSON.stringify({
  rejectsOddBytes: rejects(function() { contract.Blob({payload: "0x1"}); }),
  rejectsNonHexBytes: rejects(function() { contract.Blob({payload: "0xzz"}); }),
  validTopic: contract.Blob({payload: "0xabcd"}).options.topics[1]
});
`, contractAddress)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run indexed bytes filter script: %v", err)
	}
	var got struct {
		RejectsOddBytes    bool   `json:"rejectsOddBytes"`
		RejectsNonHexBytes bool   `json:"rejectsNonHexBytes"`
		ValidTopic         string `json:"validTopic"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode indexed bytes result %q: %v", value.String(), err)
	}
	if !got.RejectsOddBytes || !got.RejectsNonHexBytes {
		t.Fatalf("malformed indexed bytes should be rejected: odd=%t nonhex=%t", got.RejectsOddBytes, got.RejectsNonHexBytes)
	}
	if got.ValidTopic != wantTopic {
		t.Fatalf("indexed bytes topic mismatch:\nhave %s\nwant %s", got.ValidTopic, wantTopic)
	}
}

func assertEventWeb3IsTopic(t *testing.T, re *JSRE, topic string, want bool, label string) {
	t.Helper()

	value, err := re.Run(fmt.Sprintf("web3._extend.utils.isTopic(%q)", topic))
	if err != nil {
		t.Fatalf("isTopic(%s): %v", label, err)
	}
	if got := value.ToBoolean(); got != want {
		t.Fatalf("isTopic(%s) = %t, want %t", label, got, want)
	}
}

const eventEchoProviderJS = `
var currentOutput = null;
var provider = {
  send: function(payload) {
    return {jsonrpc: "2.0", id: payload.id, result: currentOutput};
  },
  sendAsync: function(payload, cb) {
    cb(null, {jsonrpc: "2.0", id: payload.id, result: currentOutput});
  },
  isConnected: function() { return true; }
};
`

// eventCaptureProviderJS records qrl_newFilter options into captured and
// acknowledges every request with a static filter id.
const eventCaptureProviderJS = `
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
  },
  isConnected: function() { return true; }
};
`

func newEmbeddedWeb3ForEvents(t *testing.T) *JSRE {
	t.Helper()

	re := New("", os.Stdout)
	t.Cleanup(func() {
		re.Stop(false)
	})

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}
	return re
}
