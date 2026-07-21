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
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/qrlclient/gqrlclient"
	"github.com/theQRL/go-qrl/rpc"
	"github.com/theQRL/go-qrl/trie"
)

type systemCheck struct {
	cfg                          config
	k                            kurtosis
	http                         httpReader
	clients                      [2]*qrlclient.Client
	lastValidatorContinuityCheck time.Time
}

const (
	validatorMetricsReachabilityTimeout = 30 * time.Second
	// With 5-second slots and 64 of 128 validators on each VC, an aggregate
	// attestation counter should advance well inside this bounded observation.
	validatorDutyObservationTimeout = 2 * time.Minute
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

func newSystemCheck(ctx context.Context, cfg config, runner commandRunner) (*systemCheck, error) {
	k := kurtosis{enclave: cfg.enclave, runner: runner}
	if err := cfg.resolveEndpoints(ctx, k); err != nil {
		return nil, err
	}
	check := &systemCheck{
		cfg:  cfg,
		k:    k,
		http: httpReader{client: &http.Client{Timeout: 15 * time.Second}},
	}
	for i, rawURL := range cfg.rpcURLs {
		client, err := qrlclient.DialContext(ctx, rawURL)
		if err != nil {
			check.close()
			return nil, fmt.Errorf("dial execution RPC %s: %w", rawURL, err)
		}
		check.clients[i] = client
	}
	return check, nil
}

func (s *systemCheck) close() {
	for i, client := range s.clients {
		if client != nil {
			client.Close()
			s.clients[i] = nil
		}
	}
}

func (s *systemCheck) run(ctx context.Context) error {
	log.Printf("systemcheck: EL endpoints: %s, %s", s.cfg.rpcURLs[0], s.cfg.rpcURLs[1])
	log.Printf("systemcheck: CL endpoints: %s, %s", s.cfg.clURLs[0], s.cfg.clURLs[1])
	log.Printf("systemcheck: topology Clef endpoint: %s", s.cfg.signerURL)

	if err := s.waitExecutionHealthy(ctx); err != nil {
		return err
	}
	if err := s.waitSignerReady(ctx); err != nil {
		return err
	}
	if err := s.verifyManagedAccounts(ctx); err != nil {
		return err
	}
	if err := s.verifyBeaconSpecs(ctx); err != nil {
		return err
	}
	initialValidatorDuties, err := s.waitInitialValidatorActivity(ctx)
	if err != nil {
		return err
	}
	logValidatorDutySnapshots("initial validator-duty baseline", initialValidatorDuties)
	if !s.cfg.requireZeroDutyHistory {
		for i, snapshot := range initialValidatorDuties {
			if snapshot.failedAttestations > 0 || snapshot.failedProposals > 0 {
				log.Printf("systemcheck: VC%d baselined pre-existing process-cumulative duty failures (attestations=%.0f proposals=%.0f); any counter increase during this check remains fatal",
					i+1, snapshot.failedAttestations, snapshot.failedProposals)
			}
		}
	}
	initialFinality, err := s.waitBeaconConvergence(ctx, 1, 0, initialValidatorDuties)
	if err != nil {
		return err
	}
	if _, err := s.waitExecutionFinality(ctx, initialValidatorDuties, 1, nil); err != nil {
		return err
	}
	if err := s.checkValidatorContinuityIfDue(ctx, initialValidatorDuties, true, 0, 1); err != nil {
		return err
	}
	log.Printf("PASS: both EL/CL/VC pairs are peered, healthy, active, and finalized at beacon epoch %d", initialFinality[0].finalizedEpoch)
	if err := s.waitAutomaticWithdrawal(ctx, initialValidatorDuties); err != nil {
		return fmt.Errorf("automatic VM64 withdrawal gate: %w", err)
	}

	txHash, err := s.sendManagedTransfer(ctx, 0)
	if err != nil {
		return fmt.Errorf("submit EL1-origin topology-Clef transaction: %w", err)
	}
	if _, err := s.verifyTransferOnBoth(ctx, txHash); err != nil {
		return err
	}
	log.Printf("PASS: topology Clef signed EL1-origin transaction %s and both execution nodes agreed on its sender, receipt, header, state transition, and fee-recipient reward", txHash)

	el2Hash, err := s.sendManagedTransfer(ctx, 1)
	if err != nil {
		return fmt.Errorf("submit EL2-origin topology-Clef transaction: %w", err)
	}
	if _, err := s.verifyTransferOnBoth(ctx, el2Hash); err != nil {
		return fmt.Errorf("verify EL2-origin topology-Clef transaction: %w", err)
	}
	log.Printf("PASS: EL2 originated managed transaction %s through topology Clef and both execution nodes agreed on its full VM64 transition", el2Hash)

	accessListHash, err := s.checkManagedAccessListTransaction(ctx)
	if err != nil {
		return fmt.Errorf("topology-Clef non-empty access-list transaction: %w", err)
	}
	log.Printf("PASS: topology Clef signed non-empty access-list transaction %s and both execution nodes agreed on its exact 64-byte contract address, storage key, receipt, and VM64 state transition", accessListHash)

	preFaultValidatorDuties, err := s.waitValidatorProgress(ctx, initialValidatorDuties, "pre-fault validator duty progress")
	if err != nil {
		return fmt.Errorf("validator health before fault injection: %w", err)
	}
	logValidatorDutySnapshots("pre-fault validator-duty snapshot", preFaultValidatorDuties)

	if s.cfg.skipRestarts {
		log.Printf("systemcheck: restart checks skipped by request")
		return nil
	}
	if err := s.restartSigner(ctx); err != nil {
		return err
	}
	if err := s.restartSecondParticipant(ctx, initialFinality[0].finalizedEpoch, preFaultValidatorDuties); err != nil {
		return err
	}
	return nil
}

func (s *systemCheck) verifyBeaconSpecs(ctx context.Context) error {
	var specs [2]map[string]string
	for i, rawURL := range s.cfg.clURLs {
		spec, err := s.http.beaconSpec(ctx, rawURL)
		if err != nil {
			return fmt.Errorf("CL%d beacon spec: %w", i+1, err)
		}
		specs[i] = spec
	}
	if err := validateBeaconSpecs(specs); err != nil {
		return err
	}
	log.Printf("PASS: both beacon nodes report identical mainnet VM64 test timing (5-second slots, 128-slot epochs)")
	return nil
}

func (s *systemCheck) waitExecutionHealthy(ctx context.Context) error {
	return waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, "execution nodes to become healthy and peered", func(ctx context.Context) (bool, error) {
		var chainIDs [2]*big.Int
		var networkIDs [2]*big.Int
		for i, client := range s.clients {
			chainID, err := client.ChainID(ctx)
			if err != nil {
				return false, fmt.Errorf("EL%d chain ID: %w", i+1, err)
			}
			networkID, err := client.NetworkID(ctx)
			if err != nil {
				return false, fmt.Errorf("EL%d network ID: %w", i+1, err)
			}
			block, err := client.BlockNumber(ctx)
			if err != nil {
				return false, fmt.Errorf("EL%d block number: %w", i+1, err)
			}
			if block == 0 {
				return false, fmt.Errorf("EL%d has not imported a post-genesis block", i+1)
			}
			peers, err := client.PeerCount(ctx)
			if err != nil {
				return false, fmt.Errorf("EL%d peer count: %w", i+1, err)
			}
			if peers == 0 {
				return false, fmt.Errorf("EL%d has no execution peers", i+1)
			}
			progress, err := client.SyncProgress(ctx)
			if err != nil {
				return false, fmt.Errorf("EL%d sync progress: %w", i+1, err)
			}
			if progress != nil {
				return false, fmt.Errorf("EL%d is still syncing at %+v", i+1, progress)
			}
			chainIDs[i], networkIDs[i] = chainID, networkID
		}
		if chainIDs[0].Cmp(chainIDs[1]) != 0 {
			return false, fmt.Errorf("execution chain IDs differ: %s != %s", chainIDs[0], chainIDs[1])
		}
		if networkIDs[0].Cmp(networkIDs[1]) != 0 {
			return false, fmt.Errorf("execution network IDs differ: %s != %s", networkIDs[0], networkIDs[1])
		}
		return true, nil
	})
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
			s.cfg.signerURL = endpoint
		}
		return true, nil
	})
}

func (s *systemCheck) verifyManagedAccounts(ctx context.Context) error {
	if len(s.cfg.signerAddress.Bytes()) != common.AddressLength {
		return fmt.Errorf("configured signer address has width %d, want %d", len(s.cfg.signerAddress.Bytes()), common.AddressLength)
	}
	for i, client := range s.clients {
		var accounts []common.Address
		if err := client.Client().CallContext(ctx, &accounts, "qrl_accounts"); err != nil {
			return fmt.Errorf("EL%d qrl_accounts: %w", i+1, err)
		}
		if !containsAddress(accounts, s.cfg.signerAddress) {
			return fmt.Errorf("EL%d is not connected to topology Clef account %s", i+1, s.cfg.signerAddress)
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

func (s *systemCheck) sendManagedTransfer(ctx context.Context, origin int) (common.Hash, error) {
	if origin < 0 || origin >= len(s.clients) || s.clients[origin] == nil {
		return common.Hash{}, fmt.Errorf("invalid execution origin EL%d", origin+1)
	}
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
	if err := s.clients[origin].Client().CallContext(ctx, &hash, "qrl_sendTransaction", args); err != nil {
		return common.Hash{}, err
	}
	if hash == (common.Hash{}) {
		return common.Hash{}, fmt.Errorf("qrl_sendTransaction returned a zero hash")
	}
	return hash, nil
}

func (s *systemCheck) checkManagedAccessListTransaction(ctx context.Context) (common.Hash, error) {
	creationCode, runtimeCode := managedAccessListContractCode()
	deployHash, err := s.sendManagedContractTransaction(ctx, 0, nil, creationCode, nil)
	if err != nil {
		return common.Hash{}, fmt.Errorf("deploy access-list storage contract: %w", err)
	}
	deployReceipts, err := s.waitMatchingSuccessfulReceipts(ctx, "access-list contract deployment", deployHash)
	if err != nil {
		return common.Hash{}, err
	}
	contract := deployReceipts[0].ContractAddress
	if contract == (common.Address{}) || deployReceipts[1].ContractAddress != contract {
		return common.Hash{}, fmt.Errorf("access-list contract address mismatch: EL1=%s EL2=%s", contract, deployReceipts[1].ContractAddress)
	}
	if len(contract.Bytes()) != common.AddressLength {
		return common.Hash{}, fmt.Errorf("access-list contract address width is %d, want %d", len(contract.Bytes()), common.AddressLength)
	}
	var upperHalfNonzero, lowerHalfNonzero bool
	for i, value := range contract {
		if i < common.AddressLength/2 {
			upperHalfNonzero = upperHalfNonzero || value != 0
		} else {
			lowerHalfNonzero = lowerHalfNonzero || value != 0
		}
	}
	if !upperHalfNonzero || !lowerHalfNonzero {
		return common.Hash{}, fmt.Errorf("access-list contract address does not exercise both VM64 halves: %s", contract)
	}
	for i, client := range s.clients {
		code, err := client.CodeAt(ctx, contract, deployReceipts[i].BlockNumber)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list contract code: %w", i+1, err)
		}
		if !bytes.Equal(code, runtimeCode) {
			return common.Hash{}, fmt.Errorf("EL%d access-list contract runtime mismatch: have %x want %x", i+1, code, runtimeCode)
		}
	}

	slot := common.Hash{}
	var nextValue common.StorageValue64
	for i := range nextValue {
		nextValue[i] = byte((i*37 + 11) & 0xff)
	}
	// Keep the high half observably nonzero so a 32-byte value truncation cannot
	// satisfy the post-state assertion by accident.
	nextValue[0] = 0x80
	expectedAccessList := types.AccessList{{
		Address:     contract,
		StorageKeys: []common.Hash{slot},
	}}
	call := qrl.CallMsg{
		From: s.cfg.signerAddress,
		To:   &contract,
		Data: append([]byte(nil), nextValue[:]...),
	}
	for i, client := range s.clients {
		created, gasUsed, vmError, err := gqrlclient.New(client.Client()).CreateAccessList(ctx, call)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d qrl_createAccessList: %w", i+1, err)
		}
		if vmError != "" {
			return common.Hash{}, fmt.Errorf("EL%d qrl_createAccessList VM error: %s", i+1, vmError)
		}
		if gasUsed == 0 {
			return common.Hash{}, fmt.Errorf("EL%d qrl_createAccessList reported zero gas", i+1)
		}
		if err := validateManagedAccessList(created, contract, slot); err != nil {
			return common.Hash{}, fmt.Errorf("EL%d qrl_createAccessList result: %w", i+1, err)
		}
		stored, err := readVM64StorageSlot(ctx, client, contract, slot, nil)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d storage after qrl_createAccessList: %w", i+1, err)
		}
		if !stored.IsZero() {
			return common.Hash{}, fmt.Errorf("EL%d qrl_createAccessList mutated storage to %s", i+1, stored.Hex())
		}
	}

	// Submit the independently derived list, not either RPC result. This keeps
	// qrl_createAccessList validation separate from transaction execution.
	hash, err := s.sendManagedContractTransaction(ctx, 0, &contract, nextValue[:], &expectedAccessList)
	if err != nil {
		return common.Hash{}, fmt.Errorf("submit topology-Clef access-list transaction: %w", err)
	}
	receipts, err := s.waitMatchingSuccessfulReceipts(ctx, "access-list transaction", hash)
	if err != nil {
		return common.Hash{}, err
	}
	if receipts[0].BlockNumber.Cmp(deployReceipts[0].BlockNumber) <= 0 {
		return common.Hash{}, fmt.Errorf("access-list transaction block %s did not follow deployment block %s", receipts[0].BlockNumber, deployReceipts[0].BlockNumber)
	}
	blockNumber := new(big.Int).Set(receipts[0].BlockNumber)
	previousBlock := new(big.Int).Sub(new(big.Int).Set(blockNumber), big.NewInt(1))
	var headers [2]*types.Header
	for i, client := range s.clients {
		tx, pending, err := client.TransactionByHash(ctx, hash)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list transaction %s: %w", i+1, hash, err)
		}
		if pending {
			return common.Hash{}, fmt.Errorf("EL%d still reports access-list transaction %s as pending", i+1, hash)
		}
		if tx.Hash() != hash || tx.Type() != types.DynamicFeeTxType {
			return common.Hash{}, fmt.Errorf("EL%d access-list transaction hash/type mismatch: hash=%s type=%d", i+1, tx.Hash(), tx.Type())
		}
		if tx.To() == nil || *tx.To() != contract {
			return common.Hash{}, fmt.Errorf("EL%d access-list transaction recipient mismatch: have %v want %s", i+1, tx.To(), contract)
		}
		if tx.Value().Sign() != 0 || !bytes.Equal(tx.Data(), nextValue[:]) {
			return common.Hash{}, fmt.Errorf("EL%d access-list transaction value/body mismatch: value=%s body=%x", i+1, tx.Value(), tx.Data())
		}
		observedAccessList := tx.AccessList()
		if err := validateManagedAccessList(&observedAccessList, contract, slot); err != nil {
			return common.Hash{}, fmt.Errorf("EL%d mined access list: %w", i+1, err)
		}
		sender, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d recover topology-Clef access-list sender: %w", i+1, err)
		}
		if sender != s.cfg.signerAddress {
			return common.Hash{}, fmt.Errorf("EL%d recovered access-list sender %s, want topology-Clef account %s", i+1, sender, s.cfg.signerAddress)
		}

		before, err := readVM64StorageSlot(ctx, client, contract, slot, previousBlock)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list storage before transaction: %w", i+1, err)
		}
		if !before.IsZero() {
			return common.Hash{}, fmt.Errorf("EL%d access-list storage before transaction is %s, want zero", i+1, before.Hex())
		}
		after, err := readVM64StorageSlot(ctx, client, contract, slot, blockNumber)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list storage after transaction: %w", i+1, err)
		}
		if after != nextValue {
			return common.Hash{}, fmt.Errorf("EL%d access-list storage mismatch: have %s want %s", i+1, after.Hex(), nextValue.Hex())
		}
		code, err := client.CodeAt(ctx, contract, blockNumber)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list contract code after transaction: %w", i+1, err)
		}
		if !bytes.Equal(code, runtimeCode) {
			return common.Hash{}, fmt.Errorf("EL%d access-list contract runtime changed: have %x want %x", i+1, code, runtimeCode)
		}
		headers[i], err = client.HeaderByNumber(ctx, blockNumber)
		if err != nil {
			return common.Hash{}, fmt.Errorf("EL%d access-list transaction header: %w", i+1, err)
		}
	}
	if err := compareExecutionHeaders(headers[0], headers[1]); err != nil {
		return common.Hash{}, fmt.Errorf("access-list transaction inclusion: %w", err)
	}
	return hash, nil
}

func (s *systemCheck) sendManagedContractTransaction(ctx context.Context, origin int, to *common.Address, input []byte, accessList *types.AccessList) (common.Hash, error) {
	if origin < 0 || origin >= len(s.clients) || s.clients[origin] == nil {
		return common.Hash{}, fmt.Errorf("invalid execution origin EL%d", origin+1)
	}
	if len(input) == 0 {
		return common.Hash{}, fmt.Errorf("managed contract transaction input is empty")
	}
	args := struct {
		From       common.Address    `json:"from"`
		To         *common.Address   `json:"to,omitempty"`
		Input      hexutil.Bytes     `json:"input"`
		AccessList *types.AccessList `json:"accessList,omitempty"`
	}{
		From:       s.cfg.signerAddress,
		To:         to,
		Input:      hexutil.Bytes(append([]byte(nil), input...)),
		AccessList: accessList,
	}
	var hash common.Hash
	if err := s.clients[origin].Client().CallContext(ctx, &hash, "qrl_sendTransaction", args); err != nil {
		return common.Hash{}, err
	}
	if hash == (common.Hash{}) {
		return common.Hash{}, fmt.Errorf("qrl_sendTransaction returned a zero hash")
	}
	return hash, nil
}

func (s *systemCheck) waitMatchingSuccessfulReceipts(ctx context.Context, label string, hash common.Hash) ([2]*types.Receipt, error) {
	var receipts [2]*types.Receipt
	for i := range s.clients {
		receipt, err := s.waitReceipt(ctx, i, hash)
		if err != nil {
			return receipts, fmt.Errorf("%s EL%d receipt: %w", label, i+1, err)
		}
		receipts[i] = receipt
		if receipt.TxHash != hash {
			return receipts, fmt.Errorf("%s EL%d receipt transaction hash is %s, want %s", label, i+1, receipt.TxHash, hash)
		}
		if receipt.Status != types.ReceiptStatusSuccessful {
			return receipts, fmt.Errorf("%s failed on EL%d with status %d", label, i+1, receipt.Status)
		}
		if receipt.BlockNumber == nil || receipt.BlockNumber.Sign() <= 0 {
			return receipts, fmt.Errorf("%s has invalid EL%d inclusion block", label, i+1)
		}
	}
	if receipts[0].BlockNumber.Cmp(receipts[1].BlockNumber) != 0 || receipts[0].BlockHash != receipts[1].BlockHash {
		return receipts, fmt.Errorf("%s inclusion differs: EL1=%s/%s EL2=%s/%s", label,
			receipts[0].BlockNumber, receipts[0].BlockHash, receipts[1].BlockNumber, receipts[1].BlockHash)
	}
	return receipts, nil
}

func validateManagedAccessList(list *types.AccessList, contract common.Address, slot common.Hash) error {
	if list == nil {
		return fmt.Errorf("access list is nil")
	}
	if len(*list) != 1 {
		return fmt.Errorf("access list has %d entries, want 1", len(*list))
	}
	tuple := (*list)[0]
	if len(tuple.Address.Bytes()) != common.AddressLength {
		return fmt.Errorf("access-list address width is %d, want %d", len(tuple.Address.Bytes()), common.AddressLength)
	}
	if tuple.Address != contract {
		return fmt.Errorf("access-list address is %s, want full VM64 contract address %s", tuple.Address, contract)
	}
	if len(tuple.StorageKeys) != 1 {
		return fmt.Errorf("access-list tuple has %d storage keys, want 1", len(tuple.StorageKeys))
	}
	if tuple.StorageKeys[0] != slot {
		return fmt.Errorf("access-list storage key is %s, want %s", tuple.StorageKeys[0], slot)
	}
	return nil
}

func readVM64StorageSlot(ctx context.Context, client *qrlclient.Client, contract common.Address, slot common.Hash, blockNumber *big.Int) (common.StorageValue64, error) {
	raw, err := client.StorageAt(ctx, contract, slot, blockNumber)
	if err != nil {
		return common.StorageValue64{}, err
	}
	if len(raw) != common.StorageValue64Length {
		return common.StorageValue64{}, fmt.Errorf("storage value width is %d, want %d", len(raw), common.StorageValue64Length)
	}
	return common.BytesToStorageValue64(raw), nil
}

func managedAccessListContractCode() (creationCode, runtimeCode []byte) {
	runtimeCode = []byte{
		byte(vm.PUSH1), 0x00,
		byte(vm.CALLDATALOAD),
		byte(vm.PUSH1), 0x00,
		byte(vm.SSTORE),
		byte(vm.STOP),
	}
	initCode := []byte{
		byte(vm.PUSH1), byte(len(runtimeCode)),
		byte(vm.PUSH1), 0x00, // Patched below with the runtime offset.
		byte(vm.PUSH1), 0x00,
		byte(vm.CODECOPY),
		byte(vm.PUSH1), byte(len(runtimeCode)),
		byte(vm.PUSH1), 0x00,
		byte(vm.RETURN),
	}
	initCode[3] = byte(len(initCode))
	creationCode = append(append([]byte(nil), initCode...), runtimeCode...)
	return creationCode, append([]byte(nil), runtimeCode...)
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

func (s *systemCheck) restartSigner(ctx context.Context) (err error) {
	log.Printf("systemcheck: stopping %s to prove EL signing depends on the topology signer", s.cfg.signerSvc)
	if err := s.k.stop(ctx, s.cfg.signerSvc); err != nil {
		return err
	}
	stopped := true
	defer func() {
		if stopped {
			s.recoverService(s.cfg.signerSvc)
		}
	}()

	healthCtx, healthCancel := context.WithTimeout(ctx, 20*time.Second)
	healthErr := s.verifyExecutionRPCIndependentOfSigner(healthCtx, 0)
	healthCancel()
	if healthErr != nil {
		return fmt.Errorf("EL1 RPC was not healthy while Clef was stopped: %w", healthErr)
	}
	downCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	_, sendErr := s.sendManagedTransfer(downCtx, 0)
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
	if err := s.k.start(ctx, s.cfg.signerSvc); err != nil {
		return err
	}
	stopped = false
	if err := s.waitSignerReady(ctx); err != nil {
		return fmt.Errorf("Clef did not recover after restart: %w", err)
	}
	if err := s.verifyManagedAccounts(ctx); err != nil {
		return fmt.Errorf("execution account manager did not recover after Clef restart: %w", err)
	}
	hash, err := s.sendManagedTransfer(ctx, 0)
	if err != nil {
		return fmt.Errorf("transaction after Clef restart: %w", err)
	}
	if _, err := s.verifyTransferOnBoth(ctx, hash); err != nil {
		return fmt.Errorf("verify transaction after Clef restart: %w", err)
	}
	log.Printf("PASS: stopping Clef blocked managed signing, and restarting it restored a cross-node confirmed transaction %s", hash)
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

func (s *systemCheck) restartSecondParticipant(ctx context.Context, previousFinality uint64, preFaultValidatorDuties validatorDutySnapshots) (err error) {
	beforeRestart, err := s.waitBeaconConvergence(ctx, previousFinality, 0, preFaultValidatorDuties)
	if err != nil {
		return fmt.Errorf("capture consensus checkpoint before participant restart: %w", err)
	}
	previousFinality = beforeRestart[0].finalizedEpoch
	previousHeadSlot := beforeRestart[0].headSlot
	preFaultExecutionFinality, err := s.waitExecutionFinality(ctx, preFaultValidatorDuties, 1, nil)
	if err != nil {
		return fmt.Errorf("capture execution finality before participant restart: %w", err)
	}
	if err := s.checkValidatorContinuityIfDue(ctx, preFaultValidatorDuties, true, 0, 1); err != nil {
		return fmt.Errorf("capture validator continuity before participant restart: %w", err)
	}

	stopOrder := []string{s.cfg.vcServices[1], s.cfg.clServices[1], s.cfg.elServices[1]}
	startOrder := []string{s.cfg.elServices[1], s.cfg.clServices[1], s.cfg.vcServices[1]}
	stopped := make(map[string]bool)
	defer func() {
		for _, service := range startOrder {
			if stopped[service] {
				s.recoverService(service)
			}
		}
	}()
	for _, service := range stopOrder {
		log.Printf("systemcheck: stopping %s", service)
		if err := s.k.stop(ctx, service); err != nil {
			return err
		}
		stopped[service] = true
	}

	if err := s.waitSecondParticipantEndpointsDown(ctx); err != nil {
		return err
	}

	hash, err := s.sendManagedTransfer(ctx, 0)
	if err != nil {
		return fmt.Errorf("submit transaction while participant two is offline: %w", err)
	}
	receipt, err := s.waitReceipt(ctx, 0, hash)
	if err != nil {
		return err
	}
	target := receipt.BlockNumber.Uint64() + s.cfg.catchupBlocks
	if err := waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, fmt.Sprintf("EL1 to advance to block %d while participant two is offline", target), func(ctx context.Context) (bool, error) {
		if err := s.checkValidatorContinuityIfDue(ctx, preFaultValidatorDuties, false, 0); err != nil {
			return false, err
		}
		head, err := s.clients[0].BlockNumber(ctx)
		return head >= target, err
	}); err != nil {
		return err
	}

	for _, service := range startOrder {
		log.Printf("systemcheck: starting %s", service)
		if err := s.k.start(ctx, service); err != nil {
			return err
		}
		delete(stopped, service)
		switch service {
		case s.cfg.elServices[1]:
			if err := s.redialSecondExecution(ctx); err != nil {
				return err
			}
		case s.cfg.clServices[1]:
			if err := s.waitBeaconReachable(ctx, 1); err != nil {
				return err
			}
		case s.cfg.vcServices[1]:
			if err := s.waitMetricsReachable(ctx, 1); err != nil {
				return err
			}
		}
	}
	recoveryValidatorBaseline, observedRecoveryValidators, err := s.waitRestartedValidatorBaseline(ctx, preFaultValidatorDuties)
	if err != nil {
		return fmt.Errorf("capture validator recovery baseline: %w", err)
	}
	logValidatorDutySnapshots("post-restart validator recovery baseline", observedRecoveryValidators)

	convergedBlock, err := s.waitCrossNodeCatchup(ctx, target, hash, recoveryValidatorBaseline)
	if err != nil {
		return err
	}
	if _, err := s.verifyTransferOnBoth(ctx, hash); err != nil {
		return fmt.Errorf("verify offline-window transaction after catch-up: %w", err)
	}
	aggregateRecoveryValidators, err := s.waitValidatorProgress(ctx, observedRecoveryValidators, "early aggregate validator activity without new duty failures after participant restart")
	if err != nil {
		return fmt.Errorf("early validator activity after restart: %w", err)
	}
	logValidatorDutySnapshots("early post-restart aggregate validator activity", aggregateRecoveryValidators)
	minimumFinality := previousFinality
	if s.cfg.requireFinalityAdvance {
		minimumFinality++
	} else {
		log.Printf("systemcheck: NON-STRICT finality mode: recovery must converge, but a new finalized epoch is not required")
	}
	statuses, err := s.waitBeaconConvergence(ctx, minimumFinality, previousHeadSlot, recoveryValidatorBaseline)
	if err != nil {
		return fmt.Errorf("beacon convergence after restart: %w", err)
	}
	minimumExecutionFinality := uint64(1)
	var previousExecutionFinality *executionFinalityStatus
	if s.cfg.requireFinalityAdvance {
		minimumExecutionFinality = preFaultExecutionFinality.finalizedNumber + 1
		previousExecutionFinality = &preFaultExecutionFinality
	}
	postRestartExecutionFinality, err := s.waitExecutionFinality(ctx, recoveryValidatorBaseline, minimumExecutionFinality, previousExecutionFinality)
	if err != nil {
		return fmt.Errorf("execution finality after restart: %w", err)
	}
	// Continuity above deliberately uses VC1's pre-fault baseline. The early
	// aggregate gate is measured from the post-restart observation, and the
	// complete gate below requires every VC2 key to attest in that fresh process.
	recoveredValidatorDuties, err := s.waitEveryValidatorAttested(ctx, observedRecoveryValidators, 1, "all restarted VC2 validators to attest without a reset or new duty failure")
	if err != nil {
		return fmt.Errorf("validator activity after restart: %w", err)
	}
	logValidatorDutySnapshots("post-restart validator-duty snapshot", recoveredValidatorDuties)
	if s.cfg.requireFinalityAdvance {
		log.Printf("PASS: participant two caught up to EL1 latest block %d with matching hash/state/receipt roots, preserved offline-window transaction agreement, resumed every VC2 validator duty without new failures, and advanced finalized consensus to epoch %d / execution block %d", convergedBlock, statuses[0].finalizedEpoch, postRestartExecutionFinality.finalizedNumber)
	} else {
		log.Printf("PASS (NON-STRICT FINALITY): participant two caught up to EL1 latest block %d with matching hash/state/receipt roots, preserved offline-window transaction agreement, resumed every VC2 validator duty without new failures, and converged at finalized epoch %d / execution block %d without requiring finality advancement", convergedBlock, statuses[0].finalizedEpoch, postRestartExecutionFinality.finalizedNumber)
	}
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
		var response beaconSyncResponse
		if err := s.http.getJSON(ctx, endpoint, syncStatusPath, &response); err != nil {
			return false, err
		}
		headSlot, err := strconv.ParseUint(response.Data.HeadSlot, 10, 64)
		if err != nil {
			return false, fmt.Errorf("CL%d returned invalid head slot %q: %w", index+1, response.Data.HeadSlot, err)
		}
		if headSlot == 0 {
			return false, fmt.Errorf("CL%d has not imported a post-genesis slot", index+1)
		}
		if endpoint != s.cfg.clURLs[index] {
			log.Printf("systemcheck: refreshed %s HTTP endpoint after restart: %s -> %s", s.cfg.clServices[index], s.cfg.clURLs[index], endpoint)
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

func (s *systemCheck) waitCrossNodeCatchup(ctx context.Context, minimumBlock uint64, hash common.Hash, validatorBaseline validatorDutySnapshots) (uint64, error) {
	var convergedBlock uint64
	err := waitFor(ctx, s.cfg.timeout, s.cfg.pollInterval, fmt.Sprintf("EL2 to catch up to EL1 latest head at or beyond block %d", minimumBlock), func(ctx context.Context) (bool, error) {
		if err := s.checkValidatorContinuityIfDue(ctx, validatorBaseline, false, 0, 1); err != nil {
			return false, err
		}
		headerA, err := s.clients[0].HeaderByNumber(ctx, nil)
		if err != nil {
			return false, err
		}
		headerB, err := s.clients[1].HeaderByNumber(ctx, nil)
		if err != nil {
			return false, err
		}
		if headerA.Number == nil || headerB.Number == nil {
			return false, fmt.Errorf("execution latest header has no block number")
		}
		if headerA.Number.Uint64() < minimumBlock {
			return false, fmt.Errorf("EL1 latest block %d is below offline-window target %d", headerA.Number.Uint64(), minimumBlock)
		}
		if err := compareExecutionHeaders(headerA, headerB); err != nil {
			return false, err
		}
		if _, err := s.clients[1].TransactionReceipt(ctx, hash); err != nil {
			return false, err
		}
		progress, err := s.clients[1].SyncProgress(ctx)
		if err != nil {
			return false, err
		}
		if progress != nil {
			return false, fmt.Errorf("EL2 is still syncing at %+v", progress)
		}
		convergedBlock = headerA.Number.Uint64()
		return true, nil
	})
	return convergedBlock, err
}

func compareExecutionHeaders(headerA, headerB *types.Header) error {
	if headerA == nil || headerB == nil || headerA.Number == nil || headerB.Number == nil {
		return fmt.Errorf("execution latest header is incomplete")
	}
	if headerA.Number.Cmp(headerB.Number) != 0 {
		return fmt.Errorf("execution latest block numbers differ: EL1=%s EL2=%s", headerA.Number, headerB.Number)
	}
	if headerA.Root != headerB.Root {
		return fmt.Errorf("execution latest block %s state roots differ", headerA.Number)
	}
	if headerA.ReceiptHash != headerB.ReceiptHash {
		return fmt.Errorf("execution latest block %s receipt roots differ", headerA.Number)
	}
	if headerA.Hash() != headerB.Hash() {
		return fmt.Errorf("execution latest block %s hashes differ", headerA.Number)
	}
	return nil
}

func (s *systemCheck) recoverService(service string) {
	// Recovery must outlive cancellation of the whole-run context so a timeout
	// cannot leave a service stopped.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := s.k.start(ctx, service); err != nil {
		log.Printf("WARNING: failed to recover stopped service %s: %v", service, err)
	}
}

func waitFor(ctx context.Context, timeout, interval time.Duration, description string, check func(context.Context) (bool, error)) error {
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var lastErr error
	for {
		done, err := check(waitCtx)
		if done && err == nil {
			return nil
		}
		if err != nil {
			var dutyFailure *validatorDutyFailureError
			var healthFailure *validatorHealthError
			var counterRegression *validatorCounterRegressionError
			var processReset *validatorProcessResetError
			var topologyFailure *validatorTopologyError
			if errors.As(err, &dutyFailure) || errors.As(err, &healthFailure) || errors.As(err, &counterRegression) || errors.As(err, &processReset) || errors.As(err, &topologyFailure) {
				return fmt.Errorf("failed waiting for %s: %w", description, err)
			}
			lastErr = err
		}
		timer := time.NewTimer(interval)
		select {
		case <-waitCtx.Done():
			timer.Stop()
			if lastErr != nil {
				return fmt.Errorf("timed out waiting for %s: %w", description, lastErr)
			}
			return fmt.Errorf("timed out waiting for %s: %w", description, waitCtx.Err())
		case <-timer.C:
		}
	}
}
