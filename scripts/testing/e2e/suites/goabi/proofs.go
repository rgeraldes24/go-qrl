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
	"fmt"
	"math/big"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/qrlclient/gqrlclient"
	"github.com/theQRL/go-qrl/qrldb/memorydb"
	"github.com/theQRL/go-qrl/rlp"
	"github.com/theQRL/go-qrl/trie"
)

func verifyProofNodes(root common.Hash, key []byte, nodes []string) ([]byte, error) {
	db := memorydb.New()
	defer db.Close()
	for i, encoded := range nodes {
		node, err := hexutil.Decode(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode proof node %d: %w", i, err)
		}
		if err := db.Put(crypto.Keccak256(node), node); err != nil {
			return nil, fmt.Errorf("store proof node %d: %w", i, err)
		}
	}
	return trie.VerifyProof(root, key, db)
}

func checkAddressUpperHalfIsolation(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address) error {
	// Derive collision fixtures from the sender's current nonce so repeated
	// suites against the same developer network do not reuse funded accounts.
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return fmt.Errorf("upper-half address fixture nonce: %w", err)
	}
	first, second := upperHalfIsolationAddresses(from, nonce)
	if bytes.Equal(first[:common.AddressLength/2], second[:common.AddressLength/2]) ||
		!bytes.Equal(first[common.AddressLength/2:], second[common.AddressLength/2:]) {
		return fmt.Errorf("invalid upper-half address collision fixture")
	}
	if crypto.Keccak256Hash(first.Bytes()) == crypto.Keccak256Hash(second.Bytes()) {
		return fmt.Errorf("full-width address trie keys unexpectedly collide")
	}

	firstValue := big.NewInt(1111)
	secondValue := big.NewInt(2222)
	firstReceipt, err := sendValue(ctx, client, w, from, first, firstValue)
	if err != nil {
		return fmt.Errorf("fund first upper-half address: %w", err)
	}
	firstHeader, err := client.HeaderByNumber(ctx, firstReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("header after first upper-half address funding: %w", err)
	}
	firstAtFirstBlock, err := client.BalanceAt(ctx, first, firstReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("first upper-half address balance: %w", err)
	}
	secondAtFirstBlock, err := client.BalanceAt(ctx, second, firstReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("second upper-half address pre-funding balance: %w", err)
	}
	if firstAtFirstBlock.Cmp(firstValue) != 0 || secondAtFirstBlock.Sign() != 0 {
		return fmt.Errorf("upper-half address isolation failed before second funding: first=%s second=%s", firstAtFirstBlock, secondAtFirstBlock)
	}

	proofClient := gqrlclient.New(client.Client())
	firstProof, err := proofClient.GetProof(ctx, first, nil, firstReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("first upper-half address proof: %w", err)
	}
	secondAbsentProof, err := proofClient.GetProof(ctx, second, nil, firstReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("second upper-half address absence proof: %w", err)
	}
	if err := verifyAccountProof(firstHeader.Root, first, firstProof, firstValue, true); err != nil {
		return fmt.Errorf("verify first upper-half address proof: %w", err)
	}
	if err := verifyAccountProof(firstHeader.Root, second, secondAbsentProof, new(big.Int), false); err != nil {
		return fmt.Errorf("verify second upper-half address absence proof: %w", err)
	}

	secondReceipt, err := sendValue(ctx, client, w, from, second, secondValue)
	if err != nil {
		return fmt.Errorf("fund second upper-half address: %w", err)
	}
	secondHeader, err := client.HeaderByNumber(ctx, secondReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("header after second upper-half address funding: %w", err)
	}
	if secondHeader.Root == firstHeader.Root {
		return fmt.Errorf("state root did not change after funding the second upper-half address")
	}
	firstBalance, err := client.BalanceAt(ctx, first, secondReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("first upper-half address final balance: %w", err)
	}
	secondBalance, err := client.BalanceAt(ctx, second, secondReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("second upper-half address final balance: %w", err)
	}
	if firstBalance.Cmp(firstValue) != 0 || secondBalance.Cmp(secondValue) != 0 {
		return fmt.Errorf("64-byte addresses with equal low halves aliased: first=%s second=%s", firstBalance, secondBalance)
	}

	firstProof, err = proofClient.GetProof(ctx, first, nil, secondReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("first upper-half address final proof: %w", err)
	}
	secondProof, err := proofClient.GetProof(ctx, second, nil, secondReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("second upper-half address final proof: %w", err)
	}
	if err := verifyAccountProof(secondHeader.Root, first, firstProof, firstValue, true); err != nil {
		return fmt.Errorf("verify first upper-half address final proof: %w", err)
	}
	if err := verifyAccountProof(secondHeader.Root, second, secondProof, secondValue, true); err != nil {
		return fmt.Errorf("verify second upper-half address final proof: %w", err)
	}
	return nil
}

func upperHalfIsolationAddresses(from common.Address, nonce uint64) (first, second common.Address) {
	var encodedNonce [8]byte
	binary.BigEndian.PutUint64(encodedNonce[:], nonce)
	seed := crypto.Keccak256(
		[]byte("go-qrl/vm64-address-isolation/v1"),
		from.Bytes(),
		encodedNonce[:],
	)
	firstUpper := crypto.Keccak256(seed, []byte("first-upper"))
	secondUpper := crypto.Keccak256(seed, []byte("second-upper"))
	sharedLower := crypto.Keccak256(seed, []byte("shared-lower"))
	copy(first[:common.AddressLength/2], firstUpper)
	copy(second[:common.AddressLength/2], secondUpper)
	copy(first[common.AddressLength/2:], sharedLower)
	copy(second[common.AddressLength/2:], sharedLower)
	return first, second
}

func verifyAccountProof(root common.Hash, address common.Address, proof *gqrlclient.AccountResult, wantBalance *big.Int, wantExists bool) error {
	if proof.Address != address || proof.Balance.Cmp(wantBalance) != 0 {
		return fmt.Errorf("RPC proof identity mismatch: address=%s balance=%s", proof.Address, proof.Balance)
	}
	leaf, err := verifyProofNodes(root, crypto.Keccak256(address.Bytes()), proof.AccountProof)
	if err != nil {
		return err
	}
	if !wantExists {
		if leaf != nil {
			return fmt.Errorf("absence proof returned account leaf %x", leaf)
		}
		return nil
	}
	if leaf == nil {
		return fmt.Errorf("inclusion proof returned no account leaf")
	}
	var account types.StateAccount
	if err := rlp.DecodeBytes(leaf, &account); err != nil {
		return fmt.Errorf("decode account leaf: %w", err)
	}
	if account.Balance.Cmp(wantBalance) != 0 || account.Nonce != proof.Nonce || account.Root != proof.StorageHash ||
		!bytes.Equal(account.CodeHash, proof.CodeHash[:]) {
		return fmt.Errorf("account leaf differs from RPC proof: account=%+v proof=%+v", account, proof)
	}
	return nil
}
