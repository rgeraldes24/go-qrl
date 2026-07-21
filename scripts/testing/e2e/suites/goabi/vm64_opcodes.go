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
	"fmt"
	"math/big"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

const (
	vm64ContextBalance   = uint64(1111)
	vm64CollisionBalance = uint64(2222)
	vm64ChildMarker      = byte(0xc1)
	vm64RollbackMarker   = byte(0xd2)
	vm64CatcherMarker    = byte(0xca)
)

var vm64Create2Salt = func() [vm.WordBytes]byte {
	var salt [vm.WordBytes]byte
	salt[0] = 0xa5
	salt[31] = 0x5a
	salt[vm.WordBytes-1] = 0x07
	return salt
}()

// bytecodeAssembly is intentionally small. It emits only the VM64 bytecode
// needed by this live probe, while using the production opcode constants so a
// stale legacy PUSH20/PUSH32 assumption cannot hide in a compiler fixture.
type bytecodeAssembly struct {
	code        []byte
	labels      map[string]int
	relocations []bytecodeRelocation
}

type bytecodeRelocation struct {
	label string
	pos   int
}

func newBytecodeAssembly() *bytecodeAssembly {
	return &bytecodeAssembly{labels: make(map[string]int)}
}

func (a *bytecodeAssembly) op(op vm.OpCode) {
	a.code = append(a.code, byte(op))
}

func (a *bytecodeAssembly) raw(values ...byte) {
	a.code = append(a.code, values...)
}

func (a *bytecodeAssembly) pushBytes(value []byte) {
	if len(value) == 0 || len(value) > vm.WordBytes {
		panic(fmt.Sprintf("VM64 push width %d is outside 1..%d", len(value), vm.WordBytes))
	}
	a.op(vm.OpCode(int(vm.PUSH1) + len(value) - 1))
	a.code = append(a.code, value...)
}

func (a *bytecodeAssembly) pushUint(value uint64) {
	if value == 0 {
		a.pushBytes([]byte{0})
		return
	}
	a.pushBytes(new(big.Int).SetUint64(value).Bytes())
}

func (a *bytecodeAssembly) pushAddress(address common.Address) {
	a.pushBytes(address[:])
}

func (a *bytecodeAssembly) pushWord(word [vm.WordBytes]byte) {
	a.pushBytes(word[:])
}

func (a *bytecodeAssembly) markJump(label string) {
	a.labels[label] = len(a.code)
	a.op(vm.JUMPDEST)
}

func (a *bytecodeAssembly) markData(label string) {
	a.labels[label] = len(a.code)
}

func (a *bytecodeAssembly) pushLabel(label string) {
	a.op(vm.PUSH2)
	pos := len(a.code)
	a.raw(0, 0)
	a.relocations = append(a.relocations, bytecodeRelocation{label: label, pos: pos})
}

func (a *bytecodeAssembly) jump(label string, conditional bool) {
	a.pushLabel(label)
	if conditional {
		a.op(vm.JUMPI)
	} else {
		a.op(vm.JUMP)
	}
}

func (a *bytecodeAssembly) bytes() []byte {
	out := bytes.Clone(a.code)
	for _, relocation := range a.relocations {
		offset, ok := a.labels[relocation.label]
		if !ok || offset > 0xffff {
			panic(fmt.Sprintf("VM64 bytecode label %q is missing or too large", relocation.label))
		}
		out[relocation.pos] = byte(offset >> 8)
		out[relocation.pos+1] = byte(offset)
	}
	return out
}

func deploymentBytecode(runtime []byte) []byte {
	// The prefix length can itself change the width of its CODECOPY offset.
	// Iterate to a fixed point rather than baking in a one-byte legacy offset.
	offset := uint64(0)
	for {
		prefix := newBytecodeAssembly()
		prefix.pushUint(uint64(len(runtime)))
		prefix.pushUint(offset)
		prefix.pushUint(0)
		prefix.op(vm.CODECOPY)
		prefix.pushUint(uint64(len(runtime)))
		prefix.pushUint(0)
		prefix.op(vm.RETURN)
		if uint64(len(prefix.code)) == offset {
			return append(prefix.code, runtime...)
		}
		offset = uint64(len(prefix.code))
	}
}

func contextRuntime() []byte {
	a := newBytecodeAssembly()
	for index, op := range []vm.OpCode{vm.ADDRESS, vm.ORIGIN, vm.CALLER, vm.COINBASE, vm.SELFBALANCE} {
		a.op(op)
		a.pushUint(uint64(index * vm.WordBytes))
		a.op(vm.MSTORE)
	}
	a.pushUint(5 * vm.WordBytes)
	a.pushUint(0)
	a.op(vm.RETURN)
	return a.bytes()
}

type modeBranch struct {
	mode  byte
	label string
}

func appendModeDispatch(a *bytecodeAssembly, branches []modeBranch, fallback string) {
	a.pushUint(0)
	a.op(vm.CALLDATALOAD)
	for _, branch := range branches {
		a.raw(byte(vm.DUP1))
		a.pushUint(uint64(branch.mode))
		a.op(vm.EQ)
		a.jump(branch.label, true)
	}
	a.op(vm.POP)
	a.jump(fallback, false)
}

func appendContextCall(a *bytecodeAssembly, opcode vm.OpCode, target common.Address) {
	a.pushUint(5 * vm.WordBytes) // output size
	a.pushUint(0)                // output offset
	a.pushUint(0)                // input size
	a.pushUint(0)                // input offset
	if opcode == vm.CALL {
		a.pushUint(0) // value
	}
	a.pushAddress(target)
	a.op(vm.GAS)
	a.op(opcode)
	a.pushUint(5 * vm.WordBytes)
	a.op(vm.MSTORE) // append the full-word success flag
	a.pushUint(6 * vm.WordBytes)
	a.pushUint(0)
	a.op(vm.RETURN)
}

func callRouterRuntime(target common.Address) []byte {
	a := newBytecodeAssembly()
	appendModeDispatch(a, []modeBranch{{mode: 1, label: "delegate"}, {mode: 2, label: "static"}}, "call")
	a.markJump("call")
	appendContextCall(a, vm.CALL, target)
	a.markJump("delegate")
	a.op(vm.POP)
	appendContextCall(a, vm.DELEGATECALL, target)
	a.markJump("static")
	a.op(vm.POP)
	appendContextCall(a, vm.STATICCALL, target)
	return a.bytes()
}

func introspectionRuntime(target, collision common.Address) []byte {
	a := newBytecodeAssembly()
	for index, fixture := range []struct {
		op      vm.OpCode
		address common.Address
	}{
		{vm.BALANCE, target}, {vm.BALANCE, collision},
		{vm.EXTCODESIZE, target}, {vm.EXTCODESIZE, collision},
		{vm.EXTCODEHASH, target}, {vm.EXTCODEHASH, collision},
	} {
		a.pushAddress(fixture.address)
		a.op(fixture.op)
		a.pushUint(uint64(index * vm.WordBytes))
		a.op(vm.MSTORE)
	}
	for index, address := range []common.Address{target, collision} {
		a.pushUint(vm.WordBytes)
		a.pushUint(0)
		a.pushUint(uint64((6 + index) * vm.WordBytes))
		a.pushAddress(address)
		a.op(vm.EXTCODECOPY)
	}
	a.pushUint(8 * vm.WordBytes)
	a.pushUint(0)
	a.op(vm.RETURN)
	return a.bytes()
}

func appendGasAccessProbe(a *bytecodeAssembly, first, second common.Address) {
	a.op(vm.GAS)
	a.pushAddress(first)
	a.op(vm.BALANCE)
	a.op(vm.POP)
	a.op(vm.GAS)
	a.pushAddress(second)
	a.op(vm.BALANCE)
	a.op(vm.POP)
	a.op(vm.GAS)
	for index := 2; index >= 0; index-- {
		a.pushUint(uint64(index * vm.WordBytes))
		a.op(vm.MSTORE)
	}
	a.pushUint(3 * vm.WordBytes)
	a.pushUint(0)
	a.op(vm.RETURN)
}

func warmthRuntime(target, collision common.Address) []byte {
	a := newBytecodeAssembly()
	appendModeDispatch(a, []modeBranch{{mode: 1, label: "distinct"}}, "same")
	a.markJump("same")
	appendGasAccessProbe(a, target, target)
	a.markJump("distinct")
	a.op(vm.POP)
	appendGasAccessProbe(a, target, collision)
	return a.bytes()
}

func markerRuntime(marker byte) []byte {
	a := newBytecodeAssembly()
	a.pushUint(uint64(marker))
	a.pushUint(0)
	a.op(vm.MSTORE)
	a.pushUint(vm.WordBytes)
	a.pushUint(0)
	a.op(vm.RETURN)
	return a.bytes()
}

func appendInternalCreate(a *bytecodeAssembly, opcode vm.OpCode, slot uint64, initSize int) {
	a.pushUint(uint64(initSize))
	a.pushLabel("child-init")
	a.pushUint(0)
	a.op(vm.CODECOPY)
	if opcode == vm.CREATE2 {
		a.pushWord(vm64Create2Salt)
	}
	a.pushUint(uint64(initSize))
	a.pushUint(0)
	a.pushUint(0)
	a.op(opcode)
	a.raw(byte(vm.DUP1))
	a.pushUint(slot)
	a.op(vm.SSTORE)
	a.pushUint(0)
	a.op(vm.MSTORE)
	a.pushUint(vm.WordBytes)
	a.pushUint(0)
	a.op(vm.LOG0)
	a.pushUint(vm.WordBytes)
	a.pushUint(0)
	a.op(vm.RETURN)
}

func creatorRuntime() ([]byte, []byte) {
	childRuntime := markerRuntime(vm64ChildMarker)
	childInit := deploymentBytecode(childRuntime)
	a := newBytecodeAssembly()
	appendModeDispatch(a, []modeBranch{{mode: 1, label: "create2"}}, "create")
	a.markJump("create")
	appendInternalCreate(a, vm.CREATE, 0, len(childInit))
	a.markJump("create2")
	a.op(vm.POP)
	appendInternalCreate(a, vm.CREATE2, 1, len(childInit))
	a.markData("child-init")
	a.code = append(a.code, childInit...)
	return a.bytes(), childRuntime
}

func reverterRuntime() []byte {
	a := newBytecodeAssembly()
	a.pushUint(uint64(vm64RollbackMarker))
	a.pushUint(0)
	a.op(vm.SSTORE)
	a.pushUint(uint64(vm64RollbackMarker))
	a.pushUint(0)
	a.op(vm.MSTORE)
	var topic [vm.WordBytes]byte
	topic[0] = 0x8f
	topic[vm.WordBytes-1] = vm64RollbackMarker
	a.pushWord(topic)
	a.pushUint(vm.WordBytes)
	a.pushUint(0)
	a.op(vm.LOG1)
	a.pushUint(vm.WordBytes)
	a.pushUint(0)
	a.op(vm.REVERT)
	return a.bytes()
}

func catcherRuntime(reverter common.Address) []byte {
	a := newBytecodeAssembly()
	a.pushUint(vm.WordBytes)
	a.pushUint(0)
	a.pushUint(0)
	a.pushUint(0)
	a.pushUint(0)
	a.pushAddress(reverter)
	a.op(vm.GAS)
	a.op(vm.CALL)
	a.raw(byte(vm.DUP1))
	a.pushUint(vm.WordBytes)
	a.op(vm.MSTORE)
	a.op(vm.POP)
	a.pushUint(uint64(vm64CatcherMarker))
	a.pushUint(0)
	a.op(vm.SSTORE)
	a.pushUint(2 * vm.WordBytes)
	a.pushUint(0)
	a.op(vm.LOG0)
	a.pushUint(2 * vm.WordBytes)
	a.pushUint(0)
	a.op(vm.RETURN)
	return a.bytes()
}

func modeWord(mode byte) []byte {
	word := make([]byte, vm.WordBytes)
	word[len(word)-1] = mode
	return word
}

func decodeVM64Words(output []byte, count int) ([][]byte, error) {
	if len(output) != count*vm.WordBytes {
		return nil, fmt.Errorf("VM64 output is %d bytes, want %d", len(output), count*vm.WordBytes)
	}
	words := make([][]byte, count)
	for index := range count {
		words[index] = bytes.Clone(output[index*vm.WordBytes : (index+1)*vm.WordBytes])
	}
	return words, nil
}

func wordUint64(word []byte) (uint64, error) {
	if len(word) != vm.WordBytes {
		return 0, fmt.Errorf("VM64 integer word is %d bytes", len(word))
	}
	value := new(big.Int).SetBytes(word)
	if !value.IsUint64() {
		return 0, fmt.Errorf("VM64 integer %x exceeds uint64", word)
	}
	return value.Uint64(), nil
}

func wordAddress(word []byte) (common.Address, error) {
	if len(word) != common.AddressLength {
		return common.Address{}, fmt.Errorf("VM64 address word is %d bytes", len(word))
	}
	return common.BytesToAddress(word), nil
}

func collisionAddress(address common.Address) common.Address {
	collision := address
	collision[0] ^= 0x80
	return collision
}

func deployVM64Contract(ctx context.Context, run *suiteRun, label string, client *qrlclient.Client, w wallet.Wallet, from common.Address, runtime []byte) (common.Address, *types.Receipt, error) {
	receipt, err := deployRaw(ctx, run, label, client, w, from, deploymentBytecode(runtime))
	if err != nil {
		return common.Address{}, nil, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful || receipt.ContractAddress == (common.Address{}) {
		return common.Address{}, nil, fmt.Errorf("%s receipt status=%d contract=%s", label, receipt.Status, receipt.ContractAddress)
	}
	code, err := client.CodeAt(ctx, receipt.ContractAddress, receipt.BlockNumber)
	if err != nil {
		return common.Address{}, nil, fmt.Errorf("read %s runtime: %w", label, err)
	}
	if !bytes.Equal(code, runtime) {
		return common.Address{}, nil, fmt.Errorf("%s runtime mismatch: have %x want %x", label, code, runtime)
	}
	return receipt.ContractAddress, receipt, nil
}

func sendVM64Call(ctx context.Context, run *suiteRun, label string, client *qrlclient.Client, w wallet.Wallet, from, to common.Address, data []byte) (*types.Receipt, error) {
	expected := newTransactionSemantics(&to, new(big.Int), data)
	if recorded, ok, err := run.ensurePreparedSubmitted(ctx, label, client, expected); err != nil {
		return nil, err
	} else if ok {
		return run.waitRecordedReceipt(ctx, client, recorded)
	}
	tx, err := signDynamicFeeTx(ctx, client, w, from, &to, new(big.Int), data)
	if err != nil {
		return nil, err
	}
	return run.submitPreparedAndWait(ctx, label, client, tx, expected)
}

func signVM64ExplicitGasCall(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from, to common.Address, gas uint64) (*types.Transaction, error) {
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("VM64 failure chain id: %w", err)
	}
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("VM64 failure nonce: %w", err)
	}
	gasFeeCap, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("VM64 failure gas price: %w", err)
	}
	gasTipCap, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("VM64 failure gas tip: %w", err)
	}
	gasFeeCap = new(big.Int).Mul(gasFeeCap, big.NewInt(4))
	if gasFeeCap.Cmp(gasTipCap) < 0 {
		gasFeeCap = new(big.Int).Set(gasTipCap)
	}
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: gasTipCap, GasFeeCap: gasFeeCap,
		Gas: gas, To: &to, Value: new(big.Int),
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), w)
	if err != nil {
		return nil, fmt.Errorf("sign VM64 failure transaction: %w", err)
	}
	return signed, nil
}

func sendVM64FailingCall(ctx context.Context, run *suiteRun, client *qrlclient.Client, w wallet.Wallet, from, to common.Address) (*types.Receipt, error) {
	expected := newTransactionSemantics(&to, new(big.Int), nil)
	if recorded, ok, err := run.ensurePreparedSubmitted(ctx, TransactionVM64TopLevelRevert, client, expected); err != nil {
		return nil, err
	} else if ok {
		return run.waitRecordedReceipt(ctx, client, recorded)
	}
	tx, err := signVM64ExplicitGasCall(ctx, client, w, from, to, 800_000)
	if err != nil {
		return nil, err
	}
	return run.submitPreparedAndWait(ctx, TransactionVM64TopLevelRevert, client, tx, expected)
}

func callAt(ctx context.Context, client *qrlclient.Client, from, to common.Address, data []byte, block *big.Int) ([]byte, error) {
	return client.CallContract(ctx, qrl.CallMsg{From: from, To: &to, Data: data}, block)
}

func verifyContextWords(words [][]byte, address, origin, caller, coinbase common.Address, balance uint64) error {
	wantAddresses := []common.Address{address, origin, caller, coinbase}
	for index, want := range wantAddresses {
		got, err := wordAddress(words[index])
		if err != nil || got != want {
			return fmt.Errorf("context word %d address=%s error=%v, want %s", index, got, err, want)
		}
	}
	gotBalance, err := wordUint64(words[4])
	if err != nil || gotBalance != balance {
		return fmt.Errorf("SELFBALANCE=%d error=%v, want %d", gotBalance, err, balance)
	}
	return nil
}

func verifyLiveContextAndCalls(ctx context.Context, client *qrlclient.Client, from, contextAddress, router common.Address) error {
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return fmt.Errorf("VM64 context header: %w", err)
	}
	direct, err := callAt(ctx, client, from, contextAddress, nil, header.Number)
	if err != nil {
		return fmt.Errorf("direct VM64 context call: %w", err)
	}
	words, err := decodeVM64Words(direct, 5)
	if err != nil {
		return err
	}
	if err := verifyContextWords(words, contextAddress, from, from, header.Coinbase, vm64ContextBalance); err != nil {
		return fmt.Errorf("direct ADDRESS/ORIGIN/CALLER/COINBASE/SELFBALANCE: %w", err)
	}

	probes := []struct {
		name            string
		mode            byte
		address, caller common.Address
		balance         uint64
	}{
		{name: "CALL", mode: 0, address: contextAddress, caller: router, balance: vm64ContextBalance},
		{name: "DELEGATECALL", mode: 1, address: router, caller: from, balance: 0},
		{name: "STATICCALL", mode: 2, address: contextAddress, caller: router, balance: vm64ContextBalance},
	}
	for _, probe := range probes {
		output, err := callAt(ctx, client, from, router, modeWord(probe.mode), header.Number)
		if err != nil {
			return fmt.Errorf("VM64 %s probe: %w", probe.name, err)
		}
		words, err := decodeVM64Words(output, 6)
		if err != nil {
			return fmt.Errorf("VM64 %s output: %w", probe.name, err)
		}
		if err := verifyContextWords(words, probe.address, from, probe.caller, header.Coinbase, probe.balance); err != nil {
			return fmt.Errorf("VM64 %s context/return data: %w", probe.name, err)
		}
		success, err := wordUint64(words[5])
		if err != nil || success != 1 {
			return fmt.Errorf("VM64 %s success=%d error=%v, want 1", probe.name, success, err)
		}
	}
	return nil
}

func verifyLiveIntrospection(ctx context.Context, client *qrlclient.Client, from, probe, target, collision common.Address, targetRuntime []byte) error {
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return err
	}
	output, err := callAt(ctx, client, from, probe, nil, header.Number)
	if err != nil {
		return fmt.Errorf("VM64 BALANCE/EXTCODE call: %w", err)
	}
	words, err := decodeVM64Words(output, 8)
	if err != nil {
		return err
	}
	for index, want := range []uint64{vm64ContextBalance, vm64CollisionBalance, uint64(len(targetRuntime)), 0} {
		got, err := wordUint64(words[index])
		if err != nil || got != want {
			return fmt.Errorf("VM64 account opcode word %d=%d error=%v, want %d", index, got, err, want)
		}
	}
	wantTargetHash := common.LeftPadBytes(crypto.Keccak256(targetRuntime), vm.WordBytes)
	wantCollisionHash := common.LeftPadBytes(crypto.Keccak256(nil), vm.WordBytes)
	if !bytes.Equal(words[4], wantTargetHash) || !bytes.Equal(words[5], wantCollisionHash) {
		return fmt.Errorf("VM64 EXTCODEHASH lost upper-half address identity: target=%x collision=%x", words[4], words[5])
	}
	wantTargetCopy := make([]byte, vm.WordBytes)
	copy(wantTargetCopy, targetRuntime)
	if !bytes.Equal(words[6], wantTargetCopy) || !bytes.Equal(words[7], make([]byte, vm.WordBytes)) {
		return fmt.Errorf("VM64 EXTCODECOPY lost upper-half address identity: target=%x collision=%x", words[6], words[7])
	}
	if bytes.Equal(target[:common.AddressLength/2], collision[:common.AddressLength/2]) ||
		!bytes.Equal(target[common.AddressLength/2:], collision[common.AddressLength/2:]) {
		return fmt.Errorf("VM64 introspection fixtures do not differ only in their upper half")
	}
	return nil
}

func gasAccessCosts(output []byte) (uint64, uint64, error) {
	words, err := decodeVM64Words(output, 3)
	if err != nil {
		return 0, 0, err
	}
	gas := make([]uint64, 3)
	for index := range gas {
		gas[index], err = wordUint64(words[index])
		if err != nil {
			return 0, 0, err
		}
	}
	if gas[0] <= gas[1] || gas[1] <= gas[2] {
		return 0, 0, fmt.Errorf("non-decreasing GAS samples %v", gas)
	}
	return gas[0] - gas[1], gas[1] - gas[2], nil
}

func verifyLiveWarmth(ctx context.Context, client *qrlclient.Client, from, probe common.Address) error {
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return err
	}
	same, err := callAt(ctx, client, from, probe, modeWord(0), header.Number)
	if err != nil {
		return fmt.Errorf("same-address warmth probe: %w", err)
	}
	distinct, err := callAt(ctx, client, from, probe, modeWord(1), header.Number)
	if err != nil {
		return fmt.Errorf("upper-half-distinct warmth probe: %w", err)
	}
	firstSame, secondSame, err := gasAccessCosts(same)
	if err != nil {
		return fmt.Errorf("same-address warmth costs: %w", err)
	}
	firstDistinct, secondDistinct, err := gasAccessCosts(distinct)
	if err != nil {
		return fmt.Errorf("upper-half-distinct warmth costs: %w", err)
	}
	// Under the active Berlin-or-later rules, the second access to the exact
	// same 64-byte address is warm while the equal-low-half sibling is cold.
	// A 1,000-gas margin avoids coupling this probe to one exact fork constant.
	if firstSame <= secondSame+1000 || secondDistinct <= secondSame+1000 || firstDistinct <= secondSame+1000 {
		return fmt.Errorf("full-address warm/cold distinction not observed: same=%d/%d distinct=%d/%d", firstSame, secondSame, firstDistinct, secondDistinct)
	}
	return nil
}

func verifyCreatorMutation(ctx context.Context, run *suiteRun, client *qrlclient.Client, w wallet.Wallet, from, creator common.Address, label string, mode byte, slot common.Hash, childInit, childRuntime []byte) (common.Address, error) {
	// A fresh execution also checks qrl_call's full-width return value. On a
	// resumed execution, do not simulate again: CREATE would use the next
	// creator nonce and CREATE2 would encounter the already-created address.
	// The mined LOG0 below is emitted directly from the opcode return word and
	// remains durable evidence for both paths.
	var simulated common.Address
	_, submitted := run.transactionForLabel(label)
	_, prepared := run.prepared[label]
	if !submitted && !prepared {
		header, err := client.HeaderByNumber(ctx, nil)
		if err != nil {
			return common.Address{}, err
		}
		predictedRaw, err := callAt(ctx, client, from, creator, modeWord(mode), header.Number)
		if err != nil {
			return common.Address{}, fmt.Errorf("simulate %s return address: %w", label, err)
		}
		simulated, err = wordAddress(predictedRaw)
		if err != nil || simulated == (common.Address{}) {
			return common.Address{}, fmt.Errorf("%s simulated address=%s error=%v", label, simulated, err)
		}
	}
	receipt, err := sendVM64Call(ctx, run, label, client, w, from, creator, modeWord(mode))
	if err != nil {
		return common.Address{}, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful || receipt.BlockNumber == nil || receipt.BlockNumber.Sign() == 0 || len(receipt.Logs) != 1 || receipt.Logs[0].Address != creator || len(receipt.Logs[0].Topics) != 0 {
		return common.Address{}, fmt.Errorf("%s receipt did not preserve the returned 64-byte address: status=%d logs=%+v", label, receipt.Status, receipt.Logs)
	}
	created, err := wordAddress(receipt.Logs[0].Data)
	if err != nil || created == (common.Address{}) {
		return common.Address{}, fmt.Errorf("%s logged address=%x error=%v", label, receipt.Logs[0].Data, err)
	}
	preBlock := new(big.Int).Sub(new(big.Int).Set(receipt.BlockNumber), big.NewInt(1))
	var expected common.Address
	if mode == 0 {
		creatorNonce, err := client.NonceAt(ctx, creator, preBlock)
		if err != nil {
			return common.Address{}, fmt.Errorf("creator pre-state nonce: %w", err)
		}
		expected = crypto.CreateAddress(creator, creatorNonce)
	} else {
		expected = crypto.CreateAddress2(creator, vm64Create2Salt, crypto.Keccak256(childInit))
		lowHalfSalt := vm64Create2Salt
		clear(lowHalfSalt[:vm.WordBytes/2])
		if expected == crypto.CreateAddress2(creator, lowHalfSalt, crypto.Keccak256(childInit)) {
			return common.Address{}, fmt.Errorf("CREATE2 high-half salt did not affect the expected address")
		}
	}
	if created != expected || (simulated != (common.Address{}) && simulated != created) || bytes.Equal(created[:common.AddressLength/2], make([]byte, common.AddressLength/2)) {
		return common.Address{}, fmt.Errorf("%s full returned address=%s simulated=%s want=%s", label, created, simulated, expected)
	}
	stored, err := client.StorageAt(ctx, creator, slot, receipt.BlockNumber)
	if err != nil {
		return common.Address{}, fmt.Errorf("%s stored address: %w", label, err)
	}
	if !bytes.Equal(common.LeftPadBytes(stored, vm.WordBytes), created[:]) {
		return common.Address{}, fmt.Errorf("%s stored address=%x, want %x", label, stored, created)
	}
	code, err := client.CodeAt(ctx, created, receipt.BlockNumber)
	if err != nil || !bytes.Equal(code, childRuntime) {
		return common.Address{}, fmt.Errorf("%s created runtime=%x error=%v, want %x", label, code, err, childRuntime)
	}
	childOutput, err := callAt(ctx, client, from, created, nil, receipt.BlockNumber)
	if err != nil {
		return common.Address{}, fmt.Errorf("call full-width %s child %s: %w", label, created, err)
	}
	marker, err := wordUint64(childOutput)
	if err != nil || marker != uint64(vm64ChildMarker) {
		return common.Address{}, fmt.Errorf("%s child marker=%d error=%v", label, marker, err)
	}
	return created, nil
}

func zeroStorage(value []byte) bool {
	for _, b := range value {
		if b != 0 {
			return false
		}
	}
	return true
}

func verifyCaughtRollback(ctx context.Context, run *suiteRun, client *qrlclient.Client, w wallet.Wallet, from, reverter, catcher common.Address) error {
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return err
	}
	output, err := callAt(ctx, client, from, catcher, nil, header.Number)
	if err != nil {
		return fmt.Errorf("simulate caught inner revert: %w", err)
	}
	words, err := decodeVM64Words(output, 2)
	if err != nil {
		return err
	}
	revertMarker, err := wordUint64(words[0])
	if err != nil || revertMarker != uint64(vm64RollbackMarker) {
		return fmt.Errorf("caught revert data marker=%d error=%v", revertMarker, err)
	}
	success, err := wordUint64(words[1])
	if err != nil || success != 0 {
		return fmt.Errorf("caught inner CALL success=%d error=%v, want 0", success, err)
	}
	receipt, err := sendVM64Call(ctx, run, TransactionVM64CaughtRevert, client, w, from, catcher, nil)
	if err != nil {
		return err
	}
	if receipt.Status != types.ReceiptStatusSuccessful || len(receipt.Logs) != 1 || receipt.Logs[0].Address != catcher || len(receipt.Logs[0].Topics) != 0 || !bytes.Equal(receipt.Logs[0].Data, output) {
		return fmt.Errorf("caught rollback receipt status=%d logs=%+v", receipt.Status, receipt.Logs)
	}
	reverterStorage, err := client.StorageAt(ctx, reverter, common.Hash{}, receipt.BlockNumber)
	if err != nil || !zeroStorage(reverterStorage) {
		return fmt.Errorf("inner REVERT leaked storage=%x error=%v", reverterStorage, err)
	}
	catcherStorage, err := client.StorageAt(ctx, catcher, common.Hash{}, receipt.BlockNumber)
	if err != nil {
		return err
	}
	marker, err := wordUint64(common.LeftPadBytes(catcherStorage, vm.WordBytes))
	if err != nil || marker != uint64(vm64CatcherMarker) {
		return fmt.Errorf("outer catcher storage marker=%d error=%v", marker, err)
	}
	return nil
}

func verifyTopLevelRollback(ctx context.Context, run *suiteRun, client *qrlclient.Client, w wallet.Wallet, from, reverter common.Address) error {
	receipt, err := sendVM64FailingCall(ctx, run, client, w, from, reverter)
	if err != nil {
		return err
	}
	if receipt.Status != types.ReceiptStatusFailed || receipt.BlockNumber == nil || receipt.BlockNumber.Sign() == 0 || len(receipt.Logs) != 0 {
		return fmt.Errorf("top-level revert receipt status=%d block=%v logs=%d", receipt.Status, receipt.BlockNumber, len(receipt.Logs))
	}
	tx, pending, err := client.TransactionByHash(ctx, receipt.TxHash)
	if err != nil || tx == nil || pending || tx.Hash() != receipt.TxHash {
		return fmt.Errorf("read failed top-level transaction %s: pending=%t error=%v", receipt.TxHash, pending, err)
	}
	preBlock := new(big.Int).Sub(new(big.Int).Set(receipt.BlockNumber), big.NewInt(1))
	preNonce, err := client.NonceAt(ctx, from, preBlock)
	if err != nil {
		return fmt.Errorf("failed transaction pre-nonce: %w", err)
	}
	postNonce, err := client.NonceAt(ctx, from, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("failed transaction post-nonce: %w", err)
	}
	if preNonce != tx.Nonce() || postNonce != tx.Nonce()+1 {
		return fmt.Errorf("failed transaction nonce transition pre=%d tx=%d post=%d", preNonce, tx.Nonce(), postNonce)
	}
	preBalance, err := client.BalanceAt(ctx, from, preBlock)
	if err != nil {
		return fmt.Errorf("failed transaction pre-balance: %w", err)
	}
	postBalance, err := client.BalanceAt(ctx, from, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("failed transaction post-balance: %w", err)
	}
	if receipt.EffectiveGasPrice == nil {
		return fmt.Errorf("failed transaction receipt omits effective gas price")
	}
	wantFee := new(big.Int).Mul(new(big.Int).SetUint64(receipt.GasUsed), receipt.EffectiveGasPrice)
	gotFee := new(big.Int).Sub(preBalance, postBalance)
	if gotFee.Cmp(wantFee) != 0 {
		return fmt.Errorf("failed transaction balance delta=%s, want gas fee %s", gotFee, wantFee)
	}
	storage, err := client.StorageAt(ctx, reverter, common.Hash{}, receipt.BlockNumber)
	if err != nil || !zeroStorage(storage) {
		return fmt.Errorf("top-level REVERT leaked storage=%x error=%v", storage, err)
	}
	return nil
}

// checkLiveVM64Opcodes exercises the address-sensitive VM opcodes on the live
// chain. The harness invokes this suite independently against both execution
// clients; every mutation has its own prepared/submitted label, so a resumed
// lifecycle re-observes the exact raw transaction at the failed boundary.
func checkLiveVM64Opcodes(ctx context.Context, run *suiteRun, client *qrlclient.Client, w wallet.Wallet, from common.Address) error {
	contextCode := contextRuntime()
	contextAddress, _, err := deployVM64Contract(ctx, run, TransactionVM64ContextDeploy, client, w, from, contextCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 context probe: %w", err)
	}
	collision := collisionAddress(contextAddress)
	if bytes.Equal(contextAddress[:common.AddressLength/2], collision[:common.AddressLength/2]) || !bytes.Equal(contextAddress[common.AddressLength/2:], collision[common.AddressLength/2:]) {
		return fmt.Errorf("invalid VM64 same-low-half collision fixture")
	}
	if code, err := client.CodeAt(ctx, collision, nil); err != nil || len(code) != 0 {
		return fmt.Errorf("VM64 collision fixture unexpectedly has code %x: %w", code, err)
	}
	if _, err := sendValue(ctx, run, TransactionVM64ContextFund, client, w, from, contextAddress, new(big.Int).SetUint64(vm64ContextBalance)); err != nil {
		return fmt.Errorf("fund VM64 context probe: %w", err)
	}
	collisionReceipt, err := sendValue(ctx, run, TransactionVM64CollisionFund, client, w, from, collision, new(big.Int).SetUint64(vm64CollisionBalance))
	if err != nil {
		return fmt.Errorf("fund VM64 upper-half collision: %w", err)
	}
	for address, want := range map[common.Address]uint64{contextAddress: vm64ContextBalance, collision: vm64CollisionBalance} {
		balance, err := client.BalanceAt(ctx, address, collisionReceipt.BlockNumber)
		if err != nil || balance.Cmp(new(big.Int).SetUint64(want)) != 0 {
			return fmt.Errorf("VM64 fixture %s balance=%v error=%v, want %d", address, balance, err, want)
		}
	}

	routerCode := callRouterRuntime(contextAddress)
	router, _, err := deployVM64Contract(ctx, run, TransactionVM64CallRouterDeploy, client, w, from, routerCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 call router: %w", err)
	}
	if err := verifyLiveContextAndCalls(ctx, client, from, contextAddress, router); err != nil {
		return err
	}

	introspectionCode := introspectionRuntime(contextAddress, collision)
	introspection, _, err := deployVM64Contract(ctx, run, TransactionVM64IntrospectionDeploy, client, w, from, introspectionCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 account-opcode probe: %w", err)
	}
	if err := verifyLiveIntrospection(ctx, client, from, introspection, contextAddress, collision, contextCode); err != nil {
		return err
	}

	warmthCode := warmthRuntime(contextAddress, collision)
	warmth, _, err := deployVM64Contract(ctx, run, TransactionVM64WarmthDeploy, client, w, from, warmthCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 warmth probe: %w", err)
	}
	if err := verifyLiveWarmth(ctx, client, from, warmth); err != nil {
		return err
	}

	creatorCode, childRuntime := creatorRuntime()
	creator, _, err := deployVM64Contract(ctx, run, TransactionVM64CreatorDeploy, client, w, from, creatorCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 internal creator: %w", err)
	}
	childInit := deploymentBytecode(childRuntime)
	if _, err := verifyCreatorMutation(ctx, run, client, w, from, creator, TransactionVM64Create, 0, common.Hash{}, childInit, childRuntime); err != nil {
		return fmt.Errorf("live internal CREATE: %w", err)
	}
	var create2Slot common.Hash
	create2Slot[len(create2Slot)-1] = 1
	if _, err := verifyCreatorMutation(ctx, run, client, w, from, creator, TransactionVM64Create2, 1, create2Slot, childInit, childRuntime); err != nil {
		return fmt.Errorf("live internal CREATE2: %w", err)
	}

	reverterCode := reverterRuntime()
	reverter, _, err := deployVM64Contract(ctx, run, TransactionVM64ReverterDeploy, client, w, from, reverterCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 rollback target: %w", err)
	}
	catcherCode := catcherRuntime(reverter)
	catcher, _, err := deployVM64Contract(ctx, run, TransactionVM64CatcherDeploy, client, w, from, catcherCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 rollback catcher: %w", err)
	}
	if err := verifyCaughtRollback(ctx, run, client, w, from, reverter, catcher); err != nil {
		return fmt.Errorf("caught inner storage/log rollback: %w", err)
	}
	if err := verifyTopLevelRollback(ctx, run, client, w, from, reverter); err != nil {
		return fmt.Errorf("failed top-level call rollback: %w", err)
	}
	return nil
}
