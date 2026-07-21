// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package runtime

import (
	"bytes"
	"testing"

	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto"
	pqwallet "github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	walletcommon "github.com/theQRL/go-qrllib/wallet/common"
)

func TestMLDSA87VerifyPrecompileStaticCall(t *testing.T) {
	wallet, err := pqwallet.Generate(pqwallet.ML_DSA_87)
	if err != nil {
		t.Fatal(err)
	}
	digest := crypto.Keccak256([]byte("QRL ML-DSA-87 runtime test"))
	signature, err := pqcrypto.Sign(digest, wallet)
	if err != nil {
		t.Fatal(err)
	}
	var input []byte
	input = append(input, digest...)
	input = append(input, wallet.GetPK()...)
	input = append(input, signature...)
	context := walletcommon.SigningContext(wallet.GetDescriptor())
	input = append(input, byte(len(context)))
	input = append(input, context...)

	code := []byte{
		byte(vm.CALLDATASIZE),
		byte(vm.PUSH1), 0,
		byte(vm.PUSH1), 0,
		byte(vm.CALLDATACOPY),
		byte(vm.PUSH1), byte(vm.WordBytes),
		byte(vm.PUSH1), 0,
		byte(vm.CALLDATASIZE),
		byte(vm.PUSH1), 0,
		byte(vm.PUSH1), 3,
		byte(vm.GAS),
		byte(vm.STATICCALL),
		byte(vm.POP),
		byte(vm.PUSH1), byte(vm.WordBytes),
		byte(vm.PUSH1), 0,
		byte(vm.RETURN),
	}
	output, _, err := Execute(code, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := make([]byte, vm.WordBytes)
	want[vm.WordBytes-1] = 1
	if !bytes.Equal(output, want) {
		t.Fatalf("verification output %x, want %x", output, want)
	}
}
