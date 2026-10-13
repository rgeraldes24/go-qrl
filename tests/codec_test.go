package tests

import (
	"encoding/json"
	"testing"
)

// The block and state test formats encode numbers as hex strings, which the
// generated codecs (gen_btheader.go, gen_stenv.go, gen_sttransaction.go) decode.
func TestHexEncodedTestFormats(t *testing.T) {
	var header btHeader
	if err := json.Unmarshal([]byte(`{"Number": "0x01", "GasLimit": "0x5208", "GasUsed": "0x0", "Timestamp": "0x03e8", "ExtraData": "0x0102", "BaseFeePerGas": "0x0a"}`), &header); err != nil {
		t.Fatalf("btHeader: %v", err)
	}
	if header.Number.Uint64() != 1 || header.GasLimit != 21000 || header.Timestamp != 1000 || len(header.ExtraData) != 2 || header.BaseFeePerGas.Uint64() != 10 {
		t.Fatalf("btHeader decoded wrong: %+v", header)
	}

	var env stEnv
	if err := json.Unmarshal([]byte(`{"currentCoinbase": "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000", "currentGasLimit": "0x5208", "currentNumber": "0x01", "currentTimestamp": "0x03e8", "currentBaseFee": "0x0a"}`), &env); err != nil {
		t.Fatalf("stEnv: %v", err)
	}
	if env.GasLimit != 21000 || env.Number != 1 || env.Timestamp != 1000 || env.BaseFee.Uint64() != 10 {
		t.Fatalf("stEnv decoded wrong: %+v", env)
	}

	var tx stTransaction
	if err := json.Unmarshal([]byte(`{"nonce": "0x02", "maxFeePerGas": "0x0a", "maxPriorityFeePerGas": "0x01", "gasLimit": ["0x5208"], "to": "", "value": ["0x01"], "data": ["0x"], "seed": "0x01"}`), &tx); err != nil {
		t.Fatalf("stTransaction: %v", err)
	}
	if tx.Nonce != 2 || tx.MaxFeePerGas.Uint64() != 10 || tx.GasLimit[0] != 21000 || tx.Seed != "0x01" {
		t.Fatalf("stTransaction decoded wrong: %+v", tx)
	}
}
