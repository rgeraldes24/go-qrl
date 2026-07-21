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
	"bytes"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/uint512"
)

func TestEmbeddedWeb3ABICoderUsesVM64Words(t *testing.T) {
	t.Parallel()

	const contractABI = `[
  {
    "inputs": [
      {"name": "to", "type": "address"},
      {"name": "amount", "type": "uint512"},
      {"name": "label", "type": "string"},
      {"name": "active", "type": "bool"},
      {"name": "tag", "type": "bytes33"}
    ],
    "name": "store",
    "outputs": [],
    "stateMutability": "nonpayable",
    "type": "function"
  },
  {
    "inputs": [],
    "name": "load",
    "outputs": [
      {"name": "to", "type": "address"},
      {"name": "amount", "type": "uint512"},
      {"name": "label", "type": "string"},
      {"name": "active", "type": "bool"},
      {"name": "tag", "type": "bytes33"}
    ],
    "stateMutability": "view",
    "type": "function"
  },
  {
    "inputs": [{"name": "tag", "type": "bytes33"}],
    "name": "storeTag",
    "outputs": [],
    "stateMutability": "nonpayable",
    "type": "function"
  },
  {
    "inputs": [],
    "name": "pay",
    "outputs": [],
    "stateMutability": "payable",
    "type": "function"
  }
]`

	re := newEmbeddedWeb3(t)
	address := "Q" + strings.Repeat("a", common.AddressLength*2)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	addressValue, err := common.NewAddressFromString(address)
	if err != nil {
		t.Fatal(err)
	}
	addressWord := common.Bytes2Hex(addressValue[:])
	maxUint512 := strings.Repeat("f", uint512.WordBytes*2)
	maxUint512Decimal := "134078079299425970995740249982058461274793658205923933" +
		"77723561443721764030073546976801874298166903427690031" +
		"858186486050853753882811946569946433649006084095"
	maxUint512Value, ok := new(big.Int).SetString(maxUint512Decimal, 10)
	if !ok {
		t.Fatal("parse max uint512")
	}
	bytes33 := strings.Repeat("ab", 33)
	var bytes33Value [33]byte
	copy(bytes33Value[:], bytes.Repeat([]byte{0xab}, len(bytes33Value)))
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

	parsedABI := mustParseABI(t, contractABI)
	if want := packCallHex(t, parsedABI, "store", addressValue, maxUint512Value, "hello", true, bytes33Value); expectedData != want {
		t.Fatalf("explicit calldata vector disagrees with Go ABI:\nhave %s\nwant %s", expectedData, want)
	}
	if want := packOutputHex(t, parsedABI, "load", addressValue, maxUint512Value, "hello", true, bytes33Value); output != want {
		t.Fatalf("explicit output vector disagrees with Go ABI:\nhave %s\nwant %s", output, want)
	}

	script := fmt.Sprintf(`
currentOutput = %q;

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
`, output, address, maxUint512Decimal, bytes33, address)
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
	runWeb3ContractJSON(t, re, web3CallProvider, contractABI, contractAddress, script, &got)
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

	const contractABI = `[
  {
    "inputs": [{"name": "value", "type": "bytes"}],
    "name": "storeBytes",
    "outputs": [],
    "stateMutability": "nonpayable",
    "type": "function"
  },
  {
    "inputs": [],
    "name": "loadBytes",
    "outputs": [{"name": "value", "type": "bytes"}],
    "stateMutability": "pure",
    "type": "function"
  },
  {
    "inputs": [
      {"name": "fixedValues", "type": "uint512[2]"},
      {"name": "dynamicValues", "type": "uint512[]"}
    ],
    "name": "storeArrays",
    "outputs": [],
    "stateMutability": "nonpayable",
    "type": "function"
  },
  {
    "inputs": [],
    "name": "loadArrays",
    "outputs": [
      {"name": "fixedValues", "type": "uint512[2]"},
      {"name": "dynamicValues", "type": "uint512[]"}
    ],
    "stateMutability": "view",
    "type": "function"
  }
]`

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	payload := []byte{0xa1, 0xb2, 0xc3}
	fixedValues := [2]*big.Int{big.NewInt(1), big.NewInt(2)}
	dynamicValues := []*big.Int{big.NewInt(3), big.NewInt(4), big.NewInt(5)}
	parsedABI := mustParseABI(t, contractABI)
	bytesOutput := packOutputHex(t, parsedABI, "loadBytes", payload)
	bytesData := packCallHex(t, parsedABI, "storeBytes", payload)
	arraysOutput := packOutputHex(t, parsedABI, "loadArrays", fixedValues, dynamicValues)
	arraysData := packCallHex(t, parsedABI, "storeArrays", fixedValues, dynamicValues)

	script := fmt.Sprintf(`
var bytesData = contract.storeBytes.getData(%q);
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
`, "0x"+common.Bytes2Hex(payload), bytesOutput, arraysOutput)
	var got struct {
		BytesData     string   `json:"bytesData"`
		DecodedBytes  string   `json:"decodedBytes"`
		PureMethod    string   `json:"pureMethod"`
		ArraysData    string   `json:"arraysData"`
		FixedValues   []string `json:"fixedValues"`
		DynamicValues []string `json:"dynamicValues"`
	}
	runWeb3ContractJSON(t, re, web3CallProvider, contractABI, contractAddress, script, &got)
	if got.BytesData != bytesData {
		t.Fatalf("dynamic bytes calldata mismatch:\nhave %s\nwant %s", got.BytesData, bytesData)
	}
	if got.DecodedBytes != "0x"+common.Bytes2Hex(payload) {
		t.Fatalf("decoded dynamic bytes mismatch: have %s, want 0x%x", got.DecodedBytes, payload)
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

	const contractABI = `[
  {
    "inputs": [{"name": "value", "type": "int512"}],
    "name": "storeInt",
    "outputs": [],
    "stateMutability": "nonpayable",
    "type": "function"
  },
  {
    "inputs": [],
    "name": "loadInt",
    "outputs": [{"name": "value", "type": "int512"}],
    "stateMutability": "view",
    "type": "function"
  },
  {
    "inputs": [{"name": "value", "type": "address"}],
    "name": "storeAddress",
    "outputs": [],
    "stateMutability": "nonpayable",
    "type": "function"
  }
]`

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	minimumWord := "8" + strings.Repeat("0", uint512.WordBytes*2-1)
	minimumValue := "-0x" + minimumWord
	negativeOneValue := big.NewInt(-1)
	minimumInt512 := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), uint512.WordBits-1))
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
	parsedABI := mustParseABI(t, contractABI)
	negativeOneData := packCallHex(t, parsedABI, "storeInt", negativeOneValue)
	minimumData := packCallHex(t, parsedABI, "storeInt", minimumInt512)
	negativeOneOutput := packOutputHex(t, parsedABI, "loadInt", negativeOneValue)
	minimumOutput := packOutputHex(t, parsedABI, "loadInt", minimumInt512)
	addressData := packCallHex(t, parsedABI, "storeAddress", address)

	script := fmt.Sprintf(`
var negativeOneData = contract.storeInt.getData(-1);
var minimumData = contract.storeInt.getData(%q);
currentOutput = %q;
var negativeOne = contract.loadInt().toString(16);
currentOutput = %q;
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
`, minimumValue, negativeOneOutput, minimumOutput, checksumAddress, string(invalidChecksum))
	var got struct {
		NegativeOneData        string `json:"negativeOneData"`
		MinimumData            string `json:"minimumData"`
		NegativeOne            string `json:"negativeOne"`
		Minimum                string `json:"minimum"`
		AddressData            string `json:"addressData"`
		RejectsInvalidAddress  bool   `json:"rejectsInvalidAddress"`
		RejectsInvalidChecksum bool   `json:"rejectsInvalidChecksum"`
	}
	runWeb3ContractJSON(t, re, web3CallProvider, contractABI, contractAddress, script, &got)
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
	script := `
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
	runWeb3JSON(t, re, web3CallProvider, script, &got)
	if !got.CurrentPayable || got.CurrentNonpayable || got.LegacyPayable {
		t.Fatalf("constructor stateMutability mismatch: %+v", got)
	}
}
