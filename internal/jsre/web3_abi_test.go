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
  inputs: [
    {name: "to", type: "address"},
    {name: "amount", type: "uint512"},
    {name: "label", type: "string"},
    {name: "active", type: "bool"},
    {name: "tag", type: "bytes4"}
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
    {name: "tag", type: "bytes4"}
  ],
  stateMutability: "view",
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
	item := tupleValue{Count: big.NewInt(1), Owner: owner, Label: "primary"}
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
var item = {count: 1, owner: %q, label: "primary"};
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

func TestEmbeddedWeb3SupportsFullWidthScalars(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	two512 := new(big.Int).Lsh(big.NewInt(1), 512)

	uintSelector := common.Bytes2Hex(crypto.Keccak256([]byte("encU512(uint512)"))[:4])
	intSelector := common.Bytes2Hex(crypto.Keccak256([]byte("encI512(int512)"))[:4])
	bytes64Selector := common.Bytes2Hex(crypto.Keccak256([]byte("encB64(bytes64)"))[:4])

	bytes64Value := strings.Repeat("ab", common.LogTopicLength)
	maxUint512 := new(big.Int).Sub(two512, big.NewInt(1))

	wantMaxUint512 := "0x" + uintSelector + fmt.Sprintf("%x", maxUint512)
	wantNegativeOne := "0x" + intSelector + strings.Repeat("f", common.LogTopicLength*2)
	wantBytes64 := "0x" + bytes64Selector + bytes64Value

	script := fmt.Sprintf(echoProviderJS+`
var Web3 = require("web3");
var web3 = new Web3(provider);
var contract = web3.qrl.contract([
  {name: "encU512", inputs: [{name: "v", type: "uint512"}], outputs: [], stateMutability: "nonpayable", type: "function"},
  {name: "encI512", inputs: [{name: "v", type: "int512"}], outputs: [], stateMutability: "nonpayable", type: "function"},
  {name: "encB64", inputs: [{name: "v", type: "bytes64"}], outputs: [], stateMutability: "nonpayable", type: "function"}
]).at(%q);

JSON.stringify({
  maxUint512: contract.encU512.getData(%q),
  negativeOne: contract.encI512.getData(-1),
  bytes64: contract.encB64.getData("0x%s")
});
`, contractAddress, maxUint512.String(), bytes64Value)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run full-width scalar script: %v", err)
	}
	var got struct {
		MaxUint512  string `json:"maxUint512"`
		NegativeOne string `json:"negativeOne"`
		Bytes64     string `json:"bytes64"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode scalar result %q: %v", value.String(), err)
	}
	for _, tc := range []struct{ name, have, want string }{
		{"max uint512", got.MaxUint512, wantMaxUint512},
		{"negative int512", got.NegativeOne, wantNegativeOne},
		{"bytes64", got.Bytes64, wantBytes64},
	} {
		if tc.have != tc.want {
			t.Errorf("%s mismatch:\nhave %s\nwant %s", tc.name, tc.have, tc.want)
		}
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
