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
	"strings"
	"time"
)

// validatorHealthError marks a validator endpoint or established validator
// state as unhealthy. Once an endpoint has passed its bounded readiness probe,
// retrying these errors for the whole system-check budget would only obscure a
// dead VC behind a later generic timeout.
type validatorHealthError struct {
	client int
	err    error
}

func (e *validatorHealthError) Error() string {
	return fmt.Sprintf("VC%d is unhealthy: %v", e.client, e.err)
}

func (e *validatorHealthError) Unwrap() error {
	return e.err
}

func (s *systemCheck) waitInitialValidatorActivity(ctx context.Context) (validatorDutySnapshots, error) {
	return s.waitValidatorActivity(ctx, true, s.cfg.requireZeroDutyHistory)
}

func (s *systemCheck) waitValidatorActivity(ctx context.Context, requireProposal, requireCleanDuties bool) (validatorDutySnapshots, error) {
	for i := range s.cfg.vcMetricsURLs {
		if err := s.waitMetricsReachable(ctx, i); err != nil {
			return validatorDutySnapshots{}, err
		}
	}
	return s.waitValidatorSnapshots(ctx, s.cfg.timeout, "both validator clients to report all expected validators active and attesting", true, requireProposal, requireCleanDuties)
}

func (s *systemCheck) waitValidatorSnapshots(ctx context.Context, timeout time.Duration, description string, requireAttestation, requireProposal, requireCleanDuties bool) (validatorDutySnapshots, error) {
	var snapshots validatorDutySnapshots
	err := waitFor(ctx, timeout, s.validatorPollInterval(), description, func(ctx context.Context) (bool, error) {
		for i := range s.cfg.vcMetricsURLs {
			snapshot, err := s.readValidatorDutySnapshot(ctx, i)
			if err != nil {
				return false, err
			}
			if err := validateValidatorSnapshot(snapshot, requireAttestation, requireProposal, requireCleanDuties); err != nil {
				return false, fmt.Errorf("VC%d: %w", i+1, err)
			}
			snapshots[i] = snapshot
		}
		if err := validateDisjointValidatorKeys(snapshots); err != nil {
			return false, err
		}
		return true, nil
	})
	if err == nil {
		s.lastValidatorContinuityCheck = time.Now()
	}
	return snapshots, err
}

func (s *systemCheck) waitValidatorProgress(ctx context.Context, baseline validatorDutySnapshots, description string) (validatorDutySnapshots, error) {
	var snapshots validatorDutySnapshots
	err := waitFor(ctx, validatorDutyObservationTimeout, s.validatorPollInterval(), description, func(ctx context.Context) (bool, error) {
		for i := range s.cfg.vcMetricsURLs {
			snapshot, err := s.readEstablishedValidatorDutySnapshot(ctx, i)
			if err != nil {
				return false, err
			}
			snapshots[i] = snapshot
		}
		if err := validateValidatorProgressSnapshots(baseline, snapshots); err != nil {
			return false, err
		}
		return true, nil
	})
	if err == nil {
		s.lastValidatorContinuityCheck = time.Now()
	}
	return snapshots, err
}

func validateValidatorProgressSnapshots(baseline, snapshots validatorDutySnapshots) error {
	for i, snapshot := range snapshots {
		if err := validateValidatorProgress(baseline[i], snapshot); err != nil {
			return fmt.Errorf("VC%d: %w", i+1, err)
		}
	}
	return nil
}

func (s *systemCheck) waitEveryValidatorAttested(ctx context.Context, baseline validatorDutySnapshots, index int, description string) (validatorDutySnapshots, error) {
	var snapshots validatorDutySnapshots
	err := waitFor(ctx, s.cfg.timeout, s.validatorPollInterval(), description, func(ctx context.Context) (bool, error) {
		for i := range s.cfg.vcMetricsURLs {
			snapshot, err := s.readEstablishedValidatorDutySnapshot(ctx, i)
			if err != nil {
				return false, err
			}
			if err := validateValidatorContinuity(baseline[i], snapshot); err != nil {
				return false, fmt.Errorf("VC%d: %w", i+1, err)
			}
			snapshots[i] = snapshot
		}
		if err := validateEveryValidatorAttested(snapshots, index); err != nil {
			return false, err
		}
		return true, nil
	})
	if err == nil {
		s.lastValidatorContinuityCheck = time.Now()
	}
	return snapshots, err
}

func validateEveryValidatorAttested(snapshots validatorDutySnapshots, index int) error {
	if index < 0 || index >= len(snapshots) {
		return fmt.Errorf("invalid validator client index %d", index)
	}
	if err := validateValidatorSnapshot(snapshots[index], true, false, false); err != nil {
		return fmt.Errorf("VC%d post-restart per-key activity: %w", index+1, err)
	}
	return nil
}

func (s *systemCheck) readValidatorDutySnapshot(ctx context.Context, index int) (validatorDutySnapshot, error) {
	body, err := s.http.getText(ctx, s.cfg.vcMetricsURLs[index])
	if err != nil {
		return validatorDutySnapshot{}, &validatorHealthError{client: index + 1, err: fmt.Errorf("metrics endpoint: %w", err)}
	}
	metrics, err := parseMetrics(body)
	if err != nil {
		return validatorDutySnapshot{}, &validatorHealthError{client: index + 1, err: fmt.Errorf("parse metrics: %w", err)}
	}
	snapshot, err := snapshotValidatorMetrics(metrics)
	if err != nil {
		return validatorDutySnapshot{}, fmt.Errorf("VC%d metrics: %w", index+1, err)
	}
	return snapshot, nil
}

func (s *systemCheck) readEstablishedValidatorDutySnapshot(ctx context.Context, index int) (validatorDutySnapshot, error) {
	snapshot, err := s.readValidatorDutySnapshot(ctx, index)
	if err == nil {
		return snapshot, nil
	}
	var healthErr *validatorHealthError
	if errors.As(err, &healthErr) {
		return validatorDutySnapshot{}, err
	}
	return validatorDutySnapshot{}, &validatorHealthError{client: index + 1, err: err}
}

func (s *systemCheck) checkValidatorContinuity(ctx context.Context, baseline validatorDutySnapshots, indices ...int) error {
	for _, index := range indices {
		snapshot, err := s.readEstablishedValidatorDutySnapshot(ctx, index)
		if err != nil {
			return err
		}
		if err := validateValidatorContinuity(baseline[index], snapshot); err != nil {
			return fmt.Errorf("VC%d: %w", index+1, err)
		}
	}
	return nil
}

func (s *systemCheck) validatorPollInterval() time.Duration {
	if s.cfg.validatorPollInterval > 0 {
		return s.cfg.validatorPollInterval
	}
	return s.cfg.pollInterval
}

func (s *systemCheck) checkValidatorContinuityIfDue(ctx context.Context, baseline validatorDutySnapshots, force bool, indices ...int) error {
	now := time.Now()
	if !force && !s.lastValidatorContinuityCheck.IsZero() && now.Sub(s.lastValidatorContinuityCheck) < s.validatorPollInterval() {
		return nil
	}
	if err := s.checkValidatorContinuity(ctx, baseline, indices...); err != nil {
		return err
	}
	s.lastValidatorContinuityCheck = time.Now()
	return nil
}

func logValidatorDutySnapshots(label string, snapshots validatorDutySnapshots) {
	for i, snapshot := range snapshots {
		log.Printf("systemcheck: %s VC%d: process_start=%.3f validators=%d active=%d individually_attested=%d successful_attestations=%.0f successful_proposals=%.0f failed_attestations=%.0f failed_proposals=%.0f",
			label, i+1, snapshot.processStartTime, snapshot.reportedValidators, snapshot.activeValidators, snapshot.attestedValidators, snapshot.successfulAttestations, snapshot.successfulProposals, snapshot.failedAttestations, snapshot.failedProposals)
	}
}

func beaconSlotAt(now time.Time, genesisTime uint64) (uint64, error) {
	if genesisTime == 0 {
		return 0, fmt.Errorf("beacon genesis time is zero")
	}
	nowUnix := now.Unix()
	if nowUnix < 0 || uint64(nowUnix) < genesisTime {
		return 0, fmt.Errorf("beacon genesis time %d is later than current time %d", genesisTime, nowUnix)
	}
	return (uint64(nowUnix) - genesisTime) / beaconSecondsPerSlot, nil
}

func nextOwnedProposerDuty(duties []proposerDuty, activePubkeys map[string]struct{}, currentSlot uint64) (proposerDuty, error) {
	canonicalKeys := make(map[string]struct{}, len(activePubkeys))
	for pubkey := range activePubkeys {
		canonicalKeys[strings.ToLower(pubkey)] = struct{}{}
	}
	var next proposerDuty
	for _, duty := range duties {
		if duty.slot <= currentSlot {
			continue
		}
		if _, owned := canonicalKeys[strings.ToLower(duty.pubkey)]; !owned {
			continue
		}
		if next.slot == 0 || duty.slot < next.slot {
			next = duty
		}
	}
	if next.slot == 0 {
		return proposerDuty{}, fmt.Errorf("no future proposer duty for VC2 was returned in the current or next epoch")
	}
	return next, nil
}

func validateValidatorProposalLead(currentSlot uint64, duty proposerDuty, minimumLead uint64) error {
	if duty.slot <= currentSlot {
		return fmt.Errorf("next VC2 proposer duty slot %d is not after current slot %d", duty.slot, currentSlot)
	}
	lead := duty.slot - currentSlot
	if lead < minimumLead {
		return fmt.Errorf("next VC2 proposer duty at slot %d is only %d slots after current slot %d", duty.slot, lead, currentSlot)
	}
	return nil
}

func (s *systemCheck) currentTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *systemCheck) waitValidatorProposalWindow(ctx context.Context, validatorBaseline validatorDutySnapshots, minimumLead uint64, validatorIndices ...int) (proposerDuty, error) {
	var authoritativeSlot uint64
	var nextProposal proposerDuty
	description := fmt.Sprintf("a VC2 proposer-duty window of at least %d slots", minimumLead)
	err := waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, description, func(ctx context.Context) (bool, error) {
		if err := s.checkValidatorContinuityIfDue(ctx, validatorBaseline, false, validatorIndices...); err != nil {
			return false, err
		}
		genesisTime, err := s.http.beaconGenesisTime(ctx, s.cfg.clURLs[1])
		if err != nil {
			return false, fmt.Errorf("CL2 genesis: %w", err)
		}
		now := s.currentTime()
		initialWallSlot, err := beaconSlotAt(now, genesisTime)
		if err != nil {
			return false, err
		}
		currentEpoch := initialWallSlot / beaconSlotsPerEpoch
		var currentDuties [2]proposerDutySet
		var nextDuties [2]proposerDutySet
		for i, rawURL := range s.cfg.clURLs {
			currentDuties[i], err = s.http.proposerDuties(ctx, rawURL, currentEpoch)
			if err != nil {
				return false, fmt.Errorf("CL%d current-epoch proposer duties: %w", i+1, err)
			}
			nextDuties[i], err = s.http.proposerDuties(ctx, rawURL, currentEpoch+1)
			if err != nil {
				return false, fmt.Errorf("CL%d next-epoch proposer duties: %w", i+1, err)
			}
		}
		if err := equalProposerDutySets(currentDuties[0], currentDuties[1]); err != nil {
			return false, fmt.Errorf("current-epoch proposer duties differ across beacon nodes: %w", err)
		}
		if err := equalProposerDutySets(nextDuties[0], nextDuties[1]); err != nil {
			return false, fmt.Errorf("next-epoch proposer duties differ across beacon nodes: %w", err)
		}
		var statuses [2]beaconStatus
		for i, rawURL := range s.cfg.clURLs {
			status, err := s.http.beaconStatus(ctx, rawURL)
			if err != nil {
				return false, fmt.Errorf("CL%d after proposer-duty fetch: %w", i+1, err)
			}
			statuses[i] = status
		}
		// Status collection performs six bounded HTTP reads. Resample after all
		// of them so a slow endpoint cannot make the accepted proposer lead stale.
		wallSlotAfterStatuses, err := beaconSlotAt(s.currentTime(), genesisTime)
		if err != nil {
			return false, err
		}
		authoritativeSlot = max(wallSlotAfterStatuses, statuses[0].headSlot, statuses[1].headSlot)
		if statuses[1].headSlot > wallSlotAfterStatuses+1 {
			return false, fmt.Errorf("CL2 head slot %d is ahead of wall-clock slot %d", statuses[1].headSlot, wallSlotAfterStatuses)
		}
		if wallSlotAfterStatuses > statuses[1].headSlot+validatorStartupMaxHeadLagSlots {
			return false, fmt.Errorf("CL2 head slot %d is more than %d slots behind wall-clock slot %d", statuses[1].headSlot, validatorStartupMaxHeadLagSlots, wallSlotAfterStatuses)
		}
		if authoritativeSlot/beaconSlotsPerEpoch != currentEpoch {
			return false, fmt.Errorf("proposer-duty fetch crossed from epoch %d into epoch %d", currentEpoch, authoritativeSlot/beaconSlotsPerEpoch)
		}
		allDuties := make([]proposerDuty, 0, len(currentDuties[0].duties)+len(nextDuties[0].duties))
		allDuties = append(allDuties, currentDuties[0].duties...)
		allDuties = append(allDuties, nextDuties[0].duties...)
		nextProposal, err = nextOwnedProposerDuty(allDuties, validatorBaseline[1].activePubkeys, authoritativeSlot)
		if err != nil {
			return false, err
		}
		if err := validateValidatorProposalLead(authoritativeSlot, nextProposal, minimumLead); err != nil {
			return false, err
		}
		return true, nil
	})
	if err == nil {
		log.Printf("systemcheck: safe VC2 proposer window selected at authoritative slot %d; validator %d has the next owned proposer duty at slot %d (%d-slot lead)", authoritativeSlot, nextProposal.validatorIndex, nextProposal.slot, nextProposal.slot-authoritativeSlot)
	}
	return nextProposal, err
}

type proposerBaselineWindowError struct {
	duty proposerDuty
	err  error
}

func (e *proposerBaselineWindowError) Error() string {
	return fmt.Sprintf("selected VC2 proposer duty slot %d became unsafe while establishing its counter baseline: %v", e.duty.slot, e.err)
}

func (e *proposerBaselineWindowError) Unwrap() error { return e.err }

func (s *systemCheck) waitNextScheduledValidatorProposal(ctx context.Context, validatorBaseline validatorDutySnapshots) error {
	for {
		duty, err := s.waitValidatorProposalWindow(ctx, validatorBaseline, validatorProposalProofLeadSlots, 0, 1)
		if err != nil {
			return fmt.Errorf("select scheduled VC2 proposal after restart baseline: %w", err)
		}
		if err := s.waitScheduledValidatorProposal(ctx, duty, validatorBaseline); err != nil {
			var stale *proposerBaselineWindowError
			if errors.As(err, &stale) {
				log.Printf("systemcheck: %v; selecting a later proposer duty", stale)
				continue
			}
			return err
		}
		return nil
	}
}

func metricValueForPubkey(values map[string]float64, pubkey string) float64 {
	for candidate, value := range values {
		if strings.EqualFold(candidate, pubkey) {
			return value
		}
	}
	return 0
}

func (s *systemCheck) validatorProposalCounts(ctx context.Context, index int, pubkey string) (float64, float64, error) {
	body, err := s.http.getText(ctx, s.cfg.vcMetricsURLs[index])
	if err != nil {
		return 0, 0, err
	}
	metrics, err := parseMetrics(body)
	if err != nil {
		return 0, 0, err
	}
	successful, err := pubkeyMetricValues(metrics, "validator_successful_proposals")
	if err != nil {
		return 0, 0, err
	}
	failed, err := pubkeyMetricValues(metrics, "validator_failed_proposals")
	if err != nil {
		return 0, 0, err
	}
	return metricValueForPubkey(successful, pubkey), metricValueForPubkey(failed, pubkey), nil
}

func (s *systemCheck) waitScheduledValidatorProposal(ctx context.Context, duty proposerDuty, validatorBaseline validatorDutySnapshots) error {
	if duty.slot == 0 || duty.pubkey == "" {
		return fmt.Errorf("selected proposer duty is incomplete: %+v", duty)
	}
	genesisTime, err := s.http.beaconGenesisTime(ctx, s.cfg.clURLs[1])
	if err != nil {
		return fmt.Errorf("read genesis time for scheduled proposal deadline: %w", err)
	}
	now := s.currentTime()
	deadlineUnix := genesisTime + (duty.slot+1+validatorProposalGraceSlots)*beaconSecondsPerSlot
	if now.Unix() < 0 || uint64(now.Unix()) >= deadlineUnix {
		return fmt.Errorf("selected proposer duty slot %d is already beyond its %d-slot proof deadline", duty.slot, validatorProposalGraceSlots)
	}
	baselineSuccess, baselineFailure, err := s.validatorProposalCounts(ctx, 1, duty.pubkey)
	if err != nil {
		return fmt.Errorf("capture selected validator proposal counters: %w", err)
	}
	if baselineFailure != 0 {
		return &validatorDutyFailureError{duty: "proposals in the fresh VC2 process", count: baselineFailure}
	}
	baselineSlot, err := beaconSlotAt(s.currentTime(), genesisTime)
	if err != nil {
		return err
	}
	if err := validateValidatorProposalLead(baselineSlot, duty, validatorProposalBaselineLeadSlots); err != nil {
		return &proposerBaselineWindowError{duty: duty, err: err}
	}
	now = s.currentTime()
	if now.Unix() < 0 || uint64(now.Unix()) >= deadlineUnix {
		return &proposerBaselineWindowError{duty: duty, err: fmt.Errorf("proposal proof deadline passed after counter baseline")}
	}
	dutyTimeout := time.Duration(deadlineUnix-uint64(now.Unix())) * time.Second
	dutyCtx, dutyCancel := context.WithTimeout(ctx, dutyTimeout)
	defer dutyCancel()
	var headerRoot string
	description := fmt.Sprintf("VC2 validator %d to propose scheduled slot %d without a duty failure", duty.validatorIndex, duty.slot)
	err = waitFor(dutyCtx, dutyTimeout, s.cfg.pollInterval, description, func(ctx context.Context) (bool, error) {
		if err := s.checkValidatorContinuityIfDue(ctx, validatorBaseline, false, 0, 1); err != nil {
			return false, err
		}
		successful, failed, err := s.validatorProposalCounts(ctx, 1, duty.pubkey)
		if err != nil {
			return false, err
		}
		if failed > baselineFailure {
			return false, &validatorDutyFailureError{duty: "proposals by the selected VC2 validator", count: failed - baselineFailure}
		}
		if successful <= baselineSuccess {
			return false, fmt.Errorf("selected validator proposal counter remains %.0f", successful)
		}
		var headers [2]canonicalBeaconHeader
		for i, rawURL := range s.cfg.clURLs {
			header, err := s.http.canonicalBeaconHeader(ctx, rawURL, duty.slot)
			if err != nil {
				return false, fmt.Errorf("CL%d scheduled proposal header: %w", i+1, err)
			}
			if header.proposerIndex != duty.validatorIndex {
				return false, fmt.Errorf("CL%d slot %d proposer index is %d, want selected validator %d", i+1, duty.slot, header.proposerIndex, duty.validatorIndex)
			}
			headers[i] = header
		}
		if headers[0].root != headers[1].root {
			return false, fmt.Errorf("scheduled proposal roots differ across beacon nodes: %s != %s", headers[0].root, headers[1].root)
		}
		headerRoot = headers[0].root
		return true, nil
	})
	if err == nil {
		log.Printf("PASS: restarted VC2 validator %d proposed its selected slot %d and both beacon nodes agreed on canonical root %s with zero proposal failures", duty.validatorIndex, duty.slot, headerRoot)
	} else if errors.Is(err, context.DeadlineExceeded) || errors.Is(dutyCtx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("selected VC2 proposer duty slot %d was not proven within %d grace slots: %w", duty.slot, validatorProposalGraceSlots, err)
	}
	return err
}
