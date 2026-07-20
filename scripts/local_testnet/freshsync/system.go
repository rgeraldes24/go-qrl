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

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"time"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/rpc"
)

type executionIdentity struct {
	chainID   *big.Int
	networkID *big.Int
}

type adminNodeInfo struct {
	IP    string `json:"ip"`
	Qnode string `json:"qnode"`
}

type freshSyncCheck struct {
	cfg config
	k   kurtosis

	reference *qrlclient.Client
	fresh     *qrlclient.Client
	clients   [2]*qrlclient.Client
	http      httpReader

	addedServices []string
	freshCLURL    string
	depositTarget depositTargetState
}

func runFreshSync(ctx context.Context, cfg config, runner commandRunner) (err error) {
	runCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	check := &freshSyncCheck{
		cfg: cfg,
		k:   kurtosis{enclave: cfg.enclave, runner: runner},
		http: httpReader{
			client: &http.Client{Timeout: 15 * time.Second},
		},
	}
	defer func() {
		if check.fresh != nil {
			check.fresh.Close()
		}
		if check.reference != nil {
			check.reference.Close()
		}
		shouldCleanup := (err == nil && !cfg.keepServices) || (err != nil && cfg.cleanupOnFailure)
		if !shouldCleanup || len(check.addedServices) == 0 {
			if len(check.addedServices) != 0 {
				log.Printf("freshsync: preserving temporary services for diagnostics: %v", check.addedServices)
			}
			return
		}
		// Cleanup must outlive cancellation of the whole-run context so a
		// timeout can still remove temporary services when requested.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		if cleanupErr := check.cleanup(cleanupCtx); cleanupErr != nil {
			err = errors.Join(err, cleanupErr)
		}
	}()
	return check.run(runCtx)
}

func (s *freshSyncCheck) run(ctx context.Context) error {
	if s.cfg.referenceRPC == "" {
		endpoint, err := s.k.endpoint(ctx, s.cfg.referenceService, "rpc", "http")
		if err != nil {
			return fmt.Errorf("resolve reference RPC: %w", err)
		}
		s.cfg.referenceRPC = endpoint
	}
	reference, err := qrlclient.DialContext(ctx, s.cfg.referenceRPC)
	if err != nil {
		return fmt.Errorf("dial reference execution RPC %s: %w", s.cfg.referenceRPC, err)
	}
	s.reference = reference
	s.clients[0] = reference

	identity, err := s.waitReference(ctx)
	if err != nil {
		return err
	}
	target, err := s.reference.HeaderByNumber(ctx, big.NewInt(int64(rpc.FinalizedBlockNumber)))
	if err != nil {
		return fmt.Errorf("capture reference finalized header: %w", err)
	}
	if target.Number == nil || target.Number.Sign() <= 0 {
		return fmt.Errorf("reference finalized head remains at genesis")
	}
	s.depositTarget, err = readAndVerifyDepositTarget(ctx, s.reference, s.cfg.depositContract, target)
	if err != nil {
		return fmt.Errorf("capture reference finalized deposit state: %w", err)
	}
	log.Printf("freshsync: captured deposit contract=%s slot=%s value=0x%x count=%d root=0x%x with verified account/storage proofs and VM calls", s.cfg.depositContract.Hex(), s.depositTarget.slot.Hex(), s.depositTarget.value, s.depositTarget.count, s.depositTarget.root)
	log.Printf("freshsync: reference=%s finalized target=%d/%s mode=%s", s.cfg.referenceRPC, target.Number.Uint64(), target.Hash(), s.cfg.syncMode)

	elConfig, err := s.k.inspect(ctx, s.cfg.elTemplateService)
	if err != nil {
		return fmt.Errorf("inspect execution template: %w", err)
	}
	if err := mutateExecutionConfig(elConfig, s.cfg.syncMode); err != nil {
		return fmt.Errorf("prepare empty execution clone: %w", err)
	}
	if err := s.k.add(ctx, s.cfg.freshELService, elConfig); err != nil {
		return fmt.Errorf("add fresh execution service: %w", err)
	}
	s.addedServices = append(s.addedServices, s.cfg.freshELService)

	freshIP, err := s.waitFreshExecutionRPC(ctx)
	if err != nil {
		return err
	}
	engineEndpoint, err := engineURL(freshIP, 8551)
	if err != nil {
		return err
	}
	clConfig, err := s.k.inspect(ctx, s.cfg.clTemplateService)
	if err != nil {
		return fmt.Errorf("inspect beacon template: %w", err)
	}
	if err := mutateBeaconConfig(clConfig, engineEndpoint); err != nil {
		return fmt.Errorf("prepare beacon sync driver: %w", err)
	}
	if err := s.k.add(ctx, s.cfg.freshCLService, clConfig); err != nil {
		return fmt.Errorf("add fresh beacon service: %w", err)
	}
	s.addedServices = append(s.addedServices, s.cfg.freshCLService)
	if err := s.waitFreshCLPort(ctx); err != nil {
		return err
	}

	if err := s.waitExecutionCatchup(ctx, identity, target); err != nil {
		return err
	}
	if err := s.verifySyncModeEvidence(ctx); err != nil {
		return err
	}
	if err := s.waitBeaconHealthy(ctx); err != nil {
		return err
	}
	if err := s.verifyManagedAccounts(ctx); err != nil {
		return err
	}
	log.Printf("PASS: %s started only after its fail-closed empty-datadir guard and %s-synced through finalized block %d with matching VM64 state", s.cfg.freshELService, s.cfg.syncMode, target.Number.Uint64())

	hash, err := s.sendManagedTransfer(ctx)
	if err != nil {
		return fmt.Errorf("submit post-catch-up VM64 transfer: %w", err)
	}
	if err := s.verifyTransfer(ctx, hash); err != nil {
		return err
	}
	log.Printf("PASS: fresh execution node imported topology-Clef transaction %s and agreed on its 64-byte sender, receipt, header, state root, balances, and nonce transition", hash)
	return nil
}

func (s *freshSyncCheck) verifySyncModeEvidence(ctx context.Context) error {
	logs, err := s.k.logs(ctx, s.cfg.freshELService)
	if err != nil {
		return fmt.Errorf("read fresh execution logs for %s evidence: %w", s.cfg.syncMode, err)
	}
	const (
		snapCycle = "Starting snapshot sync cycle"
		snapPivot = "Committing snap sync pivot as new head"
	)
	if s.cfg.syncMode == "snap" {
		if !strings.Contains(logs, snapCycle) || !strings.Contains(logs, snapPivot) {
			return fmt.Errorf("snap sync completed without required state-download evidence: cycle=%t pivot=%t", strings.Contains(logs, snapCycle), strings.Contains(logs, snapPivot))
		}
		log.Printf("PASS: fresh execution logs prove a snapshot state-download cycle and committed snap pivot")
		return nil
	}
	for _, marker := range []string{snapCycle, snapPivot, "Enabled snap sync", "Switch sync mode from full sync to snap sync"} {
		if strings.Contains(logs, marker) {
			return fmt.Errorf("full sync unexpectedly used snap path marker %q", marker)
		}
	}
	const fullDownload = "Block synchronisation started"
	if !strings.Contains(logs, fullDownload) {
		return fmt.Errorf("full sync completed without positive block-downloader evidence %q", fullDownload)
	}
	log.Printf("PASS: fresh execution logs prove the block downloader started and contain no snap-sync activation, cycle, or pivot markers")
	return nil
}

func (s *freshSyncCheck) waitReference(ctx context.Context) (executionIdentity, error) {
	var identity executionIdentity
	err := waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, "reference execution node to become healthy", func(ctx context.Context) (bool, error) {
		chainID, err := s.reference.ChainID(ctx)
		if err != nil {
			return false, err
		}
		networkID, err := s.reference.NetworkID(ctx)
		if err != nil {
			return false, err
		}
		block, err := s.reference.BlockNumber(ctx)
		if err != nil {
			return false, err
		}
		if block == 0 {
			return false, fmt.Errorf("reference remains at genesis")
		}
		peers, err := s.reference.PeerCount(ctx)
		if err != nil {
			return false, err
		}
		if peers == 0 {
			return false, fmt.Errorf("reference has no execution peers")
		}
		progress, err := s.reference.SyncProgress(ctx)
		if err != nil {
			return false, err
		}
		if progress != nil {
			return false, fmt.Errorf("reference is syncing at %+v", progress)
		}
		identity = executionIdentity{chainID: chainID, networkID: networkID}
		return true, nil
	})
	return identity, err
}

func (s *freshSyncCheck) waitFreshExecutionRPC(ctx context.Context) (string, error) {
	startupTimeout := s.cfg.timeout
	if startupTimeout > 5*time.Minute {
		startupTimeout = 5 * time.Minute
	}
	var privateIP string
	err := waitFor(ctx, startupTimeout, s.cfg.pollInterval, "fresh execution RPC and private-IP substitution", func(ctx context.Context) (bool, error) {
		endpoint, err := s.k.endpoint(ctx, s.cfg.freshELService, "rpc", "http")
		if err != nil {
			return false, err
		}
		client, err := qrlclient.DialContext(ctx, endpoint)
		if err != nil {
			return false, err
		}
		var info adminNodeInfo
		if err := client.Client().CallContext(ctx, &info, "admin_nodeInfo"); err != nil {
			client.Close()
			return false, err
		}
		if _, err := engineURL(info.IP, 8551); err != nil {
			client.Close()
			return false, err
		}
		if info.Qnode == "" {
			client.Close()
			return false, fmt.Errorf("admin_nodeInfo returned an empty qnode")
		}
		s.fresh = client
		s.clients[1] = client
		privateIP = info.IP
		log.Printf("freshsync: fresh EL RPC=%s private-ip=%s", endpoint, info.IP)
		return true, nil
	})
	return privateIP, err
}

func (s *freshSyncCheck) waitFreshCLPort(ctx context.Context) error {
	startupTimeout := s.cfg.timeout
	if startupTimeout > 5*time.Minute {
		startupTimeout = 5 * time.Minute
	}
	return waitFor(ctx, startupTimeout, s.cfg.pollInterval, "fresh beacon HTTP endpoint", func(ctx context.Context) (bool, error) {
		endpoint, err := s.k.endpoint(ctx, s.cfg.freshCLService, "http", "http")
		if err != nil {
			return false, err
		}
		s.freshCLURL = endpoint
		return true, nil
	})
}

func (s *freshSyncCheck) waitExecutionCatchup(ctx context.Context, identity executionIdentity, target *types.Header) error {
	return waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, fmt.Sprintf("fresh execution node to catch finalized block %d", target.Number.Uint64()), func(ctx context.Context) (bool, error) {
		chainID, err := s.fresh.ChainID(ctx)
		if err != nil {
			return false, err
		}
		if chainID.Cmp(identity.chainID) != 0 {
			return false, fmt.Errorf("fresh chain ID %s differs from reference %s", chainID, identity.chainID)
		}
		networkID, err := s.fresh.NetworkID(ctx)
		if err != nil {
			return false, err
		}
		if networkID.Cmp(identity.networkID) != 0 {
			return false, fmt.Errorf("fresh network ID %s differs from reference %s", networkID, identity.networkID)
		}
		peers, err := s.fresh.PeerCount(ctx)
		if err != nil {
			return false, err
		}
		if peers == 0 {
			return false, fmt.Errorf("fresh execution node has no peers")
		}
		header, err := s.fresh.HeaderByNumber(ctx, target.Number)
		if err != nil {
			return false, err
		}
		if header.Hash() != target.Hash() || header.Root != target.Root || header.ReceiptHash != target.ReceiptHash {
			return false, fmt.Errorf("fresh finalized target differs: got %s/%s/%s want %s/%s/%s", header.Hash(), header.Root, header.ReceiptHash, target.Hash(), target.Root, target.ReceiptHash)
		}
		progress, err := s.fresh.SyncProgress(ctx)
		if err != nil {
			return false, err
		}
		if progress != nil {
			return false, fmt.Errorf("fresh execution node still reports sync progress %+v", progress)
		}
		refBalance, err := s.reference.BalanceAt(ctx, s.cfg.recipient, target.Number)
		if err != nil {
			return false, fmt.Errorf("reference recipient balance at target: %w", err)
		}
		freshBalance, err := s.fresh.BalanceAt(ctx, s.cfg.recipient, target.Number)
		if err != nil {
			return false, fmt.Errorf("fresh recipient balance at target: %w", err)
		}
		if refBalance.Cmp(freshBalance) != 0 {
			return false, fmt.Errorf("recipient VM64 state differs at target: %s != %s", refBalance, freshBalance)
		}
		refNonce, err := s.reference.NonceAt(ctx, s.cfg.signerAddress, target.Number)
		if err != nil {
			return false, fmt.Errorf("reference signer nonce at target: %w", err)
		}
		freshNonce, err := s.fresh.NonceAt(ctx, s.cfg.signerAddress, target.Number)
		if err != nil {
			return false, fmt.Errorf("fresh signer nonce at target: %w", err)
		}
		if refNonce != freshNonce {
			return false, fmt.Errorf("signer VM64 nonce differs at target: %d != %d", refNonce, freshNonce)
		}
		freshDeposit, err := readAndVerifyDepositTarget(ctx, s.fresh, s.cfg.depositContract, target)
		if err != nil {
			return false, fmt.Errorf("fresh finalized deposit state: %w", err)
		}
		if err := compareDepositTarget(freshDeposit, s.depositTarget); err != nil {
			return false, err
		}
		return true, nil
	})
}

func (s *freshSyncCheck) waitBeaconHealthy(ctx context.Context) error {
	return waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, "fresh beacon node to sync and keep its execution client online", func(ctx context.Context) (bool, error) {
		status, err := s.http.beaconStatus(ctx, s.freshCLURL)
		if err != nil {
			return false, err
		}
		log.Printf("freshsync: fresh CL healthy at slot %d with %d peers", status.headSlot, status.connectedPeers)
		return true, nil
	})
}

func (s *freshSyncCheck) verifyManagedAccounts(ctx context.Context) error {
	if len(s.cfg.signerAddress.Bytes()) != common.AddressLength {
		return fmt.Errorf("signer address width is %d, want %d", len(s.cfg.signerAddress.Bytes()), common.AddressLength)
	}
	for i, client := range s.clients {
		var accounts []common.Address
		if err := client.Client().CallContext(ctx, &accounts, "qrl_accounts"); err != nil {
			return fmt.Errorf("EL%d qrl_accounts: %w", i+1, err)
		}
		if !containsAddress(accounts, s.cfg.signerAddress) {
			return fmt.Errorf("EL%d does not expose topology signer %s", i+1, s.cfg.signerAddress)
		}
	}
	return nil
}

func containsAddress(accounts []common.Address, want common.Address) bool {
	for _, account := range accounts {
		if account == want {
			return true
		}
	}
	return false
}

func (s *freshSyncCheck) sendManagedTransfer(ctx context.Context) (common.Hash, error) {
	value := new(big.Int).SetUint64(s.cfg.transferValue)
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
	if err := s.reference.Client().CallContext(ctx, &hash, "qrl_sendTransaction", args); err != nil {
		return common.Hash{}, err
	}
	if hash == (common.Hash{}) {
		return common.Hash{}, fmt.Errorf("qrl_sendTransaction returned a zero hash")
	}
	return hash, nil
}

func (s *freshSyncCheck) waitReceipt(ctx context.Context, index int, hash common.Hash) (*types.Receipt, error) {
	var receipt *types.Receipt
	err := waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, fmt.Sprintf("EL%d receipt %s", index+1, hash), func(ctx context.Context) (bool, error) {
		got, err := s.clients[index].TransactionReceipt(ctx, hash)
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

func (s *freshSyncCheck) verifyTransfer(ctx context.Context, hash common.Hash) error {
	receiptA, err := s.waitReceipt(ctx, 0, hash)
	if err != nil {
		return err
	}
	receiptB, err := s.waitReceipt(ctx, 1, hash)
	if err != nil {
		return err
	}
	if receiptA.Status != types.ReceiptStatusSuccessful || receiptB.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("transaction %s failed: reference status=%d fresh status=%d", hash, receiptA.Status, receiptB.Status)
	}
	if receiptA.BlockNumber == nil || receiptB.BlockNumber == nil || receiptA.BlockNumber.Sign() <= 0 {
		return fmt.Errorf("transaction %s has invalid inclusion block", hash)
	}
	if receiptA.BlockNumber.Cmp(receiptB.BlockNumber) != 0 || receiptA.BlockHash != receiptB.BlockHash {
		return fmt.Errorf("receipt inclusion differs: reference=%s/%s fresh=%s/%s", receiptA.BlockNumber, receiptA.BlockHash, receiptB.BlockNumber, receiptB.BlockHash)
	}

	var signed *types.Transaction
	for i, client := range s.clients {
		tx, pending, err := client.TransactionByHash(ctx, hash)
		if err != nil {
			return fmt.Errorf("EL%d transaction %s: %w", i+1, hash, err)
		}
		if pending {
			return fmt.Errorf("EL%d still reports transaction %s as pending", i+1, hash)
		}
		if tx.To() == nil || *tx.To() != s.cfg.recipient {
			return fmt.Errorf("EL%d transaction recipient mismatch", i+1)
		}
		if tx.Value().Cmp(new(big.Int).SetUint64(s.cfg.transferValue)) != 0 {
			return fmt.Errorf("EL%d transaction value mismatch: %s", i+1, tx.Value())
		}
		sender, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
		if err != nil {
			return fmt.Errorf("EL%d recover transaction sender: %w", i+1, err)
		}
		if sender != s.cfg.signerAddress || len(sender.Bytes()) != common.AddressLength {
			return fmt.Errorf("EL%d recovered sender %s with width %d, want %s with width %d", i+1, sender, len(sender.Bytes()), s.cfg.signerAddress, common.AddressLength)
		}
		signed = tx
	}

	blockNumber := new(big.Int).Set(receiptA.BlockNumber)
	previous := new(big.Int).Sub(new(big.Int).Set(blockNumber), big.NewInt(1))
	var headers [2]*types.Header
	var balances [2]*big.Int
	for i, client := range s.clients {
		header, err := client.HeaderByNumber(ctx, blockNumber)
		if err != nil {
			return fmt.Errorf("EL%d inclusion header: %w", i+1, err)
		}
		headers[i] = header
		before, err := client.BalanceAt(ctx, s.cfg.recipient, previous)
		if err != nil {
			return fmt.Errorf("EL%d recipient balance before transfer: %w", i+1, err)
		}
		after, err := client.BalanceAt(ctx, s.cfg.recipient, blockNumber)
		if err != nil {
			return fmt.Errorf("EL%d recipient balance after transfer: %w", i+1, err)
		}
		balances[i] = after
		delta := new(big.Int).Sub(new(big.Int).Set(after), before)
		if delta.Cmp(new(big.Int).SetUint64(s.cfg.transferValue)) != 0 {
			return fmt.Errorf("EL%d recipient balance delta is %s, want %d", i+1, delta, s.cfg.transferValue)
		}
		nonceBefore, err := client.NonceAt(ctx, s.cfg.signerAddress, previous)
		if err != nil {
			return fmt.Errorf("EL%d signer nonce before transfer: %w", i+1, err)
		}
		nonceAfter, err := client.NonceAt(ctx, s.cfg.signerAddress, blockNumber)
		if err != nil {
			return fmt.Errorf("EL%d signer nonce after transfer: %w", i+1, err)
		}
		if nonceBefore != signed.Nonce() || nonceAfter != signed.Nonce()+1 {
			return fmt.Errorf("EL%d signer nonce transition is %d -> %d for tx nonce %d", i+1, nonceBefore, nonceAfter, signed.Nonce())
		}
	}
	if headers[0].Hash() != headers[1].Hash() || headers[0].Root != headers[1].Root || headers[0].ReceiptHash != headers[1].ReceiptHash {
		return fmt.Errorf("reference and fresh nodes disagree on inclusion header/state/receipt roots")
	}
	if balances[0].Cmp(balances[1]) != 0 {
		return fmt.Errorf("reference and fresh recipient balances differ after transfer: %s != %s", balances[0], balances[1])
	}
	return nil
}

func (s *freshSyncCheck) cleanup(ctx context.Context) error {
	var errs []error
	for i := len(s.addedServices) - 1; i >= 0; i-- {
		service := s.addedServices[i]
		if err := s.k.remove(ctx, service); err != nil {
			errs = append(errs, fmt.Errorf("remove temporary service %s: %w", service, err))
		}
	}
	return errors.Join(errs...)
}

func waitFor(ctx context.Context, timeout, poll time.Duration, description string, condition func(context.Context) (bool, error)) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	var lastErr error
	for {
		ok, err := condition(ctx)
		if ok && err == nil {
			return nil
		}
		if err != nil {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("wait for %s: %w", description, ctx.Err())
		case <-deadline.C:
			if lastErr == nil {
				lastErr = fmt.Errorf("condition remained false")
			}
			return fmt.Errorf("wait for %s after %s: %w", description, timeout, lastErr)
		case <-ticker.C:
		}
	}
}
