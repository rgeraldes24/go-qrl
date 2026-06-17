// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.
//
// go-qrl is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-qrl is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.

package vm

import (
	"bytes"
	"math/big"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/params"
)

func vm64WordHex(s string) string {
	return strings.Repeat("0", 128-len(s)) + s
}

func vm64IntFromHex(t *testing.T, s string) *uint512.Int {
	t.Helper()
	return new(uint512.Int).SetBytes(common.Hex2Bytes(s))
}

func TestVM64GoldenOpcodeWidthBoundaries(t *testing.T) {
	allOnes := strings.Repeat("f", 128)
	minSigned := "8" + strings.Repeat("0", 127)
	bit510 := "4" + strings.Repeat("0", 127)
	signExtendByte62Input := "0080" + strings.Repeat("0", 124)
	signExtendByte62Output := "ff80" + strings.Repeat("0", 124)
	signExtendByte31Input := strings.Repeat("0", 64) + "80" + strings.Repeat("0", 62)
	signExtendByte31Output := strings.Repeat("f", 64) + "80" + strings.Repeat("0", 62)

	t.Run("add wraps at 2^512", func(t *testing.T) {
		testTwoOperandOp(t, []TwoOperandTestcase{
			{allOnes, "01", strings.Repeat("0", 128)},
		}, opAdd, "add")
	})
	t.Run("sar uses bit 511 and handles shift 512", func(t *testing.T) {
		testTwoOperandOp(t, []TwoOperandTestcase{
			{minSigned, "01fe", strings.Repeat("f", 126) + "fe"}, // -2^511 >> 510 == -2
			{minSigned, "01ff", allOnes},                         // -2^511 >> 511 == -1
			{minSigned, "0200", allOnes},                         // shift == 512 saturates negative to -1
			{bit510, "01fe", vm64WordHex("1")},                   // +2^510 >> 510 == 1
		}, opSAR, "sar")
	})
	t.Run("signextend spans the full 64-byte word", func(t *testing.T) {
		testTwoOperandOp(t, []TwoOperandTestcase{
			{signExtendByte31Input, "1f", signExtendByte31Output},
			{signExtendByte62Input, "3e", signExtendByte62Output},
			{signExtendByte62Input, "3f", signExtendByte62Input},
		}, opSignExtend, "signextend")
	})
}

func TestVM64GoldenSignedArithmetic(t *testing.T) {
	allOnes := strings.Repeat("f", 128)
	minSigned := vm64IntFromHex(t, "8"+strings.Repeat("0", 127))
	negFive := vm64IntFromHex(t, strings.Repeat("f", 126)+"fb")
	negOne := vm64IntFromHex(t, allOnes)
	two := uint512.NewInt(2)
	maxPositive := vm64IntFromHex(t, "7"+strings.Repeat("f", 127))

	if got := new(uint512.Int).SDiv(minSigned, negOne); !got.Eq(minSigned) {
		t.Fatalf("(-2^511) / -1 should wrap to -2^511, got %x", got.Bytes64())
	}
	if got := new(uint512.Int).SDiv(negFive, two); !got.Eq(vm64IntFromHex(t, strings.Repeat("f", 126)+"fe")) {
		t.Fatalf("-5 / 2 should truncate toward zero to -2, got %x", got.Bytes64())
	}
	if got := new(uint512.Int).SMod(negFive, two); !got.Eq(negOne) {
		t.Fatalf("-5 %% 2 should keep dividend sign and equal -1, got %x", got.Bytes64())
	}
	if !minSigned.Slt(uint512.NewInt(1)) {
		t.Fatal("-2^511 should be signed-less-than 1")
	}
	if !maxPositive.Sgt(negOne) {
		t.Fatal("2^511-1 should be signed-greater-than -1")
	}
}

type vm64GoldenStateDB struct {
	storage map[common.Address]map[common.Hash]common.StorageValue64
	logs    []*types.Log
}

func newVM64GoldenStateDB() *vm64GoldenStateDB {
	return &vm64GoldenStateDB{storage: make(map[common.Address]map[common.Hash]common.StorageValue64)}
}

func (db *vm64GoldenStateDB) CreateAccount(common.Address)           {}
func (db *vm64GoldenStateDB) SubBalance(common.Address, *big.Int)    {}
func (db *vm64GoldenStateDB) AddBalance(common.Address, *big.Int)    {}
func (db *vm64GoldenStateDB) GetBalance(common.Address) *big.Int     { return new(big.Int) }
func (db *vm64GoldenStateDB) GetNonce(common.Address) uint64         { return 0 }
func (db *vm64GoldenStateDB) SetNonce(common.Address, uint64)        {}
func (db *vm64GoldenStateDB) GetCodeHash(common.Address) common.Hash { return common.Hash{} }
func (db *vm64GoldenStateDB) GetCode(common.Address) []byte          { return nil }
func (db *vm64GoldenStateDB) SetCode(common.Address, []byte)         {}
func (db *vm64GoldenStateDB) GetCodeSize(common.Address) int         { return 0 }
func (db *vm64GoldenStateDB) AddRefund(uint64)                       {}
func (db *vm64GoldenStateDB) SubRefund(uint64)                       {}
func (db *vm64GoldenStateDB) GetRefund() uint64                      { return 0 }
func (db *vm64GoldenStateDB) GetCommittedState(addr common.Address, key common.Hash) common.StorageValue64 {
	return db.GetState(addr, key)
}
func (db *vm64GoldenStateDB) GetState(addr common.Address, key common.Hash) common.StorageValue64 {
	if db.storage[addr] == nil {
		return common.StorageValue64{}
	}
	return db.storage[addr][key]
}
func (db *vm64GoldenStateDB) SetState(addr common.Address, key common.Hash, value common.StorageValue64) {
	if db.storage[addr] == nil {
		db.storage[addr] = make(map[common.Hash]common.StorageValue64)
	}
	db.storage[addr][key] = value
}
func (db *vm64GoldenStateDB) Exist(common.Address) bool               { return true }
func (db *vm64GoldenStateDB) Empty(common.Address) bool               { return false }
func (db *vm64GoldenStateDB) AddressInAccessList(common.Address) bool { return true }
func (db *vm64GoldenStateDB) SlotInAccessList(common.Address, common.Hash) (bool, bool) {
	return true, true
}
func (db *vm64GoldenStateDB) AddAddressToAccessList(common.Address)           {}
func (db *vm64GoldenStateDB) AddSlotToAccessList(common.Address, common.Hash) {}
func (db *vm64GoldenStateDB) Prepare(params.Rules, common.Address, common.Address, *common.Address, []common.Address, types.AccessList) {
}
func (db *vm64GoldenStateDB) RevertToSnapshot(int)            {}
func (db *vm64GoldenStateDB) Snapshot() int                   { return 0 }
func (db *vm64GoldenStateDB) AddLog(log *types.Log)           { db.logs = append(db.logs, log) }
func (db *vm64GoldenStateDB) AddPreimage(common.Hash, []byte) {}

func TestVM64GoldenStorageValueAndLogTopics(t *testing.T) {
	statedb := newVM64GoldenStateDB()
	contractAddr := common.BytesToAddress([]byte{0xca, 0xfe})
	env := NewQRVM(BlockContext{BlockNumber: big.NewInt(7)}, TxContext{}, statedb, params.TestChainConfig, Config{})
	contract := NewContract(AccountRef(contractAddr), AccountRef(contractAddr), new(big.Int), 1_000_000)
	scope := &ScopeContext{Memory: NewMemory(), Stack: newstack(), Contract: contract}
	pc := uint64(0)

	lowStorageKey := "11223344556677889900aabbccddeeff00112233445566778899aabbccddeeff"
	key := vm64IntFromHex(t, "01"+strings.Repeat("0", 62)+lowStorageKey)
	aliasKey := vm64IntFromHex(t, "ff"+strings.Repeat("0", 62)+lowStorageKey)
	value := vm64IntFromHex(t, "0123456789abcdeffedcba987654321000112233445566778899aabbccddeeffffeeddccbbaa998877665544332211000102030405060708090a0b0c0d0e0f")
	scope.Stack.push(value)
	scope.Stack.push(key)
	if _, err := opSstore(&pc, env.interpreter, scope); err != nil {
		t.Fatalf("SSTORE failed: %v", err)
	}
	scope.Stack.push(key)
	if _, err := opSload(&pc, env.interpreter, scope); err != nil {
		t.Fatalf("SLOAD failed: %v", err)
	}
	if got := scope.Stack.pop(); !got.Eq(value) {
		t.Fatalf("SLOAD value mismatch:\ngot  %x\nwant %x", got.Bytes64(), value.Bytes64())
	}
	scope.Stack.push(aliasKey)
	if _, err := opSload(&pc, env.interpreter, scope); err != nil {
		t.Fatalf("SLOAD alias failed: %v", err)
	}
	if got := scope.Stack.pop(); !got.Eq(value) {
		t.Fatalf("SLOAD alias with same low 32-byte key mismatch:\ngot  %x\nwant %x", got.Bytes64(), value.Bytes64())
	}
	if _, ok := statedb.storage[contractAddr][common.HexToHash(lowStorageKey)]; !ok {
		t.Fatalf("storage value was not stored under low 32-byte key %s", lowStorageKey)
	}

	scope.Memory.Resize(3)
	scope.Memory.Set(0, 3, []byte{0xaa, 0xbb, 0xcc})
	topic0 := vm64IntFromHex(t, "80"+strings.Repeat("0", 126))
	topic1 := vm64IntFromHex(t, strings.Repeat("f", 128))
	scope.Stack.push(topic1)
	scope.Stack.push(topic0)
	scope.Stack.push(uint512.NewInt(3))
	scope.Stack.push(uint512.NewInt(0))
	if _, err := makeLog(2)(&pc, env.interpreter, scope); err != nil {
		t.Fatalf("LOG2 failed: %v", err)
	}
	if len(statedb.logs) != 1 {
		t.Fatalf("expected one log, got %d", len(statedb.logs))
	}
	log := statedb.logs[0]
	if log.Address != contractAddr {
		t.Fatalf("log address mismatch: got %s want %s", log.Address, contractAddr)
	}
	if !bytes.Equal(log.Data, []byte{0xaa, 0xbb, 0xcc}) {
		t.Fatalf("log data mismatch: got %x", log.Data)
	}
	if got, want := log.Topics[0], common.LogTopic(topic0.Bytes64()); got != want {
		t.Fatalf("topic0 mismatch:\ngot  %x\nwant %x", got, want)
	}
	if got, want := log.Topics[1], common.LogTopic(topic1.Bytes64()); got != want {
		t.Fatalf("topic1 mismatch:\ngot  %x\nwant %x", got, want)
	}
}
