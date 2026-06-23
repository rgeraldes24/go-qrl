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
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/internal/jsre/deps"
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
