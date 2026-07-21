// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"reflect"
	"sort"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
)

const (
	signerAccountBaselineObservation       = "signer-restart/account-baseline"
	signerOutageAssertedObservation        = "signer-restart/outage-asserted"
	signerQuiescenceAssertedObservation    = "signer-restart/cancellation-quiescent"
	participantFaultBaselineObservation    = "participant-restart/fault-baseline"
	participantOfflineAdvancedObservation  = "participant-restart/offline-window-advanced"
	participantDataPlaneObservation        = "participant-restart/data-plane-recovered"
	participantRecoveryBaselineObservation = "participant-restart/recovery-validator-baseline"
	participantProposalObservation         = "participant-restart/scheduled-proposal-asserted"
	participantAggregateObservation        = "participant-restart/aggregate-validator-progress"
	participantConsensusObservation        = "participant-restart/consensus-finality-asserted"
	participantExecutionObservation        = "participant-restart/execution-finality-asserted"
	participantEveryValidatorObservation   = "participant-restart/every-validator-attested"
)

type assertionMilestone struct {
	Version   int  `json:"version"`
	Completed bool `json:"completed"`
}

type managedAccountStateEvidence struct {
	Head             uint64 `json:"head"`
	Nonce            uint64 `json:"nonce"`
	PendingNonce     uint64 `json:"pending_nonce"`
	RecipientBalance string `json:"recipient_balance"`
}

type managedAccountBaselineEvidence struct {
	Version int                            `json:"version"`
	EL      [2]managedAccountStateEvidence `json:"el"`
}

type executionFinalityEvidence struct {
	SafeNumber      uint64 `json:"safe_number"`
	SafeHash        string `json:"safe_hash"`
	FinalizedNumber uint64 `json:"finalized_number"`
	FinalizedHash   string `json:"finalized_hash"`
}

type validatorDutyEvidence struct {
	ReportedValidators     int      `json:"reported_validators"`
	ActiveValidators       int      `json:"active_validators"`
	AttestedValidators     int      `json:"attested_validators"`
	ActivePubkeys          []string `json:"active_pubkeys"`
	ProcessStartTime       float64  `json:"process_start_time"`
	SuccessfulAttestations float64  `json:"successful_attestations"`
	SuccessfulProposals    float64  `json:"successful_proposals"`
	FailedAttestations     float64  `json:"failed_attestations"`
	FailedProposals        float64  `json:"failed_proposals"`
}

type participantFaultBaselineEvidence struct {
	Version           int                       `json:"version"`
	FinalizedEpoch    uint64                    `json:"finalized_epoch"`
	HeadSlot          uint64                    `json:"head_slot"`
	ExecutionFinality executionFinalityEvidence `json:"execution_finality"`
	ValidatorDuties   [2]validatorDutyEvidence  `json:"validator_duties"`
}

type participantOfflineAdvancedEvidence struct {
	Version         int    `json:"version"`
	TransactionHash string `json:"transaction_hash"`
	InclusionBlock  uint64 `json:"inclusion_block"`
	TargetBlock     uint64 `json:"target_block"`
}

type participantDataPlaneEvidence struct {
	Version         int    `json:"version"`
	TransactionHash string `json:"transaction_hash"`
	MinimumBlock    uint64 `json:"minimum_block"`
	ConvergedBlock  uint64 `json:"converged_block"`
}

type participantRecoveryBaselineEvidence struct {
	Version  int                      `json:"version"`
	PreFault [2]validatorDutyEvidence `json:"pre_fault"`
	Baseline [2]validatorDutyEvidence `json:"baseline"`
	Observed [2]validatorDutyEvidence `json:"observed"`
}

func (s *systemCheck) recordTypedSystemObservation(ctx context.Context, label string, value any) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode system observation %s: %w", label, err)
	}
	return s.recordSystemObservation(ctx, label, string(payload))
}

func (s *systemCheck) recordAssertionMilestone(ctx context.Context, label string) error {
	return s.recordTypedSystemObservation(ctx, label, assertionMilestone{Version: 1, Completed: true})
}

func (s *systemCheck) hasAssertionMilestone(label string) bool {
	raw, ok := s.resume.observations[label]
	if !ok {
		return false
	}
	var milestone assertionMilestone
	return decodeStrictJSON(raw, &milestone) == nil && milestone.Version == 1 && milestone.Completed
}

func managedAccountBaselineToEvidence(states [2]managedAccountState) managedAccountBaselineEvidence {
	evidence := managedAccountBaselineEvidence{Version: 1}
	for index, state := range states {
		evidence.EL[index] = managedAccountStateEvidence{
			Head: state.head, Nonce: state.nonce, PendingNonce: state.pendingNonce,
			RecipientBalance: hexutil.EncodeBig(state.recipientBalance),
		}
	}
	return evidence
}

func managedAccountBaselineFromEvidence(evidence managedAccountBaselineEvidence) ([2]managedAccountState, error) {
	var states [2]managedAccountState
	if evidence.Version != 1 {
		return states, fmt.Errorf("managed account baseline version is %d, want 1", evidence.Version)
	}
	for index, value := range evidence.EL {
		balance, err := hexutil.DecodeBig(value.RecipientBalance)
		if err != nil || balance.Sign() < 0 || value.Head == 0 {
			return states, fmt.Errorf("EL%d managed account baseline is invalid", index+1)
		}
		states[index] = managedAccountState{
			head: value.Head, nonce: value.Nonce, pendingNonce: value.PendingNonce,
			recipientBalance: balance,
		}
	}
	if err := validateManagedAccountBaseline(states); err != nil {
		return states, err
	}
	return states, nil
}

func (s *systemCheck) recordedManagedAccountBaseline() ([2]managedAccountState, bool, error) {
	raw, ok := s.resume.observations[signerAccountBaselineObservation]
	if !ok {
		return [2]managedAccountState{}, false, nil
	}
	var evidence managedAccountBaselineEvidence
	if err := decodeStrictJSON(raw, &evidence); err != nil {
		return [2]managedAccountState{}, true, fmt.Errorf("decode signer managed-account baseline: %w", err)
	}
	states, err := managedAccountBaselineFromEvidence(evidence)
	return states, true, err
}

func executionFinalityToEvidence(status executionFinalityStatus) executionFinalityEvidence {
	return executionFinalityEvidence{
		SafeNumber: status.safeNumber, SafeHash: status.safeHash.Hex(),
		FinalizedNumber: status.finalizedNumber, FinalizedHash: status.finalizedHash.Hex(),
	}
}

func executionFinalityFromEvidence(evidence executionFinalityEvidence) (executionFinalityStatus, error) {
	if evidence.SafeNumber == 0 || evidence.FinalizedNumber == 0 || evidence.SafeNumber < evidence.FinalizedNumber {
		return executionFinalityStatus{}, errors.New("execution finality numbers are invalid")
	}
	var safeHash common.Hash
	if err := safeHash.UnmarshalText([]byte(evidence.SafeHash)); err != nil || safeHash == (common.Hash{}) || safeHash.Hex() != evidence.SafeHash {
		return executionFinalityStatus{}, errors.New("execution safe hash is invalid or non-canonical")
	}
	var finalizedHash common.Hash
	if err := finalizedHash.UnmarshalText([]byte(evidence.FinalizedHash)); err != nil || finalizedHash == (common.Hash{}) || finalizedHash.Hex() != evidence.FinalizedHash {
		return executionFinalityStatus{}, errors.New("execution finalized hash is invalid or non-canonical")
	}
	return executionFinalityStatus{
		safeNumber: evidence.SafeNumber, safeHash: safeHash,
		finalizedNumber: evidence.FinalizedNumber, finalizedHash: finalizedHash,
	}, nil
}

func validatorDutyToEvidence(snapshot validatorDutySnapshot) validatorDutyEvidence {
	pubkeys := make([]string, 0, len(snapshot.activePubkeys))
	for pubkey := range snapshot.activePubkeys {
		pubkeys = append(pubkeys, pubkey)
	}
	sort.Strings(pubkeys)
	return validatorDutyEvidence{
		ReportedValidators: snapshot.reportedValidators, ActiveValidators: snapshot.activeValidators,
		AttestedValidators: snapshot.attestedValidators, ActivePubkeys: pubkeys,
		ProcessStartTime: snapshot.processStartTime, SuccessfulAttestations: snapshot.successfulAttestations,
		SuccessfulProposals: snapshot.successfulProposals, FailedAttestations: snapshot.failedAttestations,
		FailedProposals: snapshot.failedProposals,
	}
}

func validatorDutyFromEvidence(evidence validatorDutyEvidence) (validatorDutySnapshot, error) {
	if evidence.ReportedValidators != expectedValidatorsPerClient || evidence.ActiveValidators != expectedValidatorsPerClient || evidence.AttestedValidators < 0 || evidence.AttestedValidators > expectedValidatorsPerClient || len(evidence.ActivePubkeys) != expectedValidatorsPerClient || evidence.ProcessStartTime <= 0 {
		return validatorDutySnapshot{}, errors.New("validator topology/count evidence is invalid")
	}
	values := []float64{evidence.ProcessStartTime, evidence.SuccessfulAttestations, evidence.SuccessfulProposals, evidence.FailedAttestations, evidence.FailedProposals}
	for _, value := range values {
		if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			return validatorDutySnapshot{}, errors.New("validator counter evidence is invalid")
		}
	}
	active := make(map[string]struct{}, len(evidence.ActivePubkeys))
	for _, pubkey := range evidence.ActivePubkeys {
		if pubkey == "" {
			return validatorDutySnapshot{}, errors.New("validator evidence has an empty pubkey")
		}
		if _, exists := active[pubkey]; exists {
			return validatorDutySnapshot{}, fmt.Errorf("validator evidence repeats pubkey %q", pubkey)
		}
		active[pubkey] = struct{}{}
	}
	return validatorDutySnapshot{
		reportedValidators: evidence.ReportedValidators, activeValidators: evidence.ActiveValidators,
		attestedValidators: evidence.AttestedValidators, activePubkeys: active,
		processStartTime: evidence.ProcessStartTime, successfulAttestations: evidence.SuccessfulAttestations,
		successfulProposals: evidence.SuccessfulProposals, failedAttestations: evidence.FailedAttestations,
		failedProposals: evidence.FailedProposals,
	}, nil
}

func validatorSnapshotsToEvidence(snapshots validatorDutySnapshots) [2]validatorDutyEvidence {
	return [2]validatorDutyEvidence{validatorDutyToEvidence(snapshots[0]), validatorDutyToEvidence(snapshots[1])}
}

func validatorSnapshotsFromEvidence(evidence [2]validatorDutyEvidence) (validatorDutySnapshots, error) {
	var snapshots validatorDutySnapshots
	for index := range evidence {
		snapshot, err := validatorDutyFromEvidence(evidence[index])
		if err != nil {
			return snapshots, fmt.Errorf("VC%d baseline: %w", index+1, err)
		}
		snapshots[index] = snapshot
	}
	if err := validateDisjointValidatorKeys(snapshots); err != nil {
		return snapshots, err
	}
	return snapshots, nil
}

func participantFaultBaselineToEvidence(finalizedEpoch, headSlot uint64, execution executionFinalityStatus, validators validatorDutySnapshots) participantFaultBaselineEvidence {
	return participantFaultBaselineEvidence{
		Version: 1, FinalizedEpoch: finalizedEpoch, HeadSlot: headSlot,
		ExecutionFinality: executionFinalityToEvidence(execution), ValidatorDuties: validatorSnapshotsToEvidence(validators),
	}
}

func participantFaultBaselineFromEvidence(evidence participantFaultBaselineEvidence) (uint64, uint64, executionFinalityStatus, validatorDutySnapshots, error) {
	if evidence.Version != 1 || evidence.FinalizedEpoch == 0 || evidence.HeadSlot == 0 {
		return 0, 0, executionFinalityStatus{}, validatorDutySnapshots{}, errors.New("participant fault baseline version/finality/head is invalid")
	}
	execution, err := executionFinalityFromEvidence(evidence.ExecutionFinality)
	if err != nil {
		return 0, 0, executionFinalityStatus{}, validatorDutySnapshots{}, err
	}
	validators, err := validatorSnapshotsFromEvidence(evidence.ValidatorDuties)
	if err != nil {
		return 0, 0, executionFinalityStatus{}, validatorDutySnapshots{}, err
	}
	return evidence.FinalizedEpoch, evidence.HeadSlot, execution, validators, nil
}

func (s *systemCheck) recordedParticipantFaultBaseline() (uint64, uint64, executionFinalityStatus, validatorDutySnapshots, bool, error) {
	raw, ok := s.resume.observations[participantFaultBaselineObservation]
	if !ok {
		return 0, 0, executionFinalityStatus{}, validatorDutySnapshots{}, false, nil
	}
	var evidence participantFaultBaselineEvidence
	if err := decodeStrictJSON(raw, &evidence); err != nil {
		return 0, 0, executionFinalityStatus{}, validatorDutySnapshots{}, true, err
	}
	epoch, head, execution, validators, err := participantFaultBaselineFromEvidence(evidence)
	return epoch, head, execution, validators, true, err
}

func decodeStrictJSON(raw string, target any) error {
	decoder := json.NewDecoder(bytes.NewBufferString(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return errors.New("JSON observation has trailing data")
	}
	return nil
}

func validateSystemObservationResumeState(cfg config, resume *resumeState) error {
	allowed := map[string]bool{}
	if cfg.phase == string(PhaseSignerRestart) || cfg.phase == string(PhaseAll) {
		for _, label := range []string{signerAccountBaselineObservation, signerOutageAssertedObservation, signerQuiescenceAssertedObservation} {
			allowed[label] = true
		}
	}
	if cfg.phase == string(PhaseParticipantRestart) || cfg.phase == string(PhaseAll) {
		for _, label := range []string{
			participantFaultBaselineObservation, participantOfflineAdvancedObservation, participantDataPlaneObservation,
			participantRecoveryBaselineObservation, participantProposalObservation, participantAggregateObservation,
			participantConsensusObservation, participantExecutionObservation, participantEveryValidatorObservation,
		} {
			allowed[label] = true
		}
	}
	for label, raw := range resume.observations {
		if !allowed[label] {
			return fmt.Errorf("system observation %q is not valid for phase %q", label, cfg.phase)
		}
		if err := validateSystemObservationValue(label, raw); err != nil {
			return fmt.Errorf("system observation %q is invalid: %w", label, err)
		}
	}
	if cfg.phase == string(PhaseSignerRestart) || cfg.phase == string(PhaseAll) {
		if len(resume.restarts[cfg.signerSvc]) > 0 && resume.observations[signerAccountBaselineObservation] == "" {
			return errors.New("signer restart history has no durable pre-outage account baseline")
		}
		if containsRestartState(currentRestartGeneration(resume.restarts[cfg.signerSvc]), RestartStartIntent) && resume.observations[signerOutageAssertedObservation] == "" {
			return errors.New("signer planned Start exists before the outage assertion milestone")
		}
		if _, submitted := resume.transactions[TransactionLabelSignerRecoveryTransfer]; submitted && resume.observations[signerQuiescenceAssertedObservation] == "" {
			return errors.New("signer recovery transaction exists before canceled-request quiescence was asserted")
		}
		if _, prepared := resume.managedIntents[TransactionLabelSignerRecoveryTransfer]; prepared && resume.observations[signerQuiescenceAssertedObservation] == "" {
			return errors.New("signer recovery transaction intent exists before canceled-request quiescence was asserted")
		}
		if resume.observations[signerQuiescenceAssertedObservation] != "" && resume.observations[signerOutageAssertedObservation] == "" {
			return errors.New("signer quiescence milestone has no outage assertion predecessor")
		}
	}
	if cfg.phase == string(PhaseParticipantRestart) || cfg.phase == string(PhaseAll) {
		services := []string{cfg.elServices[1], cfg.clServices[1], cfg.vcServices[1]}
		hasHistory := false
		for _, service := range services {
			hasHistory = hasHistory || len(resume.restarts[service]) > 0
		}
		if hasHistory && resume.observations[participantFaultBaselineObservation] == "" {
			return errors.New("participant restart history has no durable pre-fault baseline")
		}
		for _, service := range services {
			if containsRestartState(currentRestartGeneration(resume.restarts[service]), RestartStartIntent) && resume.observations[participantOfflineAdvancedObservation] == "" {
				return fmt.Errorf("participant planned Start for %s exists before the offline-window advancement milestone", service)
			}
		}
		if containsRestartState(currentRestartGeneration(resume.restarts[cfg.vcServices[1]]), RestartStartIntent) && resume.observations[participantDataPlaneObservation] == "" {
			return errors.New("VC2 planned Start exists before participant data-plane recovery was asserted")
		}
		ordered := []string{
			participantFaultBaselineObservation, participantOfflineAdvancedObservation, participantDataPlaneObservation,
			participantRecoveryBaselineObservation, participantProposalObservation, participantAggregateObservation,
			participantConsensusObservation, participantExecutionObservation, participantEveryValidatorObservation,
		}
		seenGap := false
		for _, label := range ordered {
			present := resume.observations[label] != ""
			if present && seenGap {
				return fmt.Errorf("participant observation %q is missing an ordered predecessor", label)
			}
			seenGap = seenGap || !present
		}
	}
	return nil
}

func validateSystemObservationValue(label, raw string) error {
	switch label {
	case signerAccountBaselineObservation:
		var evidence managedAccountBaselineEvidence
		if err := decodeStrictJSON(raw, &evidence); err != nil {
			return err
		}
		_, err := managedAccountBaselineFromEvidence(evidence)
		return err
	case participantFaultBaselineObservation:
		var evidence participantFaultBaselineEvidence
		if err := decodeStrictJSON(raw, &evidence); err != nil {
			return err
		}
		_, _, _, _, err := participantFaultBaselineFromEvidence(evidence)
		return err
	case participantOfflineAdvancedObservation:
		var evidence participantOfflineAdvancedEvidence
		if err := decodeStrictJSON(raw, &evidence); err != nil {
			return err
		}
		if evidence.Version != 1 || evidence.InclusionBlock == 0 || evidence.TargetBlock < evidence.InclusionBlock || !common.IsHexEncodedHash(evidence.TransactionHash) || common.HexToHash(evidence.TransactionHash).Hex() != evidence.TransactionHash {
			return errors.New("offline-window advancement evidence is malformed")
		}
		return nil
	case participantDataPlaneObservation:
		var evidence participantDataPlaneEvidence
		if err := decodeStrictJSON(raw, &evidence); err != nil {
			return err
		}
		if evidence.Version != 1 || evidence.MinimumBlock == 0 || evidence.ConvergedBlock <= evidence.MinimumBlock || !common.IsHexEncodedHash(evidence.TransactionHash) || common.HexToHash(evidence.TransactionHash).Hex() != evidence.TransactionHash {
			return errors.New("participant data-plane evidence is malformed")
		}
		return nil
	case participantRecoveryBaselineObservation:
		var evidence participantRecoveryBaselineEvidence
		if err := decodeStrictJSON(raw, &evidence); err != nil || evidence.Version != 1 {
			return errors.New("participant recovery baseline JSON/version is invalid")
		}
		preFault, err := validatorSnapshotsFromEvidence(evidence.PreFault)
		if err != nil {
			return err
		}
		baseline, err := validatorSnapshotsFromEvidence(evidence.Baseline)
		if err != nil {
			return err
		}
		observed, err := validatorSnapshotsFromEvidence(evidence.Observed)
		if err != nil {
			return err
		}
		derived, err := restartedValidatorBaseline(preFault, observed)
		if err != nil {
			return err
		}
		if !reflect.DeepEqual(derived, baseline) {
			return errors.New("persisted recovery validator baseline differs from the pre-fault/observed derivation")
		}
		return nil
	default:
		var milestone assertionMilestone
		if err := decodeStrictJSON(raw, &milestone); err != nil || milestone.Version != 1 || !milestone.Completed {
			return errors.New("assertion milestone is malformed")
		}
		return nil
	}
}

func decodeParticipantOfflineEvidence(raw string) (participantOfflineAdvancedEvidence, error) {
	var evidence participantOfflineAdvancedEvidence
	err := decodeStrictJSON(raw, &evidence)
	return evidence, err
}

func decodeParticipantDataPlaneEvidence(raw string) (participantDataPlaneEvidence, error) {
	var evidence participantDataPlaneEvidence
	err := decodeStrictJSON(raw, &evidence)
	return evidence, err
}

func decodeParticipantRecoveryEvidence(raw string) (validatorDutySnapshots, validatorDutySnapshots, validatorDutySnapshots, error) {
	var evidence participantRecoveryBaselineEvidence
	if err := decodeStrictJSON(raw, &evidence); err != nil || evidence.Version != 1 {
		return validatorDutySnapshots{}, validatorDutySnapshots{}, validatorDutySnapshots{}, errors.New("participant recovery baseline is invalid")
	}
	preFault, err := validatorSnapshotsFromEvidence(evidence.PreFault)
	if err != nil {
		return validatorDutySnapshots{}, validatorDutySnapshots{}, validatorDutySnapshots{}, err
	}
	baseline, err := validatorSnapshotsFromEvidence(evidence.Baseline)
	if err != nil {
		return validatorDutySnapshots{}, validatorDutySnapshots{}, validatorDutySnapshots{}, err
	}
	observed, err := validatorSnapshotsFromEvidence(evidence.Observed)
	return preFault, baseline, observed, err
}
