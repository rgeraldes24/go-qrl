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
	"errors"
	"math/big"
	"testing"

	qrlabi "github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/state"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	vmruntime "github.com/theQRL/go-qrl/core/vm/runtime"
	"github.com/theQRL/go-qrl/crypto"
)

func vm64TestAddress(seed byte) common.Address {
	var address common.Address
	for index := range address {
		address[index] = seed + byte(index*3)
	}
	return address
}

func vm64TestState(t *testing.T) *state.StateDB {
	t.Helper()
	database, err := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	return database
}

func TestVM64DeploymentAndContextBytecode(t *testing.T) {
	t.Parallel()
	runtime := contextRuntime()
	deployed, _, err := vmruntime.Execute(deploymentBytecode(runtime), nil, nil)
	if err != nil {
		t.Fatalf("execute VM64 deployment bytecode: %v", err)
	}
	if !bytes.Equal(deployed, runtime) {
		t.Fatalf("deployment returned %x, want runtime %x", deployed, runtime)
	}

	origin := vm64TestAddress(0x21)
	coinbase := vm64TestAddress(0x61)
	output, _, err := vmruntime.Execute(runtime, nil, &vmruntime.Config{Origin: origin, Coinbase: coinbase})
	if err != nil {
		t.Fatalf("execute VM64 context runtime: %v", err)
	}
	contract := common.BytesToAddress([]byte("contract"))
	if err := verifyContextOutput(output, contract, origin, origin, coinbase, 0, false); err != nil {
		t.Fatal(err)
	}
}

func TestVM64CallAndAccountOpcodeBytecode(t *testing.T) {
	t.Parallel()
	origin := vm64TestAddress(0x12)
	coinbase := vm64TestAddress(0x72)
	target := vm64TestAddress(0x31)
	collision := collisionAddress(target)
	contextCode := contextRuntime()
	main := common.BytesToAddress([]byte("contract"))

	for _, probe := range []struct {
		name            string
		mode            byte
		address, caller common.Address
		balance         uint64
	}{
		{name: "CALL", mode: 0, address: target, caller: main, balance: vm64ContextBalance},
		{name: "DELEGATECALL", mode: 1, address: main, caller: origin, balance: 0},
		{name: "STATICCALL", mode: 2, address: target, caller: main, balance: vm64ContextBalance},
	} {
		t.Run(probe.name, func(t *testing.T) {
			database := vm64TestState(t)
			database.CreateAccount(target)
			database.SetCode(target, contextCode)
			database.SetBalance(target, new(big.Int).SetUint64(vm64ContextBalance))
			output, _, err := vmruntime.Execute(callRouterRuntime(target), modeWord(probe.mode), &vmruntime.Config{
				State: database, Origin: origin, Coinbase: coinbase,
			})
			if err != nil {
				t.Fatalf("execute router: %v", err)
			}
			if err := verifyContextOutput(output, probe.address, origin, probe.caller, coinbase, probe.balance, true); err != nil {
				t.Fatal(err)
			}
		})
	}

	database := vm64TestState(t)
	database.CreateAccount(target)
	database.CreateAccount(collision)
	database.SetCode(target, contextCode)
	database.SetBalance(target, new(big.Int).SetUint64(vm64ContextBalance))
	database.SetBalance(collision, new(big.Int).SetUint64(vm64CollisionBalance))
	output, _, err := vmruntime.Execute(introspectionRuntime(target, collision), nil, &vmruntime.Config{State: database, Origin: origin})
	if err != nil {
		t.Fatalf("execute introspection runtime: %v", err)
	}
	if err := verifyIntrospectionOutput(output, target, collision, contextCode); err != nil {
		t.Fatal(err)
	}
}

func TestVM64WarmthBytecodeUsesFullAddress(t *testing.T) {
	t.Parallel()
	target := vm64TestAddress(0x18)
	collision := collisionAddress(target)
	code := warmthRuntime(target, collision)
	run := func(mode byte) (uint64, uint64) {
		database := vm64TestState(t)
		database.CreateAccount(target)
		database.CreateAccount(collision)
		output, _, err := vmruntime.Execute(code, modeWord(mode), &vmruntime.Config{State: database, Origin: vm64TestAddress(0x88)})
		if err != nil {
			t.Fatalf("execute mode %d: %v", mode, err)
		}
		first, second, err := gasAccessCosts(output)
		if err != nil {
			t.Fatal(err)
		}
		return first, second
	}
	firstSame, secondSame := run(0)
	firstDistinct, secondDistinct := run(1)
	if firstSame <= secondSame+1000 || firstDistinct <= secondSame+1000 || secondDistinct <= secondSame+1000 {
		t.Fatalf("same=%d/%d distinct=%d/%d", firstSame, secondSame, firstDistinct, secondDistinct)
	}
}

func TestVM64CreateAndRollbackBytecode(t *testing.T) {
	t.Parallel()
	creatorCode, childRuntime := creatorRuntime()
	database := vm64TestState(t)
	origin := vm64TestAddress(0x42)
	creator := common.BytesToAddress([]byte("contract"))

	createdRaw, database, err := vmruntime.Execute(creatorCode, modeWord(0), &vmruntime.Config{State: database, Origin: origin})
	if err != nil {
		t.Fatalf("execute CREATE: %v", err)
	}
	created, err := unpackVM64Address(createdRaw)
	if err != nil {
		t.Fatal(err)
	}
	if want := crypto.CreateAddress(creator, 0); created != want {
		t.Fatalf("CREATE address=%s, want %s", created, want)
	}
	if code := database.GetCode(created); !bytes.Equal(code, childRuntime) {
		t.Fatalf("CREATE runtime=%x, want %x", code, childRuntime)
	}

	created2Raw, database, err := vmruntime.Execute(creatorCode, modeWord(1), &vmruntime.Config{State: database, Origin: origin})
	if err != nil {
		t.Fatalf("execute CREATE2: %v", err)
	}
	created2, err := unpackVM64Address(created2Raw)
	if err != nil {
		t.Fatal(err)
	}
	childInit := deploymentBytecode(childRuntime)
	if want := crypto.CreateAddress2(creator, vm64Create2Salt, crypto.Keccak256(childInit)); created2 != want {
		t.Fatalf("CREATE2 address=%s, want %s", created2, want)
	}
	if code := database.GetCode(created2); !bytes.Equal(code, childRuntime) {
		t.Fatalf("CREATE2 runtime=%x, want %x", code, childRuntime)
	}

	reverter := vm64TestAddress(0x51)
	database.CreateAccount(reverter)
	reverterCode := reverterRuntime()
	customOutput, _, customErr := vmruntime.Execute(reverterCode, modeWord(1), &vmruntime.Config{State: vm64TestState(t), Origin: origin})
	if !errors.Is(customErr, vm.ErrExecutionReverted) || !bytes.Equal(customOutput, vm64CustomError.payload) {
		t.Fatalf("custom-error revert output=%x error=%v, want %x", customOutput, customErr, vm64CustomError.payload)
	}
	for _, vector := range []struct {
		name    string
		mode    byte
		payload []byte
		want    string
	}{
		{name: "Error(string)", mode: 2, payload: vm64ErrorPayload, want: vm64ErrorReason},
		{name: "Panic(uint256)", mode: 3, payload: vm64PanicPayload, want: "arithmetic underflow or overflow"},
	} {
		output, _, revertErr := vmruntime.Execute(reverterCode, modeWord(vector.mode), &vmruntime.Config{State: vm64TestState(t), Origin: origin})
		if !errors.Is(revertErr, vm.ErrExecutionReverted) || !bytes.Equal(output, vector.payload) {
			t.Fatalf("%s revert output=%x error=%v, want %x", vector.name, output, revertErr, vector.payload)
		}
		got, unpackErr := qrlabi.UnpackRevert(output)
		if unpackErr != nil || got != vector.want {
			t.Fatalf("UnpackRevert %s=%q error=%v, want %q", vector.name, got, unpackErr, vector.want)
		}
	}
	database.SetCode(reverter, reverterCode)
	output, database, err := vmruntime.Execute(catcherRuntime(reverter), nil, &vmruntime.Config{State: database, Origin: origin})
	if err != nil {
		t.Fatalf("execute caught revert: %v", err)
	}
	if err := verifyVM64Output(
		output,
		vm64UintValue(uint64(vm64RollbackMarker)),
		vm64UintValue(0),
	); err != nil {
		t.Fatalf("caught output: %v", err)
	}
	if got := database.GetState(reverter, common.Hash{}); got != (common.StorageValue64{}) {
		t.Fatalf("reverter storage leaked %x", got)
	}
	if got := database.GetState(creator, common.Hash{}); new(big.Int).SetBytes(got[:]).Uint64() != uint64(vm64CatcherMarker) {
		t.Fatalf("catcher storage marker=%x", got)
	}
}

func TestVM64OutputDecodingRejectsInvalidWords(t *testing.T) {
	t.Parallel()
	if _, err := unpackVM64Output(make([]byte, vm.WordBytes-1), vm64Uint512ABIType); err == nil {
		t.Fatal("short VM64 word was accepted")
	}
	tooWide := new(big.Int).Lsh(big.NewInt(1), 64)
	if _, err := vm64OutputUint64(tooWide); err == nil {
		t.Fatal("oversized VM64 integer was accepted")
	}
}
