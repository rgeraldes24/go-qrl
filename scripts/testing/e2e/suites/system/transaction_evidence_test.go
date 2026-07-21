// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package system

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/rpc"
)

type transactionEvidenceExecutionAPI struct {
	hash  common.Hash
	calls int
}

func (api *transactionEvidenceExecutionAPI) SendTransaction(context.Context, map[string]any) common.Hash {
	api.calls++
	return api.hash
}

func TestManagedTransactionSubmissionRecordsBeforeCallerCanWait(t *testing.T) {
	for _, test := range []struct {
		name   string
		label  string
		submit func(context.Context, *systemCheck, string) (common.Hash, error)
	}{
		{
			name:  "transfer",
			label: TransactionLabelBaseEL1Transfer,
			submit: func(ctx context.Context, check *systemCheck, label string) (common.Hash, error) {
				return check.sendManagedTransfer(ctx, 0, label)
			},
		},
		{
			name:  "contract",
			label: TransactionLabelBaseAccessListDeploy,
			submit: func(ctx context.Context, check *systemCheck, label string) (common.Hash, error) {
				return check.sendManagedContractTransaction(ctx, 0, nil, []byte{0x01}, nil, label)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			wantHash := common.HexToHash("0x1234")
			fixed := time.Unix(1_700_000_000, 0).UTC()
			client := newTransactionEvidenceClient(t, wantHash)
			returned := false
			var recorded []TransactionEvidence
			check := &systemCheck{
				cfg: config{
					phase: string(PhaseBase), signerAddress: common.Address{common.AddressLength - 1: 0x11},
					recipient: common.Address{common.AddressLength - 1: 0x22}, transferValue: 1,
				},
				clients: [2]*qrlclient.Client{client, nil}, now: func() time.Time { return fixed },
				transactions: TransactionRecorderFunc(func(_ context.Context, evidence TransactionEvidence) error {
					if returned {
						return errors.New("transaction evidence was recorded after submission returned")
					}
					recorded = append(recorded, evidence)
					return nil
				}),
			}
			hash, err := test.submit(t.Context(), check, test.label)
			returned = true // Receipt/state waits can only begin after this point.
			if err != nil {
				t.Fatal(err)
			}
			if hash != wantHash || len(recorded) != 1 {
				t.Fatalf("hash = %s, transaction evidence = %+v", hash, recorded)
			}
			evidence := recorded[0]
			if evidence.Phase != PhaseBase || evidence.Label != test.label || evidence.Hash != wantHash || !evidence.At.Equal(fixed) {
				t.Fatalf("transaction evidence = %+v", evidence)
			}
		})
	}
}

func TestManagedTransactionRecorderFailureAbortsSubmission(t *testing.T) {
	wantHash := common.HexToHash("0x5678")
	client := newTransactionEvidenceClient(t, wantHash)
	recorderFailure := errors.New("checkpoint write failed")
	records := 0
	check := &systemCheck{
		cfg: config{
			phase: string(PhaseBase), signerAddress: common.Address{common.AddressLength - 1: 0x11},
			recipient: common.Address{common.AddressLength - 1: 0x22}, transferValue: 1,
		},
		clients: [2]*qrlclient.Client{client, nil},
		transactions: TransactionRecorderFunc(func(context.Context, TransactionEvidence) error {
			records++
			return recorderFailure
		}),
	}
	hash, err := check.sendManagedTransfer(t.Context(), 0, TransactionLabelBaseEL1Transfer)
	if !errors.Is(err, recorderFailure) || hash != (common.Hash{}) || records != 1 {
		t.Fatalf("hash = %s, records = %d, error = %v", hash, records, err)
	}
}

func TestRecordedManagedTransactionBypassesSubmission(t *testing.T) {
	wantHash := common.HexToHash("0x9876")
	client, api := newCountingTransactionEvidenceClient(t, common.HexToHash("0x1111"))
	recorded := 0
	check := &systemCheck{
		clients: [2]*qrlclient.Client{client, nil},
		resume:  resumeState{transactions: map[string]common.Hash{TransactionLabelBaseEL1Transfer: wantHash}},
		transactions: TransactionRecorderFunc(func(context.Context, TransactionEvidence) error {
			recorded++
			return nil
		}),
	}
	hash, err := check.sendManagedTransfer(t.Context(), 0, TransactionLabelBaseEL1Transfer)
	if err != nil || hash != wantHash || api.calls != 0 || recorded != 0 {
		t.Fatalf("hash=%s calls=%d recorded=%d error=%v", hash, api.calls, recorded, err)
	}
}

func newTransactionEvidenceClient(t *testing.T, hash common.Hash) *qrlclient.Client {
	client, _ := newCountingTransactionEvidenceClient(t, hash)
	return client
}

func newCountingTransactionEvidenceClient(t *testing.T, hash common.Hash) (*qrlclient.Client, *transactionEvidenceExecutionAPI) {
	t.Helper()
	server := rpc.NewServer()
	api := &transactionEvidenceExecutionAPI{hash: hash}
	if err := server.RegisterName("qrl", api); err != nil {
		t.Fatal(err)
	}
	client := qrlclient.NewClient(rpc.DialInProc(server))
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	return client, api
}
