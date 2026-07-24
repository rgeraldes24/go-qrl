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
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	qrlmath "github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/qrlclient/gqrlclient"
	"github.com/theQRL/go-qrl/rlp"
)

func checkVM64StorageSlots(ctx context.Context, client *qrlclient.Client, contract common.Address, block *big.Int, amount, delta *big.Int, tag [64]byte, recipient common.Address) error {
	want := []common.StorageValue64{
		common.BytesToStorageValue64(qrlmath.U512Bytes(new(big.Int).Set(amount))),
		common.BytesToStorageValue64(qrlmath.U512Bytes(new(big.Int).Set(delta))),
		common.StorageValue64(tag),
		common.StorageValue64(recipient),
	}
	for i, expected := range want {
		slot := common.BigToHash(big.NewInt(int64(i)))
		value, err := client.StorageAt(ctx, contract, slot, block)
		if err != nil {
			return fmt.Errorf("read VM64 storage slot %d: %w", i, err)
		}
		if !bytes.Equal(value, expected[:]) {
			return fmt.Errorf("VM64 storage slot %d mismatch:\nhave %x\nwant %x", i, value, expected)
		}
	}
	return nil
}

func checkStorageAPIs(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address, graphqlURL string) error {
	var value [common.StorageValue64Length]byte
	for i := range value {
		value[i] = byte(i + 1)
	}
	receipt, err := deployRaw(ctx, client, w, from, storageContractCode(value))
	if err != nil {
		return fmt.Errorf("deploy storage contract: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("storage contract deployment failed with status %d", receipt.Status)
	}
	slot := common.Hash{}
	storage, err := client.StorageAt(ctx, receipt.ContractAddress, slot, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("qrl_getStorageAt through qrlclient: %w", err)
	}
	if !bytes.Equal(storage, value[:]) {
		return fmt.Errorf("storage value mismatch:\nhave %x\nwant %x", storage, value)
	}
	output, err := client.CallContract(ctx, qrl.CallMsg{
		From: from,
		To:   &receipt.ContractAddress,
		Data: nil,
	}, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("qrl_call through qrlclient: %w", err)
	}
	if !bytes.Equal(output, value[:]) {
		return fmt.Errorf("contract call output mismatch:\nhave %x\nwant %x", output, value)
	}
	absentSlot := common.BigToHash(big.NewInt(0xdead))
	proof, err := gqrlclient.New(client.Client()).GetProof(ctx, receipt.ContractAddress, []string{slot.Hex(), absentSlot.Hex()}, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("qrl_getProof through gqrlclient: %w", err)
	}
	if len(proof.StorageProof) != 2 {
		return fmt.Errorf("storage proof length mismatch: have %d want 2", len(proof.StorageProof))
	}
	if proof.StorageProof[0].Key != slot.Hex() {
		return fmt.Errorf("storage proof key mismatch: have %s want %s", proof.StorageProof[0].Key, slot.Hex())
	}
	if proof.StorageProof[0].Value.Cmp(new(big.Int).SetBytes(value[:])) != 0 {
		return fmt.Errorf("storage proof value mismatch: have %s want 0x%x", proof.StorageProof[0].Value.Text(16), value)
	}
	if proof.StorageProof[1].Key != absentSlot.Hex() || proof.StorageProof[1].Value.Sign() != 0 {
		return fmt.Errorf("absent storage proof mismatch: %+v", proof.StorageProof[1])
	}

	header, err := client.HeaderByNumber(ctx, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("header for proof block: %w", err)
	}
	accountLeaf, err := verifyProofNodes(header.Root, crypto.Keccak256(receipt.ContractAddress.Bytes()), proof.AccountProof)
	if err != nil {
		return fmt.Errorf("verify account proof: %w", err)
	}
	if accountLeaf == nil {
		return fmt.Errorf("verified account proof returned an absent contract")
	}
	var account types.StateAccount
	if err := rlp.DecodeBytes(accountLeaf, &account); err != nil {
		return fmt.Errorf("decode account proof leaf: %w", err)
	}
	if account.Nonce != proof.Nonce || account.Balance.Cmp(proof.Balance) != 0 || account.Root != proof.StorageHash ||
		!bytes.Equal(account.CodeHash, proof.CodeHash[:]) {
		return fmt.Errorf("verified account leaf differs from RPC result: account=%+v proof=%+v", account, proof)
	}

	storageLeaf, err := verifyProofNodes(proof.StorageHash, crypto.Keccak256(slot.Bytes()), proof.StorageProof[0].Proof)
	if err != nil {
		return fmt.Errorf("verify storage inclusion proof: %w", err)
	}
	if storageLeaf == nil {
		return fmt.Errorf("verified storage proof returned an absent populated slot")
	}
	var trimmedStorage []byte
	if err := rlp.DecodeBytes(storageLeaf, &trimmedStorage); err != nil {
		return fmt.Errorf("decode storage proof leaf: %w", err)
	}
	if got := common.BytesToStorageValue64(trimmedStorage); got != common.StorageValue64(value) {
		return fmt.Errorf("verified storage leaf mismatch: have %x want %x", got, value)
	}

	absentLeaf, err := verifyProofNodes(proof.StorageHash, crypto.Keccak256(absentSlot.Bytes()), proof.StorageProof[1].Proof)
	if err != nil {
		return fmt.Errorf("verify storage absence proof: %w", err)
	}
	if absentLeaf != nil {
		return fmt.Errorf("verified storage absence proof returned value %x", absentLeaf)
	}
	tampered := append([]string(nil), proof.AccountProof...)
	if len(tampered) == 0 {
		return fmt.Errorf("account proof unexpectedly has no nodes")
	}
	tamperedNode, err := hexutil.Decode(tampered[0])
	if err != nil || len(tamperedNode) == 0 {
		return fmt.Errorf("decode account proof node for tamper check: %w", err)
	}
	tamperedNode[len(tamperedNode)/2] ^= 0x01
	tampered[0] = hexutil.Encode(tamperedNode)
	if _, err := verifyProofNodes(header.Root, crypto.Keccak256(receipt.ContractAddress.Bytes()), tampered); err == nil {
		return fmt.Errorf("tampered account proof unexpectedly verified")
	}
	if err := checkGraphQLStorage(ctx, graphqlURL, receipt.ContractAddress, receipt.BlockNumber, from, slot, value); err != nil {
		return err
	}
	return nil
}

func storageContractCode(value [common.StorageValue64Length]byte) []byte {
	runtime := []byte{
		byte(vm.PUSH1), 0x00,
		byte(vm.SLOAD),
		byte(vm.PUSH1), 0x00,
		byte(vm.MSTORE),
		byte(vm.PUSH1), common.StorageValue64Length,
		byte(vm.PUSH1), 0x00,
		byte(vm.RETURN),
	}
	code := []byte{byte(vm.PUSH64)}
	code = append(code, value[:]...)
	code = append(code, byte(vm.PUSH1), 0x00, byte(vm.SSTORE))
	code = append(code, byte(vm.PUSH1)+byte(len(runtime))-1)
	code = append(code, runtime...)
	code = append(code,
		byte(vm.PUSH1), 0x00,
		byte(vm.MSTORE),
		byte(vm.PUSH1), byte(len(runtime)),
		byte(vm.PUSH1), byte(common.StorageValue64Length-len(runtime)),
		byte(vm.RETURN),
	)
	return code
}
