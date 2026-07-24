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

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

func newTransactor(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address) (*bind.TransactOpts, error) {
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
	return auth, nil
}

func sendValue(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from, to common.Address, value *big.Int) (*types.Receipt, error) {
	signed, err := signDynamicFeeTx(ctx, client, w, from, &to, value, nil)
	if err != nil {
		return nil, err
	}
	return submitTransaction(ctx, client, signed, types.ReceiptStatusSuccessful)
}

func deployRaw(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address, payload []byte) (*types.Receipt, error) {
	signed, err := signDynamicFeeTx(ctx, client, w, from, nil, big.NewInt(0), payload)
	if err != nil {
		return nil, err
	}
	return submitTransaction(ctx, client, signed, types.ReceiptStatusSuccessful)
}

func signDynamicFeeTx(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address, to *common.Address, value *big.Int, payload []byte) (*types.Transaction, error) {
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("chain id: %w", err)
	}
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("nonce of %s: %w", from.Hex(), err)
	}
	gasFeeCap, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("gas price: %w", err)
	}
	gasTipCap, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("gas tip: %w", err)
	}
	gasFeeCap = new(big.Int).Mul(gasFeeCap, big.NewInt(4))
	if gasFeeCap.Cmp(gasTipCap) < 0 {
		gasFeeCap = gasTipCap
	}
	gas, err := client.EstimateGas(ctx, qrl.CallMsg{
		From:  from,
		To:    to,
		Value: value,
		Data:  payload,
	})
	if err != nil {
		return nil, fmt.Errorf("estimate gas: %w", err)
	}
	gas += gas / 5

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gas,
		To:        to,
		Value:     value,
		Data:      payload,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), w)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	return signed, nil
}

func submitTransaction(
	ctx context.Context,
	client *qrlclient.Client,
	tx *types.Transaction,
	expectedStatus uint64,
) (*types.Receipt, error) {
	if err := client.SendTransaction(ctx, tx); err != nil {
		return nil, fmt.Errorf("send transaction %s: %w", tx.Hash(), err)
	}
	return waitTransaction(ctx, client, tx, expectedStatus)
}

func waitTransaction(
	ctx context.Context,
	client *qrlclient.Client,
	tx *types.Transaction,
	expectedStatus uint64,
) (*types.Receipt, error) {
	receipt, err := bind.WaitMined(ctx, client, tx)
	if err != nil {
		return nil, fmt.Errorf("wait for transaction %s: %w", tx.Hash(), err)
	}
	if receipt == nil || receipt.BlockNumber == nil || receipt.BlockNumber.Sign() <= 0 {
		return nil, fmt.Errorf("transaction %s has no mined receipt", tx.Hash())
	}
	if receipt.TxHash != tx.Hash() {
		return nil, fmt.Errorf("transaction %s returned receipt for %s", tx.Hash(), receipt.TxHash)
	}
	if receipt.Status != expectedStatus {
		return nil, fmt.Errorf(
			"transaction %s status is %d, want %d",
			tx.Hash(),
			receipt.Status,
			expectedStatus,
		)
	}
	return receipt, nil
}
