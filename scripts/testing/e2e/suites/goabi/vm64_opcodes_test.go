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
	"math/big"
	"strings"
	"testing"

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
	words, err := decodeVM64Words(output, 5)
	if err != nil {
		t.Fatal(err)
	}
	contract := common.BytesToAddress([]byte("contract"))
	if err := verifyContextWords(words, contract, origin, origin, coinbase, 0); err != nil {
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
			words, err := decodeVM64Words(output, 6)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifyContextWords(words, probe.address, origin, probe.caller, coinbase, probe.balance); err != nil {
				t.Fatal(err)
			}
			success, err := wordUint64(words[5])
			if err != nil || success != 1 {
				t.Fatalf("success=%d error=%v", success, err)
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
	words, err := decodeVM64Words(output, 8)
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []uint64{vm64ContextBalance, vm64CollisionBalance, uint64(len(contextCode)), 0} {
		got, err := wordUint64(words[index])
		if err != nil || got != want {
			t.Fatalf("word %d=%d error=%v, want %d", index, got, err, want)
		}
	}
	if !bytes.Equal(words[4], common.LeftPadBytes(crypto.Keccak256(contextCode), vm.WordBytes)) ||
		!bytes.Equal(words[5], common.LeftPadBytes(crypto.Keccak256(nil), vm.WordBytes)) {
		t.Fatal("EXTCODEHASH output mismatch")
	}
	wantCopy := make([]byte, vm.WordBytes)
	copy(wantCopy, contextCode)
	if !bytes.Equal(words[6], wantCopy) || !bytes.Equal(words[7], make([]byte, vm.WordBytes)) {
		t.Fatal("EXTCODECOPY output mismatch")
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
	created, err := wordAddress(createdRaw)
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
	created2, err := wordAddress(created2Raw)
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
	database.SetCode(reverter, reverterRuntime())
	output, database, err := vmruntime.Execute(catcherRuntime(reverter), nil, &vmruntime.Config{State: database, Origin: origin})
	if err != nil {
		t.Fatalf("execute caught revert: %v", err)
	}
	words, err := decodeVM64Words(output, 2)
	if err != nil {
		t.Fatal(err)
	}
	marker, _ := wordUint64(words[0])
	success, _ := wordUint64(words[1])
	if marker != uint64(vm64RollbackMarker) || success != 0 {
		t.Fatalf("caught output marker=%d success=%d", marker, success)
	}
	if got := database.GetState(reverter, common.Hash{}); got != (common.StorageValue64{}) {
		t.Fatalf("reverter storage leaked %x", got)
	}
	if got := database.GetState(creator, common.Hash{}); new(big.Int).SetBytes(got[:]).Uint64() != uint64(vm64CatcherMarker) {
		t.Fatalf("catcher storage marker=%x", got)
	}
}

func TestVM64WordDecodingAndMutationJournalOrder(t *testing.T) {
	t.Parallel()
	if _, err := decodeVM64Words(make([]byte, vm.WordBytes-1), 1); err == nil {
		t.Fatal("short VM64 word was accepted")
	}
	tooWide := make([]byte, vm.WordBytes)
	tooWide[0] = 1
	if _, err := wordUint64(tooWide); err == nil {
		t.Fatal("oversized VM64 integer was accepted")
	}

	hash := common.HexToHash("0x1234").Hex()
	recorded := make(map[string]string)
	for _, label := range mandatoryTransactionLabelsInSuiteOrder[:6] {
		recorded[label] = hash
	}
	_, prepared := testPreparedTransaction(91)
	if _, err := newSuiteRun(Options{RecordedTransactions: recorded, PreparedTransactions: map[string]PreparedTransaction{
		TransactionVM64ContextFund: prepared,
	}}); err == nil || !strings.Contains(err.Error(), TransactionVM64ContextDeploy) {
		t.Fatalf("out-of-order VM64 prepared mutation error=%v", err)
	}
	if _, err := validateRecordedTransactions(map[string]string{
		TransactionEventEmitterDeploy:     hash,
		TransactionEventEmitterStore:      hash,
		TransactionEventEmitterClear:      hash,
		TransactionStorageContractDeploy:  hash,
		TransactionAddressIsolationFirst:  hash,
		TransactionAddressIsolationSecond: hash,
		TransactionVM64ContextFund:        hash,
	}); err == nil || !strings.Contains(err.Error(), TransactionVM64ContextDeploy) {
		t.Fatalf("VM64 recorded journal hole error=%v", err)
	}
	recorded[TransactionVM64ContextDeploy] = hash
	if _, err := newSuiteRun(Options{RecordedTransactions: recorded, PreparedTransactions: map[string]PreparedTransaction{
		TransactionVM64ContextFund: prepared,
	}}); err != nil {
		t.Fatalf("next VM64 prepared mutation rejected: %v", err)
	}
}
