// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package deposit

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
)

func TestSubmittedDepositIsRecordedBeforeReceiptWait(t *testing.T) {
	var events []string
	wantReceipt := &types.Receipt{Status: types.ReceiptStatusSuccessful}
	recorder := TransactionRecorderFunc(func(label, hash string) error {
		events = append(events, "record:"+label+":"+hash)
		return nil
	})
	got, err := recordAndWaitForDeposit(t.Context(), recorder, "deposit-0", "0x1234", func(context.Context) (*types.Receipt, error) {
		events = append(events, "wait")
		return wantReceipt, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != wantReceipt {
		t.Fatalf("receipt = %p, want %p", got, wantReceipt)
	}
	wantEvents := []string{"record:deposit-0:0x1234", "wait"}
	if !slices.Equal(events, wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}
}

func TestSubmittedDepositDoesNotWaitWhenDurableRecordFails(t *testing.T) {
	waited := false
	recorder := TransactionRecorderFunc(func(string, string) error {
		return errors.New("checkpoint unavailable")
	})
	_, err := recordAndWaitForDeposit(t.Context(), recorder, "deposit-1", "0xabcd", func(context.Context) (*types.Receipt, error) {
		waited = true
		return nil, nil
	})
	if err == nil || !strings.Contains(err.Error(), "transaction 0xabcd as deposit-1 was submitted but could not be recorded") {
		t.Fatalf("recording error = %v", err)
	}
	if waited {
		t.Fatal("receipt wait started after durable transaction recording failed")
	}
}

func TestDepositTreeRootVectors(t *testing.T) {
	empty, err := depositTreeRoot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if empty != [32]byte(emptyDepositRoot) {
		t.Fatalf("empty root = 0x%x, want %s", empty, emptyDepositRoot.Hex())
	}
	leaves := make([][32]byte, depositValidatorCount)
	for i := range leaves {
		for j := range leaves[i] {
			leaves[i][j] = byte(0x11 * (i + 1))
		}
	}
	wants := []common.Hash{
		common.HexToHash("0xbf2a632805e4c66893c728ce0665ef41d69d810dd15400501dd2a1d62d64ba4a"),
		common.HexToHash("0xe11e696a490d2fbb7982a46d120db4419d66d5063878a540cad939600a40e6da"),
		common.HexToHash("0x75bbd8cf2ac0c02a478f61ca1c946c2da8600decbb5689f132c3532d384c160f"),
	}
	for count, want := range wants {
		got, err := depositTreeRoot(leaves[:count+1])
		if err != nil {
			t.Fatal(err)
		}
		if got != [32]byte(want) {
			t.Fatalf("%d-leaf root = 0x%x, want %s", count+1, got, want.Hex())
		}
	}
	branch := depositTreeBranch(leaves)
	if branch[0] != leaves[2] {
		t.Fatalf("three-leaf branch[0] = 0x%x, want third leaf", branch[0])
	}
	wantCarry := common.HexToHash("0x5189c77d29fe5d546a045ec46986852785fea5c13ac7da9c115ff5fb6edf817c")
	if branch[1] != [32]byte(wantCarry) {
		t.Fatalf("three-leaf branch[1] = 0x%x, want %s", branch[1], wantCarry.Hex())
	}
	packed := packedDepositBranchWord(branch, 0)
	if !bytes.Equal(packed[:32], wantCarry[:]) || !bytes.Equal(packed[32:], leaves[2][:]) {
		t.Fatalf("packed branch slot 0 = 0x%x, want odd carry || even third leaf", packed)
	}
}

func TestDepositABIHandlesVM64EventAndCalldata(t *testing.T) {
	parsed, err := parseDepositABI()
	if err != nil {
		t.Fatal(err)
	}
	event := parsed.Events["DepositEvent"]
	wantEventID := common.HexToHash("0x649bbc62d0e31342afea4e5cd82d4049e7e1ee912fc0889aa790803be39038c5")
	if event.ID != wantEventID {
		t.Fatalf("DepositEvent ID = %s, want %s", event.ID.Hex(), wantEventID.Hex())
	}
	pubkey := bytes.Repeat([]byte{0x11}, publicKeyLength)
	withdrawal := common.MustParseAddress(defaultWithdrawal).Bytes()
	signature := bytes.Repeat([]byte{0x33}, signatureLength)
	amount := make([]byte, 8)
	binary.LittleEndian.PutUint64(amount, depositAmountShor)
	index := make([]byte, 8)
	eventData, err := event.Inputs.NonIndexed().Pack(pubkey, withdrawal, amount, signature, index)
	if err != nil {
		t.Fatal(err)
	}
	contract := bind.NewBoundContract(common.Address{}, parsed, nil, nil, nil)
	var decoded depositContractEvent
	if err := contract.UnpackLog(&decoded, "DepositEvent", types.Log{
		Topics: []common.LogTopic{common.HashToLogTopic(event.ID)},
		Data:   eventData,
	}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(decoded.WithdrawalCredentials, withdrawal) || len(decoded.WithdrawalCredentials) != common.AddressLength {
		t.Fatalf("decoded withdrawal credentials = %x", decoded.WithdrawalCredentials)
	}

	var root [32]byte
	calldata, err := parsed.Pack("deposit", pubkey, withdrawal, signature, root)
	if err != nil {
		t.Fatal(err)
	}
	values, err := parsed.Methods["deposit"].Inputs.Unpack(calldata[4:])
	if err != nil {
		t.Fatal(err)
	}
	if got := values[1].([]byte); !bytes.Equal(got, withdrawal) || len(got) != common.AddressLength {
		t.Fatalf("ABI calldata withdrawal credentials = %x", got)
	}
}
