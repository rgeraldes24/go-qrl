// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package lifecycle

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
)

const SchemaVersion = 1

var (
	uuidPattern     = regexp.MustCompile(`^[0-9a-f]{32}$`)
	shaPattern      = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	txHashPattern   = regexp.MustCompile(`^0x[0-9a-f]{64}$`)
	hexDataPattern  = regexp.MustCompile(`^0x(?:[0-9a-f]{2})*$`)
	addressPattern  = regexp.MustCompile(`^Q[0-9a-f]{128}$`)
	quantityPattern = regexp.MustCompile(`^0x(?:0|[1-9a-f][0-9a-f]*)$`)
)

type EnclaveRef struct {
	Name  string `json:"name"`
	UUID  string `json:"uuid"`
	Owned bool   `json:"owned"`
}

func (ref EnclaveRef) Validate() error {
	if ref.Name == "" {
		return errors.New("enclave name is empty")
	}
	if !uuidPattern.MatchString(ref.UUID) {
		return fmt.Errorf("enclave UUID %q is not a full 32-character lowercase UUID", ref.UUID)
	}
	return nil
}

type OwnershipRecord struct {
	Schema             int        `json:"schema"`
	RunID              string     `json:"run_id"`
	RequestedName      string     `json:"requested_name"`
	CreatedAt          time.Time  `json:"created_at"`
	UUID               *string    `json:"uuid"`
	DestroyRequestedAt *time.Time `json:"destroy_requested_at,omitempty"`
	DestroyedAt        *time.Time `json:"destroyed_at,omitempty"`
	Preserved          bool       `json:"preserved,omitempty"`
	PreserveReason     string     `json:"preserve_reason,omitempty"`
}

func (record OwnershipRecord) Validate() error {
	if record.Schema != SchemaVersion {
		return fmt.Errorf("ownership schema is %d, want %d", record.Schema, SchemaVersion)
	}
	if record.RunID == "" || record.RequestedName == "" || record.CreatedAt.IsZero() {
		return errors.New("ownership record is missing run ID, requested name, or creation time")
	}
	if record.UUID != nil && !uuidPattern.MatchString(*record.UUID) {
		return fmt.Errorf("ownership UUID %q is invalid", *record.UUID)
	}
	if (record.DestroyRequestedAt != nil || record.DestroyedAt != nil) && record.UUID == nil {
		return errors.New("ownership record cannot request or record destruction without a captured UUID")
	}
	if record.DestroyRequestedAt != nil {
		if record.DestroyRequestedAt.IsZero() {
			return errors.New("ownership destruction request time is zero")
		}
		_, offset := record.DestroyRequestedAt.Zone()
		if offset != 0 {
			return errors.New("ownership destruction request time is not UTC")
		}
		if record.DestroyRequestedAt.Before(record.CreatedAt) {
			return errors.New("ownership destruction request predates ownership creation")
		}
	}
	if record.DestroyedAt != nil {
		if record.DestroyedAt.IsZero() {
			return errors.New("ownership destruction time is zero")
		}
		_, offset := record.DestroyedAt.Zone()
		if offset != 0 {
			return errors.New("ownership destruction time is not UTC")
		}
		if record.DestroyedAt.Before(record.CreatedAt) {
			return errors.New("ownership destruction predates ownership creation")
		}
		if record.DestroyRequestedAt != nil && record.DestroyedAt.Before(*record.DestroyRequestedAt) {
			return errors.New("ownership destruction predates its durable request")
		}
	}
	return nil
}

type Status string

const (
	StatusRunning             Status = "running"
	StatusFailed              Status = "failed"
	StatusCompleteClean       Status = "complete_clean"
	StatusCompleteAfterResume Status = "complete_after_resume"
	StatusCleanedAfterFailure Status = "cleaned_after_failure"
)

type FailureCategory string

const (
	FailureNone           FailureCategory = ""
	FailureAssertion      FailureCategory = "assertion_failure"
	FailureTimeout        FailureCategory = "timeout"
	FailureCancellation   FailureCategory = "cancellation"
	FailureProcessExit    FailureCategory = "process_exit"
	FailureSDK            FailureCategory = "sdk_error"
	FailureInfrastructure FailureCategory = "infrastructure_failure"
	FailureCleanup        FailureCategory = "cleanup_failure"
)

type Attempt struct {
	Stage           string          `json:"stage"`
	Attempt         int             `json:"attempt"`
	StartedAt       time.Time       `json:"started_at"`
	FinishedAt      *time.Time      `json:"finished_at"`
	ExitCode        *int            `json:"exit_code"`
	Reconciled      bool            `json:"reconciled,omitempty"`
	FailureCategory FailureCategory `json:"failure_category,omitempty"`
	FailureMessage  string          `json:"failure_message,omitempty"`
}

type ResumeEvent struct {
	At                  time.Time `json:"at"`
	TreeID              string    `json:"tree_id"`
	ConfigurationDigest string    `json:"configuration_digest"`
	ReconciledStage     string    `json:"reconciled_stage,omitempty"`
}

type RestartRecord struct {
	ServiceName string     `json:"service_name"`
	ServiceUUID string     `json:"service_uuid"`
	StoppedAt   time.Time  `json:"stopped_at"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
}

type ServiceTransition struct {
	Phase       string    `json:"phase"`
	ServiceName string    `json:"service_name"`
	ServiceUUID string    `json:"service_uuid"`
	State       string    `json:"state"`
	At          time.Time `json:"at"`
}

type EndpointRefresh struct {
	Phase       string    `json:"phase"`
	ServiceName string    `json:"service_name"`
	ServiceUUID string    `json:"service_uuid"`
	Kind        string    `json:"kind"`
	Previous    string    `json:"previous"`
	Current     string    `json:"current"`
	At          time.Time `json:"at"`
}

// PreparedTransaction journals the exact signed bytes before broadcast. A
// resume can query Hash and, when absent, rebroadcast Raw without creating a
// second logical transaction.
type PreparedTransaction struct {
	Hash string `json:"hash"`
	Raw  string `json:"raw"`
}

// ManagedTransactionIntent identifies a qrl_sendTransaction request before
// the node chooses fees and returns a signed hash. Nonce plus exact arguments
// let resume find the transaction in the canonical chain or txpool.
type ManagedAccessTuple struct {
	Address     string   `json:"address"`
	StorageKeys []string `json:"storage_keys"`
}

type ManagedTransactionIntent struct {
	Phase             string               `json:"phase"`
	Label             string               `json:"label"`
	Origin            int                  `json:"origin"`
	OriginServiceName string               `json:"origin_service_name"`
	OriginServiceUUID string               `json:"origin_service_uuid"`
	ChainID           string               `json:"chain_id"`
	From              string               `json:"from"`
	To                string               `json:"to"`
	Value             string               `json:"value"`
	Input             string               `json:"input"`
	AccessList        []ManagedAccessTuple `json:"access_list"`
	Nonce             uint64               `json:"nonce"`
	StartBlock        uint64               `json:"start_block"`
	StartBlockHash    string               `json:"start_block_hash"`
	PreparedAt        time.Time            `json:"prepared_at"`
}

// TemporaryServiceCreationIntent is written before a fresh-sync service-add
// call. Marker is also attached to the created Kurtosis service, which lets a
// resumed run distinguish its exact creation from an unrelated name reuse.
type TemporaryServiceCreationIntent struct {
	Name         string    `json:"name"`
	EnclaveUUID  string    `json:"enclave_uuid"`
	ConfigDigest string    `json:"config_digest"`
	Marker       string    `json:"marker"`
	PreparedAt   time.Time `json:"prepared_at"`
}

// Checkpoint deliberately retains the original Python schema-v1 field names.
// New Go-runner evidence is additive so old fixtures remain readable.
type Checkpoint struct {
	Schema                            int                                       `json:"schema"`
	RunID                             string                                    `json:"run_id,omitempty"`
	SourceSHA                         string                                    `json:"source_sha"`
	ConfigurationDigest               string                                    `json:"configuration_digest,omitempty"`
	Enclave                           EnclaveRef                                `json:"enclave"`
	DumpDir                           string                                    `json:"dump_dir"`
	InitialTreeID                     string                                    `json:"initial_tree_id"`
	ResumeTreeIDs                     []string                                  `json:"resume_tree_ids"`
	Status                            Status                                    `json:"status"`
	CurrentStage                      *string                                   `json:"current_stage"`
	Completed                         []string                                  `json:"completed"`
	Attempts                          []Attempt                                 `json:"attempts"`
	Resumed                           bool                                      `json:"resumed"`
	CreatedAt                         time.Time                                 `json:"created_at"`
	UpdatedAt                         time.Time                                 `json:"updated_at"`
	Transactions                      map[string]string                         `json:"transaction_hashes,omitempty"`
	PreparedTransactions              map[string]PreparedTransaction            `json:"prepared_transactions,omitempty"`
	ManagedTransactionIntents         map[string]ManagedTransactionIntent       `json:"managed_transaction_intents,omitempty"`
	ManagedTransactionInitialAttempts map[string]time.Time                      `json:"managed_transaction_initial_attempts,omitempty"`
	ManagedTransactionResubmits       map[string]time.Time                      `json:"managed_transaction_resubmits,omitempty"`
	SystemObservations                map[string]string                         `json:"system_observations,omitempty"`
	TemporaryServiceCreationIntents   map[string]TemporaryServiceCreationIntent `json:"temporary_service_creation_intents,omitempty"`
	TemporaryServices                 map[string]string                         `json:"temporary_service_uuids,omitempty"`
	RestartedServices                 []RestartRecord                           `json:"restarted_services,omitempty"`
	ServiceTransitions                []ServiceTransition                       `json:"service_transitions,omitempty"`
	EndpointRefreshes                 []EndpointRefresh                         `json:"endpoint_refreshes,omitempty"`
	FailureCategory                   FailureCategory                           `json:"failure_category,omitempty"`
	FailureMessage                    string                                    `json:"failure_message,omitempty"`
	ResumeHistory                     []ResumeEvent                             `json:"resume_history,omitempty"`
}

func NewCheckpoint(runID, sourceSHA, configDigest, dumpDir, treeID string, enclave EnclaveRef, now time.Time) Checkpoint {
	return Checkpoint{
		Schema:                            SchemaVersion,
		RunID:                             runID,
		SourceSHA:                         sourceSHA,
		ConfigurationDigest:               configDigest,
		Enclave:                           enclave,
		DumpDir:                           dumpDir,
		InitialTreeID:                     treeID,
		ResumeTreeIDs:                     []string{},
		Status:                            StatusRunning,
		Completed:                         []string{},
		Attempts:                          []Attempt{},
		CreatedAt:                         now.UTC(),
		UpdatedAt:                         now.UTC(),
		Transactions:                      map[string]string{},
		PreparedTransactions:              map[string]PreparedTransaction{},
		ManagedTransactionIntents:         map[string]ManagedTransactionIntent{},
		ManagedTransactionInitialAttempts: map[string]time.Time{},
		ManagedTransactionResubmits:       map[string]time.Time{},
		SystemObservations:                map[string]string{},
		TemporaryServiceCreationIntents:   map[string]TemporaryServiceCreationIntent{},
		TemporaryServices:                 map[string]string{},
		RestartedServices:                 []RestartRecord{},
		ServiceTransitions:                []ServiceTransition{},
		EndpointRefreshes:                 []EndpointRefresh{},
		ResumeHistory:                     []ResumeEvent{},
	}
}

func (state Checkpoint) Validate(stageOrder []string) error {
	if state.Schema != SchemaVersion {
		return fmt.Errorf("checkpoint schema is %d, want %d", state.Schema, SchemaVersion)
	}
	if !shaPattern.MatchString(state.SourceSHA) {
		return errors.New("checkpoint source SHA is invalid")
	}
	if state.ConfigurationDigest != "" && !digestPattern.MatchString(state.ConfigurationDigest) {
		return errors.New("checkpoint configuration digest is invalid")
	}
	if err := state.Enclave.Validate(); err != nil {
		return err
	}
	if state.DumpDir == "" || !digestPattern.MatchString(state.InitialTreeID) {
		return errors.New("checkpoint dump directory or initial tree ID is invalid")
	}
	for _, treeID := range state.ResumeTreeIDs {
		if !digestPattern.MatchString(treeID) {
			return errors.New("checkpoint contains an invalid resume tree ID")
		}
	}
	if len(state.Completed) > len(stageOrder) || !slices.Equal(state.Completed, stageOrder[:len(state.Completed)]) {
		return errors.New("completed checkpoint stages are not an exact ordered prefix")
	}
	validStatus := state.Status == StatusRunning || state.Status == StatusFailed || state.Status == StatusCompleteClean || state.Status == StatusCompleteAfterResume || state.Status == StatusCleanedAfterFailure
	if !validStatus {
		return fmt.Errorf("checkpoint status %q is invalid", state.Status)
	}
	if state.CurrentStage != nil && !slices.Contains(stageOrder, *state.CurrentStage) {
		return fmt.Errorf("checkpoint current stage %q is invalid", *state.CurrentStage)
	}
	expectedIndex := 0
	unfinished := -1
	for i, attempt := range state.Attempts {
		if expectedIndex >= len(stageOrder) || attempt.Stage != stageOrder[expectedIndex] {
			return fmt.Errorf("attempt %d for stage %q is out of order", i, attempt.Stage)
		}
		if attempt.Attempt < 1 || attempt.StartedAt.IsZero() || (attempt.FinishedAt == nil) != (attempt.ExitCode == nil) {
			return fmt.Errorf("attempt %d is incomplete or malformed", i)
		}
		if attempt.FinishedAt == nil {
			if unfinished >= 0 || i != len(state.Attempts)-1 {
				return errors.New("checkpoint has more than one or a non-final unfinished attempt")
			}
			unfinished = i
			continue
		}
		if *attempt.ExitCode == 0 {
			expectedIndex++
		}
	}
	if expectedIndex != len(state.Completed) {
		return errors.New("successful attempts do not match completed stages")
	}
	if unfinished >= 0 {
		attempt := state.Attempts[unfinished]
		if state.Status != StatusRunning || state.CurrentStage == nil || *state.CurrentStage != attempt.Stage {
			return errors.New("checkpoint running attempt does not match current stage")
		}
	}
	if state.Status == StatusFailed {
		if len(state.Attempts) == 0 || state.Attempts[len(state.Attempts)-1].ExitCode == nil || *state.Attempts[len(state.Attempts)-1].ExitCode == 0 || state.CurrentStage == nil {
			return errors.New("failed checkpoint does not identify a failed attempt")
		}
		if *state.CurrentStage != state.Attempts[len(state.Attempts)-1].Stage {
			return errors.New("failed checkpoint current stage does not match failed attempt")
		}
	}
	if state.Status == StatusCompleteClean || state.Status == StatusCompleteAfterResume {
		if len(state.Completed) != len(stageOrder) || state.CurrentStage != nil || unfinished >= 0 {
			return errors.New("successful checkpoint is not fully complete")
		}
	}
	for label, prepared := range state.PreparedTransactions {
		if label == "" || validatePreparedTransaction(prepared) != nil {
			return fmt.Errorf("prepared transaction %q is invalid", label)
		}
		if submitted, exists := state.Transactions[label]; exists && submitted != prepared.Hash {
			return fmt.Errorf("prepared transaction %q hash differs from submitted hash", label)
		}
	}
	for label, intent := range state.ManagedTransactionIntents {
		if err := validateManagedTransactionIntent(label, intent); err != nil {
			return fmt.Errorf("managed transaction intent %q is invalid", label)
		}
	}
	for label, at := range state.ManagedTransactionInitialAttempts {
		intent, ok := state.ManagedTransactionIntents[label]
		if !ok || at.IsZero() || at.Before(intent.PreparedAt) {
			return fmt.Errorf("managed transaction initial attempt %q is invalid", label)
		}
	}
	for label, at := range state.ManagedTransactionResubmits {
		initial, initialOK := state.ManagedTransactionInitialAttempts[label]
		if _, intentOK := state.ManagedTransactionIntents[label]; !intentOK || !initialOK || at.IsZero() || at.Before(initial) {
			return fmt.Errorf("managed transaction resubmit %q is invalid", label)
		}
	}
	for label, value := range state.SystemObservations {
		if label == "" || value == "" || !json.Valid([]byte(value)) {
			return fmt.Errorf("system observation %q is invalid", label)
		}
	}
	for name, intent := range state.TemporaryServiceCreationIntents {
		if name == "" || intent.Name != name || intent.EnclaveUUID != state.Enclave.UUID || !uuidPattern.MatchString(intent.EnclaveUUID) || !digestPattern.MatchString(intent.ConfigDigest) || !digestPattern.MatchString(intent.Marker) || intent.PreparedAt.IsZero() || intent.PreparedAt.Before(state.CreatedAt) {
			return fmt.Errorf("temporary service creation intent %q is invalid", name)
		}
		_, offset := intent.PreparedAt.Zone()
		if offset != 0 {
			return fmt.Errorf("temporary service creation intent %q preparation time is not UTC", name)
		}
	}
	return nil
}

func validateManagedTransactionIntent(label string, intent ManagedTransactionIntent) error {
	if label == "" || intent.Label != label || intent.Phase == "" || intent.Origin < 0 || intent.OriginServiceName == "" || !uuidPattern.MatchString(intent.OriginServiceUUID) {
		return errors.New("managed transaction identity is invalid")
	}
	if !quantityPattern.MatchString(intent.ChainID) || intent.ChainID == "0x0" || !addressPattern.MatchString(intent.From) || intent.To != "" && !addressPattern.MatchString(intent.To) {
		return errors.New("managed transaction chain or address is invalid")
	}
	if !quantityPattern.MatchString(intent.Value) || !hexDataPattern.MatchString(intent.Input) || !txHashPattern.MatchString(intent.StartBlockHash) || intent.PreparedAt.IsZero() {
		return errors.New("managed transaction value, input, block, or preparation time is invalid")
	}
	_, offset := intent.PreparedAt.Zone()
	if offset != 0 {
		return errors.New("managed transaction preparation time is not UTC")
	}
	for _, tuple := range intent.AccessList {
		if !addressPattern.MatchString(tuple.Address) {
			return errors.New("managed transaction access-list address is invalid")
		}
		for _, key := range tuple.StorageKeys {
			if !txHashPattern.MatchString(key) {
				return errors.New("managed transaction access-list storage key is invalid")
			}
		}
	}
	return nil
}

func validatePreparedTransaction(prepared PreparedTransaction) error {
	if !txHashPattern.MatchString(prepared.Hash) || len(prepared.Raw) <= 2 || !hexDataPattern.MatchString(prepared.Raw) {
		return errors.New("prepared transaction hash or raw bytes are invalid")
	}
	raw, err := hexutil.Decode(prepared.Raw)
	if err != nil {
		return err
	}
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		return err
	}
	canonical, err := tx.MarshalBinary()
	if err != nil {
		return err
	}
	if !bytes.Equal(canonical, raw) {
		return errors.New("prepared transaction raw bytes are not canonically encoded")
	}
	if tx.Hash().Hex() != prepared.Hash {
		return fmt.Errorf("decoded transaction hash %s differs from journal hash %s", tx.Hash(), prepared.Hash)
	}
	chainID := tx.ChainId()
	if chainID == nil || chainID.Sign() <= 0 {
		return errors.New("prepared transaction has no positive chain ID")
	}
	if _, err := types.Sender(types.LatestSignerForChainID(chainID), tx); err != nil {
		return fmt.Errorf("prepared transaction signature is invalid: %w", err)
	}
	return nil
}

func (state Checkpoint) StageComplete(name string) bool {
	return slices.Contains(state.Completed, name)
}
