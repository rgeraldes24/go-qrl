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
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/crypto/pqcrypto"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/source"
)

const (
	vm64DepositRootExpected       = "0033398ac7d5822aba0b3f614e7728940a9597e122ddd462fe3b5c7c458a3d1a"
	mldsa87VerifyVectorName       = "mldsa87_verify_valid"
	mldsa87VerifyPublicKeyOffset  = common.HashLength
	mldsa87VerifySignatureOffset  = mldsa87VerifyPublicKeyOffset + pqcrypto.MLDSA87PublicKeyLength
	mldsa87VerifyContextLenOffset = mldsa87VerifySignatureOffset +
		pqcrypto.MLDSA87SignatureLength
	mldsa87VerifyContextOffset = mldsa87VerifyContextLenOffset + 1
)

type mldsa87VerifyVector struct {
	Input    string `json:"Input"`
	Expected string `json:"Expected"`
	Name     string `json:"Name"`
}

func loadMLDSA87VerifyVector() ([]byte, []byte, error) {
	searchRoot := strings.TrimSpace(os.Getenv("E2E_REPO_ROOT"))
	if searchRoot == "" {
		var err error
		searchRoot, err = os.Getwd()
		if err != nil {
			return nil, nil, fmt.Errorf("resolve working directory for ML-DSA-87 vector: %w", err)
		}
	}
	repoRoot, err := source.FindRepoRoot(searchRoot)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve repository root for ML-DSA-87 vector: %w", err)
	}
	path := filepath.Join(
		repoRoot,
		"core", "vm", "testdata", "precompiles", "mldsa87_verify.json",
	)
	encoded, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("read checked-in ML-DSA-87 vector %s: %w", path, err)
	}
	return decodeMLDSA87VerifyVector(encoded)
}

func decodeMLDSA87VerifyVector(encoded []byte) ([]byte, []byte, error) {
	var vectors []mldsa87VerifyVector
	if err := json.Unmarshal(encoded, &vectors); err != nil {
		return nil, nil, fmt.Errorf("decode checked-in ML-DSA-87 vectors: %w", err)
	}
	var selected *mldsa87VerifyVector
	for i := range vectors {
		if vectors[i].Name != mldsa87VerifyVectorName {
			continue
		}
		if selected != nil {
			return nil, nil, fmt.Errorf("checked-in ML-DSA-87 vector %q is duplicated", mldsa87VerifyVectorName)
		}
		selected = &vectors[i]
	}
	if selected == nil {
		return nil, nil, fmt.Errorf("checked-in ML-DSA-87 vector %q is missing", mldsa87VerifyVectorName)
	}
	input, err := hex.DecodeString(selected.Input)
	if err != nil {
		return nil, nil, fmt.Errorf("decode checked-in ML-DSA-87 input: %w", err)
	}
	expected, err := hex.DecodeString(selected.Expected)
	if err != nil {
		return nil, nil, fmt.Errorf("decode checked-in ML-DSA-87 expected output: %w", err)
	}
	if err := validateMLDSA87VerifyVector(input, expected); err != nil {
		return nil, nil, err
	}
	return input, expected, nil
}

func validateMLDSA87VerifyVector(input, expected []byte) error {
	if len(input) < mldsa87VerifyContextOffset {
		return fmt.Errorf(
			"checked-in ML-DSA-87 input is %d bytes, want at least %d",
			len(input),
			mldsa87VerifyContextOffset,
		)
	}
	contextLength := int(input[mldsa87VerifyContextLenOffset])
	if got, want := len(input), mldsa87VerifyContextOffset+contextLength; got != want {
		return fmt.Errorf("checked-in ML-DSA-87 input is %d bytes, context frame requires %d", got, want)
	}
	trueWord := common.LeftPadBytes([]byte{1}, vm.WordBytes)
	if !bytes.Equal(expected, trueWord) {
		return fmt.Errorf("checked-in ML-DSA-87 expected output is %x, want VM64 true word %x", expected, trueWord)
	}
	return nil
}

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
	return slices.Concat(
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

	mldsaInput, wantMLDSA, err := loadMLDSA87VerifyVector()
	if err != nil {
		return err
	}
	got, err = call(3, mldsaInput)
	if err != nil {
		return fmt.Errorf("live ML-DSA-87 verify precompile at 0x03: %w", err)
	}
	if !bytes.Equal(got, wantMLDSA) {
		return fmt.Errorf("live ML-DSA-87 verification mismatch: have %x want %x", got, wantMLDSA)
	}

	invalidSignature := bytes.Clone(mldsaInput)
	invalidSignature[mldsa87VerifySignatureOffset] ^= 0x01
	got, err = call(3, invalidSignature)
	if err != nil {
		return fmt.Errorf("live ML-DSA-87 invalid-signature verification: %w", err)
	}
	if len(got) != 0 {
		return fmt.Errorf("live ML-DSA-87 invalid signature returned %x, want empty output", got)
	}

	if len(mldsaInput) == mldsa87VerifyContextOffset {
		return fmt.Errorf("checked-in ML-DSA-87 vector has no context to invalidate")
	}
	invalidContext := bytes.Clone(mldsaInput)
	invalidContext[mldsa87VerifyContextOffset] ^= 0x01
	got, err = call(3, invalidContext)
	if err != nil {
		return fmt.Errorf("live ML-DSA-87 invalid-context verification: %w", err)
	}
	if len(got) != 0 {
		return fmt.Errorf("live ML-DSA-87 invalid context returned %x, want empty output", got)
	}

	modExpInput := slices.Concat(
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
