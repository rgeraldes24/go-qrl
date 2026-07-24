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
	"math/big"
	"strings"
	"testing"

	qrl "github.com/theQRL/go-qrl"
	qrlabi "github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	qrlwallet "github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
)

const (
	specialFallbackMarker = byte(0xfa)
	specialReceiveMarker  = byte(0xce)
	specialRecordWords    = 4
)

type specialEntrypointFixture struct {
	fallback        bool
	receive         bool
	fallbackPayable bool
}

func TestABISpecialEntrypointRoutingRuntime(t *testing.T) {
	t.Run("fallback-only", func(t *testing.T) {
		auth, sim := newSpecialEntrypointHarness(t)
		address, contract := deploySpecialEntrypointFixture(t, auth, sim,
			`[{"stateMutability":"payable","type":"fallback"}]`,
			specialEntrypointFixture{fallback: true, fallbackPayable: true},
		)
		wantBalance := new(big.Int)
		exerciseSuccessfulSpecialEntrypoint(t, auth, sim, address, contract, nil, new(big.Int), specialFallbackMarker, wantBalance)
		wantBalance.Add(wantBalance, big.NewInt(37))
		exerciseSuccessfulSpecialEntrypoint(t, auth, sim, address, contract, nil, big.NewInt(37), specialFallbackMarker, wantBalance)
	})

	t.Run("receive-only", func(t *testing.T) {
		auth, sim := newSpecialEntrypointHarness(t)
		address, contract := deploySpecialEntrypointFixture(t, auth, sim,
			`[{"stateMutability":"payable","type":"receive"}]`,
			specialEntrypointFixture{receive: true},
		)
		wantBalance := new(big.Int)
		exerciseSuccessfulSpecialEntrypoint(t, auth, sim, address, contract, nil, new(big.Int), specialReceiveMarker, wantBalance)
		wantBalance.Add(wantBalance, big.NewInt(41))
		exerciseSuccessfulSpecialEntrypoint(t, auth, sim, address, contract, nil, big.NewInt(41), specialReceiveMarker, wantBalance)
		exerciseRejectedSpecialEntrypoint(t, auth, sim, address, contract, []byte{0x01}, new(big.Int), wantBalance)
	})

	t.Run("receive-and-fallback", func(t *testing.T) {
		auth, sim := newSpecialEntrypointHarness(t)
		address, contract := deploySpecialEntrypointFixture(t, auth, sim,
			`[{"stateMutability":"payable","type":"fallback"},{"stateMutability":"payable","type":"receive"}]`,
			specialEntrypointFixture{fallback: true, receive: true, fallbackPayable: true},
		)
		wantBalance := new(big.Int)
		exerciseSuccessfulSpecialEntrypoint(t, auth, sim, address, contract, nil, new(big.Int), specialReceiveMarker, wantBalance)
		wantBalance.Add(wantBalance, big.NewInt(43))
		exerciseSuccessfulSpecialEntrypoint(t, auth, sim, address, contract, nil, big.NewInt(43), specialReceiveMarker, wantBalance)
		calldata := bytes.Repeat([]byte{0xa5}, vm.WordBytes+1)
		exerciseSuccessfulSpecialEntrypoint(t, auth, sim, address, contract, calldata, new(big.Int), specialFallbackMarker, wantBalance)
		wantBalance.Add(wantBalance, big.NewInt(47))
		exerciseSuccessfulSpecialEntrypoint(t, auth, sim, address, contract, calldata, big.NewInt(47), specialFallbackMarker, wantBalance)
	})

	t.Run("nonpayable-fallback-rejects-value", func(t *testing.T) {
		auth, sim := newSpecialEntrypointHarness(t)
		address, contract := deploySpecialEntrypointFixture(t, auth, sim,
			`[{"stateMutability":"nonpayable","type":"fallback"}]`,
			specialEntrypointFixture{fallback: true},
		)
		wantBalance := new(big.Int)
		exerciseSuccessfulSpecialEntrypoint(t, auth, sim, address, contract, []byte{0xde, 0xad, 0xbe, 0xef}, new(big.Int), specialFallbackMarker, wantBalance)
		exerciseRejectedSpecialEntrypoint(t, auth, sim, address, contract, nil, big.NewInt(53), wantBalance)
		exerciseRejectedSpecialEntrypoint(t, auth, sim, address, contract, []byte{0x01, 0x02, 0x03}, big.NewInt(59), wantBalance)
	})
}

func newSpecialEntrypointHarness(t *testing.T) (*bind.TransactOpts, *backends.SimulatedBackend) {
	t.Helper()
	wallet, err := qrlwallet.Generate(qrlwallet.ML_DSA_87)
	if err != nil {
		t.Fatalf("generate special-entrypoint wallet: %v", err)
	}
	auth, err := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
	if err != nil {
		t.Fatalf("create special-entrypoint transactor: %v", err)
	}
	sim := backends.NewSimulatedBackend(core.GenesisAlloc{
		auth.From: {Balance: new(big.Int).Mul(big.NewInt(1_000_000), big.NewInt(1e18))},
	}, 10_000_000)
	t.Cleanup(func() { _ = sim.Close() })
	return auth, sim
}

func deploySpecialEntrypointFixture(
	t *testing.T,
	auth *bind.TransactOpts,
	sim *backends.SimulatedBackend,
	abiJSON string,
	fixture specialEntrypointFixture,
) (common.Address, *bind.BoundContract) {
	t.Helper()
	parsed, err := qrlabi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		t.Fatalf("parse special-entrypoint ABI: %v", err)
	}
	if parsed.HasFallback() != fixture.fallback || parsed.HasReceive() != fixture.receive {
		t.Fatalf("special-entrypoint ABI has fallback=%t receive=%t, want fallback=%t receive=%t",
			parsed.HasFallback(), parsed.HasReceive(), fixture.fallback, fixture.receive)
	}
	if fixture.fallback && parsed.Fallback.IsPayable() != fixture.fallbackPayable {
		t.Fatalf("fallback payable=%t, want %t", parsed.Fallback.IsPayable(), fixture.fallbackPayable)
	}
	if fixture.receive && !parsed.Receive.IsPayable() {
		t.Fatal("receive entrypoint is not payable")
	}

	address, tx, contract, err := bind.DeployContract(auth, parsed, deploymentBytecode(specialEntrypointRuntime(fixture)), sim)
	if err != nil {
		t.Fatalf("deploy special-entrypoint fixture: %v", err)
	}
	sim.Commit()
	receipt, err := bind.WaitMined(t.Context(), sim, tx)
	if err != nil {
		t.Fatalf("wait for special-entrypoint fixture deployment: %v", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful || receipt.ContractAddress != address {
		t.Fatalf("special-entrypoint deployment receipt = %+v, want successful contract %s", receipt, address)
	}
	return address, contract
}

func specialEntrypointRuntime(fixture specialEntrypointFixture) []byte {
	a := newBytecodeAssembly()
	switch {
	case fixture.fallback && fixture.receive:
		a.op(vm.CALLDATASIZE)
		a.op(vm.ISZERO)
		a.jump("receive", true)
		a.jump("fallback", false)
	case fixture.receive:
		a.op(vm.CALLDATASIZE)
		a.op(vm.ISZERO)
		a.jump("receive", true)
		appendEmptyRevert(a)
	case fixture.fallback:
		a.jump("fallback", false)
	default:
		appendEmptyRevert(a)
	}

	if fixture.fallback {
		a.markJump("fallback")
		if !fixture.fallbackPayable {
			a.op(vm.CALLVALUE)
			a.op(vm.ISZERO)
			a.jump("fallback-no-value", true)
			appendEmptyRevert(a)
			a.markJump("fallback-no-value")
		}
		appendSpecialEntrypointRecord(a, specialFallbackMarker)
	}
	if fixture.receive {
		a.markJump("receive")
		appendSpecialEntrypointRecord(a, specialReceiveMarker)
	}
	return a.bytes()
}

func appendEmptyRevert(a *bytecodeAssembly) {
	a.pushUint(0)
	a.pushUint(0)
	a.op(vm.REVERT)
}

func appendSpecialEntrypointRecord(a *bytecodeAssembly, marker byte) {
	// Persist the selected route and its inputs in four full VM64 storage words.
	a.pushUint(uint64(marker))
	a.pushUint(0)
	a.op(vm.SSTORE)
	a.op(vm.CALLDATASIZE)
	a.pushUint(1)
	a.op(vm.SSTORE)
	a.op(vm.CALLVALUE)
	a.pushUint(2)
	a.op(vm.SSTORE)
	a.pushUint(0)
	a.op(vm.CALLDATALOAD)
	a.pushUint(3)
	a.op(vm.SSTORE)

	// Return and log the same four-word record so calls, receipts, and state can
	// independently prove which special entrypoint actually executed.
	a.pushUint(uint64(marker))
	a.pushUint(0)
	a.op(vm.MSTORE)
	a.op(vm.CALLDATASIZE)
	a.pushUint(vm.WordBytes)
	a.op(vm.MSTORE)
	a.op(vm.CALLVALUE)
	a.pushUint(2 * vm.WordBytes)
	a.op(vm.MSTORE)
	a.pushUint(0)
	a.op(vm.CALLDATALOAD)
	a.pushUint(3 * vm.WordBytes)
	a.op(vm.MSTORE)
	a.pushUint(uint64(marker))
	a.pushUint(specialRecordWords * vm.WordBytes)
	a.pushUint(0)
	a.op(vm.LOG1)
	a.pushUint(specialRecordWords * vm.WordBytes)
	a.pushUint(0)
	a.op(vm.RETURN)
}

func exerciseSuccessfulSpecialEntrypoint(
	t *testing.T,
	auth *bind.TransactOpts,
	sim *backends.SimulatedBackend,
	address common.Address,
	contract *bind.BoundContract,
	calldata []byte,
	value *big.Int,
	marker byte,
	wantBalance *big.Int,
) {
	t.Helper()
	wantRecord := specialEntrypointRecord(marker, calldata, value)
	output, err := sim.CallContract(t.Context(), qrl.CallMsg{
		From:  auth.From,
		To:    &address,
		Value: new(big.Int).Set(value),
		Data:  bytes.Clone(calldata),
	}, nil)
	if err != nil {
		t.Fatalf("call special entrypoint marker %#x, calldata %x, value %s: %v", marker, calldata, value, err)
	}
	if !bytes.Equal(output, wantRecord) {
		t.Fatalf("special-entrypoint call output\nhave %x\nwant %x", output, wantRecord)
	}

	txOpts := *auth
	txOpts.Value = new(big.Int).Set(value)
	txOpts.GasLimit = 1_000_000
	var tx *types.Transaction
	if len(calldata) == 0 {
		tx, err = contract.Transfer(&txOpts)
	} else {
		tx, err = contract.RawTransact(&txOpts, calldata)
	}
	if err != nil {
		t.Fatalf("transact special entrypoint marker %#x, calldata %x, value %s: %v", marker, calldata, value, err)
	}
	if !bytes.Equal(tx.Data(), calldata) || tx.Value().Cmp(value) != 0 {
		t.Fatalf("special-entrypoint transaction data=%x value=%s, want data=%x value=%s", tx.Data(), tx.Value(), calldata, value)
	}
	sim.Commit()
	receipt, err := bind.WaitMined(t.Context(), sim, tx)
	if err != nil {
		t.Fatalf("wait for special-entrypoint transaction: %v", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful || len(receipt.Logs) != 1 {
		t.Fatalf("special-entrypoint receipt status=%d logs=%d, want successful receipt with one log", receipt.Status, len(receipt.Logs))
	}
	log := receipt.Logs[0]
	var wantTopic common.LogTopic
	wantTopic[len(wantTopic)-1] = marker
	if log.Address != address || len(log.Topics) != 1 || log.Topics[0] != wantTopic || !bytes.Equal(log.Data, wantRecord) {
		t.Fatalf("special-entrypoint log = %+v, want address=%s topic=%s data=%x", log, address, wantTopic, wantRecord)
	}
	assertSpecialEntrypointStorage(t, sim, address, receipt.BlockNumber, wantRecord)
	assertSpecialEntrypointBalance(t, sim, address, receipt.BlockNumber, wantBalance)
}

func exerciseRejectedSpecialEntrypoint(
	t *testing.T,
	auth *bind.TransactOpts,
	sim *backends.SimulatedBackend,
	address common.Address,
	contract *bind.BoundContract,
	calldata []byte,
	value *big.Int,
	wantBalance *big.Int,
) {
	t.Helper()
	before := readSpecialEntrypointStorage(t, sim, address, nil)
	if output, err := sim.CallContract(t.Context(), qrl.CallMsg{
		From:  auth.From,
		To:    &address,
		Value: new(big.Int).Set(value),
		Data:  bytes.Clone(calldata),
	}, nil); err == nil {
		t.Fatalf("rejected special entrypoint returned output %x for calldata %x, value %s", output, calldata, value)
	}

	txOpts := *auth
	txOpts.Value = new(big.Int).Set(value)
	// An explicit limit bypasses estimation so the reverted transaction is
	// mined and its rollback can be inspected.
	txOpts.GasLimit = 1_000_000
	var (
		tx  *types.Transaction
		err error
	)
	if len(calldata) == 0 {
		tx, err = contract.Transfer(&txOpts)
	} else {
		tx, err = contract.RawTransact(&txOpts, calldata)
	}
	if err != nil {
		t.Fatalf("submit rejected special-entrypoint transaction: %v", err)
	}
	sim.Commit()
	receipt, err := bind.WaitMined(t.Context(), sim, tx)
	if err != nil {
		t.Fatalf("wait for rejected special-entrypoint transaction: %v", err)
	}
	if receipt.Status != types.ReceiptStatusFailed || len(receipt.Logs) != 0 {
		t.Fatalf("rejected special-entrypoint receipt status=%d logs=%d, want failed receipt without logs", receipt.Status, len(receipt.Logs))
	}
	after := readSpecialEntrypointStorage(t, sim, address, receipt.BlockNumber)
	if !bytes.Equal(after, before) {
		t.Fatalf("rejected special entrypoint changed storage\nbefore %x\nafter  %x", before, after)
	}
	assertSpecialEntrypointBalance(t, sim, address, receipt.BlockNumber, wantBalance)
}

func specialEntrypointRecord(marker byte, calldata []byte, value *big.Int) []byte {
	record := make([]byte, specialRecordWords*vm.WordBytes)
	record[vm.WordBytes-1] = marker
	new(big.Int).SetUint64(uint64(len(calldata))).FillBytes(record[vm.WordBytes : 2*vm.WordBytes])
	value.FillBytes(record[2*vm.WordBytes : 3*vm.WordBytes])
	copy(record[3*vm.WordBytes:], calldata)
	return record
}

func assertSpecialEntrypointStorage(
	t *testing.T,
	sim *backends.SimulatedBackend,
	address common.Address,
	blockNumber *big.Int,
	want []byte,
) {
	t.Helper()
	have := readSpecialEntrypointStorage(t, sim, address, blockNumber)
	if !bytes.Equal(have, want) {
		t.Fatalf("special-entrypoint storage\nhave %x\nwant %x", have, want)
	}
}

func readSpecialEntrypointStorage(t *testing.T, sim *backends.SimulatedBackend, address common.Address, blockNumber *big.Int) []byte {
	t.Helper()
	storage := make([]byte, 0, specialRecordWords*vm.WordBytes)
	for slot := range specialRecordWords {
		value, err := sim.StorageAt(t.Context(), address, common.BigToHash(big.NewInt(int64(slot))), blockNumber)
		if err != nil {
			t.Fatalf("read special-entrypoint storage slot %d: %v", slot, err)
		}
		storage = append(storage, common.LeftPadBytes(value, vm.WordBytes)...)
	}
	return storage
}

func assertSpecialEntrypointBalance(
	t *testing.T,
	sim *backends.SimulatedBackend,
	address common.Address,
	blockNumber *big.Int,
	want *big.Int,
) {
	t.Helper()
	balance, err := sim.BalanceAt(t.Context(), address, blockNumber)
	if err != nil {
		t.Fatalf("read special-entrypoint balance: %v", err)
	}
	if balance.Cmp(want) != 0 {
		t.Fatalf("special-entrypoint balance=%s, want %s", balance, want)
	}
}
