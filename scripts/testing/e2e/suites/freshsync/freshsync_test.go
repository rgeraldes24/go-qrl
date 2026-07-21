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
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/qrlclient/gqrlclient"
	"github.com/theQRL/go-qrl/rlp"
	"github.com/theQRL/go-qrl/trie"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := ParseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SyncMode != "snap" || cfg.ELTemplateService != "el-2-gqrl-qrysm" || cfg.CLTemplateService != "cl-2-qrysm-gqrl" {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if len(cfg.SignerAddress.Bytes()) != common.AddressLength || len(cfg.Recipient.Bytes()) != common.AddressLength || len(cfg.DepositContract.Bytes()) != common.AddressLength {
		t.Fatalf("default addresses are not VM64 width")
	}
	if cfg.CleanupOnFailure || cfg.KeepServices {
		t.Fatalf("unexpected cleanup defaults: %+v", cfg)
	}
}

func TestParseConfigRejectsUnsafeInputs(t *testing.T) {
	tests := [][]string{
		{"-syncmode", "fast"},
		{"-fresh-el-service", "EL_UPPER"},
		{"-fresh-el-service", "el-1-gqrl-qrysm"},
		{"-fresh-el-service", "same", "-fresh-cl-service", "same"},
		{"-value", "0"},
		{"-poll", "0s"},
		{"-signer-address", "Q1234"},
		{"-deposit-contract", "Q1234"},
	}
	for _, args := range tests {
		if _, err := ParseConfig(args); err == nil {
			t.Errorf("ParseConfig(%q) succeeded, want error", args)
		}
	}
}

func TestDecodeMutableDepositBranchRequiresFullNonzeroVM64Word(t *testing.T) {
	raw := append(bytes.Repeat([]byte{0x11}, 32), bytes.Repeat([]byte{0x22}, 32)...)
	got, err := decodeMutableDepositBranch(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:], raw) {
		t.Fatalf("mutable branch = 0x%x, want 0x%x", got, raw)
	}
	for name, input := range map[string][]byte{
		"truncated": raw[:32],
		"zero high": append(make([]byte, 32), raw[32:]...),
		"zero low":  append(bytes.Clone(raw[:32]), make([]byte, 32)...),
	} {
		if _, err := decodeMutableDepositBranch(input); err == nil {
			t.Errorf("%s mutable branch accepted", name)
		}
	}
}

func TestVerifyDepositProofAcceptsVM64InclusionAndRejectsTampering(t *testing.T) {
	address, err := common.NewAddressFromString(defaultDepositContract)
	if err != nil {
		t.Fatal(err)
	}
	slot := depositStorageKey(depositBranchFirstSlot)
	var want common.StorageValue64
	copy(want[:32], bytes.Repeat([]byte{0x11}, 32))
	copy(want[32:], bytes.Repeat([]byte{0x22}, 32))

	storageTrie := trie.NewEmpty(trie.NewDatabase(rawdb.NewMemoryDatabase(), nil))
	storageLeaf, err := rlp.EncodeToBytes(want[:])
	if err != nil {
		t.Fatal(err)
	}
	storageKey := crypto.Keccak256(slot.Bytes())
	if err := storageTrie.Update(storageKey, storageLeaf); err != nil {
		t.Fatal(err)
	}
	storageProof := newProofStrings(t, storageTrie, storageKey)

	account := types.StateAccount{
		Nonce:    7,
		Balance:  big.NewInt(12345),
		Root:     storageTrie.Hash(),
		CodeHash: crypto.Keccak256([]byte("vm64 deposit runtime")),
	}
	accountLeaf, err := rlp.EncodeToBytes(&account)
	if err != nil {
		t.Fatal(err)
	}
	accountTrie := trie.NewEmpty(trie.NewDatabase(rawdb.NewMemoryDatabase(), nil))
	accountKey := crypto.Keccak256(address.Bytes())
	if err := accountTrie.Update(accountKey, accountLeaf); err != nil {
		t.Fatal(err)
	}
	accountProof := newProofStrings(t, accountTrie, accountKey)
	proof := &gqrlclient.AccountResult{
		Address:      address,
		AccountProof: accountProof,
		Balance:      new(big.Int).Set(account.Balance),
		CodeHash:     common.BytesToHash(account.CodeHash),
		Nonce:        account.Nonce,
		StorageHash:  account.Root,
		StorageProof: []gqrlclient.StorageResult{{
			Key:   slot.Hex(),
			Value: new(big.Int).SetBytes(want[:]),
			Proof: storageProof,
		}},
	}
	if err := verifyDepositProof(accountTrie.Hash(), address, slot, want, proof); err != nil {
		t.Fatalf("valid proof rejected: %v", err)
	}

	badValue := cloneAccountProof(proof)
	badValue.StorageProof[0].Value.Add(badValue.StorageProof[0].Value, big.NewInt(1))
	if err := verifyDepositProof(accountTrie.Hash(), address, slot, want, badValue); err == nil || !strings.Contains(err.Error(), "RPC value") {
		t.Fatalf("tampered RPC value error = %v", err)
	}

	badNode := cloneAccountProof(proof)
	decoded, err := hexutil.Decode(badNode.StorageProof[0].Proof[0])
	if err != nil {
		t.Fatal(err)
	}
	decoded[len(decoded)/2] ^= 1
	badNode.StorageProof[0].Proof[0] = hexutil.Encode(decoded)
	if err := verifyDepositProof(accountTrie.Hash(), address, slot, want, badNode); err == nil || !strings.Contains(err.Error(), "storage inclusion proof") {
		t.Fatalf("tampered proof node error = %v", err)
	}
}

func TestDecodeDepositCallsFailsClosed(t *testing.T) {
	parsed, err := parseDepositReadABI()
	if err != nil {
		t.Fatal(err)
	}
	root := [32]byte{1, 2, 3}
	rootCall, err := parsed.Methods["get_deposit_root"].Outputs.Pack(root)
	if err != nil {
		t.Fatal(err)
	}
	countBytes := make([]byte, 8)
	binary.LittleEndian.PutUint64(countBytes, 3)
	countCall, err := parsed.Methods["get_deposit_count"].Outputs.Pack(countBytes)
	if err != nil {
		t.Fatal(err)
	}
	gotRoot, gotCount, err := decodeDepositCalls(parsed, rootCall, countCall)
	if err != nil {
		t.Fatal(err)
	}
	if gotRoot != root || gotCount != 3 {
		t.Fatalf("decoded root/count = %x/%d, want %x/3", gotRoot, gotCount, root)
	}

	zeroCountCall, err := parsed.Methods["get_deposit_count"].Outputs.Pack(make([]byte, 8))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := decodeDepositCalls(parsed, rootCall, zeroCountCall); err == nil || !strings.Contains(err.Error(), "remains zero") {
		t.Fatalf("zero count error = %v", err)
	}
	if _, _, err := decodeDepositCalls(parsed, []byte{1}, countCall); err == nil || !strings.Contains(err.Error(), "decode get_deposit_root") {
		t.Fatalf("malformed root error = %v", err)
	}
}

type proofStrings []string

func (p *proofStrings) Put(_ []byte, value []byte) error {
	*p = append(*p, hexutil.Encode(bytes.Clone(value)))
	return nil
}

func (*proofStrings) Delete([]byte) error {
	return errors.New("proof deletion is unsupported")
}

func newProofStrings(t *testing.T, tr *trie.Trie, key []byte) []string {
	t.Helper()
	var proof proofStrings
	if err := tr.Prove(key, &proof); err != nil {
		t.Fatal(err)
	}
	if len(proof) == 0 {
		t.Fatal("generated proof is empty")
	}
	return []string(proof)
}

func cloneAccountProof(proof *gqrlclient.AccountResult) *gqrlclient.AccountResult {
	clone := *proof
	clone.Balance = new(big.Int).Set(proof.Balance)
	clone.AccountProof = append([]string(nil), proof.AccountProof...)
	clone.StorageProof = append([]gqrlclient.StorageResult(nil), proof.StorageProof...)
	for i := range clone.StorageProof {
		clone.StorageProof[i].Value = new(big.Int).Set(proof.StorageProof[i].Value)
		clone.StorageProof[i].Proof = append([]string(nil), proof.StorageProof[i].Proof...)
	}
	return &clone
}

func TestRunFreshSyncAppliesWholeRunTimeout(t *testing.T) {
	cfg, err := ParseConfig([]string{"-timeout", "20ms"})
	if err != nil {
		t.Fatal(err)
	}
	err = runFreshSync(context.Background(), cfg, freshSyncDeadlineRunner{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runFreshSync error = %v, want context deadline exceeded", err)
	}
}

type freshSyncDeadlineRunner struct{}

func (freshSyncDeadlineRunner) run(ctx context.Context, _ []byte, _ ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		return "", errors.New("whole-run context has no deadline")
	}
	<-ctx.Done()
	return "", ctx.Err()
}

func TestMutateExecutionConfigPreservesArtifactsAndFailsClosed(t *testing.T) {
	cfg := testExecutionConfig(t)
	unknownBefore := append([]byte(nil), cfg["future_field"]...)
	if err := mutateExecutionConfig(cfg, "snap"); err != nil {
		t.Fatal(err)
	}

	var files map[string][]string
	if err := cfg.decode("files", &files); err != nil {
		t.Fatal(err)
	}
	if _, ok := files[executionDataDir]; ok {
		t.Fatalf("execution datadir artifact survived: %v", files)
	}
	if !slices.Equal(files[genesisMount], []string{"genesis-artifact"}) || !slices.Equal(files[jwtMount], []string{"jwt-artifact"}) || !slices.Equal(files["/extra-read-only"], []string{"extra-artifact"}) {
		t.Fatalf("read-only artifacts changed: %v", files)
	}
	if _, ok := cfg["public_ports"]; ok {
		t.Fatalf("public ports were copied")
	}
	if !slices.Equal(unknownBefore, cfg["future_field"]) {
		t.Fatalf("unknown inspect/add field was not preserved")
	}
	var placeholder string
	if err := cfg.decode("private_ip_address_placeholder", &placeholder); err != nil {
		t.Fatal(err)
	}
	if placeholder != elIPPlaceholder {
		t.Fatalf("placeholder = %q", placeholder)
	}
	var cmd []string
	if err := cfg.decode("cmd", &cmd); err != nil {
		t.Fatal(err)
	}
	if len(cmd) != 1 || !strings.HasPrefix(cmd[0], "if [ -e "+executionDataDir+" ];") {
		t.Fatalf("missing fail-closed pre-init guard: %q", cmd)
	}
	for _, want := range []string{"--syncmode=snap", "--verbosity=4", "--nat=extip:" + elIPPlaceholder, "--bootnodes=qnode://boot", "--signer=http://signer:8550"} {
		if !strings.Contains(cmd[0], want) {
			t.Errorf("mutated command missing %q: %s", want, cmd[0])
		}
	}
	if strings.Contains(cmd[0], "--syncmode=full") || strings.Contains(cmd[0], "172.16.0.2") {
		t.Fatalf("source service state leaked into command: %s", cmd[0])
	}
}

func TestVerifySyncModeEvidence(t *testing.T) {
	for _, test := range []struct {
		name    string
		mode    string
		logs    string
		wantErr string
	}{
		{name: "snap markers", mode: "snap", logs: "Starting snapshot sync cycle\nCommitting snap sync pivot as new head"},
		{name: "snap missing pivot", mode: "snap", logs: "Starting snapshot sync cycle", wantErr: "pivot=false"},
		{name: "full clean", mode: "full", logs: "Block synchronisation started\nImported new chain segment"},
		{name: "full missing downloader", mode: "full", logs: "Imported new chain segment", wantErr: "without positive block-downloader evidence"},
		{name: "full fell back", mode: "full", logs: "Switch sync mode from full sync to snap sync", wantErr: "unexpectedly used snap"},
	} {
		t.Run(test.name, func(t *testing.T) {
			runner := &recordingRunner{outputs: []string{test.logs}}
			check := &freshSyncCheck{
				cfg: Config{Enclave: "testnet", FreshELService: "fresh-el", SyncMode: test.mode},
				k:   cliKurtosis{enclave: "testnet", runner: runner},
			}
			err := check.verifySyncModeEvidence(t.Context())
			if test.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)) {
				t.Fatalf("got %v, want error containing %q", err, test.wantErr)
			}
			wantArgs := []string{"service", "logs", "testnet", "fresh-el", "--all"}
			if !slices.Equal(runner.calls[0].args, wantArgs) {
				t.Fatalf("log args = %v, want %v", runner.calls[0].args, wantArgs)
			}
		})
	}
}

func TestMutateExecutionConfigRejectsAmbiguousStateOrTopology(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(rawServiceConfig)
		want   string
	}{
		{
			name: "parent mount",
			mutate: func(cfg rawServiceConfig) {
				var files map[string][]string
				_ = cfg.decode("files", &files)
				files["/data/gqrl"] = []string{"ambiguous"}
				_ = cfg.set("files", files)
			},
			want: "could seed",
		},
		{
			name: "bind mount",
			mutate: func(cfg rawServiceConfig) {
				_ = cfg.set("bind_mounts", map[string]string{"/host": executionDataDir})
			},
			want: "host bind mounts",
		},
		{
			name: "future persistence",
			mutate: func(cfg rawServiceConfig) {
				_ = cfg.set("persistent_directories", map[string]string{executionDataDir: "old-volume"})
			},
			want: "persistence-like field",
		},
		{
			name: "future directory schema",
			mutate: func(cfg rawServiceConfig) {
				_ = cfg.set("directories", map[string]string{executionDataDir: "old-volume"})
			},
			want: "persistence-like field",
		},
		{
			name: "missing bootnode",
			mutate: func(cfg rawServiceConfig) {
				var cmd []string
				_ = cfg.decode("cmd", &cmd)
				cmd[0] = strings.ReplaceAll(cmd[0], " --bootnodes=qnode://boot", "")
				_ = cfg.set("cmd", cmd)
			},
			want: "no bootnodes",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := testExecutionConfig(t)
			test.mutate(cfg)
			if err := mutateExecutionConfig(cfg, "snap"); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestMutateBeaconConfigRetargetsFreshExecution(t *testing.T) {
	cfg := testBeaconConfig(t)
	endpoint := "http://172.16.0.99:8551"
	if err := mutateBeaconConfig(cfg, endpoint); err != nil {
		t.Fatal(err)
	}
	var files map[string][]string
	if err := cfg.decode("files", &files); err != nil {
		t.Fatal(err)
	}
	if _, ok := files[beaconDataDir]; ok {
		t.Fatalf("beacon datadir artifact survived")
	}
	if !slices.Equal(files[genesisMount], []string{"genesis-artifact"}) || !slices.Equal(files[jwtMount], []string{"jwt-artifact"}) {
		t.Fatalf("beacon read-only artifacts changed: %v", files)
	}
	var cmd []string
	if err := cfg.decode("cmd", &cmd); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--execution-endpoint=" + endpoint, "--p2p-host-ip=" + clIPPlaceholder, "--bootstrap-node=qnr:boot", "--min-sync-peers=0", "--sync-from=head"} {
		if !slices.Contains(cmd, want) {
			t.Errorf("beacon command missing %q: %v", want, cmd)
		}
	}
	if slices.Contains(cmd, "--p2p-static-id=true") || slices.Contains(cmd, "--execution-endpoint=http://172.16.0.2:8551") {
		t.Fatalf("source beacon identity/endpoint survived: %v", cmd)
	}
	if countPrefix(cmd, "--min-sync-peers=") != 1 || countPrefix(cmd, "--sync-from=") != 1 {
		t.Fatalf("sync flags were not normalized: %v", cmd)
	}
}

func TestMutateBeaconConfigRequiresBootstrapNode(t *testing.T) {
	cfg := testBeaconConfig(t)
	var cmd []string
	if err := cfg.decode("cmd", &cmd); err != nil {
		t.Fatal(err)
	}
	cmd = slices.DeleteFunc(cmd, func(arg string) bool { return strings.HasPrefix(arg, "--bootstrap-node=") })
	if err := cfg.set("cmd", cmd); err != nil {
		t.Fatal(err)
	}
	if err := mutateBeaconConfig(cfg, "http://172.16.0.99:8551"); err == nil || !strings.Contains(err.Error(), "no bootstrap node") {
		t.Fatalf("got %v, want missing-bootstrap error", err)
	}
}

func TestParseServiceConfigToleratesCLIContextAndPreservesUnknown(t *testing.T) {
	output := "INFO connected\n{\n  \"image\": \"client:v1\",\n  \"future\": {\"enabled\": true}\n}\nINFO done\n"
	cfg, err := parseServiceConfig(output)
	if err != nil {
		t.Fatal(err)
	}
	var image string
	if err := cfg.decode("image", &image); err != nil {
		t.Fatal(err)
	}
	if image != "client:v1" || len(cfg["future"]) == 0 {
		t.Fatalf("unexpected config: %v", cfg)
	}
}

func TestKurtosisUsesStableInspectAddRoundTrip(t *testing.T) {
	runner := &recordingRunner{
		outputs: []string{
			`{"image":"client:v1","ports":{},"files":{}}`,
			"Service ID: fresh-sync-el\n",
			"127.0.0.1:18545\n",
			"removed\n",
		},
	}
	k := cliKurtosis{enclave: "testnet", runner: runner}
	cfg, err := k.inspect(context.Background(), "el-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := k.add(context.Background(), "fresh-sync-el", cfg); err != nil {
		t.Fatal(err)
	}
	endpoint, err := k.endpoint(context.Background(), "fresh-sync-el", "rpc", "http")
	if err != nil {
		t.Fatal(err)
	}
	if endpoint != "http://127.0.0.1:18545" {
		t.Fatalf("endpoint = %q", endpoint)
	}
	if err := k.remove(context.Background(), "fresh-sync-el"); err != nil {
		t.Fatal(err)
	}
	wantArgs := [][]string{
		{"service", "inspect", "testnet", "el-2", "--output", "json"},
		{"service", "add", "testnet", "fresh-sync-el", "--json-service-config", "-"},
		{"port", "print", "testnet", "fresh-sync-el", "rpc", "--format", "ip,number"},
		{"service", "rm", "testnet", "fresh-sync-el"},
	}
	for i, want := range wantArgs {
		if !slices.Equal(runner.calls[i].args, want) {
			t.Errorf("call %d args = %v, want %v", i, runner.calls[i].args, want)
		}
	}
	if len(runner.calls[1].input) == 0 || !json.Valid(runner.calls[1].input) {
		t.Fatalf("service add did not receive valid config on stdin: %q", runner.calls[1].input)
	}
}

func TestBeaconStatusRequiresSynchronizedExecutionAndPeers(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		var body string
		switch req.URL.Path {
		case syncStatusPath:
			body = `{"data":{"head_slot":"42","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`
		case peerCountPath:
			body = `{"data":{"connected":"2"}}`
		default:
			return &http.Response{StatusCode: http.StatusNotFound, Status: "404 Not Found", Body: io.NopCloser(strings.NewReader("missing")), Request: req}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
	})}
	reader := httpReader{client: client}
	status, err := reader.beaconStatus(context.Background(), "http://beacon")
	if err != nil {
		t.Fatal(err)
	}
	if status.headSlot != 42 || status.connectedPeers != 2 || status.syncDistance != 0 {
		t.Fatalf("unexpected status: %+v", status)
	}
}

func TestEngineURL(t *testing.T) {
	got, err := engineURL("2001:db8::1", 8551)
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://[2001:db8::1]:8551" {
		t.Fatalf("got %q", got)
	}
	for _, ip := range []string{"", "127.0.0.1", "0.0.0.0"} {
		if _, err := engineURL(ip, 8551); err == nil {
			t.Errorf("engineURL(%q) succeeded", ip)
		}
	}
}

func TestWaitForRetries(t *testing.T) {
	var attempts int
	err := waitFor(context.Background(), time.Second, time.Millisecond, "test condition", func(context.Context) (bool, error) {
		attempts++
		if attempts < 3 {
			return false, fmt.Errorf("not yet")
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
}

func testExecutionConfig(t *testing.T) rawServiceConfig {
	t.Helper()
	cfg := make(rawServiceConfig)
	mustSet(t, cfg, "image", "theqrl-dev/go-qrl:sha")
	mustSet(t, cfg, "ports", map[string]portSpec{"rpc": {Number: 8545}, "engine-rpc": {Number: 8551}})
	mustSet(t, cfg, "public_ports", map[string]portSpec{"rpc": {Number: 18545}})
	mustSet(t, cfg, "files", map[string][]string{
		genesisMount:       {"genesis-artifact"},
		jwtMount:           {"jwt-artifact"},
		executionDataDir:   {"old-persistent-state"},
		"/extra-read-only": {"extra-artifact"},
	})
	mustSet(t, cfg, "entrypoint", []string{"sh", "-c"})
	mustSet(t, cfg, "cmd", []string{"gqrl init --datadir=" + executionDataDir + " /network-configs/genesis.json && gqrl --datadir=" + executionDataDir + " --networkid=1337 --nat=extip:172.16.0.2 --syncmode=full --bootnodes=qnode://boot --signer=http://signer:8550"})
	mustSet(t, cfg, "privileged", false)
	mustSet(t, cfg, "bind_mounts", map[string]string{})
	mustSet(t, cfg, "host_pid_namespace", false)
	cfg["future_field"] = json.RawMessage(`{"kept":true}`)
	return cfg
}

func testBeaconConfig(t *testing.T) rawServiceConfig {
	t.Helper()
	cfg := make(rawServiceConfig)
	mustSet(t, cfg, "image", "qrledger/qrysm:beacon")
	mustSet(t, cfg, "ports", map[string]portSpec{"http": {Number: 3500}})
	mustSet(t, cfg, "public_ports", map[string]portSpec{"http": {Number: 13500}})
	mustSet(t, cfg, "files", map[string][]string{
		genesisMount:  {"genesis-artifact"},
		jwtMount:      {"jwt-artifact"},
		beaconDataDir: {"old-beacon-state"},
	})
	mustSet(t, cfg, "cmd", []string{
		"--datadir=" + beaconDataDir + "/",
		"--execution-endpoint=http://172.16.0.2:8551",
		"--p2p-host-ip=172.16.0.3",
		"--p2p-static-id=true",
		"--bootstrap-node=qnr:boot",
		"--min-sync-peers=1",
		"--min-sync-peers=0",
		"--sync-from=genesis",
	})
	return cfg
}

func mustSet(t *testing.T, cfg rawServiceConfig, key string, value any) {
	t.Helper()
	if err := cfg.set(key, value); err != nil {
		t.Fatal(err)
	}
}

func countPrefix(values []string, prefix string) int {
	var count int
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			count++
		}
	}
	return count
}

type runnerCall struct {
	input []byte
	args  []string
}

type recordingRunner struct {
	outputs []string
	calls   []runnerCall
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func (r *recordingRunner) run(_ context.Context, input []byte, args ...string) (string, error) {
	r.calls = append(r.calls, runnerCall{input: append([]byte(nil), input...), args: append([]string(nil), args...)})
	if len(r.outputs) == 0 {
		return "", fmt.Errorf("unexpected call %v", args)
	}
	out := r.outputs[0]
	r.outputs = r.outputs[1:]
	return out, nil
}
