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
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/qrlclient"
)

// TransactionRecorder durably records a broadcast transaction before the
// suite starts any receipt or beacon wait. Implementations must not return
// until the label and hash are durable. Labels are deposit-0 through
// deposit-2, in transaction submission order.
type TransactionRecorder interface {
	RecordTransaction(label, hash string) error
}

// TransactionRecorderFunc adapts a function, including a closure around
// lifecycle.Checkpoint.RecordTransaction, to TransactionRecorder.
type TransactionRecorderFunc func(label, hash string) error

func (fn TransactionRecorderFunc) RecordTransaction(label, hash string) error {
	return fn(label, hash)
}

type PreparedTransaction struct {
	Hash string
	Raw  string
}

type PreparedTransactionRecorder interface {
	RecordPreparedTransaction(label, hash, raw string) error
}

type PreparedTransactionRecorderFunc func(label, hash, raw string) error

func (fn PreparedTransactionRecorderFunc) RecordPreparedTransaction(label, hash, raw string) error {
	return fn(label, hash, raw)
}

// Options contains lifecycle integrations that are deliberately separate
// from Config so configuration digests never include process-local callbacks.
type Options struct {
	TransactionRecorder         TransactionRecorder
	PreparedTransactionRecorder PreparedTransactionRecorder
	// RecordedTransactions contains the durable prefix from a previous
	// attempt. Only deposit-0 through deposit-2 are accepted.
	RecordedTransactions map[string]string
	PreparedTransactions map[string]PreparedTransaction
}

// Run executes the complete three-deposit VM64 lifecycle suite.
func Run(parent context.Context, cfg Config, options Options) error {
	return run(parent, cfg, execRunner{}, options.TransactionRecorder, options.PreparedTransactionRecorder, options.RecordedTransactions, options.PreparedTransactions)
}

func run(parent context.Context, cfg Config, runner commandRunner, recorder TransactionRecorder, preparedRecorder PreparedTransactionRecorder, recordedValues map[string]string, preparedValues map[string]PreparedTransaction) error {
	ctx, cancel := context.WithTimeout(parent, cfg.Timeout)
	defer cancel()
	if err := cfg.resolveEndpoints(ctx, runner); err != nil {
		return err
	}
	data, imageID, err := generateDepositData(ctx, runner, cfg.GeneratorImage, cfg.Withdrawal, cfg.ForkVersion)
	if err != nil {
		return err
	}
	log.Printf("depositcheck: generator=%s image_id=%s", cfg.GeneratorImage, imageID)
	log.Printf("depositcheck: validators=%d indices=%d..%d validator_pubkey_bytes=%d withdrawal=%s", len(data), deterministicIndex, deterministicIndex+len(data)-1, len(data[0].publicKey), cfg.Withdrawal.Hex())
	manifest, err := loadDepositContractManifest(ctx, runner, imageID)
	if err != nil {
		return err
	}
	log.Printf("depositcheck: deposit_runtime_bytes=%d sha256=%s storage_layout=%s", manifest.RuntimeCodeBytes, manifest.RuntimeCodeSHA256, manifest.StorageLayout)

	parsed, err := parseDepositABI()
	if err != nil {
		return err
	}
	recorded, err := validateRecordedDeposits(recordedValues, len(data))
	if err != nil {
		return err
	}
	prepared, err := validatePreparedDeposits(preparedValues, recorded, len(data))
	if err != nil {
		return err
	}
	clients := [2]*qrlclient.Client{}
	for i, endpoint := range cfg.RPCURLs {
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
	w, err := wallet.RestoreFromSeedHex(cfg.FundingSeed)
	if err != nil {
		return fmt.Errorf("restore prefunded wallet for verification: %w", err)
	}
	from := common.Address(w.GetAddress())

	// Durable submission evidence can precede mining. Reconcile every exact
	// prepared transaction before settling its recorded hash; this also covers
	// a transaction that was recorded, later dropped from the txpool, and must
	// be rebroadcast from the immutable signed bytes. Then reconcile the one
	// allowed prepared-only successor before comparing current contract state
	// with the journal prefix.
	settled := make(map[int]depositResult, len(recorded))
	for index := 0; index < len(recorded); index++ {
		if tx := prepared[index]; tx != nil {
			expectedNonce, nonceErr := expectedPreparedDepositNonce(ctx, clients, from, index, len(recorded), settled, tx)
			if nonceErr != nil {
				return fmt.Errorf("prepared deposit-%d nonce: %w", index, nonceErr)
			}
			if err := validateAndEnsurePreparedDeposit(ctx, clients[0], tx, cfg.DepositContract, data[index], parsed, from, expectedNonce, chainID1); err != nil {
				return fmt.Errorf("prepared deposit-%d semantics: %w", index, err)
			}
		}
		for nodeIndex, client := range clients {
			result, err := loadRecordedDeposit(ctx, client, recorded[index])
			if err != nil {
				return fmt.Errorf("settle recorded deposit-%d on execution node %d: %w", index, nodeIndex+1, err)
			}
			if nodeIndex == 0 {
				settled[index] = result
			}
		}
	}
	if index := len(recorded); prepared[index] != nil {
		tx := prepared[index]
		expectedNonce, nonceErr := expectedPreparedDepositNonce(ctx, clients, from, index, index+1, settled, tx)
		if nonceErr != nil {
			return fmt.Errorf("prepared deposit-%d nonce: %w", index, nonceErr)
		}
		if err := validateAndEnsurePreparedDeposit(ctx, clients[0], tx, cfg.DepositContract, data[index], parsed, from, expectedNonce, chainID1); err != nil {
			return fmt.Errorf("prepared deposit-%d semantics: %w", index, err)
		}
		if recorder != nil {
			if err := recorder.RecordTransaction(fmt.Sprintf("deposit-%d", index), tx.Hash().Hex()); err != nil {
				return fmt.Errorf("transaction %s as deposit-%d was submitted but could not be recorded: %w", tx.Hash(), index, err)
			}
		}
		recorded[index] = tx.Hash()
		for nodeIndex, client := range clients {
			if _, err := loadRecordedDeposit(ctx, client, tx.Hash()); err != nil {
				return fmt.Errorf("settle prepared deposit-%d on execution node %d: %w", index, nodeIndex+1, err)
			}
		}
	}

	recordedCount := len(recorded)
	prefixLeaves := make([][32]byte, recordedCount)
	wantBeforeBalance := new(big.Int)
	for index := 0; index < recordedCount; index++ {
		prefixLeaves[index] = data[index].root
		wantBeforeBalance.Add(wantBeforeBalance, new(big.Int).Mul(new(big.Int).SetUint64(data[index].amount), big.NewInt(params.Shor)))
	}
	wantBeforeRoot, err := depositTreeRoot(prefixLeaves)
	if err != nil {
		return err
	}

	before := [2]contractState{}
	for i, client := range clients {
		before[i], err = readContractState(ctx, client, cfg.DepositContract, parsed)
		if err != nil {
			return fmt.Errorf("execution node %d deposit-contract preflight: %w", i+1, err)
		}
	}
	for i, client := range clients {
		codeDigest := sha256.Sum256(before[i].code)
		if uint64(len(before[i].code)) != manifest.RuntimeCodeBytes || hex.EncodeToString(codeDigest[:]) != manifest.RuntimeCodeSHA256 {
			return fmt.Errorf("execution node %d deposit runtime provenance mismatch: bytes=%d sha256=%x; want bytes=%d sha256=%s from generator image %s", i+1, len(before[i].code), codeDigest, manifest.RuntimeCodeBytes, manifest.RuntimeCodeSHA256, imageID)
		}
		if err := verifyDepositStorage(ctx, client, cfg.DepositContract, manifest); err != nil {
			return fmt.Errorf("execution node %d deposit storage provenance: %w", i+1, err)
		}
		if err := verifyDepositBranchStorage(ctx, client, cfg.DepositContract, prefixLeaves, nil); err != nil {
			return fmt.Errorf("execution node %d checkpointed deposit branch storage: %w", i+1, err)
		}
	}
	if !bytes.Equal(before[0].code, before[1].code) {
		return fmt.Errorf("execution nodes disagree on deposit contract bytecode")
	}
	log.Printf("depositcheck: constructor-equivalent VM64 storage verified on both nodes sha256=%s", manifest.StorageSHA256)
	for i := range before {
		if before[i].count != uint64(recordedCount) || before[i].root != wantBeforeRoot || before[i].balance.Cmp(wantBeforeBalance) != 0 {
			return fmt.Errorf("execution node %d deposit contract does not match the %d recorded deposits: count=%d root=0x%x balance=%s; want count=%d root=0x%x balance=%s", i+1, recordedCount, before[i].count, before[i].root, before[i].balance, recordedCount, wantBeforeRoot, wantBeforeBalance)
		}
	}

	httpClient := &http.Client{Timeout: 10 * time.Second}
	for i := recordedCount; i < len(data); i++ {
		if err := requireUnknownValidator(ctx, httpClient, cfg.CLURLs, data[i].publicKey); err != nil {
			return fmt.Errorf("validator index %d preflight: %w", deterministicIndex+i, err)
		}
	}
	confirmedNonce, err := clients[0].NonceAt(ctx, from, nil)
	if err != nil {
		return fmt.Errorf("read confirmed funding nonce: %w", err)
	}
	pendingNonce, err := clients[0].PendingNonceAt(ctx, from)
	if err != nil {
		return fmt.Errorf("read pending funding nonce: %w", err)
	}
	if pendingNonce != confirmedNonce {
		return fmt.Errorf("funding account %s has unjournaled pending transactions (confirmed nonce %d, pending nonce %d); inspect them and restore the matching lifecycle checkpoint before resuming", from.Hex(), confirmedNonce, pendingNonce)
	}
	var initialNonce uint64
	if recordedCount == 0 {
		initialNonce = confirmedNonce
	} else {
		first, err := loadRecordedDeposit(ctx, clients[0], recorded[0])
		if err != nil {
			return fmt.Errorf("load recorded deposit-0: %w", err)
		}
		initialNonce = first.tx.Nonce()
		if initialNonce > ^uint64(0)-uint64(recordedCount) || initialNonce+uint64(recordedCount) != confirmedNonce {
			return fmt.Errorf("funding nonce %d does not follow the %d recorded deposits beginning at nonce %d", confirmedNonce, recordedCount, initialNonce)
		}
	}
	if initialNonce > ^uint64(0)-uint64(len(data)) {
		return fmt.Errorf("funding nonce %d cannot accommodate %d deposits", initialNonce, len(data))
	}
	results := make([]depositResult, len(data))
	leaves := make([][32]byte, 0, len(data))
	wantBalance := new(big.Int)
	for depositIndex := range data {
		expectedNonce := initialNonce + uint64(depositIndex)
		var result depositResult
		if hash, ok := recorded[depositIndex]; ok {
			result, err = loadRecordedDeposit(ctx, clients[0], hash)
		} else {
			result, err = submitDeposit(ctx, clients[0], cfg.FundingSeed, cfg.DepositContract, data[depositIndex], parsed, recorder, preparedRecorder, fmt.Sprintf("deposit-%d", depositIndex))
		}
		if err != nil {
			return fmt.Errorf("submit validator index %d: %w", deterministicIndex+depositIndex, err)
		}
		results[depositIndex] = result
		if err := verifyDepositTransaction(result.tx, result.receipt, cfg.DepositContract, data[depositIndex], parsed, from, uint64(depositIndex), expectedNonce); err != nil {
			return fmt.Errorf("execution node 1 validator index %d: %w", deterministicIndex+depositIndex, err)
		}
		if _, err := verifyRemoteDeposit(ctx, clients[1], result, cfg.DepositContract, data[depositIndex], parsed, from, uint64(depositIndex), expectedNonce); err != nil {
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
			after, err := readContractStateAt(ctx, client, cfg.DepositContract, parsed, result.receipt.BlockNumber)
			if err != nil {
				return fmt.Errorf("execution node %d deposit-contract post-state after deposit %d: %w", nodeIndex+1, wantCount, err)
			}
			if after.count != wantCount || after.root != wantRoot || after.balance.Cmp(wantBalance) != 0 {
				return fmt.Errorf("execution node %d deposit state mismatch after deposit %d: count=%d root=0x%x balance=%s; want count=%d root=0x%x balance=%s", nodeIndex+1, wantCount, after.count, after.root, after.balance, wantCount, wantRoot, wantBalance)
			}
			if err := verifyDepositBranchStorage(ctx, client, cfg.DepositContract, leaves, result.receipt.BlockNumber); err != nil {
				return fmt.Errorf("execution node %d deposit branch after deposit %d: %w", nodeIndex+1, wantCount, err)
			}
		}
		log.Printf("depositcheck: deposit %d/%d execution receipt/event/root/count/packed-branch verified on both nodes tx=%s block=%d", wantCount, len(data), result.tx.Hash().Hex(), result.receipt.BlockNumber.Uint64())
	}
	wantFinalNonce := initialNonce + uint64(len(data))
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
		statuses, err := waitForBeaconIngestion(ctx, httpClient, cfg.CLURLs, data[i].publicKey, block, cfg.PollInterval)
		if err != nil {
			return fmt.Errorf("validator index %d beacon ingestion: %w", deterministicIndex+i, err)
		}
		log.Printf("depositcheck: validator index %d beacon ingestion verified at execution block %d (node1=%s node2=%s)", deterministicIndex+i, block, statuses[0].status, statuses[1].status)
	}
	return nil
}

func expectedPreparedDepositNonce(ctx context.Context, clients [2]*qrlclient.Client, from common.Address, index, durablePrefix int, settled map[int]depositResult, tx *types.Transaction) (uint64, error) {
	readers := [2]depositNonceReader{clients[0], clients[1]}
	return expectedPreparedDepositNonceFromReaders(ctx, readers, from, index, durablePrefix, settled, tx)
}

func expectedPreparedDepositNonceFromReaders(ctx context.Context, clients [2]depositNonceReader, from common.Address, index, durablePrefix int, settled map[int]depositResult, tx *types.Transaction) (uint64, error) {
	if tx == nil {
		return 0, fmt.Errorf("prepared transaction is nil")
	}
	if index < 0 {
		return 0, fmt.Errorf("deposit index %d is negative", index)
	}
	if index > 0 {
		first, ok := settled[0]
		if !ok || first.tx == nil {
			return 0, fmt.Errorf("deposit-0 is not settled before deposit-%d", index)
		}
		if first.tx.Nonce() > ^uint64(0)-uint64(index) {
			return 0, fmt.Errorf("nonce overflows from deposit-0 nonce %d", first.tx.Nonce())
		}
		return first.tx.Nonce() + uint64(index), nil
	}

	// For deposit-0 the signed transaction itself is the durable nonce intent.
	// A response-loss resume may observe any nonce covered by the exact durable
	// deposit prefix (including the next nonce after all of it was mined).
	// Anything beyond that window means another transaction mutated the funding
	// account and is not a safe basis for replay.
	expected := tx.Nonce()
	if durablePrefix < 1 {
		return 0, fmt.Errorf("durable deposit prefix %d cannot cover deposit-0", durablePrefix)
	}
	if expected > ^uint64(0)-uint64(durablePrefix) {
		return 0, fmt.Errorf("deposit-0 nonce cannot advance across %d durable deposits", durablePrefix)
	}
	maximum := expected + uint64(durablePrefix)
	for nodeIndex, client := range clients {
		if client == nil {
			return 0, fmt.Errorf("execution node %d nonce reader is nil", nodeIndex+1)
		}
		confirmed, err := client.NonceAt(ctx, from, nil)
		if err != nil {
			return 0, fmt.Errorf("read execution node %d confirmed funding nonce: %w", nodeIndex+1, err)
		}
		pending, err := client.PendingNonceAt(ctx, from)
		if err != nil {
			return 0, fmt.Errorf("read execution node %d pending funding nonce: %w", nodeIndex+1, err)
		}
		if confirmed < expected || confirmed > maximum || pending < confirmed || pending > maximum {
			return 0, fmt.Errorf("execution node %d funding nonce window is confirmed=%d pending=%d, want %d through %d from the durable prefix", nodeIndex+1, confirmed, pending, expected, maximum)
		}
	}
	return expected, nil
}

func validatePreparedDeposits(values map[string]PreparedTransaction, recorded map[int]common.Hash, count int) (map[int]*types.Transaction, error) {
	prepared := make(map[int]*types.Transaction, len(values))
	preparedOnly := -1
	for label, value := range values {
		var index int
		if _, err := fmt.Sscanf(label, "deposit-%d", &index); err != nil || index < 0 || index >= count || label != fmt.Sprintf("deposit-%d", index) {
			return nil, fmt.Errorf("prepared deposit transaction label %q is invalid", label)
		}
		raw, err := hexutil.Decode(value.Raw)
		if err != nil || len(raw) == 0 {
			return nil, fmt.Errorf("prepared deposit transaction %q has invalid raw bytes", label)
		}
		tx := new(types.Transaction)
		if err := tx.UnmarshalBinary(raw); err != nil {
			return nil, fmt.Errorf("decode prepared deposit transaction %q: %w", label, err)
		}
		if tx.Hash().Hex() != value.Hash {
			return nil, fmt.Errorf("prepared deposit transaction %q hash is %s, want %s", label, tx.Hash(), value.Hash)
		}
		if submitted, ok := recorded[index]; ok {
			if submitted != tx.Hash() {
				return nil, fmt.Errorf("prepared deposit transaction %q differs from submitted hash %s", label, submitted)
			}
		} else if preparedOnly >= 0 {
			return nil, fmt.Errorf("prepared deposits %d and %d are both unsubmitted", preparedOnly, index)
		} else {
			preparedOnly = index
		}
		prepared[index] = tx
	}
	if preparedOnly >= 0 && preparedOnly != len(recorded) {
		return nil, fmt.Errorf("prepared deposit-%d is out of order; next deposit is %d", preparedOnly, len(recorded))
	}
	return prepared, nil
}

func validateRecordedDeposits(values map[string]string, count int) (map[int]common.Hash, error) {
	recorded := make(map[int]common.Hash, len(values))
	for label, raw := range values {
		if !strings.HasPrefix(label, "deposit-") {
			return nil, fmt.Errorf("recorded deposit transaction label %q is unknown", label)
		}
		var index int
		if _, err := fmt.Sscanf(label, "deposit-%d", &index); err != nil || index < 0 || index >= count || label != fmt.Sprintf("deposit-%d", index) {
			return nil, fmt.Errorf("recorded deposit transaction label %q is invalid", label)
		}
		var hash common.Hash
		if err := hash.UnmarshalText([]byte(raw)); err != nil || hash == (common.Hash{}) || hash.Hex() != strings.ToLower(raw) {
			return nil, fmt.Errorf("recorded deposit transaction %q has invalid canonical hash %q", label, raw)
		}
		recorded[index] = hash
	}
	for index := 0; index < len(recorded); index++ {
		if _, ok := recorded[index]; !ok {
			return nil, fmt.Errorf("recorded deposit transactions are not an exact prefix: missing deposit-%d", index)
		}
	}
	return recorded, nil
}
