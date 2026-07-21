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
	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
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
	binding *EventEmitter
}

func checkLiveEventRoundTrip(ctx context.Context, run *suiteRun, client *qrlclient.Client, w wallet.Wallet, from common.Address, binHex, graphqlURL string) error {
	if normalizeHex(EventEmitterBin) != normalizeHex(binHex) {
		return fmt.Errorf("generated binding bytecode differs from JS fixture")
	}
	deployment, err := deployEventEmitter(ctx, run, TransactionEventEmitterDeploy, client, w, from)
	if err != nil {
		return err
	}
	receipt := deployment.receipt

	parsed, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return fmt.Errorf("parse emitter ABI from generated binding: %w", err)
	}
	log := receipt.Logs[0]
	values, err := parsed.Unpack("Deployed", log.Data)
	if err != nil {
		return fmt.Errorf("decode deployment log data: %w", err)
	}
	if len(values) != 1 || values[0].(*big.Int).Cmp(big.NewInt(1337)) != 0 {
		return fmt.Errorf("decoded deployment value mismatch: %v", values)
	}

	parsedEvent, err := deployment.binding.ParseDeployed(*log)
	if err != nil {
		return fmt.Errorf("parse deployment log through generated binding: %w", err)
	}
	if parsedEvent.Value.Cmp(big.NewInt(1337)) != 0 || parsedEvent.Raw.TxHash != receipt.TxHash {
		return fmt.Errorf("generated binding parsed event mismatch: %+v", parsedEvent)
	}

	it, err := deployment.binding.FilterDeployed(&bind.FilterOpts{
		Start:   receipt.BlockNumber.Uint64(),
		End:     uint64Ptr(receipt.BlockNumber.Uint64()),
		Context: ctx,
	})
	if err != nil {
		return fmt.Errorf("filter through generated binding: %w", err)
	}
	defer it.Close()
	if !it.Next() {
		if err := it.Error(); err != nil {
			return fmt.Errorf("generated binding event iterator: %w", err)
		}
		return fmt.Errorf("generated binding event iterator returned no events")
	}
	if it.Event.Value.Cmp(big.NewInt(1337)) != 0 || it.Event.Raw.TxHash != receipt.TxHash {
		return fmt.Errorf("generated binding filtered event mismatch: %+v", it.Event)
	}
	if it.Next() {
		return fmt.Errorf("generated binding filter returned more than one event")
	}
	if err := it.Error(); err != nil {
		return fmt.Errorf("generated binding event iterator final error: %w", err)
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
	if graphqlURL != "" {
		if err := checkGraphQLEventLog(ctx, graphqlURL, deployment); err != nil {
			return err
		}
	}
	if err := checkLiveVM64Contract(ctx, run, client, w, from, deployment); err != nil {
		return err
	}
	return nil
}

func checkLiveVM64Contract(ctx context.Context, run *suiteRun, client *qrlclient.Client, w wallet.Wallet, from common.Address, deployment *eventDeployment) error {
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

	echoAmount, echoDelta, echoTag, echoRecipient, echoPayload, echoNote, echoEnabled, err := deployment.binding.Echo(
		callOpts, amount, delta, tag, from, payload, note, true,
	)
	if err != nil {
		return fmt.Errorf("VM64 echo through generated binding: %w", err)
	}
	if echoAmount.Cmp(amount) != 0 || echoDelta.Cmp(delta) != 0 || echoTag != tag || echoRecipient != from ||
		!bytes.Equal(echoPayload, payload) || echoNote != note || !echoEnabled {
		return fmt.Errorf("VM64 echo mismatch: amount=%x delta=%x tag=%x recipient=%s payload=%x note=%q enabled=%t",
			echoAmount, echoDelta, echoTag, echoRecipient, echoPayload, echoNote, echoEnabled)
	}

	value1 := [1]byte{0xa5}
	var value32 [32]byte
	var value33 [33]byte
	for i := range value32 {
		value32[i] = byte(i + 1)
	}
	for i := range value33 {
		value33[i] = byte(0x40 + i)
	}
	fixed1, fixed32, fixed33, fixed64, err := deployment.binding.EchoFixed(callOpts, value1, value32, value33, tag)
	if err != nil {
		return fmt.Errorf("VM64 fixed-bytes echo through generated binding: %w", err)
	}
	if fixed1 != value1 || fixed32 != value32 || fixed33 != value33 || fixed64 != tag {
		return fmt.Errorf("VM64 fixed-bytes echo mismatch")
	}

	arrayValues := []*big.Int{big.NewInt(0), big.NewInt(1), amount}
	arrayTags := [2][64]byte{tag, {}}
	copy(arrayTags[1][:], payload[:64])
	gotValues, gotTags, err := deployment.binding.EchoArrays(callOpts, arrayValues, arrayTags)
	if err != nil {
		return fmt.Errorf("VM64 array echo through generated binding: %w", err)
	}
	if len(gotValues) != len(arrayValues) || gotTags != arrayTags {
		return fmt.Errorf("VM64 array echo shape mismatch: values=%d tags=%x", len(gotValues), gotTags)
	}
	for i := range arrayValues {
		if gotValues[i].Cmp(arrayValues[i]) != 0 {
			return fmt.Errorf("VM64 array echo value %d mismatch: have %x want %x", i, gotValues[i], arrayValues[i])
		}
	}

	record := EventEmitterRecord{Amount: amount, Recipient: from, Tag: tag}
	gotRecord, err := deployment.binding.EchoRecord(callOpts, record)
	if err != nil {
		return fmt.Errorf("VM64 tuple echo through generated binding: %w", err)
	}
	if gotRecord.Amount.Cmp(record.Amount) != 0 || gotRecord.Recipient != record.Recipient || gotRecord.Tag != record.Tag {
		return fmt.Errorf("VM64 tuple echo mismatch: %+v", gotRecord)
	}

	parsedABI, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return fmt.Errorf("parse emitter ABI for transaction semantics: %w", err)
	}
	storeInput, err := parsedABI.Pack("store", amount, delta, tag, from, payload, note, true)
	if err != nil {
		return fmt.Errorf("encode VM64 store transaction semantics: %w", err)
	}
	storeExpected := newTransactionSemantics(&deployment.address, new(big.Int), storeInput)
	receipt, err := run.resumeOrRecordAndWaitReceipt(ctx, TransactionEventEmitterStore, client, storeExpected, func() (common.Hash, error) {
		auth, err := newTransactor(ctx, client, w, from)
		if err != nil {
			return common.Hash{}, err
		}
		backend := &journalBackend{ContractBackend: client, run: run, label: TransactionEventEmitterStore, semantics: storeExpected}
		binding, err := NewEventEmitter(deployment.address, backend)
		if err != nil {
			return common.Hash{}, fmt.Errorf("bind VM64 store journal backend: %w", err)
		}
		tx, err := binding.Store(auth, amount, delta, tag, from, payload, note, true)
		if err != nil {
			return common.Hash{}, fmt.Errorf("VM64 store through generated binding: %w", err)
		}
		return tx.Hash(), nil
	})
	if err != nil {
		return err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("VM64 store failed with status %d", receipt.Status)
	}
	if len(receipt.Logs) != 3 {
		return fmt.Errorf("VM64 store emitted %d logs, want 3", len(receipt.Logs))
	}

	read, err := deployment.binding.Read(&bind.CallOpts{Context: ctx, BlockNumber: receipt.BlockNumber})
	if err != nil {
		return fmt.Errorf("VM64 read through generated binding: %w", err)
	}
	if read.Amount.Cmp(amount) != 0 || read.Delta.Cmp(delta) != 0 || read.Tag != tag || read.Recipient != from ||
		!bytes.Equal(read.Payload, payload) || read.Note != note || !read.Enabled {
		return fmt.Errorf("VM64 stored state mismatch: %+v", read)
	}
	if err := checkVM64StorageSlots(ctx, client, deployment.address, receipt.BlockNumber, amount, delta, tag, from); err != nil {
		return err
	}
	if err := checkVM64EventLogs(ctx, deployment.binding, receipt, from, amount, delta, tag, payload, note); err != nil {
		return err
	}

	clearInput, err := parsedABI.Pack("clear")
	if err != nil {
		return fmt.Errorf("encode VM64 clear transaction semantics: %w", err)
	}
	clearExpected := newTransactionSemantics(&deployment.address, new(big.Int), clearInput)
	clearReceipt, err := run.resumeOrRecordAndWaitReceipt(ctx, TransactionEventEmitterClear, client, clearExpected, func() (common.Hash, error) {
		auth, err := newTransactor(ctx, client, w, from)
		if err != nil {
			return common.Hash{}, err
		}
		backend := &journalBackend{ContractBackend: client, run: run, label: TransactionEventEmitterClear, semantics: clearExpected}
		binding, err := NewEventEmitter(deployment.address, backend)
		if err != nil {
			return common.Hash{}, fmt.Errorf("bind VM64 clear journal backend: %w", err)
		}
		clearTx, err := binding.Clear(auth)
		if err != nil {
			return common.Hash{}, fmt.Errorf("VM64 clear through generated binding: %w", err)
		}
		return clearTx.Hash(), nil
	})
	if err != nil {
		return err
	}
	if clearReceipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("VM64 clear failed with status %d", clearReceipt.Status)
	}
	cleared, err := deployment.binding.Read(&bind.CallOpts{Context: ctx, BlockNumber: clearReceipt.BlockNumber})
	if err != nil {
		return fmt.Errorf("VM64 read after clear: %w", err)
	}
	if cleared.Amount.Sign() != 0 || cleared.Delta.Sign() != 0 || cleared.Tag != ([64]byte{}) ||
		cleared.Recipient != (common.Address{}) || len(cleared.Payload) != 0 || cleared.Note != "" || cleared.Enabled {
		return fmt.Errorf("VM64 clear left non-zero state: %+v", cleared)
	}
	return nil
}

func checkVM64EventLogs(ctx context.Context, binding *EventEmitter, receipt *types.Receipt, recipient common.Address, amount, delta *big.Int, tag [64]byte, payload []byte, note string) error {
	parsed, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return fmt.Errorf("parse VM64 event ABI: %w", err)
	}
	storedLog, dynamicLog, anonymousLog := receipt.Logs[0], receipt.Logs[1], receipt.Logs[2]

	storedTopic := hashTopic(parsed.Events["Stored"].ID)
	if len(storedLog.Topics) != 4 || storedLog.Topics[0] != storedTopic ||
		storedLog.Topics[1] != bytesTopic(recipient[:]) ||
		storedLog.Topics[2] != bytesTopic(unsignedWord(amount)) ||
		storedLog.Topics[3] != bytesTopic(signedWord(delta)) {
		return fmt.Errorf("VM64 Stored topics mismatch: %v", storedLog.Topics)
	}
	stored, err := binding.ParseStored(*storedLog)
	if err != nil {
		return fmt.Errorf("parse VM64 Stored event through generated binding: %w", err)
	}
	if stored.Recipient != recipient || stored.Amount.Cmp(amount) != 0 || stored.Delta.Cmp(delta) != 0 ||
		stored.Tag != tag || !bytes.Equal(stored.Payload, payload) || stored.Note != note || !stored.Enabled {
		return fmt.Errorf("decoded VM64 Stored event mismatch: %+v", stored)
	}
	storedIt, err := binding.FilterStored(&bind.FilterOpts{
		Start: receipt.BlockNumber.Uint64(), End: uint64Ptr(receipt.BlockNumber.Uint64()), Context: ctx,
	}, []common.Address{recipient}, []*big.Int{amount}, []*big.Int{delta})
	if err != nil {
		return fmt.Errorf("filter VM64 Stored event through generated binding: %w", err)
	}
	defer storedIt.Close()
	if !storedIt.Next() || storedIt.Event.Raw.TxHash != receipt.TxHash || storedIt.Event.Delta.Cmp(delta) != 0 {
		if err := storedIt.Error(); err != nil {
			return fmt.Errorf("iterate VM64 Stored event: %w", err)
		}
		return fmt.Errorf("filtered VM64 Stored event mismatch")
	}
	if storedIt.Next() {
		return fmt.Errorf("VM64 Stored filter returned more than one event")
	}
	if err := storedIt.Error(); err != nil {
		return fmt.Errorf("finish VM64 Stored event iterator: %w", err)
	}

	dynamicTopic := hashTopic(parsed.Events["Dynamic"].ID)
	payloadHash := crypto.Keccak256Hash(payload)
	noteHash := crypto.Keccak256Hash([]byte(note))
	if len(dynamicLog.Topics) != 3 || dynamicLog.Topics[0] != dynamicTopic ||
		dynamicLog.Topics[1] != hashTopic(payloadHash) || dynamicLog.Topics[2] != hashTopic(noteHash) {
		return fmt.Errorf("VM64 Dynamic topics mismatch: %v", dynamicLog.Topics)
	}
	dynamic, err := binding.ParseDynamic(*dynamicLog)
	if err != nil {
		return fmt.Errorf("parse VM64 Dynamic event through generated binding: %w", err)
	}
	if dynamic.Payload != payloadHash || dynamic.Note != noteHash || dynamic.Amount.Cmp(amount) != 0 {
		return fmt.Errorf("decoded VM64 Dynamic event mismatch: %+v", dynamic)
	}
	dynamicIt, err := binding.FilterDynamic(&bind.FilterOpts{
		Start: receipt.BlockNumber.Uint64(), End: uint64Ptr(receipt.BlockNumber.Uint64()), Context: ctx,
	}, [][]byte{payload}, []string{note})
	if err != nil {
		return fmt.Errorf("filter VM64 Dynamic event through generated binding: %w", err)
	}
	defer dynamicIt.Close()
	if !dynamicIt.Next() || dynamicIt.Event.Raw.TxHash != receipt.TxHash {
		if err := dynamicIt.Error(); err != nil {
			return fmt.Errorf("iterate VM64 Dynamic event: %w", err)
		}
		return fmt.Errorf("filtered VM64 Dynamic event mismatch")
	}
	if dynamicIt.Next() {
		return fmt.Errorf("VM64 Dynamic filter returned more than one event")
	}
	if err := dynamicIt.Error(); err != nil {
		return fmt.Errorf("finish VM64 Dynamic event iterator: %w", err)
	}

	wantAnonymous := []common.LogTopic{
		bytesTopic(recipient[:]),
		bytesTopic(unsignedWord(amount)),
		bytesTopic(tag[:]),
		bytesTopic(unsignedWord(big.NewInt(1))),
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
	return nil
}

func deployEventEmitter(ctx context.Context, run *suiteRun, label string, client *qrlclient.Client, w wallet.Wallet, from common.Address) (*eventDeployment, error) {
	parsed, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("parse emitter ABI from generated binding: %w", err)
	}
	deployed := parsed.Events["Deployed"]
	expectedTopic := hashTopic(deployed.ID)
	expectedTransaction := newTransactionSemantics(nil, new(big.Int), common.FromHex(EventEmitterBin))

	var (
		contractAddress common.Address
		tx              *types.Transaction
		contract        *EventEmitter
		receipt         *types.Receipt
	)
	if recorded, ok, resumeErr := run.ensurePreparedSubmitted(ctx, label, client, expectedTransaction); resumeErr != nil {
		return nil, resumeErr
	} else if ok {
		receipt, err = run.waitRecordedReceipt(ctx, client, recorded)
		if err != nil {
			return nil, err
		}
		var pending bool
		tx, pending, err = client.TransactionByHash(ctx, recorded.hash)
		if err != nil {
			return nil, fmt.Errorf("load recorded deployment %s: %w", recorded.hash, err)
		}
		if pending {
			return nil, fmt.Errorf("recorded deployment %s is still pending", recorded.hash)
		}
		contractAddress = receipt.ContractAddress
		contract, err = NewEventEmitter(contractAddress, client)
		if err != nil {
			return nil, fmt.Errorf("bind recorded deployment %s: %w", recorded.hash, err)
		}
	} else {
		chainID, err := client.ChainID(ctx)
		if err != nil {
			return nil, fmt.Errorf("chain id: %w", err)
		}
		auth, err := bind.NewKeyedTransactorWithChainID(w, chainID)
		if err != nil {
			return nil, fmt.Errorf("generated binding transactor: %w", err)
		}
		auth.Context = ctx
		auth.From = from

		backend := &journalBackend{ContractBackend: client, run: run, label: label, semantics: expectedTransaction}
		contractAddress, tx, contract, err = DeployEventEmitter(auth, backend)
		if err != nil {
			return nil, fmt.Errorf("deploy through generated binding: %w", err)
		}
		receipt, err = run.recordAndWaitReceipt(ctx, label, client, tx.Hash())
		if err != nil {
			return nil, err
		}
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("deployment failed with status %d", receipt.Status)
	}
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
	}, nil
}
