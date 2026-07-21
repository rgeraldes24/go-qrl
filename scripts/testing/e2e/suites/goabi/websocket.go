// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package goabi

import (
	"context"
	"fmt"
	"math/big"
	"time"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

func checkWebSocketSubscriptions(ctx context.Context, run *suiteRun, wsURL string, httpClient *qrlclient.Client, w wallet.Wallet, from common.Address) error {
	if wsURL == "" {
		return nil
	}
	wsClient, err := qrlclient.DialContext(ctx, wsURL)
	if err != nil {
		return fmt.Errorf("dial websocket %s: %w", wsURL, err)
	}
	defer wsClient.Close()

	headers := make(chan *types.Header, 4)
	headSub, err := wsClient.SubscribeNewHead(ctx, headers)
	if err != nil {
		return fmt.Errorf("subscribe new heads: %w", err)
	}
	defer headSub.Unsubscribe()

	parsed, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return fmt.Errorf("parse emitter ABI from generated binding: %w", err)
	}
	expectedTopic := hashTopic(parsed.Events["Deployed"].ID)
	events := make(chan types.Log, 4)
	logSub, err := wsClient.SubscribeFilterLogs(ctx, qrl.FilterQuery{
		Topics: [][]common.LogTopic{{expectedTopic}},
	}, events)
	if err != nil {
		return fmt.Errorf("subscribe logs: %w", err)
	}
	defer logSub.Unsubscribe()

	// A historical deployment cannot be delivered to a new subscription. Exact
	// raw predecessors are replayed and required to mine successfully in nonce
	// order before a distinctly labelled live continuation is created.
	expectedTransaction := newTransactionSemantics(nil, new(big.Int), common.FromHex(EventEmitterBin))
	wait := func(waitCtx context.Context, hash common.Hash) (*types.Receipt, error) {
		return run.waitRecordedReceipt(waitCtx, httpClient, recordedTransaction{hash: hash})
	}
	label, err := run.reconcileWebSocketPredecessors(ctx, httpClient, expectedTransaction, wait)
	if err != nil {
		return err
	}
	deployment, err := deployEventEmitter(ctx, run, label, httpClient, w, from)
	if err != nil {
		return err
	}

	deadline := time.After(90 * time.Second)
	var gotHead, gotLog bool
	for !gotHead || !gotLog {
		select {
		case header := <-headers:
			if header != nil && header.Number != nil && header.Number.Cmp(deployment.receipt.BlockNumber) >= 0 {
				gotHead = true
			}
		case log := <-events:
			if log.TxHash == deployment.receipt.TxHash && len(log.Topics) == 1 && log.Topics[0] == deployment.topic {
				gotLog = true
			}
		case err := <-headSub.Err():
			return fmt.Errorf("new head subscription: %w", err)
		case err := <-logSub.Err():
			return fmt.Errorf("log subscription: %w", err)
		case <-deadline:
			return fmt.Errorf("timed out waiting for websocket events: head=%t log=%t", gotHead, gotLog)
		case <-ctx.Done():
			return fmt.Errorf("wait websocket subscriptions: %w", ctx.Err())
		}
	}
	return nil
}

func websocketProbeName(index int) string {
	if index == 0 {
		return TransactionWebSocketEmitterDeploy
	}
	return fmt.Sprintf("%s/resume-%d", TransactionWebSocketEmitterDeploy, index)
}

func (run *suiteRun) reconcileWebSocketPredecessors(ctx context.Context, client transactionSubmitter, expected transactionSemantics, wait transactionReceiptWaiter) (string, error) {
	var previous *types.Transaction
	for index := 0; ; index++ {
		label := websocketProbeName(index)
		prepared := run.prepared[label]
		submittedHash, submitted := run.recordedHash(label)
		if prepared == nil && !submitted {
			return label, nil
		}
		if submitted && prepared == nil {
			return "", fmt.Errorf("recorded websocket transaction %s as %s has no prepared raw bytes; refusing a continuation because exact semantics and nonce adjacency cannot be proven", submittedHash, label)
		}
		if prepared != nil && previous != nil {
			if previous.Nonce() == ^uint64(0) || prepared.Nonce() != previous.Nonce()+1 {
				return "", fmt.Errorf("prepared websocket continuation %s has nonce %d, want %d", label, prepared.Nonce(), previous.Nonce()+1)
			}
		}

		var transaction recordedTransaction
		if prepared != nil {
			if submitted && prepared.Hash() != submittedHash {
				return "", fmt.Errorf("recorded websocket transaction %s as %s differs from prepared transaction %s", submittedHash, label, prepared.Hash())
			}
			var ok bool
			var err error
			transaction, ok, err = run.ensurePreparedSubmitted(ctx, label, client, expected)
			if err != nil {
				return "", err
			}
			if !ok {
				return "", fmt.Errorf("prepared websocket transaction %s was not reconciled", label)
			}
		}
		if _, err := requireSuccessfulMinedReceipt(ctx, label, transaction.hash, wait); err != nil {
			return "", err
		}
		previous = prepared
	}
}
