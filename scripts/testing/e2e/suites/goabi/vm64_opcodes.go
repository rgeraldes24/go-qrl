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
	"errors"
	"fmt"
	"math/big"

	qrl "github.com/theQRL/go-qrl"
	qrlabi "github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/rpc"
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

type vm64CustomErrorVector struct {
	definition qrlabi.Error
	payload    []byte
	recipient  common.Address
	amount     *big.Int
	tag        [64]byte
	data       []byte
	reason     string
	values     [2]*big.Int
}

func mustVM64ABIType(name string) qrlabi.Type {
	typ, err := qrlabi.NewType(name, "", nil)
	if err != nil {
		panic(fmt.Sprintf("build live ABI type %s: %v", name, err))
	}
	return typ
}

var (
	vm64AddressABIType = mustVM64ABIType("address")
	vm64Uint512ABIType = mustVM64ABIType("uint512")
	vm64Bytes64ABIType = mustVM64ABIType("bytes64")
)

func mustVM64RevertPayload(signature, typeName string, value any) []byte {
	body, err := (qrlabi.Arguments{{Type: mustVM64ABIType(typeName)}}).Pack(value)
	if err != nil {
		panic(fmt.Sprintf("pack live %s revert payload: %v", signature, err))
	}
	return append(append([]byte{}, crypto.Keccak256([]byte(signature))[:4]...), body...)
}

var (
	vm64ErrorReason  = "live Error(string) crosses a VM64 word: " + string(bytes.Repeat([]byte{'e'}, vm.WordBytes+1)) + " \u754c\x00"
	vm64ErrorPayload = mustVM64RevertPayload("Error(string)", "string", vm64ErrorReason)
	vm64PanicPayload = mustVM64RevertPayload("Panic(uint256)", "uint256", big.NewInt(0x11))
)

var vm64CustomError = func() vm64CustomErrorVector {
	vector := vm64CustomErrorVector{
		recipient: common.BytesToAddress(bytes.Repeat([]byte{0xa7}, common.AddressLength)),
		amount:    new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1)),
		data:      bytes.Repeat([]byte{0x41}, 65),
		reason:    "live VM64 custom error crosses a 64-byte word: \u754c\x00",
		values:    [2]*big.Int{big.NewInt(0), new(big.Int).Lsh(big.NewInt(1), 511)},
	}
	for i := range vector.tag {
		vector.tag[i] = byte(i*23 + 9)
	}
	vector.definition = qrlabi.NewError("VM64Failure", qrlabi.Arguments{
		{Name: "recipient", Type: vm64AddressABIType},
		{Name: "amount", Type: vm64Uint512ABIType},
		{Name: "tag", Type: vm64Bytes64ABIType},
		{Name: "data", Type: mustVM64ABIType("bytes")},
		{Name: "reason", Type: mustVM64ABIType("string")},
		{Name: "values", Type: mustVM64ABIType("uint512[2]")},
	})
	body, err := vector.definition.Inputs.Pack(vector.recipient, vector.amount, vector.tag, vector.data, vector.reason, vector.values)
	if err != nil {
		panic(fmt.Sprintf("pack live custom-error vector: %v", err))
	}
	vector.payload = append(append([]byte{}, vector.definition.ID[:4]...), body...)
	return vector
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
	appendModeDispatch(a, []modeBranch{
		{mode: 1, label: "custom-error"},
		{mode: 2, label: "error-string"},
		{mode: 3, label: "panic"},
	}, "rollback")
	a.markJump("rollback")
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
	a.markJump("custom-error")
	a.pushUint(uint64(len(vm64CustomError.payload)))
	a.pushLabel("custom-error-data")
	a.pushUint(0)
	a.op(vm.CODECOPY)
	a.pushUint(uint64(len(vm64CustomError.payload)))
	a.pushUint(0)
	a.op(vm.REVERT)
	a.markJump("error-string")
	a.pushUint(uint64(len(vm64ErrorPayload)))
	a.pushLabel("error-string-data")
	a.pushUint(0)
	a.op(vm.CODECOPY)
	a.pushUint(uint64(len(vm64ErrorPayload)))
	a.pushUint(0)
	a.op(vm.REVERT)
	a.markJump("panic")
	a.pushUint(uint64(len(vm64PanicPayload)))
	a.pushLabel("panic-data")
	a.pushUint(0)
	a.op(vm.CODECOPY)
	a.pushUint(uint64(len(vm64PanicPayload)))
	a.pushUint(0)
	a.op(vm.REVERT)
	a.markData("custom-error-data")
	a.raw(vm64CustomError.payload...)
	a.markData("error-string-data")
	a.raw(vm64ErrorPayload...)
	a.markData("panic-data")
	a.raw(vm64PanicPayload...)
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

type vm64ABIValue struct {
	typ   qrlabi.Type
	value any
}

func vm64AddressValue(value common.Address) vm64ABIValue {
	return vm64ABIValue{typ: vm64AddressABIType, value: value}
}

func vm64UintValue(value uint64) vm64ABIValue {
	return vm64ABIValue{typ: vm64Uint512ABIType, value: new(big.Int).SetUint64(value)}
}

func vm64Bytes64Value(value []byte) vm64ABIValue {
	if len(value) != vm.WordBytes {
		panic(fmt.Sprintf("VM64 bytes64 value is %d bytes", len(value)))
	}
	var word [vm.WordBytes]byte
	copy(word[:], value)
	return vm64ABIValue{typ: vm64Bytes64ABIType, value: word}
}

func verifyVM64Output(output []byte, expected ...vm64ABIValue) error {
	arguments := make(qrlabi.Arguments, len(expected))
	values := make([]any, len(expected))
	for index, item := range expected {
		arguments[index].Type = item.typ
		values[index] = item.value
	}
	want, err := arguments.Pack(values...)
	if err != nil {
		return fmt.Errorf("pack expected VM64 output: %w", err)
	}
	if !bytes.Equal(output, want) {
		return fmt.Errorf("VM64 output=%x, want %x", output, want)
	}
	return nil
}

func unpackVM64Output(output []byte, types ...qrlabi.Type) ([]any, error) {
	if len(output) != len(types)*vm.WordBytes {
		return nil, fmt.Errorf("VM64 output is %d bytes, want %d", len(output), len(types)*vm.WordBytes)
	}
	arguments := make(qrlabi.Arguments, len(types))
	for index, typ := range types {
		arguments[index].Type = typ
	}
	values, err := arguments.Unpack(output)
	if err != nil {
		return nil, fmt.Errorf("unpack VM64 ABI output: %w", err)
	}
	return values, nil
}

func unpackVM64Address(output []byte) (common.Address, error) {
	values, err := unpackVM64Output(output, vm64AddressABIType)
	if err != nil {
		return common.Address{}, err
	}
	address, ok := values[0].(common.Address)
	if !ok {
		return common.Address{}, fmt.Errorf("VM64 address output has type %T", values[0])
	}
	return address, nil
}

func vm64OutputUint64(value any) (uint64, error) {
	integer, ok := value.(*big.Int)
	if !ok {
		return 0, fmt.Errorf("VM64 integer output has type %T", value)
	}
	if !integer.IsUint64() {
		return 0, fmt.Errorf("VM64 integer %x exceeds uint64", integer)
	}
	return integer.Uint64(), nil
}

func collisionAddress(address common.Address) common.Address {
	collision := address
	collision[0] ^= 0x80
	return collision
}

func deployVM64Contract(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address, runtime []byte) (common.Address, *types.Receipt, error) {
	receipt, err := deployRaw(ctx, client, w, from, deploymentBytecode(runtime))
	if err != nil {
		return common.Address{}, nil, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful || receipt.ContractAddress == (common.Address{}) {
		return common.Address{}, nil, fmt.Errorf("VM64 deployment receipt status=%d contract=%s", receipt.Status, receipt.ContractAddress)
	}
	code, err := client.CodeAt(ctx, receipt.ContractAddress, receipt.BlockNumber)
	if err != nil {
		return common.Address{}, nil, fmt.Errorf("read VM64 runtime: %w", err)
	}
	if !bytes.Equal(code, runtime) {
		return common.Address{}, nil, fmt.Errorf("VM64 runtime mismatch: have %x want %x", code, runtime)
	}
	return receipt.ContractAddress, receipt, nil
}

func sendVM64Transaction(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from, to common.Address, data []byte, gasLimit uint64, expectedStatus uint64) (*types.Receipt, error) {
	var (
		tx  *types.Transaction
		err error
	)
	if gasLimit == 0 {
		tx, err = signDynamicFeeTx(ctx, client, w, from, &to, new(big.Int), data)
	} else {
		auth, authErr := newTransactor(ctx, client, w, from)
		if authErr != nil {
			return nil, authErr
		}
		auth.GasLimit = gasLimit
		auth.NoSend = true
		tx, err = bind.NewBoundContract(to, qrlabi.ABI{}, client, client, client).RawTransact(auth, data)
	}
	if err != nil {
		return nil, fmt.Errorf("sign VM64 transaction: %w", err)
	}
	return submitTransaction(ctx, client, tx, expectedStatus)
}

func sendVM64Call(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from, to common.Address, data []byte) (*types.Receipt, error) {
	return sendVM64Transaction(ctx, client, w, from, to, data, 0, types.ReceiptStatusSuccessful)
}

func sendVM64FailingCall(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from, to common.Address) (*types.Receipt, error) {
	return sendVM64Transaction(ctx, client, w, from, to, nil, 800_000, types.ReceiptStatusFailed)
}

func callAt(ctx context.Context, client *qrlclient.Client, from, to common.Address, data []byte, block *big.Int) ([]byte, error) {
	return client.CallContract(ctx, qrl.CallMsg{From: from, To: &to, Data: data}, block)
}

func verifyContextOutput(output []byte, address, origin, caller, coinbase common.Address, balance uint64, includeSuccess bool) error {
	expected := []vm64ABIValue{
		vm64AddressValue(address),
		vm64AddressValue(origin),
		vm64AddressValue(caller),
		vm64AddressValue(coinbase),
		vm64UintValue(balance),
	}
	if includeSuccess {
		expected = append(expected, vm64UintValue(1))
	}
	return verifyVM64Output(output, expected...)
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
	if err := verifyContextOutput(direct, contextAddress, from, from, header.Coinbase, vm64ContextBalance, false); err != nil {
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
		if err := verifyContextOutput(output, probe.address, from, probe.caller, header.Coinbase, probe.balance, true); err != nil {
			return fmt.Errorf("VM64 %s context/return data: %w", probe.name, err)
		}
	}
	return nil
}

func verifyIntrospectionOutput(output []byte, target, collision common.Address, targetRuntime []byte) error {
	wantTargetHash := common.LeftPadBytes(crypto.Keccak256(targetRuntime), vm.WordBytes)
	wantCollisionHash := common.LeftPadBytes(crypto.Keccak256(nil), vm.WordBytes)
	wantTargetCopy := make([]byte, vm.WordBytes)
	copy(wantTargetCopy, targetRuntime)
	if bytes.Equal(target[:common.AddressLength/2], collision[:common.AddressLength/2]) ||
		!bytes.Equal(target[common.AddressLength/2:], collision[common.AddressLength/2:]) {
		return fmt.Errorf("VM64 introspection fixtures do not differ only in their upper half")
	}
	return verifyVM64Output(
		output,
		vm64UintValue(vm64ContextBalance),
		vm64UintValue(vm64CollisionBalance),
		vm64UintValue(uint64(len(targetRuntime))),
		vm64UintValue(0),
		vm64Bytes64Value(wantTargetHash),
		vm64Bytes64Value(wantCollisionHash),
		vm64Bytes64Value(wantTargetCopy),
		vm64Bytes64Value(make([]byte, vm.WordBytes)),
	)
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
	if err := verifyIntrospectionOutput(output, target, collision, targetRuntime); err != nil {
		return fmt.Errorf("VM64 BALANCE/EXTCODE output: %w", err)
	}
	return nil
}

func gasAccessCosts(output []byte) (uint64, uint64, error) {
	values, err := unpackVM64Output(output, vm64Uint512ABIType, vm64Uint512ABIType, vm64Uint512ABIType)
	if err != nil {
		return 0, 0, err
	}
	gas := make([]uint64, 3)
	for index := range gas {
		gas[index], err = vm64OutputUint64(values[index])
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

func verifyCreatorMutation(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from, creator common.Address, mode byte, slot common.Hash, childInit, childRuntime []byte) (common.Address, error) {
	operation := "CREATE"
	if mode != 0 {
		operation = "CREATE2"
	}
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return common.Address{}, err
	}
	predictedRaw, err := callAt(ctx, client, from, creator, modeWord(mode), header.Number)
	if err != nil {
		return common.Address{}, fmt.Errorf("simulate %s return address: %w", operation, err)
	}
	simulated, err := unpackVM64Address(predictedRaw)
	if err != nil || simulated == (common.Address{}) {
		return common.Address{}, fmt.Errorf("%s simulated address=%s error=%v", operation, simulated, err)
	}
	receipt, err := sendVM64Call(ctx, client, w, from, creator, modeWord(mode))
	if err != nil {
		return common.Address{}, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful || receipt.BlockNumber == nil || receipt.BlockNumber.Sign() == 0 || len(receipt.Logs) != 1 || receipt.Logs[0].Address != creator || len(receipt.Logs[0].Topics) != 0 {
		return common.Address{}, fmt.Errorf("%s receipt did not preserve the returned 64-byte address: status=%d logs=%+v", operation, receipt.Status, receipt.Logs)
	}
	created, err := unpackVM64Address(receipt.Logs[0].Data)
	if err != nil || created == (common.Address{}) {
		return common.Address{}, fmt.Errorf("%s logged address=%x error=%v", operation, receipt.Logs[0].Data, err)
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
		return common.Address{}, fmt.Errorf("%s full returned address=%s simulated=%s want=%s", operation, created, simulated, expected)
	}
	stored, err := client.StorageAt(ctx, creator, slot, receipt.BlockNumber)
	if err != nil {
		return common.Address{}, fmt.Errorf("%s stored address: %w", operation, err)
	}
	if !bytes.Equal(common.LeftPadBytes(stored, vm.WordBytes), created[:]) {
		return common.Address{}, fmt.Errorf("%s stored address=%x, want %x", operation, stored, created)
	}
	code, err := client.CodeAt(ctx, created, receipt.BlockNumber)
	if err != nil || !bytes.Equal(code, childRuntime) {
		return common.Address{}, fmt.Errorf("%s created runtime=%x error=%v, want %x", operation, code, err, childRuntime)
	}
	childOutput, err := callAt(ctx, client, from, created, nil, receipt.BlockNumber)
	if err != nil {
		return common.Address{}, fmt.Errorf("call full-width %s child %s: %w", operation, created, err)
	}
	if err := verifyVM64Output(childOutput, vm64UintValue(uint64(vm64ChildMarker))); err != nil {
		return common.Address{}, fmt.Errorf("%s child marker: %w", operation, err)
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

func liveRevertData(ctx context.Context, client *qrlclient.Client, from, reverter common.Address, mode byte) ([]byte, error) {
	_, err := callAt(ctx, client, from, reverter, modeWord(mode), nil)
	if err == nil {
		return nil, errors.New("live VM64 revert call unexpectedly succeeded")
	}
	var dataError rpc.DataError
	if !errors.As(err, &dataError) {
		return nil, fmt.Errorf("live VM64 revert call returned %T, want rpc.DataError: %w", err, err)
	}
	hexData, ok := dataError.ErrorData().(string)
	if !ok {
		return nil, fmt.Errorf("live VM64 revert data has type %T, want string", dataError.ErrorData())
	}
	return common.FromHex(hexData), nil
}

func verifyLiveStandardReverts(ctx context.Context, client *qrlclient.Client, from, reverter common.Address) error {
	vectors := []struct {
		name    string
		mode    byte
		payload []byte
		want    string
	}{
		{name: "Error(string)", mode: 2, payload: vm64ErrorPayload, want: vm64ErrorReason},
		{name: "Panic(uint256)", mode: 3, payload: vm64PanicPayload, want: "arithmetic underflow or overflow"},
	}
	for _, vector := range vectors {
		revertData, err := liveRevertData(ctx, client, from, reverter, vector.mode)
		if err != nil {
			return fmt.Errorf("%s: %w", vector.name, err)
		}
		if !bytes.Equal(revertData, vector.payload) {
			return fmt.Errorf("live %s payload=%x, want %x", vector.name, revertData, vector.payload)
		}
		got, err := qrlabi.UnpackRevert(revertData)
		if err != nil || got != vector.want {
			return fmt.Errorf("decode live %s=%q, error=%v, want %q", vector.name, got, err, vector.want)
		}
	}
	return nil
}

func verifyLiveCustomError(ctx context.Context, client *qrlclient.Client, from, reverter common.Address) error {
	revertData, err := liveRevertData(ctx, client, from, reverter, 1)
	if err != nil {
		return err
	}
	if !bytes.Equal(revertData, vm64CustomError.payload) {
		return fmt.Errorf("live VM64 custom-error payload=%x, want %x", revertData, vm64CustomError.payload)
	}
	if len(revertData) < 4 {
		return fmt.Errorf("live VM64 custom-error payload is only %d bytes", len(revertData))
	}
	parsed := qrlabi.ABI{Errors: map[string]qrlabi.Error{vm64CustomError.definition.Name: vm64CustomError.definition}}
	var selector [4]byte
	copy(selector[:], revertData[:4])
	resolved, err := parsed.ErrorByID(selector)
	if err != nil || resolved.Sig != vm64CustomError.definition.Sig {
		return fmt.Errorf("resolve live VM64 custom error: error=%v signature=%v", err, resolved)
	}
	decoded, err := resolved.Unpack(revertData)
	if err != nil {
		return fmt.Errorf("decode live VM64 custom error: %w", err)
	}
	values := decoded.([]any)
	if len(values) != 6 || values[0] != vm64CustomError.recipient || values[1].(*big.Int).Cmp(vm64CustomError.amount) != 0 ||
		values[2] != vm64CustomError.tag || !bytes.Equal(values[3].([]byte), vm64CustomError.data) || values[4] != vm64CustomError.reason ||
		values[5].([2]*big.Int)[0].Sign() != 0 || values[5].([2]*big.Int)[1].Cmp(vm64CustomError.values[1]) != 0 {
		return fmt.Errorf("decoded live VM64 custom-error values=%#v", values)
	}
	type customErrorResult struct {
		Recipient common.Address
		Amount    *big.Int
		Tag       [64]byte
		Data      []byte
		Reason    string
		Values    [2]*big.Int
	}
	var structured customErrorResult
	if err := parsed.UnpackIntoInterface(&structured, vm64CustomError.definition.Name, revertData[4:]); err != nil {
		return fmt.Errorf("decode live VM64 custom error into interface: %w", err)
	}
	if structured.Recipient != vm64CustomError.recipient || structured.Amount.Cmp(vm64CustomError.amount) != 0 || structured.Tag != vm64CustomError.tag ||
		!bytes.Equal(structured.Data, vm64CustomError.data) || structured.Reason != vm64CustomError.reason ||
		structured.Values[0].Sign() != 0 || structured.Values[1].Cmp(vm64CustomError.values[1]) != 0 {
		return fmt.Errorf("live VM64 custom-error structured decode=%+v", structured)
	}
	mapped := make(map[string]any)
	if err := parsed.UnpackIntoMap(mapped, vm64CustomError.definition.Name, revertData[4:]); err != nil {
		return fmt.Errorf("decode live VM64 custom error into map: %w", err)
	}
	if mapped["recipient"] != vm64CustomError.recipient || mapped["reason"] != vm64CustomError.reason || !bytes.Equal(mapped["data"].([]byte), vm64CustomError.data) {
		return fmt.Errorf("live VM64 custom-error map decode=%#v", mapped)
	}
	return nil
}

func verifyCaughtRollback(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from, reverter, catcher common.Address) error {
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return err
	}
	output, err := callAt(ctx, client, from, catcher, nil, header.Number)
	if err != nil {
		return fmt.Errorf("simulate caught inner revert: %w", err)
	}
	if err := verifyVM64Output(
		output,
		vm64UintValue(uint64(vm64RollbackMarker)),
		vm64UintValue(0),
	); err != nil {
		return fmt.Errorf("caught revert data and CALL status: %w", err)
	}
	receipt, err := sendVM64Call(ctx, client, w, from, catcher, nil)
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
	if err := verifyVM64Output(
		common.LeftPadBytes(catcherStorage, vm.WordBytes),
		vm64UintValue(uint64(vm64CatcherMarker)),
	); err != nil {
		return fmt.Errorf("outer catcher storage marker: %w", err)
	}
	return nil
}

func verifyTopLevelRollback(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from, reverter common.Address) error {
	receipt, err := sendVM64FailingCall(ctx, client, w, from, reverter)
	if err != nil {
		return err
	}
	if receipt.Status != types.ReceiptStatusFailed || receipt.BlockNumber == nil || receipt.BlockNumber.Sign() == 0 ||
		receipt.BlockHash == (common.Hash{}) || len(receipt.Logs) != 0 {
		return fmt.Errorf(
			"top-level revert receipt status=%d block=%v blockHash=%s logs=%d",
			receipt.Status,
			receipt.BlockNumber,
			receipt.BlockHash,
			len(receipt.Logs),
		)
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
	storage, err := client.StorageAt(ctx, reverter, common.Hash{}, receipt.BlockNumber)
	if err != nil || !zeroStorage(storage) {
		return fmt.Errorf("top-level REVERT leaked storage=%x error=%v", storage, err)
	}
	return nil
}

// checkLiveVM64Opcodes exercises the address-sensitive VM opcodes on the live
// chain. The harness invokes this suite independently against both execution
// clients.
func checkLiveVM64Opcodes(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address) error {
	contextCode := contextRuntime()
	contextAddress, _, err := deployVM64Contract(ctx, client, w, from, contextCode)
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
	if _, err := sendValue(ctx, client, w, from, contextAddress, new(big.Int).SetUint64(vm64ContextBalance)); err != nil {
		return fmt.Errorf("fund VM64 context probe: %w", err)
	}
	collisionReceipt, err := sendValue(ctx, client, w, from, collision, new(big.Int).SetUint64(vm64CollisionBalance))
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
	router, _, err := deployVM64Contract(ctx, client, w, from, routerCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 call router: %w", err)
	}
	if err := verifyLiveContextAndCalls(ctx, client, from, contextAddress, router); err != nil {
		return err
	}

	introspectionCode := introspectionRuntime(contextAddress, collision)
	introspection, _, err := deployVM64Contract(ctx, client, w, from, introspectionCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 account-opcode probe: %w", err)
	}
	if err := verifyLiveIntrospection(ctx, client, from, introspection, contextAddress, collision, contextCode); err != nil {
		return err
	}

	warmthCode := warmthRuntime(contextAddress, collision)
	warmth, _, err := deployVM64Contract(ctx, client, w, from, warmthCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 warmth probe: %w", err)
	}
	if err := verifyLiveWarmth(ctx, client, from, warmth); err != nil {
		return err
	}

	creatorCode, childRuntime := creatorRuntime()
	creator, _, err := deployVM64Contract(ctx, client, w, from, creatorCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 internal creator: %w", err)
	}
	childInit := deploymentBytecode(childRuntime)
	if _, err := verifyCreatorMutation(ctx, client, w, from, creator, 0, common.Hash{}, childInit, childRuntime); err != nil {
		return fmt.Errorf("live internal CREATE: %w", err)
	}
	var create2Slot common.Hash
	create2Slot[len(create2Slot)-1] = 1
	if _, err := verifyCreatorMutation(ctx, client, w, from, creator, 1, create2Slot, childInit, childRuntime); err != nil {
		return fmt.Errorf("live internal CREATE2: %w", err)
	}

	reverterCode := reverterRuntime()
	reverter, _, err := deployVM64Contract(ctx, client, w, from, reverterCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 rollback target: %w", err)
	}
	if err := verifyLiveCustomError(ctx, client, from, reverter); err != nil {
		return fmt.Errorf("live custom-error revert data: %w", err)
	}
	if err := verifyLiveStandardReverts(ctx, client, from, reverter); err != nil {
		return fmt.Errorf("live standard revert data: %w", err)
	}
	catcherCode := catcherRuntime(reverter)
	catcher, _, err := deployVM64Contract(ctx, client, w, from, catcherCode)
	if err != nil {
		return fmt.Errorf("deploy VM64 rollback catcher: %w", err)
	}
	if err := verifyCaughtRollback(ctx, client, w, from, reverter, catcher); err != nil {
		return fmt.Errorf("caught inner storage/log rollback: %w", err)
	}
	if err := verifyTopLevelRollback(ctx, client, w, from, reverter); err != nil {
		return fmt.Errorf("failed top-level call rollback: %w", err)
	}
	return nil
}
