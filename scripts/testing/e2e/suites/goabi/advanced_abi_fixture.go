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
	"strings"

	"github.com/google/go-cmp/cmp"
	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/rpc"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/testdata/contracts"
)

func advancedABIInitialRecord() dynamicRecord {
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1))
	return dynamicRecord{
		Amount:  maximum,
		Note:    "constructor tuple crosses a VM64 word: " + strings.Repeat("n", 65) + " \u754c\x00",
		Payload: bytes.Repeat([]byte{0xa7}, 129),
		Values:  [][]uint16{{}, {0, 1, 0xffff}, {0x1234}, {}},
	}
}

func advancedABITag() [64]byte {
	var tag [64]byte
	for index := range tag {
		tag[index] = byte(index + 1)
	}
	return tag
}

func advancedABIInitcode(artifact *contracts.Artifact, initial dynamicRecord) ([]byte, error) {
	arguments, err := artifact.ABI.Constructor.Inputs.Pack(initial)
	if err != nil {
		return nil, fmt.Errorf("pack AdvancedABI constructor: %w", err)
	}
	return append(append([]byte{}, artifact.Bytecode...), arguments...), nil
}

// checkAdvancedABIReadOnlyCreation proves that the compiler constructor
// decoder accepts the canonical dynamic tuple and rejects the same initcode
// after its nested-array tail has been truncated. Some external RPC servers
// disable creation-form qrl_call; supported reports that capability without
// weakening the portable simulated-backend assertion.
func checkAdvancedABIReadOnlyCreation(
	ctx context.Context,
	caller qrl.ContractCaller,
	from common.Address,
	blockNumber *big.Int,
	initial dynamicRecord,
) (supported bool, err error) {
	artifact, err := loadAdvancedABIArtifact()
	if err != nil {
		return false, err
	}
	initcode, err := advancedABIInitcode(artifact, initial)
	if err != nil {
		return false, err
	}
	output, err := caller.CallContract(ctx, qrl.CallMsg{From: from, Data: initcode}, blockNumber)
	if err != nil {
		message := strings.ToLower(err.Error())
		if (strings.Contains(message, "contract creation") && strings.Contains(message, "not supported")) ||
			strings.Contains(message, "missing required field \"to\"") ||
			strings.Contains(message, "missing required field 'to'") {
			return false, nil
		}
		return false, fmt.Errorf("canonical AdvancedABI creation qrl_call: %w", err)
	}
	if len(output) == 0 {
		return true, errors.New("canonical AdvancedABI creation qrl_call returned empty runtime bytecode")
	}
	if len(initcode) <= common.LogTopicLength {
		return true, fmt.Errorf("AdvancedABI initcode has only %d bytes", len(initcode))
	}
	truncated := append([]byte{}, initcode[:len(initcode)-common.LogTopicLength]...)
	output, err = caller.CallContract(ctx, qrl.CallMsg{From: from, Data: truncated}, blockNumber)
	if err == nil {
		return true, fmt.Errorf("truncated AdvancedABI creation qrl_call unexpectedly returned %x", output)
	}
	var dataError rpc.DataError
	if errors.As(err, &dataError) || errors.Is(err, vm.ErrExecutionReverted) {
		return true, nil
	}
	return true, fmt.Errorf("truncated AdvancedABI creation returned %T instead of an execution revert: %w", err, err)
}

func checkAdvancedABIContract(
	ctx context.Context,
	caller qrl.ContractCaller,
	from, address common.Address,
	contract *bind.BoundContract,
	blockNumber *big.Int,
	receipt *types.Receipt,
	initial dynamicRecord,
) error {
	artifact, err := loadAdvancedABIArtifact()
	if err != nil {
		return err
	}
	for _, vector := range []contractCall{
		{Method: "read", Want: []any{initial}},
		{Method: "echo", Args: []any{initial}, Want: []any{initial}},
		{Method: "overloaded", Args: []any{initial}, Want: []any{initial}},
		{Method: "overloaded0", Args: []any{initial.Amount}, Want: []any{initial.Amount}},
		{Method: "overloaded1", Args: []any{initial.Note}, Want: []any{initial.Note}},
	} {
		if err := checkContractCall(ctx, caller, from, address, blockNumber, contract, &artifact.ABI, vector); err != nil {
			return fmt.Errorf("AdvancedABI: %w", err)
		}
	}
	callOpts := &bind.CallOpts{Context: ctx, BlockNumber: blockNumber}
	numberReason := "file scoped failure " + strings.Repeat("r", 65)
	if err := checkContractError(
		&artifact.ABI,
		contract,
		callOpts,
		"failNumber",
		"ScopedFailure(uint512,string)",
		initial.Amount,
		numberReason,
	); err != nil {
		return err
	}
	if err := checkContractError(
		&artifact.ABI,
		contract,
		callOpts,
		"failDynamic",
		"ScopedFailure(bytes,uint16[][])",
		bytes.Repeat([]byte{0x5d}, 129),
		[][]uint16{{}, {1, 0xffff}, {0x1234}},
	); err != nil {
		return err
	}
	return checkAdvancedABIConstructorEvents(ctx, contract, &artifact.ABI, receipt, initial)
}

func checkAdvancedABIConstructorEvents(
	ctx context.Context,
	contract *bind.BoundContract,
	parsed *abi.ABI,
	receipt *types.Receipt,
	initial dynamicRecord,
) error {
	if receipt == nil || receipt.BlockNumber == nil {
		return errors.New("AdvancedABI constructor receipt has no block number")
	}
	if len(receipt.Logs) != 3 {
		return fmt.Errorf("AdvancedABI constructor emitted %d logs, want 3", len(receipt.Logs))
	}
	tag := advancedABITag()
	vectors := []struct {
		name         string
		log          *types.Log
		indexedValue any
		dataValues   []any
		want         map[string]any
	}{
		{
			name: "Constructed", log: receipt.Logs[0], indexedValue: initial.Amount,
			dataValues: []any{initial.Note}, want: map[string]any{"amount": initial.Amount, "note": initial.Note},
		},
		{
			name: "Overloaded", log: receipt.Logs[1], indexedValue: initial.Amount,
			dataValues: []any{initial.Note}, want: map[string]any{"value": initial.Amount, "note": initial.Note},
		},
		{
			name: "Overloaded0", log: receipt.Logs[2], indexedValue: tag,
			dataValues: []any{initial.Payload}, want: map[string]any{"tag": tag, "payload": initial.Payload},
		},
	}
	opts, err := receiptBlockRange(ctx, receipt)
	if err != nil {
		return err
	}
	for _, vector := range vectors {
		definition := parsed.Events[vector.name]
		indexedTopic, err := abi.MakeTopic(definition.Inputs[0].Type, vector.indexedValue)
		if err != nil {
			return fmt.Errorf("encode %s indexed constructor topic: %w", vector.name, err)
		}
		wantTopics := []common.LogTopic{common.HashToLogTopic(definition.ID), indexedTopic}
		if diff := cmp.Diff(wantTopics, vector.log.Topics, abiCompareOptions...); diff != "" {
			return fmt.Errorf("%s constructor topics mismatch (-want +have):\n%s", vector.name, diff)
		}
		wantData, err := definition.Inputs.NonIndexed().Pack(vector.dataValues...)
		if err != nil {
			return fmt.Errorf("pack %s constructor event data: %w", vector.name, err)
		}
		if !bytes.Equal(vector.log.Data, wantData) {
			return fmt.Errorf("%s constructor data is non-canonical:\nhave %x\nwant %x", vector.name, vector.log.Data, wantData)
		}
		decoded, err := unpackEvent(contract, vector.name, *vector.log)
		if err != nil {
			return fmt.Errorf("unpack %s constructor event: %w", vector.name, err)
		}
		if diff := cmp.Diff(vector.want, decoded, abiCompareOptions...); diff != "" {
			return fmt.Errorf("%s constructor event mismatch (-want +have):\n%s", vector.name, diff)
		}
		filtered, err := oneFilteredLog(ctx, contract, opts, vector.name, []any{vector.indexedValue})
		if err != nil {
			return fmt.Errorf("filter %s constructor event: %w", vector.name, err)
		}
		if filtered.TxHash != receipt.TxHash {
			return fmt.Errorf("%s filtered transaction = %s, want %s", vector.name, filtered.TxHash, receipt.TxHash)
		}
		filteredValues, err := unpackEvent(contract, vector.name, filtered)
		if err != nil {
			return fmt.Errorf("unpack filtered %s constructor event: %w", vector.name, err)
		}
		if diff := cmp.Diff(vector.want, filteredValues, abiCompareOptions...); diff != "" {
			return fmt.Errorf("filtered %s event mismatch (-want +have):\n%s", vector.name, diff)
		}
	}
	return nil
}

func checkLiveAdvancedABI(
	ctx context.Context,
	client *qrlclient.Client,
	w wallet.Wallet,
	from common.Address,
) error {
	artifact, err := loadAdvancedABIArtifact()
	if err != nil {
		return err
	}
	initial := advancedABIInitialRecord()
	auth, err := newTransactor(ctx, client, w, from)
	if err != nil {
		return err
	}
	_, tx, _, err := bind.DeployContract(auth, artifact.ABI, artifact.Bytecode, client, initial)
	if err != nil {
		return fmt.Errorf("deploy AdvancedABI: %w", err)
	}
	receipt, err := waitTransaction(ctx, client, tx, types.ReceiptStatusSuccessful)
	if err != nil {
		return err
	}
	contract := bindArtifact(artifact, receipt.ContractAddress, client)
	supported, err := checkAdvancedABIReadOnlyCreation(ctx, client, from, receipt.BlockNumber, initial)
	if err != nil {
		return fmt.Errorf("live AdvancedABI constructor decoder: %w", err)
	}
	if !supported {
		return errors.New("live RPC does not support creation-form qrl_call required by the AdvancedABI constructor decoder check")
	}
	return checkAdvancedABIContract(ctx, client, from, receipt.ContractAddress, contract, receipt.BlockNumber, receipt, initial)
}
