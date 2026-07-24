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
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"

	"github.com/google/go-cmp/cmp"
	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/rpc"
)

// checkHyperionCompilerCalls crosses the BoundContract and compiler ABI
// boundary with the nested layouts and rejection cases covered by Hyperion's
// ABIEncoderV2 and ABIDecoder tests at commit
// 2b9a0f1d5352cf7a4d64718fb04b4b6640041ba1.
func checkHyperionCompilerCalls(
	ctx context.Context,
	caller qrl.ContractCaller,
	from common.Address,
	address common.Address,
	contract *bind.BoundContract,
	callOpts *bind.CallOpts,
	amount *big.Int,
	tag [64]byte,
) error {
	primary := dynamicRecord{
		Amount:  new(big.Int).Set(amount),
		Note:    "nested tuple crosses one VM64 word: " + string(bytes.Repeat([]byte{'n'}, 65)) + " \u754c\x00",
		Payload: bytes.Repeat([]byte{0xa5}, 129),
		Values:  [][]uint16{{}, {0, 1, 0xffff}, {0x1234}},
	}
	maximum := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 512), big.NewInt(1))
	secondary := dynamicRecord{
		Amount:  maximum,
		Note:    "",
		Payload: []byte{},
		Values:  [][]uint16{{0xffff}, {}},
	}
	records := []dynamicRecord{secondary, primary}
	cube := [][][]uint16{
		{},
		{{}},
		{{1, 2}, {}, {0xffff}},
	}
	artifact, err := loadEventEmitterArtifact()
	if err != nil {
		return err
	}
	fixed := [3]byte{0xa1, 0xb2, 0xc3}
	for _, vector := range []contractCall{
		{
			Method: "echoNested",
			Args:   []any{primary, records, cube},
			Want:   []any{primary, records, cube},
		},
		{
			Method: "validateScalars",
			Args:   []any{true, int16(-2), fixed},
			Want:   []any{true, int16(-2), fixed},
		},
	} {
		if err := checkContractCall(ctx, caller, from, address, callOpts.BlockNumber, contract, &artifact.ABI, vector); err != nil {
			return fmt.Errorf("Hyperion: %w", err)
		}
	}
	parsed := &artifact.ABI
	if err := checkCompilerEntrypoints(ctx, caller, from, address, callOpts.BlockNumber, parsed); err != nil {
		return err
	}
	if err := checkCompilerLooseDecoderLayouts(ctx, caller, from, address, callOpts.BlockNumber, parsed, amount, tag); err != nil {
		return err
	}
	if err := checkCompilerCustomError(parsed, contract, callOpts, amount, from, tag); err != nil {
		return err
	}
	if err := checkCompilerDecoderRejections(ctx, caller, from, address, callOpts.BlockNumber, parsed, primary, records, cube, amount, tag); err != nil {
		return err
	}
	return nil
}

func checkCompilerEntrypoints(
	ctx context.Context,
	caller qrl.ContractCaller,
	from common.Address,
	address common.Address,
	blockNumber *big.Int,
	parsed *abi.ABI,
) error {
	if !parsed.HasFallback() || parsed.Fallback.StateMutability != "payable" {
		return errors.New("Hyperion fixture has no payable fallback entrypoint")
	}
	if !parsed.HasReceive() || parsed.Receive.StateMutability != "payable" {
		return errors.New("Hyperion fixture has no payable receive entrypoint")
	}
	unknownCall := []byte{0xde, 0xad, 0xbe, 0xef, 0x01}
	if _, err := parsed.MethodById(unknownCall); err == nil {
		return fmt.Errorf("fallback probe selector %x unexpectedly resolves to a method", unknownCall[:4])
	}
	for _, vector := range []struct {
		name string
		data []byte
	}{
		{name: "fallback", data: unknownCall},
		{name: "receive", data: nil},
	} {
		output, err := caller.CallContract(ctx, qrl.CallMsg{
			From:  from,
			To:    &address,
			Value: big.NewInt(1),
			Data:  vector.data,
		}, blockNumber)
		if err != nil {
			return fmt.Errorf("compiler-produced payable %s eth_call: %w", vector.name, err)
		}
		if len(output) != 0 {
			return fmt.Errorf("compiler-produced payable %s returned %x, want empty output", vector.name, output)
		}
	}
	return nil
}

func checkCompilerLooseDecoderLayouts(
	ctx context.Context,
	caller qrl.ContractCaller,
	from common.Address,
	address common.Address,
	blockNumber *big.Int,
	parsed *abi.ABI,
	amount *big.Int,
	tag [64]byte,
) error {
	const headWords = 7
	payload := []byte("payload")
	note := "note"
	delta := big.NewInt(-2)
	canonical, err := parsed.Pack("echo", amount, delta, tag, from, payload, note, true)
	if err != nil {
		return fmt.Errorf("pack canonical echo for loose decoder layouts: %w", err)
	}
	payloadOffsetPosition := 4 + 4*common.LogTopicLength
	noteOffsetPosition := 4 + 5*common.LogTopicLength
	payloadRelative, err := readVM64Offset(canonical, payloadOffsetPosition)
	if err != nil {
		return fmt.Errorf("read canonical bytes offset: %w", err)
	}
	noteRelative, err := readVM64Offset(canonical, noteOffsetPosition)
	if err != nil {
		return fmt.Errorf("read canonical string offset: %w", err)
	}
	headEnd := 4 + headWords*common.LogTopicLength
	payloadStart := 4 + payloadRelative
	noteStart := 4 + noteRelative
	if payloadStart != headEnd || noteStart <= payloadStart || noteStart > len(canonical) {
		return fmt.Errorf("unexpected canonical echo layout: head=%d payload=%d note=%d total=%d", headEnd, payloadStart, noteStart, len(canonical))
	}
	payloadTail := append([]byte{}, canonical[payloadStart:noteStart]...)
	noteTail := append([]byte{}, canonical[noteStart:]...)

	alias := append([]byte{}, canonical...)
	if err := overwriteVM64Word(alias, noteOffsetPosition, big.NewInt(int64(payloadRelative))); err != nil {
		return fmt.Errorf("build aliased loose decoder call: %w", err)
	}
	if err := rawEchoMustMatch(ctx, caller, from, address, blockNumber, parsed, alias, amount, delta, tag, payload, string(payload)); err != nil {
		return fmt.Errorf("Hyperion loose decoder alias layout: %w", err)
	}

	gap := append([]byte{}, canonical[:headEnd]...)
	gap = append(gap, make([]byte, common.LogTopicLength)...)
	gap = append(gap, canonical[headEnd:]...)
	if err := overwriteVM64Word(gap, payloadOffsetPosition, big.NewInt(int64(payloadRelative+common.LogTopicLength))); err != nil {
		return fmt.Errorf("build gapped bytes offset: %w", err)
	}
	if err := overwriteVM64Word(gap, noteOffsetPosition, big.NewInt(int64(noteRelative+common.LogTopicLength))); err != nil {
		return fmt.Errorf("build gapped string offset: %w", err)
	}
	if err := rawEchoMustMatch(ctx, caller, from, address, blockNumber, parsed, gap, amount, delta, tag, payload, note); err != nil {
		return fmt.Errorf("Hyperion loose decoder gap layout: %w", err)
	}

	nonmonotonic := append([]byte{}, canonical[:headEnd]...)
	nonmonotonic = append(nonmonotonic, noteTail...)
	nonmonotonic = append(nonmonotonic, payloadTail...)
	noteFirst := headWords * common.LogTopicLength
	payloadSecond := noteFirst + len(noteTail)
	if err := overwriteVM64Word(nonmonotonic, payloadOffsetPosition, big.NewInt(int64(payloadSecond))); err != nil {
		return fmt.Errorf("build nonmonotonic bytes offset: %w", err)
	}
	if err := overwriteVM64Word(nonmonotonic, noteOffsetPosition, big.NewInt(int64(noteFirst))); err != nil {
		return fmt.Errorf("build nonmonotonic string offset: %w", err)
	}
	if err := rawEchoMustMatch(ctx, caller, from, address, blockNumber, parsed, nonmonotonic, amount, delta, tag, payload, note); err != nil {
		return fmt.Errorf("Hyperion loose decoder nonmonotonic layout: %w", err)
	}
	return nil
}

func rawEchoMustMatch(
	ctx context.Context,
	caller qrl.ContractCaller,
	from common.Address,
	address common.Address,
	blockNumber *big.Int,
	parsed *abi.ABI,
	calldata []byte,
	amount *big.Int,
	delta *big.Int,
	tag [64]byte,
	payload []byte,
	note string,
) error {
	output, err := caller.CallContract(ctx, qrl.CallMsg{From: from, To: &address, Data: calldata}, blockNumber)
	if err != nil {
		return err
	}
	wantOutput, err := parsed.Methods["echo"].Outputs.Pack(amount, delta, tag, from, payload, note, true)
	if err != nil {
		return fmt.Errorf("pack canonical echo output: %w", err)
	}
	if !bytes.Equal(output, wantOutput) {
		return fmt.Errorf("compiler echo output is non-canonical:\nhave %x\nwant %x", output, wantOutput)
	}
	decoded, err := parsed.Unpack("echo", output)
	if err != nil {
		return fmt.Errorf("decode echo output: %w", err)
	}
	if len(decoded) != 7 || decoded[0].(*big.Int).Cmp(amount) != 0 || decoded[1].(*big.Int).Cmp(delta) != 0 ||
		decoded[2].([64]byte) != tag || decoded[3].(common.Address) != from ||
		!bytes.Equal(decoded[4].([]byte), payload) || decoded[5].(string) != note || !decoded[6].(bool) {
		return fmt.Errorf("echo output mismatch: %#v", decoded)
	}
	return nil
}

func checkCompilerCustomError(
	parsed *abi.ABI,
	contract *bind.BoundContract,
	callOpts *bind.CallOpts,
	code *big.Int,
	recipient common.Address,
	tag [64]byte,
) error {
	reason := "compiler custom error crosses one VM64 word: " + string(bytes.Repeat([]byte{'r'}, 65)) + " \u754c\x00"
	payload := bytes.Repeat([]byte{0x5c}, 129)
	record := eventRecord{Amount: new(big.Int).Set(code), Recipient: recipient, Tag: tag}
	nested := [][]uint16{{}, {1, 0xffff}, {0x1234}}
	return checkContractError(
		parsed,
		contract,
		callOpts,
		"failComplex",
		"ComplexFailure(uint512,string,bytes,(uint512,address,bytes64),uint16[][])",
		code,
		reason,
		payload,
		record,
		nested,
	)
}

func checkCompilerDecoderRejections(
	ctx context.Context,
	caller qrl.ContractCaller,
	from common.Address,
	address common.Address,
	blockNumber *big.Int,
	parsed *abi.ABI,
	primary dynamicRecord,
	records []dynamicRecord,
	cube [][][]uint16,
	amount *big.Int,
	tag [64]byte,
) error {
	echo, err := parsed.Pack("echo", amount, big.NewInt(-2), tag, from, []byte{1, 2, 3}, "offsets", true)
	if err != nil {
		return fmt.Errorf("pack valid echo call for malformed vectors: %w", err)
	}
	nested, err := parsed.Pack("echoNested", primary, records, cube)
	if err != nil {
		return fmt.Errorf("pack valid nested call for malformed vectors: %w", err)
	}
	scalars, err := parsed.Pack("validateScalars", true, int16(-2), [3]byte{0xa1, 0xb2, 0xc3})
	if err != nil {
		return fmt.Errorf("pack valid scalar call for malformed vectors: %w", err)
	}

	shortHead := append([]byte{}, parsed.Methods["echo"].ID...)
	noteRelative, err := readVM64Offset(echo, 4+5*common.LogTopicLength)
	if err != nil {
		return fmt.Errorf("locate string calldata tail: %w", err)
	}
	noteStart := 4 + noteRelative
	truncatedDynamicTailEnd := noteStart + common.LogTopicLength + len("offsets") - 1
	if truncatedDynamicTailEnd < 0 || truncatedDynamicTailEnd > len(echo) {
		return fmt.Errorf("truncated string tail end %d exceeds calldata length %d", truncatedDynamicTailEnd, len(echo))
	}
	truncatedDynamicTail := append([]byte{}, echo[:truncatedDynamicTailEnd]...)
	oversizedDynamicTail := append([]byte{}, echo...)
	if err := overwriteVM64Word(
		oversizedDynamicTail,
		noteStart,
		big.NewInt(int64(len(echo)+common.LogTopicLength)),
	); err != nil {
		return fmt.Errorf("oversize string calldata tail: %w", err)
	}
	impossibleLengthFromHead := append([]byte{}, echo...)
	if err := overwriteVM64Word(impossibleLengthFromHead, 4+4*common.LogTopicLength, big.NewInt(common.LogTopicLength)); err != nil {
		return err
	}
	highOffset := append([]byte{}, echo...)
	if err := overwriteVM64Word(highOffset, 4+4*common.LogTopicLength, new(big.Int).Lsh(big.NewInt(1), 511)); err != nil {
		return err
	}
	nestedOffset := append([]byte{}, nested...)
	cubeRelative, err := readVM64Offset(nestedOffset, 4+2*common.LogTopicLength)
	if err != nil {
		return fmt.Errorf("locate 3-D-array calldata tail: %w", err)
	}
	cubeStart := 4 + cubeRelative
	if err := overwriteVM64Word(nestedOffset, cubeStart+common.LogTopicLength, big.NewInt(common.LogTopicLength)); err != nil {
		return fmt.Errorf("corrupt nested 3-D-array offset: %w", err)
	}
	outerOffsetsStart := cubeStart + common.LogTopicLength
	thirdPlaneRelative, err := readVM64Offset(nested, outerOffsetsStart+2*common.LogTopicLength)
	if err != nil {
		return fmt.Errorf("locate non-empty 3-D-array plane: %w", err)
	}
	thirdPlaneStart := outerOffsetsStart + thirdPlaneRelative
	firstRowRelative, err := readVM64Offset(nested, thirdPlaneStart+common.LogTopicLength)
	if err != nil {
		return fmt.Errorf("locate non-empty nested uint16 row: %w", err)
	}
	firstRowStart := thirdPlaneStart + common.LogTopicLength + firstRowRelative
	firstUint16Word := firstRowStart + common.LogTopicLength
	if firstUint16Word < 0 || firstUint16Word+common.LogTopicLength > len(nested) {
		return fmt.Errorf("nested uint16 word at %d exceeds calldata length %d", firstUint16Word, len(nested))
	}
	dirtyUint16 := append([]byte{}, nested...)
	dirtyUint16[firstUint16Word] = 1

	dirtyBool := append([]byte{}, scalars...)
	if err := overwriteVM64Word(dirtyBool, 4, big.NewInt(2)); err != nil {
		return err
	}
	dirtyInt := append([]byte{}, scalars...)
	dirtyIntWord := make([]byte, common.LogTopicLength)
	dirtyIntWord[len(dirtyIntWord)-2] = 0xff
	dirtyIntWord[len(dirtyIntWord)-1] = 0xfe
	if err := overwriteVM64WordBytes(dirtyInt, 4+common.LogTopicLength, dirtyIntWord); err != nil {
		return err
	}
	dirtyBytesN := append([]byte{}, scalars...)
	dirtyByte := 4 + 2*common.LogTopicLength + 3
	if dirtyByte >= len(dirtyBytesN) {
		return fmt.Errorf("bytes3 dirty-padding position %d exceeds calldata length %d", dirtyByte, len(dirtyBytesN))
	}
	dirtyBytesN[dirtyByte] = 1

	vectors := []struct {
		name string
		data []byte
	}{
		{name: "short head", data: shortHead},
		{name: "truncated dynamic string tail", data: truncatedDynamicTail},
		{name: "dynamic string length exceeds calldata tail", data: oversizedDynamicTail},
		{name: "impossible dynamic length loaded through a head alias", data: impossibleLengthFromHead},
		{name: "offset outside calldata range", data: highOffset},
		{name: "impossible nested-array length loaded through an offset-table alias", data: nestedOffset},
		{name: "dirty nested uint16 high padding", data: dirtyUint16},
		{name: "dirty bool", data: dirtyBool},
		{name: "dirty int16 sign extension", data: dirtyInt},
		{name: "dirty bytes3 padding", data: dirtyBytesN},
	}
	for _, vector := range vectors {
		if err := rawCallMustRevert(ctx, caller, from, address, blockNumber, vector.data); err != nil {
			return fmt.Errorf("Hyperion compiler decoder accepted %s: %w", vector.name, err)
		}
	}
	return nil
}

func checkHyperionCompositeEvent(
	ctx context.Context,
	binding *bind.BoundContract,
	receipt *types.Receipt,
	eventLog *types.Log,
	recipient common.Address,
	amount *big.Int,
	tag [64]byte,
	payload []byte,
	note string,
) error {
	artifact, err := loadEventEmitterArtifact()
	if err != nil {
		return err
	}
	eventDef := artifact.ABI.Events["CompositeIndexed"]
	values := []*big.Int{amount, big.NewInt(7)}
	record := eventRecord{Amount: amount, Recipient: recipient, Tag: tag}
	dynamicValue := compositeDynamicRecord(amount, payload, note)
	wantData, err := eventDef.Inputs.NonIndexed().Pack(values, record, dynamicValue, note)
	if err != nil {
		return fmt.Errorf("pack canonical Hyperion composite event data: %w", err)
	}
	if !bytes.Equal(eventLog.Data, wantData) {
		return fmt.Errorf("compiler composite event data is non-canonical:\nhave %x\nwant %x", eventLog.Data, wantData)
	}
	valuesTopic, err := abi.MakeTopic(eventDef.Inputs[0].Type, values)
	if err != nil {
		return fmt.Errorf("make indexed array topic: %w", err)
	}
	recordTopic, err := abi.MakeTopic(eventDef.Inputs[1].Type, record)
	if err != nil {
		return fmt.Errorf("make indexed struct topic: %w", err)
	}
	dynamicTopic, err := abi.MakeTopic(eventDef.Inputs[2].Type, dynamicValue)
	if err != nil {
		return fmt.Errorf("make indexed dynamic-struct topic: %w", err)
	}
	wantTopics := []common.LogTopic{
		common.HashToLogTopic(eventDef.ID),
		valuesTopic,
		recordTopic,
		dynamicTopic,
	}
	if len(eventLog.Topics) != len(wantTopics) {
		return fmt.Errorf("Hyperion composite event topic count = %d, want %d", len(eventLog.Topics), len(wantTopics))
	}
	for i := range wantTopics {
		if eventLog.Topics[i] != wantTopics[i] {
			return fmt.Errorf("Hyperion composite event topic %d = %s, want %s", i, eventLog.Topics[i].Hex(), wantTopics[i].Hex())
		}
	}

	valuesHash, err := abi.MakeTopicHash(eventDef.Inputs[0].Type, values)
	if err != nil {
		return fmt.Errorf("indexed array topic: %w", err)
	}
	recordHash, err := abi.MakeTopicHash(eventDef.Inputs[1].Type, record)
	if err != nil {
		return fmt.Errorf("indexed struct topic: %w", err)
	}
	dynamicHash, err := abi.MakeTopicHash(eventDef.Inputs[2].Type, dynamicValue)
	if err != nil {
		return fmt.Errorf("indexed dynamic-struct topic: %w", err)
	}
	type decodedComposite struct {
		IndexedValues  common.Hash
		IndexedRecord  common.Hash
		IndexedDynamic common.Hash
		Values         []*big.Int
		Record         eventRecord
		DynamicRecord  dynamicRecord
		Note           string
	}
	wantDecoded := decodedComposite{
		IndexedValues: valuesHash, IndexedRecord: recordHash, IndexedDynamic: dynamicHash,
		Values: values, Record: record, DynamicRecord: dynamicValue, Note: note,
	}
	checkDecoded := func(log types.Log) error {
		decoded, err := unpackEvent(binding, "CompositeIndexed", log)
		if err != nil {
			return err
		}
		have := decodedComposite{
			IndexedValues:  decoded["indexedValues"].(common.Hash),
			IndexedRecord:  decoded["indexedRecord"].(common.Hash),
			IndexedDynamic: decoded["indexedDynamic"].(common.Hash),
			Values:         *abi.ConvertType(decoded["values"], new([]*big.Int)).(*[]*big.Int),
			Record:         *abi.ConvertType(decoded["record"], new(eventRecord)).(*eventRecord),
			DynamicRecord:  *abi.ConvertType(decoded["dynamicRecord"], new(dynamicRecord)).(*dynamicRecord),
			Note:           decoded["note"].(string),
		}
		if diff := cmp.Diff(wantDecoded, have, abiCompareOptions...); diff != "" {
			return fmt.Errorf("composite event mismatch (-want +have):\n%s", diff)
		}
		return nil
	}
	if err := checkDecoded(*eventLog); err != nil {
		return fmt.Errorf("decode Hyperion composite event: %w", err)
	}
	opts, err := receiptBlockRange(ctx, receipt)
	if err != nil {
		return err
	}
	filtered, err := oneFilteredLog(
		ctx,
		binding,
		opts,
		"CompositeIndexed",
		[]any{valuesHash},
		[]any{recordHash},
		[]any{dynamicHash},
	)
	if err != nil {
		return fmt.Errorf("filter Hyperion composite event: %w", err)
	}
	if filtered.TxHash != receipt.TxHash {
		return fmt.Errorf("filtered Hyperion composite transaction = %s, want %s", filtered.TxHash, receipt.TxHash)
	}
	if err := checkDecoded(filtered); err != nil {
		return fmt.Errorf("decode filtered Hyperion composite event: %w", err)
	}

	wrongHash := valuesHash
	wrongHash[0] ^= 0xff
	filtered, err = oneFilteredLog(
		ctx,
		binding,
		opts,
		"CompositeIndexed",
		[]any{wrongHash, valuesHash},
		nil,
		[]any{dynamicHash},
	)
	if err != nil {
		return fmt.Errorf("filter Hyperion composite event with OR/wildcard topics: %w", err)
	}
	if filtered.TxHash != receipt.TxHash {
		return fmt.Errorf("Hyperion composite OR/wildcard transaction = %s, want %s", filtered.TxHash, receipt.TxHash)
	}
	if err := requireNoFilteredLogs(ctx, binding, opts, "CompositeIndexed", []any{wrongHash}, nil, nil); err != nil {
		return fmt.Errorf("Hyperion composite mismatched filter: %w", err)
	}
	return nil
}

func rawCallMustRevert(
	ctx context.Context,
	caller qrl.ContractCaller,
	from common.Address,
	to common.Address,
	blockNumber *big.Int,
	data []byte,
) error {
	output, err := caller.CallContract(ctx, qrl.CallMsg{From: from, To: &to, Data: data}, blockNumber)
	if err == nil {
		return fmt.Errorf("call unexpectedly succeeded with output %x", output)
	}
	var dataError rpc.DataError
	if errors.As(err, &dataError) || errors.Is(err, vm.ErrExecutionReverted) {
		return nil
	}
	return fmt.Errorf("call returned %T instead of an execution revert: %w", err, err)
}

func rpcRevertData(err error) ([]byte, error) {
	var dataError rpc.DataError
	if !errors.As(err, &dataError) {
		return nil, fmt.Errorf("call returned %T, want rpc.DataError: %w", err, err)
	}
	encoded, ok := dataError.ErrorData().(string)
	if !ok {
		return nil, fmt.Errorf("revert data has type %T, want hex string", dataError.ErrorData())
	}
	decoded, decodeErr := hexutil.Decode(encoded)
	if decodeErr != nil {
		return nil, fmt.Errorf("decode revert data %q: %w", encoded, decodeErr)
	}
	return decoded, nil
}

func overwriteVM64Word(calldata []byte, offset int, value *big.Int) error {
	if value == nil || value.Sign() < 0 || value.BitLen() > common.LogTopicLength*8 {
		return fmt.Errorf("invalid VM64 word value %v", value)
	}
	return overwriteVM64WordBytes(calldata, offset, value.FillBytes(make([]byte, common.LogTopicLength)))
}

func overwriteVM64WordBytes(calldata []byte, offset int, word []byte) error {
	if len(word) != common.LogTopicLength {
		return fmt.Errorf("VM64 word has %d bytes, want %d", len(word), common.LogTopicLength)
	}
	if offset < 0 || offset+len(word) > len(calldata) {
		return fmt.Errorf("VM64 word offset %d exceeds calldata length %d", offset, len(calldata))
	}
	copy(calldata[offset:offset+len(word)], word)
	return nil
}

func readVM64Offset(calldata []byte, offset int) (int, error) {
	if offset < 0 || offset+common.LogTopicLength > len(calldata) {
		return 0, fmt.Errorf("VM64 offset word at %d exceeds calldata length %d", offset, len(calldata))
	}
	word := calldata[offset : offset+common.LogTopicLength]
	for _, value := range word[:common.LogTopicLength-8] {
		if value != 0 {
			return 0, errors.New("VM64 offset does not fit uint64")
		}
	}
	value := binary.BigEndian.Uint64(word[common.LogTopicLength-8:])
	if value > uint64(len(calldata)) {
		return 0, fmt.Errorf("VM64 offset %d exceeds calldata length %d", value, len(calldata))
	}
	return int(value), nil
}

func compositeDynamicRecord(amount *big.Int, payload []byte, note string) dynamicRecord {
	return dynamicRecord{
		Amount:  amount,
		Note:    note,
		Payload: payload,
		Values:  [][]uint16{{1, 0xffff}, {}},
	}
}
