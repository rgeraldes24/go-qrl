// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
)

const managedTestSeed = "010000a7b1a3005d9e110009c48d45deb43f0a0e31846ed2c5aaefb6d4238040ad4c08794ffe65585c13eb6948c2faf6db90c2"

type managedExecutionStub struct {
	name         string
	chainID      *big.Int
	confirmed    uint64
	pendingNonce uint64
	head         *types.Header
	headers      map[uint64]*types.Header
	blocks       map[uint64][]*types.Transaction
	pending      []*types.Transaction
	sendHash     common.Hash
	sendErr      error
	send         func(managedRPCArgs) (common.Hash, error)
	sends        []managedRPCArgs
	events       *[]string
}

func (stub *managedExecutionStub) ChainID(context.Context) (*big.Int, error) {
	return new(big.Int).Set(stub.chainID), nil
}

func (stub *managedExecutionStub) PendingNonceAt(context.Context, common.Address) (uint64, error) {
	return stub.pendingNonce, nil
}

func (stub *managedExecutionStub) NonceAt(context.Context, common.Address) (uint64, error) {
	return stub.confirmed, nil
}

func (stub *managedExecutionStub) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	if number == nil {
		return stub.head, nil
	}
	return stub.headers[number.Uint64()], nil
}

func (stub *managedExecutionStub) BlockNumber(context.Context) (uint64, error) {
	return stub.head.Number.Uint64(), nil
}

func (stub *managedExecutionStub) TransactionsByNumber(_ context.Context, number uint64) ([]*types.Transaction, error) {
	return stub.blocks[number], nil
}

func (stub *managedExecutionStub) PendingTransactions(context.Context) ([]*types.Transaction, error) {
	return stub.pending, nil
}

func (stub *managedExecutionStub) SendManagedTransaction(_ context.Context, args managedRPCArgs) (common.Hash, error) {
	stub.sends = append(stub.sends, args)
	if stub.events != nil {
		*stub.events = append(*stub.events, "rpc-"+stub.name)
	}
	if stub.send != nil {
		return stub.send(args)
	}
	return stub.sendHash, stub.sendErr
}

type managedJournalStub struct {
	events      *[]string
	intent      ManagedTransactionIntent
	intentErr   error
	initialErr  error
	resubmitErr error
}

func (stub *managedJournalStub) RecordManagedTransactionIntent(_ context.Context, intent ManagedTransactionIntent) error {
	*stub.events = append(*stub.events, "intent")
	if stub.intentErr == nil {
		stub.intent = intent
	}
	return stub.intentErr
}

func (stub *managedJournalStub) RecordManagedTransactionInitialAttempt(context.Context, string, time.Time) error {
	*stub.events = append(*stub.events, "initial")
	return stub.initialErr
}

func (stub *managedJournalStub) RecordManagedTransactionResubmit(context.Context, string, time.Time) error {
	*stub.events = append(*stub.events, "resubmit")
	return stub.resubmitErr
}

type managedFixture struct {
	check   *systemCheck
	request managedTransactionRequest
	first   *managedExecutionStub
	second  *managedExecutionStub
	journal *managedJournalStub
	events  []string
	wallet  wallet.Wallet
}

func newManagedFixture(t *testing.T, origin int, accessList *types.AccessList) *managedFixture {
	t.Helper()
	w, err := wallet.RestoreFromSeedHex(managedTestSeed)
	if err != nil {
		t.Fatal(err)
	}
	boundary := &types.Header{Number: big.NewInt(10), Extra: []byte("shared")}
	first := &managedExecutionStub{
		name: "EL1", chainID: big.NewInt(1337), confirmed: 7, pendingNonce: 7,
		head: &types.Header{Number: big.NewInt(11), Extra: []byte("ahead")}, headers: map[uint64]*types.Header{10: boundary},
		blocks: make(map[uint64][]*types.Transaction), sendHash: common.HexToHash("0x1001"),
	}
	second := &managedExecutionStub{
		name: "EL2", chainID: big.NewInt(1337), confirmed: 7, pendingNonce: 7,
		head: boundary, headers: map[uint64]*types.Header{10: boundary},
		blocks: make(map[uint64][]*types.Transaction), sendHash: common.HexToHash("0x1002"),
	}
	fixture := &managedFixture{first: first, second: second, wallet: w}
	journal := &managedJournalStub{events: &fixture.events}
	fixture.journal = journal
	first.events = &fixture.events
	second.events = &fixture.events
	recipient := common.Address{common.AddressLength - 1: 0x22}
	fixture.request = managedTransactionRequest{
		origin: origin, to: &recipient, value: big.NewInt(19), input: []byte{0xaa, 0xbb}, accessList: accessList,
	}
	fixed := time.Unix(1_700_000_000, 0).UTC()
	fixture.check = &systemCheck{
		cfg: config{
			phase: string(PhaseBase), signerAddress: common.Address(w.GetAddress()),
			elServices: [2]string{"el-1", "el-2"},
		},
		managedExecutions: [2]managedExecution{first, second}, managedJournal: journal,
		transactions: TransactionRecorderFunc(func(_ context.Context, evidence TransactionEvidence) error {
			fixture.events = append(fixture.events, "record-"+evidence.Label)
			return nil
		}),
		resume: resumeState{
			transactions: make(map[string]common.Hash), managedIntents: make(map[string]ManagedTransactionIntent),
			managedInitialAttempts: make(map[string]time.Time), managedResubmits: make(map[string]time.Time),
			serviceUUIDs: map[string]string{"el-1": "00112233445566778899aabbccddeeff", "el-2": "ffeeddccbbaa99887766554433221100"},
		},
		now: func() time.Time { return fixed },
	}
	return fixture
}

func (fixture *managedFixture) prepare(t *testing.T, label string) ManagedTransactionIntent {
	t.Helper()
	intent, err := fixture.check.prepareManagedTransactionIntent(t.Context(), label, fixture.request)
	if err != nil {
		t.Fatal(err)
	}
	return intent
}

func TestPrepareManagedTransactionIntentUsesSharedDualELBoundary(t *testing.T) {
	fixture := newManagedFixture(t, 0, nil)
	intent := fixture.prepare(t, "managed")
	if intent.StartBlock != 10 || intent.StartBlockHash != fixture.second.head.Hash().Hex() {
		t.Fatalf("shared start boundary = %d/%s, want 10/%s", intent.StartBlock, intent.StartBlockHash, fixture.second.head.Hash())
	}
	if intent.ChainID != "0x539" || intent.Nonce != 7 || intent.Origin != 0 || intent.OriginServiceUUID != fixture.check.resume.serviceUUIDs["el-1"] {
		t.Fatalf("prepared intent = %+v", intent)
	}
	if !strings.HasPrefix(intent.From, "Q") || intent.From[1:] != strings.ToLower(intent.From[1:]) || !strings.HasPrefix(intent.To, "Q") || intent.To[1:] != strings.ToLower(intent.To[1:]) {
		t.Fatalf("managed intent addresses are not lowercase checkpoint canon: from=%s to=%s", intent.From, intent.To)
	}
	if len(fixture.first.sends)+len(fixture.second.sends) != 0 || len(fixture.events) != 0 {
		t.Fatalf("preparation mutated RPC/journal state: sends=%d events=%v", len(fixture.first.sends)+len(fixture.second.sends), fixture.events)
	}
}

func TestPrepareManagedTransactionIntentRejectsDualELDivergence(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*managedFixture)
		match  string
	}{
		{name: "chain ID", mutate: func(f *managedFixture) { f.second.chainID = big.NewInt(1338) }, match: "shared chain ID"},
		{name: "canonical boundary", mutate: func(f *managedFixture) {
			f.second.headers[10] = &types.Header{Number: big.NewInt(10), Extra: []byte("fork")}
		}, match: "shared canonical block"},
		{name: "EL confirmed pending", mutate: func(f *managedFixture) { f.first.pendingNonce++ }, match: "unambiguous shared nonce"},
		{name: "cross EL nonce", mutate: func(f *managedFixture) { f.second.confirmed++; f.second.pendingNonce++ }, match: "unambiguous shared nonce"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newManagedFixture(t, 0, nil)
			test.mutate(fixture)
			_, err := fixture.check.prepareManagedTransactionIntent(t.Context(), "managed", fixture.request)
			if err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("prepare error = %v, want %q", err, test.match)
			}
			if len(fixture.first.sends)+len(fixture.second.sends) != 0 || len(fixture.events) != 0 {
				t.Fatalf("divergent preparation mutated state: events=%v", fixture.events)
			}
		})
	}
}

func TestJournaledManagedTransactionDurabilityGatesAndReplayBudget(t *testing.T) {
	wantFailure := errors.New("checkpoint failed")
	for _, test := range []struct {
		name      string
		configure func(*managedFixture, ManagedTransactionIntent)
		wantEvent []string
		wantError error
		wantMatch string
		wantSends int
	}{
		{
			name: "intent save failure", configure: func(f *managedFixture, _ ManagedTransactionIntent) { f.journal.intentErr = wantFailure },
			wantEvent: []string{"intent"}, wantError: wantFailure,
		},
		{
			name: "initial marker save failure", configure: func(f *managedFixture, _ ManagedTransactionIntent) { f.journal.initialErr = wantFailure },
			wantEvent: []string{"intent", "initial"}, wantError: wantFailure,
		},
		{
			name: "intent only performs initial attempt", configure: func(f *managedFixture, intent ManagedTransactionIntent) {
				f.check.resume.managedIntents[intent.Label] = intent
			},
			wantEvent: []string{"initial", "rpc-EL1", "record-managed"}, wantSends: 1,
		},
		{
			name: "interrupted initial attempt performs sole replay", configure: func(f *managedFixture, intent ManagedTransactionIntent) {
				f.check.resume.managedIntents[intent.Label] = intent
				f.check.resume.managedInitialAttempts[intent.Label] = intent.PreparedAt.Add(time.Second)
			},
			wantEvent: []string{"resubmit", "rpc-EL1", "record-managed"}, wantSends: 1,
		},
		{
			name: "second pre-call interruption retries the exact nonce", configure: func(f *managedFixture, intent ManagedTransactionIntent) {
				f.check.resume.managedIntents[intent.Label] = intent
				f.check.resume.managedInitialAttempts[intent.Label] = intent.PreparedAt.Add(time.Second)
				f.check.resume.managedResubmits[intent.Label] = intent.PreparedAt.Add(2 * time.Second)
			},
			wantEvent: []string{"rpc-EL1", "record-managed"}, wantSends: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newManagedFixture(t, 0, nil)
			intent := fixture.prepare(t, "managed")
			test.configure(fixture, intent)
			fixture.events = nil
			hash, err := fixture.check.sendJournaledManagedTransaction(t.Context(), "managed", fixture.request)
			if test.wantError != nil && !errors.Is(err, test.wantError) {
				t.Fatalf("submission error = %v, want %v", err, test.wantError)
			}
			if test.wantMatch != "" && (err == nil || !strings.Contains(err.Error(), test.wantMatch)) {
				t.Fatalf("submission error = %v, want %q", err, test.wantMatch)
			}
			if test.wantError == nil && test.wantMatch == "" && (err != nil || hash == (common.Hash{})) {
				t.Fatalf("submission = %s, %v", hash, err)
			}
			if got := len(fixture.first.sends) + len(fixture.second.sends); got != test.wantSends {
				t.Fatalf("RPC sends = %d, want %d", got, test.wantSends)
			}
			if !reflect.DeepEqual(fixture.events, test.wantEvent) {
				t.Fatalf("events = %v, want %v", fixture.events, test.wantEvent)
			}
		})
	}
}

func TestManagedTransactionReconciliationRequiresCommonDualELCandidate(t *testing.T) {
	for _, test := range []struct {
		name        string
		configure   func(*managedFixture, ManagedTransactionIntent, *types.Transaction)
		wantFound   bool
		wantError   string
		wantRecords int
	}{
		{
			name: "common pending candidate", wantFound: true, wantRecords: 1,
			configure: func(f *managedFixture, _ ManagedTransactionIntent, tx *types.Transaction) {
				f.first.pending = []*types.Transaction{tx}
				f.second.pending = []*types.Transaction{tx}
			},
		},
		{
			name: "one sided candidate", wantError: "one-sided exact candidate",
			configure: func(f *managedFixture, _ ManagedTransactionIntent, tx *types.Transaction) {
				f.first.pending = []*types.Transaction{tx}
			},
		},
		{
			name: "different hashes", wantError: "different exact candidates",
			configure: func(f *managedFixture, intent ManagedTransactionIntent, tx *types.Transaction) {
				f.first.pending = []*types.Transaction{tx}
				f.second.pending = []*types.Transaction{managedSignedCandidate(t, f.wallet, intent, 200_001)}
			},
		},
		{
			name: "ambiguous candidates", wantError: "ambiguous exact candidates",
			configure: func(f *managedFixture, intent ManagedTransactionIntent, tx *types.Transaction) {
				other := managedSignedCandidate(t, f.wallet, intent, 200_001)
				f.first.pending = []*types.Transaction{tx, other}
				f.second.pending = []*types.Transaction{tx, other}
			},
		},
		{
			name: "nonce advanced without candidate", wantError: "nonce advanced or diverged",
			configure: func(f *managedFixture, _ ManagedTransactionIntent, _ *types.Transaction) { f.second.pendingNonce++ },
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newManagedFixture(t, 0, nil)
			intent := fixture.prepare(t, "managed")
			fixture.check.resume.managedIntents[intent.Label] = intent
			candidate := managedSignedCandidate(t, fixture.wallet, intent, 200_000)
			test.configure(fixture, intent, candidate)
			fixture.events = nil
			hash, err := fixture.check.sendJournaledManagedTransaction(t.Context(), intent.Label, fixture.request)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("reconcile error = %v, want %q", err, test.wantError)
				}
			} else if err != nil || !test.wantFound || hash != candidate.Hash() {
				t.Fatalf("reconcile = %s, %v; want %s", hash, err, candidate.Hash())
			}
			if len(fixture.first.sends)+len(fixture.second.sends) != 0 {
				t.Fatalf("reconciliation sent an RPC: EL1=%d EL2=%d", len(fixture.first.sends), len(fixture.second.sends))
			}
			if got := countManagedEvents(fixture.events, "record-"); got != test.wantRecords {
				t.Fatalf("record events = %v, want count %d", fixture.events, test.wantRecords)
			}
		})
	}
}

func TestManagedTransactionRPCErrorRecoversCommonCandidate(t *testing.T) {
	fixture := newManagedFixture(t, 1, nil)
	intent := fixture.prepare(t, "managed")
	fixture.check.resume.managedIntents[intent.Label] = intent
	candidate := managedSignedCandidate(t, fixture.wallet, intent, 200_000)
	fixture.second.send = func(managedRPCArgs) (common.Hash, error) {
		fixture.first.pending = []*types.Transaction{candidate}
		fixture.second.pending = []*types.Transaction{candidate}
		return common.Hash{}, errors.New("response lost")
	}
	fixture.events = nil
	hash, err := fixture.check.sendJournaledManagedTransaction(t.Context(), intent.Label, fixture.request)
	if err != nil || hash != candidate.Hash() {
		t.Fatalf("recovered submission = %s, %v; want %s", hash, err, candidate.Hash())
	}
	want := []string{"initial", "rpc-EL2", "record-managed"}
	if !reflect.DeepEqual(fixture.events, want) {
		t.Fatalf("events = %v, want %v", fixture.events, want)
	}
}

func TestManagedTransactionRecordFailureResumesFromAcceptedCandidate(t *testing.T) {
	fixture := newManagedFixture(t, 0, nil)
	intent := fixture.prepare(t, "managed")
	candidate := managedSignedCandidate(t, fixture.wallet, intent, 200_000)
	fixture.first.send = func(managedRPCArgs) (common.Hash, error) {
		fixture.first.pending = []*types.Transaction{candidate}
		fixture.second.pending = []*types.Transaction{candidate}
		return candidate.Hash(), nil
	}
	recordFailure := errors.New("transaction checkpoint failed")
	records := 0
	fixture.check.transactions = TransactionRecorderFunc(func(context.Context, TransactionEvidence) error {
		records++
		if records == 1 {
			return recordFailure
		}
		return nil
	})
	if _, err := fixture.check.sendJournaledManagedTransaction(t.Context(), intent.Label, fixture.request); !errors.Is(err, recordFailure) {
		t.Fatalf("first submission error = %v, want %v", err, recordFailure)
	}
	if len(fixture.first.sends) != 1 || len(fixture.check.resume.managedInitialAttempts) != 1 || len(fixture.check.resume.managedResubmits) != 0 {
		t.Fatalf("state after record failure: sends=%d initial=%v resubmits=%v", len(fixture.first.sends), fixture.check.resume.managedInitialAttempts, fixture.check.resume.managedResubmits)
	}
	hash, err := fixture.check.sendJournaledManagedTransaction(t.Context(), intent.Label, fixture.request)
	if err != nil || hash != candidate.Hash() || len(fixture.first.sends) != 1 || records != 2 {
		t.Fatalf("resume = %s, %v; sends=%d records=%d", hash, err, len(fixture.first.sends), records)
	}
}

func TestManagedTransactionPassesImmutableExplicitRPCArguments(t *testing.T) {
	key := common.HexToHash("0x1234")
	accessList := types.AccessList{{Address: common.Address{common.AddressLength - 1: 0x33}, StorageKeys: []common.Hash{key}}}
	for _, test := range []struct {
		name       string
		accessList *types.AccessList
		wantNil    bool
	}{
		{name: "nil access list", accessList: nil, wantNil: true},
		{name: "explicit empty access list", accessList: func() *types.AccessList { value := types.AccessList{}; return &value }()},
		{name: "populated access list", accessList: &accessList},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newManagedFixture(t, 1, test.accessList)
			hash, err := fixture.check.sendJournaledManagedTransaction(t.Context(), "managed", fixture.request)
			if err != nil || hash != fixture.second.sendHash {
				t.Fatalf("submission = %s, %v", hash, err)
			}
			if len(fixture.first.sends) != 0 || len(fixture.second.sends) != 1 {
				t.Fatalf("origin sends = EL1:%d EL2:%d", len(fixture.first.sends), len(fixture.second.sends))
			}
			args := fixture.second.sends[0]
			if args.From != fixture.check.cfg.signerAddress || args.To == nil || *args.To != *fixture.request.to || (*big.Int)(args.Value).Cmp(fixture.request.value) != 0 || !reflect.DeepEqual([]byte(args.Input), fixture.request.input) || args.Nonce == nil || uint64(*args.Nonce) != 7 || args.ChainID == nil || (*big.Int)(args.ChainID).Cmp(big.NewInt(1337)) != 0 {
				t.Fatalf("managed RPC args changed: %+v", args)
			}
			if (args.AccessList == nil) != test.wantNil || !reflect.DeepEqual(args.AccessList, test.accessList) {
				t.Fatalf("access list = %#v, want %#v", args.AccessList, test.accessList)
			}
		})
	}
}

func TestManagedAccessListRejectsInvalidAddressAndStorageKey(t *testing.T) {
	validAddress := (common.Address{common.AddressLength - 1: 1}).Hex()
	for _, list := range [][]ManagedAccessTuple{
		{{Address: "Q01"}},
		{{Address: validAddress, StorageKeys: []string{"0x01"}}},
		{{Address: validAddress, StorageKeys: []string{"0X" + strings.Repeat("00", common.HashLength)}}},
	} {
		if _, err := managedAccessList(list); err == nil {
			t.Fatalf("invalid managed access list accepted: %+v", list)
		}
	}
}

func managedSignedCandidate(t *testing.T, w wallet.Wallet, intent ManagedTransactionIntent, gas uint64) *types.Transaction {
	t.Helper()
	chainID, err := hexutil.DecodeBig(intent.ChainID)
	if err != nil {
		t.Fatal(err)
	}
	value, err := hexutil.DecodeBig(intent.Value)
	if err != nil {
		t.Fatal(err)
	}
	data, err := hexutil.Decode(intent.Input)
	if err != nil {
		t.Fatal(err)
	}
	var to *common.Address
	if intent.To != "" {
		address, err := common.NewAddressFromString(intent.To)
		if err != nil {
			t.Fatal(err)
		}
		to = &address
	}
	accessList, err := managedAccessList(intent.AccessList)
	if err != nil {
		t.Fatal(err)
	}
	var list types.AccessList
	if accessList != nil {
		list = *accessList
	}
	unsigned := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: intent.Nonce, GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(2),
		Gas: gas, To: to, Value: value, Data: data, AccessList: list,
	})
	signed, err := types.SignTx(unsigned, types.LatestSignerForChainID(chainID), w)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func countManagedEvents(events []string, prefix string) int {
	count := 0
	for _, event := range events {
		if strings.HasPrefix(event, prefix) {
			count++
		}
	}
	return count
}

func (fixture *managedFixture) String() string {
	return fmt.Sprintf("EL1 sends=%d EL2 sends=%d events=%v", len(fixture.first.sends), len(fixture.second.sends), fixture.events)
}
