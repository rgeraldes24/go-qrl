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
	"slices"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/internal/jsre/deps"
)

func TestEmbeddedWeb3ABICoderUsesVM64Words(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	address := "Q" + strings.Repeat("a", common.AddressLength*2)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	addressWord := strings.Repeat("a", common.AddressLength*2)
	maxUint512 := strings.Repeat("f", uint512.WordBytes*2)
	maxUint512Decimal := "134078079299425970995740249982058461274793658205923933" +
		"77723561443721764030073546976801874298166903427690031" +
		"858186486050853753882811946569946433649006084095"
	bytes33 := strings.Repeat("ab", 33)
	bytes33Word := bytes33 + strings.Repeat("0", (uint512.WordBytes-33)*2)
	labelOffsetWord := abiWordHex(5 * uint512.WordBytes)
	activeWord := abiWordHex(1)
	labelLengthWord := abiWordHex(5)
	labelDataWord := common.Bytes2Hex([]byte("hello")) + strings.Repeat("0", uint512.WordBytes*2-len("hello")*2)
	encodedValues := strings.Join([]string{
		addressWord,
		maxUint512,
		labelOffsetWord,
		activeWord,
		bytes33Word,
		labelLengthWord,
		labelDataWord,
	}, "")
	output := "0x" + encodedValues
	expectedData := "0x" + methodSelector("store(address,uint512,string,bool,bytes33)") + encodedValues
	expectedEmptyTagData := "0x" +
		methodSelector("storeTag(bytes33)") +
		strings.Repeat("0", uint512.WordBytes*2)

	script := fmt.Sprintf(web3CallProvider+`
currentOutput = %q;

var Web3 = require("web3");
var web3 = new Web3(provider);
var contractAbi = [{
  inputs: [
    {name: "to", type: "address"},
    {name: "amount", type: "uint512"},
    {name: "label", type: "string"},
    {name: "active", type: "bool"},
    {name: "tag", type: "bytes33"}
  ],
  name: "store",
  outputs: [],
  stateMutability: "nonpayable",
  type: "function"
}, {
  inputs: [],
  name: "load",
  outputs: [
    {name: "to", type: "address"},
    {name: "amount", type: "uint512"},
    {name: "label", type: "string"},
    {name: "active", type: "bool"},
    {name: "tag", type: "bytes33"}
  ],
  stateMutability: "view",
  type: "function"
}, {
  inputs: [{name: "tag", type: "bytes33"}],
  name: "storeTag",
  outputs: [],
  stateMutability: "nonpayable",
  type: "function"
}, {
  inputs: [],
  name: "pay",
  outputs: [],
  stateMutability: "payable",
  type: "function"
}];
var contract = web3.qrl.contract(contractAbi).at(%q);
var data = contract.store.getData(%q, %q, "hello", true, "0x%s");
var emptyTagData = contract.storeTag.getData("0x");
var decoded = contract.load();
var loadMethod = lastPayload.method;
contract.pay({from: %q, value: 1});

JSON.stringify({
  data: data,
  emptyTagData: emptyTagData,
  address: decoded[0],
  amount: decoded[1].toString(16),
  label: decoded[2],
  active: decoded[3],
  tag: decoded[4],
  loadMethod: loadMethod,
  payMethod: lastPayload.method
});
`, output, contractAddress, address, maxUint512Decimal, bytes33, address)
	var got struct {
		Data         string `json:"data"`
		EmptyTagData string `json:"emptyTagData"`
		Address      string `json:"address"`
		Amount       string `json:"amount"`
		Label        string `json:"label"`
		Active       bool   `json:"active"`
		Tag          string `json:"tag"`
		LoadMethod   string `json:"loadMethod"`
		PayMethod    string `json:"payMethod"`
	}
	runWeb3JSON(t, re, script, &got)
	if got.Data != expectedData {
		t.Fatalf("calldata mismatch:\nhave %s\nwant %s", got.Data, expectedData)
	}
	if got.EmptyTagData != expectedEmptyTagData {
		t.Fatalf("empty fixed bytes calldata mismatch:\nhave %s\nwant %s", got.EmptyTagData, expectedEmptyTagData)
	}
	if got.Address != address || got.Amount != maxUint512 || got.Label != "hello" || !got.Active || got.Tag != "0x"+bytes33 {
		t.Fatalf("decoded values mismatch: %+v", got)
	}
	if got.LoadMethod != "qrl_call" || got.PayMethod != "qrl_sendTransaction" {
		t.Fatalf("stateMutability routing mismatch: load=%q pay=%q", got.LoadMethod, got.PayMethod)
	}
}

func TestEmbeddedWeb3DynamicBytesAndArrays(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	payload := "a1b2c3"
	payloadWord := payload + strings.Repeat("0", uint512.WordBytes*2-len(payload))
	encodedBytes := strings.Join([]string{
		abiWordHex(uint512.WordBytes),
		abiWordHex(3),
		payloadWord,
	}, "")
	bytesOutput := "0x" + encodedBytes
	bytesData := "0x" + methodSelector("storeBytes(bytes)") + encodedBytes

	encodedArrays := strings.Join([]string{
		abiWordHex(1),
		abiWordHex(2),
		abiWordHex(3 * uint512.WordBytes),
		abiWordHex(3),
		abiWordHex(3),
		abiWordHex(4),
		abiWordHex(5),
	}, "")
	arraysOutput := "0x" + encodedArrays
	arraysData := "0x" + methodSelector("storeArrays(uint512[2],uint512[])") + encodedArrays

	script := fmt.Sprintf(web3CallProvider+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([{
  inputs: [{name: "value", type: "bytes"}],
  name: "storeBytes",
  outputs: [],
  stateMutability: "nonpayable",
  type: "function"
}, {
  inputs: [],
  name: "loadBytes",
  outputs: [{name: "value", type: "bytes"}],
  stateMutability: "pure",
  type: "function"
}, {
  inputs: [{name: "fixedValues", type: "uint512[2]"}, {name: "dynamicValues", type: "uint512[]"}],
  name: "storeArrays",
  outputs: [],
  stateMutability: "nonpayable",
  type: "function"
}, {
  inputs: [],
  name: "loadArrays",
  outputs: [{name: "fixedValues", type: "uint512[2]"}, {name: "dynamicValues", type: "uint512[]"}],
  stateMutability: "view",
  type: "function"
}]).at(%q);

var bytesData = contract.storeBytes.getData("0x%s");
currentOutput = %q;
var decodedBytes = contract.loadBytes();
var pureMethod = lastPayload.method;

var arraysData = contract.storeArrays.getData([1, 2], [3, 4, 5]);
currentOutput = %q;
var decodedArrays = contract.loadArrays();

JSON.stringify({
  bytesData: bytesData,
  decodedBytes: decodedBytes,
  pureMethod: pureMethod,
  arraysData: arraysData,
  fixedValues: decodedArrays[0].map(function (value) { return value.toString(10); }),
  dynamicValues: decodedArrays[1].map(function (value) { return value.toString(10); })
});
`, contractAddress, payload, bytesOutput, arraysOutput)
	var got struct {
		BytesData     string   `json:"bytesData"`
		DecodedBytes  string   `json:"decodedBytes"`
		PureMethod    string   `json:"pureMethod"`
		ArraysData    string   `json:"arraysData"`
		FixedValues   []string `json:"fixedValues"`
		DynamicValues []string `json:"dynamicValues"`
	}
	runWeb3JSON(t, re, script, &got)
	if got.BytesData != bytesData || got.DecodedBytes != "0x"+payload {
		t.Fatalf("dynamic bytes mismatch: %+v", got)
	}
	if got.PureMethod != "qrl_call" {
		t.Fatalf("pure function used %q, want qrl_call", got.PureMethod)
	}
	if got.ArraysData != arraysData {
		t.Fatalf("array calldata mismatch:\nhave %s\nwant %s", got.ArraysData, arraysData)
	}
	if want := []string{"1", "2"}; !slices.Equal(got.FixedValues, want) {
		t.Fatalf("decoded fixed array mismatch: have %v, want %v", got.FixedValues, want)
	}
	if want := []string{"3", "4", "5"}; !slices.Equal(got.DynamicValues, want) {
		t.Fatalf("decoded dynamic array mismatch: have %v, want %v", got.DynamicValues, want)
	}
}

func TestEmbeddedWeb3SignedInt512AndAddressInput(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	negativeOneWord := strings.Repeat("f", uint512.WordBytes*2)
	minimumWord := "8" + strings.Repeat("0", uint512.WordBytes*2-1)
	minimumValue := "-0x" + minimumWord
	negativeOneData := "0x" + methodSelector("storeInt(int512)") + negativeOneWord
	minimumData := "0x" + methodSelector("storeInt(int512)") + minimumWord
	lowerAddress := "Q" + strings.Repeat("a", common.AddressLength*2)
	address, err := common.NewAddressFromString(lowerAddress)
	if err != nil {
		t.Fatal(err)
	}
	checksumAddress := address.Hex()
	invalidChecksum := []byte(checksumAddress)
	if invalidChecksum[1] == 'a' {
		invalidChecksum[1] = 'A'
	} else {
		invalidChecksum[1] = 'a'
	}
	addressData := "0x" + methodSelector("storeAddress(address)") + strings.Repeat("a", common.AddressLength*2)

	script := fmt.Sprintf(web3CallProvider+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([{
  inputs: [{name: "value", type: "int512"}],
  name: "storeInt",
  outputs: [],
  stateMutability: "nonpayable",
  type: "function"
}, {
  inputs: [],
  name: "loadInt",
  outputs: [{name: "value", type: "int512"}],
  stateMutability: "view",
  type: "function"
}, {
  inputs: [{name: "value", type: "address"}],
  name: "storeAddress",
  outputs: [],
  stateMutability: "nonpayable",
  type: "function"
}]).at(%q);

var negativeOneData = contract.storeInt.getData(-1);
var minimumData = contract.storeInt.getData(%q);
currentOutput = "0x%s";
var negativeOne = contract.loadInt().toString(16);
currentOutput = "0x%s";
var minimum = contract.loadInt().toString(16);
var addressData = contract.storeAddress.getData(%q);
var rejectsInvalidAddress = false;
try {
  contract.storeAddress.getData("Q1234");
} catch (err) {
  rejectsInvalidAddress = true;
}
var rejectsInvalidChecksum = false;
try {
  contract.storeAddress.getData(%q);
} catch (err) {
  rejectsInvalidChecksum = true;
}

JSON.stringify({
  negativeOneData: negativeOneData,
  minimumData: minimumData,
  negativeOne: negativeOne,
  minimum: minimum,
  addressData: addressData,
  rejectsInvalidAddress: rejectsInvalidAddress,
  rejectsInvalidChecksum: rejectsInvalidChecksum
});
`, contractAddress, minimumValue, negativeOneWord, minimumWord, checksumAddress, string(invalidChecksum))
	var got struct {
		NegativeOneData        string `json:"negativeOneData"`
		MinimumData            string `json:"minimumData"`
		NegativeOne            string `json:"negativeOne"`
		Minimum                string `json:"minimum"`
		AddressData            string `json:"addressData"`
		RejectsInvalidAddress  bool   `json:"rejectsInvalidAddress"`
		RejectsInvalidChecksum bool   `json:"rejectsInvalidChecksum"`
	}
	runWeb3JSON(t, re, script, &got)
	if got.NegativeOneData != negativeOneData || got.MinimumData != minimumData {
		t.Fatalf("signed calldata mismatch: %+v", got)
	}
	if got.NegativeOne != "-1" || got.Minimum != "-"+minimumWord || !strings.EqualFold(got.AddressData, addressData) || !got.RejectsInvalidAddress || !got.RejectsInvalidChecksum {
		t.Fatalf("signed decode or address validation mismatch: %+v", got)
	}
}

func TestEmbeddedWeb3ConstructorStateMutability(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	script := web3CallProvider + `
var Web3 = require("web3");
var web3 = new Web3(provider);
web3.qrl.sendTransaction = function() { return "0x01"; };
web3.qrl.filter = function() { return {stopWatching: function() {}}; };

function acceptsValue(constructorAbi) {
  try {
    web3.qrl.contract([constructorAbi]).new({data: "0x", value: 1});
    return true;
  } catch (err) {
    return false;
  }
}

JSON.stringify({
  currentPayable: acceptsValue({inputs: [], stateMutability: "payable", type: "constructor"}),
  currentNonpayable: acceptsValue({inputs: [], stateMutability: "nonpayable", type: "constructor"}),
  legacyPayable: acceptsValue({inputs: [], payable: true, type: "constructor"})
});
`
	var got struct {
		CurrentPayable    bool `json:"currentPayable"`
		CurrentNonpayable bool `json:"currentNonpayable"`
		LegacyPayable     bool `json:"legacyPayable"`
	}
	runWeb3JSON(t, re, script, &got)
	if !got.CurrentPayable || got.CurrentNonpayable || got.LegacyPayable {
		t.Fatalf("constructor stateMutability mismatch: %+v", got)
	}
}

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

	script := fmt.Sprintf(web3FilterProvider+`
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
	runWeb3JSON(t, re, script, &got)
	wantTopics := []string{signatureTopic, addressTopic, labelTopic, payloadTopic}
	if !slices.Equal(got.Topics, wantTopics) {
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
	fixedBytes32 := strings.Repeat("ab", 32)
	fixedBytes64 := strings.Repeat("cd", common.LogTopicLength)

	script := fmt.Sprintf(web3FilterProvider+`
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
	var got []string
	runWeb3JSON(t, re, script, &got)
	want := []string{
		"0x" + abiWordHex(2),
		"0x" + abiWordHex(2),
		"0x" + strings.Repeat("f", common.LogTopicLength*2-1) + "e",
		"0x" + abiWordHex(1),
		"0x" + abiWordHex(0),
		"0x" + fixedBytes32 + strings.Repeat("0", (common.LogTopicLength-32)*2),
		"0x" + fixedBytes64,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("event topic alignment mismatch:\nhave %v\nwant %v", got, want)
	}
}

func abiWordHex(value uint64) string {
	return fmt.Sprintf("%0*x", uint512.WordBytes*2, value)
}

func methodSelector(signature string) string {
	return common.Bytes2Hex(crypto.Keccak256([]byte(signature))[:4])
}

const web3CallProvider = `
var currentOutput = null;
var lastPayload = null;
var provider = {
  send: function(payload) {
    lastPayload = payload;
    return {jsonrpc: "2.0", id: payload.id, result: currentOutput};
  },
  sendAsync: function(payload, cb) {
    lastPayload = payload;
    cb(null, {jsonrpc: "2.0", id: payload.id, result: currentOutput});
  }
};
`

const web3FilterProvider = `
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

func newEmbeddedWeb3(t *testing.T) *JSRE {
	t.Helper()

	re := New("", os.Stdout)
	t.Cleanup(func() { re.Stop(false) })
	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}
	return re
}

func runWeb3JSON(t *testing.T, re *JSRE, script string, result any) {
	t.Helper()

	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run web3 script: %v", err)
	}
	if err := json.Unmarshal([]byte(value.String()), result); err != nil {
		t.Fatalf("decode web3 result %q: %v", value.String(), err)
	}
}
