// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

func run(parent context.Context, cfg config, runner commandRunner) error {
	ctx, cancel := context.WithTimeout(parent, cfg.timeout)
	defer cancel()
	if err := cfg.resolveEndpoints(ctx, runner); err != nil {
		return err
	}
	data, imageID, err := generateDepositData(ctx, runner, cfg.generatorImage, cfg.withdrawal, cfg.forkVersion)
	if err != nil {
		return err
	}
	log.Printf("depositcheck: generator=%s image_id=%s", cfg.generatorImage, imageID)
	log.Printf("depositcheck: validators=%d indices=%d..%d validator_pubkey_bytes=%d withdrawal=%s", len(data), deterministicIndex, deterministicIndex+len(data)-1, len(data[0].publicKey), cfg.withdrawal.Hex())
	manifest, err := loadDepositContractManifest(ctx, runner, imageID)
	if err != nil {
		return err
	}
	log.Printf("depositcheck: deposit_runtime_bytes=%d sha256=%s storage_layout=%s", manifest.RuntimeCodeBytes, manifest.RuntimeCodeSHA256, manifest.StorageLayout)

	parsed, err := parseDepositABI()
	if err != nil {
		return err
	}
	clients := [2]*qrlclient.Client{}
	for i, endpoint := range cfg.rpcURLs {
		clients[i], err = qrlclient.Dial(endpoint)
		if err != nil {
			return fmt.Errorf("dial execution node %d at %s: %w", i+1, endpoint, err)
		}
		defer clients[i].Close()
	}
	chainID1, err := clients[0].ChainID(ctx)
	if err != nil {
		return fmt.Errorf("execution node 1 chain ID: %w", err)
	}
	chainID2, err := clients[1].ChainID(ctx)
	if err != nil {
		return fmt.Errorf("execution node 2 chain ID: %w", err)
	}
	if chainID1.Cmp(chainID2) != 0 {
		return fmt.Errorf("execution nodes disagree on chain ID: %s vs %s", chainID1, chainID2)
	}

	before := [2]contractState{}
	for i, client := range clients {
		before[i], err = readContractState(ctx, client, cfg.depositContract, parsed)
		if err != nil {
			return fmt.Errorf("execution node %d deposit-contract preflight: %w", i+1, err)
		}
	}
	for i, client := range clients {
		codeDigest := sha256.Sum256(before[i].code)
		if uint64(len(before[i].code)) != manifest.RuntimeCodeBytes || hex.EncodeToString(codeDigest[:]) != manifest.RuntimeCodeSHA256 {
			return fmt.Errorf("execution node %d deposit runtime provenance mismatch: bytes=%d sha256=%x; want bytes=%d sha256=%s from generator image %s", i+1, len(before[i].code), codeDigest, manifest.RuntimeCodeBytes, manifest.RuntimeCodeSHA256, imageID)
		}
		if err := verifyDepositStorage(ctx, client, cfg.depositContract, manifest); err != nil {
			return fmt.Errorf("execution node %d deposit storage provenance: %w", i+1, err)
		}
		if err := verifyDepositBranchStorage(ctx, client, cfg.depositContract, nil); err != nil {
			return fmt.Errorf("execution node %d empty deposit branch storage: %w", i+1, err)
		}
	}
	if !bytes.Equal(before[0].code, before[1].code) {
		return fmt.Errorf("execution nodes disagree on deposit contract bytecode")
	}
	log.Printf("depositcheck: constructor-equivalent VM64 storage verified on both nodes sha256=%s", manifest.StorageSHA256)
	for i := range before {
		if before[i].count != 0 || before[i].root != [32]byte(emptyDepositRoot) || before[i].balance.Sign() != 0 {
			return fmt.Errorf("execution node %d deposit contract is not fresh: count=%d root=0x%x balance=%s", i+1, before[i].count, before[i].root, before[i].balance)
		}
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	for i := range data {
		if err := requireUnknownValidator(ctx, httpClient, cfg.clURLs, data[i].publicKey); err != nil {
			return fmt.Errorf("validator index %d preflight: %w", deterministicIndex+i, err)
		}
	}
	w, err := wallet.RestoreFromSeedHex(cfg.fundingSeed)
	if err != nil {
		return fmt.Errorf("restore prefunded wallet for verification: %w", err)
	}
	from := common.Address(w.GetAddress())
	confirmedNonce, err := clients[0].NonceAt(ctx, from, nil)
	if err != nil {
		return fmt.Errorf("read confirmed funding nonce: %w", err)
	}
	pendingNonce, err := clients[0].PendingNonceAt(ctx, from)
	if err != nil {
		return fmt.Errorf("read pending funding nonce: %w", err)
	}
	if pendingNonce != confirmedNonce {
		return fmt.Errorf("funding account %s has pending transactions (confirmed nonce %d, pending nonce %d); inspect the pending transaction and recreate the enclave before rerunning depositcheck", from.Hex(), confirmedNonce, pendingNonce)
	}
	if confirmedNonce > ^uint64(0)-uint64(len(data)) {
		return fmt.Errorf("funding nonce %d cannot accommodate %d deposits", confirmedNonce, len(data))
	}
	results := make([]depositResult, len(data))
	leaves := make([][32]byte, 0, len(data))
	wantBalance := new(big.Int)
	for depositIndex := range data {
		expectedNonce := confirmedNonce + uint64(depositIndex)
		result, err := submitDeposit(ctx, clients[0], cfg.fundingSeed, cfg.depositContract, data[depositIndex], parsed)
		if err != nil {
			return fmt.Errorf("submit validator index %d: %w", deterministicIndex+depositIndex, err)
		}
		results[depositIndex] = result
		if err := verifyDepositTransaction(result.tx, result.receipt, cfg.depositContract, data[depositIndex], parsed, from, uint64(depositIndex), expectedNonce); err != nil {
			return fmt.Errorf("execution node 1 validator index %d: %w", deterministicIndex+depositIndex, err)
		}
		if _, err := verifyRemoteDeposit(ctx, clients[1], result, cfg.depositContract, data[depositIndex], parsed, from, uint64(depositIndex), expectedNonce); err != nil {
			return fmt.Errorf("validator index %d: %w", deterministicIndex+depositIndex, err)
		}

		leaves = append(leaves, data[depositIndex].root)
		wantRoot, err := depositTreeRoot(leaves)
		if err != nil {
			return err
		}
		wantBalance.Add(wantBalance, result.tx.Value())
		wantCount := uint64(depositIndex + 1)
		for nodeIndex, client := range clients {
			after, err := readContractState(ctx, client, cfg.depositContract, parsed)
			if err != nil {
				return fmt.Errorf("execution node %d deposit-contract post-state after deposit %d: %w", nodeIndex+1, wantCount, err)
			}
			if after.count != wantCount || after.root != wantRoot || after.balance.Cmp(wantBalance) != 0 {
				return fmt.Errorf("execution node %d deposit state mismatch after deposit %d: count=%d root=0x%x balance=%s; want count=%d root=0x%x balance=%s", nodeIndex+1, wantCount, after.count, after.root, after.balance, wantCount, wantRoot, wantBalance)
			}
			if err := verifyDepositBranchStorage(ctx, client, cfg.depositContract, leaves); err != nil {
				return fmt.Errorf("execution node %d deposit branch after deposit %d: %w", nodeIndex+1, wantCount, err)
			}
		}
		log.Printf("depositcheck: deposit %d/%d execution receipt/event/root/count/packed-branch verified on both nodes tx=%s block=%d", wantCount, len(data), result.tx.Hash().Hex(), result.receipt.BlockNumber.Uint64())
	}
	wantFinalNonce := confirmedNonce + uint64(len(data))
	for i, client := range clients {
		finalNonce, err := client.NonceAt(ctx, from, nil)
		if err != nil {
			return fmt.Errorf("execution node %d final confirmed funding nonce: %w", i+1, err)
		}
		finalPendingNonce, err := client.PendingNonceAt(ctx, from)
		if err != nil {
			return fmt.Errorf("execution node %d final pending funding nonce: %w", i+1, err)
		}
		if finalNonce != wantFinalNonce || finalPendingNonce != wantFinalNonce {
			return fmt.Errorf("execution node %d funding nonce after deposits is confirmed=%d pending=%d, want %d", i+1, finalNonce, finalPendingNonce, wantFinalNonce)
		}
	}
	for i := range data {
		block := results[i].receipt.BlockNumber.Uint64()
		statuses, err := waitForBeaconIngestion(ctx, httpClient, cfg.clURLs, data[i].publicKey, block, cfg.pollInterval)
		if err != nil {
			return fmt.Errorf("validator index %d beacon ingestion: %w", deterministicIndex+i, err)
		}
		log.Printf("depositcheck: validator index %d beacon ingestion verified at execution block %d (node1=%s node2=%s)", deterministicIndex+i, block, statuses[0].status, statuses[1].status)
	}
	return nil
}
