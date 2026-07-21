// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

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
)

type managedTransactionRequest struct {
	origin     int
	to         *common.Address
	value      *big.Int
	input      []byte
	accessList *types.AccessList
}

type managedRPCArgs struct {
	From       common.Address    `json:"from"`
	To         *common.Address   `json:"to"`
	Value      *hexutil.Big      `json:"value"`
	Input      hexutil.Bytes     `json:"input"`
	AccessList *types.AccessList `json:"accessList,omitempty"`
	Nonce      *hexutil.Uint64   `json:"nonce"`
	ChainID    *hexutil.Big      `json:"chainId"`
}

type managedExecution interface {
	ChainID(context.Context) (*big.Int, error)
	PendingNonceAt(context.Context, common.Address) (uint64, error)
	NonceAt(context.Context, common.Address) (uint64, error)
	HeaderByNumber(context.Context, *big.Int) (*types.Header, error)
	BlockNumber(context.Context) (uint64, error)
	TransactionsByNumber(context.Context, uint64) ([]*types.Transaction, error)
	PendingTransactions(context.Context) ([]*types.Transaction, error)
	SendManagedTransaction(context.Context, managedRPCArgs) (common.Hash, error)
}

type qrlManagedExecution struct {
	client *qrlclient.Client
}

func (execution qrlManagedExecution) ChainID(ctx context.Context) (*big.Int, error) {
	return execution.client.ChainID(ctx)
}
func (execution qrlManagedExecution) PendingNonceAt(ctx context.Context, address common.Address) (uint64, error) {
	return execution.client.PendingNonceAt(ctx, address)
}
func (execution qrlManagedExecution) NonceAt(ctx context.Context, address common.Address) (uint64, error) {
	return execution.client.NonceAt(ctx, address, nil)
}
func (execution qrlManagedExecution) HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error) {
	return execution.client.HeaderByNumber(ctx, number)
}
func (execution qrlManagedExecution) BlockNumber(ctx context.Context) (uint64, error) {
	return execution.client.BlockNumber(ctx)
}
func (execution qrlManagedExecution) TransactionsByNumber(ctx context.Context, number uint64) ([]*types.Transaction, error) {
	block, err := execution.client.BlockByNumber(ctx, new(big.Int).SetUint64(number))
	if err != nil {
		return nil, err
	}
	return block.Transactions(), nil
}
func (execution qrlManagedExecution) PendingTransactions(ctx context.Context) ([]*types.Transaction, error) {
	var pending []*types.Transaction
	err := execution.client.Client().CallContext(ctx, &pending, "qrl_pendingTransactions")
	return pending, err
}
func (execution qrlManagedExecution) SendManagedTransaction(ctx context.Context, args managedRPCArgs) (common.Hash, error) {
	var hash common.Hash
	err := execution.client.Client().CallContext(ctx, &hash, "qrl_sendTransaction", args)
	return hash, err
}

func (s *systemCheck) managedExecution(origin int) (managedExecution, error) {
	if origin < 0 || origin >= len(s.clients) {
		return nil, fmt.Errorf("invalid execution origin EL%d", origin+1)
	}
	if s.managedExecutions[origin] != nil {
		return s.managedExecutions[origin], nil
	}
	if s.clients[origin] == nil {
		return nil, fmt.Errorf("execution origin EL%d is unavailable", origin+1)
	}
	return qrlManagedExecution{client: s.clients[origin]}, nil
}

func (s *systemCheck) managedExecutionPair() ([2]managedExecution, error) {
	var executions [2]managedExecution
	for index := range executions {
		execution, err := s.managedExecution(index)
		if err != nil {
			return [2]managedExecution{}, err
		}
		executions[index] = execution
	}
	return executions, nil
}

func (s *systemCheck) sendJournaledManagedTransaction(ctx context.Context, label string, request managedTransactionRequest) (common.Hash, error) {
	if hash, ok := s.recordedTransaction(label); ok {
		return hash, nil
	}
	if s.managedJournal == nil {
		return common.Hash{}, errors.New("managed transaction journal is not configured")
	}
	if _, err := s.managedExecution(request.origin); err != nil {
		return common.Hash{}, err
	}
	if request.value == nil || request.value.Sign() < 0 {
		return common.Hash{}, errors.New("managed transaction value is missing or negative")
	}

	intent, exists := s.resume.managedIntents[label]
	if !exists {
		prepared, err := s.prepareManagedTransactionIntent(ctx, label, request)
		if err != nil {
			return common.Hash{}, err
		}
		if err := s.managedJournal.RecordManagedTransactionIntent(ctx, prepared); err != nil {
			return common.Hash{}, &evidenceError{err: fmt.Errorf("record managed transaction intent %s: %w", label, err)}
		}
		if s.resume.managedIntents == nil {
			s.resume.managedIntents = make(map[string]ManagedTransactionIntent)
		}
		s.resume.managedIntents[label] = prepared
		intent = prepared
	} else if err := s.validateManagedTransactionIntent(ctx, intent, label, request); err != nil {
		return common.Hash{}, err
	}

	if hash, found, err := s.reconcileManagedTransaction(ctx, intent); err != nil {
		return common.Hash{}, err
	} else if found {
		if err := s.recordTransaction(context.WithoutCancel(ctx), label, hash); err != nil {
			return common.Hash{}, err
		}
		s.resume.transactions[label] = hash
		return hash, nil
	}

	startedAt := s.currentTime().UTC()
	if _, started := s.resume.managedInitialAttempts[label]; !started {
		if err := s.managedJournal.RecordManagedTransactionInitialAttempt(ctx, label, startedAt); err != nil {
			return common.Hash{}, &evidenceError{err: fmt.Errorf("record managed transaction initial attempt %s: %w", label, err)}
		}
		if s.resume.managedInitialAttempts == nil {
			s.resume.managedInitialAttempts = make(map[string]time.Time)
		}
		s.resume.managedInitialAttempts[label] = startedAt
	} else if _, replayed := s.resume.managedResubmits[label]; !replayed {
		if err := s.managedJournal.RecordManagedTransactionResubmit(ctx, label, startedAt); err != nil {
			return common.Hash{}, &evidenceError{err: fmt.Errorf("record managed transaction resubmit %s: %w", label, err)}
		}
		if s.resume.managedResubmits == nil {
			s.resume.managedResubmits = make(map[string]time.Time)
		}
		s.resume.managedResubmits[label] = startedAt
	} else {
		// Both markers can be durable even when the process was interrupted
		// before either RPC reached the node. Reconciliation has just proved the
		// intent absent on both ELs. Reissuing the exact request is state-safe:
		// its immutable sender and explicit nonce make all signatures mutually
		// exclusive, so at most one state transition can be included.
	}

	hash, rpcErr := s.callManagedTransaction(ctx, intent)
	if rpcErr != nil {
		if recovered, found, reconcileErr := s.reconcileManagedTransaction(ctx, intent); reconcileErr == nil && found {
			if err := s.recordTransaction(context.WithoutCancel(ctx), label, recovered); err != nil {
				return common.Hash{}, errors.Join(rpcErr, err)
			}
			s.resume.transactions[label] = recovered
			return recovered, nil
		}
		return common.Hash{}, rpcErr
	}
	if hash == (common.Hash{}) {
		return common.Hash{}, errors.New("qrl_sendTransaction returned a zero hash")
	}
	if err := s.recordTransaction(context.WithoutCancel(ctx), label, hash); err != nil {
		return common.Hash{}, err
	}
	s.resume.transactions[label] = hash
	return hash, nil
}

func (s *systemCheck) prepareManagedTransactionIntent(ctx context.Context, label string, request managedTransactionRequest) (ManagedTransactionIntent, error) {
	executions, err := s.managedExecutionPair()
	if err != nil {
		return ManagedTransactionIntent{}, err
	}
	var chainIDs [2]*big.Int
	var confirmed [2]uint64
	var pending [2]uint64
	var heads [2]*types.Header
	for index, execution := range executions {
		chainIDs[index], err = execution.ChainID(ctx)
		if err != nil || chainIDs[index] == nil || chainIDs[index].Sign() <= 0 {
			return ManagedTransactionIntent{}, fmt.Errorf("EL%d chain ID for %s is unavailable or invalid: %w", index+1, label, err)
		}
		confirmed[index], err = execution.NonceAt(ctx, s.cfg.signerAddress)
		if err != nil {
			return ManagedTransactionIntent{}, fmt.Errorf("EL%d confirmed nonce for %s: %w", index+1, label, err)
		}
		pending[index], err = execution.PendingNonceAt(ctx, s.cfg.signerAddress)
		if err != nil {
			return ManagedTransactionIntent{}, fmt.Errorf("EL%d pending nonce for %s: %w", index+1, label, err)
		}
		heads[index], err = execution.HeaderByNumber(ctx, nil)
		if err != nil || heads[index] == nil || heads[index].Number == nil {
			return ManagedTransactionIntent{}, fmt.Errorf("EL%d start header for %s is unavailable: %w", index+1, label, err)
		}
	}
	if chainIDs[0].Cmp(chainIDs[1]) != 0 {
		return ManagedTransactionIntent{}, fmt.Errorf("managed transaction %s cannot establish a shared chain ID: EL1=%s EL2=%s", label, chainIDs[0], chainIDs[1])
	}
	startBlock := heads[0].Number.Uint64()
	if heads[1].Number.Uint64() < startBlock {
		startBlock = heads[1].Number.Uint64()
	}
	var boundaries [2]*types.Header
	for index, execution := range executions {
		boundaries[index], err = execution.HeaderByNumber(ctx, new(big.Int).SetUint64(startBlock))
		if err != nil || boundaries[index] == nil || boundaries[index].Number == nil || boundaries[index].Number.Uint64() != startBlock {
			return ManagedTransactionIntent{}, fmt.Errorf("EL%d shared start boundary %d for %s is unavailable: %w", index+1, startBlock, label, err)
		}
	}
	if boundaries[0].Hash() != boundaries[1].Hash() {
		return ManagedTransactionIntent{}, fmt.Errorf("managed transaction %s cannot establish shared canonical block %d: EL1=%s EL2=%s", label, startBlock, boundaries[0].Hash(), boundaries[1].Hash())
	}
	if confirmed[0] != pending[0] || confirmed[1] != pending[1] || confirmed[0] != confirmed[1] {
		return ManagedTransactionIntent{}, fmt.Errorf("managed transaction %s cannot establish an unambiguous shared nonce: EL1 confirmed=%d pending=%d EL2 confirmed=%d pending=%d", label, confirmed[0], pending[0], confirmed[1], pending[1])
	}
	serviceName := s.cfg.elServices[request.origin]
	serviceUUID := s.resume.serviceUUIDs[serviceName]
	if serviceUUID == "" {
		return ManagedTransactionIntent{}, fmt.Errorf("managed transaction %s has no immutable UUID for origin service %s", label, serviceName)
	}
	return ManagedTransactionIntent{
		Phase: string(Phase(s.cfg.phase)), Label: label, Origin: request.origin,
		OriginServiceName: serviceName, OriginServiceUUID: serviceUUID,
		ChainID: hexutil.EncodeBig(chainIDs[0]), From: managedAddressString(&s.cfg.signerAddress), To: managedAddressString(request.to),
		Value: hexutil.EncodeBig(request.value), Input: hexutil.Encode(request.input), AccessList: managedAccessListEvidence(request.accessList),
		Nonce: pending[0], StartBlock: startBlock, StartBlockHash: boundaries[0].Hash().Hex(), PreparedAt: s.currentTime().UTC(),
	}, nil
}

func (s *systemCheck) validateManagedTransactionIntent(ctx context.Context, intent ManagedTransactionIntent, label string, request managedTransactionRequest) error {
	if intent.Label != label || intent.Phase != s.cfg.phase || intent.Origin != request.origin {
		return fmt.Errorf("managed transaction intent %s has different phase, label, or origin", label)
	}
	serviceName := s.cfg.elServices[request.origin]
	if intent.OriginServiceName != serviceName || intent.OriginServiceUUID != s.resume.serviceUUIDs[serviceName] || intent.OriginServiceUUID == "" {
		return fmt.Errorf("managed transaction intent %s origin identity changed from %s/%s", label, intent.OriginServiceName, intent.OriginServiceUUID)
	}
	executions, err := s.managedExecutionPair()
	if err != nil {
		return err
	}
	for index, execution := range executions {
		chainID, err := execution.ChainID(ctx)
		if err != nil {
			return fmt.Errorf("read current EL%d chain ID for managed transaction %s: %w", index+1, label, err)
		}
		if intent.ChainID != hexutil.EncodeBig(chainID) {
			return fmt.Errorf("managed transaction intent %s chain ID differs from EL%d", label, index+1)
		}
	}
	if intent.From != managedAddressString(&s.cfg.signerAddress) || intent.To != managedAddressString(request.to) || intent.Value != hexutil.EncodeBig(request.value) || intent.Input != hexutil.Encode(request.input) || !reflect.DeepEqual(intent.AccessList, managedAccessListEvidence(request.accessList)) {
		return fmt.Errorf("managed transaction intent %s arguments differ from the requested sender, chain, recipient, value, input, or access list", label)
	}
	return nil
}

func (s *systemCheck) callManagedTransaction(ctx context.Context, intent ManagedTransactionIntent) (common.Hash, error) {
	from, err := common.NewAddressFromString(intent.From)
	if err != nil {
		return common.Hash{}, err
	}
	var to *common.Address
	if intent.To != "" {
		address, err := common.NewAddressFromString(intent.To)
		if err != nil {
			return common.Hash{}, err
		}
		to = &address
	}
	value, err := hexutil.DecodeBig(intent.Value)
	if err != nil {
		return common.Hash{}, err
	}
	chainID, err := hexutil.DecodeBig(intent.ChainID)
	if err != nil {
		return common.Hash{}, err
	}
	input, err := hexutil.Decode(intent.Input)
	if err != nil {
		return common.Hash{}, err
	}
	nonce := hexutil.Uint64(intent.Nonce)
	accessList, err := managedAccessList(intent.AccessList)
	if err != nil {
		return common.Hash{}, err
	}
	args := managedRPCArgs{
		From: from, To: to, Value: (*hexutil.Big)(value), Input: hexutil.Bytes(input),
		AccessList: accessList, Nonce: &nonce, ChainID: (*hexutil.Big)(chainID),
	}
	execution, err := s.managedExecution(intent.Origin)
	if err != nil {
		return common.Hash{}, err
	}
	return execution.SendManagedTransaction(ctx, args)
}

func (s *systemCheck) reconcileManagedTransaction(ctx context.Context, intent ManagedTransactionIntent) (common.Hash, bool, error) {
	executions, err := s.managedExecutionPair()
	if err != nil {
		return common.Hash{}, false, err
	}
	for pass := 0; pass < 2; pass++ {
		var matches [2][]common.Hash
		for index, execution := range executions {
			matches[index], err = s.scanManagedTransactionCandidates(ctx, intent, index, execution)
			if err != nil {
				return common.Hash{}, false, err
			}
		}
		if len(matches[0]) > 1 || len(matches[1]) > 1 {
			return common.Hash{}, false, fmt.Errorf("managed transaction %s has ambiguous exact candidates: EL1=%d EL2=%d", intent.Label, len(matches[0]), len(matches[1]))
		}
		if len(matches[0]) != len(matches[1]) {
			return common.Hash{}, false, fmt.Errorf("managed transaction %s has a one-sided exact candidate: EL1=%d EL2=%d", intent.Label, len(matches[0]), len(matches[1]))
		}
		if len(matches[0]) == 1 {
			if matches[0][0] != matches[1][0] {
				return common.Hash{}, false, fmt.Errorf("managed transaction %s has different exact candidates: EL1=%s EL2=%s", intent.Label, matches[0][0], matches[1][0])
			}
			return matches[0][0], true, nil
		}
		var confirmed [2]uint64
		var pending [2]uint64
		for index, execution := range executions {
			confirmed[index], err = execution.NonceAt(ctx, s.cfg.signerAddress)
			if err != nil {
				return common.Hash{}, false, fmt.Errorf("read EL%d confirmed nonce for managed transaction %s: %w", index+1, intent.Label, err)
			}
			pending[index], err = execution.PendingNonceAt(ctx, s.cfg.signerAddress)
			if err != nil {
				return common.Hash{}, false, fmt.Errorf("read EL%d pending nonce for managed transaction %s: %w", index+1, intent.Label, err)
			}
		}
		if confirmed[0] != intent.Nonce || pending[0] != intent.Nonce || confirmed[1] != intent.Nonce || pending[1] != intent.Nonce {
			return common.Hash{}, false, fmt.Errorf("managed transaction %s has no common exact candidate but signer nonce advanced or diverged: EL1 confirmed=%d pending=%d EL2 confirmed=%d pending=%d want=%d", intent.Label, confirmed[0], pending[0], confirmed[1], pending[1], intent.Nonce)
		}
	}
	return common.Hash{}, false, nil
}

func (s *systemCheck) scanManagedTransactionCandidates(ctx context.Context, intent ManagedTransactionIntent, index int, client managedExecution) ([]common.Hash, error) {
	startHeader, err := client.HeaderByNumber(ctx, new(big.Int).SetUint64(intent.StartBlock))
	if err != nil || startHeader == nil || startHeader.Hash().Hex() != intent.StartBlockHash {
		return nil, fmt.Errorf("managed transaction %s EL%d start block %d changed from %s: %w", intent.Label, index+1, intent.StartBlock, intent.StartBlockHash, err)
	}
	latest, err := client.BlockNumber(ctx)
	if err != nil {
		return nil, err
	}
	matches := make(map[common.Hash]struct{})
	for number := intent.StartBlock; number <= latest; number++ {
		transactions, err := client.TransactionsByNumber(ctx, number)
		if err != nil {
			return nil, fmt.Errorf("scan EL%d block %d for managed transaction %s: %w", index+1, number, intent.Label, err)
		}
		for _, tx := range transactions {
			if managedTransactionMatches(tx, intent) {
				matches[tx.Hash()] = struct{}{}
			}
		}
		if number == ^uint64(0) {
			break
		}
	}
	pending, err := client.PendingTransactions(ctx)
	if err != nil {
		return nil, fmt.Errorf("scan EL%d pending transactions for managed intent %s: %w", index+1, intent.Label, err)
	}
	for _, tx := range pending {
		if managedTransactionMatches(tx, intent) {
			matches[tx.Hash()] = struct{}{}
		}
	}
	result := make([]common.Hash, 0, len(matches))
	for hash := range matches {
		result = append(result, hash)
	}
	return result, nil
}

func managedTransactionMatches(tx *types.Transaction, intent ManagedTransactionIntent) bool {
	if tx == nil || tx.Nonce() != intent.Nonce || tx.ChainId() == nil || hexutil.EncodeBig(tx.ChainId()) != intent.ChainID || managedAddressString(tx.To()) != intent.To || hexutil.EncodeBig(tx.Value()) != intent.Value || hexutil.Encode(tx.Data()) != intent.Input {
		return false
	}
	from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
	if err != nil || managedAddressString(&from) != intent.From {
		return false
	}
	want, err := managedAccessList(intent.AccessList)
	if err != nil {
		return false
	}
	if want == nil {
		return len(tx.AccessList()) == 0
	}
	return reflect.DeepEqual(tx.AccessList(), *want)
}

func managedAddressString(address *common.Address) string {
	if address == nil {
		return ""
	}
	return "Q" + hex.EncodeToString(address[:])
}

func managedAccessListEvidence(list *types.AccessList) []ManagedAccessTuple {
	if list == nil {
		return nil
	}
	result := make([]ManagedAccessTuple, len(*list))
	for index, tuple := range *list {
		result[index].Address = managedAddressString(&tuple.Address)
		result[index].StorageKeys = make([]string, len(tuple.StorageKeys))
		for keyIndex, key := range tuple.StorageKeys {
			result[index].StorageKeys[keyIndex] = key.Hex()
		}
	}
	return result
}

func managedAccessList(list []ManagedAccessTuple) (*types.AccessList, error) {
	if list == nil {
		return nil, nil
	}
	result := make(types.AccessList, len(list))
	for index, tuple := range list {
		address, err := common.NewAddressFromString(tuple.Address)
		if err != nil {
			return nil, fmt.Errorf("invalid managed access-list address at tuple %d: %w", index, err)
		}
		result[index].Address = address
		result[index].StorageKeys = make([]common.Hash, len(tuple.StorageKeys))
		for keyIndex, raw := range tuple.StorageKeys {
			if !common.IsHexEncodedHash(raw) || common.HexToHash(raw).Hex() != raw {
				return nil, fmt.Errorf("invalid canonical managed access-list storage key at tuple %d key %d", index, keyIndex)
			}
			result[index].StorageKeys[keyIndex] = common.HexToHash(raw)
		}
	}
	return &result, nil
}

func managedRequestsEqual(left, right managedTransactionRequest) bool {
	return left.origin == right.origin && managedAddressString(left.to) == managedAddressString(right.to) && left.value.Cmp(right.value) == 0 && bytes.Equal(left.input, right.input) && reflect.DeepEqual(managedAccessListEvidence(left.accessList), managedAccessListEvidence(right.accessList))
}
