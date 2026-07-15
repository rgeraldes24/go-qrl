// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package apitypes

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
)

const emptyTypedDataJSON = `{"types":{"QRLTypedDataDomain":[{"name":"name","type":"string"},{"name":"version","type":"string"},{"name":"chainId","type":"uint256"},{"name":"verifyingContract","type":"address"},{"name":"salt","type":"bytes32"}],"Empty":[]},"primaryType":"Empty","domain":{"name":"test","version":"1","chainId":"1","verifyingContract":"Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000","salt":"0x0000000000000000000000000000000000000000000000000000000000000000"},"message":{}}`

func TestIsPrimitive(t *testing.T) {
	t.Parallel()
	for _, typ := range []string{
		"address", "bool", "string", "bytes",
		"int8", "int256", "int512", "uint8", "uint256", "uint512",
		"bytes1", "bytes32", "bytes64",
		"uint512[]", "bytes64[2]", "address[][3]",
	} {
		if !isPrimitiveTypeValid(typ) {
			t.Errorf("expected %q to be a valid primitive type", typ)
		}
	}
	for _, typ := range []string{
		"int", "uint", "int0", "uint0", "int7", "uint513", "uint008",
		"bytes0", "bytes65", "bytes064", "function",
		"uint256[0]", "uint256[01]", "uint256[", "uint256[]x",
	} {
		if isPrimitiveTypeValid(typ) {
			t.Errorf("expected %q to be rejected", typ)
		}
	}
}

func TestTypedDataRejectsReservedCustomType(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"address", "bytes32", "function", "int", "uint512"} {
		types := Types{
			TypedDataDomainType: append([]Type(nil), qrlTypedDataDomain...),
			name:                {{Name: "nested", Type: "bool"}},
		}
		if err := types.validate(); err == nil || !strings.Contains(err.Error(), "reserved") {
			t.Errorf("type name %q: expected reserved-name error, got %v", name, err)
		}
	}
}

func TestTypedDataAcceptsPrimitivePrefixedCustomType(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"bytesEnvelope", "intention", "uintConfig"} {
		types := Types{
			TypedDataDomainType: append([]Type(nil), qrlTypedDataDomain...),
			name:                {{Name: "value", Type: "bool"}},
		}
		if err := types.validate(); err != nil {
			t.Errorf("type name %q: %v", name, err)
		}
	}
}

func TestTypedDataJSONRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"types":{},"primaryType":"Message","domain":{},"message":{},"unexpected":true}`,
		`{"types":{},"primaryType":"Message","domain":{"unexpected":true},"message":{}}`,
	} {
		var typedData TypedData
		if err := json.Unmarshal([]byte(input), &typedData); err == nil {
			t.Fatalf("unknown field accepted in %s", input)
		}
	}
}

func TestTypedDataJSONRejectsDuplicateKeys(t *testing.T) {
	t.Parallel()
	for _, input := range []string{
		`{"types":{},"types":{},"primaryType":"Message","domain":{},"message":{}}`,
		`{"types":{},"primaryType":"Message","domain":{"name":"one","name":"two"},"message":{}}`,
		`{"types":{},"primaryType":"Message","domain":{},"message":{"value":1,"value":2}}`,
	} {
		var typedData TypedData
		if err := json.Unmarshal([]byte(input), &typedData); err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("duplicate key accepted in %s: %v", input, err)
		}
	}
}

func TestTypedDataJSONRequiresExactObjectShape(t *testing.T) {
	t.Parallel()
	var valid TypedData
	if err := json.Unmarshal([]byte(emptyTypedDataJSON), &valid); err != nil {
		t.Fatalf("valid empty struct: %v", err)
	}
	if _, _, err := TypedDataAndHash(valid); err != nil {
		t.Fatalf("hash valid empty struct: %v", err)
	}

	tests := []string{
		strings.Replace(emptyTypedDataJSON, `"types":`, `"Types":`, 1),
		strings.Replace(emptyTypedDataJSON, `{"name":`, `{"Name":`, 1),
		strings.Replace(emptyTypedDataJSON, `"chainId":`, `"ChainId":`, 1),
		strings.Replace(emptyTypedDataJSON, `"Empty":[]`, `"Empty":null`, 1),
		strings.Replace(emptyTypedDataJSON, `"message":{}`, `"message":null`, 1),
		strings.Replace(emptyTypedDataJSON, `,"message":{}`, ``, 1),
	}
	for _, input := range tests {
		var typedData TypedData
		if err := json.Unmarshal([]byte(input), &typedData); err == nil {
			t.Errorf("invalid typed-data shape accepted: %s", input)
		}
	}
}

func TestTypedDataJSONNormalizesNilTypeDeclaration(t *testing.T) {
	t.Parallel()
	var typedData TypedData
	if err := json.Unmarshal([]byte(emptyTypedDataJSON), &typedData); err != nil {
		t.Fatal(err)
	}
	typedData.Types["Empty"] = nil
	encoded, err := json.Marshal(typedData)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"Empty":[]`)) {
		t.Fatalf("nil type declaration was not normalized: %s", encoded)
	}
	var decoded TypedData
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("normalized typed data did not round-trip: %v", err)
	}
}

func TestTypedDataJSONCanonicalForm(t *testing.T) {
	t.Parallel()
	input := strings.Replace(emptyTypedDataJSON, `"chainId":"1"`, `"chainId":"0x1"`, 1)
	var typedData TypedData
	if err := json.Unmarshal([]byte(input), &typedData); err != nil {
		t.Fatal(err)
	}
	const lowerAddress = "Q0000000000000000000000000000000000000000000000000000000000000000dead000000000000000000000000000000000000000000000000000000000000"
	typedData.Domain.VerifyingContract = lowerAddress
	typedData.Domain.Salt = "0xABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABAB"

	encoded, err := json.Marshal(typedData)
	if err != nil {
		t.Fatal(err)
	}
	address, err := common.NewAddressFromString(lowerAddress)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		`"chainId":"1"`,
		`"verifyingContract":"` + address.Hex() + `"`,
		`"salt":"0xabababababababababababababababababababababababababababababababab"`,
	} {
		if !bytes.Contains(encoded, []byte(expected)) {
			t.Errorf("canonical JSON does not contain %s: %s", expected, encoded)
		}
	}
}

func TestTypedDataJSONRejectsNonIntegerNumbers(t *testing.T) {
	t.Parallel()
	for _, value := range []string{"1.0", "1e3"} {
		input := strings.Replace(emptyTypedDataJSON, `"chainId":"1"`, `"chainId":`+value, 1)
		var typedData TypedData
		if err := json.Unmarshal([]byte(input), &typedData); err == nil {
			t.Errorf("non-integer numeric syntax %s accepted", value)
		}
	}
}

func TestTypedDataRejectsInvalidUTF8(t *testing.T) {
	t.Parallel()
	var typedData TypedData
	if err := json.Unmarshal([]byte(emptyTypedDataJSON), &typedData); err != nil {
		t.Fatal(err)
	}
	typedData.Domain.Name = string([]byte{0xff})
	if _, _, err := TypedDataAndHash(typedData); err == nil {
		t.Fatal("programmatic invalid UTF-8 accepted")
	}
	if _, err := json.Marshal(typedData); err == nil {
		t.Fatal("invalid UTF-8 marshaled")
	}

	input := []byte(emptyTypedDataJSON)
	marker := []byte(`"name":"test"`)
	index := bytes.Index(input, marker)
	if index == -1 {
		t.Fatal("domain name marker not found")
	}
	input[index+len(`"name":"`)] = 0xff
	if err := json.Unmarshal(input, new(TypedData)); err == nil {
		t.Fatal("invalid UTF-8 JSON accepted")
	}
}
