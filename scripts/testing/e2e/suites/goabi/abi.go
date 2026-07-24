// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package goabi

import (
	"bytes"
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto"
)

const vm64ABI = `[
	{"name":"store","type":"function","inputs":[
		{"name":"amount","type":"uint512"},
		{"name":"tag","type":"bytes4"},
		{"name":"recipient","type":"address"},
		{"name":"payload","type":"bytes"}
	],"outputs":[]},
	{"name":"read","type":"function","inputs":[],"outputs":[
		{"name":"amount","type":"uint512"},
		{"name":"tag","type":"bytes4"},
		{"name":"recipient","type":"address"},
		{"name":"payload","type":"bytes"}
	]},
	{"name":"acceptBytes64","type":"function","inputs":[{"name":"value","type":"bytes64"}],"outputs":[]}
]`

func checkGoABILayout(addr common.Address) error {
	parsed, err := abi.JSON(strings.NewReader(vm64ABI))
	if err != nil {
		return fmt.Errorf("parse VM64 ABI: %w", err)
	}

	tag := [4]byte{1, 2, 3, 4}
	packed, err := parsed.Pack("store", big.NewInt(1337), tag, addr, []byte{0xab, 0xcd})
	if err != nil {
		return fmt.Errorf("pack VM64 calldata: %w", err)
	}
	expected := slices.Concat(
		crypto.Keccak256([]byte("store(uint512,bytes4,address,bytes)"))[:4],
		common.LeftPadBytes(common.FromHex("539"), common.LogTopicLength),
		common.RightPadBytes(common.FromHex("01020304"), common.LogTopicLength),
		addr[:],
		common.LeftPadBytes(common.FromHex("100"), common.LogTopicLength),
		common.LeftPadBytes(common.FromHex("2"), common.LogTopicLength),
		common.RightPadBytes(common.FromHex("abcd"), common.LogTopicLength),
	)
	if !bytes.Equal(packed, expected) {
		return fmt.Errorf("Go ABI calldata mismatch:\nhave %x\nwant %x", packed, expected)
	}

	var b64 [64]byte
	for i := range b64 {
		b64[i] = 0xab
	}
	packed, err = parsed.Pack("acceptBytes64", b64)
	if err != nil {
		return fmt.Errorf("pack bytes64: %w", err)
	}
	expected = append(crypto.Keccak256([]byte("acceptBytes64(bytes64)"))[:4], b64[:]...)
	if !bytes.Equal(packed, expected) {
		return fmt.Errorf("Go ABI bytes64 mismatch:\nhave %x\nwant %x", packed, expected)
	}

	output := slices.Concat(
		common.LeftPadBytes(common.FromHex("539"), common.LogTopicLength),
		common.RightPadBytes(common.FromHex("01020304"), common.LogTopicLength),
		addr[:],
		common.LeftPadBytes(common.FromHex("100"), common.LogTopicLength),
		common.LeftPadBytes(common.FromHex("2"), common.LogTopicLength),
		common.RightPadBytes(common.FromHex("abcd"), common.LogTopicLength),
	)
	values, err := parsed.Unpack("read", output)
	if err != nil {
		return fmt.Errorf("unpack VM64 output: %w", err)
	}
	if len(values) != 4 {
		return fmt.Errorf("unpack returned %d values", len(values))
	}
	if values[0].(*big.Int).Cmp(big.NewInt(1337)) != 0 {
		return fmt.Errorf("decoded amount mismatch: %v", values[0])
	}
	if values[1].([4]byte) != tag {
		return fmt.Errorf("decoded bytes4 mismatch: %x", values[1])
	}
	if values[2].(common.Address) != addr {
		return fmt.Errorf("decoded address mismatch: %s", values[2])
	}
	if !bytes.Equal(values[3].([]byte), []byte{0xab, 0xcd}) {
		return fmt.Errorf("decoded bytes mismatch: %x", values[3])
	}
	return nil
}
