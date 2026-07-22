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
	"sort"
	"strings"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/trie"
)

const baseAutomaticWithdrawalFreshObservationPrefix = "base/automatic-withdrawal-fresh-balance-verified/"

type withdrawalEvidence struct {
	blockNumber      uint64
	blockHash        common.Hash
	withdrawalsRoot  common.Hash
	withdrawals      types.Withdrawals
	amount           *big.Int
	balances         [2]withdrawalBalanceEvidence
	balancesVerified bool
}

type withdrawalBalanceEvidence struct {
	before *big.Int
	after  *big.Int
	delta  *big.Int
}

type automaticWithdrawalObservation struct {
	Version               int                            `json:"version"`
	BlockNumber           uint64                         `json:"block_number"`
	BlockHash             string                         `json:"block_hash"`
	WithdrawalsRoot       string                         `json:"withdrawals_root"`
	Withdrawals           []withdrawalObservation        `json:"withdrawals"`
	Recipient             string                         `json:"recipient"`
	RecipientAmountPlanck string                         `json:"recipient_amount_planck"`
	ELBalances            []withdrawalBalanceObservation `json:"el_balances"`
}

type withdrawalObservation struct {
	Index          uint64 `json:"index"`
	ValidatorIndex uint64 `json:"validator_index"`
	Address        string `json:"address"`
	AmountShor     uint64 `json:"amount_shor"`
}

type withdrawalBalanceObservation struct {
	BeforePlanck string `json:"before_planck"`
	AfterPlanck  string `json:"after_planck"`
	DeltaPlanck  string `json:"delta_planck"`
}

func (s *systemCheck) waitAutomaticWithdrawal(ctx context.Context, validatorBaseline validatorDutySnapshots) error {
	if err := s.checkValidatorContinuityIfDue(ctx, validatorBaseline, false, 0, 1); err != nil {
		return err
	}
	found, recorded, rescanFrom, err := s.recordedAutomaticWithdrawalEvidence(ctx)
	if err != nil {
		return err
	}
	if !recorded {
		headA, err := s.clients[0].BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("EL1 withdrawal scan start: %w", err)
		}
		headB, err := s.clients[1].BlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("EL2 withdrawal scan start: %w", err)
		}
		next := max(headA, headB) + 1
		if rescanFrom != 0 {
			next = rescanFrom
		}
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
		balances, err := s.verifyWithdrawalBalanceDeltaAt(ctx, evidence)
		if err != nil {
			return withdrawalEvidence{}, err
		}
		evidence.balances = balances
		evidence.balancesVerified = true
		observation, err := automaticWithdrawalObservationFromEvidence(evidence, s.cfg.withdrawalRecipient)
		if err != nil {
			return withdrawalEvidence{}, fmt.Errorf("encode fresh automatic withdrawal evidence: %w", err)
		}
		label := automaticWithdrawalObservationLabel(evidence.blockHash)
		if err := s.recordTypedSystemObservation(context.WithoutCancel(ctx), label, observation); err != nil {
			return withdrawalEvidence{}, err
		}
	}
	return evidence, nil
}

func (s *systemCheck) recordedAutomaticWithdrawalEvidence(ctx context.Context) (withdrawalEvidence, bool, uint64, error) {
	labels := make([]string, 0)
	for label := range s.resume.observations {
		if isAutomaticWithdrawalObservationLabel(label) {
			labels = append(labels, label)
		}
	}
	sort.Strings(labels)
	var rescanFrom uint64
	for _, label := range labels {
		found, err := decodeAutomaticWithdrawalObservation(s.resume.observations[label], s.cfg.withdrawalRecipient)
		if err != nil {
			return withdrawalEvidence{}, true, 0, fmt.Errorf("decode recorded fresh automatic withdrawal evidence %q: %w", label, err)
		}
		if want := automaticWithdrawalObservationLabel(found.blockHash); label != want {
			return withdrawalEvidence{}, true, 0, fmt.Errorf("recorded fresh automatic withdrawal label %q does not match block hash; want %q", label, want)
		}
		confirmed, err := s.withdrawalBlockEvidenceAt(ctx, found.blockNumber)
		if err != nil {
			return withdrawalEvidence{}, true, 0, fmt.Errorf("revalidate recorded fresh automatic withdrawal block %d: %w", found.blockNumber, err)
		}
		if confirmed.blockHash != found.blockHash {
			// A candidate is recorded before finality so a crash cannot erase the
			// fresh balance proof. Preserve a legitimately reorged candidate as
			// append-only evidence, but allow a new canonical candidate to be
			// scanned and recorded under its own block-hash-scoped key.
			if rescanFrom == 0 || found.blockNumber < rescanFrom {
				rescanFrom = found.blockNumber
			}
			continue
		}
		if err := validateWithdrawalEvidence(found, confirmed); err != nil {
			return withdrawalEvidence{}, true, 0, fmt.Errorf("revalidate recorded fresh automatic withdrawal block %d: %w", found.blockNumber, err)
		}
		return found, true, 0, nil
	}
	return withdrawalEvidence{}, false, rescanFrom, nil
}

func automaticWithdrawalObservationLabel(hash common.Hash) string {
	return baseAutomaticWithdrawalFreshObservationPrefix + strings.TrimPrefix(hash.Hex(), "0x")
}

func isAutomaticWithdrawalObservationLabel(label string) bool {
	return strings.HasPrefix(label, baseAutomaticWithdrawalFreshObservationPrefix)
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
	return withdrawalEvidence{
		blockNumber: number, blockHash: blockA.Hash(), withdrawalsRoot: *blockA.Header().WithdrawalsHash,
		withdrawals: cloneWithdrawals(blockA.Withdrawals()), amount: amount,
	}, nil
}

func (s *systemCheck) verifyWithdrawalBalanceDeltaAt(ctx context.Context, evidence withdrawalEvidence) ([2]withdrawalBalanceEvidence, error) {
	var verified [2]withdrawalBalanceEvidence
	if evidence.blockNumber == 0 {
		return verified, fmt.Errorf("cannot verify a withdrawal balance delta at genesis")
	}
	if evidence.amount == nil || evidence.amount.Sign() <= 0 {
		return verified, fmt.Errorf("withdrawal block %d has no positive balance credit to verify", evidence.blockNumber)
	}
	blockNumber := new(big.Int).SetUint64(evidence.blockNumber)
	previous := new(big.Int).Sub(new(big.Int).Set(blockNumber), big.NewInt(1))
	for i, client := range s.clients {
		before, err := client.BalanceAt(ctx, s.cfg.withdrawalRecipient, previous)
		if err != nil {
			return verified, fmt.Errorf("EL%d withdrawal-recipient balance before block %d: %w", i+1, evidence.blockNumber, err)
		}
		after, err := client.BalanceAt(ctx, s.cfg.withdrawalRecipient, blockNumber)
		if err != nil {
			return verified, fmt.Errorf("EL%d withdrawal-recipient balance at block %d: %w", i+1, evidence.blockNumber, err)
		}
		delta := new(big.Int).Sub(after, before)
		if delta.Cmp(evidence.amount) != 0 {
			return verified, fmt.Errorf("EL%d withdrawal-recipient balance delta at block %d is %s, want exact withdrawal credit %s", i+1, evidence.blockNumber, delta, evidence.amount)
		}
		verified[i] = withdrawalBalanceEvidence{
			before: new(big.Int).Set(before), after: new(big.Int).Set(after), delta: new(big.Int).Set(delta),
		}
	}
	return verified, nil
}

func validateWithdrawalEvidence(before, after withdrawalEvidence) error {
	if before.blockNumber != after.blockNumber {
		return fmt.Errorf("withdrawal block number changed from %d to %d", before.blockNumber, after.blockNumber)
	}
	if before.blockHash != after.blockHash {
		return fmt.Errorf("withdrawal block %d hash changed from %s to %s", before.blockNumber, before.blockHash, after.blockHash)
	}
	if before.withdrawalsRoot != after.withdrawalsRoot {
		return fmt.Errorf("withdrawal block %d root changed from %s to %s", before.blockNumber, before.withdrawalsRoot, after.withdrawalsRoot)
	}
	if err := compareWithdrawals(before.withdrawals, after.withdrawals); err != nil {
		return fmt.Errorf("withdrawal block %d list changed: %w", before.blockNumber, err)
	}
	if before.amount == nil || after.amount == nil {
		return fmt.Errorf("withdrawal block %d has nil amount evidence", before.blockNumber)
	}
	if before.amount.Sign() <= 0 || after.amount.Cmp(before.amount) != 0 {
		return fmt.Errorf("withdrawal block %d amount changed from %s to %s", before.blockNumber, before.amount, after.amount)
	}
	return nil
}

func automaticWithdrawalObservationFromEvidence(evidence withdrawalEvidence, recipient common.Address) (automaticWithdrawalObservation, error) {
	if evidence.blockNumber == 0 || evidence.blockHash == (common.Hash{}) || evidence.withdrawalsRoot == (common.Hash{}) || evidence.amount == nil || evidence.amount.Sign() <= 0 {
		return automaticWithdrawalObservation{}, fmt.Errorf("fresh withdrawal block identity or amount is incomplete")
	}
	if !evidence.balancesVerified {
		return automaticWithdrawalObservation{}, fmt.Errorf("fresh withdrawal balance deltas were not verified")
	}
	root := types.DeriveSha(evidence.withdrawals, trie.NewStackTrie(nil))
	if root != evidence.withdrawalsRoot {
		return automaticWithdrawalObservation{}, fmt.Errorf("fresh withdrawal list derives root %s, want %s", root, evidence.withdrawalsRoot)
	}
	amount, err := withdrawalValue(evidence.withdrawals, recipient)
	if err != nil {
		return automaticWithdrawalObservation{}, err
	}
	if amount.Cmp(evidence.amount) != 0 {
		return automaticWithdrawalObservation{}, fmt.Errorf("fresh withdrawal list credits %s planck, want %s", amount, evidence.amount)
	}
	observation := automaticWithdrawalObservation{
		Version: 1, BlockNumber: evidence.blockNumber, BlockHash: evidence.blockHash.Hex(),
		WithdrawalsRoot: evidence.withdrawalsRoot.Hex(), Recipient: recipient.Hex(),
		RecipientAmountPlanck: hexutil.EncodeBig(evidence.amount),
		Withdrawals:           make([]withdrawalObservation, len(evidence.withdrawals)),
		ELBalances:            make([]withdrawalBalanceObservation, len(evidence.balances)),
	}
	for i, withdrawal := range evidence.withdrawals {
		if withdrawal == nil {
			return automaticWithdrawalObservation{}, fmt.Errorf("fresh withdrawal %d is nil", i)
		}
		observation.Withdrawals[i] = withdrawalObservation{
			Index: withdrawal.Index, ValidatorIndex: withdrawal.Validator,
			Address: withdrawal.Address.Hex(), AmountShor: withdrawal.Amount,
		}
	}
	for i, balance := range evidence.balances {
		if balance.before == nil || balance.after == nil || balance.delta == nil || balance.before.Sign() < 0 || balance.after.Sign() < 0 || balance.delta.Cmp(evidence.amount) != 0 || new(big.Int).Sub(balance.after, balance.before).Cmp(balance.delta) != 0 {
			return automaticWithdrawalObservation{}, fmt.Errorf("EL%d fresh withdrawal balance evidence is invalid", i+1)
		}
		observation.ELBalances[i] = withdrawalBalanceObservation{
			BeforePlanck: hexutil.EncodeBig(balance.before), AfterPlanck: hexutil.EncodeBig(balance.after), DeltaPlanck: hexutil.EncodeBig(balance.delta),
		}
	}
	return observation, nil
}

func decodeAutomaticWithdrawalObservation(raw string, recipient common.Address) (withdrawalEvidence, error) {
	var observation automaticWithdrawalObservation
	if err := decodeStrictJSON(raw, &observation); err != nil {
		return withdrawalEvidence{}, err
	}
	if observation.Version != 1 || observation.BlockNumber == 0 {
		return withdrawalEvidence{}, fmt.Errorf("automatic withdrawal observation version or block number is invalid")
	}
	blockHash, err := canonicalObservationHash("block hash", observation.BlockHash)
	if err != nil {
		return withdrawalEvidence{}, err
	}
	withdrawalsRoot, err := canonicalObservationHash("withdrawals root", observation.WithdrawalsRoot)
	if err != nil {
		return withdrawalEvidence{}, err
	}
	observedRecipient, err := common.NewAddressFromString(observation.Recipient)
	if err != nil || observedRecipient.Hex() != observation.Recipient || observedRecipient != recipient {
		return withdrawalEvidence{}, fmt.Errorf("automatic withdrawal recipient is invalid or differs from configured recipient")
	}
	if len(observation.Withdrawals) == 0 {
		return withdrawalEvidence{}, fmt.Errorf("automatic withdrawal list is empty")
	}
	withdrawals := make(types.Withdrawals, len(observation.Withdrawals))
	for i, entry := range observation.Withdrawals {
		address, err := common.NewAddressFromString(entry.Address)
		if err != nil || address.Hex() != entry.Address {
			return withdrawalEvidence{}, fmt.Errorf("automatic withdrawal %d address is invalid or non-canonical", i)
		}
		withdrawals[i] = &types.Withdrawal{Index: entry.Index, Validator: entry.ValidatorIndex, Address: address, Amount: entry.AmountShor}
	}
	derivedRoot := types.DeriveSha(withdrawals, trie.NewStackTrie(nil))
	if derivedRoot != withdrawalsRoot {
		return withdrawalEvidence{}, fmt.Errorf("automatic withdrawal list derives root %s, want %s", derivedRoot, withdrawalsRoot)
	}
	amount, err := withdrawalValue(withdrawals, recipient)
	if err != nil {
		return withdrawalEvidence{}, fmt.Errorf("automatic withdrawal recipient amount is invalid: %w", err)
	}
	if amount.Sign() <= 0 {
		return withdrawalEvidence{}, fmt.Errorf("automatic withdrawal recipient amount is not positive")
	}
	recordedAmount, err := canonicalObservationBig("recipient amount", observation.RecipientAmountPlanck)
	if err != nil {
		return withdrawalEvidence{}, err
	}
	if recordedAmount.Cmp(amount) != 0 {
		return withdrawalEvidence{}, fmt.Errorf("automatic withdrawal recipient amount is %s, want list-derived %s", recordedAmount, amount)
	}
	if len(observation.ELBalances) != 2 {
		return withdrawalEvidence{}, fmt.Errorf("automatic withdrawal balance evidence has %d ELs, want 2", len(observation.ELBalances))
	}
	var balances [2]withdrawalBalanceEvidence
	for i, entry := range observation.ELBalances {
		before, err := canonicalObservationBig(fmt.Sprintf("EL%d before balance", i+1), entry.BeforePlanck)
		if err != nil {
			return withdrawalEvidence{}, err
		}
		after, err := canonicalObservationBig(fmt.Sprintf("EL%d after balance", i+1), entry.AfterPlanck)
		if err != nil {
			return withdrawalEvidence{}, err
		}
		delta, err := canonicalObservationBig(fmt.Sprintf("EL%d balance delta", i+1), entry.DeltaPlanck)
		if err != nil {
			return withdrawalEvidence{}, err
		}
		if new(big.Int).Sub(after, before).Cmp(delta) != 0 || delta.Cmp(amount) != 0 {
			return withdrawalEvidence{}, fmt.Errorf("EL%d automatic withdrawal balance delta does not equal the exact recipient credit", i+1)
		}
		balances[i] = withdrawalBalanceEvidence{before: before, after: after, delta: delta}
	}
	return withdrawalEvidence{
		blockNumber: observation.BlockNumber, blockHash: blockHash, withdrawalsRoot: withdrawalsRoot,
		withdrawals: withdrawals, amount: recordedAmount, balances: balances, balancesVerified: true,
	}, nil
}

func canonicalObservationHash(name, value string) (common.Hash, error) {
	if !common.IsHexEncodedHash(value) {
		return common.Hash{}, fmt.Errorf("automatic withdrawal %s is invalid", name)
	}
	hash := common.HexToHash(value)
	if hash == (common.Hash{}) || hash.Hex() != value {
		return common.Hash{}, fmt.Errorf("automatic withdrawal %s is zero or non-canonical", name)
	}
	return hash, nil
}

func canonicalObservationBig(name, value string) (*big.Int, error) {
	number, err := hexutil.DecodeBig(value)
	if err != nil || number.Sign() < 0 || hexutil.EncodeBig(number) != value {
		return nil, fmt.Errorf("automatic withdrawal %s is invalid or non-canonical", name)
	}
	return number, nil
}

func cloneWithdrawals(values types.Withdrawals) types.Withdrawals {
	cloned := make(types.Withdrawals, len(values))
	for i, value := range values {
		if value != nil {
			copy := *value
			cloned[i] = &copy
		}
	}
	return cloned
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
