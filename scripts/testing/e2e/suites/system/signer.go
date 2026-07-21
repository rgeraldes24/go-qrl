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
	"math/big"
	"strings"
	"time"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/rpc"
)

type managedAccountState struct {
	head             uint64
	nonce            uint64
	pendingNonce     uint64
	recipientBalance *big.Int
}

func (s *systemCheck) waitSignerReady(ctx context.Context) error {
	return waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, "topology Clef to become ready", func(ctx context.Context) (bool, error) {
		endpoint, err := s.cfg.signerEndpoint(ctx, s.k)
		if err != nil {
			return false, err
		}
		client, err := rpc.DialContext(ctx, endpoint)
		if err != nil {
			return false, err
		}
		defer client.Close()
		var version string
		if err := client.CallContext(ctx, &version, "account_version"); err != nil {
			return false, err
		}
		if version == "" {
			return false, fmt.Errorf("Clef returned an empty API version")
		}
		var accounts []common.Address
		if err := client.CallContext(ctx, &accounts, "account_list"); err != nil {
			return false, err
		}
		if !containsAddress(accounts, s.cfg.signerAddress) {
			return false, fmt.Errorf("Clef account list does not contain %s", s.cfg.signerAddress)
		}
		if endpoint != s.cfg.signerURL {
			log.Printf("systemcheck: refreshed %s HTTP endpoint after restart: %s -> %s", s.cfg.signerSvc, s.cfg.signerURL, endpoint)
			if err := s.recordEndpoint(ctx, s.cfg.signerSvc, "signer-http", s.cfg.signerURL, endpoint); err != nil {
				return false, err
			}
			s.cfg.signerURL = endpoint
		}
		return true, nil
	})
}

func (s *systemCheck) sendManagedTransfer(ctx context.Context, origin int, evidenceLabel string) (common.Hash, error) {
	if origin < 0 || origin >= len(s.clients) || s.clients[origin] == nil {
		return common.Hash{}, fmt.Errorf("invalid execution origin EL%d", origin+1)
	}
	if hash, ok := s.recordedTransaction(evidenceLabel); ok {
		return hash, nil
	}
	value := new(big.Int).SetUint64(s.cfg.transferValue)
	if evidenceLabel == TransactionLabelSignerOutageProbe {
		// Keep the deliberately timed-out outage request distinguishable from
		// the later recovery transfer. If it executes after the finite
		// quiescence window, it cannot be mistaken for the exact journaled
		// recovery intent and its value/nonce side effect remains detectable.
		value.Add(value, big.NewInt(1))
	}
	if s.managedJournal != nil && evidenceLabel != TransactionLabelSignerOutageProbe {
		to := s.cfg.recipient
		return s.sendJournaledManagedTransaction(ctx, evidenceLabel, managedTransactionRequest{
			origin: origin, to: &to, value: value, input: []byte{}, accessList: nil,
		})
	}
	args := struct {
		From  common.Address `json:"from"`
		To    common.Address `json:"to"`
		Value *hexutil.Big   `json:"value"`
	}{
		From:  s.cfg.signerAddress,
		To:    s.cfg.recipient,
		Value: (*hexutil.Big)(value),
	}
	var hash common.Hash
	if err := s.clients[origin].Client().CallContext(ctx, &hash, "qrl_sendTransaction", args); err != nil {
		return common.Hash{}, err
	}
	if hash == (common.Hash{}) {
		return common.Hash{}, fmt.Errorf("qrl_sendTransaction returned a zero hash")
	}
	if err := s.recordTransaction(ctx, evidenceLabel, hash); err != nil {
		return common.Hash{}, err
	}
	return hash, nil
}

func (s *systemCheck) readManagedAccountStates(ctx context.Context) ([2]managedAccountState, error) {
	var states [2]managedAccountState
	for i, client := range s.clients {
		if client == nil {
			return states, fmt.Errorf("EL%d client is unavailable", i+1)
		}
		head, err := client.BlockNumber(ctx)
		if err != nil {
			return states, fmt.Errorf("EL%d block number: %w", i+1, err)
		}
		nonce, err := client.NonceAt(ctx, s.cfg.signerAddress, nil)
		if err != nil {
			return states, fmt.Errorf("EL%d signer nonce: %w", i+1, err)
		}
		pendingNonce, err := client.PendingNonceAt(ctx, s.cfg.signerAddress)
		if err != nil {
			return states, fmt.Errorf("EL%d pending signer nonce: %w", i+1, err)
		}
		balance, err := client.BalanceAt(ctx, s.cfg.recipient, nil)
		if err != nil {
			return states, fmt.Errorf("EL%d recipient balance: %w", i+1, err)
		}
		states[i] = managedAccountState{
			head:             head,
			nonce:            nonce,
			pendingNonce:     pendingNonce,
			recipientBalance: new(big.Int).Set(balance),
		}
	}
	return states, nil
}

func validateManagedAccountBaseline(states [2]managedAccountState) error {
	for i, state := range states {
		if state.recipientBalance == nil {
			return fmt.Errorf("EL%d recipient balance is missing", i+1)
		}
		if state.pendingNonce != state.nonce {
			return fmt.Errorf("EL%d signer has pending nonce %d above confirmed nonce %d", i+1, state.pendingNonce, state.nonce)
		}
	}
	if states[0].nonce != states[1].nonce {
		return fmt.Errorf("signer nonces differ: EL1=%d EL2=%d", states[0].nonce, states[1].nonce)
	}
	if states[0].recipientBalance.Cmp(states[1].recipientBalance) != 0 {
		return fmt.Errorf("recipient balances differ: EL1=%s EL2=%s", states[0].recipientBalance, states[1].recipientBalance)
	}
	return nil
}

func validateManagedAccountUnchanged(baseline, current [2]managedAccountState) error {
	for i := range current {
		if current[i].recipientBalance == nil {
			return fmt.Errorf("EL%d recipient balance is missing", i+1)
		}
		if current[i].nonce != baseline[i].nonce {
			return fmt.Errorf("EL%d confirmed signer nonce changed from %d to %d", i+1, baseline[i].nonce, current[i].nonce)
		}
		if current[i].pendingNonce != baseline[i].pendingNonce {
			return fmt.Errorf("EL%d pending signer nonce changed from %d to %d", i+1, baseline[i].pendingNonce, current[i].pendingNonce)
		}
		if baseline[i].recipientBalance == nil || current[i].recipientBalance.Cmp(baseline[i].recipientBalance) != 0 {
			return fmt.Errorf("EL%d recipient balance changed from %v to %s", i+1, baseline[i].recipientBalance, current[i].recipientBalance)
		}
	}
	return nil
}

func (s *systemCheck) waitManagedAccountBaseline(ctx context.Context) ([2]managedAccountState, error) {
	var states [2]managedAccountState
	err := waitFor(ctx, signerCancellationObservationTimeout, s.cfg.pollInterval, "managed account state to become quiescent before the Clef outage", func(ctx context.Context) (bool, error) {
		observed, err := s.readManagedAccountStates(ctx)
		if err != nil {
			return false, err
		}
		if err := validateManagedAccountBaseline(observed); err != nil {
			return false, err
		}
		states = observed
		return true, nil
	})
	return states, err
}

func (s *systemCheck) waitCanceledTransferQuiescence(ctx context.Context, baseline [2]managedAccountState) error {
	waitCtx, cancel := context.WithTimeout(ctx, signerCancellationObservationTimeout)
	defer cancel()
	var (
		lastErr     error
		targetHeads [2]uint64
		haveTargets bool
	)
	for {
		current, err := s.readManagedAccountStates(waitCtx)
		if err == nil {
			if err := validateManagedAccountUnchanged(baseline, current); err != nil {
				return fmt.Errorf("timed-out Clef-outage request caused a transaction side effect: %w", err)
			}
			if !haveTargets {
				for i := range current {
					targetHeads[i] = current[i].head + signerCancellationObservationBlocks
				}
				haveTargets = true
				lastErr = fmt.Errorf("captured post-restart heads EL1=%d EL2=%d; waiting for EL1=%d EL2=%d", current[0].head, current[1].head, targetHeads[0], targetHeads[1])
			} else {
				advanced := true
				for i := range current {
					if current[i].head < targetHeads[i] {
						advanced = false
						lastErr = fmt.Errorf("EL%d is at block %d, waiting for post-restart block %d", i+1, current[i].head, targetHeads[i])
					}
				}
				if advanced {
					return nil
				}
			}
		} else {
			lastErr = err
		}
		timer := time.NewTimer(s.cfg.pollInterval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if lastErr != nil {
				return fmt.Errorf("timed out proving the canceled Clef-outage request stayed side-effect free: %w", lastErr)
			}
			return fmt.Errorf("timed out proving the canceled Clef-outage request stayed side-effect free: %w", waitCtx.Err())
		case <-timer.C:
		}
	}
}

func (s *systemCheck) waitReceipt(ctx context.Context, clientIndex int, hash common.Hash) (*types.Receipt, error) {
	var receipt *types.Receipt
	err := waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, fmt.Sprintf("EL%d receipt %s", clientIndex+1, hash), func(ctx context.Context) (bool, error) {
		got, err := s.clients[clientIndex].TransactionReceipt(ctx, hash)
		if err != nil {
			if errors.Is(err, qrl.NotFound) {
				return false, err
			}
			return false, err
		}
		receipt = got
		return true, nil
	})
	return receipt, err
}

func (s *systemCheck) verifyTransferOnBoth(ctx context.Context, hash common.Hash) (*types.Receipt, error) {
	receiptA, err := s.waitReceipt(ctx, 0, hash)
	if err != nil {
		return nil, err
	}
	receiptB, err := s.waitReceipt(ctx, 1, hash)
	if err != nil {
		return nil, err
	}
	if receiptA.Status != types.ReceiptStatusSuccessful || receiptB.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("transaction %s failed: EL1 status=%d EL2 status=%d", hash, receiptA.Status, receiptB.Status)
	}
	if receiptA.BlockNumber == nil || receiptB.BlockNumber == nil || receiptA.BlockNumber.Sign() <= 0 {
		return nil, fmt.Errorf("transaction %s has invalid inclusion block", hash)
	}
	if receiptA.BlockNumber.Cmp(receiptB.BlockNumber) != 0 || receiptA.BlockHash != receiptB.BlockHash {
		return nil, fmt.Errorf("receipt inclusion differs: EL1=%s/%s EL2=%s/%s", receiptA.BlockNumber, receiptA.BlockHash, receiptB.BlockNumber, receiptB.BlockHash)
	}
	receipts := [2]*types.Receipt{receiptA, receiptB}

	var signed *types.Transaction
	for i, client := range s.clients {
		tx, pending, err := client.TransactionByHash(ctx, hash)
		if err != nil {
			return nil, fmt.Errorf("EL%d transaction %s: %w", i+1, hash, err)
		}
		if pending {
			return nil, fmt.Errorf("EL%d still reports transaction %s as pending", i+1, hash)
		}
		if tx.To() == nil || *tx.To() != s.cfg.recipient {
			return nil, fmt.Errorf("EL%d transaction recipient mismatch", i+1)
		}
		if tx.Value().Cmp(new(big.Int).SetUint64(s.cfg.transferValue)) != 0 {
			return nil, fmt.Errorf("EL%d transaction value mismatch: %s", i+1, tx.Value())
		}
		sender, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
		if err != nil {
			return nil, fmt.Errorf("EL%d recover transaction sender: %w", i+1, err)
		}
		if sender != s.cfg.signerAddress {
			return nil, fmt.Errorf("EL%d recovered sender %s, want %s", i+1, sender, s.cfg.signerAddress)
		}
		signed = tx
	}

	blockNumber := new(big.Int).Set(receiptA.BlockNumber)
	previous := new(big.Int).Sub(new(big.Int).Set(blockNumber), big.NewInt(1))
	var headers [2]*types.Header
	for i, client := range s.clients {
		header, err := client.HeaderByNumber(ctx, blockNumber)
		if err != nil {
			return nil, fmt.Errorf("EL%d inclusion header: %w", i+1, err)
		}
		headers[i] = header
		if header.Coinbase != s.cfg.feeRecipient {
			return nil, fmt.Errorf("EL%d inclusion header fee recipient is %s, want qrl-package VM64 recipient %s", i+1, header.Coinbase, s.cfg.feeRecipient)
		}
		before, err := client.BalanceAt(ctx, s.cfg.recipient, previous)
		if err != nil {
			return nil, fmt.Errorf("EL%d recipient balance before transfer: %w", i+1, err)
		}
		after, err := client.BalanceAt(ctx, s.cfg.recipient, blockNumber)
		if err != nil {
			return nil, fmt.Errorf("EL%d recipient balance after transfer: %w", i+1, err)
		}
		delta := new(big.Int).Sub(after, before)
		if delta.Cmp(new(big.Int).SetUint64(s.cfg.transferValue)) != 0 {
			return nil, fmt.Errorf("EL%d recipient balance delta is %s, want %d", i+1, delta, s.cfg.transferValue)
		}
		nonceBefore, err := client.NonceAt(ctx, s.cfg.signerAddress, previous)
		if err != nil {
			return nil, fmt.Errorf("EL%d signer nonce before transfer: %w", i+1, err)
		}
		nonceAfter, err := client.NonceAt(ctx, s.cfg.signerAddress, blockNumber)
		if err != nil {
			return nil, fmt.Errorf("EL%d signer nonce after transfer: %w", i+1, err)
		}
		if nonceBefore != signed.Nonce() || nonceAfter != signed.Nonce()+1 {
			return nil, fmt.Errorf("EL%d signer nonce transition is %d -> %d for tx nonce %d", i+1, nonceBefore, nonceAfter, signed.Nonce())
		}
		feeBefore, err := client.BalanceAt(ctx, s.cfg.feeRecipient, previous)
		if err != nil {
			return nil, fmt.Errorf("EL%d fee-recipient balance before transfer: %w", i+1, err)
		}
		feeAfter, err := client.BalanceAt(ctx, s.cfg.feeRecipient, blockNumber)
		if err != nil {
			return nil, fmt.Errorf("EL%d fee-recipient balance after transfer: %w", i+1, err)
		}
		minimumReward, err := minimumFeeRecipientReward(header, receipts[i])
		if err != nil {
			return nil, fmt.Errorf("EL%d fee-recipient reward evidence: %w", i+1, err)
		}
		expectedReward, err := blockFeeRecipientCredit(ctx, client, header, s.cfg.feeRecipient)
		if err != nil {
			return nil, fmt.Errorf("EL%d total fee-recipient block credit: %w", i+1, err)
		}
		if expectedReward.Cmp(minimumReward) < 0 {
			return nil, fmt.Errorf("EL%d total block credit %s is below managed transaction tip %s", i+1, expectedReward, minimumReward)
		}
		feeDelta := new(big.Int).Sub(feeAfter, feeBefore)
		if feeDelta.Cmp(expectedReward) != 0 {
			return nil, fmt.Errorf("EL%d fee-recipient balance delta is %s, want exact block credit %s", i+1, feeDelta, expectedReward)
		}
	}
	if headers[0].Hash() != headers[1].Hash() || headers[0].Root != headers[1].Root || headers[0].ReceiptHash != headers[1].ReceiptHash {
		return nil, fmt.Errorf("execution nodes disagree on inclusion header/state/receipt roots")
	}
	return receiptA, nil
}

func minimumFeeRecipientReward(header *types.Header, receipt *types.Receipt) (*big.Int, error) {
	if header == nil || header.BaseFee == nil {
		return nil, fmt.Errorf("inclusion header has no base fee")
	}
	if receipt == nil || receipt.EffectiveGasPrice == nil {
		return nil, fmt.Errorf("receipt has no effective gas price")
	}
	if receipt.GasUsed == 0 {
		return nil, fmt.Errorf("receipt has zero gas used")
	}
	tipPerGas := new(big.Int).Sub(new(big.Int).Set(receipt.EffectiveGasPrice), header.BaseFee)
	if tipPerGas.Sign() <= 0 {
		return nil, fmt.Errorf("effective priority fee is %s, want a positive value", tipPerGas)
	}
	return tipPerGas.Mul(tipPerGas, new(big.Int).SetUint64(receipt.GasUsed)), nil
}

func blockFeeRecipientCredit(ctx context.Context, client *qrlclient.Client, header *types.Header, recipient common.Address) (*big.Int, error) {
	block, err := client.BlockByHash(ctx, header.Hash())
	if err != nil {
		return nil, fmt.Errorf("inclusion block: %w", err)
	}
	credit := new(big.Int)
	for _, tx := range block.Transactions() {
		receipt, err := client.TransactionReceipt(ctx, tx.Hash())
		if err != nil {
			return nil, fmt.Errorf("receipt %s: %w", tx.Hash(), err)
		}
		if receipt.EffectiveGasPrice == nil {
			return nil, fmt.Errorf("receipt %s has no effective gas price", tx.Hash())
		}
		tipPerGas := new(big.Int).Sub(new(big.Int).Set(receipt.EffectiveGasPrice), header.BaseFee)
		if tipPerGas.Sign() < 0 {
			return nil, fmt.Errorf("transaction %s has negative priority fee %s", tx.Hash(), tipPerGas)
		}
		credit.Add(credit, tipPerGas.Mul(tipPerGas, new(big.Int).SetUint64(receipt.GasUsed)))
		if receipt.Status == types.ReceiptStatusSuccessful && tx.To() != nil && *tx.To() == recipient {
			credit.Add(credit, tx.Value())
		}
	}
	return credit, nil
}

func (s *systemCheck) loadOrRecordSignerAccountBaseline(ctx context.Context) ([2]managedAccountState, error) {
	baseline, recorded, err := s.recordedManagedAccountBaseline()
	if err != nil {
		return [2]managedAccountState{}, err
	}
	if recorded {
		return baseline, nil
	}
	baseline, err = s.waitManagedAccountBaseline(ctx)
	if err != nil {
		return [2]managedAccountState{}, fmt.Errorf("capture managed account state before Clef outage: %w", err)
	}
	if err := s.recordTypedSystemObservation(ctx, signerAccountBaselineObservation, managedAccountBaselineToEvidence(baseline)); err != nil {
		return [2]managedAccountState{}, err
	}
	return baseline, nil
}

func (s *systemCheck) assertSignerOutage(ctx context.Context) error {
	if s.hasAssertionMilestone(signerOutageAssertedObservation) {
		return nil
	}
	healthCtx, healthCancel := context.WithTimeout(ctx, 20*time.Second)
	healthErr := s.verifyExecutionRPCIndependentOfSigner(healthCtx, 0)
	healthCancel()
	if healthErr != nil {
		return fmt.Errorf("EL1 RPC was not healthy while Clef was stopped: %w", healthErr)
	}
	downCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	_, sendErr := s.sendManagedTransfer(downCtx, 0, TransactionLabelSignerOutageProbe)
	cancel()
	if sendErr == nil {
		return fmt.Errorf("qrl_sendTransaction unexpectedly succeeded while %s was stopped", s.cfg.signerSvc)
	}
	postFailureHealthCtx, postFailureHealthCancel := context.WithTimeout(ctx, 20*time.Second)
	postFailureHealthErr := s.verifyExecutionRPCIndependentOfSigner(postFailureHealthCtx, 0)
	postFailureHealthCancel()
	if postFailureHealthErr != nil {
		return fmt.Errorf("EL1 RPC was not healthy after the expected signing failure while Clef was stopped: %w", postFailureHealthErr)
	}
	if !isSignerUnavailableError(sendErr) {
		return fmt.Errorf("qrl_sendTransaction failed while EL1 was healthy, but not with a signing/Clef-specific error: %w", sendErr)
	}
	return s.recordAssertionMilestone(context.WithoutCancel(ctx), signerOutageAssertedObservation)
}

func (s *systemCheck) assertCanceledSignerRequestQuiescent(ctx context.Context, baseline [2]managedAccountState) error {
	milestone := s.hasAssertionMilestone(signerQuiescenceAssertedObservation)
	if _, submitted := s.recordedTransaction(TransactionLabelSignerRecoveryTransfer); submitted {
		if !milestone {
			return errors.New("signer recovery transaction is already submitted without a durable canceled-request quiescence assertion")
		}
		return nil
	}
	if !milestone {
		if err := s.waitCanceledTransferQuiescence(ctx, baseline); err != nil {
			return err
		}
		if err := s.recordAssertionMilestone(context.WithoutCancel(ctx), signerQuiescenceAssertedObservation); err != nil {
			return err
		}
		milestone = true
	}

	// A recovery RPC may have succeeded before its hash checkpoint write. Look
	// up only that exact immutable intent before comparing the live account
	// state, so a legitimate accepted recovery transfer can be recorded while a
	// distinct delayed outage request can never masquerade as it.
	if intent, exists := s.resume.managedIntents[TransactionLabelSignerRecoveryTransfer]; exists {
		to := s.cfg.recipient
		request := managedTransactionRequest{
			origin: 0, to: &to, value: new(big.Int).SetUint64(s.cfg.transferValue), input: []byte{}, accessList: nil,
		}
		if err := s.validateManagedTransactionIntent(ctx, intent, TransactionLabelSignerRecoveryTransfer, request); err != nil {
			return err
		}
		if _, attempted := s.resume.managedInitialAttempts[TransactionLabelSignerRecoveryTransfer]; attempted {
			hash, found, err := s.reconcileManagedTransaction(ctx, intent)
			if err != nil {
				return err
			}
			if found {
				if err := s.recordTransaction(context.WithoutCancel(ctx), TransactionLabelSignerRecoveryTransfer, hash); err != nil {
					return err
				}
				s.resume.transactions[TransactionLabelSignerRecoveryTransfer] = hash
				return nil
			}
		}
	}
	if !milestone {
		return errors.New("signer cancellation quiescence milestone was not persisted")
	}
	current, err := s.readManagedAccountStates(ctx)
	if err != nil {
		return fmt.Errorf("revalidate canceled Clef-outage request after resume: %w", err)
	}
	if err := validateManagedAccountUnchanged(baseline, current); err != nil {
		return fmt.Errorf("timed-out Clef-outage request caused a delayed transaction side effect after its quiescence checkpoint: %w", err)
	}
	return nil
}

func (s *systemCheck) restartSigner(ctx context.Context) (err error) {
	baseline, err := s.loadOrRecordSignerAccountBaseline(ctx)
	if err != nil {
		return err
	}
	stopped := false
	defer func() {
		if stopped {
			err = errors.Join(err, s.recoverService(s.cfg.signerSvc))
		}
	}()
	log.Printf("systemcheck: stopping %s to prove EL signing depends on the topology signer", s.cfg.signerSvc)
	if err := s.recordRestart(ctx, s.cfg.signerSvc, RestartStopIntent); err != nil {
		return err
	}
	// From the durable intent onward, a failed Stop response is ambiguous. The
	// status-aware safety defer restores the service whether Stop was applied or
	// not, and the next attempt starts a new append-only generation.
	stopped = true
	if err := s.k.Stop(ctx, s.cfg.signerSvc); err != nil {
		return err
	}
	if err := s.recordRestart(ctx, s.cfg.signerSvc, RestartStopped); err != nil {
		return err
	}

	if err := s.assertSignerOutage(ctx); err != nil {
		return err
	}
	if err := s.recordRestart(ctx, s.cfg.signerSvc, RestartStartIntent); err != nil {
		return err
	}
	if err := s.k.Start(ctx, s.cfg.signerSvc); err != nil {
		return err
	}
	if err := s.recordRestart(ctx, s.cfg.signerSvc, RestartStarted); err != nil {
		return err
	}
	if err := s.waitSignerReady(ctx); err != nil {
		return fmt.Errorf("Clef did not recover after restart: %w", err)
	}
	if err := s.recordRestart(ctx, s.cfg.signerSvc, RestartHealthy); err != nil {
		return err
	}
	if err := s.verifyManagedAccounts(ctx); err != nil {
		return fmt.Errorf("execution account manager did not recover after Clef restart: %w", err)
	}
	if err := s.assertCanceledSignerRequestQuiescent(ctx, baseline); err != nil {
		return err
	}
	hash, err := s.sendManagedTransfer(ctx, 0, TransactionLabelSignerRecoveryTransfer)
	if err != nil {
		return fmt.Errorf("transaction after Clef restart: %w", err)
	}
	if _, err := s.verifyTransferOnBoth(ctx, hash); err != nil {
		return fmt.Errorf("verify transaction after Clef restart: %w", err)
	}
	stopped = false
	log.Printf("PASS: stopping Clef blocked managed signing without a delayed transaction side effect, and restarting it restored a cross-node confirmed transaction %s", hash)
	return nil
}

// resumeSigner continues from the last completed durable mutation. Ambiguous
// intent-only checkpoints are rejected by validateResumeState before this
// method is reached.
func (s *systemCheck) resumeSigner(ctx context.Context) (err error) {
	state, ok := s.restartState(s.cfg.signerSvc)
	if !ok {
		return fmt.Errorf("signer resume requested without restart history")
	}
	// Any resumed signer generation has crossed a durable mutation boundary.
	// Keep the safety defer armed until every resumed assertion succeeds; its
	// status-aware recovery is a no-op when the signer is already running.
	stopped := true
	defer func() {
		if stopped {
			err = errors.Join(err, s.recoverService(s.cfg.signerSvc))
		}
	}()
	baseline, recorded, err := s.recordedManagedAccountBaseline()
	if err != nil {
		return err
	}
	if !recorded {
		return errors.New("cannot resume signer fault cycle without its durable pre-outage account baseline")
	}
	if state == RestartStopIntent {
		if err := s.resolveStopIntent(ctx, s.cfg.signerSvc); err != nil {
			return err
		}
		state = RestartStopped
	}
	if state == RestartEmergencyStartIntent {
		if err := s.resolveEmergencyStartIntent(ctx, s.cfg.signerSvc); err != nil {
			return err
		}
		state = RestartEmergencyStarted
	}
	if state == RestartStartIntent {
		if err := s.resolveStartIntent(ctx, s.cfg.signerSvc); err != nil {
			return err
		}
		state = RestartStarted
	}
	if state == RestartEmergencyStarted {
		if !s.hasAssertionMilestone(signerOutageAssertedObservation) {
			log.Printf("systemcheck: signer safety recovery preceded the durable outage assertion; beginning a new fault-cycle generation")
			stopped, err = s.reenterFaultAfterEmergency(ctx, s.cfg.signerSvc)
			if err != nil {
				return err
			}
			state = RestartStopped
		} else {
			state = RestartStarted
		}
	}
	if state == RestartStopped {
		if err := s.assertSignerOutage(ctx); err != nil {
			return fmt.Errorf("resumed Clef-outage assertion: %w", err)
		}
		if err := s.recordRestart(ctx, s.cfg.signerSvc, RestartStartIntent); err != nil {
			return err
		}
		if err := s.k.Start(ctx, s.cfg.signerSvc); err != nil {
			return err
		}
		if err := s.recordRestart(ctx, s.cfg.signerSvc, RestartStarted); err != nil {
			return err
		}
		state = RestartStarted
	}
	if state == RestartStarted || state == RestartHealthy {
		recovered, err := s.ensureServiceRunning(ctx, s.cfg.signerSvc)
		if err != nil {
			return err
		}
		if recovered {
			state = RestartStarted
		}
	}
	if state == RestartStarted {
		if err := s.waitSignerReady(ctx); err != nil {
			return fmt.Errorf("Clef did not recover while resuming restart: %w", err)
		}
		if err := s.recordRestart(ctx, s.cfg.signerSvc, RestartHealthy); err != nil {
			return err
		}
		state = RestartHealthy
	}
	if state != RestartHealthy {
		return fmt.Errorf("signer resume reached unsupported durable state %q", state)
	}
	if err := s.verifyManagedAccounts(ctx); err != nil {
		return fmt.Errorf("execution account manager is not healthy after resumed Clef restart: %w", err)
	}
	if err := s.assertCanceledSignerRequestQuiescent(ctx, baseline); err != nil {
		return err
	}
	hash, err := s.sendManagedTransfer(ctx, 0, TransactionLabelSignerRecoveryTransfer)
	if err != nil {
		return fmt.Errorf("transaction after resumed Clef restart: %w", err)
	}
	if _, err := s.verifyTransferOnBoth(ctx, hash); err != nil {
		return fmt.Errorf("verify transaction after resumed Clef restart: %w", err)
	}
	baselineSystem, err := s.establishSystemBaseline(ctx)
	if err != nil {
		return fmt.Errorf("system baseline after resumed Clef restart: %w", err)
	}
	if err := s.checkValidatorContinuityIfDue(ctx, baselineSystem.validatorDuties, true, 0, 1); err != nil {
		return err
	}
	stopped = false
	return nil
}

// verifyExecutionRPCIndependentOfSigner checks only RPCs that are served by the
// execution node itself. In particular, it must not call qrl_accounts or any
// signing method: those requests can reach the external signer that this fault
// injection deliberately stopped.
func (s *systemCheck) verifyExecutionRPCIndependentOfSigner(ctx context.Context, index int) error {
	if index < 0 || index >= len(s.clients) || s.clients[index] == nil {
		return fmt.Errorf("invalid execution client EL%d", index+1)
	}
	client := s.clients[index]
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return fmt.Errorf("chain ID: %w", err)
	}
	if chainID.Sign() <= 0 {
		return fmt.Errorf("chain ID is not positive: %s", chainID)
	}
	networkID, err := client.NetworkID(ctx)
	if err != nil {
		return fmt.Errorf("network ID: %w", err)
	}
	if networkID.Sign() <= 0 {
		return fmt.Errorf("network ID is not positive: %s", networkID)
	}
	block, err := client.BlockNumber(ctx)
	if err != nil {
		return fmt.Errorf("block number: %w", err)
	}
	if block == 0 {
		return fmt.Errorf("execution node has not imported a post-genesis block")
	}
	return nil
}

func isSignerUnavailableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := strings.ToLower(err.Error())
	markers := []string{
		"account_signtransaction",
		"external signer",
		"clef",
		"signer",
		"connection refused",
		"dial tcp",
		"broken pipe",
		"deadline exceeded",
		"request timed out",
	}
	marked := false
	for _, marker := range markers {
		if strings.Contains(message, marker) {
			marked = true
			break
		}
	}
	if !marked {
		return false
	}
	var rpcErr rpc.Error
	if !errors.As(err, &rpcErr) {
		// A client-side deadline can be returned directly instead of being
		// serialized by the execution RPC server.
		return strings.Contains(message, "deadline exceeded") || strings.Contains(message, "request timed out")
	}
	switch rpcErr.ErrorCode() {
	case -32000, -32002, -32603:
		return true
	default:
		return false
	}
}
