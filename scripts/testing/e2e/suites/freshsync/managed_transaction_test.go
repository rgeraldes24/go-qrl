// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package freshsync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
	kurtosisapi "github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

const managedTestSeed = "010000a7b1a3005d9e110009c48d45deb43f0a0e31846ed2c5aaefb6d4238040ad4c08794ffe65585c13eb6948c2faf6db90c2"

type managedClientStub struct {
	name                string
	chainID             *big.Int
	head                *types.Header
	headers             map[uint64]*types.Header
	blocks              map[uint64]*types.Block
	confirmed           uint64
	historicalNonce     uint64
	pending             uint64
	pendingTransactions []*types.Transaction
	sendHash            common.Hash
	sendErr             error
	onSend              func(managedTransactionRequest) (common.Hash, error)
	sends               []managedTransactionRequest
	events              *[]string
}

func (client *managedClientStub) ChainID(context.Context) (*big.Int, error) {
	return new(big.Int).Set(client.chainID), nil
}

func (client *managedClientStub) HeaderByNumber(_ context.Context, number *big.Int) (*types.Header, error) {
	if number == nil {
		return client.head, nil
	}
	if client.head == nil || client.head.Number == nil || number.Cmp(client.head.Number) > 0 {
		return nil, fmt.Errorf("header %d is ahead of the current head", number.Uint64())
	}
	header, ok := client.headers[number.Uint64()]
	if !ok {
		return nil, fmt.Errorf("header %d not found", number.Uint64())
	}
	return header, nil
}

func (client *managedClientStub) BlockByNumber(_ context.Context, number *big.Int) (*types.Block, error) {
	if block, ok := client.blocks[number.Uint64()]; ok {
		return block, nil
	}
	header, ok := client.headers[number.Uint64()]
	if !ok {
		return nil, fmt.Errorf("block %d not found", number.Uint64())
	}
	return types.NewBlockWithHeader(header), nil
}

func (client *managedClientStub) NonceAt(_ context.Context, _ common.Address, number *big.Int) (uint64, error) {
	if number != nil {
		return client.historicalNonce, nil
	}
	return client.confirmed, nil
}

func (client *managedClientStub) PendingNonceAt(context.Context, common.Address) (uint64, error) {
	return client.pending, nil
}

func (client *managedClientStub) PendingTransactions(context.Context) ([]*types.Transaction, error) {
	return append([]*types.Transaction(nil), client.pendingTransactions...), nil
}

func (client *managedClientStub) SendManagedTransaction(_ context.Context, request managedTransactionRequest) (common.Hash, error) {
	client.sends = append(client.sends, request)
	if client.events != nil {
		*client.events = append(*client.events, "send:"+client.name)
	}
	if client.onSend != nil {
		return client.onSend(request)
	}
	return client.sendHash, client.sendErr
}

type managedRecorderStub struct {
	events      *[]string
	intent      lifecycle.ManagedTransactionIntent
	hashes      map[string]string
	intentErr   error
	initialErr  error
	resubmitErr error
	hashErr     error
}

func (recorder *managedRecorderStub) RecordManagedTransactionIntent(_ context.Context, _ string, intent lifecycle.ManagedTransactionIntent) error {
	if recorder.events != nil {
		*recorder.events = append(*recorder.events, "intent")
	}
	if recorder.intentErr != nil {
		return recorder.intentErr
	}
	recorder.intent = intent
	return nil
}

func (recorder *managedRecorderStub) RecordManagedTransactionInitialAttempt(context.Context, string) error {
	if recorder.events != nil {
		*recorder.events = append(*recorder.events, "initial")
	}
	return recorder.initialErr
}

func (recorder *managedRecorderStub) RecordManagedTransactionResubmit(context.Context, string) error {
	if recorder.events != nil {
		*recorder.events = append(*recorder.events, "resubmit")
	}
	return recorder.resubmitErr
}

func (recorder *managedRecorderStub) RecordTransaction(_ context.Context, label, hash string) error {
	if recorder.events != nil {
		*recorder.events = append(*recorder.events, "hash")
	}
	if recorder.hashErr != nil {
		return recorder.hashErr
	}
	if recorder.hashes == nil {
		recorder.hashes = make(map[string]string)
	}
	recorder.hashes[label] = hash
	return nil
}

type managedFixture struct {
	check    *freshSyncCheck
	clients  [2]*managedClientStub
	recorder *managedRecorderStub
	wallet   wallet.Wallet
	header   *types.Header
	now      time.Time
}

func newManagedFixture(t *testing.T) managedFixture {
	t.Helper()
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	w, err := wallet.RestoreFromSeedHex(managedTestSeed)
	if err != nil {
		t.Fatal(err)
	}
	cfg.SignerAddress = common.Address(w.GetAddress())
	cfg.Recipient = common.Address{0x42}
	fake, enclave := freshSyncFake(t)
	if err := fake.AddService(enclave, kurtosisapi.Service{Name: cfg.ReferenceService, UUID: testELUUID}); err != nil {
		t.Fatal(err)
	}
	header := &types.Header{Number: big.NewInt(12), Extra: []byte("managed-start")}
	events := make([]string, 0, 8)
	clients := [2]*managedClientStub{}
	for index := range clients {
		clients[index] = &managedClientStub{
			name: "EL" + fmt.Sprint(index+1), chainID: big.NewInt(1337), head: header,
			headers: map[uint64]*types.Header{12: header}, blocks: make(map[uint64]*types.Block),
			confirmed: 7, historicalNonce: 7, pending: 7, sendHash: common.BytesToHash([]byte{byte(index + 1)}), events: &events,
		}
	}
	recorder := &managedRecorderStub{events: &events}
	now := time.Date(2026, 7, 21, 12, 30, 0, 0, time.UTC)
	check := &freshSyncCheck{
		cfg: cfg, client: fake, enclave: enclave, txRecord: recorder, managedRecord: recorder,
		managedClients: [2]managedExecutionClient{clients[0], clients[1]}, now: func() time.Time { return now },
	}
	return managedFixture{check: check, clients: clients, recorder: recorder, wallet: w, header: header, now: now}
}

func (fixture managedFixture) prepare(t *testing.T) (lifecycle.ManagedTransactionIntent, managedTransactionRequest) {
	t.Helper()
	label := transferTransactionLabel(fixture.check.cfg.SyncMode)
	intent, err := fixture.check.prepareManagedIntent(t.Context(), label)
	if err != nil {
		t.Fatal(err)
	}
	request, err := managedRequestFromIntent(intent)
	if err != nil {
		t.Fatal(err)
	}
	return intent, request
}

func signedManagedTransaction(t *testing.T, w wallet.Wallet, request managedTransactionRequest, feeCap int64) *types.Transaction {
	t.Helper()
	var to *common.Address
	if request.To != nil {
		copy := *request.To
		to = &copy
	}
	transaction := types.NewTx(&types.DynamicFeeTx{
		ChainID: new(big.Int).Set((*big.Int)(request.ChainID)), Nonce: uint64(request.Nonce),
		GasTipCap: big.NewInt(1), GasFeeCap: big.NewInt(feeCap), Gas: 100_000,
		To: to, Value: new(big.Int).Set((*big.Int)(request.Value)), Data: append([]byte(nil), request.Input...),
		AccessList: append(types.AccessList(nil), request.AccessList...),
	})
	signed, err := types.SignTx(transaction, types.LatestSignerForChainID(transaction.ChainId()), w)
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func TestManagedTransferPersistsCompleteIntentAndAttemptBeforeRPC(t *testing.T) {
	fixture := newManagedFixture(t)
	hash, err := fixture.check.managedTransferForVerification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if hash != fixture.clients[0].sendHash {
		t.Fatalf("hash = %s, want %s", hash, fixture.clients[0].sendHash)
	}
	wantEvents := []string{"intent", "initial", "send:EL1", "hash"}
	if !slices.Equal(*fixture.recorder.events, wantEvents) {
		t.Fatalf("events = %v, want %v", *fixture.recorder.events, wantEvents)
	}
	if len(fixture.clients[0].sends) != 1 || len(fixture.clients[1].sends) != 0 {
		t.Fatalf("send counts = %d/%d, want 1/0", len(fixture.clients[0].sends), len(fixture.clients[1].sends))
	}
	intent := fixture.recorder.intent
	request := fixture.clients[0].sends[0]
	if intent.OriginServiceName != fixture.check.cfg.ReferenceService || intent.OriginServiceUUID != testELUUID || intent.ChainID != "0x539" || intent.Nonce != 7 || intent.StartBlock != 12 || intent.StartBlockHash != fixture.header.Hash().Hex() || !intent.PreparedAt.Equal(fixture.now) {
		t.Fatalf("incomplete managed intent: %+v", intent)
	}
	if intent.From != canonicalManagedAddress(fixture.check.cfg.SignerAddress) || intent.To != canonicalManagedAddress(fixture.check.cfg.Recipient) || intent.Input != "0x" || intent.AccessList == nil || len(intent.AccessList) != 0 {
		t.Fatalf("managed intent request evidence = %+v", intent)
	}
	if request.Nonce != 7 || (*big.Int)(request.ChainID).Cmp(big.NewInt(1337)) != 0 || request.To == nil || *request.To != fixture.check.cfg.Recipient || request.AccessList == nil || len(request.AccessList) != 0 || len(request.Input) != 0 {
		t.Fatalf("RPC request omitted explicit fields: %+v", request)
	}
}

func TestManagedTransferRecorderFailuresPreventSubmission(t *testing.T) {
	for _, test := range []struct {
		name string
		set  func(*managedRecorderStub)
		want string
	}{
		{name: "intent", set: func(recorder *managedRecorderStub) { recorder.intentErr = errors.New("intent disk full") }, want: "persist managed transaction intent"},
		{name: "initial attempt", set: func(recorder *managedRecorderStub) { recorder.initialErr = errors.New("attempt disk full") }, want: "persist managed transaction initial-attempt marker"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newManagedFixture(t)
			test.set(fixture.recorder)
			_, err := fixture.check.managedTransferForVerification(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if len(fixture.clients[0].sends) != 0 || len(fixture.clients[1].sends) != 0 {
				t.Fatalf("recorder failure reached RPC: sends=%d/%d", len(fixture.clients[0].sends), len(fixture.clients[1].sends))
			}
		})
	}
}

func TestManagedTransferRecoversMatchingPendingTransactionOnBothELs(t *testing.T) {
	fixture := newManagedFixture(t)
	intent, request := fixture.prepare(t)
	transaction := signedManagedTransaction(t, fixture.wallet, request, 2)
	for _, client := range fixture.clients {
		client.pendingTransactions = []*types.Transaction{transaction}
		client.pending = intent.Nonce + 1
	}
	fixture.check.recordedIntent = &intent
	fixture.check.initialAttempt = true
	hash, err := fixture.check.managedTransferForVerification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if hash != transaction.Hash() || len(fixture.clients[0].sends) != 0 {
		t.Fatalf("recovered hash/sends = %s/%d, want %s/0", hash, len(fixture.clients[0].sends), transaction.Hash())
	}
	if !slices.Equal(*fixture.recorder.events, []string{"hash"}) {
		t.Fatalf("events = %v, want hash only", *fixture.recorder.events)
	}
}

func TestManagedTransferRecoversCanonicalAndPendingAgreement(t *testing.T) {
	fixture := newManagedFixture(t)
	intent, request := fixture.prepare(t)
	transaction := signedManagedTransaction(t, fixture.wallet, request, 2)
	canonicalHead := &types.Header{ParentHash: fixture.header.Hash(), Number: big.NewInt(13), Extra: []byte("included")}
	fixture.clients[0].head = canonicalHead
	fixture.clients[0].headers[13] = canonicalHead
	fixture.clients[0].blocks[13] = types.NewBlockWithHeader(canonicalHead).WithBody(types.Body{Transactions: []*types.Transaction{transaction}})
	fixture.clients[0].confirmed = intent.Nonce + 1
	fixture.clients[0].pending = intent.Nonce + 1
	fixture.clients[1].pendingTransactions = []*types.Transaction{transaction}
	fixture.clients[1].pending = intent.Nonce + 1
	fixture.check.recordedIntent = &intent
	fixture.check.initialAttempt = true
	hash, err := fixture.check.managedTransferForVerification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if hash != transaction.Hash() || len(fixture.clients[0].sends) != 0 {
		t.Fatalf("canonical/pending recovery = %s with %d sends", hash, len(fixture.clients[0].sends))
	}
}

func TestManagedTransferDurableResubmitMarkerSurvivesPreCallInterruption(t *testing.T) {
	fixture := newManagedFixture(t)
	intent, _ := fixture.prepare(t)
	fixture.check.recordedIntent = &intent
	fixture.check.initialAttempt = true
	hash, err := fixture.check.managedTransferForVerification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if hash != fixture.clients[0].sendHash || !slices.Equal(*fixture.recorder.events, []string{"resubmit", "send:EL1", "hash"}) {
		t.Fatalf("single resubmit hash/events = %s/%v", hash, *fixture.recorder.events)
	}

	resumed := newManagedFixture(t)
	resumedIntent, _ := resumed.prepare(t)
	resumed.check.recordedIntent = &resumedIntent
	resumed.check.initialAttempt = true
	resumed.check.resubmitted = true
	resumedHash, err := resumed.check.managedTransferForVerification(t.Context())
	if err != nil || resumedHash != resumed.clients[0].sendHash {
		t.Fatalf("pre-call interrupted replay = %s, %v", resumedHash, err)
	}
	if len(resumed.clients[0].sends) != 1 || !slices.Equal(*resumed.recorder.events, []string{"send:EL1", "hash"}) {
		t.Fatalf("pre-call interrupted replay sends/events=%d/%v", len(resumed.clients[0].sends), *resumed.recorder.events)
	}
}

func TestManagedTransferRecoversAfterSubmissionResponseLoss(t *testing.T) {
	fixture := newManagedFixture(t)
	fixture.clients[0].onSend = func(request managedTransactionRequest) (common.Hash, error) {
		transaction := signedManagedTransaction(t, fixture.wallet, request, 2)
		for _, client := range fixture.clients {
			client.pendingTransactions = []*types.Transaction{transaction}
			client.pending = uint64(request.Nonce) + 1
		}
		return common.Hash{}, errors.New("connection reset after submit")
	}
	hash, err := fixture.check.managedTransferForVerification(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if hash == (common.Hash{}) || !slices.Equal(*fixture.recorder.events, []string{"intent", "initial", "send:EL1", "hash"}) {
		t.Fatalf("response-loss recovery hash/events = %s/%v", hash, *fixture.recorder.events)
	}
}

func TestManagedTransferRecoveryFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name    string
		arrange func(*testing.T, managedFixture, lifecycle.ManagedTransactionIntent, managedTransactionRequest)
		want    string
	}{
		{
			name: "mismatched transaction", want: "mismatched transaction",
			arrange: func(t *testing.T, fixture managedFixture, intent lifecycle.ManagedTransactionIntent, request managedTransactionRequest) {
				wrong := request
				wrong.Value = (*hexutil.Big)(new(big.Int).Add((*big.Int)(request.Value), big.NewInt(1)))
				transaction := signedManagedTransaction(t, fixture.wallet, wrong, 2)
				for _, client := range fixture.clients {
					client.pendingTransactions = []*types.Transaction{transaction}
					client.pending = intent.Nonce + 1
				}
			},
		},
		{
			name: "nonce consumed", want: "was consumed without an exact transaction match",
			arrange: func(_ *testing.T, fixture managedFixture, intent lifecycle.ManagedTransactionIntent, _ managedTransactionRequest) {
				for _, client := range fixture.clients {
					client.confirmed = intent.Nonce + 1
					client.pending = intent.Nonce + 1
				}
			},
		},
		{
			name: "one-sided visibility", want: "disagree on managed transaction visibility",
			arrange: func(t *testing.T, fixture managedFixture, intent lifecycle.ManagedTransactionIntent, request managedTransactionRequest) {
				fixture.clients[0].pendingTransactions = []*types.Transaction{signedManagedTransaction(t, fixture.wallet, request, 2)}
				fixture.clients[0].pending = intent.Nonce + 1
			},
		},
		{
			name: "different hashes", want: "recovered different managed transaction hashes",
			arrange: func(t *testing.T, fixture managedFixture, intent lifecycle.ManagedTransactionIntent, request managedTransactionRequest) {
				fixture.clients[0].pendingTransactions = []*types.Transaction{signedManagedTransaction(t, fixture.wallet, request, 2)}
				fixture.clients[1].pendingTransactions = []*types.Transaction{signedManagedTransaction(t, fixture.wallet, request, 3)}
				for _, client := range fixture.clients {
					client.pending = intent.Nonce + 1
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newManagedFixture(t)
			intent, request := fixture.prepare(t)
			fixture.check.recordedIntent = &intent
			fixture.check.initialAttempt = true
			test.arrange(t, fixture, intent, request)
			_, err := fixture.check.managedTransferForVerification(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if len(fixture.clients[0].sends) != 0 || len(fixture.clients[1].sends) != 0 {
				t.Fatalf("fail-closed recovery sent transaction: %d/%d", len(fixture.clients[0].sends), len(fixture.clients[1].sends))
			}
		})
	}
}

func TestManagedIntentResumeFailsClosedOnImmutableDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(managedFixture, *lifecycle.ManagedTransactionIntent)
		want   string
	}{
		{name: "origin name", mutate: func(_ managedFixture, intent *lifecycle.ManagedTransactionIntent) {
			intent.OriginServiceName = "different-origin"
		}, want: "origin changed"},
		{name: "origin full UUID", mutate: func(_ managedFixture, intent *lifecycle.ManagedTransactionIntent) {
			intent.OriginServiceUUID = strings.Repeat("3", 32)
		}, want: "resolve managed transaction origin"},
		{name: "chain ID", mutate: func(_ managedFixture, intent *lifecycle.ManagedTransactionIntent) {
			intent.ChainID = "0x53a"
		}, want: "chain ID differs"},
		{name: "recipient", mutate: func(_ managedFixture, intent *lifecycle.ManagedTransactionIntent) {
			intent.To = canonicalManagedAddress(common.Address{0x99})
		}, want: "request differs"},
		{name: "configured recipient", mutate: func(fixture managedFixture, _ *lifecycle.ManagedTransactionIntent) {
			fixture.check.cfg.Recipient = common.Address{0x99}
		}, want: "request differs"},
		{name: "value", mutate: func(_ managedFixture, intent *lifecycle.ManagedTransactionIntent) {
			intent.Value = "0x2"
		}, want: "request differs"},
		{name: "configured value", mutate: func(fixture managedFixture, _ *lifecycle.ManagedTransactionIntent) {
			fixture.check.cfg.TransferValue++
		}, want: "request differs"},
		{name: "input", mutate: func(_ managedFixture, intent *lifecycle.ManagedTransactionIntent) {
			intent.Input = "0x00"
		}, want: "request differs"},
		{name: "omitted access list", mutate: func(_ managedFixture, intent *lifecycle.ManagedTransactionIntent) {
			intent.AccessList = nil
		}, want: "exact canonical empty list"},
		{name: "non-empty access list", mutate: func(fixture managedFixture, intent *lifecycle.ManagedTransactionIntent) {
			intent.AccessList = []lifecycle.ManagedAccessTuple{{Address: canonicalManagedAddress(fixture.check.cfg.Recipient), StorageKeys: []string{common.Hash{}.Hex()}}}
		}, want: "exact canonical empty list"},
		{name: "nonce", mutate: func(_ managedFixture, intent *lifecycle.ManagedTransactionIntent) {
			intent.Nonce++
		}, want: "sender nonce at managed intent start block"},
		{name: "start hash", mutate: func(_ managedFixture, intent *lifecycle.ManagedTransactionIntent) {
			intent.StartBlockHash = common.BytesToHash([]byte("different")).Hex()
		}, want: "canonical start block differs"},
		{name: "canonical start block", mutate: func(fixture managedFixture, _ *lifecycle.ManagedTransactionIntent) {
			fixture.clients[1].headers[12] = &types.Header{Number: big.NewInt(12), Extra: []byte("reorged")}
		}, want: "canonical start block differs"},
		{name: "head behind start boundary", mutate: func(fixture managedFixture, _ *lifecycle.ManagedTransactionIntent) {
			fixture.clients[1].head = &types.Header{Number: big.NewInt(11), Extra: []byte("behind")}
		}, want: "ahead of the current head"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newManagedFixture(t)
			intent, _ := fixture.prepare(t)
			test.mutate(fixture, &intent)
			fixture.check.recordedIntent = &intent
			fixture.check.initialAttempt = true
			_, err := fixture.check.managedTransferForVerification(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
			if len(fixture.clients[0].sends) != 0 || len(fixture.clients[1].sends) != 0 || len(*fixture.recorder.events) != 0 {
				t.Fatalf("immutable drift crossed boundary: sends=%d/%d events=%v", len(fixture.clients[0].sends), len(fixture.clients[1].sends), *fixture.recorder.events)
			}
		})
	}
}

func TestManagedIntentUsesSharedCanonicalHeightWhenHeadsAreSkewed(t *testing.T) {
	fixture := newManagedFixture(t)
	referenceHead := &types.Header{ParentHash: fixture.header.Hash(), Number: big.NewInt(13), Extra: []byte("reference-ahead")}
	fixture.clients[0].head = referenceHead
	fixture.clients[0].headers[13] = referenceHead
	intent, _ := fixture.prepare(t)
	if intent.StartBlock != 12 || intent.StartBlockHash != fixture.header.Hash().Hex() {
		t.Fatalf("shared start boundary = %d/%s, want 12/%s", intent.StartBlock, intent.StartBlockHash, fixture.header.Hash())
	}
}

func TestRunRejectsInconsistentManagedCheckpointBeforeNetworkMutation(t *testing.T) {
	fixture := newManagedFixture(t)
	intent, _ := fixture.prepare(t)
	label := intent.Label
	fixture.check.cfg.Enclave = fixture.check.enclave.UUID
	tests := []struct {
		name   string
		change func(*Options)
		want   string
	}{
		{name: "attempt without intent", change: func(options *Options) {
			options.ManagedTransactionInitialAttempts[label] = fixture.now
		}, want: "attempt marker without immutable intent"},
		{name: "resubmit without initial", change: func(options *Options) {
			options.RecordedManagedTransactionIntents[label] = intent
			options.ManagedTransactionResubmits[label] = fixture.now
		}, want: "resubmit marker without an initial-attempt marker"},
		{name: "hash without intent", change: func(options *Options) {
			options.RecordedTransactions[label] = common.BytesToHash([]byte{1}).Hex()
		}, want: "submitted hash without immutable intent"},
		{name: "hash and intent without initial", change: func(options *Options) {
			options.RecordedManagedTransactionIntents[label] = intent
			options.RecordedTransactions[label] = common.BytesToHash([]byte{1}).Hex()
		}, want: "submitted hash and immutable intent without an initial-attempt marker"},
		{name: "missing durable recorders", change: func(options *Options) {
			options.TransactionRecorder = nil
			options.ManagedTransactionRecorder = nil
		}, want: "requires durable intent, attempt, and hash recorders"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			options := Options{
				Client: fixture.check.client, Enclave: fixture.check.enclave,
				TransactionRecorder: fixture.recorder, ManagedTransactionRecorder: fixture.recorder,
				RecordedTransactions: make(map[string]string), RecordedManagedTransactionIntents: make(map[string]lifecycle.ManagedTransactionIntent),
				ManagedTransactionInitialAttempts: make(map[string]time.Time), ManagedTransactionResubmits: make(map[string]time.Time),
			}
			test.change(&options)
			err := Run(t.Context(), fixture.check.cfg, options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run error = %v, want %q", err, test.want)
			}
			for _, call := range fixture.check.client.(*kurtosisapi.FakeClient).Calls {
				if strings.HasPrefix(call, "remove:") || strings.HasPrefix(call, "start:") || strings.HasPrefix(call, "stop:") {
					t.Fatalf("inconsistent checkpoint mutated network: calls=%v", fixture.check.client.(*kurtosisapi.FakeClient).Calls)
				}
			}
		})
	}
}

func TestRunValidatesManagedCheckpointBeforeTemporaryServiceRecoveryMutation(t *testing.T) {
	fixture := newManagedFixture(t)
	intent, _ := fixture.prepare(t)
	label := intent.Label
	fixture.check.cfg.Enclave = fixture.check.enclave.UUID
	fake := fixture.check.client.(*kurtosisapi.FakeClient)
	if err := fake.AddService(fixture.check.enclave, kurtosisapi.Service{
		Name: fixture.check.cfg.FreshELService, UUID: testCLUUID,
	}); err != nil {
		t.Fatal(err)
	}

	store, recorder := newTemporaryServiceCheckpoint(t, fixture.check.enclave, fixture.now)
	state, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	active := "fresh-" + fixture.check.cfg.SyncMode
	state.CurrentStage = &active
	state.Attempts = []lifecycle.Attempt{{Stage: active, Attempt: 1, StartedAt: fixture.now}}
	state.TemporaryServices = make(map[string]string)
	state.TemporaryServices[fixture.check.cfg.FreshELService] = testCLUUID
	state.UpdatedAt = fixture.now
	if err := store.Save(state); err != nil {
		t.Fatal(err)
	}
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}

	err = Run(t.Context(), fixture.check.cfg, Options{
		Client: fake, Enclave: fixture.check.enclave, Recorder: recorder,
		TransactionRecorder: fixture.recorder, ManagedTransactionRecorder: fixture.recorder,
		RecordedTransactions:              map[string]string{label: common.BytesToHash([]byte{1}).Hex()},
		RecordedManagedTransactionIntents: map[string]lifecycle.ManagedTransactionIntent{label: intent},
		ManagedTransactionInitialAttempts: make(map[string]time.Time),
		ManagedTransactionResubmits:       make(map[string]time.Time),
	})
	if err == nil || !strings.Contains(err.Error(), "submitted hash and immutable intent without an initial-attempt marker") {
		t.Fatalf("Run error = %v, want managed-journal validation failure", err)
	}
	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatalf("invalid managed checkpoint mutated lifecycle state:\nbefore=%+v\nafter=%+v", before, after)
	}
	if _, err := fake.Service(t.Context(), fixture.check.enclave, testCLUUID); err != nil {
		t.Fatalf("invalid managed checkpoint removed the recoverable temporary service: %v", err)
	}
	for _, call := range fake.Calls {
		if strings.HasPrefix(call, "remove:") || strings.HasPrefix(call, "start:") || strings.HasPrefix(call, "stop:") {
			t.Fatalf("invalid managed checkpoint mutated Kurtosis: calls=%v", fake.Calls)
		}
	}
}

func TestRunRequiresTemporaryServiceReconcilerBeforeLegacyCleanup(t *testing.T) {
	fixture := newManagedFixture(t)
	fixture.check.cfg.Enclave = fixture.check.enclave.UUID
	fake := fixture.check.client.(*kurtosisapi.FakeClient)
	if err := fake.AddService(fixture.check.enclave, kurtosisapi.Service{
		Name: fixture.check.cfg.FreshELService, UUID: testCLUUID,
	}); err != nil {
		t.Fatal(err)
	}
	recorder := new(temporaryLifecycleRecorderStub)
	err := Run(t.Context(), fixture.check.cfg, Options{
		Client: fake, Enclave: fixture.check.enclave, Recorder: recorder,
		TransactionRecorder: fixture.recorder, ManagedTransactionRecorder: fixture.recorder,
		RecordedServices:     map[string]string{fixture.check.cfg.FreshELService: testCLUUID},
		RecordedTransactions: make(map[string]string), RecordedManagedTransactionIntents: make(map[string]lifecycle.ManagedTransactionIntent),
		ManagedTransactionInitialAttempts: make(map[string]time.Time), ManagedTransactionResubmits: make(map[string]time.Time),
	})
	if err == nil || !strings.Contains(err.Error(), "legacy temporary services require a recorder that can safely reconcile UUIDs") {
		t.Fatalf("Run error = %v, want pre-cleanup reconciler validation", err)
	}
	if len(recorder.services) != 0 || len(recorder.intents) != 0 {
		t.Fatalf("missing reconciler mutated checkpoint recorder: services=%v intents=%v", recorder.services, recorder.intents)
	}
	if _, err := fake.Service(t.Context(), fixture.check.enclave, testCLUUID); err != nil {
		t.Fatalf("missing reconciler was detected only after cleanup: %v", err)
	}
	for _, call := range fake.Calls {
		if strings.HasPrefix(call, "remove:") || strings.HasPrefix(call, "start:") || strings.HasPrefix(call, "stop:") {
			t.Fatalf("missing reconciler mutated Kurtosis: calls=%v", fake.Calls)
		}
	}
}

func TestManagedTransactionAccessListSemanticsAreExact(t *testing.T) {
	fixture := newManagedFixture(t)
	_, request := fixture.prepare(t)
	matching := signedManagedTransaction(t, fixture.wallet, request, 2)
	if err := validateManagedTransaction(matching, request); err != nil {
		t.Fatalf("exact empty access list rejected: %v", err)
	}
	omitted := request
	omitted.AccessList = nil
	if err := validateManagedTransaction(matching, omitted); err == nil || !strings.Contains(err.Error(), "access list") {
		t.Fatalf("omitted versus explicit-empty access list error = %v", err)
	}

	key := common.BytesToHash([]byte{1})
	request.AccessList = types.AccessList{{Address: fixture.check.cfg.Recipient, StorageKeys: []common.Hash{key}}}
	listed := signedManagedTransaction(t, fixture.wallet, request, 2)
	if err := validateManagedTransaction(listed, request); err != nil {
		t.Fatalf("exact non-empty access list rejected: %v", err)
	}
	wrong := request
	wrong.AccessList = types.AccessList{{Address: fixture.check.cfg.Recipient, StorageKeys: []common.Hash{common.BytesToHash([]byte{2})}}}
	if err := validateManagedTransaction(listed, wrong); err == nil || !strings.Contains(err.Error(), "access list") {
		t.Fatalf("wrong storage key error = %v", err)
	}
	if reflect.DeepEqual(canonicalManagedAccessList(nil), canonicalManagedAccessList(types.AccessList{})) {
		t.Fatal("canonical access-list evidence collapsed omitted and explicit-empty semantics")
	}
}

func TestManagedExecutionAdapterUsesPublicPendingRPCAndExplicitWireFields(t *testing.T) {
	fixture := newManagedFixture(t)
	_, request := fixture.prepare(t)
	transaction := signedManagedTransaction(t, fixture.wallet, request, 2)
	wantHash := common.BytesToHash([]byte("managed-wire-hash"))
	var methods []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, incoming *http.Request) {
		defer incoming.Body.Close()
		var envelope struct {
			JSONRPC string            `json:"jsonrpc"`
			ID      json.RawMessage   `json:"id"`
			Method  string            `json:"method"`
			Params  []json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(incoming.Body).Decode(&envelope); err != nil {
			t.Errorf("decode JSON-RPC request: %v", err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		methods = append(methods, envelope.Method)
		writer.Header().Set("Content-Type", "application/json")
		switch envelope.Method {
		case "qrl_pendingTransactions":
			if len(envelope.Params) != 0 {
				t.Errorf("pending params = %s, want none", envelope.Params)
			}
			_ = json.NewEncoder(writer).Encode(struct {
				JSONRPC string               `json:"jsonrpc"`
				ID      json.RawMessage      `json:"id"`
				Result  []*types.Transaction `json:"result"`
			}{JSONRPC: "2.0", ID: envelope.ID, Result: []*types.Transaction{transaction}})
		case "qrl_sendTransaction":
			if len(envelope.Params) != 1 {
				t.Errorf("send params = %d, want one", len(envelope.Params))
				writer.WriteHeader(http.StatusBadRequest)
				return
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(envelope.Params[0], &fields); err != nil {
				t.Errorf("decode send arguments: %v", err)
			}
			for field, want := range map[string]string{
				"nonce": "\"0x7\"", "chainId": "\"0x539\"", "value": "\"0x1\"", "input": "\"0x\"", "accessList": "[]",
			} {
				if got := string(fields[field]); got != want {
					t.Errorf("%s wire value = %s, want %s", field, got, want)
				}
			}
			for _, field := range []string{"from", "to"} {
				var address string
				if err := json.Unmarshal(fields[field], &address); err != nil || len(address) != 1+2*common.AddressLength || address[0] != 'Q' {
					t.Errorf("%s wire address = %q (%v), want canonical 64-byte Q address", field, address, err)
				}
			}
			_ = json.NewEncoder(writer).Encode(struct {
				JSONRPC string          `json:"jsonrpc"`
				ID      json.RawMessage `json:"id"`
				Result  common.Hash     `json:"result"`
			}{JSONRPC: "2.0", ID: envelope.ID, Result: wantHash})
		default:
			t.Errorf("unexpected JSON-RPC method %q", envelope.Method)
			writer.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client, err := qrlclient.DialContext(t.Context(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	adapter := qrlManagedExecutionClient{client: client}
	pending, err := adapter.PendingTransactions(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].Hash() != transaction.Hash() {
		t.Fatalf("decoded pending transactions = %v, want %s", pending, transaction.Hash())
	}
	if hash, err := adapter.SendManagedTransaction(t.Context(), request); err != nil || hash != wantHash {
		t.Fatalf("send hash/error = %s/%v, want %s", hash, err, wantHash)
	}
	if !slices.Equal(methods, []string{"qrl_pendingTransactions", "qrl_sendTransaction"}) {
		t.Fatalf("RPC methods = %v", methods)
	}
}

func TestManagedTransferResumesFromSameCheckpointAfterLostResponse(t *testing.T) {
	fixture := newManagedFixture(t)
	label := transferTransactionLabel(fixture.check.cfg.SyncMode)
	state := lifecycle.NewCheckpoint(
		"run-managed", strings.Repeat("a", 40), strings.Repeat("b", 64), t.TempDir(), strings.Repeat("c", 64), fixture.check.enclave, fixture.now,
	)
	store := lifecycle.Store{Path: t.TempDir() + "/checkpoint.json", StageOrder: []string{"fresh-snap"}, Now: func() time.Time { return fixture.now }}
	if err := store.Create(state); err != nil {
		t.Fatal(err)
	}
	recorder := CheckpointRecorder{Store: store, Now: func() time.Time { return fixture.now }}
	fixture.check.managedRecord = recorder
	fixture.check.txRecord = recorder
	fixture.clients[0].sendErr = errors.New("response lost with no immediately visible candidate")
	if _, err := fixture.check.managedTransferForVerification(t.Context()); err == nil || !strings.Contains(err.Error(), "response lost") {
		t.Fatalf("first interrupted attempt error = %v", err)
	}

	interrupted, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	intent, ok := interrupted.ManagedTransactionIntents[label]
	if !ok || intent.AccessList == nil {
		t.Fatalf("checkpoint lost complete managed intent: %+v", interrupted.ManagedTransactionIntents)
	}
	if _, ok := interrupted.ManagedTransactionInitialAttempts[label]; !ok || interrupted.Transactions[label] != "" || len(interrupted.ManagedTransactionResubmits) != 0 {
		t.Fatalf("interrupted checkpoint boundary is wrong: initial=%v resubmits=%v txs=%v", interrupted.ManagedTransactionInitialAttempts, interrupted.ManagedTransactionResubmits, interrupted.Transactions)
	}
	request, err := managedRequestFromIntent(intent)
	if err != nil {
		t.Fatal(err)
	}
	transaction := signedManagedTransaction(t, fixture.wallet, request, 2)
	for _, client := range fixture.clients {
		client.pendingTransactions = []*types.Transaction{transaction}
		client.pending = intent.Nonce + 1
	}
	resumed := *fixture.check
	resumed.recordedIntent = &intent
	resumed.initialAttempt = true
	resumed.resubmitted = false
	resumed.recordedTransaction = ""
	resumed.managedRecord = recorder
	resumed.txRecord = recorder
	if hash, err := resumed.managedTransferForVerification(t.Context()); err != nil || hash != transaction.Hash() {
		t.Fatalf("same-checkpoint resume hash/error = %s/%v, want %s", hash, err, transaction.Hash())
	}
	completed, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if completed.Transactions[label] != transaction.Hash().Hex() || len(completed.ManagedTransactionResubmits) != 0 {
		t.Fatalf("resumed checkpoint evidence = txs %v resubmits %v", completed.Transactions, completed.ManagedTransactionResubmits)
	}
}
