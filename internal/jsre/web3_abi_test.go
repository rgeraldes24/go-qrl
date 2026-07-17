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
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/internal/jsre/deps"
)

func TestEmbeddedWeb3ABICoderUsesVM64Words(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	address := "Q" + strings.Repeat("a", common.AddressLength*2)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	maxUint512 := strings.Repeat("f", common.StorageValue64Length*2)
	maxUint512Decimal := "134078079299425970995740249982058461274793658205923933" +
		"77723561443721764030073546976801874298166903427690031" +
		"858186486050853753882811946569946433649006084095"
	bytes33 := strings.Repeat("ab", 33)
	bytes33Word := bytes33 + strings.Repeat("0", (common.StorageValue64Length-33)*2)
	offsetWord := abiWordHex(5 * common.StorageValue64Length)
	boolWord := abiWordHex(1)
	lengthWord := abiWordHex(5)
	labelWord := common.Bytes2Hex([]byte("hello")) + strings.Repeat("0", common.StorageValue64Length*2-len("hello")*2)
	output := "0x" + strings.Repeat("a", common.AddressLength*2) + maxUint512 + offsetWord + boolWord + bytes33Word + lengthWord + labelWord
	expectedData := "0x" +
		common.Bytes2Hex(crypto.Keccak256([]byte("store(address,uint512,string,bool,bytes33)"))[:4]) +
		strings.Repeat("a", common.AddressLength*2) + maxUint512 + offsetWord + boolWord + bytes33Word + lengthWord + labelWord
	expectedEmptyTagData := "0x" +
		common.Bytes2Hex(crypto.Keccak256([]byte("storeTag(bytes33)"))[:4]) +
		strings.Repeat("0", common.StorageValue64Length*2)

	script := fmt.Sprintf(web3EchoProvider+`
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
var loadRequest = contract.load.request();
var pendingLoadRequest = contract.load.request("pending");
var decoded = contract.load();
var loadMethod = lastPayload.method;
contract.pay({from: %q, value: 1});

JSON.stringify({
  data: data,
  emptyTagData: emptyTagData,
  loadRequestMethod: loadRequest.method,
  loadRequestParams: loadRequest.params.length,
  loadRequestBlock: loadRequest.params[1],
  pendingLoadRequestBlock: pendingLoadRequest.params[1],
  address: decoded[0],
  amount: decoded[1].toString(16),
  label: decoded[2],
  active: decoded[3],
  tag: decoded[4],
  loadMethod: loadMethod,
  payMethod: lastPayload.method
});
`, output, contractAddress, address, maxUint512Decimal, bytes33, address)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run ABI coder script: %v", err)
	}
	var got struct {
		Data                    string `json:"data"`
		EmptyTagData            string `json:"emptyTagData"`
		LoadRequestMethod       string `json:"loadRequestMethod"`
		LoadRequestParams       int    `json:"loadRequestParams"`
		LoadRequestBlock        string `json:"loadRequestBlock"`
		PendingLoadRequestBlock string `json:"pendingLoadRequestBlock"`
		Address                 string `json:"address"`
		Amount                  string `json:"amount"`
		Label                   string `json:"label"`
		Active                  bool   `json:"active"`
		Tag                     string `json:"tag"`
		LoadMethod              string `json:"loadMethod"`
		PayMethod               string `json:"payMethod"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode ABI coder result %q: %v", value.String(), err)
	}
	if got.Data != expectedData {
		t.Fatalf("calldata mismatch:\nhave %s\nwant %s", got.Data, expectedData)
	}
	if got.EmptyTagData != expectedEmptyTagData {
		t.Fatalf("empty fixed bytes calldata mismatch:\nhave %s\nwant %s", got.EmptyTagData, expectedEmptyTagData)
	}
	if got.LoadRequestMethod != "qrl_call" || got.LoadRequestParams != 2 || got.LoadRequestBlock != "latest" || got.PendingLoadRequestBlock != "pending" {
		t.Fatalf("call request mismatch: %+v", got)
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
	payloadWord := payload + strings.Repeat("0", common.StorageValue64Length*2-len(payload))
	bytesOutput := "0x" + abiWordHex(common.StorageValue64Length) + abiWordHex(3) + payloadWord
	bytesData := "0x" +
		common.Bytes2Hex(crypto.Keccak256([]byte("storeBytes(bytes)"))[:4]) +
		abiWordHex(common.StorageValue64Length) + abiWordHex(3) + payloadWord

	arraysOutput := "0x" +
		abiWordHex(1) + abiWordHex(2) + abiWordHex(3*common.StorageValue64Length) +
		abiWordHex(3) + abiWordHex(3) + abiWordHex(4) + abiWordHex(5)
	arraysData := "0x" +
		common.Bytes2Hex(crypto.Keccak256([]byte("storeArrays(uint512[2],uint512[])"))[:4]) +
		strings.TrimPrefix(arraysOutput, "0x")

	script := fmt.Sprintf(web3EchoProvider+`
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
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run dynamic bytes and arrays script: %v", err)
	}
	var got struct {
		BytesData     string   `json:"bytesData"`
		DecodedBytes  string   `json:"decodedBytes"`
		PureMethod    string   `json:"pureMethod"`
		ArraysData    string   `json:"arraysData"`
		FixedValues   []string `json:"fixedValues"`
		DynamicValues []string `json:"dynamicValues"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode dynamic bytes and arrays result %q: %v", value.String(), err)
	}
	if got.BytesData != bytesData || got.DecodedBytes != "0x"+payload {
		t.Fatalf("dynamic bytes mismatch: %+v", got)
	}
	if got.PureMethod != "qrl_call" {
		t.Fatalf("pure function used %q, want qrl_call", got.PureMethod)
	}
	if got.ArraysData != arraysData || strings.Join(got.FixedValues, ",") != "1,2" || strings.Join(got.DynamicValues, ",") != "3,4,5" {
		t.Fatalf("array encoding mismatch: %+v", got)
	}
}

func TestEmbeddedWeb3SignedInt512AndAddressInput(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	negativeOneWord := strings.Repeat("f", common.StorageValue64Length*2)
	minimumWord := "8" + strings.Repeat("0", common.StorageValue64Length*2-1)
	minimumValue := "-0x" + minimumWord
	negativeOneData := "0x" + common.Bytes2Hex(crypto.Keccak256([]byte("storeInt(int512)"))[:4]) + negativeOneWord
	minimumData := "0x" + common.Bytes2Hex(crypto.Keccak256([]byte("storeInt(int512)"))[:4]) + minimumWord

	script := fmt.Sprintf(web3EchoProvider+`
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
var rejectsInvalidAddress = false;
try {
  contract.storeAddress.getData("Q1234");
} catch (err) {
  rejectsInvalidAddress = true;
}

JSON.stringify({
  negativeOneData: negativeOneData,
  minimumData: minimumData,
  negativeOne: negativeOne,
  minimum: minimum,
  rejectsInvalidAddress: rejectsInvalidAddress
});
`, contractAddress, minimumValue, negativeOneWord, minimumWord)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run signed integer script: %v", err)
	}
	var got struct {
		NegativeOneData       string `json:"negativeOneData"`
		MinimumData           string `json:"minimumData"`
		NegativeOne           string `json:"negativeOne"`
		Minimum               string `json:"minimum"`
		RejectsInvalidAddress bool   `json:"rejectsInvalidAddress"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode signed integer result %q: %v", value.String(), err)
	}
	if got.NegativeOneData != negativeOneData || got.MinimumData != minimumData {
		t.Fatalf("signed calldata mismatch: %+v", got)
	}
	if got.NegativeOne != "-1" || got.Minimum != "-"+minimumWord || !got.RejectsInvalidAddress {
		t.Fatalf("signed decode or address validation mismatch: %+v", got)
	}
}

func TestEmbeddedWeb3ConstructorStateMutability(t *testing.T) {
	t.Parallel()

	re := newEmbeddedWeb3(t)
	script := web3EchoProvider + `
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
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run constructor stateMutability script: %v", err)
	}
	var got struct {
		CurrentPayable    bool `json:"currentPayable"`
		CurrentNonpayable bool `json:"currentNonpayable"`
		LegacyPayable     bool `json:"legacyPayable"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode constructor stateMutability result %q: %v", value.String(), err)
	}
	if !got.CurrentPayable || got.CurrentNonpayable || got.LegacyPayable {
		t.Fatalf("constructor stateMutability mismatch: %+v", got)
	}
}

func abiWordHex(value uint64) string {
	return fmt.Sprintf("%0*x", common.StorageValue64Length*2, value)
}

const web3EchoProvider = `
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
