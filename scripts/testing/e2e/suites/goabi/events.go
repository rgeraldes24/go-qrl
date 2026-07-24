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

	"github.com/google/go-cmp/cmp"
	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	qrlmath "github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

type eventDeployment struct {
	address common.Address
	tx      *types.Transaction
	receipt *types.Receipt
	event   abi.Event
	topic   common.LogTopic
	binding *bind.BoundContract
	smoke   *EventEmitterBindingSmoke
}

func checkLiveEventRoundTrip(
	ctx context.Context,
	client *qrlclient.Client,
	w wallet.Wallet,
	from common.Address,
	graphqlURL string,
) error {
	artifact, err := loadEventEmitterArtifact()
	if err != nil {
		return err
	}
	deployment, err := deployEventEmitter(ctx, client, w, from)
	if err != nil {
		return err
	}
	receipt := deployment.receipt
	parsed := &artifact.ABI
	log := receipt.Logs[0]
	wantDeploymentData, err := deployment.event.Inputs.NonIndexed().Pack(big.NewInt(1337))
	if err != nil {
		return fmt.Errorf("pack canonical deployment log data: %w", err)
	}
	if !bytes.Equal(log.Data, wantDeploymentData) {
		return fmt.Errorf("compiler deployment log data is non-canonical:\nhave %x\nwant %x", log.Data, wantDeploymentData)
	}
	values, err := parsed.Unpack("Deployed", log.Data)
	if err != nil {
		return fmt.Errorf("decode deployment log data: %w", err)
	}
	if len(values) != 1 || values[0].(*big.Int).Cmp(big.NewInt(1337)) != 0 {
		return fmt.Errorf("decoded deployment value mismatch: %v", values)
	}

	parsedEvent, err := deployment.smoke.ParseDeployed(*log)
	if err != nil {
		return fmt.Errorf("parse deployment through generated binding: %w", err)
	}
	if parsedEvent.Value.Cmp(big.NewInt(1337)) != 0 || parsedEvent.Raw.TxHash != receipt.TxHash {
		return fmt.Errorf("generated binding parsed event mismatch: %+v", parsedEvent)
	}
	opts, err := receiptBlockRange(ctx, receipt)
	if err != nil {
		return err
	}
	iterator, err := deployment.smoke.FilterDeployed(opts)
	if err != nil {
		return fmt.Errorf("filter deployment through generated binding: %w", err)
	}
	defer iterator.Close()
	if !iterator.Next() {
		return fmt.Errorf("generated deployment filter returned no event: %v", iterator.Error())
	}
	if iterator.Event.Value.Cmp(big.NewInt(1337)) != 0 || iterator.Event.Raw.TxHash != receipt.TxHash {
		return fmt.Errorf("generated deployment filter mismatch: %+v", iterator.Event)
	}
	if iterator.Next() || iterator.Error() != nil {
		return fmt.Errorf("generated deployment filter returned an unexpected tail: %v", iterator.Error())
	}

	topics, err := abi.MakeTopics([]any{deployment.event.ID})
	if err != nil {
		return fmt.Errorf("make event filter topic: %w", err)
	}
	logs, err := client.FilterLogs(ctx, qrl.FilterQuery{
		FromBlock: receipt.BlockNumber,
		ToBlock:   receipt.BlockNumber,
		Addresses: []common.Address{receipt.ContractAddress},
		Topics:    topics,
	})
	if err != nil {
		return fmt.Errorf("filter deployment logs: %w", err)
	}
	if len(logs) != 1 || logs[0].TxHash != receipt.TxHash || logs[0].Topics[0] != deployment.topic {
		return fmt.Errorf("filtered log mismatch: %+v", logs)
	}
	if err := checkGraphQLEventLog(ctx, graphqlURL, deployment); err != nil {
		return err
	}
	if err := checkLiveVM64Contract(ctx, client, w, from, deployment); err != nil {
		return err
	}
	return checkLiveAdvancedABI(ctx, client, w, from)
}

func checkLiveVM64Contract(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address, deployment *eventDeployment) error {
	artifact, err := loadEventEmitterArtifact()
	if err != nil {
		return err
	}
	amount := new(big.Int).Lsh(big.NewInt(1), 511)
	amount.Add(amount, big.NewInt(0x1234))
	delta := new(big.Int).Lsh(big.NewInt(1), 510)
	delta.Neg(delta)
	delta.Add(delta, big.NewInt(42))

	var tag [64]byte
	for i := range tag {
		tag[i] = byte(0x80 + i)
	}
	payload := make([]byte, 129)
	for i := range payload {
		payload[i] = byte((i*29 + 7) & 0xff)
	}
	note := "VM64 string crosses the 64-byte ABI word boundary: 0123456789abcdef0123456789abcdef"
	callOpts := &bind.CallOpts{Context: ctx, BlockNumber: deployment.receipt.BlockNumber}

	value1 := [1]byte{0xa5}
	var value32 [32]byte
	var value33 [33]byte
	for i := range value32 {
		value32[i] = byte(i + 1)
	}
	for i := range value33 {
		value33[i] = byte(0x40 + i)
	}
	arrayValues := []*big.Int{big.NewInt(0), big.NewInt(1), amount}
	arrayTags := [2][64]byte{tag, {}}
	copy(arrayTags[1][:], payload[:64])
	record := eventRecord{Amount: amount, Recipient: from, Tag: tag}
	for _, vector := range []contractCall{
		{
			Method: "echo",
			Args:   []any{amount, delta, tag, from, payload, note, true},
			Want:   []any{amount, delta, tag, from, payload, note, true},
		},
		{
			Method: "echoFixed",
			Args:   []any{value1, value32, value33, tag},
			Want:   []any{value1, value32, value33, tag},
		},
		{Method: "echoArrays", Args: []any{arrayValues, arrayTags}, Want: []any{arrayValues, arrayTags}},
		{Method: "echoRecord", Args: []any{record}, Want: []any{record}},
	} {
		if err := checkContractCall(ctx, client, from, deployment.address, callOpts.BlockNumber, deployment.binding, &artifact.ABI, vector); err != nil {
			return fmt.Errorf("VM64 live call: %w", err)
		}
	}
	if err := checkHyperionCompilerCalls(
		ctx,
		client,
		from,
		deployment.address,
		deployment.binding,
		callOpts,
		amount,
		tag,
	); err != nil {
		return err
	}
	nested := dynamicRecord{Amount: amount, Note: note, Payload: payload, Values: [][]uint16{{1, 2}, {}, {3}}}
	nestedRecords := []dynamicRecord{nested, {Amount: big.NewInt(0), Note: "", Payload: []byte{}, Values: [][]uint16{}}}
	nestedCube := [][][]uint16{{{1}, {2, 3}}, {}, {{4}}}
	gotNested, gotRecords, gotCube, err := deployment.smoke.EchoNested(callOpts, nested, nestedRecords, nestedCube)
	if err != nil {
		return fmt.Errorf("live echoNested through generated binding: %w", err)
	}
	if diff := cmp.Diff(
		[]any{nested, nestedRecords, nestedCube},
		[]any{gotNested, gotRecords, gotCube},
		abiCompareOptions...,
	); diff != "" {
		return fmt.Errorf("generated live echoNested mismatch (-want +have):\n%s", diff)
	}
	session := EventEmitterBindingSmokeSession{Contract: deployment.smoke, CallOpts: *callOpts}
	sessionNested, sessionRecords, sessionCube, err := session.EchoNested(nested, nestedRecords, nestedCube)
	if err != nil {
		return fmt.Errorf("live echoNested through generated session: %w", err)
	}
	if diff := cmp.Diff(
		[]any{nested, nestedRecords, nestedCube},
		[]any{sessionNested, sessionRecords, sessionCube},
		abiCompareOptions...,
	); diff != "" {
		return fmt.Errorf("generated live session mismatch (-want +have):\n%s", diff)
	}
	var rawOutput []any
	if err := (&EventEmitterBindingSmokeRaw{Contract: deployment.smoke}).Call(
		callOpts,
		&rawOutput,
		"echoNested",
		nested,
		nestedRecords,
		nestedCube,
	); err != nil {
		return fmt.Errorf("live echoNested through generated raw binding: %w", err)
	}
	rawPacked, err := artifact.ABI.Methods["echoNested"].Outputs.Pack(rawOutput...)
	if err != nil {
		return fmt.Errorf("pack generated raw echoNested output: %w", err)
	}
	wantPacked, err := artifact.ABI.Methods["echoNested"].Outputs.Pack(nested, nestedRecords, nestedCube)
	if err != nil {
		return fmt.Errorf("pack expected raw echoNested output: %w", err)
	}
	if !bytes.Equal(rawPacked, wantPacked) {
		return fmt.Errorf("generated raw echoNested output mismatch")
	}

	parsedABI := &artifact.ABI
	auth, err := newTransactor(ctx, client, w, from)
	if err != nil {
		return err
	}
	storeTx, err := deployment.smoke.Store(auth, amount, delta, tag, from, payload, note, true)
	if err != nil {
		return fmt.Errorf("VM64 store through generated smoke binding: %w", err)
	}
	receipt, err := waitTransaction(ctx, client, storeTx, types.ReceiptStatusSuccessful)
	if err != nil {
		return err
	}
	if len(receipt.Logs) != 4 {
		return fmt.Errorf("VM64 store emitted %d logs, want 4", len(receipt.Logs))
	}
	generatedStored, err := deployment.smoke.ParseStored(*receipt.Logs[0])
	if err != nil {
		return fmt.Errorf("parse live Stored event through generated binding: %w", err)
	}
	if generatedStored.Recipient != from || generatedStored.Amount.Cmp(amount) != 0 ||
		generatedStored.Delta.Cmp(delta) != 0 || generatedStored.Tag != tag ||
		!bytes.Equal(generatedStored.Payload, payload) || generatedStored.Note != note ||
		!generatedStored.Enabled || generatedStored.Raw.TxHash != receipt.TxHash {
		return fmt.Errorf("generated live Stored event mismatch: %+v", generatedStored)
	}
	filterOpts, err := receiptBlockRange(ctx, receipt)
	if err != nil {
		return err
	}
	storedIterator, err := deployment.smoke.FilterStored(
		filterOpts,
		[]common.Address{from},
		[]*big.Int{amount},
		[]*big.Int{delta},
	)
	if err != nil {
		return fmt.Errorf("filter live Stored event through generated binding: %w", err)
	}
	defer storedIterator.Close()
	if !storedIterator.Next() {
		return fmt.Errorf("generated live Stored filter returned no event: %v", storedIterator.Error())
	}
	if storedIterator.Event.Raw.TxHash != receipt.TxHash {
		return fmt.Errorf("generated live Stored filter transaction = %s, want %s", storedIterator.Event.Raw.TxHash, receipt.TxHash)
	}
	if storedIterator.Next() || storedIterator.Error() != nil {
		return fmt.Errorf("generated live Stored filter returned an unexpected tail: %v", storedIterator.Error())
	}
	if err := checkContractCall(
		ctx,
		client,
		from,
		deployment.address,
		receipt.BlockNumber,
		deployment.binding,
		parsedABI,
		contractCall{Method: "read", Want: []any{amount, delta, tag, from, payload, note, true}},
	); err != nil {
		return fmt.Errorf("VM64 stored state: %w", err)
	}
	if err := checkVM64StorageSlots(ctx, client, deployment.address, receipt.BlockNumber, amount, delta, tag, from); err != nil {
		return err
	}
	if err := checkVM64EventLogs(ctx, deployment.binding, receipt, from, amount, delta, tag, payload, note); err != nil {
		return err
	}

	auth, err = newTransactor(ctx, client, w, from)
	if err != nil {
		return err
	}
	clearTx, err := deployment.binding.Transact(auth, "clear")
	if err != nil {
		return fmt.Errorf("VM64 clear through BoundContract: %w", err)
	}
	clearReceipt, err := waitTransaction(ctx, client, clearTx, types.ReceiptStatusSuccessful)
	if err != nil {
		return err
	}
	if err := checkContractCall(
		ctx,
		client,
		from,
		deployment.address,
		clearReceipt.BlockNumber,
		deployment.binding,
		parsedABI,
		contractCall{
			Method: "read",
			Want:   []any{new(big.Int), new(big.Int), [64]byte{}, common.Address{}, []byte{}, "", false},
		},
	); err != nil {
		return err
	}
	for _, entrypoint := range []struct {
		name  string
		value int64
		data  []byte
	}{
		{name: "receive", value: 11},
		{name: "fallback", value: 13, data: bytes.Repeat([]byte{0x5a}, 65)},
	} {
		auth, err := newTransactor(ctx, client, w, from)
		if err != nil {
			return err
		}
		auth.Value, auth.GasLimit = big.NewInt(entrypoint.value), 1_000_000
		session := EventEmitterBindingSmokeSession{Contract: deployment.smoke, TransactOpts: *auth}
		var tx *types.Transaction
		if entrypoint.data == nil {
			tx, err = session.Receive()
		} else {
			tx, err = session.Fallback(entrypoint.data)
		}
		if err != nil {
			return fmt.Errorf("generated live %s: %w", entrypoint.name, err)
		}
		if !bytes.Equal(tx.Data(), entrypoint.data) || tx.Value().Cmp(big.NewInt(entrypoint.value)) != 0 {
			return fmt.Errorf(
				"generated live %s transaction data=%x value=%s",
				entrypoint.name,
				tx.Data(),
				tx.Value(),
			)
		}
		if _, err := waitTransaction(ctx, client, tx, types.ReceiptStatusSuccessful); err != nil {
			return err
		}
	}
	balance, err := client.BalanceAt(ctx, deployment.address, nil)
	if err != nil {
		return fmt.Errorf("read generated payable entrypoint balance: %w", err)
	}
	if balance.Cmp(big.NewInt(24)) != 0 {
		return fmt.Errorf("generated payable entrypoint balance = %v, want 24", balance)
	}
	return nil
}

func checkVM64EventLogs(ctx context.Context, binding *bind.BoundContract, receipt *types.Receipt, recipient common.Address, amount, delta *big.Int, tag [64]byte, payload []byte, note string) error {
	artifact, err := loadEventEmitterArtifact()
	if err != nil {
		return err
	}
	parsed := &artifact.ABI
	storedLog, dynamicLog, compositeLog, anonymousLog := receipt.Logs[0], receipt.Logs[1], receipt.Logs[2], receipt.Logs[3]
	opts, err := receiptBlockRange(ctx, receipt)
	if err != nil {
		return err
	}
	checkDecoded := func(name string, log types.Log, want map[string]any) error {
		have, err := unpackEvent(binding, name, log)
		if err != nil {
			return fmt.Errorf("unpack %s: %w", name, err)
		}
		if diff := cmp.Diff(want, have, abiCompareOptions...); diff != "" {
			return fmt.Errorf("%s mismatch (-want +have):\n%s", name, diff)
		}
		return nil
	}
	checkFilter := func(name string, want map[string]any, query ...[]any) error {
		log, err := oneFilteredLog(ctx, binding, opts, name, query...)
		if err != nil {
			return err
		}
		if log.TxHash != receipt.TxHash {
			return fmt.Errorf("%s filtered transaction = %s, want %s", name, log.TxHash, receipt.TxHash)
		}
		return checkDecoded(name, log, want)
	}

	wantStoredData, err := parsed.Events["Stored"].Inputs.NonIndexed().Pack(tag, payload, note, true)
	if err != nil {
		return fmt.Errorf("pack canonical VM64 Stored data: %w", err)
	}
	if !bytes.Equal(storedLog.Data, wantStoredData) {
		return fmt.Errorf("compiler Stored data is non-canonical:\nhave %x\nwant %x", storedLog.Data, wantStoredData)
	}
	storedTopic := common.HashToLogTopic(parsed.Events["Stored"].ID)
	if len(storedLog.Topics) != 4 || storedLog.Topics[0] != storedTopic ||
		storedLog.Topics[1] != common.BytesToLeftAlignedLogTopic(recipient[:]) ||
		storedLog.Topics[2] != common.BytesToLeftAlignedLogTopic(qrlmath.U512Bytes(new(big.Int).Set(amount))) ||
		storedLog.Topics[3] != common.BytesToLeftAlignedLogTopic(qrlmath.U512Bytes(new(big.Int).Set(delta))) {
		return fmt.Errorf("VM64 Stored topics mismatch: %v", storedLog.Topics)
	}
	wantStored := map[string]any{
		"recipient": recipient, "amount": amount, "delta": delta, "tag": tag,
		"payload": payload, "note": note, "enabled": true,
	}
	if err := checkDecoded("Stored", *storedLog, wantStored); err != nil {
		return err
	}
	if err := checkFilter("Stored", wantStored, []any{recipient}, []any{amount}, []any{delta}); err != nil {
		return fmt.Errorf("filter VM64 Stored event: %w", err)
	}

	dynamicTopic := common.HashToLogTopic(parsed.Events["Dynamic"].ID)
	payloadHash := crypto.Keccak256Hash(payload)
	noteHash := crypto.Keccak256Hash([]byte(note))
	wantDynamicData, err := parsed.Events["Dynamic"].Inputs.NonIndexed().Pack(amount)
	if err != nil {
		return fmt.Errorf("pack canonical VM64 Dynamic data: %w", err)
	}
	if !bytes.Equal(dynamicLog.Data, wantDynamicData) {
		return fmt.Errorf("compiler Dynamic data is non-canonical:\nhave %x\nwant %x", dynamicLog.Data, wantDynamicData)
	}
	if len(dynamicLog.Topics) != 3 || dynamicLog.Topics[0] != dynamicTopic ||
		dynamicLog.Topics[1] != common.HashToLogTopic(payloadHash) || dynamicLog.Topics[2] != common.HashToLogTopic(noteHash) {
		return fmt.Errorf("VM64 Dynamic topics mismatch: %v", dynamicLog.Topics)
	}
	wantDynamic := map[string]any{"payload": payloadHash, "note": noteHash, "amount": amount}
	if err := checkDecoded("Dynamic", *dynamicLog, wantDynamic); err != nil {
		return err
	}
	if err := checkFilter("Dynamic", wantDynamic, []any{payload}, []any{note}); err != nil {
		return fmt.Errorf("filter VM64 Dynamic event: %w", err)
	}
	if err := checkHyperionCompositeEvent(ctx, binding, receipt, compositeLog, recipient, amount, tag, payload, note); err != nil {
		return err
	}

	wantAnonymous := []common.LogTopic{
		common.BytesToLeftAlignedLogTopic(recipient[:]),
		common.BytesToLeftAlignedLogTopic(qrlmath.U512Bytes(new(big.Int).Set(amount))),
		common.BytesToLeftAlignedLogTopic(tag[:]),
		common.BytesToLeftAlignedLogTopic(qrlmath.U512Bytes(big.NewInt(1))),
	}
	if len(anonymousLog.Topics) != len(wantAnonymous) {
		return fmt.Errorf("VM64 anonymous event topic count mismatch: have %d want %d", len(anonymousLog.Topics), len(wantAnonymous))
	}
	for i := range wantAnonymous {
		if anonymousLog.Topics[i] != wantAnonymous[i] {
			return fmt.Errorf("VM64 anonymous event topic %d mismatch: have %s want %s", i, anonymousLog.Topics[i], wantAnonymous[i])
		}
	}
	if len(anonymousLog.Data) != 0 {
		return fmt.Errorf("VM64 anonymous event unexpectedly has data: %x", anonymousLog.Data)
	}
	wantAnonymousValues := map[string]any{
		"recipient": recipient, "amount": amount, "tag": tag, "enabled": true,
	}
	if err := checkDecoded("AnonymousStored", *anonymousLog, wantAnonymousValues); err != nil {
		return err
	}
	if err := checkFilter(
		"AnonymousStored",
		wantAnonymousValues,
		[]any{recipient},
		[]any{amount},
		[]any{tag},
		[]any{true},
	); err != nil {
		return fmt.Errorf("filter VM64 anonymous event: %w", err)
	}

	wrongRecipient := recipient
	wrongRecipient[0] ^= 0xff
	orWildcard, err := oneFilteredLog(
		ctx,
		binding,
		opts,
		"AnonymousStored",
		[]any{wrongRecipient, recipient},
		nil,
		[]any{tag},
		nil,
	)
	if err != nil {
		return fmt.Errorf("filter VM64 anonymous event with OR/wildcard topics: %w", err)
	}
	if orWildcard.TxHash != receipt.TxHash {
		return fmt.Errorf("VM64 anonymous OR/wildcard transaction = %s, want %s", orWildcard.TxHash, receipt.TxHash)
	}
	if err := requireNoFilteredLogs(ctx, binding, opts, "AnonymousStored", nil, nil, nil, []any{false}); err != nil {
		return fmt.Errorf("VM64 anonymous mismatched filter: %w", err)
	}
	return nil
}

func deployEventEmitter(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address) (*eventDeployment, error) {
	artifact, err := loadEventEmitterArtifact()
	if err != nil {
		return nil, err
	}
	parsed := &artifact.ABI
	deployed := parsed.Events["Deployed"]
	expectedTopic := common.HashToLogTopic(deployed.ID)
	auth, err := newTransactor(ctx, client, w, from)
	if err != nil {
		return nil, err
	}
	contractAddress, tx, smoke, err := DeployEventEmitterBindingSmoke(auth, client)
	if err != nil {
		return nil, fmt.Errorf("deploy through generated smoke binding: %w", err)
	}
	receipt, err := waitTransaction(ctx, client, tx, types.ReceiptStatusSuccessful)
	if err != nil {
		return nil, err
	}
	contract := bindArtifact(artifact, contractAddress, client)
	if receipt.ContractAddress != contractAddress {
		return nil, fmt.Errorf("contract address mismatch: receipt %s binding %s", receipt.ContractAddress, contractAddress)
	}
	if len(receipt.Logs) != 1 {
		return nil, fmt.Errorf("expected one deployment log, got %d", len(receipt.Logs))
	}
	log := receipt.Logs[0]
	if len(log.Topics) != 1 || log.Topics[0] != expectedTopic {
		return nil, fmt.Errorf("event topic mismatch: have %v want %s", log.Topics, expectedTopic.Hex())
	}
	return &eventDeployment{
		address: contractAddress,
		tx:      tx,
		receipt: receipt,
		event:   deployed,
		topic:   expectedTopic,
		binding: contract,
		smoke:   smoke,
	}, nil
}
