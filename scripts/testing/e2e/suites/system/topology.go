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

// Package system runs the VM64 multi-participant topology, finality, signer, duty, restart, access-list, and withdrawal checks.
package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/qrlclient"
)

type commandRunner interface {
	run(context.Context, ...string) (string, error)
}

type execRunner struct{}

func (execRunner) run(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "kurtosis", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("kurtosis %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

type kurtosis struct {
	enclave string
	runner  commandRunner
}

func (k kurtosis) Endpoint(ctx context.Context, service, portID, scheme string) (string, error) {
	out, err := k.runner.run(ctx, "port", "print", k.enclave, service, portID, "--format", "ip,number")
	if err != nil {
		return "", err
	}
	return parsePortOutput(out, scheme)
}

func (k kurtosis) Status(ctx context.Context, service string) (ServiceStatus, error) {
	out, err := k.runner.run(ctx, "service", "inspect", k.enclave, service, "--full-uuid", "-o", "json")
	if err != nil {
		return "", err
	}
	return parseServiceStatus(out)
}

func parseServiceStatus(output string) (ServiceStatus, error) {
	var document any
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		return "", fmt.Errorf("decode Kurtosis service status: %w", err)
	}
	var find func(any) (ServiceStatus, bool)
	find = func(value any) (ServiceStatus, bool) {
		switch typed := value.(type) {
		case map[string]any:
			for key, nested := range typed {
				normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
				if normalized == "status" || normalized == "service_status" {
					if text, ok := nested.(string); ok {
						switch strings.ToUpper(text) {
						case "RUNNING":
							return ServiceRunning, true
						case "STOPPED":
							return ServiceStopped, true
						}
					}
				}
				if status, ok := find(nested); ok {
					return status, true
				}
			}
		case []any:
			for _, nested := range typed {
				if status, ok := find(nested); ok {
					return status, true
				}
			}
		}
		return "", false
	}
	if status, ok := find(document); ok {
		return status, nil
	}
	return "", fmt.Errorf("Kurtosis service status is missing or unknown")
}

func (k kurtosis) Stop(ctx context.Context, service string) error {
	_, err := k.runner.run(ctx, "service", "stop", k.enclave, service)
	return err
}

func (k kurtosis) Start(ctx context.Context, service string) error {
	_, err := k.runner.run(ctx, "service", "start", k.enclave, service)
	return err
}

func parsePortOutput(output, scheme string) (string, error) {
	var endpoint string
	for _, line := range strings.Split(output, "\n") {
		if value := strings.TrimSpace(line); value != "" {
			endpoint = value
		}
	}
	if endpoint == "" {
		return "", fmt.Errorf("empty Kurtosis port output")
	}
	if !strings.Contains(endpoint, "://") {
		endpoint = scheme + "://" + endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("invalid Kurtosis port output %q: %w", endpoint, err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid Kurtosis port output %q", endpoint)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

func (cfg *config) resolveEndpoints(ctx context.Context, k ServiceController) error {
	for i := range cfg.rpcURLs {
		if cfg.rpcURLs[i] == "" {
			cfg.rpcURLsFromKurtosis[i] = true
			endpoint, err := cfg.executionEndpoint(ctx, k, i)
			if err != nil {
				return err
			}
			cfg.rpcURLs[i] = endpoint
		}
		if cfg.clURLs[i] == "" {
			cfg.clURLsFromKurtosis[i] = true
			endpoint, err := cfg.beaconEndpoint(ctx, k, i)
			if err != nil {
				return err
			}
			cfg.clURLs[i] = endpoint
		}
		if cfg.vcMetricsURLs[i] == "" {
			cfg.vcMetricsURLsFromKurtosis[i] = true
			endpoint, err := cfg.validatorEndpoint(ctx, k, i)
			if err != nil {
				return err
			}
			cfg.vcMetricsURLs[i] = endpoint
		}
	}
	if cfg.signerURL == "" {
		cfg.signerURLFromKurtosis = true
		endpoint, err := cfg.signerEndpoint(ctx, k)
		if err != nil {
			return err
		}
		cfg.signerURL = endpoint
	}
	return nil
}

func (cfg *config) executionEndpoint(ctx context.Context, k ServiceController, index int) (string, error) {
	if !cfg.rpcURLsFromKurtosis[index] {
		return cfg.rpcURLs[index], nil
	}
	endpoint, err := k.Endpoint(ctx, cfg.elServices[index], "rpc", "http")
	if err != nil {
		return "", fmt.Errorf("resolve %s RPC: %w", cfg.elServices[index], err)
	}
	return endpoint, nil
}

func (cfg *config) beaconEndpoint(ctx context.Context, k ServiceController, index int) (string, error) {
	if !cfg.clURLsFromKurtosis[index] {
		return cfg.clURLs[index], nil
	}
	endpoint, err := k.Endpoint(ctx, cfg.clServices[index], "http", "http")
	if err != nil {
		return "", fmt.Errorf("resolve %s HTTP: %w", cfg.clServices[index], err)
	}
	return endpoint, nil
}

func (cfg *config) validatorEndpoint(ctx context.Context, k ServiceController, index int) (string, error) {
	if !cfg.vcMetricsURLsFromKurtosis[index] {
		return cfg.vcMetricsURLs[index], nil
	}
	endpoint, err := k.Endpoint(ctx, cfg.vcServices[index], "metrics", "http")
	if err != nil {
		return "", fmt.Errorf("resolve %s metrics: %w", cfg.vcServices[index], err)
	}
	return strings.TrimRight(endpoint, "/") + "/metrics", nil
}

func (cfg *config) signerEndpoint(ctx context.Context, k ServiceController) (string, error) {
	if !cfg.signerURLFromKurtosis {
		return cfg.signerURL, nil
	}
	endpoint, err := k.Endpoint(ctx, cfg.signerSvc, "http", "http")
	if err != nil {
		return "", fmt.Errorf("resolve %s HTTP: %w", cfg.signerSvc, err)
	}
	return endpoint, nil
}

type systemCheck struct {
	cfg                          config
	k                            ServiceController
	http                         httpReader
	clients                      [2]*qrlclient.Client
	managedExecutions            [2]managedExecution
	lastValidatorContinuityCheck time.Time
	now                          func() time.Time
	evidence                     EvidenceRecorder
	transactions                 TransactionRecorder
	managedJournal               ManagedTransactionJournal
	observations                 SystemObservationRecorder
	resume                       resumeState
}

const (
	validatorMetricsReachabilityTimeout = 30 * time.Second
	// With 5-second slots and 64 of 128 validators on each VC, an aggregate
	// attestation counter should advance well inside this bounded observation.
	validatorDutyObservationTimeout = 2 * time.Minute
	// A canceled external-signing request must remain side-effect free after
	// Clef recovers and the chain has had time to include any leaked transaction.
	signerCancellationObservationTimeout = 45 * time.Second
	signerCancellationObservationBlocks  = uint64(2)
	// VC startup takes several seconds. Requiring four slots of proposer lead
	// time leaves at least fifteen seconds even when the readiness sample lands
	// at the very end of a five-second slot.
	validatorStartupLeadSlots = uint64(4)
	// Once the fresh VC2 metrics baseline exists, select a separate proof duty
	// far enough ahead that its per-key counter baseline and both header probes
	// are established before that slot begins.
	validatorProposalProofLeadSlots = uint64(3)
	// After scraping the selected validator's counters, keep at least one full
	// slot before the proposal slot begins. If the scrape consumed too much of
	// the selected window, discard it and select a later duty so the proposal
	// cannot be absorbed into the baseline.
	validatorProposalBaselineLeadSlots = uint64(2)
	validatorProposalGraceSlots        = uint64(2)
	// A healthy recovered beacon node should have observed a recent canonical
	// head before its validator is allowed to resume duties. A short missed-slot
	// run is tolerated while VC2 remains intentionally offline.
	validatorStartupMaxHeadLagSlots = uint64(2)
)

func newSystemCheck(ctx context.Context, cfg config, runner commandRunner) (*systemCheck, error) {
	return newSystemCheckWithController(ctx, cfg, kurtosis{enclave: cfg.enclave, runner: runner}, nil, nil)
}

func newSystemCheckWithController(ctx context.Context, cfg config, controller ServiceController, evidence EvidenceRecorder, transactions TransactionRecorder) (*systemCheck, error) {
	return newSystemCheckWithResume(ctx, cfg, controller, evidence, transactions, nil, nil, resumeState{})
}

func newSystemCheckWithResume(ctx context.Context, cfg config, controller ServiceController, evidence EvidenceRecorder, transactions TransactionRecorder, managedJournal ManagedTransactionJournal, observations SystemObservationRecorder, resume resumeState) (*systemCheck, error) {
	if controller == nil {
		return nil, fmt.Errorf("system suite service controller is nil")
	}
	if err := cfg.resolveEndpoints(ctx, controller); err != nil {
		return nil, err
	}
	check := &systemCheck{
		cfg: cfg, k: controller, evidence: evidence, transactions: transactions, managedJournal: managedJournal, observations: observations, resume: resume,
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

func runSystemCheck(ctx context.Context, cfg config, runner commandRunner) error {
	return runSystemCheckWithController(ctx, cfg, kurtosis{enclave: cfg.enclave, runner: runner}, nil, nil)
}

func runSystemCheckWithController(ctx context.Context, cfg config, controller ServiceController, evidence EvidenceRecorder, transactions TransactionRecorder) error {
	return runSystemCheckWithResume(ctx, cfg, controller, evidence, transactions, nil, nil, resumeState{})
}

func runSystemCheckWithResume(ctx context.Context, cfg config, controller ServiceController, evidence EvidenceRecorder, transactions TransactionRecorder, managedJournal ManagedTransactionJournal, observations SystemObservationRecorder, resume resumeState) error {
	runCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	check, err := newSystemCheckWithResume(runCtx, cfg, controller, evidence, transactions, managedJournal, observations, resume)
	if err != nil {
		return err
	}
	defer check.close()
	return check.run(runCtx)
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
	log.Printf("systemcheck: phase: %s", s.cfg.phase)

	if s.hasRestartHistory() {
		switch s.cfg.phase {
		case string(PhaseSignerRestart):
			return s.resumeSigner(ctx)
		case string(PhaseParticipantRestart):
			return s.resumeSecondParticipant(ctx)
		}
	}

	baseline, err := s.establishSystemBaseline(ctx)
	if err != nil {
		return err
	}
	switch s.cfg.phase {
	case "base":
		_, err := s.runBaseChecks(ctx, baseline)
		return err
	case "signer-restart":
		progressed, err := s.waitValidatorProgress(ctx, baseline.validatorDuties, "validator duty progress before signer restart")
		if err != nil {
			return fmt.Errorf("validator health before signer restart: %w", err)
		}
		if err := s.restartSigner(ctx); err != nil {
			return err
		}
		return s.checkValidatorContinuityIfDue(ctx, progressed, true, 0, 1)
	case "participant-restart":
		progressed, err := s.waitValidatorProgress(ctx, baseline.validatorDuties, "validator duty progress before participant restart")
		if err != nil {
			return fmt.Errorf("validator health before participant restart: %w", err)
		}
		return s.restartSecondParticipant(ctx, baseline.finality[0].finalizedEpoch, progressed)
	case "all":
		preFaultValidatorDuties, err := s.runBaseChecks(ctx, baseline)
		if err != nil {
			return err
		}
		if s.cfg.skipRestarts {
			log.Printf("systemcheck: restart checks skipped by request")
			return nil
		}
		if err := s.restartSigner(ctx); err != nil {
			return err
		}
		return s.restartSecondParticipant(ctx, baseline.finality[0].finalizedEpoch, preFaultValidatorDuties)
	default:
		return fmt.Errorf("unsupported systemcheck phase %q", s.cfg.phase)
	}
}

type systemBaseline struct {
	validatorDuties validatorDutySnapshots
	finality        [2]beaconStatus
}

func (s *systemCheck) establishSystemBaseline(ctx context.Context) (systemBaseline, error) {
	if err := s.waitExecutionHealthy(ctx); err != nil {
		return systemBaseline{}, err
	}
	if err := s.waitSignerReady(ctx); err != nil {
		return systemBaseline{}, err
	}
	if err := s.verifyManagedAccounts(ctx); err != nil {
		return systemBaseline{}, err
	}
	if err := s.verifyBeaconSpecs(ctx); err != nil {
		return systemBaseline{}, err
	}
	initialValidatorDuties, err := s.waitInitialValidatorActivity(ctx)
	if err != nil {
		return systemBaseline{}, err
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
		return systemBaseline{}, err
	}
	if _, err := s.waitExecutionFinality(ctx, initialValidatorDuties, 1, nil); err != nil {
		return systemBaseline{}, err
	}
	if err := s.checkValidatorContinuityIfDue(ctx, initialValidatorDuties, true, 0, 1); err != nil {
		return systemBaseline{}, err
	}
	log.Printf("PASS: both EL/CL/VC pairs are peered, healthy, active, and finalized at beacon epoch %d", initialFinality[0].finalizedEpoch)
	return systemBaseline{validatorDuties: initialValidatorDuties, finality: initialFinality}, nil
}

func (s *systemCheck) runBaseChecks(ctx context.Context, baseline systemBaseline) (validatorDutySnapshots, error) {
	if err := s.waitAutomaticWithdrawal(ctx, baseline.validatorDuties); err != nil {
		return validatorDutySnapshots{}, fmt.Errorf("automatic VM64 withdrawal gate: %w", err)
	}

	txHash, err := s.sendManagedTransfer(ctx, 0, TransactionLabelBaseEL1Transfer)
	if err != nil {
		return validatorDutySnapshots{}, fmt.Errorf("submit EL1-origin topology-Clef transaction: %w", err)
	}
	if _, err := s.verifyTransferOnBoth(ctx, txHash); err != nil {
		return validatorDutySnapshots{}, err
	}
	log.Printf("PASS: topology Clef signed EL1-origin transaction %s and both execution nodes agreed on its sender, receipt, header, state transition, and fee-recipient reward", txHash)

	el2Hash, err := s.sendManagedTransfer(ctx, 1, TransactionLabelBaseEL2Transfer)
	if err != nil {
		return validatorDutySnapshots{}, fmt.Errorf("submit EL2-origin topology-Clef transaction: %w", err)
	}
	if _, err := s.verifyTransferOnBoth(ctx, el2Hash); err != nil {
		return validatorDutySnapshots{}, fmt.Errorf("verify EL2-origin topology-Clef transaction: %w", err)
	}
	log.Printf("PASS: EL2 originated managed transaction %s through topology Clef and both execution nodes agreed on its full VM64 transition", el2Hash)

	accessListHash, err := s.checkManagedAccessListTransaction(ctx)
	if err != nil {
		return validatorDutySnapshots{}, fmt.Errorf("topology-Clef non-empty access-list transaction: %w", err)
	}
	log.Printf("PASS: topology Clef signed non-empty access-list transaction %s and both execution nodes agreed on its exact 64-byte contract address, storage key, receipt, and VM64 state transition", accessListHash)

	preFaultValidatorDuties, err := s.waitValidatorProgress(ctx, baseline.validatorDuties, "pre-fault validator duty progress")
	if err != nil {
		return validatorDutySnapshots{}, fmt.Errorf("validator health before fault injection: %w", err)
	}
	logValidatorDutySnapshots("pre-fault validator-duty snapshot", preFaultValidatorDuties)
	return preFaultValidatorDuties, nil
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
			var evidenceFailure *evidenceError
			if errors.As(err, &dutyFailure) || errors.As(err, &healthFailure) || errors.As(err, &counterRegression) || errors.As(err, &processReset) || errors.As(err, &topologyFailure) || errors.As(err, &evidenceFailure) {
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
