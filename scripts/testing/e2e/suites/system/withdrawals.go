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
	"log"
	"math/big"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/trie"
)

type withdrawalEvidence struct {
	blockNumber uint64
	blockHash   common.Hash
	amount      *big.Int
}

func (s *systemCheck) waitAutomaticWithdrawal(ctx context.Context, validatorBaseline validatorDutySnapshots) error {
	if err := s.checkValidatorContinuityIfDue(ctx, validatorBaseline, false, 0, 1); err != nil {
		return err
	}
	headA, err := s.clients[0].BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("EL1 withdrawal scan start: %w", err)
	}
	headB, err := s.clients[1].BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("EL2 withdrawal scan start: %w", err)
	}
	next := max(headA, headB) + 1
	var found withdrawalEvidence
	err = waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, fmt.Sprintf("automatic partial withdrawal to %s in new canonical blocks", s.cfg.withdrawalRecipient), func(ctx context.Context) (bool, error) {
		if err := s.checkValidatorContinuityIfDue(ctx, validatorBaseline, false, 0, 1); err != nil {
			return false, err
		}
		headA, err := s.clients[0].BlockNumber(ctx)
		if err != nil {
			return false, fmt.Errorf("EL1 withdrawal scan head: %w", err)
		}
		headB, err := s.clients[1].BlockNumber(ctx)
		if err != nil {
			return false, fmt.Errorf("EL2 withdrawal scan head: %w", err)
		}
		sharedHead := min(headA, headB)
		for number := next; number <= sharedHead; number++ {
			evidence, err := s.withdrawalEvidenceAt(ctx, number)
			if err != nil {
				return false, err
			}
			if evidence.amount.Sign() == 0 {
				next = number + 1
				continue
			}
			found = evidence
			return true, nil
		}
		return false, fmt.Errorf("scanned through shared canonical block %d without an automatic withdrawal", sharedHead)
	})
	if err != nil {
		return err
	}
	finality, err := s.waitExecutionFinality(ctx, validatorBaseline, found.blockNumber, nil)
	if err != nil {
		return fmt.Errorf("finalize withdrawal block %d: %w", found.blockNumber, err)
	}
	if err := s.checkValidatorContinuityIfDue(ctx, validatorBaseline, true, 0, 1); err != nil {
		return err
	}
	// Normal full nodes may prune the historical state needed for BalanceAt while
	// finality advances. The exact balance delta was already verified on both ELs
	// when the block was fresh, so finalized revalidation intentionally checks the
	// canonical block hash, withdrawal root, body, and amount without archive state.
	confirmed, err := s.withdrawalBlockEvidenceAt(ctx, found.blockNumber)
	if err != nil {
		return fmt.Errorf("revalidate finalized withdrawal block %d: %w", found.blockNumber, err)
	}
	if err := validateWithdrawalEvidence(found, confirmed); err != nil {
		return fmt.Errorf("revalidate finalized withdrawal block %d: %w", found.blockNumber, err)
	}
	log.Printf("PASS: both execution nodes credited %s planck to %s when block %d was fresh and retained identical full-width canonical withdrawal evidence through finalized head %d", found.amount, s.cfg.withdrawalRecipient, found.blockNumber, finality.finalizedNumber)
	return nil
}

func (s *systemCheck) withdrawalEvidenceAt(ctx context.Context, number uint64) (withdrawalEvidence, error) {
	evidence, err := s.withdrawalBlockEvidenceAt(ctx, number)
	if err != nil {
		return withdrawalEvidence{}, err
	}
	if evidence.amount.Sign() > 0 {
		if err := s.verifyWithdrawalBalanceDeltaAt(ctx, evidence); err != nil {
			return withdrawalEvidence{}, err
		}
	}
	return evidence, nil
}

func (s *systemCheck) withdrawalBlockEvidenceAt(ctx context.Context, number uint64) (withdrawalEvidence, error) {
	blockNumber := new(big.Int).SetUint64(number)
	blockA, err := s.clients[0].BlockByNumber(ctx, blockNumber)
	if err != nil {
		return withdrawalEvidence{}, fmt.Errorf("EL1 withdrawal scan block %d: %w", number, err)
	}
	blockB, err := s.clients[1].BlockByNumber(ctx, blockNumber)
	if err != nil {
		return withdrawalEvidence{}, fmt.Errorf("EL2 withdrawal scan block %d: %w", number, err)
	}
	if blockA.NumberU64() != number || blockB.NumberU64() != number {
		return withdrawalEvidence{}, fmt.Errorf("withdrawal scan requested block %d but EL1 returned %d and EL2 returned %d", number, blockA.NumberU64(), blockB.NumberU64())
	}
	if blockA.Hash() != blockB.Hash() {
		return withdrawalEvidence{}, fmt.Errorf("withdrawal scan block %d hashes differ: EL1=%s EL2=%s", number, blockA.Hash(), blockB.Hash())
	}
	if err := validateWithdrawalRoot(blockA.Header(), blockA.Withdrawals()); err != nil {
		return withdrawalEvidence{}, fmt.Errorf("EL1 withdrawal scan block %d: %w", number, err)
	}
	if err := validateWithdrawalRoot(blockB.Header(), blockB.Withdrawals()); err != nil {
		return withdrawalEvidence{}, fmt.Errorf("EL2 withdrawal scan block %d: %w", number, err)
	}
	if err := compareWithdrawals(blockA.Withdrawals(), blockB.Withdrawals()); err != nil {
		return withdrawalEvidence{}, fmt.Errorf("withdrawal scan block %d: %w", number, err)
	}
	amount, err := withdrawalValue(blockA.Withdrawals(), s.cfg.withdrawalRecipient)
	if err != nil {
		return withdrawalEvidence{}, fmt.Errorf("withdrawal scan block %d: %w", number, err)
	}
	return withdrawalEvidence{blockNumber: number, blockHash: blockA.Hash(), amount: amount}, nil
}

func (s *systemCheck) verifyWithdrawalBalanceDeltaAt(ctx context.Context, evidence withdrawalEvidence) error {
	if evidence.blockNumber == 0 {
		return fmt.Errorf("cannot verify a withdrawal balance delta at genesis")
	}
	if evidence.amount == nil || evidence.amount.Sign() <= 0 {
		return fmt.Errorf("withdrawal block %d has no positive balance credit to verify", evidence.blockNumber)
	}
	blockNumber := new(big.Int).SetUint64(evidence.blockNumber)
	previous := new(big.Int).Sub(new(big.Int).Set(blockNumber), big.NewInt(1))
	for i, client := range s.clients {
		before, err := client.BalanceAt(ctx, s.cfg.withdrawalRecipient, previous)
		if err != nil {
			return fmt.Errorf("EL%d withdrawal-recipient balance before block %d: %w", i+1, evidence.blockNumber, err)
		}
		after, err := client.BalanceAt(ctx, s.cfg.withdrawalRecipient, blockNumber)
		if err != nil {
			return fmt.Errorf("EL%d withdrawal-recipient balance at block %d: %w", i+1, evidence.blockNumber, err)
		}
		delta := new(big.Int).Sub(after, before)
		if delta.Cmp(evidence.amount) != 0 {
			return fmt.Errorf("EL%d withdrawal-recipient balance delta at block %d is %s, want exact withdrawal credit %s", i+1, evidence.blockNumber, delta, evidence.amount)
		}
	}
	return nil
}

func validateWithdrawalEvidence(before, after withdrawalEvidence) error {
	if before.blockNumber != after.blockNumber {
		return fmt.Errorf("withdrawal block number changed from %d to %d", before.blockNumber, after.blockNumber)
	}
	if before.blockHash != after.blockHash {
		return fmt.Errorf("withdrawal block %d hash changed from %s to %s", before.blockNumber, before.blockHash, after.blockHash)
	}
	if before.amount == nil || after.amount == nil {
		return fmt.Errorf("withdrawal block %d has nil amount evidence", before.blockNumber)
	}
	if before.amount.Sign() <= 0 || after.amount.Cmp(before.amount) != 0 {
		return fmt.Errorf("withdrawal block %d amount changed from %s to %s", before.blockNumber, before.amount, after.amount)
	}
	return nil
}

func compareWithdrawals(a, b types.Withdrawals) error {
	if len(a) != len(b) {
		return fmt.Errorf("withdrawal counts differ: EL1=%d EL2=%d", len(a), len(b))
	}
	for i := range a {
		if a[i] == nil || b[i] == nil {
			return fmt.Errorf("withdrawal %d is nil", i)
		}
		if *a[i] != *b[i] {
			return fmt.Errorf("withdrawal %d differs: EL1=%+v EL2=%+v", i, *a[i], *b[i])
		}
	}
	return nil
}

func validateWithdrawalRoot(header *types.Header, withdrawals types.Withdrawals) error {
	if header == nil || header.WithdrawalsHash == nil {
		return fmt.Errorf("post-Zond header has no withdrawals root")
	}
	root := types.DeriveSha(withdrawals, trie.NewStackTrie(nil))
	if root != *header.WithdrawalsHash {
		return fmt.Errorf("withdrawals root is %s, calculated %s from RPC body", *header.WithdrawalsHash, root)
	}
	return nil
}

func withdrawalValue(withdrawals types.Withdrawals, recipient common.Address) (*big.Int, error) {
	totalShor := new(big.Int)
	for i, withdrawal := range withdrawals {
		if withdrawal == nil {
			return nil, fmt.Errorf("withdrawal %d is nil", i)
		}
		if got := len(withdrawal.Address.Bytes()); got != common.AddressLength {
			return nil, fmt.Errorf("withdrawal %d address width is %d, want %d", i, got, common.AddressLength)
		}
		if withdrawal.Address == recipient {
			if withdrawal.Amount == 0 {
				return nil, fmt.Errorf("withdrawal %d to configured recipient has zero amount", i)
			}
			totalShor.Add(totalShor, new(big.Int).SetUint64(withdrawal.Amount))
		}
	}
	return totalShor.Mul(totalShor, big.NewInt(params.Shor)), nil
}
