// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package goabi

import (
	"math/big"
	"strings"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto"
)

func normalizeHex(input string) string {
	return strings.TrimPrefix(strings.ToLower(input), "0x")
}

func methodID(signature string) []byte {
	return crypto.Keccak256([]byte(signature))[:4]
}

func concat(parts ...[]byte) []byte {
	var size int
	for _, part := range parts {
		size += len(part)
	}
	out := make([]byte, 0, size)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func hashTopic(hash common.Hash) common.LogTopic {
	var topic common.LogTopic
	copy(topic[:common.HashLength], hash[:])
	return topic
}

func bytesTopic(input []byte) common.LogTopic {
	var topic common.LogTopic
	copy(topic[:], input)
	return topic
}

func unsignedWord(value *big.Int) []byte {
	return common.LeftPadBytes(value.Bytes(), common.LogTopicLength)
}

func signedWord(value *big.Int) []byte {
	encoded := new(big.Int).Set(value)
	if encoded.Sign() < 0 {
		encoded.Add(encoded, new(big.Int).Lsh(big.NewInt(1), common.LogTopicLength*8))
	}
	return unsignedWord(encoded)
}

func word(hex string) []byte {
	raw := common.FromHex(hex)
	out := make([]byte, common.LogTopicLength)
	copy(out[len(out)-len(raw):], raw)
	return out
}

func fixedBytes(hex string) []byte {
	raw := common.FromHex(hex)
	out := make([]byte, common.LogTopicLength)
	copy(out, raw)
	return out
}
