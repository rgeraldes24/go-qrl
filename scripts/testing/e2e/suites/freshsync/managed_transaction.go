// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package freshsync

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

const managedEvidenceTimeout = 30 * time.Second

type managedTransactionRequest struct {
	From       common.Address   `json:"from"`
	To         *common.Address  `json:"to"`
	Value      *hexutil.Big     `json:"value"`
	Input      hexutil.Bytes    `json:"input"`
	AccessList types.AccessList `json:"accessList"`
	Nonce      hexutil.Uint64   `json:"nonce"`
	ChainID    *hexutil.Big     `json:"chainId"`
}

type managedExecutionClient interface {
	ChainID(context.Context) (*big.Int, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
	BlockByNumber(context.Context, *big.Int) (*types.Block, error)
	NonceAt(context.Context, common.Address, *big.Int) (uint64, error)
	PendingNonceAt(context.Context, common.Address) (uint64, error)
	PendingTransactions(context.Context) ([]*types.Transaction, error)
	SendManagedTransaction(context.Context, managedTransactionRequest) (common.Hash, error)
}

type qrlManagedExecutionClient struct {
	client *qrlclient.Client
}

func (client qrlManagedExecutionClient) ChainID(ctx context.Context) (*big.Int, error) {
	return client.client.ChainID(ctx)
}

func (client qrlManagedExecutionClient) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return client.client.HeaderByNumber(ctx, number)
}

func (client qrlManagedExecutionClient) BlockByNumber(ctx context.Context, number *big.Int) (*types.Block, error) {
	return client.client.BlockByNumber(ctx, number)
}

func (client qrlManagedExecutionClient) NonceAt(ctx context.Context, address common.Address, number *big.Int) (uint64, error) {
	return client.client.NonceAt(ctx, address, number)
}

func (client qrlManagedExecutionClient) PendingNonceAt(ctx context.Context, address common.Address) (uint64, error) {
	return client.client.PendingNonceAt(ctx, address)
}

func (client qrlManagedExecutionClient) PendingTransactions(ctx context.Context) ([]*types.Transaction, error) {
	var pending []*types.Transaction
	if err := client.client.Client().CallContext(ctx, &pending, "qrl_pendingTransactions"); err != nil {
		return nil, err
	}
	return pending, nil
}

func (client qrlManagedExecutionClient) SendManagedTransaction(ctx context.Context, request managedTransactionRequest) (common.Hash, error) {
	var hash common.Hash
	if err := client.client.Client().CallContext(ctx, &hash, "qrl_sendTransaction", request); err != nil {
		return common.Hash{}, err
	}
	if hash == (common.Hash{}) {
		return common.Hash{}, errors.New("qrl_sendTransaction returned a zero hash")
	}
	return hash, nil
}

func (s *freshSyncCheck) managedTransferForVerification(ctx context.Context) (common.Hash, error) {
	label := transferTransactionLabel(s.cfg.SyncMode)
	if s.recordedTransaction != "" {
		hash, err := parseTransactionHash(s.recordedTransaction)
		if err != nil {
			return common.Hash{}, fmt.Errorf("recorded transaction %s: %w", label, err)
		}
		if s.recordedIntent != nil {
			if _, err := s.validateManagedIntent(ctx, label, *s.recordedIntent); err != nil {
				return common.Hash{}, err
			}
		}
		return hash, nil
	}
	if s.managedRecord == nil || s.txRecord == nil {
		return common.Hash{}, errors.New("fresh-sync managed transfer requires durable intent, attempt, and transaction recorders")
	}

	var intent lifecycle.ManagedTransactionIntent
	if s.recordedIntent == nil {
		prepared, err := s.prepareManagedIntent(ctx, label)
		if err != nil {
			return common.Hash{}, err
		}
		if err := s.recordIntent(ctx, label, prepared); err != nil {
			return common.Hash{}, err
		}
		intent = prepared
		s.recordedIntent = &intent
	} else {
		intent = *s.recordedIntent
	}
	request, err := s.validateManagedIntent(ctx, label, intent)
	if err != nil {
		return common.Hash{}, err
	}

	clients, err := s.managedExecutionClients()
	if err != nil {
		return common.Hash{}, err
	}
	if !s.initialAttempt {
		if err := s.recordInitialAttempt(ctx, label); err != nil {
			return common.Hash{}, err
		}
		s.initialAttempt = true
		return s.sendOrRecoverManagedTransaction(ctx, clients, intent, request, label, "initial")
	}

	recovered, found, err := observeManagedIntent(ctx, clients, intent, request)
	if err != nil {
		return common.Hash{}, err
	}
	if found {
		return s.recordManagedHash(ctx, label, recovered)
	}
	if !s.resubmitted {
		if err := s.recordResubmit(ctx, label); err != nil {
			return common.Hash{}, err
		}
		s.resubmitted = true
	}
	// A process may be interrupted after the resubmit marker but before the RPC.
	// Since both nodes just proved the exact sender/nonce intent absent, retry
	// the immutable request. Competing signatures for one explicit account
	// nonce are mutually exclusive and cannot create duplicate state changes.
	return s.sendOrRecoverManagedTransaction(ctx, clients, intent, request, label, "durable recovery")
}

func (s *freshSyncCheck) sendOrRecoverManagedTransaction(ctx context.Context, clients [2]managedExecutionClient, intent lifecycle.ManagedTransactionIntent, request managedTransactionRequest, label, attempt string) (common.Hash, error) {
	hash, rpcErr := clients[intent.Origin].SendManagedTransaction(ctx, request)
	if rpcErr == nil {
		return s.recordManagedHash(ctx, label, hash)
	}
	recovered, found, recoveryErr := observeManagedIntent(ctx, clients, intent, request)
	if recoveryErr != nil {
		return common.Hash{}, errors.Join(fmt.Errorf("%s qrl_sendTransaction for %s: %w", attempt, label, rpcErr), recoveryErr)
	}
	if !found {
		return common.Hash{}, fmt.Errorf("%s qrl_sendTransaction for %s: %w", attempt, label, rpcErr)
	}
	return s.recordManagedHash(ctx, label, recovered)
}

func (s *freshSyncCheck) prepareManagedIntent(ctx context.Context, label string) (lifecycle.ManagedTransactionIntent, error) {
	clients, err := s.managedExecutionClients()
	if err != nil {
		return lifecycle.ManagedTransactionIntent{}, err
	}
	origin, err := s.resolveManagedOrigin(ctx, s.cfg.ReferenceService)
	if err != nil {
		return lifecycle.ManagedTransactionIntent{}, err
	}
	chainID, err := clients[0].ChainID(ctx)
	if err != nil || chainID == nil || chainID.Sign() <= 0 {
		return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("read reference chain ID: %w", err)
	}
	otherChainID, err := clients[1].ChainID(ctx)
	if err != nil {
		return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("read fresh chain ID: %w", err)
	}
	if otherChainID == nil || chainID.Cmp(otherChainID) != 0 {
		return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("execution nodes disagree on managed transaction chain ID: reference=%v fresh=%v", chainID, otherChainID)
	}
	var heads [2]*types.Header
	for index, client := range clients {
		header, err := client.HeaderByNumber(ctx, nil)
		if err != nil {
			return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("read EL%d managed transaction head: %w", index+1, err)
		}
		if header == nil || header.Number == nil {
			return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("EL%d managed transaction head is unavailable", index+1)
		}
		heads[index] = header
	}
	sharedNumber := new(big.Int).Set(heads[0].Number)
	if heads[1].Number.Cmp(sharedNumber) < 0 {
		sharedNumber.Set(heads[1].Number)
	}
	var starts [2]*types.Header
	for index, client := range clients {
		header, err := client.HeaderByNumber(ctx, sharedNumber)
		if err != nil {
			return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("read EL%d shared managed transaction start header %s: %w", index+1, sharedNumber, err)
		}
		if header == nil || header.Number == nil || header.Number.Cmp(sharedNumber) != 0 {
			return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("EL%d shared managed transaction start header %s is unavailable", index+1, sharedNumber)
		}
		starts[index] = header
	}
	if starts[1].Hash() != starts[0].Hash() {
		return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("execution nodes disagree on managed transaction start block %d: reference=%s fresh=%s", sharedNumber.Uint64(), starts[0].Hash(), starts[1].Hash())
	}
	start := starts[0]
	var confirmed [2]uint64
	var pending [2]uint64
	for index, client := range clients {
		confirmed[index], err = client.NonceAt(ctx, s.cfg.SignerAddress, start.Number)
		if err != nil {
			return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("EL%d confirmed nonce at managed start block: %w", index+1, err)
		}
		pending[index], err = client.PendingNonceAt(ctx, s.cfg.SignerAddress)
		if err != nil {
			return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("EL%d pending nonce before managed transaction: %w", index+1, err)
		}
	}
	if confirmed[0] != confirmed[1] || pending[0] != pending[1] || confirmed[0] != pending[0] {
		return lifecycle.ManagedTransactionIntent{}, fmt.Errorf("execution nodes disagree on a clean explicit nonce: confirmed=%v pending=%v", confirmed, pending)
	}
	accessList := make(types.AccessList, 0)
	return lifecycle.ManagedTransactionIntent{
		Phase: "fresh-" + s.cfg.SyncMode, Label: label, Origin: 0,
		OriginServiceName: origin.Name, OriginServiceUUID: origin.UUID,
		ChainID: hexutil.EncodeBig(chainID), From: canonicalManagedAddress(s.cfg.SignerAddress), To: canonicalManagedAddress(s.cfg.Recipient),
		Value: hexutil.EncodeBig(new(big.Int).SetUint64(s.cfg.TransferValue)), Input: "0x", AccessList: canonicalManagedAccessList(accessList),
		Nonce: confirmed[0], StartBlock: start.Number.Uint64(), StartBlockHash: start.Hash().Hex(), PreparedAt: s.currentTime().UTC(),
	}, nil
}

func (s *freshSyncCheck) validateManagedIntent(ctx context.Context, label string, intent lifecycle.ManagedTransactionIntent) (managedTransactionRequest, error) {
	if intent.Label != label || intent.Phase != "fresh-"+s.cfg.SyncMode || intent.Origin != 0 {
		return managedTransactionRequest{}, fmt.Errorf("managed transaction %s phase, label, or origin differs from the resumed suite", label)
	}
	if intent.From != canonicalManagedAddress(s.cfg.SignerAddress) || intent.To != canonicalManagedAddress(s.cfg.Recipient) || intent.Value != hexutil.EncodeBig(new(big.Int).SetUint64(s.cfg.TransferValue)) || intent.Input != "0x" {
		return managedTransactionRequest{}, fmt.Errorf("managed transaction %s request differs from the resumed fresh-sync configuration", label)
	}
	if intent.AccessList == nil || len(intent.AccessList) != 0 {
		return managedTransactionRequest{}, fmt.Errorf("managed transaction %s access list is not the exact canonical empty list", label)
	}
	origin, err := s.resolveManagedOrigin(ctx, intent.OriginServiceUUID)
	if err != nil {
		return managedTransactionRequest{}, err
	}
	if origin.Name != intent.OriginServiceName || origin.UUID != intent.OriginServiceUUID || origin.Name != s.cfg.ReferenceService {
		return managedTransactionRequest{}, fmt.Errorf("managed transaction origin changed: live=%s/%s intent=%s/%s configured=%s", origin.Name, origin.UUID, intent.OriginServiceName, intent.OriginServiceUUID, s.cfg.ReferenceService)
	}
	request, err := managedRequestFromIntent(intent)
	if err != nil {
		return managedTransactionRequest{}, err
	}
	clients, err := s.managedExecutionClients()
	if err != nil {
		return managedTransactionRequest{}, err
	}
	for index, client := range clients {
		chainID, err := client.ChainID(ctx)
		if err != nil {
			return managedTransactionRequest{}, fmt.Errorf("read EL%d chain ID while validating managed intent: %w", index+1, err)
		}
		if chainID == nil || chainID.Cmp((*big.Int)(request.ChainID)) != 0 {
			return managedTransactionRequest{}, fmt.Errorf("EL%d chain ID differs from managed intent %s", index+1, intent.ChainID)
		}
		startNumber := new(big.Int).SetUint64(intent.StartBlock)
		header, err := client.HeaderByNumber(ctx, startNumber)
		if err != nil {
			return managedTransactionRequest{}, fmt.Errorf("read EL%d managed intent start block %d: %w", index+1, intent.StartBlock, err)
		}
		if header == nil || header.Number == nil || header.Number.Cmp(startNumber) != 0 || header.Hash().Hex() != intent.StartBlockHash {
			return managedTransactionRequest{}, fmt.Errorf("EL%d canonical start block differs from managed intent %d/%s", index+1, intent.StartBlock, intent.StartBlockHash)
		}
		nonce, err := client.NonceAt(ctx, request.From, startNumber)
		if err != nil {
			return managedTransactionRequest{}, fmt.Errorf("read EL%d sender nonce at managed intent start block: %w", index+1, err)
		}
		if nonce != intent.Nonce {
			return managedTransactionRequest{}, fmt.Errorf("EL%d sender nonce at managed intent start block is %d, want %d", index+1, nonce, intent.Nonce)
		}
	}
	return request, nil
}

func managedRequestFromIntent(intent lifecycle.ManagedTransactionIntent) (managedTransactionRequest, error) {
	from, err := common.NewAddressFromString(intent.From)
	if err != nil {
		return managedTransactionRequest{}, fmt.Errorf("decode managed sender: %w", err)
	}
	var to *common.Address
	if intent.To != "" {
		address, err := common.NewAddressFromString(intent.To)
		if err != nil {
			return managedTransactionRequest{}, fmt.Errorf("decode managed recipient: %w", err)
		}
		to = &address
	}
	value, err := hexutil.DecodeBig(intent.Value)
	if err != nil {
		return managedTransactionRequest{}, fmt.Errorf("decode managed value: %w", err)
	}
	chainID, err := hexutil.DecodeBig(intent.ChainID)
	if err != nil {
		return managedTransactionRequest{}, fmt.Errorf("decode managed chain ID: %w", err)
	}
	input, err := hexutil.Decode(intent.Input)
	if err != nil {
		return managedTransactionRequest{}, fmt.Errorf("decode managed input: %w", err)
	}
	accessList, err := managedAccessListFromIntent(intent.AccessList)
	if err != nil {
		return managedTransactionRequest{}, err
	}
	return managedTransactionRequest{
		From: from, To: to, Value: (*hexutil.Big)(value), Input: hexutil.Bytes(input), AccessList: accessList,
		Nonce: hexutil.Uint64(intent.Nonce), ChainID: (*hexutil.Big)(chainID),
	}, nil
}

func observeManagedIntent(ctx context.Context, clients [2]managedExecutionClient, intent lifecycle.ManagedTransactionIntent, request managedTransactionRequest) (common.Hash, bool, error) {
	var hashes [2]common.Hash
	var found [2]bool
	for index, client := range clients {
		hash, exists, err := observeManagedIntentOnClient(ctx, client, intent, request)
		if err != nil {
			return common.Hash{}, false, fmt.Errorf("EL%d managed transaction recovery: %w", index+1, err)
		}
		hashes[index], found[index] = hash, exists
	}
	if found[0] != found[1] {
		return common.Hash{}, false, fmt.Errorf("execution nodes disagree on managed transaction visibility: EL1=%s EL2=%s", hashes[0], hashes[1])
	}
	if found[0] {
		if hashes[0] != hashes[1] {
			return common.Hash{}, false, fmt.Errorf("execution nodes recovered different managed transaction hashes: EL1=%s EL2=%s", hashes[0], hashes[1])
		}
		return hashes[0], true, nil
	}
	return common.Hash{}, false, nil
}

func observeManagedIntentOnClient(ctx context.Context, client managedExecutionClient, intent lifecycle.ManagedTransactionIntent, request managedTransactionRequest) (common.Hash, bool, error) {
	confirmed, err := client.NonceAt(ctx, request.From, nil)
	if err != nil {
		return common.Hash{}, false, err
	}
	pending, err := client.PendingNonceAt(ctx, request.From)
	if err != nil {
		return common.Hash{}, false, err
	}
	head, err := client.HeaderByNumber(ctx, nil)
	if err != nil || head == nil || head.Number == nil {
		return common.Hash{}, false, fmt.Errorf("read recovery head: %w", err)
	}
	if head.Number.Uint64() < intent.StartBlock {
		return common.Hash{}, false, fmt.Errorf("recovery head %d precedes intent start block %d", head.Number.Uint64(), intent.StartBlock)
	}
	candidates := make(map[common.Hash]bool)
	if confirmed > intent.Nonce {
		for number := intent.StartBlock; ; number++ {
			block, err := client.BlockByNumber(ctx, new(big.Int).SetUint64(number))
			if err != nil {
				return common.Hash{}, false, fmt.Errorf("scan canonical block %d: %w", number, err)
			}
			if block == nil {
				return common.Hash{}, false, fmt.Errorf("scan canonical block %d: execution node returned nil", number)
			}
			for _, transaction := range block.Transactions() {
				if err := collectManagedCandidate(candidates, transaction, intent, request, fmt.Sprintf("canonical block %d", number)); err != nil {
					return common.Hash{}, false, err
				}
			}
			if number == head.Number.Uint64() {
				break
			}
		}
	}
	pendingTransactions, err := client.PendingTransactions(ctx)
	if err != nil {
		return common.Hash{}, false, fmt.Errorf("read qrl_pendingTransactions: %w", err)
	}
	for _, transaction := range pendingTransactions {
		if err := collectManagedCandidate(candidates, transaction, intent, request, "pending transaction set"); err != nil {
			return common.Hash{}, false, err
		}
	}
	if len(candidates) > 1 {
		return common.Hash{}, false, fmt.Errorf("multiple transaction hashes occupy managed nonce %d", intent.Nonce)
	}
	for hash := range candidates {
		return hash, true, nil
	}
	if confirmed != intent.Nonce || pending != intent.Nonce {
		return common.Hash{}, false, fmt.Errorf("managed nonce %d was consumed without an exact transaction match: confirmed=%d pending=%d", intent.Nonce, confirmed, pending)
	}
	return common.Hash{}, false, nil
}

func collectManagedCandidate(candidates map[common.Hash]bool, transaction *types.Transaction, intent lifecycle.ManagedTransactionIntent, request managedTransactionRequest, location string) error {
	if transaction == nil || transaction.Nonce() != intent.Nonce {
		return nil
	}
	sender, err := types.Sender(types.LatestSignerForChainID(transaction.ChainId()), transaction)
	if err != nil {
		return fmt.Errorf("recover %s transaction sender: %w", location, err)
	}
	if sender != request.From {
		return nil
	}
	if err := validateManagedTransaction(transaction, request); err != nil {
		return fmt.Errorf("%s contains a mismatched transaction at managed nonce %d: %w", location, intent.Nonce, err)
	}
	candidates[transaction.Hash()] = true
	return nil
}

func validateManagedTransaction(transaction *types.Transaction, request managedTransactionRequest) error {
	if transaction.Type() != types.DynamicFeeTxType || transaction.ChainId().Cmp((*big.Int)(request.ChainID)) != 0 {
		return errors.New("transaction type or chain ID differs")
	}
	if !equalAddressPointers(transaction.To(), request.To) || transaction.Value().Cmp((*big.Int)(request.Value)) != 0 {
		return errors.New("transaction recipient or value differs")
	}
	if !bytes.Equal(transaction.Data(), []byte(request.Input)) {
		return errors.New("transaction input differs")
	}
	if !reflect.DeepEqual(canonicalManagedAccessList(transaction.AccessList()), canonicalManagedAccessList(request.AccessList)) {
		return errors.New("transaction access list differs")
	}
	return nil
}

func equalAddressPointers(left, right *common.Address) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return *left == *right
}

func canonicalManagedAccessList(list types.AccessList) []lifecycle.ManagedAccessTuple {
	if list == nil {
		return nil
	}
	result := make([]lifecycle.ManagedAccessTuple, len(list))
	for index, tuple := range list {
		keys := make([]string, len(tuple.StorageKeys))
		for keyIndex, key := range tuple.StorageKeys {
			keys[keyIndex] = key.Hex()
		}
		result[index] = lifecycle.ManagedAccessTuple{Address: canonicalManagedAddress(tuple.Address), StorageKeys: keys}
	}
	return result
}

func managedAccessListFromIntent(list []lifecycle.ManagedAccessTuple) (types.AccessList, error) {
	if list == nil {
		return nil, nil
	}
	result := make(types.AccessList, len(list))
	for index, tuple := range list {
		address, err := common.NewAddressFromString(tuple.Address)
		if err != nil {
			return nil, err
		}
		keys := make([]common.Hash, len(tuple.StorageKeys))
		for keyIndex, raw := range tuple.StorageKeys {
			if err := keys[keyIndex].UnmarshalText([]byte(raw)); err != nil {
				return nil, err
			}
		}
		result[index] = types.AccessTuple{Address: address, StorageKeys: keys}
	}
	return result, nil
}

func canonicalManagedAddress(address common.Address) string {
	return "Q" + hex.EncodeToString(address[:])
}

func (s *freshSyncCheck) managedExecutionClients() ([2]managedExecutionClient, error) {
	clients := s.managedClients
	for index := range clients {
		if clients[index] == nil {
			if s.clients[index] == nil {
				return clients, fmt.Errorf("EL%d managed transaction client is not initialized", index+1)
			}
			clients[index] = qrlManagedExecutionClient{client: s.clients[index]}
		}
	}
	return clients, nil
}

func (s *freshSyncCheck) resolveManagedOrigin(ctx context.Context, identifier string) (TemporaryService, error) {
	if s.client == nil {
		return TemporaryService{}, errors.New("managed transaction origin requires the UUID-validating Kurtosis client")
	}
	service, err := s.client.Service(ctx, s.enclave, identifier)
	if err != nil {
		return TemporaryService{}, fmt.Errorf("resolve managed transaction origin %q: %w", identifier, err)
	}
	origin := TemporaryService{Name: service.Name, UUID: service.UUID}
	if err := origin.Validate(); err != nil {
		return TemporaryService{}, err
	}
	return origin, nil
}

func (s *freshSyncCheck) currentTime() time.Time {
	if s.now != nil {
		return s.now().UTC()
	}
	return time.Now().UTC()
}

func (s *freshSyncCheck) recordIntent(ctx context.Context, label string, intent lifecycle.ManagedTransactionIntent) error {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), managedEvidenceTimeout)
	defer cancel()
	if err := s.managedRecord.RecordManagedTransactionIntent(recordCtx, label, intent); err != nil {
		return fmt.Errorf("persist managed transaction intent %s: %w", label, err)
	}
	return nil
}

func (s *freshSyncCheck) recordInitialAttempt(ctx context.Context, label string) error {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), managedEvidenceTimeout)
	defer cancel()
	if err := s.managedRecord.RecordManagedTransactionInitialAttempt(recordCtx, label); err != nil {
		return fmt.Errorf("persist managed transaction initial-attempt marker %s: %w", label, err)
	}
	return nil
}

func (s *freshSyncCheck) recordResubmit(ctx context.Context, label string) error {
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), managedEvidenceTimeout)
	defer cancel()
	if err := s.managedRecord.RecordManagedTransactionResubmit(recordCtx, label); err != nil {
		return fmt.Errorf("persist managed transaction resubmit marker %s: %w", label, err)
	}
	return nil
}

func (s *freshSyncCheck) recordManagedHash(ctx context.Context, label string, hash common.Hash) (common.Hash, error) {
	if hash == (common.Hash{}) {
		return common.Hash{}, errors.New("managed transaction hash is zero")
	}
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), managedEvidenceTimeout)
	defer cancel()
	if err := s.txRecord.RecordTransaction(recordCtx, label, hash.Hex()); err != nil {
		return common.Hash{}, fmt.Errorf("persist managed transaction %s as %s: %w", hash, label, err)
	}
	return hash, nil
}

var _ managedExecutionClient = qrlManagedExecutionClient{}
