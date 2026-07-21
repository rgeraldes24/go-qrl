// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package system

import (
	"context"
	"errors"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/qrlclient"
)

func (s *systemCheck) loadOrRecordParticipantFaultBaseline(ctx context.Context, minimumFinality uint64, validators validatorDutySnapshots) (uint64, uint64, executionFinalityStatus, validatorDutySnapshots, error) {
	finalizedEpoch, headSlot, execution, recordedValidators, recorded, err := s.recordedParticipantFaultBaseline()
	if err != nil {
		return 0, 0, executionFinalityStatus{}, validatorDutySnapshots{}, err
	}
	if recorded {
		return finalizedEpoch, headSlot, execution, recordedValidators, nil
	}
	beforeRestart, err := s.waitBeaconConvergence(ctx, minimumFinality, 0, validators)
	if err != nil {
		return 0, 0, executionFinalityStatus{}, validatorDutySnapshots{}, fmt.Errorf("capture consensus checkpoint before participant restart: %w", err)
	}
	finalizedEpoch = beforeRestart[0].finalizedEpoch
	headSlot = beforeRestart[0].headSlot
	execution, err = s.waitExecutionFinality(ctx, validators, 1, nil)
	if err != nil {
		return 0, 0, executionFinalityStatus{}, validatorDutySnapshots{}, fmt.Errorf("capture execution finality before participant restart: %w", err)
	}
	if err := s.checkValidatorContinuityIfDue(ctx, validators, true, 0, 1); err != nil {
		return 0, 0, executionFinalityStatus{}, validatorDutySnapshots{}, fmt.Errorf("capture validator continuity before participant restart: %w", err)
	}
	evidence := participantFaultBaselineToEvidence(finalizedEpoch, headSlot, execution, validators)
	if err := s.recordTypedSystemObservation(ctx, participantFaultBaselineObservation, evidence); err != nil {
		return 0, 0, executionFinalityStatus{}, validatorDutySnapshots{}, err
	}
	return finalizedEpoch, headSlot, execution, validators, nil
}

func (s *systemCheck) ensureParticipantOfflineWindowAdvanced(ctx context.Context, hash common.Hash, receipt *types.Receipt, validators validatorDutySnapshots) (uint64, error) {
	if receipt == nil || receipt.BlockNumber == nil || receipt.BlockNumber.Sign() <= 0 {
		return 0, fmt.Errorf("offline-window transaction %s has no valid inclusion block", hash)
	}
	inclusion := receipt.BlockNumber.Uint64()
	if inclusion > ^uint64(0)-s.cfg.catchupBlocks {
		return 0, errors.New("offline-window catch-up target overflows uint64")
	}
	target := inclusion + s.cfg.catchupBlocks
	if raw, ok := s.resume.observations[participantOfflineAdvancedObservation]; ok {
		evidence, err := decodeParticipantOfflineEvidence(raw)
		if err != nil || evidence.Version != 1 || evidence.TransactionHash != hash.Hex() || evidence.InclusionBlock != inclusion || evidence.TargetBlock != target {
			return 0, fmt.Errorf("offline-window advancement evidence does not match transaction %s at block %d", hash, inclusion)
		}
		return target, nil
	}
	if err := waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, fmt.Sprintf("EL1 to advance to block %d while participant two is offline", target), func(ctx context.Context) (bool, error) {
		if err := s.checkValidatorContinuityIfDue(ctx, validators, false, 0); err != nil {
			return false, err
		}
		head, err := s.clients[0].BlockNumber(ctx)
		return head >= target, err
	}); err != nil {
		return 0, err
	}
	evidence := participantOfflineAdvancedEvidence{Version: 1, TransactionHash: hash.Hex(), InclusionBlock: inclusion, TargetBlock: target}
	if err := s.recordTypedSystemObservation(context.WithoutCancel(ctx), participantOfflineAdvancedObservation, evidence); err != nil {
		return 0, err
	}
	return target, nil
}

func (s *systemCheck) ensureParticipantDataPlaneRecovered(ctx context.Context, previousHeadSlot, target uint64, hash common.Hash, validators validatorDutySnapshots) (uint64, error) {
	if raw, ok := s.resume.observations[participantDataPlaneObservation]; ok {
		evidence, err := decodeParticipantDataPlaneEvidence(raw)
		if err != nil || evidence.Version != 1 || evidence.TransactionHash != hash.Hex() || evidence.MinimumBlock != target || evidence.ConvergedBlock <= target {
			return 0, fmt.Errorf("participant data-plane evidence does not match transaction %s and target %d", hash, target)
		}
		return evidence.ConvergedBlock, nil
	}
	convergedBlock, err := s.waitSecondParticipantDataPlaneRecovery(ctx, previousHeadSlot, target, hash, validators)
	if err != nil {
		return 0, err
	}
	if _, err := s.verifyTransferOnBoth(ctx, hash); err != nil {
		return 0, fmt.Errorf("verify offline-window transaction after data-plane recovery: %w", err)
	}
	if _, err := s.waitValidatorProposalWindow(ctx, validators, validatorStartupLeadSlots, 0); err != nil {
		return 0, err
	}
	evidence := participantDataPlaneEvidence{Version: 1, TransactionHash: hash.Hex(), MinimumBlock: target, ConvergedBlock: convergedBlock}
	if err := s.recordTypedSystemObservation(context.WithoutCancel(ctx), participantDataPlaneObservation, evidence); err != nil {
		return 0, err
	}
	return convergedBlock, nil
}

func (s *systemCheck) loadOrRecordParticipantRecoveryBaseline(ctx context.Context, preFault validatorDutySnapshots) (validatorDutySnapshots, validatorDutySnapshots, error) {
	if raw, ok := s.resume.observations[participantRecoveryBaselineObservation]; ok {
		recordedPreFault, baseline, observed, err := decodeParticipantRecoveryEvidence(raw)
		if err != nil {
			return validatorDutySnapshots{}, validatorDutySnapshots{}, err
		}
		if !reflect.DeepEqual(recordedPreFault, preFault) {
			return validatorDutySnapshots{}, validatorDutySnapshots{}, errors.New("participant recovery baseline refers to a different pre-fault validator baseline")
		}
		return baseline, observed, nil
	}
	baseline, observed, err := s.waitRestartedValidatorBaseline(ctx, preFault)
	if err != nil {
		return validatorDutySnapshots{}, validatorDutySnapshots{}, fmt.Errorf("capture validator recovery baseline: %w", err)
	}
	evidence := participantRecoveryBaselineEvidence{
		Version: 1, PreFault: validatorSnapshotsToEvidence(preFault),
		Baseline: validatorSnapshotsToEvidence(baseline), Observed: validatorSnapshotsToEvidence(observed),
	}
	if err := s.recordTypedSystemObservation(context.WithoutCancel(ctx), participantRecoveryBaselineObservation, evidence); err != nil {
		return validatorDutySnapshots{}, validatorDutySnapshots{}, err
	}
	return baseline, observed, nil
}

func (s *systemCheck) completeParticipantRecoveryAssertions(ctx context.Context, previousFinality, previousHeadSlot uint64, previousExecutionFinality executionFinalityStatus, recoveryBaseline, observedRecovery validatorDutySnapshots) error {
	if !s.hasAssertionMilestone(participantProposalObservation) {
		if err := s.waitNextScheduledValidatorProposal(ctx, recoveryBaseline); err != nil {
			return fmt.Errorf("scheduled VC2 proposal after restart: %w", err)
		}
		if err := s.recordAssertionMilestone(context.WithoutCancel(ctx), participantProposalObservation); err != nil {
			return err
		}
	}
	if !s.hasAssertionMilestone(participantAggregateObservation) {
		aggregate, err := s.waitValidatorProgress(ctx, observedRecovery, "early aggregate validator activity without new duty failures after participant restart")
		if err != nil {
			return fmt.Errorf("early validator activity after restart: %w", err)
		}
		logValidatorDutySnapshots("early post-restart aggregate validator activity", aggregate)
		if err := s.recordAssertionMilestone(context.WithoutCancel(ctx), participantAggregateObservation); err != nil {
			return err
		}
	}
	if !s.hasAssertionMilestone(participantConsensusObservation) {
		minimumFinality := previousFinality
		if s.cfg.requireFinalityAdvance {
			minimumFinality++
		}
		if _, err := s.waitBeaconConvergence(ctx, minimumFinality, previousHeadSlot, recoveryBaseline); err != nil {
			return fmt.Errorf("beacon convergence after restart: %w", err)
		}
		if err := s.recordAssertionMilestone(context.WithoutCancel(ctx), participantConsensusObservation); err != nil {
			return err
		}
	}
	if !s.hasAssertionMilestone(participantExecutionObservation) {
		minimumExecutionFinality := uint64(1)
		var previous *executionFinalityStatus
		if s.cfg.requireFinalityAdvance {
			minimumExecutionFinality = previousExecutionFinality.finalizedNumber + 1
			previous = &previousExecutionFinality
		}
		if _, err := s.waitExecutionFinality(ctx, recoveryBaseline, minimumExecutionFinality, previous); err != nil {
			return fmt.Errorf("execution finality after restart: %w", err)
		}
		if err := s.recordAssertionMilestone(context.WithoutCancel(ctx), participantExecutionObservation); err != nil {
			return err
		}
	}
	if !s.hasAssertionMilestone(participantEveryValidatorObservation) {
		recovered, err := s.waitEveryValidatorAttested(ctx, observedRecovery, 1, "all restarted VC2 validators to attest without a reset or new duty failure")
		if err != nil {
			return fmt.Errorf("validator activity after restart: %w", err)
		}
		logValidatorDutySnapshots("post-restart validator-duty snapshot", recovered)
		if err := s.recordAssertionMilestone(context.WithoutCancel(ctx), participantEveryValidatorObservation); err != nil {
			return err
		}
	}
	return nil
}

func (s *systemCheck) restartSecondParticipant(ctx context.Context, previousFinality uint64, preFaultValidatorDuties validatorDutySnapshots) (err error) {
	previousFinality, previousHeadSlot, preFaultExecutionFinality, preFaultValidatorDuties, err := s.loadOrRecordParticipantFaultBaseline(ctx, previousFinality, preFaultValidatorDuties)
	if err != nil {
		return err
	}

	stopOrder := []string{s.cfg.vcServices[1], s.cfg.clServices[1], s.cfg.elServices[1]}
	startOrder := []string{s.cfg.elServices[1], s.cfg.clServices[1], s.cfg.vcServices[1]}
	stopped := make(map[string]bool)
	defer func() {
		for _, service := range startOrder {
			if stopped[service] {
				err = errors.Join(err, s.recoverService(service))
			}
		}
	}()
	for _, service := range stopOrder {
		log.Printf("systemcheck: stopping %s", service)
		if err := s.recordRestart(ctx, service, RestartStopIntent); err != nil {
			return err
		}
		stopped[service] = true
		if err := s.k.Stop(ctx, service); err != nil {
			return err
		}
		if err := s.recordRestart(ctx, service, RestartStopped); err != nil {
			return err
		}
	}

	if err := s.waitSecondParticipantEndpointsDown(ctx); err != nil {
		return err
	}

	hash, err := s.sendManagedTransfer(ctx, 0, TransactionLabelParticipantOffline)
	if err != nil {
		return fmt.Errorf("submit transaction while participant two is offline: %w", err)
	}
	receipt, err := s.waitReceipt(ctx, 0, hash)
	if err != nil {
		return err
	}
	target, err := s.ensureParticipantOfflineWindowAdvanced(ctx, hash, receipt, preFaultValidatorDuties)
	if err != nil {
		return err
	}

	var convergedBlock uint64
	for _, service := range startOrder {
		log.Printf("systemcheck: starting %s", service)
		if err := s.recordRestart(ctx, service, RestartStartIntent); err != nil {
			return err
		}
		if err := s.k.Start(ctx, service); err != nil {
			return err
		}
		if err := s.recordRestart(ctx, service, RestartStarted); err != nil {
			return err
		}
		switch service {
		case s.cfg.elServices[1]:
			if err := s.redialSecondExecution(ctx); err != nil {
				return err
			}
		case s.cfg.clServices[1]:
			if err := s.waitBeaconReachable(ctx, 1); err != nil {
				return err
			}
			convergedBlock, err = s.ensureParticipantDataPlaneRecovered(ctx, previousHeadSlot, target, hash, preFaultValidatorDuties)
			if err != nil {
				return err
			}
		case s.cfg.vcServices[1]:
			if err := s.waitMetricsReachable(ctx, 1); err != nil {
				return err
			}
		}
		if err := s.recordRestart(ctx, service, RestartHealthy); err != nil {
			return err
		}
	}
	recoveryValidatorBaseline, observedRecoveryValidators, err := s.loadOrRecordParticipantRecoveryBaseline(ctx, preFaultValidatorDuties)
	if err != nil {
		return err
	}
	logValidatorDutySnapshots("post-restart validator recovery baseline", observedRecoveryValidators)
	if !s.cfg.requireFinalityAdvance {
		log.Printf("systemcheck: NON-STRICT finality mode: recovery must converge, but a new finalized epoch is not required")
	}
	if err := s.completeParticipantRecoveryAssertions(ctx, previousFinality, previousHeadSlot, preFaultExecutionFinality, recoveryValidatorBaseline, observedRecoveryValidators); err != nil {
		return err
	}
	clear(stopped)
	log.Printf("PASS: participant two recovered from the durable pre-fault baseline, caught up through execution block %d, preserved offline-window transaction agreement, and completed all consensus/execution/validator recovery assertions", convergedBlock)
	return nil
}

// resumeSecondParticipant finishes the exact transition prefix preserved in
// the checkpoint. It never repeats a completed Stop/Start and it reuses the
// offline-window transaction hash. Once every service is healthy it rebuilds
// fresh consensus/duty baselines and repeats the post-recovery assertions.
func (s *systemCheck) resumeSecondParticipant(ctx context.Context) (err error) {
	stopOrder := []string{s.cfg.vcServices[1], s.cfg.clServices[1], s.cfg.elServices[1]}
	startOrder := []string{s.cfg.elServices[1], s.cfg.clServices[1], s.cfg.vcServices[1]}
	states := make(map[string]RestartState, len(stopOrder))
	stopped := make(map[string]bool, len(stopOrder))
	for _, service := range stopOrder {
		if len(s.resume.restarts[service]) != 0 {
			stopped[service] = true
		}
	}
	defer func() {
		for _, service := range startOrder {
			if stopped[service] {
				err = errors.Join(err, s.recoverService(service))
			}
		}
	}()

	previousFinality, previousHeadSlot, preFaultExecutionFinality, preFaultValidatorDuties, recordedBaseline, err := s.recordedParticipantFaultBaseline()
	if err != nil {
		return err
	}
	if !recordedBaseline {
		return errors.New("cannot resume participant fault cycle without its durable pre-fault finality/execution/validator baseline")
	}
	for _, service := range stopOrder {
		generation := currentRestartGeneration(s.resume.restarts[service])
		state, exists := lastRestartState(generation)
		if !exists {
			if s.resume.observations[participantOfflineAdvancedObservation] != "" {
				return fmt.Errorf("participant fault cycle advanced its offline window before %s acquired restart history", service)
			}
			log.Printf("systemcheck: resuming participant fault by stopping %s", service)
			if err := s.recordRestart(ctx, service, RestartStopIntent); err != nil {
				return err
			}
			stopped[service] = true
			if err := s.k.Stop(ctx, service); err != nil {
				return err
			}
			if err := s.recordRestart(ctx, service, RestartStopped); err != nil {
				return err
			}
			state = RestartStopped
		}
		if state == RestartStopIntent {
			stopped[service] = true
			if err := s.resolveStopIntent(ctx, service); err != nil {
				return err
			}
			state = RestartStopped
		}
		if state == RestartEmergencyStartIntent {
			stopped[service] = true
			if err := s.resolveEmergencyStartIntent(ctx, service); err != nil {
				return err
			}
			state = RestartEmergencyStarted
		}
		if containsRestartState(generation, RestartEmergencyStarted) || state == RestartEmergencyStarted {
			if s.resume.observations[participantOfflineAdvancedObservation] == "" {
				log.Printf("systemcheck: %s safety recovery preceded the durable offline-window assertion; beginning a new fault-cycle generation", service)
				stopped[service], err = s.reenterFaultAfterEmergency(ctx, service)
				if err != nil {
					return err
				}
				state = RestartStopped
			}
		}
		states[service] = state
		if state == RestartStopped {
			stopped[service] = true
		}
	}

	allStopped := true
	for _, service := range stopOrder {
		allStopped = allStopped && states[service] == RestartStopped
	}
	if allStopped {
		if err := s.waitSecondParticipantEndpointsDown(ctx); err != nil {
			return err
		}
	}

	hash, recorded := s.recordedTransaction(TransactionLabelParticipantOffline)
	if !recorded {
		if !allStopped {
			return fmt.Errorf("participant recovery began without a durable offline-window transaction")
		}
		hash, err = s.sendManagedTransfer(ctx, 0, TransactionLabelParticipantOffline)
		if err != nil {
			return fmt.Errorf("submit transaction while resumed participant two is offline: %w", err)
		}
	}
	receipt, err := s.waitReceipt(ctx, 0, hash)
	if err != nil {
		return err
	}
	if receipt.BlockNumber == nil {
		return fmt.Errorf("offline-window transaction %s has no inclusion block", hash)
	}
	if !allStopped && s.resume.observations[participantOfflineAdvancedObservation] == "" {
		return errors.New("participant services recovered before offline-window advancement was durably asserted")
	}
	target, err := s.ensureParticipantOfflineWindowAdvanced(ctx, hash, receipt, preFaultValidatorDuties)
	if err != nil {
		return err
	}

	var convergedBlock uint64
	for _, service := range startOrder {
		state := states[service]
		if state == RestartEmergencyStarted {
			state = RestartStarted
		}
		if state == RestartStartIntent {
			stopped[service] = true
			if err := s.resolveStartIntent(ctx, service); err != nil {
				return err
			}
			state = RestartStarted
		}
		if state == RestartStopped {
			log.Printf("systemcheck: resuming participant recovery by starting %s", service)
			if err := s.recordRestart(ctx, service, RestartStartIntent); err != nil {
				return err
			}
			if err := s.k.Start(ctx, service); err != nil {
				return err
			}
			if err := s.recordRestart(ctx, service, RestartStarted); err != nil {
				return err
			}
			state = RestartStarted
		}
		if state == RestartStarted || state == RestartHealthy {
			recovered, err := s.ensureServiceRunning(ctx, service)
			if err != nil {
				return err
			}
			if recovered {
				state = RestartStarted
			}
			switch service {
			case s.cfg.elServices[1]:
				if err := s.redialSecondExecution(ctx); err != nil {
					return err
				}
			case s.cfg.clServices[1]:
				if err := s.waitBeaconReachable(ctx, 1); err != nil {
					return err
				}
				convergedBlock, err = s.ensureParticipantDataPlaneRecovered(ctx, previousHeadSlot, target, hash, preFaultValidatorDuties)
				if err != nil {
					return err
				}
			case s.cfg.vcServices[1]:
				if s.resume.observations[participantDataPlaneObservation] == "" {
					return errors.New("refusing VC2 recovery before participant data-plane recovery is durable")
				}
				if err := s.waitMetricsReachable(ctx, 1); err != nil {
					return err
				}
			}
			if state == RestartStarted {
				if err := s.recordRestart(ctx, service, RestartHealthy); err != nil {
					return err
				}
				state = RestartHealthy
			}
		}
		if state != RestartHealthy {
			return fmt.Errorf("participant resume reached unsupported state %q for %s", state, service)
		}
	}

	if _, err := s.verifyTransferOnBoth(ctx, hash); err != nil {
		return fmt.Errorf("verify resumed offline-window transaction after recovery: %w", err)
	}
	recoveryValidatorBaseline, observedRecoveryValidators, err := s.loadOrRecordParticipantRecoveryBaseline(ctx, preFaultValidatorDuties)
	if err != nil {
		return err
	}
	if err := s.completeParticipantRecoveryAssertions(ctx, previousFinality, previousHeadSlot, preFaultExecutionFinality, recoveryValidatorBaseline, observedRecoveryValidators); err != nil {
		return err
	}
	clear(stopped)
	log.Printf("PASS: resumed participant-two recovery at durable block %d without repeating completed service transitions, transaction submission, baselines, or assertion milestones (%s)", convergedBlock, hash)
	return nil
}

func (s *systemCheck) waitSecondParticipantEndpointsDown(ctx context.Context) error {
	probes := []struct {
		name string
		url  string
	}{
		{name: "EL2 RPC", url: s.cfg.rpcURLs[1]},
		{name: "CL2 HTTP", url: strings.TrimRight(s.cfg.clURLs[1], "/") + syncStatusPath},
		{name: "VC2 metrics", url: s.cfg.vcMetricsURLs[1]},
	}
	for _, probe := range probes {
		if err := waitFor(ctx, 30*time.Second, s.cfg.pollInterval, probe.name+" to become unavailable", func(ctx context.Context) (bool, error) {
			probeCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			defer cancel()
			if err := s.http.responds(probeCtx, probe.url); err != nil {
				return true, nil
			}
			return false, fmt.Errorf("%s remained available after its service was stopped", probe.name)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *systemCheck) redialSecondExecution(ctx context.Context) error {
	return waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, "EL2 RPC after restart", func(ctx context.Context) (bool, error) {
		endpoint, err := s.cfg.executionEndpoint(ctx, s.k, 1)
		if err != nil {
			return false, err
		}
		client, err := qrlclient.DialContext(ctx, endpoint)
		if err != nil {
			return false, err
		}
		chainID, err := client.ChainID(ctx)
		if err != nil {
			client.Close()
			return false, err
		}
		if chainID.Sign() <= 0 {
			client.Close()
			return false, fmt.Errorf("EL2 chain ID is not positive: %s", chainID)
		}
		block, err := client.BlockNumber(ctx)
		if err != nil {
			client.Close()
			return false, err
		}
		if block == 0 {
			client.Close()
			return false, fmt.Errorf("EL2 has not imported a post-genesis block")
		}
		if endpoint != s.cfg.rpcURLs[1] {
			log.Printf("systemcheck: refreshed %s RPC endpoint after restart: %s -> %s", s.cfg.elServices[1], s.cfg.rpcURLs[1], endpoint)
			if err := s.recordEndpoint(ctx, s.cfg.elServices[1], "execution-rpc", s.cfg.rpcURLs[1], endpoint); err != nil {
				client.Close()
				return false, err
			}
		}
		previous := s.clients[1]
		s.cfg.rpcURLs[1] = endpoint
		s.clients[1] = client
		if previous != nil {
			previous.Close()
		}
		return true, nil
	})
}

func (s *systemCheck) waitBeaconReachable(ctx context.Context, index int) error {
	return waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, fmt.Sprintf("CL%d HTTP after restart", index+1), func(ctx context.Context) (bool, error) {
		endpoint, err := s.cfg.beaconEndpoint(ctx, s.k, index)
		if err != nil {
			return false, err
		}
		status, err := s.http.beaconStatus(ctx, endpoint)
		if err != nil {
			return false, err
		}
		if status.headSlot == 0 {
			return false, fmt.Errorf("CL%d has not imported a post-genesis slot", index+1)
		}
		if endpoint != s.cfg.clURLs[index] {
			log.Printf("systemcheck: refreshed %s HTTP endpoint after restart: %s -> %s", s.cfg.clServices[index], s.cfg.clURLs[index], endpoint)
			if err := s.recordEndpoint(ctx, s.cfg.clServices[index], "consensus-http", s.cfg.clURLs[index], endpoint); err != nil {
				return false, err
			}
			s.cfg.clURLs[index] = endpoint
		}
		return true, nil
	})
}

func (s *systemCheck) waitMetricsReachable(ctx context.Context, index int) error {
	interval := min(s.validatorPollInterval(), 5*time.Second)
	return waitFor(ctx, validatorMetricsReachabilityTimeout, interval, fmt.Sprintf("VC%d metrics endpoint to become reachable", index+1), func(ctx context.Context) (bool, error) {
		endpoint, err := s.cfg.validatorEndpoint(ctx, s.k, index)
		if err != nil {
			return false, err
		}
		body, err := s.http.getText(ctx, endpoint)
		if err != nil {
			return false, err
		}
		metrics, err := parseMetrics(body)
		if err != nil {
			return false, err
		}
		processStart, err := checkedSingleMetric(metrics, "process_start_time_seconds")
		if err != nil {
			return false, err
		}
		if processStart <= 0 {
			return false, fmt.Errorf("VC%d process start time has invalid value %v", index+1, processStart)
		}
		if endpoint != s.cfg.vcMetricsURLs[index] {
			log.Printf("systemcheck: refreshed %s metrics endpoint after restart: %s -> %s", s.cfg.vcServices[index], s.cfg.vcMetricsURLs[index], endpoint)
			if err := s.recordEndpoint(ctx, s.cfg.vcServices[index], "validator-metrics", s.cfg.vcMetricsURLs[index], endpoint); err != nil {
				return false, err
			}
			s.cfg.vcMetricsURLs[index] = endpoint
		}
		return true, nil
	})
}

func (s *systemCheck) waitRestartedValidatorBaseline(ctx context.Context, preFault validatorDutySnapshots) (validatorDutySnapshots, validatorDutySnapshots, error) {
	var baseline validatorDutySnapshots
	var observed validatorDutySnapshots
	err := waitFor(ctx, validatorDutyObservationTimeout, s.validatorPollInterval(), "validator clients to establish clean recovery baselines", func(ctx context.Context) (bool, error) {
		vc1, err := s.readEstablishedValidatorDutySnapshot(ctx, 0)
		if err != nil {
			return false, err
		}
		vc2, err := s.readValidatorDutySnapshot(ctx, 1)
		if err != nil {
			return false, err
		}
		observed = validatorDutySnapshots{vc1, vc2}
		baseline, err = restartedValidatorBaseline(preFault, observed)
		if err != nil {
			return false, err
		}
		return true, nil
	})
	if err == nil {
		s.lastValidatorContinuityCheck = time.Now()
	}
	return baseline, observed, err
}

func restartedValidatorBaseline(preFault, observed validatorDutySnapshots) (validatorDutySnapshots, error) {
	if err := validateValidatorContinuity(preFault[0], observed[0]); err != nil {
		return validatorDutySnapshots{}, fmt.Errorf("VC1 across participant-two outage: %w", err)
	}
	if err := validateValidatorSnapshot(observed[1], false, false, true); err != nil {
		return validatorDutySnapshots{}, fmt.Errorf("VC2 fresh process: %w", err)
	}
	if observed[1].processStartTime <= preFault[1].processStartTime+validatorProcessStartTimeToleranceSeconds {
		return validatorDutySnapshots{}, fmt.Errorf("VC2 process start time %.3f has not advanced by more than the %.3fs collection tolerance beyond pre-restart %.3f", observed[1].processStartTime, validatorProcessStartTimeToleranceSeconds, preFault[1].processStartTime)
	}
	if err := validateDisjointValidatorKeys(observed); err != nil {
		return validatorDutySnapshots{}, err
	}
	if !equalPubkeySets(preFault[1].activePubkeys, observed[1].activePubkeys) {
		return validatorDutySnapshots{}, &validatorTopologyError{err: fmt.Errorf("VC2 active validator key set changed across restart")}
	}
	// VC1's baseline intentionally remains the pre-fault sample so its
	// uninterrupted process and counters are checked across the entire VC2
	// outage. VC2 starts a fresh baseline because its reset was intentional.
	return validatorDutySnapshots{preFault[0], observed[1]}, nil
}

func validatePreValidatorBeaconRecovery(statuses [2]beaconStatus, previousHeadSlot uint64) error {
	for i, status := range statuses {
		if status.headSlot <= previousHeadSlot {
			return fmt.Errorf("CL%d head slot %d has not advanced beyond pre-restart slot %d", i+1, status.headSlot, previousHeadSlot)
		}
	}
	if statuses[1].headSlot < statuses[0].headSlot {
		return fmt.Errorf("CL2 head slot %d is behind CL1 head slot %d", statuses[1].headSlot, statuses[0].headSlot)
	}
	if statuses[0].finalizedEpoch != statuses[1].finalizedEpoch || statuses[0].finalizedRoot != statuses[1].finalizedRoot {
		return fmt.Errorf("beacon finalized checkpoints differ: %d/%s != %d/%s", statuses[0].finalizedEpoch, statuses[0].finalizedRoot, statuses[1].finalizedEpoch, statuses[1].finalizedRoot)
	}
	return nil
}

func (s *systemCheck) waitSecondParticipantDataPlaneRecovery(ctx context.Context, previousHeadSlot, minimumBlock uint64, hash common.Hash, validatorBaseline validatorDutySnapshots) (uint64, error) {
	var firstConvergedBlock uint64
	var firstConvergedSlot uint64
	var observedConvergence bool
	var convergedBlock uint64
	description := fmt.Sprintf("participant-two EL/CL data plane to converge and advance beyond block %d before VC2 starts", minimumBlock)
	err := waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, description, func(ctx context.Context) (bool, error) {
		// VC2 is intentionally stopped at this boundary, so only the uninterrupted
		// validator may be monitored until it is safe to start the second one.
		if err := s.checkValidatorContinuityIfDue(ctx, validatorBaseline, false, 0); err != nil {
			return false, err
		}
		var statuses [2]beaconStatus
		for i, rawURL := range s.cfg.clURLs {
			status, err := s.http.beaconStatus(ctx, rawURL)
			if err != nil {
				return false, fmt.Errorf("CL%d: %w", i+1, err)
			}
			statuses[i] = status
		}
		if err := validatePreValidatorBeaconRecovery(statuses, previousHeadSlot); err != nil {
			return false, err
		}
		block, err := s.crossNodeCatchupSample(ctx, minimumBlock, hash)
		if err != nil {
			return false, err
		}
		commonSlot := min(statuses[0].headSlot, statuses[1].headSlot)
		if !observedConvergence {
			observedConvergence = true
			firstConvergedBlock = block
			firstConvergedSlot = commonSlot
			return false, fmt.Errorf("initial data-plane convergence observed at execution block %d / beacon slot %d; waiting for a further common advance", block, commonSlot)
		}
		if block <= firstConvergedBlock {
			return false, fmt.Errorf("common execution head remains at initial recovered block %d", firstConvergedBlock)
		}
		if commonSlot <= firstConvergedSlot {
			return false, fmt.Errorf("common beacon head remains at initial recovered slot %d", firstConvergedSlot)
		}
		convergedBlock = block
		return true, nil
	})
	if err == nil {
		log.Printf("PASS: participant-two EL/CL data plane recovered and advanced from common execution block %d / beacon slot %d to block %d before VC2 startup", firstConvergedBlock, firstConvergedSlot, convergedBlock)
	}
	return convergedBlock, err
}

func (s *systemCheck) crossNodeCatchupSample(ctx context.Context, minimumBlock uint64, hash common.Hash) (uint64, error) {
	headerA, err := s.clients[0].HeaderByNumber(ctx, nil)
	if err != nil {
		return 0, err
	}
	headerB, err := s.clients[1].HeaderByNumber(ctx, nil)
	if err != nil {
		return 0, err
	}
	if headerA.Number == nil || headerB.Number == nil {
		return 0, fmt.Errorf("execution latest header has no block number")
	}
	if headerA.Number.Uint64() < minimumBlock {
		return 0, fmt.Errorf("EL1 latest block %d is below offline-window target %d", headerA.Number.Uint64(), minimumBlock)
	}
	if err := compareExecutionHeaders(headerA, headerB); err != nil {
		return 0, err
	}
	if _, err := s.clients[1].TransactionReceipt(ctx, hash); err != nil {
		return 0, err
	}
	progress, err := s.clients[1].SyncProgress(ctx)
	if err != nil {
		return 0, err
	}
	if progress != nil {
		return 0, fmt.Errorf("EL2 is still syncing at %+v", progress)
	}
	return headerA.Number.Uint64(), nil
}

func (s *systemCheck) recoverService(service string) error {
	// Recovery must outlive cancellation of the whole-run context so a timeout
	// cannot leave a service stopped.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := s.ensureServiceRunning(ctx, service)
	return err
}

// ensureServiceRunning reconciles the live service state with the append-only
// restart journal. It is safe after a lost Stop/Start response, an unresolved
// emergency intent, or an unexpected crash after Started/Healthy. The bool is
// true when this call had to start a stopped service.
func (s *systemCheck) ensureServiceRunning(ctx context.Context, service string) (bool, error) {
	status, err := s.k.Status(ctx, service)
	if err != nil {
		return false, fmt.Errorf("inspect service %s for recovery: %w", service, err)
	}
	generation := currentRestartGeneration(s.resume.restarts[service])
	state, exists := lastRestartState(generation)
	if !exists {
		return false, fmt.Errorf("recover service %s without durable restart history", service)
	}

	if status == ServiceRunning {
		switch state {
		case RestartEmergencyStartIntent:
			// A prior Start response was lost. Only append the missing post-state;
			// never append a second consecutive emergency intent.
			if err := s.recordRestart(ctx, service, RestartEmergencyStarted); err != nil {
				return false, fmt.Errorf("record recovered service %s: %w", service, err)
			}
		case RestartStopIntent, RestartStopped, RestartStartIntent:
			// A lost Stop response can leave the service running. Close that
			// ambiguous generation with explicit safety-recovery evidence.
			if err := s.recordRestart(ctx, service, RestartEmergencyStartIntent); err != nil {
				return false, fmt.Errorf("record recovery intent for running service %s: %w", service, err)
			}
			if err := s.recordRestart(ctx, service, RestartEmergencyStarted); err != nil {
				return false, fmt.Errorf("record recovered running service %s: %w", service, err)
			}
		case RestartStarted, RestartHealthy, RestartEmergencyStarted:
			// The journal and live status already agree.
		default:
			return false, fmt.Errorf("recover running service %s from unsupported durable state %q", service, state)
		}
		return false, nil
	}
	if status != ServiceStopped {
		return false, fmt.Errorf("recover service %s: unsupported status %q", service, status)
	}

	// An emergency-recovered generation is already closed. If that service
	// subsequently stopped, open a new generation and record the observed
	// stopped state before starting it again.
	if containsRestartState(generation, RestartEmergencyStarted) && (state == RestartEmergencyStarted || state == RestartHealthy) {
		if err := s.recordRestart(ctx, service, RestartStopIntent); err != nil {
			return true, fmt.Errorf("record new recovery generation for %s: %w", service, err)
		}
		if err := s.recordRestart(ctx, service, RestartStopped); err != nil {
			return true, fmt.Errorf("record observed stopped service %s: %w", service, err)
		}
		state = RestartStopped
	}
	if state != RestartEmergencyStartIntent {
		if err := s.recordRestart(ctx, service, RestartEmergencyStartIntent); err != nil {
			return true, fmt.Errorf("record recovery intent for stopped service %s: %w", service, err)
		}
	}
	if err := s.k.Start(ctx, service); err != nil {
		return true, fmt.Errorf("recover stopped service %s: %w", service, err)
	}
	if err := s.recordRestart(ctx, service, RestartEmergencyStarted); err != nil {
		return true, fmt.Errorf("record recovered service %s: %w", service, err)
	}
	return true, nil
}
