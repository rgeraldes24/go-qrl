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
	"fmt"
	"math/big"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/rpc"
)

func (s *systemCheck) waitBeaconConvergence(ctx context.Context, minimumEpoch, minimumHeadSlot uint64, validatorBaseline validatorDutySnapshots) ([2]beaconStatus, error) {
	var statuses [2]beaconStatus
	err := waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, "beacon finality convergence", func(ctx context.Context) (bool, error) {
		if err := s.checkValidatorContinuityIfDue(ctx, validatorBaseline, false, 0, 1); err != nil {
			return false, err
		}
		for i, rawURL := range s.cfg.clURLs {
			status, err := s.http.beaconStatus(ctx, rawURL)
			if err != nil {
				return false, fmt.Errorf("CL%d: %w", i+1, err)
			}
			statuses[i] = status
			if status.finalizedEpoch < minimumEpoch {
				return false, fmt.Errorf("CL%d finalized epoch %d is below %d", i+1, status.finalizedEpoch, minimumEpoch)
			}
			if status.headSlot <= minimumHeadSlot {
				return false, fmt.Errorf("CL%d head slot %d has not advanced beyond %d", i+1, status.headSlot, minimumHeadSlot)
			}
		}
		if statuses[0].finalizedEpoch != statuses[1].finalizedEpoch || statuses[0].finalizedRoot != statuses[1].finalizedRoot {
			return false, fmt.Errorf("beacon finalized checkpoints differ: %d/%s != %d/%s", statuses[0].finalizedEpoch, statuses[0].finalizedRoot, statuses[1].finalizedEpoch, statuses[1].finalizedRoot)
		}
		return true, nil
	})
	return statuses, err
}

type executionFinalityStatus struct {
	safeNumber      uint64
	safeHash        common.Hash
	finalizedNumber uint64
	finalizedHash   common.Hash
}

func (s *systemCheck) waitExecutionFinality(ctx context.Context, validatorBaseline validatorDutySnapshots, minimumFinalizedBlock uint64, previous *executionFinalityStatus) (executionFinalityStatus, error) {
	var status executionFinalityStatus
	err := waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, "execution safe/finalized heads to converge with finalized consensus", func(ctx context.Context) (bool, error) {
		if err := s.checkValidatorContinuityIfDue(ctx, validatorBaseline, false, 0, 1); err != nil {
			return false, err
		}
		observed, err := s.executionFinality(ctx)
		if err != nil {
			return false, err
		}
		if observed.finalizedNumber < minimumFinalizedBlock {
			return false, fmt.Errorf("execution finalized head %d is below required block %d", observed.finalizedNumber, minimumFinalizedBlock)
		}
		if previous != nil {
			if err := validateExecutionFinalityAdvance(*previous, observed); err != nil {
				return false, err
			}
			if err := s.verifyFinalizedAncestor(ctx, *previous); err != nil {
				return false, err
			}
		}
		status = observed
		return true, nil
	})
	return status, err
}

func (s *systemCheck) executionFinality(ctx context.Context) (executionFinalityStatus, error) {
	tags := []struct {
		name   string
		number rpc.BlockNumber
	}{
		{name: "safe", number: rpc.SafeBlockNumber},
		{name: "finalized", number: rpc.FinalizedBlockNumber},
	}
	var status executionFinalityStatus
	for _, tag := range tags {
		var headers [2]*types.Header
		for i, client := range s.clients {
			header, err := client.HeaderByNumber(ctx, big.NewInt(int64(tag.number)))
			if err != nil {
				return executionFinalityStatus{}, fmt.Errorf("EL%d %s header: %w", i+1, tag.name, err)
			}
			headers[i] = header
		}
		if headers[0].Number.Sign() <= 0 || headers[0].Hash() != headers[1].Hash() {
			return executionFinalityStatus{}, fmt.Errorf("execution %s heads differ or remain at genesis", tag.name)
		}
		if tag.number == rpc.SafeBlockNumber {
			status.safeNumber = headers[0].Number.Uint64()
			status.safeHash = headers[0].Hash()
		} else {
			status.finalizedNumber = headers[0].Number.Uint64()
			status.finalizedHash = headers[0].Hash()
		}
	}
	if status.safeNumber < status.finalizedNumber {
		return executionFinalityStatus{}, fmt.Errorf("execution safe head %d precedes finalized head %d", status.safeNumber, status.finalizedNumber)
	}
	var payloads [2]finalizedExecutionPayload
	for i, rawURL := range s.cfg.clURLs {
		payload, err := s.http.finalizedExecutionPayload(ctx, rawURL)
		if err != nil {
			return executionFinalityStatus{}, fmt.Errorf("CL%d finalized execution payload: %w", i+1, err)
		}
		payloads[i] = payload
	}
	if err := validateFinalizedExecutionPayloads(status, payloads); err != nil {
		return executionFinalityStatus{}, err
	}
	return status, nil
}

func validateFinalizedExecutionPayloads(status executionFinalityStatus, payloads [2]finalizedExecutionPayload) error {
	if payloads[0] != payloads[1] {
		return fmt.Errorf("beacon finalized execution payloads differ: CL1=%d/%s CL2=%d/%s", payloads[0].blockNumber, payloads[0].blockHash, payloads[1].blockNumber, payloads[1].blockHash)
	}
	if payloads[0].blockNumber != status.finalizedNumber || payloads[0].blockHash != status.finalizedHash {
		return fmt.Errorf("beacon finalized execution payload %d/%s differs from execution finalized head %d/%s", payloads[0].blockNumber, payloads[0].blockHash, status.finalizedNumber, status.finalizedHash)
	}
	return nil
}

func validateExecutionFinalityAdvance(before, after executionFinalityStatus) error {
	if after.finalizedNumber <= before.finalizedNumber {
		return fmt.Errorf("execution finalized head %d/%s has not advanced beyond pre-fault %d/%s", after.finalizedNumber, after.finalizedHash, before.finalizedNumber, before.finalizedHash)
	}
	return nil
}

func (s *systemCheck) verifyFinalizedAncestor(ctx context.Context, previous executionFinalityStatus) error {
	number := new(big.Int).SetUint64(previous.finalizedNumber)
	for i, client := range s.clients {
		header, err := client.HeaderByNumber(ctx, number)
		if err != nil {
			return fmt.Errorf("EL%d pre-fault finalized block %d: %w", i+1, previous.finalizedNumber, err)
		}
		if header.Hash() != previous.finalizedHash {
			return fmt.Errorf("EL%d pre-fault finalized block %d changed hash from %s to %s", i+1, previous.finalizedNumber, previous.finalizedHash, header.Hash())
		}
	}
	return nil
}
