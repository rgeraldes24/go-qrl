// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package beacon

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTypedBeaconReads(t *testing.T) {
	publicKey := []byte("vm64-validator")
	mux := http.NewServeMux()
	mux.HandleFunc("/qrl/v1/node/syncing", jsonResponse(`{"data":{"head_slot":"257","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`))
	mux.HandleFunc("/qrl/v1/node/peer_count", jsonResponse(`{"data":{"connected":"1","connecting":"2","disconnected":"3","disconnecting":"4"}}`))
	mux.HandleFunc("/qrl/v1/beacon/genesis", jsonResponse(`{"data":{"genesis_time":"1000","genesis_validators_root":"0xAB","genesis_fork_version":"0xCD"}}`))
	mux.HandleFunc("/qrl/v1/config/spec", jsonResponse(`{"data":{"SLOTS_PER_EPOCH":"128","SECONDS_PER_SLOT":"5"}}`))
	mux.HandleFunc("/qrl/v1/beacon/states/head/finality_checkpoints", jsonResponse(`{"execution_optimistic":false,"data":{"previous_justified":{"epoch":"1","root":"0xAA"},"current_justified":{"epoch":"2","root":"0xBB"},"finalized":{"epoch":"1","root":"0xCC"}}}`))
	mux.HandleFunc("/qrl/v1/validator/duties/proposer/2", jsonResponse(`{"dependent_root":"0xDD","execution_optimistic":false,"data":[{"pubkey":"0xABC","validator_index":"64","slot":"257"}]}`))
	mux.HandleFunc("/qrl/v1/beacon/headers/257", jsonResponse(`{"execution_optimistic":false,"data":{"root":"0xEE","canonical":true,"header":{"message":{"slot":"257","proposer_index":"64"}}}}`))
	mux.HandleFunc("/qrl/v1/beacon/blocks/finalized", jsonResponse(`{"version":"ZOND","execution_optimistic":false,"finalized":true,"data":{"message":{"body":{"execution_payload":{"block_number":"99","block_hash":"0xFF"}}}}}`))
	mux.HandleFunc("/qrl/v1alpha1/validator/status", func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("public_key") != base64.StdEncoding.EncodeToString(publicKey) {
			t.Errorf("public_key = %q", request.URL.Query().Get("public_key"))
		}
		_, _ = w.Write([]byte(`{"status":3,"executionDepositBlockNumber":"77"}`))
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	client, err := New(server.URL, Options{})
	if err != nil {
		t.Fatal(err)
	}

	syncing, err := client.Syncing(t.Context())
	if err != nil || syncing.HeadSlot != 257 || syncing.SyncDistance != 0 || syncing.IsSyncing {
		t.Fatalf("syncing = %+v, err = %v", syncing, err)
	}
	peers, err := client.Peers(t.Context())
	if err != nil || peers != (PeerCount{Connected: 1, Connecting: 2, Disconnected: 3, Disconnecting: 4}) {
		t.Fatalf("peers = %+v, err = %v", peers, err)
	}
	genesis, err := client.Genesis(t.Context())
	if err != nil || genesis.GenesisTime != 1000 || genesis.GenesisValidatorsRoot != "0xab" || genesis.GenesisForkVersion != "0xcd" {
		t.Fatalf("genesis = %+v, err = %v", genesis, err)
	}
	spec, err := client.Spec(t.Context())
	if err != nil || spec["SLOTS_PER_EPOCH"] != "128" {
		t.Fatalf("spec = %+v, err = %v", spec, err)
	}
	finality, err := client.Finality(t.Context(), "head")
	if err != nil || finality.Finalized != (Checkpoint{Epoch: 1, Root: "0xcc"}) || finality.CurrentJustified.Epoch != 2 {
		t.Fatalf("finality = %+v, err = %v", finality, err)
	}
	duties, err := client.ProposerDuties(t.Context(), 2)
	if err != nil || duties.DependentRoot != "0xdd" || len(duties.Duties) != 1 || duties.Duties[0] != (ProposerDuty{Pubkey: "0xabc", ValidatorIndex: 64, Slot: 257}) {
		t.Fatalf("duties = %+v, err = %v", duties, err)
	}
	header, err := client.HeaderBySlot(t.Context(), 257)
	if err != nil || !header.Canonical || header.Root != "0xee" || header.Slot != 257 || header.ProposerIndex != 64 {
		t.Fatalf("header = %+v, err = %v", header, err)
	}
	block, err := client.FinalizedBlock(t.Context())
	if err != nil || !block.Finalized || block.Version != "zond" || block.ExecutionPayload != (ExecutionPayload{BlockNumber: 99, BlockHash: "0xff"}) {
		t.Fatalf("block = %+v, err = %v", block, err)
	}
	validator, err := client.Validator(t.Context(), publicKey)
	if err != nil || validator != (ValidatorStatus{Status: ValidatorActive, ExecutionDepositBlock: 77}) {
		t.Fatalf("validator = %+v, err = %v", validator, err)
	}
}

func TestBeaconRejectsMalformedTypedResponses(t *testing.T) {
	for _, test := range []struct {
		name string
		path string
		body string
		call func(*Client) error
		want string
	}{
		{name: "missing syncing data", path: "/qrl/v1/node/syncing", body: `{}`, call: func(client *Client) error { _, err := client.Syncing(t.Context()); return err }, want: "no data"},
		{name: "invalid decimal", path: "/qrl/v1/beacon/genesis", body: `{"data":{"genesis_time":"-1"}}`, call: func(client *Client) error { _, err := client.Genesis(t.Context()); return err }, want: "invalid unsigned decimal"},
		{name: "duty outside epoch", path: "/qrl/v1/validator/duties/proposer/2", body: `{"data":[{"pubkey":"0x1","validator_index":"1","slot":"128"}]}`, call: func(client *Client) error { _, err := client.ProposerDuties(t.Context(), 2); return err }, want: "outside epoch"},
		{name: "duplicate duty", path: "/qrl/v1/validator/duties/proposer/2", body: `{"data":[{"pubkey":"0x1","validator_index":"1","slot":"256"},{"pubkey":"0x2","validator_index":"2","slot":"256"}]}`, call: func(client *Client) error { _, err := client.ProposerDuties(t.Context(), 2); return err }, want: "repeat slot"},
		{name: "incomplete header", path: "/qrl/v1/beacon/headers/head", body: `{"data":{"canonical":true}}`, call: func(client *Client) error { _, err := client.Header(t.Context(), "head"); return err }, want: "incomplete"},
		{name: "missing execution payload", path: "/qrl/v1/beacon/blocks/finalized", body: `{"data":{"message":{"body":{}}}}`, call: func(client *Client) error { _, err := client.FinalizedBlock(t.Context()); return err }, want: "no execution payload"},
		{name: "unknown validator status", path: "/qrl/v1alpha1/validator/status", body: `{"status":"NEW_STATUS"}`, call: func(client *Client) error { _, err := client.Validator(t.Context(), []byte{1}); return err }, want: "unknown validator status"},
	} {
		t.Run(test.name, func(t *testing.T) {
			mux := http.NewServeMux()
			mux.HandleFunc(test.path, jsonResponse(test.body))
			server := httptest.NewServer(mux)
			defer server.Close()
			client, err := New(server.URL, Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err := test.call(client); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestBeaconConfigurationValidation(t *testing.T) {
	for _, prefix := range []string{"relative", "/qrl/../v1", "/qrl/v1?q=1"} {
		if _, err := New("http://example.invalid", Options{APIPrefix: prefix}); err == nil {
			t.Fatalf("prefix %q succeeded", prefix)
		}
	}
	if _, err := New("http://example.invalid", Options{APIPrefix: "/eth/v1", SlotsPerEpoch: 32}); err != nil {
		t.Fatalf("standard beacon prefix: %v", err)
	}
}

func jsonResponse(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}
}
