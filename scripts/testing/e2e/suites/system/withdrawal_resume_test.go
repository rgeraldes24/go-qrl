// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/rpc"
	"github.com/theQRL/go-qrl/trie"
)

func TestAutomaticWithdrawalObservationStrictRoundTrip(t *testing.T) {
	recipient := common.MustParseAddress(expectedWithdrawalAddress)
	observation, original := testAutomaticWithdrawalObservation(t, recipient)
	raw := marshalAutomaticWithdrawalObservation(t, observation)

	decoded, err := decodeAutomaticWithdrawalObservation(raw, recipient)
	if err != nil {
		t.Fatalf("valid automatic withdrawal observation rejected: %v", err)
	}
	if err := validateWithdrawalEvidence(original, decoded); err != nil {
		t.Fatalf("automatic withdrawal observation changed canonical block evidence: %v", err)
	}
	if !decoded.balancesVerified || decoded.balances[0].delta.Cmp(original.amount) != 0 || decoded.balances[1].delta.Cmp(original.amount) != 0 {
		t.Fatalf("decoded two-EL balance proof = %+v", decoded.balances)
	}

	tests := []struct {
		name   string
		mutate func(*automaticWithdrawalObservation)
		want   string
	}{
		{name: "version", mutate: func(value *automaticWithdrawalObservation) { value.Version = 2 }, want: "version or block number"},
		{name: "block number", mutate: func(value *automaticWithdrawalObservation) { value.BlockNumber = 0 }, want: "version or block number"},
		{name: "zero block hash", mutate: func(value *automaticWithdrawalObservation) { value.BlockHash = common.Hash{}.Hex() }, want: "block hash"},
		{name: "non-canonical block hash", mutate: func(value *automaticWithdrawalObservation) { value.BlockHash = strings.ToUpper(value.BlockHash) }, want: "block hash"},
		{name: "wrong root", mutate: func(value *automaticWithdrawalObservation) { value.WithdrawalsRoot = common.HexToHash("0x99").Hex() }, want: "derives root"},
		{name: "wrong recipient", mutate: func(value *automaticWithdrawalObservation) { value.Recipient = common.MaxAddress.Hex() }, want: "recipient"},
		{name: "legacy-width withdrawal address", mutate: func(value *automaticWithdrawalObservation) {
			value.Withdrawals[0].Address = "Q" + strings.Repeat("11", 32)
		}, want: "address"},
		{name: "changed withdrawal list", mutate: func(value *automaticWithdrawalObservation) { value.Withdrawals[0].AmountShor++ }, want: "derives root"},
		{name: "wrong recipient amount", mutate: func(value *automaticWithdrawalObservation) { value.RecipientAmountPlanck = "0x1" }, want: "recipient amount"},
		{name: "one EL", mutate: func(value *automaticWithdrawalObservation) { value.ELBalances = value.ELBalances[:1] }, want: "want 2"},
		{name: "wrong EL delta", mutate: func(value *automaticWithdrawalObservation) { value.ELBalances[1].DeltaPlanck = "0x1" }, want: "EL2"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := cloneAutomaticWithdrawalObservation(t, observation)
			test.mutate(&changed)
			_, err := decodeAutomaticWithdrawalObservation(marshalAutomaticWithdrawalObservation(t, changed), recipient)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want substring %q", err, test.want)
			}
		})
	}

	unknown := strings.Replace(raw, `"version":1`, `"version":1,"unknown":true`, 1)
	if _, err := decodeAutomaticWithdrawalObservation(unknown, recipient); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}
	if _, err := decodeAutomaticWithdrawalObservation(raw+` {}`, recipient); err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("trailing-data error = %v", err)
	}
}

func TestFreshAutomaticWithdrawalObservationRequiresBothBalanceChecksAndDurableRecord(t *testing.T) {
	recipient := common.MustParseAddress(expectedWithdrawalAddress)
	withdrawals := types.Withdrawals{&types.Withdrawal{Index: 10, Validator: 20, Address: recipient, Amount: 2}}
	rawBlock := testWithdrawalRPCBlock(t, 42, common.HexToHash("0x41"), withdrawals)
	credit := big.NewInt(2 * params.Shor)
	before := big.NewInt(10 * params.Shor)
	after := new(big.Int).Add(new(big.Int).Set(before), credit)

	newCheck := func(t *testing.T, secondAfter *big.Int, recorder SystemObservationRecorder) (*systemCheck, [2]*testWithdrawalAPI) {
		t.Helper()
		apis := [2]*testWithdrawalAPI{
			{blockNumber: 42, block: rawBlock, balances: map[rpc.BlockNumber]*big.Int{41: before, 42: after}},
			{blockNumber: 42, block: rawBlock, balances: map[rpc.BlockNumber]*big.Int{41: before, 42: secondAfter}},
		}
		return &systemCheck{
			cfg: config{withdrawalRecipient: recipient},
			clients: [2]*qrlclient.Client{
				newTestWithdrawalClient(t, apis[0]), newTestWithdrawalClient(t, apis[1]),
			},
			observations: recorder, resume: resumeState{observations: make(map[string]string)},
		}, apis
	}

	t.Run("EL2 mismatch records nothing", func(t *testing.T) {
		calls := 0
		check, apis := newCheck(t, new(big.Int).Sub(new(big.Int).Set(after), big.NewInt(1)), SystemObservationRecorderFunc(func(context.Context, string, string, time.Time) error {
			calls++
			return nil
		}))
		if _, err := check.withdrawalEvidenceAt(t.Context(), 42); err == nil || !strings.Contains(err.Error(), "EL2 withdrawal-recipient balance delta") {
			t.Fatalf("fresh balance error = %v", err)
		}
		if calls != 0 || len(check.resume.observations) != 0 {
			t.Fatalf("failed balance proof recorded observation: calls=%d observations=%v", calls, check.resume.observations)
		}
		if apis[0].balanceCalls.Load() != 2 || apis[1].balanceCalls.Load() != 2 {
			t.Fatalf("balance calls = EL1:%d EL2:%d, want two each", apis[0].balanceCalls.Load(), apis[1].balanceCalls.Load())
		}
	})

	t.Run("recorder failure remains fail closed", func(t *testing.T) {
		want := errors.New("disk full")
		check, _ := newCheck(t, after, SystemObservationRecorderFunc(func(context.Context, string, string, time.Time) error { return want }))
		if _, err := check.withdrawalEvidenceAt(t.Context(), 42); !errors.Is(err, want) {
			t.Fatalf("recording error = %v, want %v", err, want)
		}
		if len(check.resume.observations) != 0 {
			t.Fatalf("failed durable record changed resume state: %v", check.resume.observations)
		}
	})

	t.Run("success records exact immutable label", func(t *testing.T) {
		var label, raw string
		check, _ := newCheck(t, after, SystemObservationRecorderFunc(func(ctx context.Context, gotLabel, gotRaw string, _ time.Time) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			label, raw = gotLabel, gotRaw
			return nil
		}))
		evidence, err := check.withdrawalEvidenceAt(t.Context(), 42)
		if err != nil {
			t.Fatal(err)
		}
		wantLabel := automaticWithdrawalObservationLabel(evidence.blockHash)
		if label != wantLabel || check.resume.observations[label] != raw {
			t.Fatalf("recorded label/value = %q/%q, resume=%v", label, raw, check.resume.observations)
		}
		decoded, err := decodeAutomaticWithdrawalObservation(raw, recipient)
		if err != nil {
			t.Fatal(err)
		}
		if err := validateWithdrawalEvidence(evidence, decoded); err != nil {
			t.Fatalf("recorded fresh evidence differs: %v", err)
		}
	})
}

func TestRecordedAutomaticWithdrawalRevalidationIsArchiveIndependent(t *testing.T) {
	recipient := common.MustParseAddress(expectedWithdrawalAddress)
	withdrawals := types.Withdrawals{&types.Withdrawal{Index: 10, Validator: 20, Address: recipient, Amount: 2}}
	rawBlock := testWithdrawalRPCBlock(t, 42, common.HexToHash("0x41"), withdrawals)
	apis := [2]*testWithdrawalAPI{
		{blockNumber: 42, block: rawBlock, balanceErr: errors.New("missing trie node")},
		{blockNumber: 42, block: rawBlock, balanceErr: errors.New("missing trie node")},
	}
	check := &systemCheck{
		cfg: config{withdrawalRecipient: recipient},
		clients: [2]*qrlclient.Client{
			newTestWithdrawalClient(t, apis[0]), newTestWithdrawalClient(t, apis[1]),
		},
		resume: resumeState{observations: make(map[string]string)},
	}
	evidence, err := check.withdrawalBlockEvidenceAt(t.Context(), 42)
	if err != nil {
		t.Fatal(err)
	}
	before := big.NewInt(10 * params.Shor)
	after := new(big.Int).Add(new(big.Int).Set(before), evidence.amount)
	for i := range evidence.balances {
		evidence.balances[i] = withdrawalBalanceEvidence{before: new(big.Int).Set(before), after: new(big.Int).Set(after), delta: new(big.Int).Set(evidence.amount)}
	}
	evidence.balancesVerified = true
	observation, err := automaticWithdrawalObservationFromEvidence(evidence, recipient)
	if err != nil {
		t.Fatal(err)
	}
	label := automaticWithdrawalObservationLabel(evidence.blockHash)
	check.resume.observations[label] = marshalAutomaticWithdrawalObservation(t, observation)

	resumed, ok, rescanFrom, err := check.recordedAutomaticWithdrawalEvidence(t.Context())
	if err != nil || !ok {
		t.Fatalf("recorded withdrawal revalidation = ok:%t evidence:%+v error:%v", ok, resumed, err)
	}
	if rescanFrom != 0 {
		t.Fatalf("canonical recorded withdrawal requested rescan from %d", rescanFrom)
	}
	if err := validateWithdrawalEvidence(evidence, resumed); err != nil {
		t.Fatalf("resumed evidence differs: %v", err)
	}
	for i, api := range apis {
		if calls := api.balanceCalls.Load(); calls != 0 {
			t.Fatalf("EL%d resume made %d historical balance calls, want zero", i+1, calls)
		}
	}
}

func TestRecordedAutomaticWithdrawalReorgAllowsAppendOnlyReplacement(t *testing.T) {
	recipient := common.MustParseAddress(expectedWithdrawalAddress)
	staleObservation, stale := testAutomaticWithdrawalObservation(t, recipient)
	withdrawals := types.Withdrawals{&types.Withdrawal{Index: 30, Validator: 40, Address: recipient, Amount: 2}}
	canonicalBlock := testWithdrawalRPCBlock(t, stale.blockNumber, common.HexToHash("0x99"), withdrawals)
	var canonicalHeader struct {
		Hash common.Hash `json:"hash"`
	}
	if err := json.Unmarshal(canonicalBlock, &canonicalHeader); err != nil || canonicalHeader.Hash == (common.Hash{}) {
		t.Fatalf("decode canonical replacement hash: hash=%s error=%v", canonicalHeader.Hash, err)
	}
	credit := big.NewInt(2 * params.Shor)
	before := big.NewInt(10 * params.Shor)
	after := new(big.Int).Add(new(big.Int).Set(before), credit)
	previousBlock := rpc.BlockNumber(stale.blockNumber - 1)
	currentBlock := rpc.BlockNumber(stale.blockNumber)
	headers := map[rpc.BlockNumber]json.RawMessage{
		rpc.SafeBlockNumber:      canonicalBlock,
		rpc.FinalizedBlockNumber: canonicalBlock,
	}
	apis := [2]*testWithdrawalAPI{
		{blockNumber: currentBlock, block: canonicalBlock, headerBlocks: headers, balances: map[rpc.BlockNumber]*big.Int{previousBlock: before, currentBlock: after}},
		{blockNumber: currentBlock, block: canonicalBlock, headerBlocks: headers, balances: map[rpc.BlockNumber]*big.Int{previousBlock: before, currentBlock: after}},
	}
	baseline := validatorDutySnapshots{
		validatorSnapshotForTest("vc1", 100, expectedValidatorsPerClient, 1, 0, 0),
		validatorSnapshotForTest("vc2", 200, expectedValidatorsPerClient, 1, 0, 0),
	}
	metrics := [2]*httptest.Server{}
	for i := range metrics {
		prefix := fmt.Sprintf("vc%d", i+1)
		processStart := float64(100 + 100*i)
		metrics[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(validatorMetricsFixture(prefix, expectedValidatorsPerClient, expectedValidatorsPerClient, processStart)))
		}))
		defer metrics[i].Close()
	}
	consensus := [2]*httptest.Server{}
	for i := range consensus {
		consensus[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `{"version":"zond","execution_optimistic":false,"finalized":true,"data":{"message":{"body":{"execution_payload":{"block_number":"%d","block_hash":%q}}}}}`, stale.blockNumber, canonicalHeader.Hash.Hex())
		}))
		defer consensus[i].Close()
	}
	check := &systemCheck{
		cfg: config{
			withdrawalRecipient:   recipient,
			timeout:               time.Second,
			pollInterval:          time.Millisecond,
			validatorPollInterval: time.Hour,
			vcMetricsURLs:         [2]string{metrics[0].URL, metrics[1].URL},
			clURLs:                [2]string{consensus[0].URL, consensus[1].URL},
		},
		http: httpReader{client: &http.Client{Timeout: time.Second}},
		clients: [2]*qrlclient.Client{
			newTestWithdrawalClient(t, apis[0]), newTestWithdrawalClient(t, apis[1]),
		},
		observations: SystemObservationRecorderFunc(func(context.Context, string, string, time.Time) error { return nil }),
		resume:       resumeState{observations: map[string]string{automaticWithdrawalObservationLabel(stale.blockHash): marshalAutomaticWithdrawalObservation(t, staleObservation)}},
	}
	if evidence, ok, rescanFrom, err := check.recordedAutomaticWithdrawalEvidence(t.Context()); err != nil || ok || rescanFrom != stale.blockNumber {
		t.Fatalf("reorged candidate revalidation = ok:%t rescan:%d evidence:%+v error:%v", ok, rescanFrom, evidence, err)
	}
	if err := check.waitAutomaticWithdrawal(t.Context(), baseline); err != nil {
		t.Fatalf("resume automatic withdrawal through same-height replacement: %v", err)
	}
	staleLabel := automaticWithdrawalObservationLabel(stale.blockHash)
	freshLabel := automaticWithdrawalObservationLabel(canonicalHeader.Hash)
	if staleLabel == freshLabel || check.resume.observations[staleLabel] == "" || check.resume.observations[freshLabel] == "" || len(check.resume.observations) != 2 {
		t.Fatalf("append-only reorg evidence = stale:%q fresh:%q observations:%v", staleLabel, freshLabel, check.resume.observations)
	}
	for i, api := range apis {
		if calls := api.blockCalls.Load(); calls != 4 {
			t.Fatalf("EL%d full block calls = %d, want four (initial stale check, resumed stale check, replacement scan, finalized revalidation)", i+1, calls)
		}
	}
}

func TestWithdrawalFinalizedRevalidationChecksRootAndFullList(t *testing.T) {
	recipient := common.MustParseAddress(expectedWithdrawalAddress)
	_, original := testAutomaticWithdrawalObservation(t, recipient)

	tests := []struct {
		name   string
		mutate func(*withdrawalEvidence)
		want   string
	}{
		{name: "root", mutate: func(value *withdrawalEvidence) { value.withdrawalsRoot = common.HexToHash("0x99") }, want: "root changed"},
		{name: "list count", mutate: func(value *withdrawalEvidence) { value.withdrawals = value.withdrawals[:1] }, want: "counts differ"},
		{name: "validator", mutate: func(value *withdrawalEvidence) { value.withdrawals[0].Validator++ }, want: "differs"},
		{name: "address", mutate: func(value *withdrawalEvidence) { value.withdrawals[0].Address = common.MaxAddress }, want: "differs"},
		{name: "amount shor", mutate: func(value *withdrawalEvidence) { value.withdrawals[0].Amount++ }, want: "differs"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed := original
			changed.withdrawals = cloneWithdrawals(original.withdrawals)
			changed.amount = new(big.Int).Set(original.amount)
			test.mutate(&changed)
			if err := validateWithdrawalEvidence(original, changed); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("revalidation error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestAutomaticWithdrawalObservationPhaseValidationAndLegacyAbsence(t *testing.T) {
	recipient := common.MustParseAddress(expectedWithdrawalAddress)
	observation, _ := testAutomaticWithdrawalObservation(t, recipient)
	raw := marshalAutomaticWithdrawalObservation(t, observation)
	base := config{phase: string(PhaseBase), withdrawalRecipient: recipient}

	label := automaticWithdrawalObservationLabel(common.HexToHash(observation.BlockHash))
	resume := resumeState{observations: map[string]string{label: raw}}
	if err := validateSystemObservationResumeState(base, &resume); err != nil {
		t.Fatalf("valid base observation rejected: %v", err)
	}
	legacy := resumeState{
		observations:   make(map[string]string),
		managedIntents: map[string]ManagedTransactionIntent{TransactionLabelBaseEL1Transfer: {Label: TransactionLabelBaseEL1Transfer}},
	}
	if err := validateSystemObservationResumeState(base, &legacy); err != nil {
		t.Fatalf("legacy base intent without withdrawal observation must fall back to fresh scan: %v", err)
	}
	signer := config{phase: string(PhaseSignerRestart), withdrawalRecipient: recipient}
	if err := validateSystemObservationResumeState(signer, &resume); err == nil || !strings.Contains(err.Error(), "not valid for phase") {
		t.Fatalf("cross-phase observation error = %v", err)
	}
	changed := cloneAutomaticWithdrawalObservation(t, observation)
	changed.RecipientAmountPlanck = "0x1"
	resume.observations[label] = marshalAutomaticWithdrawalObservation(t, changed)
	if err := validateSystemObservationResumeState(base, &resume); err == nil || !strings.Contains(err.Error(), "recipient amount") {
		t.Fatalf("malformed base observation error = %v", err)
	}
}

func testAutomaticWithdrawalObservation(t *testing.T, recipient common.Address) (automaticWithdrawalObservation, withdrawalEvidence) {
	t.Helper()
	withdrawals := types.Withdrawals{
		&types.Withdrawal{Index: 10, Validator: 20, Address: recipient, Amount: 2},
		&types.Withdrawal{Index: 11, Validator: 21, Address: common.MaxAddress, Amount: 3},
	}
	root := types.DeriveSha(withdrawals, trie.NewStackTrie(nil))
	amount, err := withdrawalValue(withdrawals, recipient)
	if err != nil {
		t.Fatal(err)
	}
	evidence := withdrawalEvidence{
		blockNumber: 42, blockHash: common.HexToHash("0x42"), withdrawalsRoot: root,
		withdrawals: cloneWithdrawals(withdrawals), amount: amount, balancesVerified: true,
	}
	for i, base := range []int64{10 * params.Shor, 20 * params.Shor} {
		before := big.NewInt(base)
		after := new(big.Int).Add(new(big.Int).Set(before), amount)
		evidence.balances[i] = withdrawalBalanceEvidence{before: before, after: after, delta: new(big.Int).Set(amount)}
	}
	observation, err := automaticWithdrawalObservationFromEvidence(evidence, recipient)
	if err != nil {
		t.Fatal(err)
	}
	return observation, evidence
}

func marshalAutomaticWithdrawalObservation(t *testing.T, observation automaticWithdrawalObservation) string {
	t.Helper()
	payload, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	return string(payload)
}

func cloneAutomaticWithdrawalObservation(t *testing.T, observation automaticWithdrawalObservation) automaticWithdrawalObservation {
	t.Helper()
	var cloned automaticWithdrawalObservation
	if err := json.Unmarshal([]byte(marshalAutomaticWithdrawalObservation(t, observation)), &cloned); err != nil {
		t.Fatal(err)
	}
	return cloned
}
