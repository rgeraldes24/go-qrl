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
	"sync"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/testdata/contracts"
)

type dynamicRecord = EventEmitterDynamicRecord

type eventRecord struct {
	Amount    *big.Int
	Recipient common.Address
	Tag       [64]byte
}

type contractCall struct {
	Method string
	Args   []any
	Want   []any
}

var (
	loadEventEmitterArtifact = sync.OnceValues(func() (*contracts.Artifact, error) {
		return contracts.Load("EventEmitter")
	})
	loadAdvancedABIArtifact = sync.OnceValues(func() (*contracts.Artifact, error) {
		return contracts.Load("AdvancedABI")
	})
	abiCompareOptions = []cmp.Option{
		cmpopts.EquateEmpty(),
		cmp.Comparer(func(left, right *big.Int) bool {
			if left == nil || right == nil {
				return left == nil && right == nil
			}
			return left.Cmp(right) == 0
		}),
	}
)

func bindArtifact(artifact *contracts.Artifact, address common.Address, backend bind.ContractBackend) *bind.BoundContract {
	return bind.NewBoundContract(address, artifact.ABI, backend, backend, backend)
}

// checkContractCall exercises BoundContract's decoder and independently proves
// that the compiler returned the canonical ABI bytes.
func checkContractCall(
	ctx context.Context,
	caller qrl.ContractCaller,
	from, address common.Address,
	blockNumber *big.Int,
	contract *bind.BoundContract,
	parsed *abi.ABI,
	vector contractCall,
) error {
	var decoded []any
	if err := contract.Call(&bind.CallOpts{Context: ctx, BlockNumber: blockNumber}, &decoded, vector.Method, vector.Args...); err != nil {
		return fmt.Errorf("%s through BoundContract: %w", vector.Method, err)
	}
	want, err := parsed.Methods[vector.Method].Outputs.Pack(vector.Want...)
	if err != nil {
		return fmt.Errorf("pack canonical %s output: %w", vector.Method, err)
	}
	repacked, err := parsed.Methods[vector.Method].Outputs.Pack(decoded...)
	if err != nil {
		return fmt.Errorf("repack BoundContract %s output: %w", vector.Method, err)
	}
	if !bytes.Equal(repacked, want) {
		return fmt.Errorf("BoundContract %s output is non-canonical:\nhave %x\nwant %x", vector.Method, repacked, want)
	}
	input, err := parsed.Pack(vector.Method, vector.Args...)
	if err != nil {
		return fmt.Errorf("pack %s input: %w", vector.Method, err)
	}
	raw, err := caller.CallContract(ctx, qrl.CallMsg{From: from, To: &address, Data: input}, blockNumber)
	if err != nil {
		return fmt.Errorf("raw %s call: %w", vector.Method, err)
	}
	if !bytes.Equal(raw, want) {
		return fmt.Errorf("compiler %s output is non-canonical:\nhave %x\nwant %x", vector.Method, raw, want)
	}
	return nil
}

func errorBySignature(parsed *abi.ABI, signature string) (*abi.Error, error) {
	for _, definition := range parsed.Errors {
		if definition.Sig == signature {
			resolved := definition
			return &resolved, nil
		}
	}
	return nil, fmt.Errorf("ABI has no error with signature %q", signature)
}

func checkContractError(
	parsed *abi.ABI,
	contract *bind.BoundContract,
	callOpts *bind.CallOpts,
	method, signature string,
	values ...any,
) error {
	var output []any
	callErr := contract.Call(callOpts, &output, method, values...)
	if callErr == nil {
		return fmt.Errorf("%s unexpectedly succeeded", signature)
	}
	revertData, err := rpcRevertData(callErr)
	if err != nil {
		return fmt.Errorf("%s revert data: %w", signature, err)
	}
	definition, err := errorBySignature(parsed, signature)
	if err != nil {
		return err
	}
	body, err := definition.Inputs.Pack(values...)
	if err != nil {
		return fmt.Errorf("pack %s: %w", signature, err)
	}
	want := append(append([]byte{}, definition.ID[:4]...), body...)
	if !bytes.Equal(revertData, want) {
		return fmt.Errorf("%s compiler revert differs from Go ABI encoding:\nhave %x\nwant %x", signature, revertData, want)
	}
	var selector [4]byte
	copy(selector[:], revertData)
	resolved, err := parsed.ErrorByID(selector)
	if err != nil {
		return fmt.Errorf("ErrorByID(%s): %w", signature, err)
	}
	if resolved.Sig != signature {
		return fmt.Errorf("ErrorByID(%s) resolved %q", signature, resolved.Sig)
	}
	decoded, err := resolved.Unpack(revertData)
	if err != nil {
		return fmt.Errorf("decode %s: %w", signature, err)
	}
	decodedValues, ok := decoded.([]any)
	if !ok {
		return fmt.Errorf("decoded %s has type %T", signature, decoded)
	}
	repacked, err := resolved.Inputs.Pack(decodedValues...)
	if err != nil {
		return fmt.Errorf("repack %s: %w", signature, err)
	}
	if !bytes.Equal(repacked, body) {
		return fmt.Errorf("decoded %s did not round-trip:\nhave %x\nwant %x", signature, repacked, body)
	}
	return nil
}

func filterContractLogs(
	ctx context.Context,
	contract *bind.BoundContract,
	opts *bind.FilterOpts,
	event string,
	query ...[]any,
) ([]types.Log, error) {
	logs, subscription, err := contract.FilterLogs(opts, event, query...)
	if err != nil {
		return nil, err
	}
	defer subscription.Unsubscribe()
	var found []types.Log
	for {
		select {
		case log := <-logs:
			found = append(found, log)
		case err, open := <-subscription.Err():
			if open && err != nil {
				return nil, err
			}
			for {
				select {
				case log := <-logs:
					found = append(found, log)
				default:
					return found, nil
				}
			}
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func oneFilteredLog(
	ctx context.Context,
	contract *bind.BoundContract,
	opts *bind.FilterOpts,
	event string,
	query ...[]any,
) (types.Log, error) {
	logs, err := filterContractLogs(ctx, contract, opts, event, query...)
	if err != nil {
		return types.Log{}, err
	}
	if len(logs) != 1 {
		return types.Log{}, fmt.Errorf("%s filter returned %d logs, want one", event, len(logs))
	}
	return logs[0], nil
}

func unpackEvent(contract *bind.BoundContract, name string, log types.Log) (map[string]any, error) {
	values := make(map[string]any)
	if err := contract.UnpackLogIntoMap(values, name, log); err != nil {
		return nil, err
	}
	return values, nil
}

func requireNoFilteredLogs(
	ctx context.Context,
	contract *bind.BoundContract,
	opts *bind.FilterOpts,
	event string,
	query ...[]any,
) error {
	logs, err := filterContractLogs(ctx, contract, opts, event, query...)
	if err != nil {
		return err
	}
	if len(logs) != 0 {
		return fmt.Errorf("%s filter returned %d logs, want none", event, len(logs))
	}
	return nil
}

func receiptBlockRange(ctx context.Context, receipt *types.Receipt) (*bind.FilterOpts, error) {
	if receipt == nil || receipt.BlockNumber == nil {
		return nil, errors.New("receipt has no block number")
	}
	end := receipt.BlockNumber.Uint64()
	return &bind.FilterOpts{Start: end, End: &end, Context: ctx}, nil
}
