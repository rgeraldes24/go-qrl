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
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core"
	"github.com/theQRL/go-qrl/core/types"
	qrlwallet "github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
)

func TestEventEmitterCompilerAndGeneratedBindingRuntime(t *testing.T) {
	w, err := qrlwallet.Generate(qrlwallet.ML_DSA_87)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := bind.NewKeyedTransactorWithChainID(w, big.NewInt(1337))
	if err != nil {
		t.Fatal(err)
	}
	sim := backends.NewSimulatedBackend(core.GenesisAlloc{
		auth.From: {Balance: new(big.Int).Mul(big.NewInt(1_000_000), big.NewInt(1e18))},
	}, 20_000_000)
	t.Cleanup(func() { _ = sim.Close() })

	address, deployment, generated, err := DeployEventEmitterBindingSmoke(auth, sim)
	if err != nil {
		t.Fatalf("deploy EventEmitter through generated smoke binding: %v", err)
	}
	sim.Commit()
	deploymentReceipt, err := bind.WaitMined(t.Context(), sim, deployment)
	if err != nil || deploymentReceipt.Status != types.ReceiptStatusSuccessful || deploymentReceipt.ContractAddress != address {
		t.Fatalf("EventEmitter deployment receipt = %+v, %v", deploymentReceipt, err)
	}
	generated, err = NewEventEmitterBindingSmoke(address, sim)
	if err != nil {
		t.Fatalf("bind EventEmitter through generated smoke binding: %v", err)
	}
	if _, err := generated.ParseStored(types.Log{}); err == nil {
		t.Fatal("generated ParseStored accepted a log without topics")
	}
	artifact, err := loadEventEmitterArtifact()
	if err != nil {
		t.Fatal(err)
	}
	contract := bindArtifact(artifact, address, sim)
	callOpts := &bind.CallOpts{Context: t.Context()}

	amount := new(big.Int).Add(new(big.Int).Lsh(big.NewInt(1), 511), big.NewInt(0x1234))
	primary := dynamicRecord{
		Amount: amount, Note: "generated nested tuple " + string(bytes.Repeat([]byte{'n'}, 65)),
		Payload: bytes.Repeat([]byte{0xa5}, 129), Values: [][]uint16{{}, {1, 0xffff}},
	}
	records := []dynamicRecord{{Amount: big.NewInt(7)}, primary}
	cube := [][][]uint16{{}, {{}}, {{1, 2}, {}, {0xffff}}}
	assertNested := func(label string, record dynamicRecord, gotRecords []dynamicRecord, gotCube [][][]uint16, callErr error) {
		t.Helper()
		if callErr != nil {
			t.Fatalf("%s: %v", label, callErr)
		}
		if diff := cmp.Diff(
			struct {
				Record  dynamicRecord
				Records []dynamicRecord
				Cube    [][][]uint16
			}{primary, records, cube},
			struct {
				Record  dynamicRecord
				Records []dynamicRecord
				Cube    [][][]uint16
			}{record, gotRecords, gotCube},
			abiCompareOptions...,
		); diff != "" {
			t.Fatalf("%s mismatch (-want +have):\n%s", label, diff)
		}
	}
	gotRecord, gotRecords, gotCube, callErr := generated.EchoNested(callOpts, primary, records, cube)
	assertNested("generated EchoNested", gotRecord, gotRecords, gotCube, callErr)
	session := EventEmitterBindingSmokeSession{Contract: generated, CallOpts: *callOpts}
	gotRecord, gotRecords, gotCube, callErr = session.EchoNested(primary, records, cube)
	assertNested("generated session EchoNested", gotRecord, gotRecords, gotCube, callErr)

	var rawOutput []any
	raw := EventEmitterBindingSmokeRaw{Contract: generated}
	if err := raw.Call(callOpts, &rawOutput, "echoNested", primary, records, cube); err != nil {
		t.Fatalf("generated raw EchoNested: %v", err)
	}
	haveRaw, err := artifact.ABI.Methods["echoNested"].Outputs.Pack(rawOutput...)
	if err != nil {
		t.Fatalf("repack generated raw EchoNested: %v", err)
	}
	wantRaw, err := artifact.ABI.Methods["echoNested"].Outputs.Pack(primary, records, cube)
	if err != nil || !bytes.Equal(haveRaw, wantRaw) {
		t.Fatalf("generated raw EchoNested output\nhave %x\nwant %x\nerror %v", haveRaw, wantRaw, err)
	}
	var tag [64]byte
	for index := range tag {
		tag[index] = byte(0x80 + index)
	}
	if err := checkHyperionCompilerCalls(t.Context(), sim, auth.From, address, contract, callOpts, amount, tag); err != nil {
		t.Fatal(err)
	}

	delta := new(big.Int).Add(new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 510)), big.NewInt(42))
	payload := bytes.Repeat([]byte{0x6c}, 129)
	note := "generated Stored event crosses one VM64 word " + string(bytes.Repeat([]byte{'s'}, 65))
	watchSink := make(chan *EventEmitterBindingSmokeStored, 1)
	watch, err := generated.WatchStored(&bind.WatchOpts{Context: t.Context()}, watchSink, nil, nil, nil)
	if err != nil {
		t.Fatalf("watch generated Stored event: %v", err)
	}
	defer watch.Unsubscribe()
	storeTx, err := generated.Store(auth, amount, delta, tag, auth.From, payload, note, true)
	if err != nil {
		t.Fatalf("store through generated smoke binding: %v", err)
	}
	sim.Commit()
	storeReceipt, err := bind.WaitMined(t.Context(), sim, storeTx)
	if err != nil || storeReceipt.Status != types.ReceiptStatusSuccessful || len(storeReceipt.Logs) != 4 {
		t.Fatalf("generated Store receipt = %+v, %v", storeReceipt, err)
	}
	checkStored := func(label string, event *EventEmitterBindingSmokeStored) {
		t.Helper()
		if event == nil || event.Raw.TxHash != storeTx.Hash() || event.Recipient != auth.From ||
			event.Amount.Cmp(amount) != 0 || event.Delta.Cmp(delta) != 0 || event.Tag != tag ||
			!bytes.Equal(event.Payload, payload) || event.Note != note || !event.Enabled {
			t.Fatalf("%s = %+v", label, event)
		}
	}
	parsedStored, err := generated.ParseStored(*storeReceipt.Logs[0])
	if err != nil {
		t.Fatalf("parse generated Stored event: %v", err)
	}
	checkStored("parsed generated Stored event", parsedStored)
	select {
	case watched := <-watchSink:
		checkStored("watched generated Stored event", watched)
	case err := <-watch.Err():
		t.Fatalf("watch generated Stored event: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for generated Stored event")
	}
	end := storeReceipt.BlockNumber.Uint64()
	iterator, err := generated.FilterStored(
		&bind.FilterOpts{Start: end, End: &end, Context: t.Context()},
		[]common.Address{auth.From},
		[]*big.Int{amount},
		[]*big.Int{delta},
	)
	if err != nil {
		t.Fatalf("filter generated Stored event: %v", err)
	}
	defer iterator.Close()
	if !iterator.Next() {
		t.Fatalf("generated Stored filter returned no event: %v", iterator.Error())
	}
	checkStored("filtered generated Stored event", iterator.Event)
	if extra := iterator.Next(); extra || iterator.Error() != nil {
		t.Fatalf("generated Stored filter tail: next=%t error=%v", extra, iterator.Error())
	}
	if err := checkVM64EventLogs(t.Context(), contract, storeReceipt, auth.From, amount, delta, tag, payload, note); err != nil {
		t.Fatalf("validate full EventEmitter logs: %v", err)
	}
	if err := checkContractCall(
		t.Context(),
		sim,
		auth.From,
		address,
		storeReceipt.BlockNumber,
		contract,
		&artifact.ABI,
		contractCall{Method: "read", Want: []any{amount, delta, tag, auth.From, payload, note, true}},
	); err != nil {
		t.Fatalf("read stored EventEmitter state: %v", err)
	}

	assertPayable := func(label string, tx *types.Transaction, callErr error, data []byte, value int64) {
		t.Helper()
		if callErr != nil {
			t.Fatalf("%s: %v", label, callErr)
		}
		if !bytes.Equal(tx.Data(), data) || tx.Value().Cmp(big.NewInt(value)) != 0 {
			t.Fatalf("%s transaction data=%x value=%s", label, tx.Data(), tx.Value())
		}
		sim.Commit()
		receipt, err := bind.WaitMined(t.Context(), sim, tx)
		if err != nil || receipt.Status != types.ReceiptStatusSuccessful {
			t.Fatalf("%s receipt=%+v error=%v", label, receipt, err)
		}
	}
	receiveOpts := *auth
	receiveOpts.Value = big.NewInt(11)
	receiveOpts.GasLimit = 1_000_000
	session.TransactOpts = receiveOpts
	tx, callErr := session.Receive()
	assertPayable("generated session Receive", tx, callErr, nil, 11)
	fallbackData := bytes.Repeat([]byte{0x5a}, 65)
	fallbackOpts := *auth
	fallbackOpts.Value = big.NewInt(13)
	fallbackOpts.GasLimit = 1_000_000
	session.TransactOpts = fallbackOpts
	tx, callErr = session.Fallback(fallbackData)
	assertPayable("generated session Fallback", tx, callErr, fallbackData, 13)
	balance, err := sim.BalanceAt(t.Context(), address, nil)
	if err != nil || balance.Cmp(big.NewInt(24)) != 0 {
		t.Fatalf("EventEmitter payable entrypoint balance = %s, %v; want 24", balance, err)
	}
}
