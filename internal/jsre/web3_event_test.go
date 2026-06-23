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

package jsre

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/internal/jsre/deps"
	"github.com/theQRL/go-qrl/internal/web3ext"
)

func TestEmbeddedWeb3IndexedDynamicEventTopics(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	var arrayPreimage []byte
	for _, n := range []uint64{1, 2} {
		word := uint512.NewInt(n).Bytes64()
		arrayPreimage = append(arrayPreimage, word[:]...)
	}
	expected := []string{
		common.BytesToEventSignatureLogTopic(crypto.Keccak256([]byte("E(string,bytes,uint512[])"))).Hex(),
		common.BytesToLogTopic(crypto.Keccak256([]byte("hello"))).Hex(),
		common.BytesToLogTopic(crypto.Keccak256([]byte{1, 2, 3})).Hex(),
		common.BytesToLogTopic(crypto.Keccak256(arrayPreimage)).Hex(),
	}
	address := "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"

	script := fmt.Sprintf(`
var capturedOptions = null;
var provider = {
  send: function(payload) {
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    if (payload.method === "qrl_newFilter") {
      capturedOptions = payload.params[0];
    }
    cb(null, {jsonrpc: "2.0", id: payload.id, result: "0x1"});
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
var abi = [{
  type: "event",
  name: "E",
  anonymous: false,
  inputs: [
    {name: "name", type: "string", indexed: true},
    {name: "data", type: "bytes", indexed: true},
    {name: "nums", type: "uint512[]", indexed: true}
  ]
}];
var contract = web3.qrl.contract(abi).at(%q);
var filter = contract.E({name: "hello", data: "0x010203", nums: ["1", "2"]}, {});
var decoded = filter.formatter({address: %q, data: "0x", topics: filter.options.topics});
JSON.stringify({topics: filter.options.topics, captured: capturedOptions.topics, args: decoded.args});
`, address, address)

	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run event topic script: %v", err)
	}
	var got struct {
		Topics   []string          `json:"topics"`
		Captured []string          `json:"captured"`
		Args     map[string]string `json:"args"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	if !reflect.DeepEqual(got.Topics, expected) {
		t.Fatalf("encoded topics mismatch:\nhave %#v\nwant %#v", got.Topics, expected)
	}
	if !reflect.DeepEqual(got.Captured, expected) {
		t.Fatalf("captured filter topics mismatch:\nhave %#v\nwant %#v", got.Captured, expected)
	}
	if got.Args["name"] != expected[1] || got.Args["data"] != expected[2] || got.Args["nums"] != expected[3] {
		t.Fatalf("decoded indexed args mismatch: have %#v want name=%s data=%s nums=%s", got.Args, expected[1], expected[2], expected[3])
	}
}

func TestEmbeddedWeb3NestedDynamicArrayEncodingDoesNotUseNaNOffsets(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	script := `
var Web3 = require("web3");
var web3 = new Web3();
var abi = [{
  type: "function",
  name: "f",
  constant: true,
  inputs: [{name: "values", type: "string[][]"}],
  outputs: []
}];
var contract = web3.qrl.contract(abi).at("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000");
var data = contract.f.getData([["alpha"], ["bravo", "charlie"]]);
var encoded = data.slice(10);
JSON.stringify({
  hasNaN: encoded.indexOf("NaN") >= 0,
  wordAligned: encoded.length % 128 === 0,
  firstOffset: encoded.slice(0, 128)
});
`
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run nested array encoding script: %v", err)
	}
	var got struct {
		HasNaN      bool   `json:"hasNaN"`
		WordAligned bool   `json:"wordAligned"`
		FirstOffset string `json:"firstOffset"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	if got.HasNaN {
		t.Fatalf("nested dynamic array encoding produced NaN offsets")
	}
	if !got.WordAligned {
		t.Fatalf("nested dynamic array encoding should be VM-word aligned")
	}
	expectedOffset := uint512.NewInt(uint64(uint512.WordBytes)).Bytes64()
	if got.FirstOffset != fmt.Sprintf("%x", expectedOffset[:]) {
		t.Fatalf("top-level nested array offset mismatch: have %q", got.FirstOffset)
	}
}

func TestEmbeddedWeb3RawFilterTopicPadding(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	expected := common.BytesToEventSignatureLogTopic(crypto.Keccak256([]byte("Transfer(address,uint512)"))).Hex()
	script := `
var capturedOptions = null;
var provider = {
  send: function(payload) {
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    if (payload.method === "qrl_newFilter") {
      capturedOptions = payload.params[0];
    }
    cb(null, {jsonrpc: "2.0", id: payload.id, result: "0x1"});
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
var rawTopic = web3.sha3("Transfer(address,uint512)");
var filter = web3.qrl.filter({fromBlock: "0x0", toBlock: "latest", topics: [rawTopic]});
JSON.stringify({
  raw: rawTopic,
  rawIsTopic: web3._extend.utils.isTopic(rawTopic),
  paddedIsTopic: web3._extend.utils.isTopic(filter.options.topics[0]),
  options: filter.options.topics,
  captured: capturedOptions.topics
});
`
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run raw topic script: %v", err)
	}
	var got struct {
		Raw           string   `json:"raw"`
		RawIsTopic    bool     `json:"rawIsTopic"`
		PaddedIsTopic bool     `json:"paddedIsTopic"`
		Options       []string `json:"options"`
		Captured      []string `json:"captured"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	if len(got.Raw) != 66 {
		t.Fatalf("raw topic should be 32 bytes, have %q", got.Raw)
	}
	if got.RawIsTopic {
		t.Fatalf("raw 32-byte topic should not pass VM64 topic validation")
	}
	if !got.PaddedIsTopic {
		t.Fatalf("padded 64-byte topic should pass VM64 topic validation")
	}
	if !reflect.DeepEqual(got.Options, []string{expected}) {
		t.Fatalf("filter options topic mismatch: have %#v want %#v", got.Options, []string{expected})
	}
	if !reflect.DeepEqual(got.Captured, []string{expected}) {
		t.Fatalf("captured filter topic mismatch: have %#v want %#v", got.Captured, []string{expected})
	}
}

func TestEmbeddedWeb3FilterAddressFormatting(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	address := "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
	lowerPrefix := "q" + address[1:]
	script := fmt.Sprintf(`
var capturedOptions = null;
var provider = {
  send: function(payload) {
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    if (payload.method === "qrl_newFilter") {
      capturedOptions = payload.params[0];
    }
    cb(null, {jsonrpc: "2.0", id: payload.id, result: "0x1"});
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
var lowerError = null;
try {
  web3._extend.formatters.inputAddressFormatter(%q);
} catch (err) {
  lowerError = err.message;
}
var filter = web3.qrl.filter({fromBlock: "0x0", toBlock: "latest", address: %q, topics: []});
JSON.stringify({
  validUpper: web3.isAddress(%q),
  validLower: web3.isAddress(%q),
  lowerError: lowerError,
  optionAddress: filter.options.address,
  capturedAddress: capturedOptions.address
});
`, lowerPrefix, address, address, lowerPrefix)

	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run filter address script: %v", err)
	}
	var got struct {
		ValidUpper      bool   `json:"validUpper"`
		ValidLower      bool   `json:"validLower"`
		LowerError      string `json:"lowerError"`
		OptionAddress   string `json:"optionAddress"`
		CapturedAddress string `json:"capturedAddress"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	if !got.ValidUpper {
		t.Fatalf("uppercase Q address should be valid")
	}
	if got.ValidLower {
		t.Fatalf("lowercase q address should not pass console validation")
	}
	if got.LowerError != "invalid address" {
		t.Fatalf("lowercase q address formatter error mismatch: have %q", got.LowerError)
	}
	if got.OptionAddress != address || got.CapturedAddress != address {
		t.Fatalf("filter address mismatch: options=%q captured=%q want=%q", got.OptionAddress, got.CapturedAddress, address)
	}
}

func TestEmbeddedWeb3AddressMethodFormatting(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	address := "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
	lowerPrefix := "q" + address[1:]
	script := fmt.Sprintf(`
var captured = [];
var provider = {
  send: function(payload) {
    captured.push(payload);
    var result = payload.method === "qrl_getTransactionCount" ? "0x0" : "0x";
    return {jsonrpc: "2.0", id: payload.id, result: result};
  },
  sendAsync: function(payload, cb) {
    cb(null, {jsonrpc: "2.0", id: payload.id, result: null});
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
web3.qrl.getStorageAt(%q, "0x0", "latest");
web3.qrl.getTransactionCount(%q, "latest");
var storageLowerError = null;
var nonceLowerError = null;
try {
  web3.qrl.getStorageAt(%q, "0x0", "latest");
} catch (err) {
  storageLowerError = err.message;
}
try {
  web3.qrl.getTransactionCount(%q, "latest");
} catch (err) {
  nonceLowerError = err.message;
}
JSON.stringify({
  captured: captured.map(function(call) { return {method: call.method, params: call.params}; }),
  storageLowerError: storageLowerError,
  nonceLowerError: nonceLowerError
});
`, address, address, lowerPrefix, lowerPrefix)

	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run address method formatter script: %v", err)
	}
	var got struct {
		Captured []struct {
			Method string        `json:"method"`
			Params []interface{} `json:"params"`
		} `json:"captured"`
		StorageLowerError string `json:"storageLowerError"`
		NonceLowerError   string `json:"nonceLowerError"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	if len(got.Captured) != 2 {
		t.Fatalf("unexpected captured calls: %#v", got.Captured)
	}
	if got.Captured[0].Method != "qrl_getStorageAt" || got.Captured[0].Params[0] != address {
		t.Fatalf("getStorageAt params mismatch: %#v", got.Captured[0])
	}
	if got.Captured[1].Method != "qrl_getTransactionCount" || got.Captured[1].Params[0] != address {
		t.Fatalf("getTransactionCount params mismatch: %#v", got.Captured[1])
	}
	if got.StorageLowerError != "invalid address" || got.NonceLowerError != "invalid address" {
		t.Fatalf("lowercase q address errors mismatch: storage=%q nonce=%q", got.StorageLowerError, got.NonceLowerError)
	}
}

func TestWeb3ExtAddressFormatters(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}
	if _, err := re.Run(`
var captured = [];
var provider = {
  send: function(payload) {
    captured.push(payload);
    var result = null;
    if (payload.method === "debug_storageRangeAt") {
      result = {storage: {}, nextKey: null};
    } else if (payload.method === "qrl_createAccessList") {
      result = {accessList: [], gasUsed: "0x0"};
    } else if (payload.method === "debug_traceCall") {
      result = {};
    }
    return {jsonrpc: "2.0", id: payload.id, result: result};
  },
  sendAsync: function(payload, cb) {
    cb(null, this.send(payload));
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
var console = {log: function() {}};
`); err != nil {
		t.Fatalf("init web3: %v", err)
	}
	for _, module := range []string{"debug", "qrl", "dev"} {
		if err := re.Compile(module+".js", web3ext.Modules[module]); err != nil {
			t.Fatalf("compile %s.js: %v", module, err)
		}
	}

	address := "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
	lowerPrefix := "q" + address[1:]
	script := fmt.Sprintf(`
web3.debug.traceCall({from: %q, to: %q, data: "0x"}, "latest", {});
web3.debug.storageRangeAt("latest", 0, %q, "0x", 10);
web3.qrl.createAccessList({from: %q, to: %q, data: "0x"}, "latest");
web3.dev.setFeeRecipient(%q);
web3.dev.addWithdrawal({index: "0x1", validatorIndex: "0x1", address: %q, amount: "0x1"});
var lowerErrors = {};
try {
  web3.debug.traceCall({from: %q, to: %q, data: "0x"}, "latest", {});
} catch (err) {
  lowerErrors.traceCall = err.message;
}
try {
  web3.debug.storageRangeAt("latest", 0, %q, "0x", 10);
} catch (err) {
  lowerErrors.storageRangeAt = err.message;
}
try {
  web3.qrl.createAccessList({from: %q, to: %q, data: "0x"}, "latest");
} catch (err) {
  lowerErrors.createAccessList = err.message;
}
try {
  web3.dev.setFeeRecipient(%q);
} catch (err) {
  lowerErrors.setFeeRecipient = err.message;
}
try {
  web3.dev.addWithdrawal({index: "0x1", validatorIndex: "0x1", address: %q, amount: "0x1"});
} catch (err) {
  lowerErrors.addWithdrawal = err.message;
}
JSON.stringify({
  captured: captured.map(function(call) { return {method: call.method, params: call.params}; }),
  lowerErrors: lowerErrors
});
`, address, address, address, address, address, address, address, lowerPrefix, address, lowerPrefix, lowerPrefix, address, lowerPrefix, lowerPrefix)

	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run web3ext formatter script: %v", err)
	}
	var got struct {
		Captured []struct {
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		} `json:"captured"`
		LowerErrors map[string]string `json:"lowerErrors"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	wantMethods := []string{
		"debug_traceCall",
		"debug_storageRangeAt",
		"qrl_createAccessList",
		"dev_setFeeRecipient",
		"dev_addWithdrawal",
	}
	if len(got.Captured) != len(wantMethods) {
		t.Fatalf("captured call count mismatch: have %#v", got.Captured)
	}
	for i, method := range wantMethods {
		if got.Captured[i].Method != method {
			t.Fatalf("captured method %d mismatch: have %q want %q", i, got.Captured[i].Method, method)
		}
		if !strings.Contains(string(got.Captured[i].Params), address) {
			t.Fatalf("captured params for %s do not contain formatted address: %s", method, got.Captured[i].Params)
		}
	}
	for _, key := range []string{"traceCall", "storageRangeAt", "createAccessList", "setFeeRecipient", "addWithdrawal"} {
		if got.LowerErrors[key] != "invalid address" {
			t.Fatalf("%s lowercase q address error mismatch: %#v", key, got.LowerErrors)
		}
	}
}

func TestEmbeddedWeb3UnsupportedWrappersRemoved(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	script := `
var Web3 = require("web3");
var web3 = new Web3({
  send: function(payload) {
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    cb(null, {jsonrpc: "2.0", id: payload.id, result: null});
  },
  isConnected: function() { return true; }
});
JSON.stringify({
  resend: typeof web3.qrl.resend,
  submitTransaction: typeof web3.qrl.submitTransaction,
  compileHyperion: typeof web3.qrl.compile === "undefined" ? "undefined" : typeof web3.qrl.compile.hyperion
});
`
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run unsupported wrapper script: %v", err)
	}
	var got struct {
		Resend            string `json:"resend"`
		SubmitTransaction string `json:"submitTransaction"`
		CompileHyperion   string `json:"compileHyperion"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	if got.Resend != "undefined" || got.SubmitTransaction != "undefined" || got.CompileHyperion != "undefined" {
		t.Fatalf("unsupported wrappers should be absent, got %#v", got)
	}
}
