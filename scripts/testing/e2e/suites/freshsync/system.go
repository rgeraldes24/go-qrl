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

package freshsync

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"time"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/rpc"
	kurtosisapi "github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
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
	cfg Config
	k   cliKurtosis

	client              kurtosisapi.Client
	enclave             lifecycle.EnclaveRef
	recorder            TemporaryServiceRecorder
	txRecord            TransactionRecorder
	managedRecord       ManagedTransactionRecorder
	recordedTransaction string
	recordedIntent      *lifecycle.ManagedTransactionIntent
	initialAttempt      bool
	resubmitted         bool
	now                 func() time.Time
	managedClients      [2]managedExecutionClient

	reference *qrlclient.Client
	fresh     *qrlclient.Client
	clients   [2]*qrlclient.Client
	http      httpReader

	addedServices     []TemporaryService
	freshEL           TemporaryService
	freshCL           TemporaryService
	freshCLURL        string
	depositTarget     depositTargetState
	creationIntents   map[string]lifecycle.TemporaryServiceCreationIntent
	recoveredServices map[string]TemporaryService
}

func runFreshSync(ctx context.Context, cfg Config, runner commandRunner) error {
	return runFreshSyncWithLifecycle(ctx, cfg, runner, nil, lifecycle.EnclaveRef{}, nil, nil, nil, "", nil, false, false, nil, nil, nil)
}

func runFreshSyncWithLifecycle(
	ctx context.Context,
	cfg Config,
	runner commandRunner,
	client kurtosisapi.Client,
	enclave lifecycle.EnclaveRef,
	recorder TemporaryServiceRecorder,
	txRecord TransactionRecorder,
	managedRecord ManagedTransactionRecorder,
	recordedTransaction string,
	recordedIntent *lifecycle.ManagedTransactionIntent,
	initialAttempt bool,
	resubmitted bool,
	now func() time.Time,
	creationIntents map[string]lifecycle.TemporaryServiceCreationIntent,
	recoveredServices map[string]TemporaryService,
) (err error) {
	runCtx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()

	check := &freshSyncCheck{
		cfg:                 cfg,
		k:                   cliKurtosis{enclave: cfg.Enclave, runner: runner},
		client:              client,
		enclave:             enclave,
		recorder:            recorder,
		txRecord:            txRecord,
		managedRecord:       managedRecord,
		recordedTransaction: recordedTransaction,
		recordedIntent:      recordedIntent,
		initialAttempt:      initialAttempt,
		resubmitted:         resubmitted,
		now:                 now,
		creationIntents:     cloneCreationIntentMap(creationIntents),
		recoveredServices:   cloneTemporaryServiceMap(recoveredServices),
		http: httpReader{
			client: &http.Client{Timeout: 15 * time.Second},
		},
	}
	recoveredOrder, err := orderedRecoveredTemporaryServices(cfg, check.recoveredServices)
	if err != nil {
		return err
	}
	// Cleanup ownership is established for the complete recovered pair before
	// the run resumes. Otherwise an error after adopting the EL but before
	// reaching the CL could remove only the EL and leave a CL whose engine
	// endpoint is permanently bound to the old execution service.
	check.addedServices = recoveredOrder
	defer func() {
		if check.fresh != nil {
			check.fresh.Close()
		}
		if check.reference != nil {
			check.reference.Close()
		}
		shouldCleanup := (err == nil && !cfg.KeepServices) || (err != nil && cfg.CleanupOnFailure)
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

func orderedRecoveredTemporaryServices(cfg Config, recovered map[string]TemporaryService) ([]TemporaryService, error) {
	ordered := make([]TemporaryService, 0, len(recovered))
	seen := make(map[string]struct{}, len(recovered))
	for _, name := range []string{cfg.FreshELService, cfg.FreshCLService} {
		identity, exists := recovered[name]
		if !exists {
			continue
		}
		if identity.Name != name {
			return nil, fmt.Errorf("recovered temporary service key %s identifies %s/%s", name, identity.Name, identity.UUID)
		}
		if err := identity.Validate(); err != nil {
			return nil, err
		}
		ordered = append(ordered, identity)
		seen[name] = struct{}{}
	}
	if len(seen) != len(recovered) {
		return nil, fmt.Errorf("recovered temporary service set contains %d identities outside the configured EL/CL pair", len(recovered)-len(seen))
	}
	return ordered, nil
}

func (s *freshSyncCheck) run(ctx context.Context) error {
	if s.cfg.ReferenceRPC == "" {
		endpoint, err := s.serviceEndpoint(ctx, s.cfg.ReferenceService, "rpc", "http")
		if err != nil {
			return fmt.Errorf("resolve reference RPC: %w", err)
		}
		s.cfg.ReferenceRPC = endpoint
	}
	reference, err := qrlclient.DialContext(ctx, s.cfg.ReferenceRPC)
	if err != nil {
		return fmt.Errorf("dial reference execution RPC %s: %w", s.cfg.ReferenceRPC, err)
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
	s.depositTarget, err = readAndVerifyDepositTarget(ctx, s.reference, s.cfg.DepositContract, target)
	if err != nil {
		return fmt.Errorf("capture reference finalized deposit state: %w", err)
	}
	log.Printf("freshsync: captured deposit contract=%s slot=%s value=0x%x count=%d root=0x%x with verified account/storage proofs and VM calls", s.cfg.DepositContract.Hex(), s.depositTarget.slot.Hex(), s.depositTarget.value, s.depositTarget.count, s.depositTarget.root)
	log.Printf("freshsync: reference=%s finalized target=%d/%s mode=%s", s.cfg.ReferenceRPC, target.Number.Uint64(), target.Hash(), s.cfg.SyncMode)

	elConfig, err := s.k.inspect(ctx, s.cfg.ELTemplateService)
	if err != nil {
		return fmt.Errorf("inspect execution template: %w", err)
	}
	if err := mutateExecutionConfig(elConfig, s.cfg.SyncMode); err != nil {
		return fmt.Errorf("prepare empty execution clone: %w", err)
	}
	if err := s.addTemporaryService(ctx, s.cfg.FreshELService, elConfig); err != nil {
		return fmt.Errorf("add fresh execution service: %w", err)
	}

	freshIP, err := s.waitFreshExecutionRPC(ctx)
	if err != nil {
		return err
	}
	engineEndpoint, err := engineURL(freshIP, 8551)
	if err != nil {
		return err
	}
	clConfig, err := s.k.inspect(ctx, s.cfg.CLTemplateService)
	if err != nil {
		return fmt.Errorf("inspect beacon template: %w", err)
	}
	if err := mutateBeaconConfig(clConfig, engineEndpoint); err != nil {
		return fmt.Errorf("prepare beacon sync driver: %w", err)
	}
	if err := s.addTemporaryService(ctx, s.cfg.FreshCLService, clConfig); err != nil {
		return fmt.Errorf("add fresh beacon service: %w", err)
	}
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
	log.Printf("PASS: %s started only after its fail-closed empty-datadir guard and %s-synced through finalized block %d with matching VM64 state", s.cfg.FreshELService, s.cfg.SyncMode, target.Number.Uint64())

	hash, err := s.transferForVerification(ctx)
	if err != nil {
		return fmt.Errorf("prepare post-catch-up VM64 transfer verification: %w", err)
	}
	if err := s.verifyTransfer(ctx, hash); err != nil {
		return err
	}
	log.Printf("PASS: fresh execution node imported topology-Clef transaction %s and agreed on its 64-byte sender, receipt, header, state root, balances, and nonce transition", hash)
	return nil
}

func (s *freshSyncCheck) transferForVerification(ctx context.Context) (common.Hash, error) {
	return s.managedTransferForVerification(ctx)
}

func transferTransactionLabel(mode string) string {
	return "fresh-" + mode + "-transfer"
}

func parseTransactionHash(raw string) (common.Hash, error) {
	decoded, err := hexutil.Decode(raw)
	if err != nil {
		return common.Hash{}, err
	}
	if len(decoded) != common.HashLength {
		return common.Hash{}, fmt.Errorf("transaction hash has %d bytes, want %d", len(decoded), common.HashLength)
	}
	return common.BytesToHash(decoded), nil
}

func (s *freshSyncCheck) addTemporaryService(ctx context.Context, name string, cfg rawServiceConfig) error {
	digest, err := serviceConfigDigest(cfg)
	if err != nil {
		return fmt.Errorf("digest temporary service %s config: %w", name, err)
	}
	if identity, recovered := s.recoveredServices[name]; recovered {
		intent, exists := s.creationIntents[name]
		if !exists {
			return fmt.Errorf("recovered temporary service %s/%s has no creation intent", identity.Name, identity.UUID)
		}
		if intent.ConfigDigest != digest {
			return fmt.Errorf("temporary service %s resumed config changed: current=%s intent=%s", name, digest, intent.ConfigDigest)
		}
		current, err := resolveIntendedTemporaryService(ctx, s.client, s.enclave, intent)
		if err != nil {
			return fmt.Errorf("revalidate recovered temporary service %s/%s: %w", identity.Name, identity.UUID, err)
		}
		if current != identity {
			return fmt.Errorf("recovered temporary service identity changed: current=%s/%s recovered=%s/%s", current.Name, current.UUID, identity.Name, identity.UUID)
		}
		s.trackTemporaryService(name, identity)
		return nil
	}
	creationRecorder, ok := s.recorder.(TemporaryServiceCreationRecorder)
	if !ok {
		return errors.New("temporary service add requires a durable creation-intent recorder")
	}
	preparedAt := time.Now().UTC()
	if s.now != nil {
		preparedAt = s.now().UTC()
	}
	intent, err := newTemporaryServiceCreationIntent(name, s.enclave, cfg, preparedAt)
	if err != nil {
		return err
	}
	if err := creationRecorder.RecordTemporaryServiceCreationIntent(ctx, intent); err != nil {
		return fmt.Errorf("persist temporary service %s creation intent: %w", name, err)
	}
	s.creationIntents = cloneCreationIntentMap(s.creationIntents)
	s.creationIntents[name] = intent
	labeledConfig, err := configWithCreationIntent(cfg, intent)
	if err != nil {
		return fmt.Errorf("label temporary service %s creation: %w", name, err)
	}
	if err := proveTemporaryServiceNameAbsent(ctx, s.client, s.enclave, name); err != nil {
		return err
	}
	addErr := s.k.add(ctx, name, labeledConfig)
	// The CLI may be interrupted after Kurtosis committed the add but before it
	// returned a success response. Probe with a short cancellation-independent
	// context in both the success and error cases so such a service is never
	// left without a durable UUID merely because the stage was canceled.
	recordCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	identity, err := captureTemporaryService(recordCtx, s.client, s.enclave, intent, s.recorder)
	cancel()
	if identity.UUID != "" {
		s.addedServices = append(s.addedServices, identity)
	}
	if err != nil {
		// A recorder failure must not leave an untracked service behind. Once
		// the UUID is known, remove only that exact identity. If identity lookup
		// itself failed, preserve the service rather than deleting by name.
		if identity.UUID != "" {
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Minute)
			cleanupErr := removeTemporaryService(cleanupCtx, s.client, s.enclave, identity)
			cancel()
			if cleanupErr == nil {
				s.addedServices = s.addedServices[:len(s.addedServices)-1]
			}
			return errors.Join(addErr, err, cleanupErr)
		}
		return errors.Join(addErr, err)
	}
	s.trackTemporaryService(name, identity)
	// An exact marker and full UUID prove that Kurtosis committed the creation,
	// even when the CLI response itself was lost.
	return nil
}

func cloneTemporaryServiceMap(values map[string]TemporaryService) map[string]TemporaryService {
	cloned := make(map[string]TemporaryService, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}

func (s *freshSyncCheck) trackTemporaryService(name string, identity TemporaryService) {
	if !slices.Contains(s.addedServices, identity) {
		s.addedServices = append(s.addedServices, identity)
	}
	if name == s.cfg.FreshELService {
		s.freshEL = identity
	}
	if name == s.cfg.FreshCLService {
		s.freshCL = identity
	}
}

func proveTemporaryServiceNameAbsent(ctx context.Context, client kurtosisapi.Client, enclave lifecycle.EnclaveRef, name string) error {
	if client == nil {
		return errors.New("Kurtosis client is required to prove temporary-service name absence")
	}
	services, err := client.Services(ctx, enclave)
	if err != nil {
		return fmt.Errorf("prove temporary service %s absent before add: %w", name, err)
	}
	for _, service := range services {
		if service.Name == name {
			return fmt.Errorf("temporary service %s already exists as %s before add; preserving it", name, service.UUID)
		}
	}
	return nil
}

func (s *freshSyncCheck) serviceEndpoint(ctx context.Context, serviceName, portID, scheme string) (string, error) {
	if s.client == nil {
		return s.k.endpoint(ctx, serviceName, portID, scheme)
	}
	service, err := s.client.Service(ctx, s.enclave, serviceName)
	if err != nil {
		return "", err
	}
	endpoint, ok := service.PublicEndpoint(portID, scheme)
	if !ok {
		return "", fmt.Errorf("service %s has no public %s port %q", service.Name, scheme, portID)
	}
	return endpoint, nil
}

func (s *freshSyncCheck) temporaryEndpoint(ctx context.Context, identity TemporaryService, portID, scheme string) (string, error) {
	if s.client == nil {
		return s.k.endpoint(ctx, identity.Name, portID, scheme)
	}
	return refreshPublicEndpoint(ctx, s.client, s.enclave, identity, portID, scheme)
}

func (s *freshSyncCheck) verifySyncModeEvidence(ctx context.Context) error {
	logs, err := s.k.logs(ctx, s.cfg.FreshELService)
	if err != nil {
		return fmt.Errorf("read fresh execution logs for %s evidence: %w", s.cfg.SyncMode, err)
	}
	const (
		snapCycle = "Starting snapshot sync cycle"
		snapPivot = "Committing snap sync pivot as new head"
	)
	if s.cfg.SyncMode == "snap" {
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
	err := waitFor(ctx, s.cfg.Timeout, s.cfg.PollInterval, "reference execution node to become healthy", func(ctx context.Context) (bool, error) {
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
	startupTimeout := s.cfg.Timeout
	if startupTimeout > 5*time.Minute {
		startupTimeout = 5 * time.Minute
	}
	var privateIP string
	err := waitFor(ctx, startupTimeout, s.cfg.PollInterval, "fresh execution RPC and private-IP substitution", func(ctx context.Context) (bool, error) {
		endpoint, err := s.temporaryEndpoint(ctx, s.freshEL, "rpc", "http")
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
	startupTimeout := s.cfg.Timeout
	if startupTimeout > 5*time.Minute {
		startupTimeout = 5 * time.Minute
	}
	return waitFor(ctx, startupTimeout, s.cfg.PollInterval, "fresh beacon HTTP endpoint", func(ctx context.Context) (bool, error) {
		endpoint, err := s.temporaryEndpoint(ctx, s.freshCL, "http", "http")
		if err != nil {
			return false, err
		}
		s.freshCLURL = endpoint
		return true, nil
	})
}

func (s *freshSyncCheck) waitExecutionCatchup(ctx context.Context, identity executionIdentity, target *types.Header) error {
	return waitFor(ctx, s.cfg.Timeout, s.cfg.PollInterval, fmt.Sprintf("fresh execution node to catch finalized block %d", target.Number.Uint64()), func(ctx context.Context) (bool, error) {
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
		refBalance, err := s.reference.BalanceAt(ctx, s.cfg.Recipient, target.Number)
		if err != nil {
			return false, fmt.Errorf("reference recipient balance at target: %w", err)
		}
		freshBalance, err := s.fresh.BalanceAt(ctx, s.cfg.Recipient, target.Number)
		if err != nil {
			return false, fmt.Errorf("fresh recipient balance at target: %w", err)
		}
		if refBalance.Cmp(freshBalance) != 0 {
			return false, fmt.Errorf("recipient VM64 state differs at target: %s != %s", refBalance, freshBalance)
		}
		refNonce, err := s.reference.NonceAt(ctx, s.cfg.SignerAddress, target.Number)
		if err != nil {
			return false, fmt.Errorf("reference signer nonce at target: %w", err)
		}
		freshNonce, err := s.fresh.NonceAt(ctx, s.cfg.SignerAddress, target.Number)
		if err != nil {
			return false, fmt.Errorf("fresh signer nonce at target: %w", err)
		}
		if refNonce != freshNonce {
			return false, fmt.Errorf("signer VM64 nonce differs at target: %d != %d", refNonce, freshNonce)
		}
		freshDeposit, err := readAndVerifyDepositTarget(ctx, s.fresh, s.cfg.DepositContract, target)
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
	return waitFor(ctx, s.cfg.Timeout, s.cfg.PollInterval, "fresh beacon node to sync and keep its execution client online", func(ctx context.Context) (bool, error) {
		status, err := s.http.beaconStatus(ctx, s.freshCLURL)
		if err != nil {
			return false, err
		}
		log.Printf("freshsync: fresh CL healthy at slot %d with %d peers", status.headSlot, status.connectedPeers)
		return true, nil
	})
}

func (s *freshSyncCheck) verifyManagedAccounts(ctx context.Context) error {
	if len(s.cfg.SignerAddress.Bytes()) != common.AddressLength {
		return fmt.Errorf("signer address width is %d, want %d", len(s.cfg.SignerAddress.Bytes()), common.AddressLength)
	}
	for i, client := range s.clients {
		var accounts []common.Address
		if err := client.Client().CallContext(ctx, &accounts, "qrl_accounts"); err != nil {
			return fmt.Errorf("EL%d qrl_accounts: %w", i+1, err)
		}
		if !containsAddress(accounts, s.cfg.SignerAddress) {
			return fmt.Errorf("EL%d does not expose topology signer %s", i+1, s.cfg.SignerAddress)
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

func (s *freshSyncCheck) waitReceipt(ctx context.Context, index int, hash common.Hash) (*types.Receipt, error) {
	var receipt *types.Receipt
	err := waitFor(ctx, s.cfg.Timeout, s.cfg.PollInterval, fmt.Sprintf("EL%d receipt %s", index+1, hash), func(ctx context.Context) (bool, error) {
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

	var expectedRequest *managedTransactionRequest
	if s.recordedIntent != nil {
		request, err := managedRequestFromIntent(*s.recordedIntent)
		if err != nil {
			return fmt.Errorf("decode recorded managed transaction intent: %w", err)
		}
		expectedRequest = &request
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
		if tx == nil {
			return fmt.Errorf("EL%d returned a nil transaction for %s", i+1, hash)
		}
		if tx.Hash() != hash {
			return fmt.Errorf("EL%d returned transaction hash %s for requested hash %s", i+1, tx.Hash(), hash)
		}
		if tx.Type() != types.DynamicFeeTxType || tx.ChainId() == nil || tx.ChainId().Sign() <= 0 {
			return fmt.Errorf("EL%d transaction is not a chain-bound dynamic-fee transaction", i+1)
		}
		if tx.To() == nil || *tx.To() != s.cfg.Recipient {
			return fmt.Errorf("EL%d transaction recipient mismatch", i+1)
		}
		if tx.Value().Cmp(new(big.Int).SetUint64(s.cfg.TransferValue)) != 0 {
			return fmt.Errorf("EL%d transaction value mismatch: %s", i+1, tx.Value())
		}
		sender, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
		if err != nil {
			return fmt.Errorf("EL%d recover transaction sender: %w", i+1, err)
		}
		if sender != s.cfg.SignerAddress || len(sender.Bytes()) != common.AddressLength {
			return fmt.Errorf("EL%d recovered sender %s with width %d, want %s with width %d", i+1, sender, len(sender.Bytes()), s.cfg.SignerAddress, common.AddressLength)
		}
		if len(tx.Data()) != 0 {
			return fmt.Errorf("EL%d transaction input is %s, want exact empty input", i+1, hexutil.Encode(tx.Data()))
		}
		if tx.AccessList() == nil || len(tx.AccessList()) != 0 {
			return fmt.Errorf("EL%d transaction access list is not the exact explicit empty list", i+1)
		}
		if expectedRequest != nil {
			if tx.Nonce() != uint64(expectedRequest.Nonce) {
				return fmt.Errorf("EL%d transaction nonce is %d, want recorded explicit nonce %d", i+1, tx.Nonce(), expectedRequest.Nonce)
			}
			if err := validateManagedTransaction(tx, *expectedRequest); err != nil {
				return fmt.Errorf("EL%d transaction differs from recorded managed intent: %w", i+1, err)
			}
		}
		if signed != nil && (tx.Hash() != signed.Hash() || tx.ChainId().Cmp(signed.ChainId()) != 0 || tx.Nonce() != signed.Nonce()) {
			return fmt.Errorf("EL%d transaction identity differs from the reference execution node", i+1)
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
		before, err := client.BalanceAt(ctx, s.cfg.Recipient, previous)
		if err != nil {
			return fmt.Errorf("EL%d recipient balance before transfer: %w", i+1, err)
		}
		after, err := client.BalanceAt(ctx, s.cfg.Recipient, blockNumber)
		if err != nil {
			return fmt.Errorf("EL%d recipient balance after transfer: %w", i+1, err)
		}
		balances[i] = after
		delta := new(big.Int).Sub(new(big.Int).Set(after), before)
		if delta.Cmp(new(big.Int).SetUint64(s.cfg.TransferValue)) != 0 {
			return fmt.Errorf("EL%d recipient balance delta is %s, want %d", i+1, delta, s.cfg.TransferValue)
		}
		nonceBefore, err := client.NonceAt(ctx, s.cfg.SignerAddress, previous)
		if err != nil {
			return fmt.Errorf("EL%d signer nonce before transfer: %w", i+1, err)
		}
		nonceAfter, err := client.NonceAt(ctx, s.cfg.SignerAddress, blockNumber)
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
	if s.client == nil {
		return errors.New("refusing name-based temporary-service cleanup without the Kurtosis identity client")
	}
	return CleanupTemporaryServices(ctx, s.client, s.enclave, s.addedServices)
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
