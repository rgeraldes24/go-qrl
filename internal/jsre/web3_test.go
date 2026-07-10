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
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/internal/jsre/deps"
)

func TestEmbeddedWeb3QIP55Checksum(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	if _, err := re.Run("var Web3 = require('web3'); var web3 = new Web3();"); err != nil {
		t.Fatalf("init web3: %v", err)
	}

	lower := "Q" + strings.Repeat("a", common.AddressLength*2)
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

func TestEmbeddedWeb3RawFilterTopicFormatting(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)

	eventID := crypto.Keccak256([]byte("Transfer(address,uint512,string)"))
	expectedEventTopic := common.HashToLogTopic(common.BytesToHash(eventID)).Hex()
	expectedAllEventsTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("Ping(address,uint512,string)"))).Hex()
	indexedAddress := "Q" + strings.Repeat("a", common.AddressLength*2)
	expectedIndexedAddressTopic := "0x" + strings.Repeat("a", common.AddressLength*2)
	expectedIndexedValueTopic := common.BytesToRightAlignedLogTopic([]byte{2}).Hex()
	expectedIndexedLabelTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("hello"))).Hex()
	expectedRawStringFilterValue := "0x" + common.Bytes2Hex([]byte("hello"))
	address := "Q" + strings.Repeat("0", common.AddressLength*2)

	script := fmt.Sprintf(captureProviderJS+`
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
  allEventsDecodedValue: allEventsDecoded.args.value.toString(10),
  allEventsDecodedLabel: allEventsDecoded.args.label
});
`, expectedEventTopic, address, address, indexedAddress, address, address, expectedAllEventsTopic, expectedIndexedAddressTopic, expectedIndexedValueTopic, expectedIndexedLabelTopic)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run raw topic script: %v", err)
	}
	var got struct {
		Captured              []any  `json:"captured"`
		EventCaptured         []any  `json:"eventCaptured"`
		AllEventsDecodedEvent string `json:"allEventsDecodedEvent"`
		AllEventsDecodedFrom  string `json:"allEventsDecodedFrom"`
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
	if got.AllEventsDecodedFrom != indexedAddress {
		t.Fatalf("allEvents decoded address mismatch: have %q want %q", got.AllEventsDecodedFrom, indexedAddress)
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

	assertWeb3IsTopic(t, re, eventIDHex, false, "event ID")
	assertWeb3IsTopic(t, re, expectedEventTopic, true, "full-width VM64 event topic")
	assertWeb3IsTopic(t, re, upperCaseTopic, true, "uppercase 64-byte topic")
	assertWeb3IsTopic(t, re, mixedCaseTopic, false, "mixed-case 64-byte topic")
	assertWeb3IsTopic(t, re, upperPrefixTopic, false, "uppercase 0X-prefixed topic")
}

func TestEmbeddedWeb3ABICoderUsesVM64Words(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)

	address := "Q" + strings.Repeat("a", common.AddressLength*2)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	amountWord := abiWordHexUint64(2)
	offsetWord := abiWordHexUint64(5 * common.LogTopicLength)
	activeWord := abiWordHexUint64(1)
	tagWord := common.Bytes2Hex([]byte{1, 2, 3, 4}) + strings.Repeat("0", common.LogTopicLength*2-4*2)
	lengthWord := abiWordHexUint64(5)
	labelWord := common.Bytes2Hex([]byte("hello")) + strings.Repeat("0", common.LogTopicLength*2-len("hello")*2)
	output := "0x" + strings.Repeat("a", common.AddressLength*2) + amountWord + offsetWord + activeWord + tagWord + lengthWord + labelWord
	expectedData := "0x" +
		common.Bytes2Hex(crypto.Keccak256([]byte("store(address,uint512,string,bool,bytes4)"))[:4]) +
		strings.Repeat("a", common.AddressLength*2) +
		amountWord +
		offsetWord +
		activeWord +
		tagWord +
		lengthWord +
		labelWord

	script := fmt.Sprintf(echoProviderJS+`
currentOutput = %q;

var Web3 = require("web3");
var web3 = new Web3(provider);

var contractAbi = [{
  constant: false,
  inputs: [
    {name: "to", type: "address"},
    {name: "amount", type: "uint512"},
    {name: "label", type: "string"},
    {name: "active", type: "bool"},
    {name: "tag", type: "bytes4"}
  ],
  name: "store",
  outputs: [],
  type: "function"
}, {
  constant: true,
  inputs: [],
  name: "load",
  outputs: [
    {name: "to", type: "address"},
    {name: "amount", type: "uint512"},
    {name: "label", type: "string"},
    {name: "active", type: "bool"},
    {name: "tag", type: "bytes4"}
  ],
  type: "function"
}];
var contract = web3.qrl.contract(contractAbi).at(%q);
var data = contract.store.getData(%q, 2, "hello", true, "0x01020304");
var decoded = contract.load.call();

JSON.stringify({
  data: data,
  decodedAddress: decoded[0],
  decodedAmount: decoded[1].toString(10),
  decodedLabel: decoded[2],
  decodedActive: decoded[3],
  decodedTag: decoded[4]
});
`, output, contractAddress, address)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run ABI coder script: %v", err)
	}
	var got struct {
		Data           string `json:"data"`
		DecodedAddress string `json:"decodedAddress"`
		DecodedAmount  string `json:"decodedAmount"`
		DecodedLabel   string `json:"decodedLabel"`
		DecodedActive  bool   `json:"decodedActive"`
		DecodedTag     string `json:"decodedTag"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode ABI coder result %q: %v", value.String(), err)
	}
	if got.Data != expectedData {
		t.Fatalf("calldata mismatch:\nhave %s\nwant %s", got.Data, expectedData)
	}
	if got.DecodedAddress != address {
		t.Fatalf("decoded address mismatch: have %q want %q", got.DecodedAddress, address)
	}
	if got.DecodedAmount != "2" {
		t.Fatalf("decoded amount mismatch: have %q want 2", got.DecodedAmount)
	}
	if got.DecodedLabel != "hello" {
		t.Fatalf("decoded label mismatch: have %q want hello", got.DecodedLabel)
	}
	if !got.DecodedActive {
		t.Fatal("decoded active mismatch: have false want true")
	}
	if got.DecodedTag != "0x01020304" {
		t.Fatalf("decoded tag mismatch: have %q want 0x01020304", got.DecodedTag)
	}
}

func TestEmbeddedWeb3ABICoderValidatesVM64Scalars(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	twoTo512 := new(big.Int).Lsh(big.NewInt(1), 512)
	maxSignedHex := "7" + strings.Repeat("f", common.LogTopicLength*2-1)
	signedSelector := common.Bytes2Hex(crypto.Keccak256([]byte("encodeSigned(int512)"))[:4])
	wantSignedData := "0x" + signedSelector + strings.Repeat("f", common.LogTopicLength*2)
	truncatedString := "0x" + abiWordHexUint64(common.LogTopicLength) + abiWordHexUint64(5)

	script := fmt.Sprintf(echoProviderJS+`
currentOutput = "0x";

var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([
  {name: "encodeSigned", inputs: [{name: "value", type: "int512"}], outputs: [], type: "function"},
  {name: "encodeInt8", inputs: [{name: "value", type: "int8"}], outputs: [], type: "function"},
  {name: "encodeUint8", inputs: [{name: "value", type: "uint8"}], outputs: [], type: "function"},
  {name: "encodeUint512", inputs: [{name: "value", type: "uint512"}], outputs: [], type: "function"},
  {name: "encodeBool", inputs: [{name: "value", type: "bool"}], outputs: [], type: "function"},
  {name: "encodeBytes", inputs: [{name: "value", type: "bytes"}], outputs: [], type: "function"},
  {name: "encodeBytes4", inputs: [{name: "value", type: "bytes4"}], outputs: [], type: "function"},
  {name: "encodeBytes65", inputs: [{name: "value", type: "bytes65"}], outputs: [], type: "function"},
  {name: "decodeSigned", inputs: [], outputs: [{name: "value", type: "int512"}], type: "function"},
  {name: "decodeBool", inputs: [], outputs: [{name: "value", type: "bool"}], type: "function"},
  {name: "decodeUint512", inputs: [], outputs: [{name: "value", type: "uint512"}], type: "function"},
  {name: "decodeUint8", inputs: [], outputs: [{name: "value", type: "uint8"}], type: "function"},
  {name: "decodeString", inputs: [], outputs: [{name: "value", type: "string"}], type: "function"}
]).at(%q);

function rejects(fn) {
  try {
    fn();
    return false;
  } catch (err) {
    return true;
  }
}

var signedData = contract.encodeSigned.getData(-1);
currentOutput = "0x" + Array(129).join("f");
var decodedSigned = contract.decodeSigned.call().toString(10);
currentOutput = "0x" + %q;
var decodedMaxSigned = contract.decodeSigned.call().toString(16);

currentOutput = "0x" + %q;
var rejectsBoolTwo = rejects(function() { contract.decodeBool.call(); });
currentOutput = "0x" + %q;
var rejectsDecodedUint8Overflow = rejects(function() { contract.decodeUint8.call(); });
currentOutput = "0x";
var rejectsTruncatedWord = rejects(function() { contract.decodeUint512.call(); });
currentOutput = %q;
var rejectsTruncatedString = rejects(function() { contract.decodeString.call(); });

JSON.stringify({
  signedData: signedData,
  decodedSigned: decodedSigned,
  decodedMaxSigned: decodedMaxSigned,
  rejectsInt8Overflow: rejects(function() { contract.encodeInt8.getData(128); }),
  rejectsInt8Underflow: rejects(function() { contract.encodeInt8.getData(-129); }),
  rejectsUint8Overflow: rejects(function() { contract.encodeUint8.getData(256); }),
  rejectsNegativeUint: rejects(function() { contract.encodeUint512.getData(-1); }),
  rejectsUint512Overflow: rejects(function() { contract.encodeUint512.getData(%q); }),
  rejectsNonBool: rejects(function() { contract.encodeBool.getData(1); }),
  rejectsOddBytes: rejects(function() { contract.encodeBytes.getData("0x1"); }),
  rejectsInvalidBytes: rejects(function() { contract.encodeBytes.getData("0xzz"); }),
  rejectsLongBytes4: rejects(function() { contract.encodeBytes4.getData("0x0102030405"); }),
  rejectsBytes65: rejects(function() { contract.encodeBytes65.getData("0x"); }),
  rejectsBoolTwo: rejectsBoolTwo,
  rejectsDecodedUint8Overflow: rejectsDecodedUint8Overflow,
  rejectsTruncatedWord: rejectsTruncatedWord,
  rejectsTruncatedString: rejectsTruncatedString
});
`, contractAddress, maxSignedHex, abiWordHexUint64(2), abiWordHexUint64(256), truncatedString, twoTo512.String())

	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run scalar ABI validation script: %v", err)
	}
	var got struct {
		SignedData             string `json:"signedData"`
		DecodedSigned          string `json:"decodedSigned"`
		DecodedMaxSigned       string `json:"decodedMaxSigned"`
		RejectsInt8Overflow    bool   `json:"rejectsInt8Overflow"`
		RejectsInt8Underflow   bool   `json:"rejectsInt8Underflow"`
		RejectsUint8Overflow   bool   `json:"rejectsUint8Overflow"`
		RejectsNegativeUint    bool   `json:"rejectsNegativeUint"`
		RejectsUint512Overflow bool   `json:"rejectsUint512Overflow"`
		RejectsNonBool         bool   `json:"rejectsNonBool"`
		RejectsOddBytes        bool   `json:"rejectsOddBytes"`
		RejectsInvalidBytes    bool   `json:"rejectsInvalidBytes"`
		RejectsLongBytes4      bool   `json:"rejectsLongBytes4"`
		RejectsBytes65         bool   `json:"rejectsBytes65"`
		RejectsBoolTwo         bool   `json:"rejectsBoolTwo"`
		RejectsDecodedUint8    bool   `json:"rejectsDecodedUint8Overflow"`
		RejectsTruncatedWord   bool   `json:"rejectsTruncatedWord"`
		RejectsTruncatedString bool   `json:"rejectsTruncatedString"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode scalar ABI validation result %q: %v", value.String(), err)
	}
	if got.SignedData != wantSignedData {
		t.Fatalf("signed calldata mismatch:\nhave %s\nwant %s", got.SignedData, wantSignedData)
	}
	if got.DecodedSigned != "-1" {
		t.Fatalf("decoded signed value mismatch: have %q want -1", got.DecodedSigned)
	}
	if got.DecodedMaxSigned != maxSignedHex {
		t.Fatalf("decoded max signed value mismatch: have %q want %q", got.DecodedMaxSigned, maxSignedHex)
	}
	checks := map[string]bool{
		"int8 overflow":          got.RejectsInt8Overflow,
		"int8 underflow":         got.RejectsInt8Underflow,
		"uint8 overflow":         got.RejectsUint8Overflow,
		"negative uint":          got.RejectsNegativeUint,
		"uint512 overflow":       got.RejectsUint512Overflow,
		"non-bool input":         got.RejectsNonBool,
		"odd-length bytes":       got.RejectsOddBytes,
		"invalid bytes":          got.RejectsInvalidBytes,
		"overlong bytes4":        got.RejectsLongBytes4,
		"bytes65":                got.RejectsBytes65,
		"noncanonical bool":      got.RejectsBoolTwo,
		"decoded uint8 overflow": got.RejectsDecodedUint8,
		"truncated static word":  got.RejectsTruncatedWord,
		"truncated string":       got.RejectsTruncatedString,
	}
	for name, rejected := range checks {
		if !rejected {
			t.Errorf("ABI coder accepted %s", name)
		}
	}
}

func TestEmbeddedWeb3ABICoderHandlesDynamicArrays(t *testing.T) {
	t.Parallel()

	const contractABI = `[
  {"name":"storeArrays","inputs":[{"name":"labels","type":"string[]"},{"name":"payloads","type":"bytes[]"},{"name":"values","type":"uint512[][]"},{"name":"fixedLabels","type":"string[2]"},{"name":"fixedValues","type":"uint512[][2]"}],"outputs":[],"type":"function"},
  {"name":"loadArrays","inputs":[],"outputs":[{"name":"labels","type":"string[]"},{"name":"payloads","type":"bytes[]"},{"name":"values","type":"uint512[][]"},{"name":"fixedLabels","type":"string[2]"},{"name":"fixedValues","type":"uint512[][2]"}],"type":"function"}
]`
	parsedABI, err := abi.JSON(strings.NewReader(contractABI))
	if err != nil {
		t.Fatal(err)
	}
	labels := []string{"a", "bb"}
	payloads := [][]byte{{0x01}, {0x02, 0x03}}
	values := [][]*big.Int{{big.NewInt(1), big.NewInt(2)}, {big.NewInt(3)}}
	fixedLabels := [2]string{"a", "bb"}
	fixedValues := [2][]*big.Int{{big.NewInt(1), big.NewInt(2)}, {big.NewInt(3)}}
	wantData, err := parsedABI.Pack("storeArrays", labels, payloads, values, fixedLabels, fixedValues)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput, err := parsedABI.Methods["loadArrays"].Outputs.Pack(labels, payloads, values, fixedLabels, fixedValues)
	if err != nil {
		t.Fatal(err)
	}

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	script := fmt.Sprintf(echoProviderJS+`
currentOutput = %q;

var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract(%s).at(%q);
var data = contract.storeArrays.getData(
  ["a", "bb"],
  ["0x01", "0x0203"],
  [[1, 2], [3]],
  ["a", "bb"],
  [[1, 2], [3]]
);
var decoded = contract.loadArrays.call();

JSON.stringify({
  data: data,
  labels: decoded[0],
  payloads: decoded[1],
  values: decoded[2].map(function(row) { return row.map(function(v) { return v.toString(10); }); }),
  fixedLabels: decoded[3],
  fixedValues: decoded[4].map(function(row) { return row.map(function(v) { return v.toString(10); }); })
});
`, "0x"+common.Bytes2Hex(wantOutput), contractABI, contractAddress)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run dynamic array ABI script: %v", err)
	}
	var got struct {
		Data        string     `json:"data"`
		Labels      []string   `json:"labels"`
		Payloads    []string   `json:"payloads"`
		Values      [][]string `json:"values"`
		FixedLabels []string   `json:"fixedLabels"`
		FixedValues [][]string `json:"fixedValues"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode dynamic array ABI result %q: %v", value.String(), err)
	}
	if want := "0x" + common.Bytes2Hex(wantData); got.Data != want {
		t.Fatalf("dynamic array calldata mismatch:\nhave %s\nwant %s", got.Data, want)
	}
	if !reflect.DeepEqual(got.Labels, labels) || !reflect.DeepEqual(got.FixedLabels, labels) {
		t.Fatalf("decoded labels mismatch: have %#v and %#v want %#v", got.Labels, got.FixedLabels, labels)
	}
	if want := []string{"0x01", "0x0203"}; !reflect.DeepEqual(got.Payloads, want) {
		t.Fatalf("decoded payloads mismatch: have %#v want %#v", got.Payloads, want)
	}
	if want := [][]string{{"1", "2"}, {"3"}}; !reflect.DeepEqual(got.Values, want) || !reflect.DeepEqual(got.FixedValues, want) {
		t.Fatalf("decoded values mismatch: have %#v and %#v want %#v", got.Values, got.FixedValues, want)
	}
}

func TestEmbeddedWeb3CompositeEventFiltersRequirePrecomputedTopics(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	address := "Q" + strings.Repeat("0", common.AddressLength*2)
	owner := "Q" + strings.Repeat("a", common.AddressLength*2)
	tupleTopic := "0x" + strings.Repeat("ab", common.LogTopicLength)
	arrayTopic := "0x" + strings.Repeat("cd", common.LogTopicLength)
	tupleSignatureTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("Composite((uint512,address))"))).Hex()
	arraySignatureTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte("Values(uint512[2])"))).Hex()

	script := fmt.Sprintf(captureProviderJS+`
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

var decoded = contract.allEvents().formatter({
  address: %q,
  topics: [%q, %q],
  data: "0x",
  blockNumber: "0x1",
  transactionIndex: "0x0",
  logIndex: "0x0"
});

JSON.stringify({
  captured: captured.map(function (options) { return options.topics; }),
  rejectsTupleValue: rejectsTupleValue,
  rejectsArrayValue: rejectsArrayValue,
  decodedEvent: decoded.event,
  decodedTupleTopic: decoded.args.value
});
`, address, owner, tupleTopic, arrayTopic, address, tupleSignatureTopic, tupleTopic)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run composite event script: %v", err)
	}
	var got struct {
		Captured          [][]string `json:"captured"`
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
	if !got.RejectsTupleValue || !got.RejectsArrayValue {
		t.Fatalf("composite event values should require precomputed topics: tuple=%t array=%t", got.RejectsTupleValue, got.RejectsArrayValue)
	}
	if got.DecodedEvent != "Composite" || got.DecodedTupleTopic != tupleTopic {
		t.Fatalf("decoded composite event mismatch: event=%q topic=%q", got.DecodedEvent, got.DecodedTupleTopic)
	}
}

func TestEmbeddedWeb3BareIntegerAliasesUseVM64Width(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	// Needs more than 256 bits, so it only encodes if bare uint aliases uint512.
	amount := new(big.Int).Lsh(big.NewInt(1), 300)
	selector := common.Bytes2Hex(crypto.Keccak256([]byte("store(uint512)"))[:4])
	expectedData := "0x" + selector + fmt.Sprintf("%0*x", common.LogTopicLength*2, amount)

	script := fmt.Sprintf(echoProviderJS+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([{
  constant: false,
  inputs: [{name: "amount", type: "uint"}],
  name: "store",
  outputs: [],
  type: "function"
}]).at(%q);

contract.store.getData(%q);
`, contractAddress, amount.String())
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run bare integer script: %v", err)
	}
	if got := value.String(); got != expectedData {
		t.Fatalf("bare uint calldata mismatch:\nhave %s\nwant %s", got, expectedData)
	}
}

func TestEmbeddedWeb3AllEventsToleratesTopiclessLogs(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	address := "Q" + strings.Repeat("0", common.AddressLength*2)

	script := fmt.Sprintf(echoProviderJS+`
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

func TestEmbeddedWeb3IntegerInputFormatsAndBoundaries(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	two512 := new(big.Int).Lsh(big.NewInt(1), 512)

	uintSelector := common.Bytes2Hex(crypto.Keccak256([]byte("encU512(uint512)"))[:4])
	intSelector := common.Bytes2Hex(crypto.Keccak256([]byte("encI512(int512)"))[:4])
	int8Selector := common.Bytes2Hex(crypto.Keccak256([]byte("encI8(int8)"))[:4])
	bytes64Selector := common.Bytes2Hex(crypto.Keccak256([]byte("encB64(bytes64)"))[:4])

	bytes64Value := strings.Repeat("ab", common.LogTopicLength)
	maxUint512 := new(big.Int).Sub(two512, big.NewInt(1))

	wantHex16 := "0x" + uintSelector + abiWordHexUint64(0x10)
	wantNegHex16 := "0x" + intSelector + fmt.Sprintf("%x", new(big.Int).Sub(two512, big.NewInt(0x10)))
	wantMaxSafe := "0x" + uintSelector + abiWordHexUint64(9007199254740991)
	wantMaxUint512 := "0x" + uintSelector + fmt.Sprintf("%x", maxUint512)
	wantInt8Max := "0x" + int8Selector + abiWordHexUint64(127)
	wantInt8Min := "0x" + int8Selector + fmt.Sprintf("%x", new(big.Int).Sub(two512, big.NewInt(128)))
	wantBytes64 := "0x" + bytes64Selector + bytes64Value
	wantTwosComplementNeg1 := strings.Repeat("f", common.LogTopicLength*2)

	script := fmt.Sprintf(echoProviderJS+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([
  {name: "encU512", inputs: [{name: "v", type: "uint512"}], outputs: [], type: "function", constant: false},
  {name: "encI512", inputs: [{name: "v", type: "int512"}], outputs: [], type: "function", constant: false},
  {name: "encI8", inputs: [{name: "v", type: "int8"}], outputs: [], type: "function", constant: false},
  {name: "encB64", inputs: [{name: "v", type: "bytes64"}], outputs: [], type: "function", constant: false},
  {name: "load", inputs: [], outputs: [{name: "r", type: "uint512"}], type: "function", constant: true}
]).at(%q);

function rejects(fn) {
  try { fn(); return false; } catch (err) { return true; }
}

JSON.stringify({
  hexString: contract.encU512.getData("0x10"),
  negativeHexString: contract.encI512.getData("-0x10"),
  maxSafeInteger: contract.encU512.getData(9007199254740991),
  maxUint512: contract.encU512.getData(%q),
  int8Max: contract.encI8.getData(127),
  int8Min: contract.encI8.getData(-128),
  bytes64: contract.encB64.getData("0x%s"),
  twosComplementNeg1: web3._extend.utils.toTwosComplement(-1).toString(16),
  sendFormatIsNull: contract.encU512.request(1).format === null,
  callFormatIsFunction: typeof contract.load.request().format === "function",
  rejectsFractionString: rejects(function() { contract.encU512.getData("1.5"); })
});
`, contractAddress, maxUint512.String(), bytes64Value)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run integer input script: %v", err)
	}
	var got struct {
		HexString            string `json:"hexString"`
		NegativeHexString    string `json:"negativeHexString"`
		MaxSafeInteger       string `json:"maxSafeInteger"`
		MaxUint512           string `json:"maxUint512"`
		Int8Max              string `json:"int8Max"`
		Int8Min              string `json:"int8Min"`
		Bytes64              string `json:"bytes64"`
		TwosComplementNeg1   string `json:"twosComplementNeg1"`
		SendFormatIsNull     bool   `json:"sendFormatIsNull"`
		CallFormatIsFunction bool   `json:"callFormatIsFunction"`
		RejectsFraction      bool   `json:"rejectsFractionString"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode integer input result %q: %v", value.String(), err)
	}
	for _, tc := range []struct{ name, have, want string }{
		{"hex string", got.HexString, wantHex16},
		{"negative hex string", got.NegativeHexString, wantNegHex16},
		{"max safe integer", got.MaxSafeInteger, wantMaxSafe},
		{"max uint512", got.MaxUint512, wantMaxUint512},
		{"int8 max", got.Int8Max, wantInt8Max},
		{"int8 min", got.Int8Min, wantInt8Min},
		{"bytes64", got.Bytes64, wantBytes64},
		{"toTwosComplement(-1)", got.TwosComplementNeg1, wantTwosComplementNeg1},
	} {
		if tc.have != tc.want {
			t.Errorf("%s mismatch:\nhave %s\nwant %s", tc.name, tc.have, tc.want)
		}
	}
	if !got.SendFormatIsNull {
		t.Error("sendTransaction batch requests must not decode results as ABI output")
	}
	if !got.CallFormatIsFunction {
		t.Error("call batch requests must decode results as ABI output")
	}
	if !got.RejectsFraction {
		t.Error("fractional string should be rejected as ABI integer")
	}
}

func TestEmbeddedWeb3RejectsSparseArraysAndInvalidBytesFilters(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	validBytesTopic := common.HashToLogTopic(crypto.Keccak256Hash([]byte{0xab, 0xcd})).Hex()

	script := fmt.Sprintf(echoProviderJS+`
currentOutput = "0x1";

var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([
  {name: "arr", inputs: [{name: "v", type: "uint512[2]"}], outputs: [], type: "function", constant: false},
  {name: "dyn", inputs: [{name: "v", type: "uint512[]"}], outputs: [], type: "function", constant: false},
  {anonymous: false, inputs: [{indexed: true, name: "payload", type: "bytes"}], name: "Blob", type: "event"}
]).at(%q);

function rejects(fn) {
  try { fn(); return false; } catch (err) { return true; }
}

JSON.stringify({
  rejectsSparseStatic: rejects(function() { contract.arr.getData(new Array(2)); }),
  rejectsSparseDynamic: rejects(function() { contract.dyn.getData(new Array(3)); }),
  rejectsOddBytesFilter: rejects(function() { contract.Blob({payload: "0x1"}); }),
  rejectsNonHexBytesFilter: rejects(function() { contract.Blob({payload: "0xzz"}); }),
  validBytesTopic: contract.Blob({payload: "0xabcd"}).options.topics[1]
});
`, contractAddress)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run sparse array script: %v", err)
	}
	var got struct {
		RejectsSparseStatic  bool   `json:"rejectsSparseStatic"`
		RejectsSparseDynamic bool   `json:"rejectsSparseDynamic"`
		RejectsOddBytes      bool   `json:"rejectsOddBytesFilter"`
		RejectsNonHexBytes   bool   `json:"rejectsNonHexBytesFilter"`
		ValidBytesTopic      string `json:"validBytesTopic"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode sparse array result %q: %v", value.String(), err)
	}
	if !got.RejectsSparseStatic || !got.RejectsSparseDynamic {
		t.Errorf("sparse arrays should be rejected: static=%t dynamic=%t", got.RejectsSparseStatic, got.RejectsSparseDynamic)
	}
	if !got.RejectsOddBytes || !got.RejectsNonHexBytes {
		t.Errorf("malformed hex bytes filters should be rejected: odd=%t nonhex=%t", got.RejectsOddBytes, got.RejectsNonHexBytes)
	}
	if got.ValidBytesTopic != validBytesTopic {
		t.Errorf("valid bytes filter topic mismatch:\nhave %s\nwant %s", got.ValidBytesTopic, validBytesTopic)
	}
}

func assertWeb3IsTopic(t *testing.T, re *JSRE, topic string, want bool, label string) {
	t.Helper()

	value, err := re.Run(fmt.Sprintf("web3._extend.utils.isTopic(%q)", topic))
	if err != nil {
		t.Fatalf("isTopic(%s): %v", label, err)
	}
	if got := value.ToBoolean(); got != want {
		t.Fatalf("isTopic(%s) = %t, want %t", label, got, want)
	}
}

func abiWordHexUint64(value uint64) string {
	return fmt.Sprintf("%0*x", common.LogTopicLength*2, value)
}

// echoProviderJS is a mock transport whose responses echo the mutable
// currentOutput variable; scripts assign it before issuing requests.
const echoProviderJS = `
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

// captureProviderJS records qrl_newFilter options into captured and
// acknowledges every request with a static filter id.
const captureProviderJS = `
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

func newEmbeddedWeb3(t *testing.T) *JSRE {
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
