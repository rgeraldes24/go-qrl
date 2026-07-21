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
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strconv"
	"strings"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/qrlclient"
)

// Stable transaction labels in exact suite submission order. The numeric
// prefix is deliberately part of the persisted value so resume tooling can
// reject reordered or ambiguous checkpoints.
const (
	TransactionEventEmitterDeploy        = "goabi/01-event-emitter-deploy"
	TransactionEventEmitterStore         = "goabi/02-event-emitter-store"
	TransactionEventEmitterClear         = "goabi/03-event-emitter-clear"
	TransactionStorageContractDeploy     = "goabi/04-storage-contract-deploy"
	TransactionAddressIsolationFirst     = "goabi/05-address-isolation-first"
	TransactionAddressIsolationSecond    = "goabi/06-address-isolation-second"
	TransactionVM64ContextDeploy         = "goabi/vm64/01-context-deploy"
	TransactionVM64ContextFund           = "goabi/vm64/02-context-fund"
	TransactionVM64CollisionFund         = "goabi/vm64/03-collision-fund"
	TransactionVM64CallRouterDeploy      = "goabi/vm64/04-call-router-deploy"
	TransactionVM64IntrospectionDeploy   = "goabi/vm64/05-introspection-deploy"
	TransactionVM64WarmthDeploy          = "goabi/vm64/06-warmth-deploy"
	TransactionVM64CreatorDeploy         = "goabi/vm64/07-creator-deploy"
	TransactionVM64Create                = "goabi/vm64/08-create"
	TransactionVM64Create2               = "goabi/vm64/09-create2"
	TransactionVM64ReverterDeploy        = "goabi/vm64/10-reverter-deploy"
	TransactionVM64CatcherDeploy         = "goabi/vm64/11-catcher-deploy"
	TransactionVM64CaughtRevert          = "goabi/vm64/12-caught-revert"
	TransactionVM64TopLevelRevert        = "goabi/vm64/13-top-level-revert"
	TransactionGraphQLSendRawTransaction = "goabi/07-graphql-send-raw-transaction"
	TransactionWebSocketEmitterDeploy    = "goabi/08-websocket-emitter-deploy"
)

// mandatoryTransactionLabelsInSuiteOrder excludes the optional GraphQL and
// websocket probes. The VM64 namespace was deliberately added without
// renumbering those older labels so an existing durable journal remains
// intelligible after this live-probe expansion.
var mandatoryTransactionLabelsInSuiteOrder = [...]string{
	TransactionEventEmitterDeploy,
	TransactionEventEmitterStore,
	TransactionEventEmitterClear,
	TransactionStorageContractDeploy,
	TransactionAddressIsolationFirst,
	TransactionAddressIsolationSecond,
	TransactionVM64ContextDeploy,
	TransactionVM64ContextFund,
	TransactionVM64CollisionFund,
	TransactionVM64CallRouterDeploy,
	TransactionVM64IntrospectionDeploy,
	TransactionVM64WarmthDeploy,
	TransactionVM64CreatorDeploy,
	TransactionVM64Create,
	TransactionVM64Create2,
	TransactionVM64ReverterDeploy,
	TransactionVM64CatcherDeploy,
	TransactionVM64CaughtRevert,
	TransactionVM64TopLevelRevert,
}

var transactionLabelsInSuiteOrder = [...]string{
	mandatoryTransactionLabelsInSuiteOrder[0],
	mandatoryTransactionLabelsInSuiteOrder[1],
	mandatoryTransactionLabelsInSuiteOrder[2],
	mandatoryTransactionLabelsInSuiteOrder[3],
	mandatoryTransactionLabelsInSuiteOrder[4],
	mandatoryTransactionLabelsInSuiteOrder[5],
	mandatoryTransactionLabelsInSuiteOrder[6],
	mandatoryTransactionLabelsInSuiteOrder[7],
	mandatoryTransactionLabelsInSuiteOrder[8],
	mandatoryTransactionLabelsInSuiteOrder[9],
	mandatoryTransactionLabelsInSuiteOrder[10],
	mandatoryTransactionLabelsInSuiteOrder[11],
	mandatoryTransactionLabelsInSuiteOrder[12],
	mandatoryTransactionLabelsInSuiteOrder[13],
	mandatoryTransactionLabelsInSuiteOrder[14],
	mandatoryTransactionLabelsInSuiteOrder[15],
	mandatoryTransactionLabelsInSuiteOrder[16],
	mandatoryTransactionLabelsInSuiteOrder[17],
	mandatoryTransactionLabelsInSuiteOrder[18],
	TransactionGraphQLSendRawTransaction,
	TransactionWebSocketEmitterDeploy,
}

// TransactionRecorder durably persists a successfully submitted transaction
// before the suite begins waiting for its receipt or any derived event. An
// error aborts the suite immediately and is returned with the label and hash.
type TransactionRecorder interface {
	RecordTransaction(label, hash string) error
}

// TransactionRecorderFunc adapts a function to TransactionRecorder.
type TransactionRecorderFunc func(label, hash string) error

// RecordTransaction implements TransactionRecorder.
func (f TransactionRecorderFunc) RecordTransaction(label, hash string) error {
	return f(label, hash)
}

type PreparedTransaction struct {
	Hash string
	Raw  string
}

type PreparedTransactionRecorder interface {
	RecordPreparedTransaction(label, hash, raw string) error
}

type PreparedTransactionRecorderFunc func(label, hash, raw string) error

func (f PreparedTransactionRecorderFunc) RecordPreparedTransaction(label, hash, raw string) error {
	return f(label, hash, raw)
}

// Options configures optional lifecycle integration without changing Run's
// compatibility contract.
type Options struct {
	TransactionRecorder         TransactionRecorder
	PreparedTransactionRecorder PreparedTransactionRecorder
	// RecordedTransactions contains hashes durably checkpointed by an earlier
	// attempt. The suite re-observes those exact transactions instead of
	// submitting the logical mutation again.
	RecordedTransactions map[string]string
	PreparedTransactions map[string]PreparedTransaction
}

type receiptWaiter func(context.Context, *qrlclient.Client, common.Hash) (*types.Receipt, error)

// transactionReceiptWaiter keeps predecessor reconciliation independently
// testable while the live suite still uses its normal qrlclient receipt loop.
type transactionReceiptWaiter func(context.Context, common.Hash) (*types.Receipt, error)

type transactionSubmitter interface {
	TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error)
	SendTransaction(context.Context, *types.Transaction) error
}

type suiteRun struct {
	recorder         TransactionRecorder
	receiptWaiter    receiptWaiter
	recorded         map[string]common.Hash
	preparedRecorder PreparedTransactionRecorder
	prepared         map[string]*types.Transaction
	sender           common.Address
	chainID          *big.Int
	identitySet      bool
}

// transactionSemantics is the label-specific, fee-independent portion of a
// transaction. Nonce and gas fields can legitimately differ when the suite is
// resumed, but these fields define the mutation that the label is allowed to
// perform.
type transactionSemantics struct {
	recipient *common.Address
	value     *big.Int
	input     []byte
}

func newTransactionSemantics(recipient *common.Address, value *big.Int, input []byte) transactionSemantics {
	semantics := transactionSemantics{input: bytes.Clone(input)}
	if recipient != nil {
		copy := *recipient
		semantics.recipient = &copy
	}
	if value == nil {
		semantics.value = new(big.Int)
	} else {
		semantics.value = new(big.Int).Set(value)
	}
	return semantics
}

type recordedTransaction struct {
	hash common.Hash
}

type journalBackend struct {
	bind.ContractBackend
	run       *suiteRun
	label     string
	semantics transactionSemantics
}

func (backend *journalBackend) SendTransaction(ctx context.Context, tx *types.Transaction) error {
	if err := backend.run.validateTransactionSemantics(backend.label, tx, backend.semantics); err != nil {
		return err
	}
	if err := backend.run.prepareTransaction(backend.label, tx); err != nil {
		return err
	}
	if err := backend.ContractBackend.SendTransaction(ctx, tx); err != nil {
		return err
	}
	if _, err := backend.run.recordSubmittedTransaction(context.WithoutCancel(ctx), backend.label, tx.Hash()); err != nil {
		return err
	}
	if backend.run.recorded == nil {
		backend.run.recorded = make(map[string]common.Hash)
	}
	backend.run.recorded[backend.label] = tx.Hash()
	return nil
}

func (run *suiteRun) setTransactionIdentity(sender common.Address, chainID *big.Int) error {
	if chainID == nil || chainID.Sign() <= 0 {
		return fmt.Errorf("Go ABI transaction chain ID must be positive")
	}
	if run.identitySet {
		if run.sender != sender || run.chainID.Cmp(chainID) != 0 {
			return fmt.Errorf("Go ABI transaction identity changed from sender %s chain %s to sender %s chain %s", run.sender, run.chainID, sender, chainID)
		}
		return nil
	}
	run.sender = sender
	run.chainID = new(big.Int).Set(chainID)
	run.identitySet = true
	return nil
}

func (run *suiteRun) validateTransactionSemantics(label string, tx *types.Transaction, expected transactionSemantics) error {
	if tx == nil {
		return fmt.Errorf("prepared Go ABI transaction %q is nil", label)
	}
	if run == nil || !run.identitySet || run.chainID == nil {
		return fmt.Errorf("prepared Go ABI transaction %q cannot be validated without the expected sender and chain", label)
	}
	if tx.ChainId().Cmp(run.chainID) != 0 {
		return fmt.Errorf("prepared Go ABI transaction %q changed chain: have %s want %s", label, tx.ChainId(), run.chainID)
	}
	sender, err := types.Sender(types.LatestSignerForChainID(run.chainID), tx)
	if err != nil {
		return fmt.Errorf("prepared Go ABI transaction %q has an invalid sender: %w", label, err)
	}
	if sender != run.sender {
		return fmt.Errorf("prepared Go ABI transaction %q changed sender: have %s want %s", label, sender, run.sender)
	}
	actualRecipient := tx.To()
	if (actualRecipient == nil) != (expected.recipient == nil) || (actualRecipient != nil && *actualRecipient != *expected.recipient) {
		return fmt.Errorf("prepared Go ABI transaction %q changed recipient", label)
	}
	wantValue := expected.value
	if wantValue == nil {
		wantValue = new(big.Int)
	}
	if tx.Value().Cmp(wantValue) != 0 {
		return fmt.Errorf("prepared Go ABI transaction %q changed value: have %s want %s", label, tx.Value(), wantValue)
	}
	if !bytes.Equal(tx.Data(), expected.input) {
		return fmt.Errorf("prepared Go ABI transaction %q changed calldata or creation bytecode", label)
	}
	return nil
}

func newSuiteRun(options Options) (*suiteRun, error) {
	recorded, err := validateRecordedTransactions(options.RecordedTransactions)
	if err != nil {
		return nil, err
	}
	prepared, err := validatePreparedTransactions(options.PreparedTransactions)
	if err != nil {
		return nil, err
	}
	if err := validateTransactionJournals(recorded, prepared); err != nil {
		return nil, err
	}
	return &suiteRun{
		recorder:         options.TransactionRecorder,
		preparedRecorder: options.PreparedTransactionRecorder,
		receiptWaiter:    waitReceipt,
		recorded:         recorded,
		prepared:         prepared,
	}, nil
}

func validateTransactionJournals(recorded map[string]common.Hash, prepared map[string]*types.Transaction) error {
	preparedOnly := ""
	for label, tx := range prepared {
		if hash, submitted := recorded[label]; submitted {
			if hash != tx.Hash() {
				return fmt.Errorf("prepared Go ABI transaction %q hash %s differs from submitted hash %s", label, tx.Hash(), hash)
			}
			continue
		}
		if hash, recovered := recorded[label+"/recovered"]; recovered {
			if hash != tx.Hash() {
				return fmt.Errorf("prepared Go ABI transaction %q hash %s differs from recovered hash %s", label, tx.Hash(), hash)
			}
			continue
		}
		if preparedOnly != "" {
			return fmt.Errorf("prepared Go ABI transactions %q and %q are both unsubmitted", preparedOnly, label)
		}
		preparedOnly = label
	}
	if preparedOnly == "" {
		return nil
	}

	mandatoryCount := 0
	for mandatoryCount < len(mandatoryTransactionLabelsInSuiteOrder) {
		if _, ok := recorded[mandatoryTransactionLabelsInSuiteOrder[mandatoryCount]]; !ok {
			break
		}
		mandatoryCount++
	}
	if mandatoryCount < len(mandatoryTransactionLabelsInSuiteOrder) {
		want := mandatoryTransactionLabelsInSuiteOrder[mandatoryCount]
		if preparedOnly != want {
			return fmt.Errorf("prepared Go ABI transaction %q is out of order; next mutation is %q", preparedOnly, want)
		}
		return nil
	}

	graphqlNext, graphqlValidated, graphqlHistory := graphQLProbeState(recorded)
	if index, recovered, graphqlPrepared := graphQLProbeLabel(preparedOnly); graphqlPrepared {
		if recovered || graphqlValidated || index != graphqlNext {
			return fmt.Errorf("prepared GraphQL probe %q is out of order; next probe index is %d", preparedOnly, graphqlNext)
		}
		return nil
	}
	_, websocketSubmitted := recorded[TransactionWebSocketEmitterDeploy]
	if !websocketSubmitted {
		if preparedOnly == TransactionWebSocketEmitterDeploy {
			if graphqlHistory && !graphqlValidated {
				return fmt.Errorf("prepared websocket transaction precedes completion of recovered GraphQL probe %d", graphqlNext-1)
			}
			return nil
		}
		return fmt.Errorf("prepared Go ABI transaction %q is out of order after the base mutations", preparedOnly)
	}
	for index := 1; ; index++ {
		label := fmt.Sprintf("%s/resume-%d", TransactionWebSocketEmitterDeploy, index)
		if _, submitted := recorded[label]; submitted {
			continue
		}
		if preparedOnly != label {
			return fmt.Errorf("prepared Go ABI transaction %q is out of order; next websocket continuation is %q", preparedOnly, label)
		}
		return nil
	}
}

func validatePreparedTransactions(values map[string]PreparedTransaction) (map[string]*types.Transaction, error) {
	prepared := make(map[string]*types.Transaction, len(values))
	for label, value := range values {
		if !knownTransactionLabel(label) {
			return nil, fmt.Errorf("prepared Go ABI transaction label %q is unknown", label)
		}
		if _, recovered, graphQL := graphQLProbeLabel(label); graphQL && recovered {
			return nil, fmt.Errorf("prepared Go ABI transaction label %q cannot be a recovery marker", label)
		}
		raw, err := hexutil.Decode(value.Raw)
		if err != nil || len(raw) == 0 {
			return nil, fmt.Errorf("prepared Go ABI transaction %q has invalid raw bytes", label)
		}
		tx := new(types.Transaction)
		if err := tx.UnmarshalBinary(raw); err != nil {
			return nil, fmt.Errorf("decode prepared Go ABI transaction %q: %w", label, err)
		}
		if tx.Hash().Hex() != value.Hash {
			return nil, fmt.Errorf("prepared Go ABI transaction %q hash is %s, want %s", label, tx.Hash(), value.Hash)
		}
		prepared[label] = tx
	}
	return prepared, nil
}

func knownTransactionLabel(label string) bool {
	for _, known := range transactionLabelsInSuiteOrder {
		if label == known {
			return true
		}
	}
	if strings.HasPrefix(label, TransactionWebSocketEmitterDeploy+"/resume-") {
		suffix := strings.TrimPrefix(label, TransactionWebSocketEmitterDeploy+"/resume-")
		index, err := strconv.Atoi(suffix)
		return err == nil && index > 0 && strconv.Itoa(index) == suffix
	}
	if _, _, ok := graphQLProbeLabel(label); ok {
		return true
	}
	return false
}

func graphQLProbeName(index int) string {
	if index == 0 {
		return TransactionGraphQLSendRawTransaction
	}
	return fmt.Sprintf("%s/resume-%d", TransactionGraphQLSendRawTransaction, index)
}

func graphQLProbeLabel(label string) (index int, recovered bool, ok bool) {
	if strings.HasSuffix(label, "/recovered") {
		recovered = true
		label = strings.TrimSuffix(label, "/recovered")
	}
	if label == TransactionGraphQLSendRawTransaction {
		return 0, recovered, true
	}
	prefix := TransactionGraphQLSendRawTransaction + "/resume-"
	if !strings.HasPrefix(label, prefix) {
		return 0, false, false
	}
	raw := strings.TrimPrefix(label, prefix)
	index, err := strconv.Atoi(raw)
	if err != nil || index < 1 || strconv.Itoa(index) != raw {
		return 0, false, false
	}
	return index, recovered, true
}

func validateRecordedTransactions(values map[string]string) (map[string]common.Hash, error) {
	recorded := make(map[string]common.Hash, len(values))
	known := make(map[string]int, len(transactionLabelsInSuiteOrder))
	for index, label := range transactionLabelsInSuiteOrder {
		known[label] = index
	}
	websocketResumes := make([]int, 0)
	for label, raw := range values {
		if strings.HasPrefix(label, TransactionWebSocketEmitterDeploy+"/resume-") {
			suffix := strings.TrimPrefix(label, TransactionWebSocketEmitterDeploy+"/resume-")
			index, err := strconv.Atoi(suffix)
			if err != nil || index < 1 || strconv.Itoa(index) != suffix {
				return nil, fmt.Errorf("recorded Go ABI transaction label %q has an invalid websocket resume number", label)
			}
			websocketResumes = append(websocketResumes, index)
		} else if _, _, graphQL := graphQLProbeLabel(label); !graphQL {
			if _, ok := known[label]; !ok {
				return nil, fmt.Errorf("recorded Go ABI transaction label %q is unknown", label)
			}
		}
		var hash common.Hash
		if err := hash.UnmarshalText([]byte(raw)); err != nil || hash == (common.Hash{}) || hash.Hex() != strings.ToLower(raw) {
			return nil, fmt.Errorf("recorded Go ABI transaction %q has invalid canonical hash %q", label, raw)
		}
		recorded[label] = hash
	}
	if err := validateGraphQLProbeHistory(recorded); err != nil {
		return nil, err
	}

	// Mandatory mutations are serialized. Optional GraphQL/websocket evidence
	// is intentionally ignored by this predecessor check: journals produced by
	// the older suite can already contain those labels before the VM64 probe
	// namespace was introduced, and the new mutations can safely continue from
	// that durable point.
	for index, label := range mandatoryTransactionLabelsInSuiteOrder {
		if _, ok := recorded[label]; ok {
			continue
		}
		for later := index + 1; later < len(mandatoryTransactionLabelsInSuiteOrder); later++ {
			if _, exists := recorded[mandatoryTransactionLabelsInSuiteOrder[later]]; exists {
				return nil, fmt.Errorf("recorded Go ABI transaction %q is missing predecessor %q", mandatoryTransactionLabelsInSuiteOrder[later], label)
			}
		}
		break
	}
	// The original six mutations predate every optional probe. Preserve the
	// stronger historical rule for those labels and their continuations.
	for index := 0; index < 6; index++ {
		label := mandatoryTransactionLabelsInSuiteOrder[index]
		if _, ok := recorded[label]; ok {
			continue
		}
		if len(websocketResumes) != 0 {
			return nil, fmt.Errorf("recorded websocket continuation is missing predecessor %q", label)
		}
		if _, _, history := graphQLProbeState(recorded); history {
			return nil, fmt.Errorf("recorded GraphQL probe history is missing predecessor %q", label)
		}
		if _, exists := recorded[TransactionWebSocketEmitterDeploy]; exists {
			return nil, fmt.Errorf("recorded Go ABI transaction %q is missing predecessor %q", TransactionWebSocketEmitterDeploy, label)
		}
		break
	}
	if len(websocketResumes) != 0 {
		if _, ok := recorded[TransactionWebSocketEmitterDeploy]; !ok {
			return nil, fmt.Errorf("recorded websocket continuation is missing base transaction %q", TransactionWebSocketEmitterDeploy)
		}
		sort.Ints(websocketResumes)
		for index, value := range websocketResumes {
			if value != index+1 {
				return nil, fmt.Errorf("recorded websocket continuation sequence has a gap before resume-%d", value)
			}
		}
	}
	return recorded, nil
}

func validateGraphQLProbeHistory(recorded map[string]common.Hash) error {
	seen := make(map[int]string)
	for label := range recorded {
		index, recovered, ok := graphQLProbeLabel(label)
		if !ok {
			continue
		}
		kind := "validated"
		if recovered {
			kind = "recovered"
		}
		if previous, exists := seen[index]; exists {
			return fmt.Errorf("recorded GraphQL probe %d is both %s and %s", index, previous, kind)
		}
		seen[index] = kind
	}
	for index := 0; index < len(seen); index++ {
		kind, ok := seen[index]
		if !ok {
			return fmt.Errorf("recorded GraphQL probe history has a gap before resume-%d", index)
		}
		if kind == "validated" && index != len(seen)-1 {
			return fmt.Errorf("recorded GraphQL probe history continues after validated probe %d", index)
		}
	}
	return nil
}

func graphQLProbeState(recorded map[string]common.Hash) (next int, validated bool, hasHistory bool) {
	for index := 0; ; index++ {
		label := graphQLProbeName(index)
		if _, ok := recorded[label]; ok {
			return index + 1, true, true
		}
		if _, ok := recorded[label+"/recovered"]; ok {
			hasHistory = true
			continue
		}
		return index, false, hasHistory
	}
}

func (run *suiteRun) recordedHash(label string) (common.Hash, bool) {
	if run == nil {
		return common.Hash{}, false
	}
	hash, ok := run.recorded[label]
	return hash, ok
}

func (run *suiteRun) transactionForLabel(label string) (recordedTransaction, bool) {
	hash, ok := run.recordedHash(label)
	return recordedTransaction{hash: hash}, ok
}

func (run *suiteRun) prepareTransaction(label string, tx *types.Transaction) error {
	if tx == nil {
		return fmt.Errorf("prepare transaction %s: transaction is nil", label)
	}
	if existing, ok := run.prepared[label]; ok {
		if existing.Hash() != tx.Hash() {
			return fmt.Errorf("prepared transaction %s changed from %s to %s", label, existing.Hash(), tx.Hash())
		}
		return nil
	}
	raw, err := tx.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal prepared transaction %s: %w", label, err)
	}
	if run.preparedRecorder != nil {
		if err := run.preparedRecorder.RecordPreparedTransaction(label, tx.Hash().Hex(), hexutil.Encode(raw)); err != nil {
			return fmt.Errorf("journal prepared transaction %s (%s): %w", label, tx.Hash(), err)
		}
	}
	if run.prepared == nil {
		run.prepared = make(map[string]*types.Transaction)
	}
	run.prepared[label] = tx
	return nil
}

func (run *suiteRun) ensurePreparedSubmitted(ctx context.Context, label string, client transactionSubmitter, expected transactionSemantics) (recordedTransaction, bool, error) {
	recorded, submitted := run.transactionForLabel(label)
	tx, prepared := run.prepared[label]
	if submitted && !prepared {
		// Legacy journals recorded only the submitted hash. Preserve their
		// historical behavior because there are no durable raw bytes to replay.
		return recorded, true, nil
	}
	if !prepared {
		return recordedTransaction{}, false, nil
	}
	// Semantic validation deliberately precedes every transaction lookup or
	// rebroadcast. A corrupt checkpoint must not be able to turn a familiar
	// label into a different state mutation.
	if err := run.validateTransactionSemantics(label, tx, expected); err != nil {
		return recordedTransaction{}, false, err
	}
	if submitted && recorded.hash != tx.Hash() {
		return recordedTransaction{}, false, fmt.Errorf("recorded transaction %s as %s differs from prepared transaction %s", recorded.hash, label, tx.Hash())
	}
	if err := ensureExactPreparedTransactionSubmitted(ctx, label, tx, client); err != nil {
		return recordedTransaction{}, false, err
	}
	if submitted {
		return recorded, true, nil
	}
	recorded, err := run.recordSubmittedTransaction(context.WithoutCancel(ctx), label, tx.Hash())
	if err != nil {
		return recordedTransaction{}, false, err
	}
	return recorded, true, nil
}

func ensureExactPreparedTransactionSubmitted(ctx context.Context, label string, tx *types.Transaction, client transactionSubmitter) error {
	if tx == nil {
		return fmt.Errorf("reconcile prepared transaction %s: transaction is nil", label)
	}
	if client == nil {
		return fmt.Errorf("reconcile prepared transaction %s (%s): transaction client is nil", label, tx.Hash())
	}
	found, _, lookupErr := client.TransactionByHash(ctx, tx.Hash())
	if lookupErr != nil && !errors.Is(lookupErr, qrl.NotFound) {
		return fmt.Errorf("look up prepared transaction %s as %s: %w", tx.Hash(), label, lookupErr)
	}
	if lookupErr == nil && (found == nil || found.Hash() != tx.Hash()) {
		return fmt.Errorf("look up prepared transaction %s as %s returned a different transaction", tx.Hash(), label)
	}
	if errors.Is(lookupErr, qrl.NotFound) {
		if err := client.SendTransaction(ctx, tx); err != nil {
			found, _, verifyErr := client.TransactionByHash(ctx, tx.Hash())
			if verifyErr != nil || found == nil || found.Hash() != tx.Hash() {
				return fmt.Errorf("rebroadcast prepared transaction %s as %s: %w", tx.Hash(), label, err)
			}
		}
	}
	return nil
}

func (run *suiteRun) submitPreparedAndWait(ctx context.Context, label string, client *qrlclient.Client, tx *types.Transaction, expected transactionSemantics) (*types.Receipt, error) {
	if err := run.validateTransactionSemantics(label, tx, expected); err != nil {
		return nil, err
	}
	if recorded, ok, err := run.ensurePreparedSubmitted(ctx, label, client, expected); err != nil {
		return nil, err
	} else if ok {
		return run.waitRecordedReceipt(ctx, client, recorded)
	}
	if err := run.prepareTransaction(label, tx); err != nil {
		return nil, err
	}
	if err := client.SendTransaction(ctx, tx); err != nil {
		return nil, fmt.Errorf("send tx %s: %w", tx.Hash().Hex(), err)
	}
	recorded, err := run.recordSubmittedTransaction(context.WithoutCancel(ctx), label, tx.Hash())
	if err != nil {
		return nil, err
	}
	return run.waitRecordedReceipt(ctx, client, recorded)
}

func (run *suiteRun) recordSubmittedTransaction(ctx context.Context, label string, hash common.Hash) (recordedTransaction, error) {
	if run.recorder != nil {
		if err := run.recorder.RecordTransaction(label, hash.Hex()); err != nil {
			return recordedTransaction{}, fmt.Errorf("transaction %s as %s was submitted but could not be recorded: %w", hash.Hex(), label, err)
		}
	}
	if run.recorded == nil {
		run.recorded = make(map[string]common.Hash)
	}
	run.recorded[label] = hash
	return recordedTransaction{hash: hash}, nil
}

func (run *suiteRun) waitRecordedReceipt(ctx context.Context, client *qrlclient.Client, transaction recordedTransaction) (*types.Receipt, error) {
	return run.receiptWaiter(ctx, client, transaction.hash)
}

func requireSuccessfulMinedReceipt(ctx context.Context, label string, hash common.Hash, wait transactionReceiptWaiter) (*types.Receipt, error) {
	if wait == nil {
		return nil, fmt.Errorf("wait for prepared transaction %s as %s: receipt waiter is nil", hash, label)
	}
	receipt, err := wait(ctx, hash)
	if err != nil {
		return nil, fmt.Errorf("wait for prepared transaction %s as %s: %w", hash, label, err)
	}
	if receipt == nil || receipt.BlockNumber == nil || receipt.BlockNumber.Sign() <= 0 {
		return nil, fmt.Errorf("prepared transaction %s as %s has no mined receipt", hash, label)
	}
	if receipt.TxHash != hash {
		return nil, fmt.Errorf("prepared transaction %s as %s returned receipt for %s", hash, label, receipt.TxHash)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("prepared transaction %s as %s failed with status %d", hash, label, receipt.Status)
	}
	return receipt, nil
}

func (run *suiteRun) recordAndWaitReceipt(ctx context.Context, label string, client *qrlclient.Client, hash common.Hash) (*types.Receipt, error) {
	transaction, err := run.recordSubmittedTransaction(ctx, label, hash)
	if err != nil {
		return nil, err
	}
	return run.waitRecordedReceipt(ctx, client, transaction)
}

func (run *suiteRun) resumeOrRecordAndWaitReceipt(ctx context.Context, label string, client *qrlclient.Client, expected transactionSemantics, submit func() (common.Hash, error)) (*types.Receipt, error) {
	if transaction, ok, err := run.ensurePreparedSubmitted(ctx, label, client, expected); err != nil {
		return nil, err
	} else if ok {
		return run.waitRecordedReceipt(ctx, client, transaction)
	}
	hash, err := submit()
	if err != nil {
		return nil, err
	}
	return run.recordAndWaitReceipt(ctx, label, client, hash)
}
