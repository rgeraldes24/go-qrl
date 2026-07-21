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
	"encoding/binary"
	"errors"
	"fmt"
	"math/big"
	"strings"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/qrlclient"
)

const depositContractABI = `[
	{"anonymous":false,"inputs":[{"indexed":false,"name":"pubkey","type":"bytes"},{"indexed":false,"name":"withdrawal_credentials","type":"bytes"},{"indexed":false,"name":"amount","type":"bytes"},{"indexed":false,"name":"signature","type":"bytes"},{"indexed":false,"name":"index","type":"bytes"}],"name":"DepositEvent","type":"event"},
	{"inputs":[{"name":"pubkey","type":"bytes"},{"name":"withdrawal_credentials","type":"bytes"},{"name":"signature","type":"bytes"},{"name":"deposit_data_root","type":"bytes32"}],"name":"deposit","outputs":[],"stateMutability":"payable","type":"function"},
	{"inputs":[],"name":"get_deposit_count","outputs":[{"name":"","type":"bytes"}],"stateMutability":"view","type":"function"},
	{"inputs":[],"name":"get_deposit_root","outputs":[{"name":"","type":"bytes32"}],"stateMutability":"view","type":"function"}
]`

const depositTreeDepth = 32

var emptyDepositRoot = common.HexToHash("0xd70a234731285c6804c2a4f56711ddb8c82c99740f207854891028af34e27e5e")

type contractState struct {
	count   uint64
	root    [32]byte
	balance *big.Int
	code    []byte
}

type depositContractEvent struct {
	Pubkey                []byte
	WithdrawalCredentials []byte
	Amount                []byte
	Signature             []byte
	Index                 []byte
}

type depositResult struct {
	tx      *types.Transaction
	receipt *types.Receipt
}

type depositTransactionSubmitter interface {
	TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error)
	SendTransaction(context.Context, *types.Transaction) error
}

type depositNonceReader interface {
	NonceAt(context.Context, common.Address, *big.Int) (uint64, error)
	PendingNonceAt(context.Context, common.Address) (uint64, error)
}

func parseDepositABI() (abi.ABI, error) {
	parsed, err := abi.JSON(strings.NewReader(depositContractABI))
	if err != nil {
		return abi.ABI{}, fmt.Errorf("parse deposit contract ABI: %w", err)
	}
	return parsed, nil
}

func readContractState(ctx context.Context, client *qrlclient.Client, address common.Address, parsed abi.ABI) (contractState, error) {
	return readContractStateAt(ctx, client, address, parsed, nil)
}

func readContractStateAt(ctx context.Context, client *qrlclient.Client, address common.Address, parsed abi.ABI, block *big.Int) (contractState, error) {
	code, err := client.CodeAt(ctx, address, block)
	if err != nil {
		return contractState{}, fmt.Errorf("read contract code: %w", err)
	}
	if len(code) == 0 {
		return contractState{}, fmt.Errorf("no code at deposit contract %s", address.Hex())
	}
	contract := bind.NewBoundContract(address, parsed, client, client, client)

	var countOutput []any
	if err := contract.Call(&bind.CallOpts{Context: ctx, BlockNumber: block}, &countOutput, "get_deposit_count"); err != nil {
		return contractState{}, fmt.Errorf("get deposit count: %w", err)
	}
	if len(countOutput) != 1 {
		return contractState{}, fmt.Errorf("get deposit count returned %d values, want 1", len(countOutput))
	}
	countBytes := *abi.ConvertType(countOutput[0], new([]byte)).(*[]byte)
	if len(countBytes) != 8 {
		return contractState{}, fmt.Errorf("deposit count encoding is %d bytes, want 8", len(countBytes))
	}

	var rootOutput []any
	if err := contract.Call(&bind.CallOpts{Context: ctx, BlockNumber: block}, &rootOutput, "get_deposit_root"); err != nil {
		return contractState{}, fmt.Errorf("get deposit root: %w", err)
	}
	if len(rootOutput) != 1 {
		return contractState{}, fmt.Errorf("get deposit root returned %d values, want 1", len(rootOutput))
	}
	root := *abi.ConvertType(rootOutput[0], new([32]byte)).(*[32]byte)

	balance, err := client.BalanceAt(ctx, address, block)
	if err != nil {
		return contractState{}, fmt.Errorf("read contract balance: %w", err)
	}
	return contractState{
		count:   binary.LittleEndian.Uint64(countBytes),
		root:    root,
		balance: balance,
		code:    code,
	}, nil
}

func loadRecordedDeposit(ctx context.Context, client *qrlclient.Client, hash common.Hash) (depositResult, error) {
	tx, pending, err := client.TransactionByHash(ctx, hash)
	if err != nil {
		return depositResult{}, fmt.Errorf("read transaction %s: %w", hash, err)
	}
	if tx == nil || tx.Hash() != hash {
		return depositResult{}, fmt.Errorf("recorded transaction %s lookup returned a different transaction", hash)
	}
	var receipt *types.Receipt
	if pending {
		receipt, err = bind.WaitMined(ctx, client, tx)
	} else {
		receipt, err = client.TransactionReceipt(ctx, hash)
	}
	if err != nil {
		return depositResult{}, fmt.Errorf("read receipt %s: %w", hash, err)
	}
	if receipt == nil || receipt.BlockNumber == nil || receipt.TxHash != hash {
		return depositResult{}, fmt.Errorf("recorded transaction %s has an incomplete or mismatched receipt", hash)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return depositResult{}, fmt.Errorf("recorded transaction %s failed with status %d", hash, receipt.Status)
	}
	return depositResult{tx: tx, receipt: receipt}, nil
}

func ensurePreparedDepositSubmitted(ctx context.Context, client depositTransactionSubmitter, tx *types.Transaction) error {
	if tx == nil {
		return errors.New("prepared deposit transaction is nil")
	}
	found, _, err := client.TransactionByHash(ctx, tx.Hash())
	if err == nil {
		if found == nil || found.Hash() != tx.Hash() {
			return fmt.Errorf("prepared deposit lookup returned a different transaction for %s", tx.Hash())
		}
		return nil
	}
	if !errors.Is(err, qrl.NotFound) {
		return fmt.Errorf("look up prepared deposit %s: %w", tx.Hash(), err)
	}
	if sendErr := client.SendTransaction(ctx, tx); sendErr != nil {
		found, _, verifyErr := client.TransactionByHash(ctx, tx.Hash())
		if verifyErr != nil || found == nil || found.Hash() != tx.Hash() {
			return fmt.Errorf("rebroadcast prepared deposit %s: %w", tx.Hash(), sendErr)
		}
	}
	return nil
}

func validateAndEnsurePreparedDeposit(ctx context.Context, client depositTransactionSubmitter, tx *types.Transaction, address common.Address, data depositData, parsed abi.ABI, expectedFrom common.Address, expectedNonce uint64, expectedChainID *big.Int) error {
	if err := verifyPreparedDepositTransaction(tx, address, data, parsed, expectedFrom, expectedNonce, expectedChainID); err != nil {
		return err
	}
	return ensurePreparedDepositSubmitted(ctx, client, tx)
}

func journalAndSubmitDeposit(ctx context.Context, client depositTransactionSubmitter, tx *types.Transaction, recorder PreparedTransactionRecorder, label string) error {
	raw, err := tx.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal prepared deposit transaction %s: %w", tx.Hash(), err)
	}
	if recorder != nil {
		if err := recorder.RecordPreparedTransaction(label, tx.Hash().Hex(), hexutil.Encode(raw)); err != nil {
			return fmt.Errorf("journal prepared deposit transaction %s as %s: %w", tx.Hash(), label, err)
		}
	}
	return ensurePreparedDepositSubmitted(ctx, client, tx)
}

func submitDeposit(ctx context.Context, client *qrlclient.Client, fundingSeed string, address common.Address, data depositData, parsed abi.ABI, recorder TransactionRecorder, preparedRecorder PreparedTransactionRecorder, label string) (depositResult, error) {
	w, err := wallet.RestoreFromSeedHex(strings.TrimPrefix(fundingSeed, "0x"))
	if err != nil {
		return depositResult{}, fmt.Errorf("restore prefunded wallet: %w", err)
	}
	from := common.Address(w.GetAddress())
	calldata, err := parsed.Pack("deposit", data.publicKey, data.withdrawalCredentials, data.signature, data.root)
	if err != nil {
		return depositResult{}, fmt.Errorf("pack deposit calldata: %w", err)
	}
	value := new(big.Int).Mul(new(big.Int).SetUint64(data.amount), big.NewInt(params.Shor))
	tx, err := signDynamicFeeTx(ctx, client, w, from, &address, value, calldata)
	if err != nil {
		return depositResult{}, err
	}
	if err := journalAndSubmitDeposit(ctx, client, tx, preparedRecorder, label); err != nil {
		return depositResult{}, err
	}
	receipt, err := recordAndWaitForDeposit(ctx, recorder, label, tx.Hash().Hex(), func(ctx context.Context) (*types.Receipt, error) {
		return bind.WaitMined(ctx, client, tx)
	})
	if err != nil {
		return depositResult{}, fmt.Errorf("wait for deposit transaction %s: %w", tx.Hash().Hex(), err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return depositResult{}, fmt.Errorf("deposit transaction %s failed with status %d", tx.Hash().Hex(), receipt.Status)
	}
	return depositResult{tx: tx, receipt: receipt}, nil
}

func recordAndWaitForDeposit(ctx context.Context, recorder TransactionRecorder, label, hash string, wait func(context.Context) (*types.Receipt, error)) (*types.Receipt, error) {
	if recorder != nil {
		if err := recorder.RecordTransaction(label, hash); err != nil {
			return nil, fmt.Errorf("transaction %s as %s was submitted but could not be recorded: %w", hash, label, err)
		}
	}
	return wait(ctx)
}

func verifyPreparedDepositTransaction(tx *types.Transaction, address common.Address, data depositData, parsed abi.ABI, expectedFrom common.Address, expectedNonce uint64, expectedChainID *big.Int) error {
	if tx == nil {
		return errors.New("prepared deposit transaction is nil")
	}
	if expectedChainID == nil || tx.ChainId().Cmp(expectedChainID) != 0 {
		return fmt.Errorf("deposit transaction chain ID %s, want %v", tx.ChainId(), expectedChainID)
	}
	if to := tx.To(); to == nil || *to != address {
		return fmt.Errorf("deposit transaction recipient %v, want %s", to, address.Hex())
	}
	if tx.Nonce() != expectedNonce {
		return fmt.Errorf("deposit transaction nonce %d, want %d", tx.Nonce(), expectedNonce)
	}
	wantValue := new(big.Int).Mul(new(big.Int).SetUint64(data.amount), big.NewInt(params.Shor))
	if tx.Value().Cmp(wantValue) != 0 {
		return fmt.Errorf("deposit transaction value %s, want %s", tx.Value(), wantValue)
	}
	wantCalldata, err := parsed.Pack("deposit", data.publicKey, data.withdrawalCredentials, data.signature, data.root)
	if err != nil {
		return fmt.Errorf("repack deposit calldata: %w", err)
	}
	if !bytes.Equal(tx.Data(), wantCalldata) {
		return errors.New("deposit transaction calldata differs from VM64 deposit data")
	}
	from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
	if err != nil {
		return fmt.Errorf("recover deposit transaction sender: %w", err)
	}
	if from != expectedFrom {
		return fmt.Errorf("deposit transaction sender %s, want %s", from.Hex(), expectedFrom.Hex())
	}
	return nil
}

func verifyDepositTransaction(tx *types.Transaction, receipt *types.Receipt, address common.Address, data depositData, parsed abi.ABI, expectedFrom common.Address, expectedIndex, expectedNonce uint64) error {
	if receipt.TxHash != tx.Hash() {
		return fmt.Errorf("receipt transaction hash %s, want %s", receipt.TxHash.Hex(), tx.Hash().Hex())
	}
	if receipt.BlockNumber == nil {
		return fmt.Errorf("deposit receipt has no block number")
	}
	if to := tx.To(); to == nil || *to != address {
		return fmt.Errorf("deposit transaction recipient %v, want %s", to, address.Hex())
	}
	if tx.Nonce() != expectedNonce {
		return fmt.Errorf("deposit transaction nonce %d, want %d", tx.Nonce(), expectedNonce)
	}
	wantValue := new(big.Int).Mul(new(big.Int).SetUint64(data.amount), big.NewInt(params.Shor))
	if tx.Value().Cmp(wantValue) != 0 {
		return fmt.Errorf("deposit transaction value %s, want %s", tx.Value(), wantValue)
	}
	wantCalldata, err := parsed.Pack("deposit", data.publicKey, data.withdrawalCredentials, data.signature, data.root)
	if err != nil {
		return fmt.Errorf("repack deposit calldata: %w", err)
	}
	if !bytes.Equal(tx.Data(), wantCalldata) {
		return fmt.Errorf("deposit transaction calldata differs from VM64 deposit data")
	}
	from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
	if err != nil {
		return fmt.Errorf("recover deposit transaction sender: %w", err)
	}
	if from != expectedFrom {
		return fmt.Errorf("deposit transaction sender %s, want %s", from.Hex(), expectedFrom.Hex())
	}
	if len(receipt.Logs) != 1 {
		return fmt.Errorf("deposit receipt has %d logs, want exactly 1", len(receipt.Logs))
	}
	log := receipt.Logs[0]
	event := parsed.Events["DepositEvent"]
	if log.Address != address {
		return fmt.Errorf("deposit event address %s, want %s", log.Address.Hex(), address.Hex())
	}
	if len(log.Topics) != 1 || log.Topics[0] != common.HashToLogTopic(event.ID) {
		return fmt.Errorf("deposit event topics %v, want signature %s", log.Topics, common.HashToLogTopic(event.ID).Hex())
	}
	if log.TxHash != tx.Hash() || log.BlockHash != receipt.BlockHash || log.BlockNumber != receipt.BlockNumber.Uint64() || log.Removed {
		return fmt.Errorf("deposit event inclusion metadata does not match the receipt")
	}
	contract := bind.NewBoundContract(address, parsed, nil, nil, nil)
	var decoded depositContractEvent
	if err := contract.UnpackLog(&decoded, "DepositEvent", *log); err != nil {
		return fmt.Errorf("decode deposit event: %w", err)
	}
	if !bytes.Equal(decoded.Pubkey, data.publicKey) {
		return fmt.Errorf("deposit event public key differs from generated validator")
	}
	if !bytes.Equal(decoded.WithdrawalCredentials, data.withdrawalCredentials) {
		return fmt.Errorf("deposit event lost or changed the 64-byte withdrawal credentials")
	}
	if !bytes.Equal(decoded.Signature, data.signature) {
		return fmt.Errorf("deposit event signature differs from generated validator")
	}
	if len(decoded.Amount) != 8 || binary.LittleEndian.Uint64(decoded.Amount) != data.amount {
		return fmt.Errorf("deposit event amount encoding %x, want little-endian %d", decoded.Amount, data.amount)
	}
	if len(decoded.Index) != 8 || binary.LittleEndian.Uint64(decoded.Index) != expectedIndex {
		return fmt.Errorf("deposit event index encoding %x, want little-endian %d", decoded.Index, expectedIndex)
	}
	return nil
}

func verifyRemoteDeposit(ctx context.Context, client *qrlclient.Client, result depositResult, address common.Address, data depositData, parsed abi.ABI, expectedFrom common.Address, expectedIndex, expectedNonce uint64) (*types.Receipt, error) {
	receipt, err := bind.WaitMined(ctx, client, result.tx)
	if err != nil {
		return nil, fmt.Errorf("wait for deposit on peer execution node: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("peer execution receipt status = %d, want successful", receipt.Status)
	}
	if receipt.BlockNumber == nil || result.receipt.BlockNumber == nil {
		return nil, fmt.Errorf("peer or source execution receipt has no block number")
	}
	if receipt.BlockHash != result.receipt.BlockHash || receipt.BlockNumber.Cmp(result.receipt.BlockNumber) != 0 {
		return nil, fmt.Errorf("peer execution receipt inclusion differs: block %s/%s, want %s/%s", receipt.BlockNumber, receipt.BlockHash.Hex(), result.receipt.BlockNumber, result.receipt.BlockHash.Hex())
	}
	tx, pending, err := client.TransactionByHash(ctx, result.tx.Hash())
	if err != nil {
		return nil, fmt.Errorf("read deposit transaction from peer execution node: %w", err)
	}
	if pending {
		return nil, fmt.Errorf("deposit transaction is still pending on peer execution node")
	}
	if err := verifyDepositTransaction(tx, receipt, address, data, parsed, expectedFrom, expectedIndex, expectedNonce); err != nil {
		return nil, fmt.Errorf("peer execution node: %w", err)
	}
	return receipt, nil
}

func signDynamicFeeTx(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address, to *common.Address, value *big.Int, payload []byte) (*types.Transaction, error) {
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("chain ID: %w", err)
	}
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("pending nonce of %s: %w", from.Hex(), err)
	}
	feeCap, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("suggest gas price: %w", err)
	}
	tipCap, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("suggest gas tip: %w", err)
	}
	feeCap = new(big.Int).Mul(feeCap, big.NewInt(4))
	if feeCap.Cmp(tipCap) < 0 {
		feeCap = new(big.Int).Set(tipCap)
	}
	gas, err := client.EstimateGas(ctx, qrl.CallMsg{From: from, To: to, Value: value, Data: payload})
	if err != nil {
		return nil, fmt.Errorf("estimate deposit gas: %w", err)
	}
	gas += gas / 5
	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: tipCap, GasFeeCap: feeCap,
		Gas: gas, To: to, Value: value, Data: payload,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), w)
	if err != nil {
		return nil, fmt.Errorf("sign deposit transaction: %w", err)
	}
	return signed, nil
}

func depositTreeRoot(leaves [][32]byte) ([32]byte, error) {
	if uint64(len(leaves)) >= uint64(1)<<depositTreeDepth {
		return [32]byte{}, fmt.Errorf("deposit count %d exceeds tree capacity", len(leaves))
	}
	branch := depositTreeBranch(leaves)
	var zeroHashes [depositTreeDepth][32]byte
	for height := 0; height < depositTreeDepth-1; height++ {
		zeroHashes[height+1] = hashPair(zeroHashes[height], zeroHashes[height])
	}
	var node [32]byte
	size := uint64(len(leaves))
	for height := 0; height < depositTreeDepth; height++ {
		if size&1 == 1 {
			node = hashPair(branch[height], node)
		} else {
			node = hashPair(node, zeroHashes[height])
		}
		size /= 2
	}
	var mixIn [32]byte
	binary.LittleEndian.PutUint64(mixIn[:8], uint64(len(leaves)))
	return hashPair(node, mixIn), nil
}

// depositTreeBranch independently reconstructs the branch array maintained by
// the deposit contract. Keeping this verifier separate from the contract call
// catches both sparse-tree carry bugs and VM64's two-bytes32-per-slot packing.
func depositTreeBranch(leaves [][32]byte) [depositTreeDepth][32]byte {
	var branch [depositTreeDepth][32]byte
	for index, leaf := range leaves {
		node := leaf
		size := uint64(index + 1)
		for height := 0; height < depositTreeDepth; height++ {
			if size&1 == 1 {
				branch[height] = node
				break
			}
			node = hashPair(branch[height], node)
			size >>= 1
		}
	}
	return branch
}

func hashPair(left, right [32]byte) [32]byte {
	var input [64]byte
	copy(input[:32], left[:])
	copy(input[32:], right[:])
	return sha256.Sum256(input[:])
}
