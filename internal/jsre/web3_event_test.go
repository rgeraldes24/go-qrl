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
	"math/big"
	"os"
	"reflect"
	"strings"
	"testing"

	qrlabi "github.com/theQRL/go-qrl/accounts/abi"
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

func TestEmbeddedWeb3DynamicArrayEncodingMatchesGoABI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		typ         string
		jsValue     string
		goValue     any
		wantDecoded string
	}{
		{
			name:        "string slice",
			typ:         "string[]",
			jsValue:     `["alpha", "bravo"]`,
			goValue:     []string{"alpha", "bravo"},
			wantDecoded: `["alpha","bravo"]`,
		},
		{
			name:        "bytes slice",
			typ:         "bytes[]",
			jsValue:     `["0x0102", "0x030405"]`,
			goValue:     [][]byte{{0x01, 0x02}, {0x03, 0x04, 0x05}},
			wantDecoded: `["0x0102","0x030405"]`,
		},
		{
			name:        "nested string slice",
			typ:         "string[][]",
			jsValue:     `[["alpha"], ["bravo", "charlie"]]`,
			goValue:     [][]string{{"alpha"}, {"bravo", "charlie"}},
			wantDecoded: `[["alpha"],["bravo","charlie"]]`,
		},
		{
			name:        "static string array",
			typ:         "string[2]",
			jsValue:     `["alpha", "bravo"]`,
			goValue:     [2]string{"alpha", "bravo"},
			wantDecoded: `["alpha","bravo"]`,
		},
		{
			name:        "dynamic array of static string arrays",
			typ:         "string[2][]",
			jsValue:     `[["alpha", "bravo"], ["charlie", "delta"]]`,
			goValue:     [][2]string{{"alpha", "bravo"}, {"charlie", "delta"}},
			wantDecoded: `[["alpha","bravo"],["charlie","delta"]]`,
		},
		{
			name:        "static array of static string arrays",
			typ:         "string[2][2]",
			jsValue:     `[["alpha", "bravo"], ["charlie", "delta"]]`,
			goValue:     [2][2]string{{"alpha", "bravo"}, {"charlie", "delta"}},
			wantDecoded: `[["alpha","bravo"],["charlie","delta"]]`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			re := New("", os.Stdout)
			defer re.Stop(false)

			if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
				t.Fatalf("compile bignumber.js: %v", err)
			}
			if err := re.Compile("web3.js", deps.Web3JS); err != nil {
				t.Fatalf("compile web3.js: %v", err)
			}

			abiJSON := fmt.Sprintf(`[{"type":"function","name":"f","constant":true,"inputs":[{"name":"values","type":%q}],"outputs":[{"name":"","type":%q}]}]`, tt.typ, tt.typ)
			goABI, err := qrlabi.JSON(strings.NewReader(abiJSON))
			if err != nil {
				t.Fatalf("parse ABI: %v", err)
			}
			packed, err := goABI.Pack("f", tt.goValue)
			if err != nil {
				t.Fatalf("pack Go ABI calldata: %v", err)
			}
			output, err := goABI.Methods["f"].Outputs.PackValues([]any{tt.goValue})
			if err != nil {
				t.Fatalf("pack Go ABI output: %v", err)
			}
			wantData := "0x" + fmt.Sprintf("%x", packed)
			outputHex := "0x" + fmt.Sprintf("%x", output)

			script := fmt.Sprintf(`
var capturedData = null;
var provider = {
  send: function(payload) {
    if (payload.method === "qrl_call") {
      capturedData = payload.params[0].data;
      return {jsonrpc: "2.0", id: payload.id, result: %q};
    }
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    cb(null, this.send(payload));
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
var abi = JSON.parse(%q);
var contract = web3.qrl.contract(abi).at("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000");
var data = contract.f.getData(%s);
var decoded = contract.f.call(%s);
JSON.stringify({data: data, capturedData: capturedData, decoded: JSON.stringify(decoded)});
`, outputHex, abiJSON, tt.jsValue, tt.jsValue)
			value, err := re.Run(script)
			if err != nil {
				t.Fatalf("run dynamic array ABI script: %v", err)
			}
			var got struct {
				Data         string `json:"data"`
				CapturedData string `json:"capturedData"`
				Decoded      string `json:"decoded"`
			}
			if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
				t.Fatalf("decode script result %q: %v", value.String(), err)
			}
			if got.Data != wantData {
				t.Fatalf("web3 calldata mismatch:\nhave %s\nwant %s", got.Data, wantData)
			}
			if got.CapturedData != wantData {
				t.Fatalf("web3 qrl_call data mismatch:\nhave %s\nwant %s", got.CapturedData, wantData)
			}
			if got.Decoded != tt.wantDecoded {
				t.Fatalf("web3 decoded output mismatch: have %s want %s", got.Decoded, tt.wantDecoded)
			}
		})
	}
}

func TestEmbeddedWeb3MultiReturnDynamicOutputDecoding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		outputs    string
		goValues   []any
		wantNumber string
		wantSecond string
	}{
		{
			name:       "string second output",
			outputs:    `[{"name":"","type":"uint512"},{"name":"","type":"string"}]`,
			goValues:   []any{big.NewInt(7), "hello"},
			wantNumber: "7",
			wantSecond: "hello",
		},
		{
			name:       "dynamic array second output",
			outputs:    `[{"name":"","type":"uint512"},{"name":"","type":"string[]"}]`,
			goValues:   []any{big.NewInt(7), []string{"alpha", "bravo"}},
			wantNumber: "7",
			wantSecond: `["alpha","bravo"]`,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			re := New("", os.Stdout)
			defer re.Stop(false)

			if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
				t.Fatalf("compile bignumber.js: %v", err)
			}
			if err := re.Compile("web3.js", deps.Web3JS); err != nil {
				t.Fatalf("compile web3.js: %v", err)
			}

			abiJSON := fmt.Sprintf(`[{"type":"function","name":"f","constant":true,"inputs":[],"outputs":%s}]`, tt.outputs)
			goABI, err := qrlabi.JSON(strings.NewReader(abiJSON))
			if err != nil {
				t.Fatalf("parse ABI: %v", err)
			}
			output, err := goABI.Methods["f"].Outputs.PackValues(tt.goValues)
			if err != nil {
				t.Fatalf("pack Go ABI output: %v", err)
			}
			outputHex := "0x" + fmt.Sprintf("%x", output)

			script := fmt.Sprintf(`
var provider = {
  send: function(payload) {
    if (payload.method === "qrl_call") {
      return {jsonrpc: "2.0", id: payload.id, result: %q};
    }
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    cb(null, this.send(payload));
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
var abi = JSON.parse(%q);
var contract = web3.qrl.contract(abi).at("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000");
var decoded = contract.f.call();
var second = decoded[1];
if (Array.isArray(second)) {
  second = JSON.stringify(second);
}
JSON.stringify({number: decoded[0].toString(10), second: second});
`, outputHex, abiJSON)
			value, err := re.Run(script)
			if err != nil {
				t.Fatalf("run multi-return ABI script: %v", err)
			}
			var got struct {
				Number string `json:"number"`
				Second string `json:"second"`
			}
			if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
				t.Fatalf("decode script result %q: %v", value.String(), err)
			}
			if got.Number != tt.wantNumber {
				t.Fatalf("decoded number mismatch: have %s want %s", got.Number, tt.wantNumber)
			}
			if got.Second != tt.wantSecond {
				t.Fatalf("decoded second output mismatch: have %s want %s", got.Second, tt.wantSecond)
			}
		})
	}
}

func TestEmbeddedWeb3DeclaredIntegerBounds(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	word := func(n *big.Int) string {
		return fmt.Sprintf("0x%0128x", n)
	}
	uint256Overflow := new(big.Int).Lsh(common.Big1, 256)
	maxUint512 := new(big.Int).Sub(new(big.Int).Lsh(common.Big1, uint512.WordBits), common.Big1)
	int256Overflow := new(big.Int).Lsh(common.Big1, 255)
	maxInt512 := new(big.Int).Sub(new(big.Int).Lsh(common.Big1, uint512.WordBits-1), common.Big1)

	abiJSON := `[{
  "type":"function",
  "name":"u256In",
  "inputs":[{"name":"value","type":"uint256"}],
  "outputs":[]
},{
  "type":"function",
  "name":"u512In",
  "inputs":[{"name":"value","type":"uint512"}],
  "outputs":[]
},{
  "type":"function",
  "name":"i256In",
  "inputs":[{"name":"value","type":"int256"}],
  "outputs":[]
},{
  "type":"function",
  "name":"i512In",
  "inputs":[{"name":"value","type":"int512"}],
  "outputs":[]
},{
  "type":"function",
  "name":"u256Out",
  "constant":true,
  "inputs":[],
  "outputs":[{"name":"","type":"uint256"}]
},{
  "type":"function",
  "name":"u512Out",
  "constant":true,
  "inputs":[],
  "outputs":[{"name":"","type":"uint512"}]
},{
  "type":"function",
  "name":"i256Out",
  "constant":true,
  "inputs":[],
  "outputs":[{"name":"","type":"int256"}]
},{
  "type":"function",
  "name":"i512Out",
  "constant":true,
  "inputs":[],
  "outputs":[{"name":"","type":"int512"}]
}]`
	script := fmt.Sprintf(`
var response = "0x";
var provider = {
  send: function(payload) {
    if (payload.method === "qrl_call") {
      return {jsonrpc: "2.0", id: payload.id, result: response};
    }
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    cb(null, this.send(payload));
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
var BN = web3.BigNumber;
var abi = JSON.parse(%q);
var contract = web3.qrl.contract(abi).at("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000");
function errorOf(fn) {
  try {
    fn();
    return "";
  } catch (err) {
    return err.message;
  }
}
JSON.stringify({
  u256InputOverflow: errorOf(function() { contract.u256In.getData(new BN(2).pow(256)); }),
  u512InputMax: errorOf(function() { contract.u512In.getData(new BN(2).pow(512).minus(1)); }),
  u512InputOverflow: errorOf(function() { contract.u512In.getData(new BN(2).pow(512)); }),
  i256InputOverflow: errorOf(function() { contract.i256In.getData(new BN(2).pow(255)); }),
  i256InputUnderflow: errorOf(function() { contract.i256In.getData(new BN(2).pow(255).plus(1).times(-1)); }),
  i512InputMax: errorOf(function() { contract.i512In.getData(new BN(2).pow(511).minus(1)); }),
  u256OutputOverflow: errorOf(function() { response = %q; contract.u256Out.call(); }),
  u512OutputMax: errorOf(function() { response = %q; contract.u512Out.call(); }),
  i256OutputOverflow: errorOf(function() { response = %q; contract.i256Out.call(); }),
  i512OutputMax: errorOf(function() { response = %q; contract.i512Out.call(); })
});
`, abiJSON, word(uint256Overflow), word(maxUint512), word(int256Overflow), word(maxInt512))

	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run integer bounds script: %v", err)
	}
	var got map[string]string
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	for _, key := range []string{
		"u256InputOverflow",
		"u512InputOverflow",
		"i256InputOverflow",
		"i256InputUnderflow",
		"u256OutputOverflow",
		"i256OutputOverflow",
	} {
		if got[key] == "" {
			t.Fatalf("%s should reject out-of-range integer value: %#v", key, got)
		}
	}
	for _, key := range []string{
		"u512InputMax",
		"i512InputMax",
		"u512OutputMax",
		"i512OutputMax",
	} {
		if got[key] != "" {
			t.Fatalf("%s should accept full-width integer value, got error %q in %#v", key, got[key], got)
		}
	}
}

func TestEmbeddedWeb3FixedBytesBounds(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	wantWord := "0102" + strings.Repeat("0", 124)
	validOutput := "0x" + "01" + strings.Repeat("00", 63)
	invalidOutput := "0x" + "01" + strings.Repeat("00", 62) + "02"
	tooLongInput := "0x" + strings.Repeat("11", 33)

	script := fmt.Sprintf(`
var response = "0x";
var provider = {
  send: function(payload) {
    if (payload.method === "qrl_call") {
      return {jsonrpc: "2.0", id: payload.id, result: response};
    }
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    cb(null, this.send(payload));
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
var abi = [{
  type: "function",
  name: "fixedIn",
  inputs: [{name: "value", type: "bytes32"}],
  outputs: []
},{
  type: "function",
  name: "fixedOut",
  constant: true,
  inputs: [],
  outputs: [{name: "", type: "bytes32"}]
}];
var contract = web3.qrl.contract(abi).at("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000");
function errorOf(fn) {
  try {
    fn();
    return "";
  } catch (err) {
    return err.message;
  }
}
var encodedWord = contract.fixedIn.getData("0x0102").slice(10);
var tooLongInputError = errorOf(function() { contract.fixedIn.getData(%q); });
response = %q;
var validOut = contract.fixedOut.call();
response = %q;
var invalidOutputError = errorOf(function() { contract.fixedOut.call(); });
JSON.stringify({
  encodedWord: encodedWord,
  validOut: validOut,
  tooLongInputError: tooLongInputError,
  invalidOutputError: invalidOutputError
});
`, tooLongInput, validOutput, invalidOutput)

	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run fixed-bytes bounds script: %v", err)
	}
	var got struct {
		EncodedWord        string `json:"encodedWord"`
		ValidOut           string `json:"validOut"`
		TooLongInputError  string `json:"tooLongInputError"`
		InvalidOutputError string `json:"invalidOutputError"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	if got.EncodedWord != wantWord {
		t.Fatalf("encoded bytes32 word mismatch:\nhave %s\nwant %s", got.EncodedWord, wantWord)
	}
	if got.ValidOut != "0x"+"01"+strings.Repeat("00", 31) {
		t.Fatalf("decoded bytes32 output mismatch: have %s", got.ValidOut)
	}
	if got.TooLongInputError == "" {
		t.Fatalf("bytes32 input longer than 32 bytes should fail")
	}
	if got.InvalidOutputError == "" {
		t.Fatalf("bytes32 output with non-zero right padding should fail")
	}
}

func TestEmbeddedWeb3BoolOutputValidation(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	boolWord := func(value uint64) string {
		return fmt.Sprintf("0x%0128x", value)
	}
	script := fmt.Sprintf(`
var response = "0x";
var provider = {
  send: function(payload) {
    if (payload.method === "qrl_call") {
      return {jsonrpc: "2.0", id: payload.id, result: response};
    }
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    cb(null, this.send(payload));
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
var abi = [{
  type: "function",
  name: "flag",
  constant: true,
  inputs: [],
  outputs: [{name: "", type: "bool"}]
}, {
  type: "function",
  name: "echo",
  constant: true,
  inputs: [{name: "", type: "bool"}],
  outputs: [{name: "", type: "bool"}]
}];
var contract = web3.qrl.contract(abi).at("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000");
function errorOf(fn) {
  try {
    fn();
    return "";
  } catch (err) {
    return err.message;
  }
}
response = %q;
var decodedFalse = contract.flag.call();
response = %q;
var decodedTrue = contract.flag.call();
response = %q;
var invalidError = errorOf(function() { contract.flag.call(); });
response = %q;
var encodedFalse = contract.echo.call(false);
response = %q;
var encodedTrue = contract.echo.call(true);
var invalidStringInputError = errorOf(function() { contract.echo.call("false"); });
var invalidNumberInputError = errorOf(function() { contract.echo.call(2); });
JSON.stringify({
  decodedFalse: decodedFalse,
  decodedTrue: decodedTrue,
  invalidError: invalidError,
  encodedFalse: encodedFalse,
  encodedTrue: encodedTrue,
  invalidStringInputError: invalidStringInputError,
  invalidNumberInputError: invalidNumberInputError
});
`, boolWord(0), boolWord(1), boolWord(2), boolWord(0), boolWord(1))

	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run bool output validation script: %v", err)
	}
	var got struct {
		DecodedFalse            bool   `json:"decodedFalse"`
		DecodedTrue             bool   `json:"decodedTrue"`
		InvalidError            string `json:"invalidError"`
		EncodedFalse            bool   `json:"encodedFalse"`
		EncodedTrue             bool   `json:"encodedTrue"`
		InvalidStringInputError string `json:"invalidStringInputError"`
		InvalidNumberInputError string `json:"invalidNumberInputError"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	if got.DecodedFalse {
		t.Fatalf("decoded false bool mismatch")
	}
	if !got.DecodedTrue {
		t.Fatalf("decoded true bool mismatch")
	}
	if got.InvalidError == "" {
		t.Fatalf("malformed bool word should fail")
	}
	if got.EncodedFalse {
		t.Fatalf("encoded false bool mismatch")
	}
	if !got.EncodedTrue {
		t.Fatalf("encoded true bool mismatch")
	}
	if got.InvalidStringInputError == "" {
		t.Fatalf("string bool input should fail")
	}
	if got.InvalidNumberInputError == "" {
		t.Fatalf("number bool input should fail")
	}
}

func TestEmbeddedWeb3IndexedTupleTopicsUsePrecomputedTopics(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	tupleTopic := common.BytesToLogTopic([]byte{0x12, 0x34}).Hex()
	eventTopic := common.BytesToEventSignatureLogTopic(crypto.Keccak256([]byte("TupleEvent((uint512))"))).Hex()
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
  name: "TupleEvent",
  anonymous: false,
  inputs: [{
    name: "value",
    type: "tuple",
    indexed: true,
    components: [{name: "amount", type: "uint512"}]
  }]
}];
var contract = web3.qrl.contract(abi).at(%q);
function errorOf(fn) {
  try {
    fn();
    return "";
  } catch (err) {
    return err.message;
  }
}
var filter = contract.TupleEvent({value: %q}, {});
var decoded = filter.formatter({address: %q, data: "0x", topics: filter.options.topics});
var invalidError = errorOf(function() { contract.TupleEvent({value: "0x1234"}, {}); });
JSON.stringify({
  topics: filter.options.topics,
  captured: capturedOptions.topics,
  decoded: decoded.args.value,
  invalidError: invalidError
});
`, address, tupleTopic, address)

	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run indexed tuple topic script: %v", err)
	}
	var got struct {
		Topics       []string `json:"topics"`
		Captured     []string `json:"captured"`
		Decoded      string   `json:"decoded"`
		InvalidError string   `json:"invalidError"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	wantTopics := []string{eventTopic, tupleTopic}
	if !reflect.DeepEqual(got.Topics, wantTopics) {
		t.Fatalf("encoded tuple topics mismatch: have %#v want %#v", got.Topics, wantTopics)
	}
	if !reflect.DeepEqual(got.Captured, wantTopics) {
		t.Fatalf("captured tuple topics mismatch: have %#v want %#v", got.Captured, wantTopics)
	}
	if got.Decoded != tupleTopic {
		t.Fatalf("decoded tuple topic mismatch: have %s want %s", got.Decoded, tupleTopic)
	}
	if got.InvalidError == "" {
		t.Fatalf("indexed tuple filters should require a precomputed 64-byte topic")
	}
}

func TestEmbeddedWeb3MultiReturnDynamicFixedArrayHeadOffset(t *testing.T) {
	t.Parallel()

	re := New("", os.Stdout)
	defer re.Stop(false)

	if err := re.Compile("bignumber.js", deps.BigNumberJS); err != nil {
		t.Fatalf("compile bignumber.js: %v", err)
	}
	if err := re.Compile("web3.js", deps.Web3JS); err != nil {
		t.Fatalf("compile web3.js: %v", err)
	}

	abiJSON := `[{"type":"function","name":"f","constant":true,"inputs":[],"outputs":[{"name":"","type":"string[2]"},{"name":"","type":"uint512"}]}]`
	goABI, err := qrlabi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		t.Fatalf("parse ABI: %v", err)
	}
	output, err := goABI.Methods["f"].Outputs.PackValues([]any{[2]string{"alpha", "bravo"}, big.NewInt(7)})
	if err != nil {
		t.Fatalf("pack Go ABI output: %v", err)
	}
	outputHex := "0x" + fmt.Sprintf("%x", output)

	script := fmt.Sprintf(`
var provider = {
  send: function(payload) {
    if (payload.method === "qrl_call") {
      return {jsonrpc: "2.0", id: payload.id, result: %q};
    }
    return {jsonrpc: "2.0", id: payload.id, result: null};
  },
  sendAsync: function(payload, cb) {
    cb(null, this.send(payload));
  },
  isConnected: function() { return true; }
};
var Web3 = require("web3");
var web3 = new Web3(provider);
var abi = JSON.parse(%q);
var contract = web3.qrl.contract(abi).at("Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000");
var decoded = contract.f.call();
JSON.stringify({first: JSON.stringify(decoded[0]), second: decoded[1].toString(10)});
`, outputHex, abiJSON)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run multi-return fixed-array ABI script: %v", err)
	}
	var got struct {
		First  string `json:"first"`
		Second string `json:"second"`
	}
	if err := json.Unmarshal([]byte(value.String()), &got); err != nil {
		t.Fatalf("decode script result %q: %v", value.String(), err)
	}
	if got.First != `["alpha","bravo"]` {
		t.Fatalf("decoded first output mismatch: have %s", got.First)
	}
	if got.Second != "7" {
		t.Fatalf("decoded second output mismatch: have %s want 7", got.Second)
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
	mixedCaseTopic := "0X" + strings.Repeat("Aa", common.LogTopicLength)
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
var rawTopic = web3.sha3("Transfer(address,uint512)");
var filter = web3.qrl.filter({fromBlock: "0x0", toBlock: "latest", topics: [rawTopic]});
JSON.stringify({
  raw: rawTopic,
  rawIsTopic: web3._extend.utils.isTopic(rawTopic),
  paddedIsTopic: web3._extend.utils.isTopic(filter.options.topics[0]),
  mixedCaseIsTopic: web3._extend.utils.isTopic(%q),
  options: filter.options.topics,
  captured: capturedOptions.topics
});
`, mixedCaseTopic)
	value, err := re.Run(script)
	if err != nil {
		t.Fatalf("run raw topic script: %v", err)
	}
	var got struct {
		Raw              string   `json:"raw"`
		RawIsTopic       bool     `json:"rawIsTopic"`
		PaddedIsTopic    bool     `json:"paddedIsTopic"`
		MixedCaseIsTopic bool     `json:"mixedCaseIsTopic"`
		Options          []string `json:"options"`
		Captured         []string `json:"captured"`
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
	if !got.MixedCaseIsTopic {
		t.Fatalf("mixed-case 64-byte topic should pass VM64 topic validation")
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

func TestWeb3ExtQRLAddressFormatters(t *testing.T) {
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
    if (payload.method === "qrl_getLogs") {
      result = [];
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
	if err := re.Compile("qrl.js", web3ext.Modules["qrl"]); err != nil {
		t.Fatalf("compile qrl.js: %v", err)
	}

	address := "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"
	lowerPrefix := "q" + address[1:]
	script := fmt.Sprintf(`
web3.qrl.sign(%q, "0x01");
web3.qrl.getProof(%q, [], "latest");
web3.qrl.getLogs({address: [%q], topics: []});
var lowerErrors = {};
try {
  web3.qrl.sign(%q, "0x01");
} catch (err) {
  lowerErrors.sign = err.message;
}
try {
  web3.qrl.getProof(%q, [], "latest");
} catch (err) {
  lowerErrors.getProof = err.message;
}
try {
  web3.qrl.getLogs({address: [%q], topics: []});
} catch (err) {
  lowerErrors.getLogs = err.message;
}
JSON.stringify({
  captured: captured.map(function(call) { return {method: call.method, params: call.params}; }),
  lowerErrors: lowerErrors
});
`, address, address, address, lowerPrefix, lowerPrefix, lowerPrefix)

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
		"qrl_sign",
		"qrl_getProof",
		"qrl_getLogs",
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
	for _, key := range []string{"sign", "getProof", "getLogs"} {
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
