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

func TestEmbeddedWeb3ABICoderUsesVM64Words(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)

	address := "Q" + strings.Repeat("a", common.AddressLength*2)
	expectedDecodedAddress := common.MustParseAddress(address).Hex()
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
	if got.DecodedAddress != expectedDecodedAddress {
		t.Fatalf("decoded address mismatch: have %q want %q", got.DecodedAddress, expectedDecodedAddress)
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
	gappedString := "0x" + abiWordHexUint64(2*common.LogTopicLength) + abiWordHexUint64(99) + abiWordHexUint64(0)
	aliasedStrings := "0x" + abiWordHexUint64(2*common.LogTopicLength) + abiWordHexUint64(2*common.LogTopicLength) + abiWordHexUint64(0)

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
  {name: "decodeString", inputs: [], outputs: [{name: "value", type: "string"}], type: "function"},
  {name: "decodeStrings", inputs: [], outputs: [{name: "a", type: "string"}, {name: "b", type: "string"}], type: "function"}
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
currentOutput = "0x" + %q + %q;
var rejectsTrailingWord = rejects(function() { contract.decodeUint512.call(); });
currentOutput = %q;
var rejectsTruncatedString = rejects(function() { contract.decodeString.call(); });
currentOutput = %q;
var rejectsGappedString = rejects(function() { contract.decodeString.call(); });
currentOutput = %q;
var rejectsAliasedStrings = rejects(function() { contract.decodeStrings.call(); });

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
  rejectsTrailingWord: rejectsTrailingWord,
  rejectsTruncatedString: rejectsTruncatedString,
  rejectsGappedString: rejectsGappedString,
  rejectsAliasedStrings: rejectsAliasedStrings
});
`, contractAddress, maxSignedHex, abiWordHexUint64(2), abiWordHexUint64(256), abiWordHexUint64(1), abiWordHexUint64(2), truncatedString, gappedString, aliasedStrings, twoTo512.String())

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
		RejectsTrailingWord    bool   `json:"rejectsTrailingWord"`
		RejectsTruncatedString bool   `json:"rejectsTruncatedString"`
		RejectsGappedString    bool   `json:"rejectsGappedString"`
		RejectsAliasedStrings  bool   `json:"rejectsAliasedStrings"`
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
		"trailing static word":   got.RejectsTrailingWord,
		"truncated string":       got.RejectsTruncatedString,
		"gapped string tail":     got.RejectsGappedString,
		"aliased string tails":   got.RejectsAliasedStrings,
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

func TestEmbeddedWeb3HandlesHyperionTupleABI(t *testing.T) {
	t.Parallel()

	const contractABI = `[
  {"inputs":[{"components":[{"name":"count","type":"uint512"},{"name":"owner","type":"address"},{"name":"label","type":"string"}],"name":"item","type":"tuple"},{"components":[{"name":"count","type":"uint512"},{"name":"owner","type":"address"},{"name":"label","type":"string"}],"name":"items","type":"tuple[]"}],"name":"storeTuples","outputs":[],"stateMutability":"payable","type":"function"},
  {"inputs":[],"name":"loadTuples","outputs":[{"components":[{"name":"count","type":"uint512"},{"name":"owner","type":"address"},{"name":"label","type":"string"}],"name":"item","type":"tuple"},{"components":[{"name":"count","type":"uint512"},{"name":"owner","type":"address"},{"name":"label","type":"string"}],"name":"items","type":"tuple[]"}],"stateMutability":"view","type":"function"},
  {"inputs":[{"components":[{"name":"count","type":"uint512"},{"name":"owner","type":"address"},{"name":"label","type":"string"}],"name":"item","type":"tuple"}],"name":"storeNonPayable","outputs":[],"stateMutability":"nonpayable","type":"function"}
]`
	type tupleValue struct {
		Count *big.Int
		Owner common.Address
		Label string
	}

	parsedABI, err := abi.JSON(strings.NewReader(contractABI))
	if err != nil {
		t.Fatal(err)
	}
	owner := common.MustParseAddress("Q" + strings.Repeat("a", common.AddressLength*2))
	item := tupleValue{Count: big.NewInt(1), Owner: owner, Label: "a\x00b"}
	items := []tupleValue{{Count: big.NewInt(2), Owner: owner, Label: "nested"}}
	wantData, err := parsedABI.Pack("storeTuples", item, items)
	if err != nil {
		t.Fatal(err)
	}
	wantOutput, err := parsedABI.Methods["loadTuples"].Outputs.Pack(item, items)
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
var item = {count: 1, owner: %q, label: "a\u0000b"};
var items = [{count: 2, owner: %q, label: "nested"}];
var data = contract.storeTuples.getData(item, items, {from: %q, gas: 123456});
var loadRequest = contract.loadTuples.request();
var storeRequest = contract.storeTuples.request(item, items, {from: %q});

currentOutput = "0xabc";
var txHash = contract.storeTuples.sendTransaction(item, items, {from: %q, value: 1});
var rejectsNonPayableValue = false;
try {
  contract.storeNonPayable.sendTransaction(item, {from: %q, value: 1});
} catch (err) {
  rejectsNonPayableValue = true;
}

currentOutput = %q;
var decoded = contract.loadTuples.call();

JSON.stringify({
  data: data,
  txHash: txHash,
  loadMethod: loadRequest.method,
  storeMethod: storeRequest.method,
  rejectsNonPayableValue: rejectsNonPayableValue,
  itemCount: decoded[0].count.toString(10),
  itemOwner: decoded[0].owner,
  itemLabel: decoded[0].label,
  nestedCount: decoded[1][0].count.toString(10),
  nestedOwner: decoded[1][0].owner,
  nestedLabel: decoded[1][0].label
});
`, "0x"+common.Bytes2Hex(wantOutput), contractABI, contractAddress, owner.Hex(), owner.Hex(), contractAddress, contractAddress, contractAddress, contractAddress, "0x"+common.Bytes2Hex(wantOutput))
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run tuple ABI script: %v", err)
	}
	var got struct {
		Data                   string `json:"data"`
		TxHash                 string `json:"txHash"`
		LoadMethod             string `json:"loadMethod"`
		StoreMethod            string `json:"storeMethod"`
		RejectsNonPayableValue bool   `json:"rejectsNonPayableValue"`
		ItemCount              string `json:"itemCount"`
		ItemOwner              string `json:"itemOwner"`
		ItemLabel              string `json:"itemLabel"`
		NestedCount            string `json:"nestedCount"`
		NestedOwner            string `json:"nestedOwner"`
		NestedLabel            string `json:"nestedLabel"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode tuple result %q: %v", value.String(), err)
	}
	if want := "0x" + common.Bytes2Hex(wantData); got.Data != want {
		t.Fatalf("tuple calldata mismatch:\nhave %s\nwant %s", got.Data, want)
	}
	if got.TxHash != "0xabc" || got.LoadMethod != "qrl_call" || got.StoreMethod != "qrl_sendTransaction" || !got.RejectsNonPayableValue {
		t.Fatalf("state mutability mismatch: hash=%q load=%q store=%q rejectsNonPayable=%t", got.TxHash, got.LoadMethod, got.StoreMethod, got.RejectsNonPayableValue)
	}
	if got.ItemCount != "1" || got.ItemOwner != owner.Hex() || got.ItemLabel != item.Label {
		t.Fatalf("decoded tuple mismatch: count=%q owner=%q label=%q", got.ItemCount, got.ItemOwner, got.ItemLabel)
	}
	if got.NestedCount != "2" || got.NestedOwner != owner.Hex() || got.NestedLabel != items[0].Label {
		t.Fatalf("decoded nested tuple mismatch: count=%q owner=%q label=%q", got.NestedCount, got.NestedOwner, got.NestedLabel)
	}
}

func TestEmbeddedWeb3BatchContinuesAfterDecodeError(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	wordOne := abiWordHexUint64(1)
	script := fmt.Sprintf(`
var seenMethods = [];
var provider = {
  send: function(payload) {
    return {jsonrpc: "2.0", id: payload.id, result: "0x"};
  },
  sendAsync: function(payload, callback) {
    if (payload instanceof Array) {
      seenMethods = payload.map(function(request) { return request.method; });
      callback(null, [
        {jsonrpc: "2.0", id: payload[0].id, result: "0x"},
        {jsonrpc: "2.0", id: payload[1].id, result: %q}
      ]);
      return;
    }
    callback(null, {jsonrpc: "2.0", id: payload.id, result: "0x"});
  },
  isConnected: function() { return true; }
};

var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([
  {inputs: [], name: "first", outputs: [{name: "value", type: "uint512"}], stateMutability: "view", type: "function"},
  {inputs: [], name: "second", outputs: [{name: "value", type: "uint512"}], stateMutability: "pure", type: "function"}
]).at(%q);
var firstErrored = false;
var secondValue = null;
var callbackCount = 0;
var batch = web3.createBatch();
batch.add(contract.first.request(function(error) {
  firstErrored = !!error;
  callbackCount++;
}));
batch.add(contract.second.request(function(error, value) {
  if (!error) secondValue = value.toString(10);
  callbackCount++;
}));
batch.execute();

JSON.stringify({
  firstErrored: firstErrored,
  secondValue: secondValue,
  callbackCount: callbackCount,
  seenMethods: seenMethods
});
`, "0X"+strings.ToUpper(wordOne), contractAddress)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run batch script: %v", err)
	}
	var got struct {
		FirstErrored  bool     `json:"firstErrored"`
		SecondValue   string   `json:"secondValue"`
		CallbackCount int      `json:"callbackCount"`
		SeenMethods   []string `json:"seenMethods"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode batch result %q: %v", value.String(), err)
	}
	if !got.FirstErrored || got.SecondValue != "1" || got.CallbackCount != 2 {
		t.Fatalf("batch callback mismatch: error=%t second=%q count=%d", got.FirstErrored, got.SecondValue, got.CallbackCount)
	}
	if want := []string{"qrl_call", "qrl_call"}; !reflect.DeepEqual(got.SeenMethods, want) {
		t.Fatalf("batch methods mismatch: have %#v want %#v", got.SeenMethods, want)
	}
}

func TestEmbeddedWeb3RejectsBareIntegerTypes(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)

	script := fmt.Sprintf(echoProviderJS+`
var Web3 = require("web3");
var web3 = new Web3(provider);

function rejects(type) {
  try {
    var contract = web3.qrl.contract([{
      constant: false,
      inputs: [{name: "value", type: type}],
      name: "store",
      outputs: [],
      type: "function"
    }]).at(%q);
    contract.store.getData(0);
    return false;
  } catch (err) {
    return true;
  }
}

function rejectsQualifiedBareInt() {
  try {
    web3.qrl.contract([{
      constant: false,
      inputs: [{name: "value", type: "uint"}],
      name: "store(uint)",
      outputs: [],
      type: "function"
    }]).at(%q);
    return false;
  } catch (err) {
    return true;
  }
}

JSON.stringify({
  uint: rejects("uint"),
  int: rejects("int"),
  uintArray: rejects("uint[]"),
  intArray: rejects("int[2]"),
  qualifiedUint: rejectsQualifiedBareInt(),
  real: rejects("real"),
  ureal: rejects("ureal")
});
`, contractAddress, contractAddress)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run bare integer script: %v", err)
	}
	var got map[string]bool
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode bare integer result %q: %v", value.String(), err)
	}
	for name, rejected := range got {
		if !rejected {
			t.Errorf("embedded web3 accepted bare ABI type %s", name)
		}
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
  upperHexString: contract.encU512.getData("0X10"),
  negativeHexString: contract.encI512.getData("-0x10"),
  upperNegativeHexString: contract.encI512.getData("-0X10"),
  maxSafeInteger: contract.encU512.getData(9007199254740991),
  maxUint512: contract.encU512.getData(%q),
  int8Max: contract.encI8.getData(127),
  int8Min: contract.encI8.getData(-128),
  bytes64: contract.encB64.getData("0x%s"),
  upperBytes64: contract.encB64.getData("0X%s"),
  twosComplementNeg1: web3._extend.utils.toTwosComplement(-1).toString(16),
  sendFormatIsNull: contract.encU512.request(1).format === null,
  callFormatIsFunction: typeof contract.load.request().format === "function",
  rejectsFractionString: rejects(function() { contract.encU512.getData("1.5"); })
});
`, contractAddress, maxUint512.String(), bytes64Value, bytes64Value)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run integer input script: %v", err)
	}
	var got struct {
		HexString              string `json:"hexString"`
		UpperHexString         string `json:"upperHexString"`
		NegativeHexString      string `json:"negativeHexString"`
		UpperNegativeHexString string `json:"upperNegativeHexString"`
		MaxSafeInteger         string `json:"maxSafeInteger"`
		MaxUint512             string `json:"maxUint512"`
		Int8Max                string `json:"int8Max"`
		Int8Min                string `json:"int8Min"`
		Bytes64                string `json:"bytes64"`
		UpperBytes64           string `json:"upperBytes64"`
		TwosComplementNeg1     string `json:"twosComplementNeg1"`
		SendFormatIsNull       bool   `json:"sendFormatIsNull"`
		CallFormatIsFunction   bool   `json:"callFormatIsFunction"`
		RejectsFraction        bool   `json:"rejectsFractionString"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode integer input result %q: %v", value.String(), err)
	}
	for _, tc := range []struct{ name, have, want string }{
		{"hex string", got.HexString, wantHex16},
		{"uppercase hex string", got.UpperHexString, wantHex16},
		{"negative hex string", got.NegativeHexString, wantNegHex16},
		{"uppercase negative hex string", got.UpperNegativeHexString, wantNegHex16},
		{"max safe integer", got.MaxSafeInteger, wantMaxSafe},
		{"max uint512", got.MaxUint512, wantMaxUint512},
		{"int8 max", got.Int8Max, wantInt8Max},
		{"int8 min", got.Int8Min, wantInt8Min},
		{"bytes64", got.Bytes64, wantBytes64},
		{"uppercase bytes64", got.UpperBytes64, wantBytes64},
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

func TestEmbeddedWeb3RejectsSparseArraysAndInvalidAddresses(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)

	script := fmt.Sprintf(echoProviderJS+`
currentOutput = "0x1";

var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([
  {name: "arr", inputs: [{name: "v", type: "uint512[2]"}], outputs: [], type: "function", constant: false},
  {name: "dyn", inputs: [{name: "v", type: "uint512[]"}], outputs: [], type: "function", constant: false},
  {name: "addr", inputs: [{name: "v", type: "address"}], outputs: [], type: "function", constant: false},
  {name: "addrArr", inputs: [{name: "v", type: "address[2]"}], outputs: [], type: "function", constant: false},
  {name: "addrDyn", inputs: [{name: "v", type: "address[]"}], outputs: [], type: "function", constant: false}
]).at(%q);

function rejects(fn) {
  try { fn(); return false; } catch (err) { return true; }
}

JSON.stringify({
  rejectsSparseStatic: rejects(function() { contract.arr.getData(new Array(2)); }),
  rejectsSparseDynamic: rejects(function() { contract.dyn.getData(new Array(3)); }),
  rejectsInvalidAddress: rejects(function() { contract.addr.getData("not-an-address"); }),
  rejectsSparseAddressStatic: rejects(function() { contract.addrArr.getData(new Array(2)); }),
  rejectsSparseAddressDynamic: rejects(function() { contract.addrDyn.getData(new Array(2)); })
});
`, contractAddress)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run sparse array script: %v", err)
	}
	var got struct {
		RejectsSparseStatic   bool `json:"rejectsSparseStatic"`
		RejectsSparseDynamic  bool `json:"rejectsSparseDynamic"`
		RejectsInvalidAddress bool `json:"rejectsInvalidAddress"`
		RejectsAddressStatic  bool `json:"rejectsSparseAddressStatic"`
		RejectsAddressDynamic bool `json:"rejectsSparseAddressDynamic"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode sparse array result %q: %v", value.String(), err)
	}
	if !got.RejectsSparseStatic || !got.RejectsSparseDynamic {
		t.Errorf("sparse arrays should be rejected: static=%t dynamic=%t", got.RejectsSparseStatic, got.RejectsSparseDynamic)
	}
	if !got.RejectsInvalidAddress || !got.RejectsAddressStatic || !got.RejectsAddressDynamic {
		t.Errorf("invalid address inputs should be rejected: scalar=%t static=%t dynamic=%t", got.RejectsInvalidAddress, got.RejectsAddressStatic, got.RejectsAddressDynamic)
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
