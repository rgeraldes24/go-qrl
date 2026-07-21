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
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto/pqcrypto"
	"github.com/theQRL/go-qrl/qrlclient"
)

const vm64DepositRootExpected = "0033398ac7d5822aba0b3f614e7728940a9597e122ddd462fe3b5c7c458a3d1a"

func vm64DepositRootInput() []byte {
	const amountLength = 8
	publicKeyOffset := 0
	withdrawalRecipientOffset := publicKeyOffset + pqcrypto.MLDSA87PublicKeyLength
	amountOffset := withdrawalRecipientOffset + common.AddressLength
	signatureOffset := amountOffset + amountLength
	input := make([]byte, signatureOffset+pqcrypto.MLDSA87SignatureLength)

	for i := 0; i < pqcrypto.MLDSA87PublicKeyLength; i++ {
		input[publicKeyOffset+i] = byte(i*17 + 3)
	}
	// Populate both halves. In particular, the nonzero first half makes this
	// vector detect a withdrawal-recipient regression back to 32 bytes.
	for i := 0; i < common.AddressLength/2; i++ {
		input[withdrawalRecipientOffset+i] = byte(0xa0 + i)
		input[withdrawalRecipientOffset+common.AddressLength/2+i] = byte(0x30 + i)
	}
	binary.LittleEndian.PutUint64(input[amountOffset:signatureOffset], 32_000_000_000)
	for i := 0; i < pqcrypto.MLDSA87SignatureLength; i++ {
		input[signatureOffset+i] = byte(i*31 + 7)
	}
	return input
}

func legacyDepositRootInput() []byte {
	const (
		legacyWithdrawalRecipientLength = 32
		legacySignatureLength           = pqcrypto.MLDSA87SignatureLength - 32
		amountLength                    = 8
	)
	valid := vm64DepositRootInput()
	withdrawalRecipientOffset := pqcrypto.MLDSA87PublicKeyLength
	amountOffset := withdrawalRecipientOffset + common.AddressLength
	signatureOffset := amountOffset + amountLength
	return concat(
		valid[:withdrawalRecipientOffset],
		valid[withdrawalRecipientOffset+common.AddressLength-legacyWithdrawalRecipientLength:amountOffset],
		valid[amountOffset:signatureOffset],
		valid[signatureOffset:signatureOffset+legacySignatureLength],
	)
}

func checkLivePrecompiles(ctx context.Context, client *qrlclient.Client, from common.Address) error {
	call := func(address byte, input []byte) ([]byte, error) {
		to := common.BytesToAddress([]byte{address})
		return client.CallContract(ctx, qrl.CallMsg{From: from, To: &to, Data: input}, nil)
	}

	depositInput := vm64DepositRootInput()
	got, err := call(1, depositInput)
	if err != nil {
		return fmt.Errorf("live VM64 deposit-root precompile at 0x01: %w", err)
	}
	wantDepositRoot := common.Hex2Bytes(vm64DepositRootExpected)
	if !bytes.Equal(got, wantDepositRoot) {
		return fmt.Errorf("live VM64 deposit-root precompile mismatch: have %x want %x", got, wantDepositRoot)
	}
	legacyInput := legacyDepositRootInput()
	legacyRoot, err := call(1, legacyInput)
	if err != nil {
		return fmt.Errorf("live legacy-width deposit-root compatibility call: %w", err)
	}
	paddedLegacy := append(append([]byte(nil), legacyInput...), make([]byte, len(depositInput)-len(legacyInput))...)
	paddedLegacyRoot, err := call(1, paddedLegacy)
	if err != nil {
		return fmt.Errorf("live padded legacy-width deposit-root call: %w", err)
	}
	if !bytes.Equal(legacyRoot, paddedLegacyRoot) {
		return fmt.Errorf("live legacy-width compatibility root mismatch: have %x want padded %x", legacyRoot, paddedLegacyRoot)
	}
	extendedRoot, err := call(1, append(append([]byte(nil), depositInput...), 0xff))
	if err != nil {
		return fmt.Errorf("live extended deposit-root compatibility call: %w", err)
	}
	if !bytes.Equal(extendedRoot, got) {
		return fmt.Errorf("live extended deposit-root root mismatch: have %x want canonical %x", extendedRoot, got)
	}

	input := make([]byte, 129)
	for i := range input {
		input[i] = byte((i*17 + 3) & 0xff)
	}
	wantSHA := sha256.Sum256(input)
	got, err = call(2, input)
	if err != nil {
		return fmt.Errorf("live SHA-256 precompile at 0x02: %w", err)
	}
	if !bytes.Equal(got, wantSHA[:]) {
		return fmt.Errorf("live SHA-256 precompile mismatch: have %x want %x", got, wantSHA)
	}

	got, err = call(4, input)
	if err != nil {
		return fmt.Errorf("live identity precompile at 0x04: %w", err)
	}
	if !bytes.Equal(got, input) {
		return fmt.Errorf("live identity precompile mismatch: have %x want %x", got, input)
	}
	legacy, err := call(3, input)
	if err != nil {
		return fmt.Errorf("call inactive legacy precompile address 0x03: %w", err)
	}
	if len(legacy) != 0 {
		return fmt.Errorf("inactive legacy precompile address 0x03 returned %x", legacy)
	}

	modExpInput := concat(
		common.LeftPadBytes([]byte{1}, 32),
		common.LeftPadBytes([]byte{1}, 32),
		common.LeftPadBytes([]byte{1}, 32),
		[]byte{2, 5, 13},
	)
	got, err = call(5, modExpInput)
	if err != nil {
		return fmt.Errorf("live modular-exponentiation precompile at 0x05: %w", err)
	}
	if !bytes.Equal(got, []byte{6}) {
		return fmt.Errorf("live modular-exponentiation precompile mismatch: have %x want 06", got)
	}
	return nil
}
