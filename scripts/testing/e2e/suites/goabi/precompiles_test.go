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
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package goabi

import (
	"bytes"
	"encoding/binary"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/crypto/pqcrypto"
)

func TestVM64DepositRootVector(t *testing.T) {
	const (
		vm64InputLength   = 7_291
		legacyInputLength = 7_227
		amountLength      = 8
	)
	input := vm64DepositRootInput()
	if len(input) != vm64InputLength {
		t.Fatalf("VM64 deposit-root input length = %d, want %d", len(input), vm64InputLength)
	}
	if want := pqcrypto.MLDSA87PublicKeyLength + common.AddressLength + amountLength + pqcrypto.MLDSA87SignatureLength; len(input) != want {
		t.Fatalf("VM64 deposit-root input length = %d, component sum = %d", len(input), want)
	}

	recipientOffset := pqcrypto.MLDSA87PublicKeyLength
	recipient := input[recipientOffset : recipientOffset+common.AddressLength]
	if bytes.Equal(recipient[:common.AddressLength/2], make([]byte, common.AddressLength/2)) {
		t.Fatal("withdrawal recipient upper half is zero")
	}
	if got := binary.LittleEndian.Uint64(input[recipientOffset+common.AddressLength:]); got != 32_000_000_000 {
		t.Fatalf("deposit amount = %d, want 32000000000", got)
	}

	precompile := vm.PrecompiledContractsZond[common.BytesToAddress([]byte{1})]
	if precompile == nil {
		t.Fatal("production deposit-root precompile is not registered at 0x01")
	}
	root, _, err := vm.RunPrecompiledContract(precompile, input, precompile.RequiredGas(input))
	if err != nil {
		t.Fatalf("run production deposit-root precompile: %v", err)
	}
	if want := common.Hex2Bytes(vm64DepositRootExpected); !bytes.Equal(root, want) {
		t.Fatalf("deposit root = %x, want %x", root, want)
	}
	canonicalRoot := append([]byte(nil), root...)

	legacy := legacyDepositRootInput()
	if len(legacy) != legacyInputLength {
		t.Fatalf("legacy deposit-root input length = %d, want %d", len(legacy), legacyInputLength)
	}
	root, _, err = vm.RunPrecompiledContract(precompile, legacy, precompile.RequiredGas(legacy))
	if err != nil {
		t.Fatalf("legacy deposit-root compatibility call failed: %v", err)
	}
	paddedLegacy := append(append([]byte(nil), legacy...), make([]byte, vm64InputLength-len(legacy))...)
	paddedRoot, _, err := vm.RunPrecompiledContract(precompile, paddedLegacy, precompile.RequiredGas(paddedLegacy))
	if err != nil {
		t.Fatalf("padded legacy deposit-root call failed: %v", err)
	}
	if !bytes.Equal(root, paddedRoot) {
		t.Fatalf("legacy compatibility root = %x, padded root = %x", root, paddedRoot)
	}
	extended := append(append([]byte(nil), input...), 0xff)
	extendedRoot, _, err := vm.RunPrecompiledContract(precompile, extended, precompile.RequiredGas(extended))
	if err != nil {
		t.Fatalf("extended deposit-root compatibility call failed: %v", err)
	}
	if !bytes.Equal(extendedRoot, canonicalRoot) {
		t.Fatalf("extended deposit-root root = %x, canonical root = %x", extendedRoot, canonicalRoot)
	}
}
