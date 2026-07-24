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
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

func checkWebSocketSubscriptions(ctx context.Context, wsURL string, httpClient *qrlclient.Client, w wallet.Wallet, from common.Address) error {
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

	artifact, err := loadEventEmitterArtifact()
	if err != nil {
		return err
	}
	parsed := &artifact.ABI
	expectedTopic := common.HashToLogTopic(parsed.Events["Deployed"].ID)
	events := make(chan types.Log, 4)
	logSub, err := wsClient.SubscribeFilterLogs(ctx, qrl.FilterQuery{
		Topics: [][]common.LogTopic{{expectedTopic}},
	}, events)
	if err != nil {
		return fmt.Errorf("subscribe logs: %w", err)
	}
	defer logSub.Unsubscribe()

	// Bind the generated watcher before deployment. The suite owns the sender
	// nonce on its isolated test network, so the next contract address is
	// deterministic.
	nonce, err := httpClient.PendingNonceAt(ctx, from)
	if err != nil {
		return fmt.Errorf("load websocket deployment nonce: %w", err)
	}
	predictedAddress := crypto.CreateAddress(from, nonce)
	watchedBinding, err := NewEventEmitterBindingSmoke(predictedAddress, wsClient)
	if err != nil {
		return fmt.Errorf("bind predicted websocket deployment through generated binding: %w", err)
	}
	generatedEvents := make(chan *EventEmitterBindingSmokeDeployed, 1)
	generatedSub, err := watchedBinding.WatchDeployed(&bind.WatchOpts{Context: ctx}, generatedEvents)
	if err != nil {
		return fmt.Errorf("watch deployment through generated binding: %w", err)
	}
	defer generatedSub.Unsubscribe()

	deployment, err := deployEventEmitter(ctx, httpClient, w, from)
	if err != nil {
		return err
	}
	if deployment.address != predictedAddress {
		return fmt.Errorf("websocket deployment address %s, want predicted %s from nonce %d", deployment.address, predictedAddress, nonce)
	}

	deadline := time.After(90 * time.Second)
	var gotHead, gotLog, gotGenerated bool
	for !gotHead || !gotLog || !gotGenerated {
		select {
		case header := <-headers:
			if header != nil && header.Number != nil && header.Number.Cmp(deployment.receipt.BlockNumber) >= 0 {
				gotHead = true
			}
		case log := <-events:
			if log.TxHash == deployment.receipt.TxHash && len(log.Topics) == 1 && log.Topics[0] == deployment.topic {
				gotLog = true
			}
		case event := <-generatedEvents:
			if event.Raw.TxHash == deployment.receipt.TxHash &&
				event.Raw.Address == predictedAddress &&
				event.Value.Cmp(big.NewInt(1337)) == 0 {
				gotGenerated = true
			}
		case err := <-headSub.Err():
			return fmt.Errorf("new head subscription: %w", err)
		case err := <-logSub.Err():
			return fmt.Errorf("log subscription: %w", err)
		case err := <-generatedSub.Err():
			return fmt.Errorf("generated binding subscription: %w", err)
		case <-deadline:
			return fmt.Errorf("timed out waiting for websocket events: head=%t raw_log=%t generated_log=%t", gotHead, gotLog, gotGenerated)
		case <-ctx.Done():
			return fmt.Errorf("wait websocket subscriptions: %w", ctx.Err())
		}
	}
	return nil
}
