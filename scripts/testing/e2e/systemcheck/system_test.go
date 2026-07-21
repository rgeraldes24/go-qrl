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
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/rpc"
	"github.com/theQRL/go-qrl/trie"
)

func TestParseConfigDefaults(t *testing.T) {
	cfg, err := parseConfig(nil)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.enclave != "local-testnet" || cfg.signerSvc != "signer-clef" {
		t.Fatalf("unexpected service defaults: %+v", cfg)
	}
	if got := len(cfg.signerAddress.Bytes()); got != 64 {
		t.Fatalf("signer address width %d, want 64", got)
	}
	wantFeeRecipient, err := common.NewAddressFromString(expectedFeeRecipientAddress)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.feeRecipient != wantFeeRecipient {
		t.Fatalf("fee recipient %s, want %s", cfg.feeRecipient, wantFeeRecipient)
	}
	wantWithdrawalRecipient, err := common.NewAddressFromString(expectedWithdrawalAddress)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.withdrawalRecipient != wantWithdrawalRecipient || cfg.withdrawalRecipient == cfg.feeRecipient {
		t.Fatalf("withdrawal recipient %s, want distinct %s", cfg.withdrawalRecipient, wantWithdrawalRecipient)
	}
	upperHalfNonzero := false
	for _, value := range cfg.feeRecipient[:common.AddressLength/2] {
		upperHalfNonzero = upperHalfNonzero || value != 0
	}
	if !upperHalfNonzero {
		t.Fatal("fee recipient must exercise the upper 32 address bytes")
	}
	if !cfg.requireFinalityAdvance || cfg.skipRestarts {
		t.Fatalf("the complete system check must enable restart and finality-advance checks by default")
	}
	if cfg.timeout != 115*time.Minute {
		t.Fatalf("timeout %s, want 115m", cfg.timeout)
	}
	if cfg.validatorPollInterval != 30*time.Second {
		t.Fatalf("validator poll interval %s, want 30s", cfg.validatorPollInterval)
	}
	if cfg.requireZeroDutyHistory {
		t.Fatal("standalone systemcheck should baseline pre-existing process-cumulative duty counters by default")
	}
	strict, err := parseConfig([]string{"-require-zero-duty-history"})
	if err != nil {
		t.Fatal(err)
	}
	if !strict.requireZeroDutyHistory {
		t.Fatal("strict duty-history flag was not applied")
	}
}

func TestParseConfigRejectsUnsafeTransfer(t *testing.T) {
	_, err := parseConfig([]string{"-recipient", defaultSignerAddress})
	if err == nil || !strings.Contains(err.Error(), "must differ") {
		t.Fatalf("got %v, want distinct-address error", err)
	}
	_, err = parseConfig([]string{"-value", "0"})
	if err == nil || !strings.Contains(err.Error(), "positive") {
		t.Fatalf("got %v, want positive-value error", err)
	}
	_, err = parseConfig([]string{"-validator-poll", "0s"})
	if err == nil || !strings.Contains(err.Error(), "validator poll interval must be positive") {
		t.Fatalf("got %v, want positive validator-poll error", err)
	}
	_, err = parseConfig([]string{"-validator-poll", validatorDutyObservationTimeout.String()})
	if err == nil || !strings.Contains(err.Error(), "validator poll interval must be shorter") {
		t.Fatalf("got %v, want bounded validator-poll error", err)
	}
}

func TestRunSystemCheckAppliesWholeRunTimeout(t *testing.T) {
	cfg, err := parseConfig([]string{"-timeout", "20ms"})
	if err != nil {
		t.Fatal(err)
	}
	err = runSystemCheck(context.Background(), cfg, deadlineRunner{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("runSystemCheck error = %v, want context deadline exceeded", err)
	}
}

type deadlineRunner struct{}

func (deadlineRunner) run(ctx context.Context, _ ...string) (string, error) {
	if _, ok := ctx.Deadline(); !ok {
		return "", errors.New("whole-run context has no deadline")
	}
	<-ctx.Done()
	return "", ctx.Err()
}

func TestParsePortOutput(t *testing.T) {
	for _, test := range []struct {
		name   string
		input  string
		scheme string
		want   string
	}{
		{name: "host port", input: "127.0.0.1:18550\n", scheme: "http", want: "http://127.0.0.1:18550"},
		{name: "existing scheme", input: "http://127.0.0.1:18550/\n", scheme: "http", want: "http://127.0.0.1:18550"},
		{name: "last nonempty line", input: "warning\n127.0.0.1:8545\n", scheme: "http", want: "http://127.0.0.1:8545"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parsePortOutput(test.input, test.scheme)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
		})
	}
	if _, err := parsePortOutput("\n", "http"); err == nil {
		t.Fatal("empty output accepted")
	}
}

func TestResolveEndpointsUsesTopologyPorts(t *testing.T) {
	cfg, err := parseConfig([]string{"-skip-restarts"})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: map[string]string{}}
	services := []struct {
		service string
		port    string
		value   string
	}{
		{cfg.elServices[0], "rpc", "127.0.0.1:18545"},
		{cfg.elServices[1], "rpc", "127.0.0.1:28545"},
		{cfg.clServices[0], "http", "127.0.0.1:13500"},
		{cfg.clServices[1], "http", "127.0.0.1:23500"},
		{cfg.vcServices[0], "metrics", "127.0.0.1:18080"},
		{cfg.vcServices[1], "metrics", "127.0.0.1:28080"},
		{cfg.signerSvc, "http", "127.0.0.1:18550"},
	}
	for _, service := range services {
		key := strings.Join([]string{"port", "print", cfg.enclave, service.service, service.port, "--format", "ip,number"}, " ")
		runner.outputs[key] = service.value
	}
	if err := cfg.resolveEndpoints(t.Context(), kurtosis{enclave: cfg.enclave, runner: runner}); err != nil {
		t.Fatal(err)
	}
	if cfg.rpcURLs[1] != "http://127.0.0.1:28545" || cfg.vcMetricsURLs[0] != "http://127.0.0.1:18080/metrics" || cfg.signerURL != "http://127.0.0.1:18550" {
		t.Fatalf("unexpected resolved endpoints: %+v", cfg)
	}
	if !cfg.rpcURLsFromKurtosis[0] || !cfg.rpcURLsFromKurtosis[1] || !cfg.clURLsFromKurtosis[0] || !cfg.clURLsFromKurtosis[1] || !cfg.vcMetricsURLsFromKurtosis[0] || !cfg.vcMetricsURLsFromKurtosis[1] || !cfg.signerURLFromKurtosis {
		t.Fatalf("topology endpoint origins were not retained: %+v", cfg)
	}

	runner.outputs[strings.Join([]string{"port", "print", cfg.enclave, cfg.elServices[1], "rpc", "--format", "ip,number"}, " ")] = "127.0.0.1:38545"
	runner.outputs[strings.Join([]string{"port", "print", cfg.enclave, cfg.clServices[1], "http", "--format", "ip,number"}, " ")] = "127.0.0.1:33500"
	runner.outputs[strings.Join([]string{"port", "print", cfg.enclave, cfg.vcServices[1], "metrics", "--format", "ip,number"}, " ")] = "127.0.0.1:38080"
	runner.outputs[strings.Join([]string{"port", "print", cfg.enclave, cfg.signerSvc, "http", "--format", "ip,number"}, " ")] = "127.0.0.1:38550"
	k := kurtosis{enclave: cfg.enclave, runner: runner}
	rpcURL, err := cfg.executionEndpoint(t.Context(), k, 1)
	if err != nil {
		t.Fatal(err)
	}
	clURL, err := cfg.beaconEndpoint(t.Context(), k, 1)
	if err != nil {
		t.Fatal(err)
	}
	vcURL, err := cfg.validatorEndpoint(t.Context(), k, 1)
	if err != nil {
		t.Fatal(err)
	}
	signerURL, err := cfg.signerEndpoint(t.Context(), k)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.rpcURLs[1] != "http://127.0.0.1:28545" || cfg.clURLs[1] != "http://127.0.0.1:23500" || cfg.vcMetricsURLs[1] != "http://127.0.0.1:28080/metrics" || cfg.signerURL != "http://127.0.0.1:18550" {
		t.Fatalf("candidate endpoint lookup changed live configuration before readiness: %+v", cfg)
	}
	if rpcURL != "http://127.0.0.1:38545" || clURL != "http://127.0.0.1:33500" || vcURL != "http://127.0.0.1:38080/metrics" || signerURL != "http://127.0.0.1:38550" {
		t.Fatalf("unexpected restarted topology candidates: RPC=%s CL=%s VC=%s signer=%s", rpcURL, clURL, vcURL, signerURL)
	}
}

func TestExplicitEndpointsAreNotReplacedAfterRestart(t *testing.T) {
	cfg, err := parseConfig([]string{
		"-rpc1", "http://127.0.0.1:18545",
		"-rpc2", "http://127.0.0.1:28545",
		"-cl1", "http://127.0.0.1:13500",
		"-cl2", "http://127.0.0.1:23500",
		"-vc1-metrics", "http://127.0.0.1:18080/metrics",
		"-vc2-metrics", "http://127.0.0.1:28080/metrics",
		"-signer", "http://127.0.0.1:18550",
	})
	if err != nil {
		t.Fatal(err)
	}
	runner := &fakeRunner{outputs: map[string]string{}}
	k := kurtosis{enclave: cfg.enclave, runner: runner}
	if err := cfg.resolveEndpoints(t.Context(), k); err != nil {
		t.Fatal(err)
	}
	rpcURL, err := cfg.executionEndpoint(t.Context(), k, 1)
	if err != nil {
		t.Fatal(err)
	}
	clURL, err := cfg.beaconEndpoint(t.Context(), k, 1)
	if err != nil {
		t.Fatal(err)
	}
	vcURL, err := cfg.validatorEndpoint(t.Context(), k, 1)
	if err != nil {
		t.Fatal(err)
	}
	signerURL, err := cfg.signerEndpoint(t.Context(), k)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.rpcURLs[1] != "http://127.0.0.1:28545" || cfg.clURLs[1] != "http://127.0.0.1:23500" || cfg.vcMetricsURLs[1] != "http://127.0.0.1:28080/metrics" || cfg.signerURL != "http://127.0.0.1:18550" {
		t.Fatalf("explicit endpoints changed after refresh: %+v", cfg)
	}
	if rpcURL != cfg.rpcURLs[1] || clURL != cfg.clURLs[1] || vcURL != cfg.vcMetricsURLs[1] || signerURL != cfg.signerURL {
		t.Fatalf("explicit endpoint candidates changed: RPC=%s CL=%s VC=%s signer=%s", rpcURL, clURL, vcURL, signerURL)
	}
}

type fakeRunner struct {
	outputs map[string]string
}

func (r *fakeRunner) run(_ context.Context, args ...string) (string, error) {
	key := strings.Join(args, " ")
	output, ok := r.outputs[key]
	if !ok {
		return "", fmt.Errorf("unexpected command %s", key)
	}
	return output, nil
}

type runnerResult struct {
	output string
	err    error
}

type scriptedRunner struct {
	results map[string][]runnerResult
	calls   map[string]int
	onCall  func(string, int)
}

func (r *scriptedRunner) run(_ context.Context, args ...string) (string, error) {
	key := strings.Join(args, " ")
	r.calls[key]++
	if r.onCall != nil {
		r.onCall(key, r.calls[key])
	}
	results := r.results[key]
	if len(results) == 0 {
		return "", fmt.Errorf("unexpected command %s", key)
	}
	result := results[0]
	if len(results) > 1 {
		r.results[key] = results[1:]
	}
	return result.output, result.err
}

func TestRedialSecondExecutionCommitsOnlyHealthyCandidate(t *testing.T) {
	oldAPI := new(testExecutionHealthAPI)
	oldServer := rpc.NewServer()
	if err := oldServer.RegisterName("qrl", oldAPI); err != nil {
		t.Fatal(err)
	}
	oldClient := qrlclient.NewClient(rpc.DialInProc(oldServer))
	t.Cleanup(func() {
		oldClient.Close()
		oldServer.Stop()
	})

	newAPI := new(testExecutionHealthAPI)
	newRPC := rpc.NewServer()
	if err := newRPC.RegisterName("qrl", newAPI); err != nil {
		t.Fatal(err)
	}
	newHTTP := httptest.NewServer(newRPC)
	t.Cleanup(func() {
		newHTTP.Close()
		newRPC.Stop()
	})
	failingAPI := new(testExecutionFailureAPI)
	failingRPC := rpc.NewServer()
	if err := failingRPC.RegisterName("qrl", failingAPI); err != nil {
		t.Fatal(err)
	}
	failingHTTP := httptest.NewServer(failingRPC)
	t.Cleanup(func() {
		failingHTTP.Close()
		failingRPC.Stop()
	})

	cfg := config{
		enclave:             "test",
		elServices:          [2]string{"el1", "el2"},
		rpcURLs:             [2]string{"http://old-el1.invalid", "http://old-el2.invalid"},
		rpcURLsFromKurtosis: [2]bool{false, true},
		timeout:             time.Second,
		pollInterval:        time.Millisecond,
	}
	key := strings.Join([]string{"port", "print", cfg.enclave, cfg.elServices[1], "rpc", "--format", "ip,number"}, " ")
	var check *systemCheck
	var prematureCommit string
	runner := &scriptedRunner{
		results: map[string][]runnerResult{
			key: {
				{output: failingHTTP.URL},
				{output: newHTTP.URL},
			},
		},
		calls: make(map[string]int),
		onCall: func(calledKey string, count int) {
			if calledKey == key && count == 2 && (check.clients[1] != oldClient || check.cfg.rpcURLs[1] != "http://old-el2.invalid") {
				prematureCommit = "failed EL2 candidate changed live state before retry"
			}
		},
	}
	check = &systemCheck{
		cfg:     cfg,
		k:       kurtosis{enclave: cfg.enclave, runner: runner},
		clients: [2]*qrlclient.Client{nil, oldClient},
	}

	if err := check.redialSecondExecution(t.Context()); err != nil {
		t.Fatal(err)
	}
	if prematureCommit != "" {
		t.Fatal(prematureCommit)
	}
	t.Cleanup(func() { check.close() })
	if check.clients[1] == oldClient {
		t.Fatal("healthy candidate did not replace the cached EL2 client")
	}
	if check.cfg.rpcURLs[1] != newHTTP.URL {
		t.Fatalf("EL2 URL = %s, want %s", check.cfg.rpcURLs[1], newHTTP.URL)
	}
	if newAPI.chainIDCalls.Load() == 0 || newAPI.blockNumberCalls.Load() == 0 {
		t.Fatalf("candidate health calls: chain ID=%d block number=%d", newAPI.chainIDCalls.Load(), newAPI.blockNumberCalls.Load())
	}
	if failingAPI.chainIDCalls.Load() == 0 || runner.calls[key] < 2 {
		t.Fatalf("transient candidate calls: chain ID=%d discovery=%d", failingAPI.chainIDCalls.Load(), runner.calls[key])
	}
	if _, err := oldClient.ChainID(t.Context()); !errors.Is(err, rpc.ErrClientQuit) {
		t.Fatalf("old EL2 client remained open: %v", err)
	}
}

type testExecutionFailureAPI struct {
	chainIDCalls atomic.Int64
}

func (api *testExecutionFailureAPI) ChainId() (*hexutil.Big, error) {
	api.chainIDCalls.Add(1)
	return nil, errors.New("candidate is not ready")
}

func TestRedialSecondExecutionKeepsOldClientOnCandidateFailure(t *testing.T) {
	oldAPI := new(testExecutionHealthAPI)
	oldServer := rpc.NewServer()
	if err := oldServer.RegisterName("qrl", oldAPI); err != nil {
		t.Fatal(err)
	}
	oldClient := qrlclient.NewClient(rpc.DialInProc(oldServer))
	t.Cleanup(func() {
		oldClient.Close()
		oldServer.Stop()
	})

	failingAPI := new(testExecutionFailureAPI)
	failingRPC := rpc.NewServer()
	if err := failingRPC.RegisterName("qrl", failingAPI); err != nil {
		t.Fatal(err)
	}
	failingHTTP := httptest.NewServer(failingRPC)
	t.Cleanup(func() {
		failingHTTP.Close()
		failingRPC.Stop()
	})

	cfg := config{
		enclave:             "test",
		elServices:          [2]string{"el1", "el2"},
		rpcURLs:             [2]string{"http://old-el1.invalid", "http://old-el2.invalid"},
		rpcURLsFromKurtosis: [2]bool{false, true},
		timeout:             100 * time.Millisecond,
		pollInterval:        time.Millisecond,
	}
	key := strings.Join([]string{"port", "print", cfg.enclave, cfg.elServices[1], "rpc", "--format", "ip,number"}, " ")
	runner := &fakeRunner{outputs: map[string]string{key: failingHTTP.URL}}
	check := &systemCheck{
		cfg:     cfg,
		k:       kurtosis{enclave: cfg.enclave, runner: runner},
		clients: [2]*qrlclient.Client{nil, oldClient},
	}

	if err := check.redialSecondExecution(t.Context()); err == nil {
		t.Fatal("redial unexpectedly accepted an unhealthy candidate")
	}
	if check.clients[1] != oldClient || check.cfg.rpcURLs[1] != "http://old-el2.invalid" {
		t.Fatalf("failed candidate was committed: client=%p old=%p URL=%s", check.clients[1], oldClient, check.cfg.rpcURLs[1])
	}
	if _, err := oldClient.ChainID(t.Context()); err != nil {
		t.Fatalf("old EL2 client was closed after candidate failure: %v", err)
	}
	if failingAPI.chainIDCalls.Load() < 2 {
		t.Fatalf("candidate health was attempted %d times, want a bounded retry", failingAPI.chainIDCalls.Load())
	}
}

type testSignerAPI struct {
	versionCalls atomic.Int64
	listCalls    atomic.Int64
	accounts     []common.Address
}

func (api *testSignerAPI) Version() string {
	api.versionCalls.Add(1)
	return "6.1.0"
}

func (api *testSignerAPI) List() []common.Address {
	api.listCalls.Add(1)
	return api.accounts
}

func TestSignerReadinessRetriesDiscoveryAndCommitsHealthyEndpoint(t *testing.T) {
	signerAddress := common.Address{common.AddressLength - 1: 0x42}
	signerAPI := &testSignerAPI{accounts: []common.Address{signerAddress}}
	signerRPC := rpc.NewServer()
	if err := signerRPC.RegisterName("account", signerAPI); err != nil {
		t.Fatal(err)
	}
	signerHTTP := httptest.NewServer(signerRPC)
	t.Cleanup(func() {
		signerHTTP.Close()
		signerRPC.Stop()
	})
	var oldCalls atomic.Int64
	oldHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oldCalls.Add(1)
		http.Error(w, "obsolete endpoint", http.StatusServiceUnavailable)
	}))
	defer oldHTTP.Close()

	cfg := config{
		enclave:               "test",
		signerSvc:             "signer",
		signerURL:             oldHTTP.URL,
		signerURLFromKurtosis: true,
		signerAddress:         signerAddress,
		timeout:               time.Second,
		pollInterval:          time.Millisecond,
	}
	key := strings.Join([]string{"port", "print", cfg.enclave, cfg.signerSvc, "http", "--format", "ip,number"}, " ")
	runner := &scriptedRunner{
		results: map[string][]runnerResult{
			key: {
				{err: errors.New("published port is not ready")},
				{output: signerHTTP.URL},
			},
		},
		calls: make(map[string]int),
	}
	check := &systemCheck{cfg: cfg, k: kurtosis{enclave: cfg.enclave, runner: runner}}

	if err := check.waitSignerReady(t.Context()); err != nil {
		t.Fatal(err)
	}
	if runner.calls[key] < 2 {
		t.Fatalf("Kurtosis discovery calls = %d, want retry after transient failure", runner.calls[key])
	}
	if check.cfg.signerURL != signerHTTP.URL {
		t.Fatalf("signer URL = %s, want %s", check.cfg.signerURL, signerHTTP.URL)
	}
	if signerAPI.versionCalls.Load() == 0 || signerAPI.listCalls.Load() == 0 {
		t.Fatalf("new signer calls: version=%d list=%d", signerAPI.versionCalls.Load(), signerAPI.listCalls.Load())
	}
	if oldCalls.Load() != 0 {
		t.Fatalf("obsolete signer endpoint received %d readiness requests", oldCalls.Load())
	}
}

type testSignerOutageExecutionAPI struct{}

func (*testSignerOutageExecutionAPI) ChainId() *hexutil.Big {
	return (*hexutil.Big)(big.NewInt(32382))
}

func (*testSignerOutageExecutionAPI) BlockNumber() hexutil.Uint64 {
	return 42
}

func (*testSignerOutageExecutionAPI) SendTransaction(context.Context, map[string]any) (common.Hash, error) {
	return common.Hash{}, testRPCError{code: -32000, message: "external signer Clef is unavailable"}
}

func TestRestartSignerDoesNotDoubleStartAfterReadinessFailure(t *testing.T) {
	executionRPC := rpc.NewServer()
	if err := executionRPC.RegisterName("qrl", new(testSignerOutageExecutionAPI)); err != nil {
		t.Fatal(err)
	}
	if err := executionRPC.RegisterName("net", new(testExecutionHealthNetAPI)); err != nil {
		t.Fatal(err)
	}
	executionClient := qrlclient.NewClient(rpc.DialInProc(executionRPC))
	t.Cleanup(func() {
		executionClient.Close()
		executionRPC.Stop()
	})

	cfg := config{
		enclave:               "test",
		signerSvc:             "signer",
		signerURL:             "http://obsolete-signer.invalid",
		signerURLFromKurtosis: true,
		signerAddress:         common.Address{common.AddressLength - 1: 0x11},
		recipient:             common.Address{common.AddressLength - 1: 0x22},
		transferValue:         1,
		timeout:               50 * time.Millisecond,
		pollInterval:          time.Millisecond,
	}
	stopKey := strings.Join([]string{"service", "stop", cfg.enclave, cfg.signerSvc}, " ")
	startKey := strings.Join([]string{"service", "start", cfg.enclave, cfg.signerSvc}, " ")
	portKey := strings.Join([]string{"port", "print", cfg.enclave, cfg.signerSvc, "http", "--format", "ip,number"}, " ")
	runner := &scriptedRunner{
		results: map[string][]runnerResult{
			stopKey:  {{output: ""}},
			startKey: {{output: ""}},
			portKey:  {{err: errors.New("restarted port is not published")}},
		},
		calls: make(map[string]int),
	}
	check := &systemCheck{
		cfg:     cfg,
		k:       kurtosis{enclave: cfg.enclave, runner: runner},
		clients: [2]*qrlclient.Client{executionClient, nil},
	}

	err := check.restartSigner(t.Context())
	if err == nil || !strings.Contains(err.Error(), "Clef did not recover after restart") {
		t.Fatalf("restartSigner error = %v, want post-start readiness failure", err)
	}
	if runner.calls[stopKey] != 1 || runner.calls[startKey] != 1 {
		t.Fatalf("service lifecycle calls: stop=%d start=%d, want exactly one each", runner.calls[stopKey], runner.calls[startKey])
	}
	if runner.calls[portKey] < 2 {
		t.Fatalf("port discovery calls = %d, want bounded retries", runner.calls[portKey])
	}
}

func TestParticipantReadinessCommitsRediscoveredEndpoints(t *testing.T) {
	var oldCLCalls, oldVCCalls atomic.Int64
	oldCL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oldCLCalls.Add(1)
		http.Error(w, "obsolete endpoint", http.StatusServiceUnavailable)
	}))
	defer oldCL.Close()
	oldVC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		oldVCCalls.Add(1)
		http.Error(w, "obsolete endpoint", http.StatusServiceUnavailable)
	}))
	defer oldVC.Close()

	var newCLCalls, newVCCalls atomic.Int64
	newCLMux := http.NewServeMux()
	newCLMux.HandleFunc(syncStatusPath, func(w http.ResponseWriter, _ *http.Request) {
		newCLCalls.Add(1)
		fmt.Fprint(w, `{"data":{"head_slot":"96","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
	})
	newCL := httptest.NewServer(newCLMux)
	defer newCL.Close()
	newVC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		newVCCalls.Add(1)
		if r.URL.Path != "/metrics" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		fmt.Fprint(w, validatorMetricsFixture("vc2", expectedValidatorsPerClient, expectedValidatorsPerClient, 200))
	}))
	defer newVC.Close()
	badCL := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{}`)
	}))
	defer badCL.Close()
	badVC := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/metrics" {
			http.Error(w, "wrong path", http.StatusNotFound)
			return
		}
		fmt.Fprint(w, "# HELP unrelated_metric Not a validator readiness metric.\n# TYPE unrelated_metric gauge\nunrelated_metric 1\n")
	}))
	defer badVC.Close()

	cfg := config{
		enclave:                   "test",
		clServices:                [2]string{"cl1", "cl2"},
		vcServices:                [2]string{"vc1", "vc2"},
		clURLs:                    [2]string{"http://old-cl1.invalid", oldCL.URL},
		vcMetricsURLs:             [2]string{"http://old-vc1.invalid/metrics", oldVC.URL + "/metrics"},
		clURLsFromKurtosis:        [2]bool{false, true},
		vcMetricsURLsFromKurtosis: [2]bool{false, true},
		timeout:                   time.Second,
		pollInterval:              time.Millisecond,
		validatorPollInterval:     time.Millisecond,
	}
	clKey := strings.Join([]string{"port", "print", cfg.enclave, cfg.clServices[1], "http", "--format", "ip,number"}, " ")
	vcKey := strings.Join([]string{"port", "print", cfg.enclave, cfg.vcServices[1], "metrics", "--format", "ip,number"}, " ")
	var check *systemCheck
	var prematureCommit string
	runner := &scriptedRunner{
		results: map[string][]runnerResult{
			clKey: {{output: badCL.URL}, {output: newCL.URL}},
			vcKey: {{output: badVC.URL}, {output: newVC.URL + "/"}},
		},
		calls: make(map[string]int),
		onCall: func(key string, count int) {
			if count != 2 {
				return
			}
			switch key {
			case clKey:
				if check.cfg.clURLs[1] != oldCL.URL {
					prematureCommit = fmt.Sprintf("invalid CL2 candidate was committed before retry: %s", check.cfg.clURLs[1])
				}
			case vcKey:
				if check.cfg.vcMetricsURLs[1] != oldVC.URL+"/metrics" {
					prematureCommit = fmt.Sprintf("invalid VC2 candidate was committed before retry: %s", check.cfg.vcMetricsURLs[1])
				}
			}
		},
	}
	check = &systemCheck{
		cfg:  cfg,
		k:    kurtosis{enclave: cfg.enclave, runner: runner},
		http: httpReader{client: &http.Client{Timeout: time.Second}},
	}

	if err := check.waitBeaconReachable(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if err := check.waitMetricsReachable(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if prematureCommit != "" {
		t.Fatal(prematureCommit)
	}
	if check.cfg.clURLs[1] != newCL.URL || check.cfg.vcMetricsURLs[1] != newVC.URL+"/metrics" {
		t.Fatalf("rediscovered endpoints not committed: CL=%s VC=%s", check.cfg.clURLs[1], check.cfg.vcMetricsURLs[1])
	}
	if newCLCalls.Load() == 0 || newVCCalls.Load() == 0 {
		t.Fatalf("new endpoint calls: CL=%d VC=%d", newCLCalls.Load(), newVCCalls.Load())
	}
	if runner.calls[clKey] < 2 || runner.calls[vcKey] < 2 {
		t.Fatalf("transient discovery calls: CL=%d VC=%d", runner.calls[clKey], runner.calls[vcKey])
	}
	if oldCLCalls.Load() != 0 || oldVCCalls.Load() != 0 {
		t.Fatalf("obsolete endpoint calls: CL=%d VC=%d", oldCLCalls.Load(), oldVCCalls.Load())
	}
}

func TestBeaconStatus(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(syncStatusPath, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"head_slot":"96","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
	})
	mux.HandleFunc(peerCountPath, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"connected":"1"}}`)
	})
	mux.HandleFunc(finalityPath, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"execution_optimistic":false,"data":{"finalized":{"epoch":"2","root":"0x1234"}}}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	reader := httpReader{client: server.Client()}
	status, err := reader.beaconStatus(t.Context(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if status.headSlot != 96 || status.finalizedEpoch != 2 || status.connectedPeers != 1 || status.finalizedRoot != "0x1234" {
		t.Fatalf("unexpected beacon status: %+v", status)
	}
}

func TestFinalizedExecutionPayload(t *testing.T) {
	hash := common.HexToHash("0x1234")
	mux := http.NewServeMux()
	mux.HandleFunc(finalizedBlockPath, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"version":"zond","execution_optimistic":false,"finalized":true,"data":{"message":{"body":{"execution_payload":{"block_number":"42","block_hash":%q}}}}}`, hash.Hex())
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	payload, err := (httpReader{client: server.Client()}).finalizedExecutionPayload(t.Context(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if payload.blockNumber != 42 || payload.blockHash != hash {
		t.Fatalf("unexpected finalized execution payload: %+v", payload)
	}
}

func TestFinalizedExecutionPayloadRejectsLegacyForkName(t *testing.T) {
	hash := common.HexToHash("0x1234")
	mux := http.NewServeMux()
	mux.HandleFunc(finalizedBlockPath, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, `{"version":"capella","execution_optimistic":false,"finalized":true,"data":{"message":{"body":{"execution_payload":{"block_number":"42","block_hash":%q}}}}}`, hash.Hex())
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	_, err := (httpReader{client: server.Client()}).finalizedExecutionPayload(t.Context(), server.URL)
	if err == nil || !strings.Contains(err.Error(), `version is "capella", want zond`) {
		t.Fatalf("got %v, want legacy fork-name rejection", err)
	}
}

func TestBeaconStatusRejectsExecutionDisconnect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(syncStatusPath, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"head_slot":"96","sync_distance":"0","is_syncing":false,"is_optimistic":false,"el_offline":true}}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	reader := httpReader{client: server.Client()}
	_, err := reader.beaconStatus(t.Context(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "execution client offline") {
		t.Fatalf("got %v, want execution-offline error", err)
	}
}

func TestBeaconStatusRejectsSyncDistance(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(syncStatusPath, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"head_slot":"96","sync_distance":"1","is_syncing":false,"is_optimistic":false,"el_offline":false}}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	reader := httpReader{client: server.Client()}
	_, err := reader.beaconStatus(t.Context(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "sync distance is 1") {
		t.Fatalf("got %v, want nonzero-sync-distance error", err)
	}
}

func TestHTTPRespondsTreatsAnyStatusAsReachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	reader := httpReader{client: server.Client()}
	if err := reader.responds(t.Context(), server.URL); err != nil {
		t.Fatalf("non-2xx response did not prove endpoint reachability: %v", err)
	}
	server.Close()
	if err := reader.responds(t.Context(), server.URL); err == nil {
		t.Fatal("closed endpoint reported reachable")
	}
}

func TestBeaconSpec(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc(specPath, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"data":{"PRESET_BASE":"mainnet","SECONDS_PER_SLOT":"5","SLOTS_PER_EPOCH":"128","CONFIG_NAME":"vm64"}}`)
	})
	server := httptest.NewServer(mux)
	defer server.Close()
	reader := httpReader{client: server.Client()}
	spec, err := reader.beaconSpec(t.Context(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if spec["PRESET_BASE"] != "mainnet" || spec["CONFIG_NAME"] != "vm64" {
		t.Fatalf("unexpected beacon spec: %#v", spec)
	}
}

func TestValidateBeaconSpecs(t *testing.T) {
	valid := map[string]string{
		"PRESET_BASE":      "mainnet",
		"SECONDS_PER_SLOT": "5",
		"SLOTS_PER_EPOCH":  "128",
		"CONFIG_NAME":      "vm64",
	}
	clone := func(input map[string]string) map[string]string {
		output := make(map[string]string, len(input))
		for key, value := range input {
			output[key] = value
		}
		return output
	}
	if err := validateBeaconSpecs([2]map[string]string{clone(valid), clone(valid)}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func([2]map[string]string)
		want string
	}{
		{name: "wrong slot duration", edit: func(specs [2]map[string]string) { specs[0]["SECONDS_PER_SLOT"] = "6" }, want: "SECONDS_PER_SLOT"},
		{name: "missing epoch size", edit: func(specs [2]map[string]string) { delete(specs[1], "SLOTS_PER_EPOCH") }, want: "missing SLOTS_PER_EPOCH"},
		{name: "different complete specs", edit: func(specs [2]map[string]string) { specs[1]["CONFIG_NAME"] = "other" }, want: "specs differ"},
		{name: "different empty fields", edit: func(specs [2]map[string]string) { specs[0]["EMPTY_A"] = ""; specs[1]["EMPTY_B"] = "" }, want: "specs differ"},
	} {
		t.Run(test.name, func(t *testing.T) {
			specs := [2]map[string]string{clone(valid), clone(valid)}
			test.edit(specs)
			err := validateBeaconSpecs(specs)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestValidatorMetrics(t *testing.T) {
	metrics, err := parseMetrics(validatorMetricsFixture("key", expectedValidatorsPerClient, expectedValidatorsPerClient, 100))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotValidatorMetrics(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.reportedValidators != expectedValidatorsPerClient || snapshot.activeValidators != expectedValidatorsPerClient || snapshot.attestedValidators != expectedValidatorsPerClient || snapshot.successfulAttestations != expectedValidatorsPerClient || snapshot.successfulProposals != 1 || snapshot.processStartTime != 100 {
		t.Fatalf("unexpected validator snapshot: %+v", snapshot)
	}
	if err := validateValidatorSnapshot(snapshot, true, true, true); err != nil {
		t.Fatal(err)
	}
	metrics["validator_successful_attestations"] = nil
	snapshot, err = snapshotValidatorMetrics(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateValidatorSnapshot(snapshot, true, true, true); err == nil || !strings.Contains(err.Error(), "only 0 of 64") {
		t.Fatalf("got %v, want per-validator attestation error", err)
	}
	metrics, err = parseMetrics(validatorMetricsFixture("key", expectedValidatorsPerClient, expectedValidatorsPerClient, 100))
	if err != nil {
		t.Fatal(err)
	}
	metrics["validator_successful_proposals"] = nil
	snapshot, err = snapshotValidatorMetrics(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateValidatorSnapshot(snapshot, true, true, true); err == nil || !strings.Contains(err.Error(), "no successful validator proposal") {
		t.Fatalf("got %v, want missing-proposal error", err)
	}
	metrics["validator_successful_proposals"] = []metricSample{{labels: map[string]string{"pubkey": "key-0"}, value: 1}}
	metrics["validator_failed_attestations"] = []metricSample{{labels: map[string]string{"pubkey": "key-0"}, value: 1}}
	snapshot, err = snapshotValidatorMetrics(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateValidatorSnapshot(snapshot, true, true, true); err == nil || !strings.Contains(err.Error(), "failed attestations") {
		t.Fatalf("got %v, want failed-attestation error", err)
	}
	metrics["validator_failed_attestations"] = nil
	metrics["validator_failed_proposals"] = []metricSample{{labels: map[string]string{"pubkey": "key-0"}, value: 2}}
	snapshot, err = snapshotValidatorMetrics(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateValidatorSnapshot(snapshot, true, true, true); err == nil || !strings.Contains(err.Error(), "failed proposals") {
		t.Fatalf("got %v, want failed-proposal error", err)
	}
	if err := validateValidatorSnapshot(snapshot, true, false, false); err != nil {
		t.Fatalf("post-fault activity gate rejected cumulative duty failures: %v", err)
	}
}

func TestInitialValidatorActivityDutyHistoryModes(t *testing.T) {
	for _, test := range []struct {
		name       string
		strict     bool
		wantFailed bool
	}{
		{name: "standalone baselines cumulative history"},
		{name: "canonical gate requires zero history", strict: true, wantFailed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			servers := [2]*httptest.Server{
				httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(validatorMetricsFixtureWithFailures("vc1", expectedValidatorsPerClient, expectedValidatorsPerClient, 100, 2)))
				})),
				httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = w.Write([]byte(validatorMetricsFixtureWithFailures("vc2", expectedValidatorsPerClient, expectedValidatorsPerClient, 200, 3)))
				})),
			}
			defer servers[0].Close()
			defer servers[1].Close()

			check := &systemCheck{
				cfg: config{
					vcMetricsURLs:          [2]string{servers[0].URL, servers[1].URL},
					timeout:                time.Second,
					pollInterval:           time.Millisecond,
					validatorPollInterval:  time.Millisecond,
					requireZeroDutyHistory: test.strict,
				},
				http: httpReader{client: &http.Client{Timeout: time.Second}},
			}
			snapshots, err := check.waitInitialValidatorActivity(t.Context())
			if test.wantFailed {
				var dutyFailure *validatorDutyFailureError
				if !errors.As(err, &dutyFailure) {
					t.Fatalf("got %v, want strict duty-history failure", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if snapshots[0].failedAttestations != 2 || snapshots[1].failedAttestations != 3 {
				t.Fatalf("cumulative failure baseline was not preserved: %+v", snapshots)
			}
		})
	}
}

func TestValidatorContinuityCadenceAndForcedBoundary(t *testing.T) {
	var raised atomic.Bool
	var requests [2]atomic.Int64
	servers := [2]*httptest.Server{}
	baselines := validatorDutySnapshots{}
	for i := range servers {
		prefix := fmt.Sprintf("vc%d", i+1)
		processStart := float64(100 + i)
		body := validatorMetricsFixtureWithFailures(prefix, expectedValidatorsPerClient, expectedValidatorsPerClient, processStart, 2)
		metrics, err := parseMetrics(body)
		if err != nil {
			t.Fatal(err)
		}
		baselines[i], err = snapshotValidatorMetrics(metrics)
		if err != nil {
			t.Fatal(err)
		}
		index := i
		servers[i] = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests[index].Add(1)
			failures := float64(2)
			if raised.Load() {
				failures++
			}
			_, _ = w.Write([]byte(validatorMetricsFixtureWithFailures(prefix, expectedValidatorsPerClient, expectedValidatorsPerClient, processStart, failures)))
		}))
		defer servers[i].Close()
	}

	check := &systemCheck{
		cfg: config{
			vcMetricsURLs:         [2]string{servers[0].URL, servers[1].URL},
			pollInterval:          time.Millisecond,
			validatorPollInterval: time.Hour,
		},
		http: httpReader{client: &http.Client{Timeout: time.Second}},
	}
	if err := check.checkValidatorContinuityIfDue(t.Context(), baselines, false, 0, 1); err != nil {
		t.Fatal(err)
	}
	if requests[0].Load() != 1 || requests[1].Load() != 1 {
		t.Fatalf("initial continuity check requests = %d/%d, want 1/1", requests[0].Load(), requests[1].Load())
	}
	raised.Store(true)
	if err := check.checkValidatorContinuityIfDue(t.Context(), baselines, false, 0, 1); err != nil {
		t.Fatalf("not-due continuity check should have reused the recent sample: %v", err)
	}
	if requests[0].Load() != 1 || requests[1].Load() != 1 {
		t.Fatalf("not-due continuity check unexpectedly scraped metrics: %d/%d", requests[0].Load(), requests[1].Load())
	}
	var dutyFailure *validatorDutyFailureError
	if err := check.checkValidatorContinuityIfDue(t.Context(), baselines, true, 0, 1); !errors.As(err, &dutyFailure) {
		t.Fatalf("forced boundary check got %v, want new duty failure", err)
	}
	if requests[0].Load() != 2 || requests[1].Load() != 1 {
		t.Fatalf("forced boundary check requests = %d/%d, want 2/1 after VC1 terminal failure", requests[0].Load(), requests[1].Load())
	}
}

func validatorMetricsFixture(prefix string, validators, attested int, processStart float64) string {
	var input strings.Builder
	fmt.Fprintf(&input, "# TYPE process_start_time_seconds gauge\nprocess_start_time_seconds %.3f\n", processStart)
	input.WriteString("# TYPE validator_statuses gauge\n")
	for i := 0; i < validators; i++ {
		fmt.Fprintf(&input, "validator_statuses{pubkey=%q} 3\n", fmt.Sprintf("%s-%d", prefix, i))
	}
	input.WriteString("# TYPE validator_last_attested_slot gauge\n")
	for i := 0; i < attested; i++ {
		fmt.Fprintf(&input, "validator_last_attested_slot{pubkey=%q} %d\n", fmt.Sprintf("%s-%d", prefix, i), i+1)
	}
	input.WriteString("# TYPE validator_successful_attestations counter\n")
	for i := 0; i < attested; i++ {
		fmt.Fprintf(&input, "validator_successful_attestations{pubkey=%q} 1\n", fmt.Sprintf("%s-%d", prefix, i))
	}
	if validators > 0 {
		input.WriteString("# TYPE validator_successful_proposals counter\n")
		fmt.Fprintf(&input, "validator_successful_proposals{pubkey=%q} 1\n", prefix+"-0")
	}
	return input.String()
}

func validatorMetricsFixtureWithFailures(prefix string, validators, attested int, processStart, failedAttestations float64) string {
	input := validatorMetricsFixture(prefix, validators, attested, processStart)
	if failedAttestations == 0 {
		return input
	}
	return input + fmt.Sprintf("# TYPE validator_failed_attestations counter\nvalidator_failed_attestations{pubkey=%q} %.0f\n", prefix+"-0", failedAttestations)
}

func TestValidatorMetricsRequiresExactActiveCardinality(t *testing.T) {
	for _, test := range []struct {
		name     string
		statuses []metricSample
		want     string
	}{
		{name: "too few", statuses: activeValidatorStatuses(expectedValidatorsPerClient - 1), want: "reported 63 validators, want exactly 64"},
		{name: "too many", statuses: activeValidatorStatuses(expectedValidatorsPerClient + 1), want: "reported 65 validators, want exactly 64"},
		{name: "one not active", statuses: append(activeValidatorStatuses(expectedValidatorsPerClient-1), metricSample{labels: map[string]string{"pubkey": "key-inactive"}, value: 2}), want: "reported 63 active validators, want exactly 64"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := snapshotValidatorMetrics(metricSet{
				"process_start_time_seconds": {{value: 100}},
				"validator_statuses":         test.statuses,
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}

	duplicate := activeValidatorStatuses(expectedValidatorsPerClient)
	duplicate[len(duplicate)-1].labels["pubkey"] = "key-0"
	_, err := snapshotValidatorMetrics(metricSet{
		"process_start_time_seconds": {{value: 100}},
		"validator_statuses":         duplicate,
	})
	if err == nil || !strings.Contains(err.Error(), "repeats pubkey") {
		t.Fatalf("got %v, want duplicate-pubkey error", err)
	}
}

func TestValidatorMetricsRequireEveryKeyToHaveAttested(t *testing.T) {
	metrics, err := parseMetrics(validatorMetricsFixture("key", expectedValidatorsPerClient, expectedValidatorsPerClient-1, 100))
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := snapshotValidatorMetrics(metrics)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.attestedValidators != expectedValidatorsPerClient-1 {
		t.Fatalf("got %d individually attested validators, want %d", snapshot.attestedValidators, expectedValidatorsPerClient-1)
	}
	if err := validateValidatorSnapshot(snapshot, true, true, true); err == nil || !strings.Contains(err.Error(), "only 63 of 64") {
		t.Fatalf("got %v, want per-key attestation error", err)
	}
}

func activeValidatorStatuses(count int) []metricSample {
	statuses := make([]metricSample, count)
	for i := range statuses {
		statuses[i] = metricSample{labels: map[string]string{"pubkey": fmt.Sprintf("key-%d", i)}, value: 3}
	}
	return statuses
}

func TestValidateValidatorProgress(t *testing.T) {
	baseline := validatorDutySnapshot{
		reportedValidators:     expectedValidatorsPerClient,
		activeValidators:       expectedValidatorsPerClient,
		attestedValidators:     expectedValidatorsPerClient,
		activePubkeys:          validatorPubkeySet("key"),
		processStartTime:       100,
		successfulAttestations: 100,
		successfulProposals:    4,
		failedAttestations:     2,
		failedProposals:        1,
	}
	progressed := baseline
	progressed.successfulAttestations++
	if err := validateValidatorProgress(baseline, progressed); err != nil {
		t.Fatalf("valid activity delta rejected: %v", err)
	}
	if err := validateValidatorProgress(baseline, baseline); err == nil || !strings.Contains(err.Error(), "no successful validator attestation since baseline") {
		t.Fatalf("got %v, want no-progress error", err)
	}

	failed := progressed
	failed.failedAttestations++
	err := validateValidatorProgress(baseline, failed)
	var dutyFailure *validatorDutyFailureError
	if !errors.As(err, &dutyFailure) || dutyFailure.count != 1 {
		t.Fatalf("got %v, want one new duty failure", err)
	}

	regressed := progressed
	regressed.successfulProposals--
	err = validateValidatorProgress(baseline, regressed)
	var regression *validatorCounterRegressionError
	if !errors.As(err, &regression) {
		t.Fatalf("got %v, want counter-regression error", err)
	}

	restarted := progressed
	restarted.processStartTime += validatorProcessStartTimeToleranceSeconds + 1
	err = validateValidatorProgress(baseline, restarted)
	var processReset *validatorProcessResetError
	if !errors.As(err, &processReset) {
		t.Fatalf("got %v, want process-reset error", err)
	}
}

func TestValidatorProcessStartTimeTolerance(t *testing.T) {
	baseline := validatorSnapshotForTest("key", 100, 100, 4, 0, 0)
	for _, delta := range []float64{
		-validatorProcessStartTimeToleranceSeconds,
		-1,
		1,
		validatorProcessStartTimeToleranceSeconds,
	} {
		t.Run(fmt.Sprintf("delta_%+.3f", delta), func(t *testing.T) {
			observed := baseline
			observed.processStartTime += delta
			if err := validateValidatorContinuity(baseline, observed); err != nil {
				t.Fatalf("process-start jitter of %+.3fs rejected: %v", delta, err)
			}
		})
	}

	for _, delta := range []float64{
		-validatorProcessStartTimeToleranceSeconds - 0.001,
		validatorProcessStartTimeToleranceSeconds + 0.001,
	} {
		t.Run(fmt.Sprintf("reject_delta_%+.3f", delta), func(t *testing.T) {
			observed := baseline
			observed.processStartTime += delta
			err := validateValidatorContinuity(baseline, observed)
			var processReset *validatorProcessResetError
			if !errors.As(err, &processReset) {
				t.Fatalf("got %v, want process-reset error for %+.3fs beyond tolerance", err, delta)
			}
		})
	}

	t.Run("final5 regression", func(t *testing.T) {
		before := validatorSnapshotForTest("key", 1784540313.920, 100, 4, 0, 0)
		after := before
		after.processStartTime = 1784540314.920
		if err := validateValidatorContinuity(before, after); err != nil {
			t.Fatalf("known unchanged-process metric jitter rejected: %v", err)
		}
	})
}

func TestValidatorCountersDetectResetWithinProcessStartTolerance(t *testing.T) {
	baseline := validatorSnapshotForTest("key", 100, 100, 4, 2, 1)
	reset := baseline
	reset.processStartTime += validatorProcessStartTimeToleranceSeconds
	reset.successfulAttestations = 0
	reset.successfulProposals = 0
	reset.failedAttestations = 0
	reset.failedProposals = 0

	err := validateValidatorContinuity(baseline, reset)
	var regression *validatorCounterRegressionError
	if !errors.As(err, &regression) {
		t.Fatalf("got %v, want counter-regression error for reset hidden by timestamp jitter", err)
	}
}

func TestValidatorKeySetChangeDetectedWithinProcessStartTolerance(t *testing.T) {
	baseline := validatorSnapshotForTest("key", 100, 100, 4, 0, 0)
	observed := baseline
	observed.processStartTime += validatorProcessStartTimeToleranceSeconds
	observed.activePubkeys = validatorPubkeySet("changed")

	err := validateValidatorContinuity(baseline, observed)
	var topology *validatorTopologyError
	if !errors.As(err, &topology) {
		t.Fatalf("got %v, want topology error for a changed key set hidden by timestamp jitter", err)
	}
}

func validatorPubkeySet(prefix string) map[string]struct{} {
	keys := make(map[string]struct{}, expectedValidatorsPerClient)
	for i := 0; i < expectedValidatorsPerClient; i++ {
		keys[fmt.Sprintf("%s-%d", prefix, i)] = struct{}{}
	}
	return keys
}

func TestValidatorKeyIsolation(t *testing.T) {
	first := validatorDutySnapshot{activePubkeys: validatorPubkeySet("vc1")}
	second := validatorDutySnapshot{activePubkeys: validatorPubkeySet("vc2")}
	if err := validateDisjointValidatorKeys(validatorDutySnapshots{first, second}); err != nil {
		t.Fatal(err)
	}
	second.activePubkeys = validatorPubkeySet("vc1")
	if err := validateDisjointValidatorKeys(validatorDutySnapshots{first, second}); err == nil || !strings.Contains(err.Error(), "both manage pubkey") {
		t.Fatalf("got %v, want overlapping-pubkey error", err)
	}
}

func TestRestartedValidatorBaseline(t *testing.T) {
	preFault := validatorDutySnapshots{
		validatorSnapshotForTest("vc1", 100, 100, 4, 0, 0),
		validatorSnapshotForTest("vc2", 200, 100, 4, 0, 0),
	}
	observed := validatorDutySnapshots{
		validatorSnapshotForTest("vc1", 100, 120, 5, 0, 0),
		validatorSnapshotForTest("vc2", 203, 1, 0, 0, 0),
	}
	baseline, err := restartedValidatorBaseline(preFault, observed)
	if err != nil {
		t.Fatal(err)
	}
	if baseline[0].successfulAttestations != preFault[0].successfulAttestations || baseline[0].processStartTime != preFault[0].processStartTime {
		t.Fatalf("VC1 pre-fault baseline was not preserved: %+v", baseline[0])
	}
	if baseline[1].processStartTime != observed[1].processStartTime {
		t.Fatalf("VC2 fresh baseline was not captured: %+v", baseline[1])
	}

	for _, test := range []struct {
		name string
		edit func(*validatorDutySnapshots)
		want string
	}{
		{name: "VC1 reset", edit: func(got *validatorDutySnapshots) {
			got[0].processStartTime += validatorProcessStartTimeToleranceSeconds + 1
		}, want: "VC1 across participant-two outage"},
		{name: "VC1 new failure", edit: func(got *validatorDutySnapshots) { got[0].failedAttestations++ }, want: "failed attestations since baseline"},
		{name: "VC2 stale process", edit: func(got *validatorDutySnapshots) { got[1].processStartTime = preFault[1].processStartTime }, want: "has not advanced"},
		{name: "VC2 only timestamp jitter", edit: func(got *validatorDutySnapshots) {
			got[1].processStartTime = preFault[1].processStartTime + validatorProcessStartTimeToleranceSeconds
		}, want: "has not advanced"},
		{name: "VC2 fresh failure", edit: func(got *validatorDutySnapshots) { got[1].failedProposals++ }, want: "VC2 fresh process"},
		{name: "VC2 changed keys", edit: func(got *validatorDutySnapshots) { got[1].activePubkeys = validatorPubkeySet("vc2-new") }, want: "changed across restart"},
		{name: "overlapping keys", edit: func(got *validatorDutySnapshots) { got[1].activePubkeys = validatorPubkeySet("vc1") }, want: "both manage pubkey"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := observed
			test.edit(&got)
			if _, err := restartedValidatorBaseline(preFault, got); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestValidatorRecoveryRequiresEveryRestartedKeyToAttest(t *testing.T) {
	baseline := validatorDutySnapshots{
		validatorSnapshotForTest("vc1", 100, 100, 4, 0, 0),
		validatorSnapshotForTest("vc2", 200, 0, 0, 0, 0),
	}
	baseline[1].attestedValidators = 0
	progressed := baseline
	progressed[0].successfulAttestations++
	progressed[1].successfulAttestations++
	progressed[1].attestedValidators = expectedValidatorsPerClient - 1
	if err := validateValidatorProgressSnapshots(baseline, progressed); err != nil {
		t.Fatalf("aggregate recovery progress rejected before the per-key gate: %v", err)
	}
	if err := validateEveryValidatorAttested(progressed, 1); err == nil || !strings.Contains(err.Error(), "only 63 of 64") {
		t.Fatalf("got %v, want incomplete restarted-key activity error", err)
	}
	progressed[1].attestedValidators = expectedValidatorsPerClient
	if err := validateEveryValidatorAttested(progressed, 1); err != nil {
		t.Fatalf("complete restarted-key activity rejected: %v", err)
	}
}

func validatorSnapshotForTest(prefix string, processStart, successfulAttestations, successfulProposals, failedAttestations, failedProposals float64) validatorDutySnapshot {
	return validatorDutySnapshot{
		reportedValidators:     expectedValidatorsPerClient,
		activeValidators:       expectedValidatorsPerClient,
		attestedValidators:     expectedValidatorsPerClient,
		activePubkeys:          validatorPubkeySet(prefix),
		processStartTime:       processStart,
		successfulAttestations: successfulAttestations,
		successfulProposals:    successfulProposals,
		failedAttestations:     failedAttestations,
		failedProposals:        failedProposals,
	}
}

func TestBeaconFinalityWaitMonitorsBothValidatorClients(t *testing.T) {
	baseline := validatorDutySnapshots{
		validatorSnapshotForTest("vc1", 100, expectedValidatorsPerClient, 1, 0, 0),
		validatorSnapshotForTest("vc2", 200, expectedValidatorsPerClient, 1, 0, 0),
	}
	vc1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, validatorMetricsFixture("vc1", expectedValidatorsPerClient, expectedValidatorsPerClient, 100))
	}))
	defer vc1.Close()

	for _, test := range []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{name: "process reset", handler: func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprint(w, validatorMetricsFixture("vc2", expectedValidatorsPerClient, expectedValidatorsPerClient, 200+validatorProcessStartTimeToleranceSeconds+1))
		}, want: "process start time changed"},
		{name: "endpoint loss", handler: func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "stopped", http.StatusServiceUnavailable)
		}, want: "VC2 is unhealthy"},
	} {
		t.Run(test.name, func(t *testing.T) {
			vc2 := httptest.NewServer(test.handler)
			defer vc2.Close()
			check := &systemCheck{
				cfg: config{
					timeout:       time.Second,
					pollInterval:  time.Millisecond,
					vcMetricsURLs: [2]string{vc1.URL, vc2.URL},
				},
				http: httpReader{client: &http.Client{Timeout: time.Second}},
			}
			if _, err := check.waitBeaconConvergence(t.Context(), 1, 0, baseline); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want validator-monitoring error containing %q", err, test.want)
			}
		})
	}
}

type testBlockNumberAPI struct{}

func (testBlockNumberAPI) BlockNumber() hexutil.Uint64 { return 1 }

func newTestBlockNumberClient(t *testing.T) *qrlclient.Client {
	t.Helper()
	server := rpc.NewServer()
	if err := server.RegisterName("qrl", testBlockNumberAPI{}); err != nil {
		t.Fatal(err)
	}
	client := qrlclient.NewClient(rpc.DialInProc(server))
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	return client
}

func TestExecutionFinalityAndWithdrawalWaitsMonitorValidators(t *testing.T) {
	baseline := validatorDutySnapshots{
		validatorSnapshotForTest("vc1", 100, expectedValidatorsPerClient, 1, 0, 0),
		validatorSnapshotForTest("vc2", 200, expectedValidatorsPerClient, 1, 0, 0),
	}
	vc1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, validatorMetricsFixture("vc1", expectedValidatorsPerClient, expectedValidatorsPerClient, 100))
	}))
	defer vc1.Close()
	vc2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, validatorMetricsFixture("vc2", expectedValidatorsPerClient, expectedValidatorsPerClient, 200+validatorProcessStartTimeToleranceSeconds+1))
	}))
	defer vc2.Close()

	check := &systemCheck{
		cfg: config{
			timeout:       time.Second,
			pollInterval:  time.Millisecond,
			vcMetricsURLs: [2]string{vc1.URL, vc2.URL},
		},
		http: httpReader{client: &http.Client{Timeout: time.Second}},
		clients: [2]*qrlclient.Client{
			newTestBlockNumberClient(t),
			newTestBlockNumberClient(t),
		},
	}
	if _, err := check.waitExecutionFinality(t.Context(), baseline, 1, nil); err == nil || !strings.Contains(err.Error(), "process start time changed") {
		t.Fatalf("execution-finality wait got %v, want validator continuity error", err)
	}
	if err := check.waitAutomaticWithdrawal(t.Context(), baseline); err == nil || !strings.Contains(err.Error(), "process start time changed") {
		t.Fatalf("withdrawal wait got %v, want validator continuity error", err)
	}
}

func TestWaitForRetries(t *testing.T) {
	attempts := 0
	err := waitFor(t.Context(), time.Second, time.Millisecond, "third attempt", func(context.Context) (bool, error) {
		attempts++
		if attempts < 3 {
			return false, fmt.Errorf("attempt %d", attempts)
		}
		return true, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if attempts != 3 {
		t.Fatalf("got %d attempts, want 3", attempts)
	}
}

func TestWaitForFailsImmediatelyOnTerminalValidatorErrors(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want string
	}{
		{name: "duty failure", err: &validatorDutyFailureError{duty: "attestations", count: 1}, want: "failed attestations"},
		{name: "dead endpoint", err: &validatorHealthError{client: 1, err: errors.New("connection refused")}, want: "connection refused"},
		{name: "counter reset", err: &validatorCounterRegressionError{metric: "successful attestations", before: 4, after: 0}, want: "regressed"},
		{name: "process reset", err: &validatorProcessResetError{before: 100, after: 101}, want: "start time changed"},
		{name: "key overlap", err: &validatorTopologyError{err: errors.New("overlap")}, want: "topology is invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			attempts := 0
			err := waitFor(t.Context(), time.Second, time.Millisecond, "validator health", func(context.Context) (bool, error) {
				attempts++
				return false, fmt.Errorf("VC1: %w", test.err)
			})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want terminal error containing %q", err, test.want)
			}
			if attempts != 1 {
				t.Fatalf("terminal validator error retried %d times, want one attempt", attempts)
			}
		})
	}
}

func TestMinimumFeeRecipientReward(t *testing.T) {
	header := &types.Header{BaseFee: big.NewInt(100)}
	receipt := &types.Receipt{EffectiveGasPrice: big.NewInt(103), GasUsed: 21_000}
	reward, err := minimumFeeRecipientReward(header, receipt)
	if err != nil {
		t.Fatal(err)
	}
	if want := big.NewInt(63_000); reward.Cmp(want) != 0 {
		t.Fatalf("reward %s, want %s", reward, want)
	}
	for _, test := range []struct {
		name    string
		header  *types.Header
		receipt *types.Receipt
		want    string
	}{
		{name: "missing base fee", header: &types.Header{}, receipt: receipt, want: "no base fee"},
		{name: "missing gas price", header: header, receipt: &types.Receipt{GasUsed: 1}, want: "no effective gas price"},
		{name: "zero gas", header: header, receipt: &types.Receipt{EffectiveGasPrice: big.NewInt(101)}, want: "zero gas"},
		{name: "zero tip", header: header, receipt: &types.Receipt{EffectiveGasPrice: big.NewInt(100), GasUsed: 1}, want: "positive"},
		{name: "negative tip", header: header, receipt: &types.Receipt{EffectiveGasPrice: big.NewInt(99), GasUsed: 1}, want: "positive"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := minimumFeeRecipientReward(test.header, test.receipt)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestWithdrawalEvidence(t *testing.T) {
	recipient, err := common.NewAddressFromString(expectedWithdrawalAddress)
	if err != nil {
		t.Fatal(err)
	}
	withdrawalsA := types.Withdrawals{
		&types.Withdrawal{Index: 10, Validator: 20, Address: recipient, Amount: 2},
		&types.Withdrawal{Index: 11, Validator: 21, Address: common.MaxAddress, Amount: 3},
	}
	withdrawalsB := types.Withdrawals{
		&types.Withdrawal{Index: 10, Validator: 20, Address: recipient, Amount: 2},
		&types.Withdrawal{Index: 11, Validator: 21, Address: common.MaxAddress, Amount: 3},
	}
	if err := compareWithdrawals(withdrawalsA, withdrawalsB); err != nil {
		t.Fatal(err)
	}
	value, err := withdrawalValue(withdrawalsA, recipient)
	if err != nil {
		t.Fatal(err)
	}
	if want := big.NewInt(2 * params.Shor); value.Cmp(want) != 0 {
		t.Fatalf("withdrawal value %s, want %s", value, want)
	}
	withdrawalsB[1].Amount++
	if err := compareWithdrawals(withdrawalsA, withdrawalsB); err == nil || !strings.Contains(err.Error(), "differs") {
		t.Fatalf("got %v, want withdrawal mismatch", err)
	}
	if _, err := withdrawalValue(types.Withdrawals{nil}, recipient); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("got %v, want nil-withdrawal error", err)
	}
	emptyRoot := types.EmptyWithdrawalsHash
	if err := validateWithdrawalRoot(&types.Header{WithdrawalsHash: &emptyRoot}, nil); err != nil {
		t.Fatalf("valid empty withdrawal root rejected: %v", err)
	}
	wrongRoot := common.HexToHash("0x01")
	if err := validateWithdrawalRoot(&types.Header{WithdrawalsHash: &wrongRoot}, nil); err == nil || !strings.Contains(err.Error(), "calculated") {
		t.Fatalf("got %v, want withdrawal-root mismatch", err)
	}
	if err := validateWithdrawalRoot(&types.Header{}, nil); err == nil || !strings.Contains(err.Error(), "no withdrawals root") {
		t.Fatalf("got %v, want missing-root error", err)
	}

	original := withdrawalEvidence{blockNumber: 42, blockHash: common.HexToHash("0x42"), amount: big.NewInt(2 * params.Shor)}
	confirmed := withdrawalEvidence{blockNumber: original.blockNumber, blockHash: original.blockHash, amount: new(big.Int).Set(original.amount)}
	if err := validateWithdrawalEvidence(original, confirmed); err != nil {
		t.Fatalf("matching finalized withdrawal evidence rejected: %v", err)
	}
	for _, test := range []struct {
		name string
		edit func(*withdrawalEvidence)
		want string
	}{
		{name: "block number", edit: func(got *withdrawalEvidence) { got.blockNumber++ }, want: "block number changed"},
		{name: "block hash", edit: func(got *withdrawalEvidence) { got.blockHash = common.HexToHash("0x43") }, want: "hash changed"},
		{name: "nil amount", edit: func(got *withdrawalEvidence) { got.amount = nil }, want: "nil amount"},
		{name: "zero amount", edit: func(got *withdrawalEvidence) { got.amount = new(big.Int) }, want: "amount changed"},
		{name: "changed amount", edit: func(got *withdrawalEvidence) { got.amount = new(big.Int).Add(got.amount, big.NewInt(1)) }, want: "amount changed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := withdrawalEvidence{blockNumber: confirmed.blockNumber, blockHash: confirmed.blockHash, amount: new(big.Int).Set(confirmed.amount)}
			test.edit(&changed)
			if err := validateWithdrawalEvidence(original, changed); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

type testWithdrawalAPI struct {
	blockNumber  rpc.BlockNumber
	block        json.RawMessage
	balanceErr   error
	balances     map[rpc.BlockNumber]*big.Int
	balanceCalls atomic.Int64
}

func (api *testWithdrawalAPI) GetBlockByNumber(_ context.Context, number rpc.BlockNumber, fullTransactions bool) (json.RawMessage, error) {
	if number != api.blockNumber {
		return nil, fmt.Errorf("requested block %d, want %d", number, api.blockNumber)
	}
	if !fullTransactions {
		return nil, errors.New("full transactions flag is false")
	}
	return api.block, nil
}

func (api *testWithdrawalAPI) GetBalance(_ context.Context, _ common.Address, block rpc.BlockNumberOrHash) (*hexutil.Big, error) {
	api.balanceCalls.Add(1)
	if api.balanceErr != nil {
		return nil, api.balanceErr
	}
	number, ok := block.Number()
	if !ok {
		return nil, errors.New("test balance request did not use a block number")
	}
	balance, ok := api.balances[number]
	if !ok {
		return nil, fmt.Errorf("test balance at block %d is not configured", number)
	}
	return (*hexutil.Big)(new(big.Int).Set(balance)), nil
}

func newTestWithdrawalClient(t *testing.T, api *testWithdrawalAPI) *qrlclient.Client {
	t.Helper()
	server := rpc.NewServer()
	if err := server.RegisterName("qrl", api); err != nil {
		t.Fatal(err)
	}
	client := qrlclient.NewClient(rpc.DialInProc(server))
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	return client
}

func testWithdrawalRPCBlock(t *testing.T, number uint64, parentHash common.Hash, withdrawals types.Withdrawals) json.RawMessage {
	t.Helper()
	withdrawalsRoot := types.DeriveSha(withdrawals, trie.NewStackTrie(nil))
	header := &types.Header{
		ParentHash:      parentHash,
		Root:            common.HexToHash("0x1234"),
		TxHash:          types.EmptyTxsHash,
		ReceiptHash:     types.EmptyReceiptsHash,
		Number:          new(big.Int).SetUint64(number),
		GasLimit:        30_000_000,
		Time:            number,
		Extra:           []byte{},
		WithdrawalsHash: &withdrawalsRoot,
	}
	rawHeader, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	var response map[string]any
	if err := json.Unmarshal(rawHeader, &response); err != nil {
		t.Fatal(err)
	}
	response["transactions"] = []any{}
	response["withdrawals"] = withdrawals
	rawBlock, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	return rawBlock
}

func TestFinalizedWithdrawalRevalidationDoesNotRequireArchiveState(t *testing.T) {
	recipient, err := common.NewAddressFromString(expectedWithdrawalAddress)
	if err != nil {
		t.Fatal(err)
	}
	withdrawals := types.Withdrawals{
		&types.Withdrawal{Index: 10, Validator: 20, Address: recipient, Amount: 2},
	}
	rawBlock := testWithdrawalRPCBlock(t, 42, common.HexToHash("0x41"), withdrawals)
	apis := [2]*testWithdrawalAPI{
		{blockNumber: 42, block: rawBlock, balanceErr: errors.New("missing trie node: historical state is not available")},
		{blockNumber: 42, block: rawBlock, balanceErr: errors.New("missing trie node: historical state is not available")},
	}
	check := &systemCheck{
		cfg: config{withdrawalRecipient: recipient},
		clients: [2]*qrlclient.Client{
			newTestWithdrawalClient(t, apis[0]),
			newTestWithdrawalClient(t, apis[1]),
		},
	}

	evidence, err := check.withdrawalBlockEvidenceAt(t.Context(), 42)
	if err != nil {
		t.Fatalf("canonical block-only evidence rejected without archive state: %v", err)
	}
	if want := big.NewInt(2 * params.Shor); evidence.amount.Cmp(want) != 0 {
		t.Fatalf("withdrawal evidence amount %s, want %s", evidence.amount, want)
	}
	for i, api := range apis {
		if calls := api.balanceCalls.Load(); calls != 0 {
			t.Fatalf("EL%d canonical revalidation made %d historical balance calls, want zero", i+1, calls)
		}
	}

	if _, err := check.withdrawalEvidenceAt(t.Context(), 42); err == nil || !strings.Contains(err.Error(), "missing trie node") {
		t.Fatalf("fresh withdrawal evidence error = %v, want historical-state failure", err)
	}
	if calls := apis[0].balanceCalls.Load(); calls != 1 {
		t.Fatalf("EL1 fresh balance verification made %d calls, want one failing before-block call", calls)
	}
	if calls := apis[1].balanceCalls.Load(); calls != 0 {
		t.Fatalf("EL2 balance verification made %d calls after EL1 failed, want zero", calls)
	}
}

func TestFreshWithdrawalBalanceDeltaRequiresBothExecutionNodes(t *testing.T) {
	recipient, err := common.NewAddressFromString(expectedWithdrawalAddress)
	if err != nil {
		t.Fatal(err)
	}
	withdrawals := types.Withdrawals{
		&types.Withdrawal{Index: 10, Validator: 20, Address: recipient, Amount: 2},
	}
	rawBlock := testWithdrawalRPCBlock(t, 42, common.HexToHash("0x41"), withdrawals)
	credit := big.NewInt(2 * params.Shor)
	before := big.NewInt(10 * params.Shor)
	after := new(big.Int).Add(new(big.Int).Set(before), credit)
	apis := [2]*testWithdrawalAPI{
		{blockNumber: 42, block: rawBlock, balances: map[rpc.BlockNumber]*big.Int{41: before, 42: after}},
		{blockNumber: 42, block: rawBlock, balances: map[rpc.BlockNumber]*big.Int{41: before, 42: after}},
	}
	check := &systemCheck{
		cfg: config{withdrawalRecipient: recipient},
		clients: [2]*qrlclient.Client{
			newTestWithdrawalClient(t, apis[0]),
			newTestWithdrawalClient(t, apis[1]),
		},
	}

	evidence, err := check.withdrawalEvidenceAt(t.Context(), 42)
	if err != nil {
		t.Fatalf("fresh exact balance credit rejected: %v", err)
	}
	for i, api := range apis {
		if calls := api.balanceCalls.Load(); calls != 2 {
			t.Fatalf("EL%d fresh balance verification made %d calls, want before and after", i+1, calls)
		}
	}

	apis[1].balances[42] = new(big.Int).Sub(new(big.Int).Set(after), big.NewInt(1))
	for _, api := range apis {
		api.balanceCalls.Store(0)
	}
	if err := check.verifyWithdrawalBalanceDeltaAt(t.Context(), evidence); err == nil || !strings.Contains(err.Error(), "EL2 withdrawal-recipient balance delta") {
		t.Fatalf("wrong EL2 withdrawal credit error = %v, want exact-delta failure", err)
	}
	for i, api := range apis {
		if calls := api.balanceCalls.Load(); calls != 2 {
			t.Fatalf("EL%d mismatch verification made %d calls, want before and after", i+1, calls)
		}
	}
}

func TestExecutionFinalityEvidence(t *testing.T) {
	before := executionFinalityStatus{
		safeNumber:      12,
		safeHash:        common.HexToHash("0x12"),
		finalizedNumber: 10,
		finalizedHash:   common.HexToHash("0x10"),
	}
	after := executionFinalityStatus{
		safeNumber:      14,
		safeHash:        common.HexToHash("0x14"),
		finalizedNumber: 13,
		finalizedHash:   common.HexToHash("0x13"),
	}
	if err := validateExecutionFinalityAdvance(before, after); err != nil {
		t.Fatalf("valid finalized-head advance rejected: %v", err)
	}
	if err := validateExecutionFinalityAdvance(before, before); err == nil || !strings.Contains(err.Error(), "has not advanced") {
		t.Fatalf("got %v, want stale-finality error", err)
	}
	payloads := [2]finalizedExecutionPayload{
		{blockNumber: after.finalizedNumber, blockHash: after.finalizedHash},
		{blockNumber: after.finalizedNumber, blockHash: after.finalizedHash},
	}
	if err := validateFinalizedExecutionPayloads(after, payloads); err != nil {
		t.Fatalf("matching CL/EL finality evidence rejected: %v", err)
	}
	payloads[1].blockHash = common.HexToHash("0x99")
	if err := validateFinalizedExecutionPayloads(after, payloads); err == nil || !strings.Contains(err.Error(), "payloads differ") {
		t.Fatalf("got %v, want CL finality mismatch", err)
	}
}

func TestValidateManagedAccessList(t *testing.T) {
	var contract common.Address
	contract[0] = 0x80
	contract[common.AddressLength/2-1] = 0x11
	contract[common.AddressLength/2] = 0x22
	contract[common.AddressLength-1] = 0x42
	slot := common.HexToHash("0x1234")
	valid := func() *types.AccessList {
		list := types.AccessList{{
			Address:     contract,
			StorageKeys: []common.Hash{slot},
		}}
		return &list
	}

	for _, test := range []struct {
		name string
		list func() *types.AccessList
		want string
	}{
		{name: "exact full-width tuple", list: valid},
		{name: "nil", list: func() *types.AccessList { return nil }, want: "is nil"},
		{name: "empty", list: func() *types.AccessList { list := types.AccessList{}; return &list }, want: "0 entries"},
		{
			name: "upper address half differs",
			list: func() *types.AccessList {
				list := valid()
				(*list)[0].Address[0] ^= 0x01
				return list
			},
			want: "full VM64 contract address",
		},
		{
			name: "storage key differs",
			list: func() *types.AccessList {
				list := valid()
				(*list)[0].StorageKeys[0][common.HashLength-1] ^= 0x01
				return list
			},
			want: "storage key",
		},
		{
			name: "extra storage key",
			list: func() *types.AccessList {
				list := valid()
				(*list)[0].StorageKeys = append((*list)[0].StorageKeys, common.Hash{})
				return list
			},
			want: "2 storage keys",
		},
		{
			name: "extra tuple",
			list: func() *types.AccessList {
				list := valid()
				*list = append(*list, types.AccessTuple{Address: contract})
				return list
			},
			want: "2 entries",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateManagedAccessList(test.list(), contract, slot)
			if test.want == "" {
				if err != nil {
					t.Fatalf("exact access list rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestCompareExecutionHeaders(t *testing.T) {
	base := &types.Header{
		Number:      big.NewInt(42),
		Root:        common.HexToHash("0x01"),
		ReceiptHash: common.HexToHash("0x02"),
		Extra:       []byte("same"),
	}
	if err := compareExecutionHeaders(base, types.CopyHeader(base)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		edit func(*types.Header)
		want string
	}{
		{name: "number", edit: func(header *types.Header) { header.Number = big.NewInt(43) }, want: "numbers differ"},
		{name: "state root", edit: func(header *types.Header) { header.Root = common.HexToHash("0x03") }, want: "state roots differ"},
		{name: "receipt root", edit: func(header *types.Header) { header.ReceiptHash = common.HexToHash("0x04") }, want: "receipt roots differ"},
		{name: "hash", edit: func(header *types.Header) { header.Extra = []byte("different") }, want: "hashes differ"},
	} {
		t.Run(test.name, func(t *testing.T) {
			other := types.CopyHeader(base)
			test.edit(other)
			err := compareExecutionHeaders(base, other)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

type testRPCError struct {
	code    int
	message string
}

func (e testRPCError) Error() string  { return e.message }
func (e testRPCError) ErrorCode() int { return e.code }

type testExecutionHealthAPI struct {
	chainIDCalls     atomic.Int64
	blockNumberCalls atomic.Int64
	accountCalls     atomic.Int64
}

func (api *testExecutionHealthAPI) ChainId() *hexutil.Big {
	api.chainIDCalls.Add(1)
	return (*hexutil.Big)(big.NewInt(32382))
}

func (api *testExecutionHealthAPI) BlockNumber() hexutil.Uint64 {
	api.blockNumberCalls.Add(1)
	return 42
}

func (api *testExecutionHealthAPI) Accounts() ([]common.Address, error) {
	api.accountCalls.Add(1)
	return nil, errors.New("external signer is unavailable")
}

type testExecutionHealthNetAPI struct {
	versionCalls atomic.Int64
}

func (api *testExecutionHealthNetAPI) Version() string {
	api.versionCalls.Add(1)
	return "32382"
}

func TestExecutionRPCHealthIsIndependentOfManagedAccounts(t *testing.T) {
	qrlAPI := new(testExecutionHealthAPI)
	netAPI := new(testExecutionHealthNetAPI)
	server := rpc.NewServer()
	if err := server.RegisterName("qrl", qrlAPI); err != nil {
		t.Fatal(err)
	}
	if err := server.RegisterName("net", netAPI); err != nil {
		t.Fatal(err)
	}
	client := qrlclient.NewClient(rpc.DialInProc(server))
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	check := &systemCheck{clients: [2]*qrlclient.Client{client, nil}}

	if err := check.verifyExecutionRPCIndependentOfSigner(t.Context(), 0); err != nil {
		t.Fatalf("signer-independent execution health failed: %v", err)
	}
	if calls := qrlAPI.accountCalls.Load(); calls != 0 {
		t.Fatalf("signer-independent execution health made %d qrl_accounts calls, want 0", calls)
	}
	if calls := qrlAPI.chainIDCalls.Load(); calls != 1 {
		t.Fatalf("signer-independent execution health made %d qrl_chainId calls, want 1", calls)
	}
	if calls := netAPI.versionCalls.Load(); calls != 1 {
		t.Fatalf("signer-independent execution health made %d net_version calls, want 1", calls)
	}
	if calls := qrlAPI.blockNumberCalls.Load(); calls != 1 {
		t.Fatalf("signer-independent execution health made %d qrl_blockNumber calls, want 1", calls)
	}
	if err := check.verifyManagedAccounts(t.Context()); err == nil || !strings.Contains(err.Error(), "external signer is unavailable") {
		t.Fatalf("managed-account check got %v, want external-signer error", err)
	}
	if calls := qrlAPI.accountCalls.Load(); calls != 1 {
		t.Fatalf("managed-account check made %d qrl_accounts calls, want 1", calls)
	}
}

func TestIsSignerUnavailableError(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "clef connection refused", err: testRPCError{code: -32000, message: "Post http://signer-clef:8550: dial tcp: connection refused"}, want: true},
		{name: "external signer timeout", err: testRPCError{code: -32002, message: "external signer request timed out"}, want: true},
		{name: "client deadline", err: fmt.Errorf("signing call: %w", context.DeadlineExceeded), want: true},
		{name: "invalid params", err: testRPCError{code: -32602, message: "clef invalid params"}, want: false},
		{name: "transaction error", err: testRPCError{code: -32000, message: "insufficient funds"}, want: false},
		{name: "plain marker", err: errors.New("clef is unavailable"), want: false},
		{name: "nil", err: nil, want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := isSignerUnavailableError(test.err); got != test.want {
				t.Fatalf("got %t, want %t for %v", got, test.want, test.err)
			}
		})
	}
}
