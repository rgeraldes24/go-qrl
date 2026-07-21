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

// Package clef runs the standalone VM64 Clef scenario and verifies every
// response cryptographically. Secret material is kept in a private temporary
// directory and is never copied into the suite artifacts.
package clef

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
)

const (
	Name               = "clef_api"
	defaultHost        = "127.0.0.1"
	defaultPort        = 18550
	defaultChainID     = int64(1337)
	defaultReadyWait   = 30 * time.Second
	defaultPollPeriod  = time.Second
	defaultRequestWait = 15 * time.Second
	maximumRPCBody     = int64(8 << 20)

	rulesSource = `function ApproveListing(req) { return 'Approve'; }
function ApproveSignData(req) { return 'Approve'; }
function ApproveTx(req) { return 'Approve'; }
`
)

var passAssertions = []string{
	"PASS: account_version and account_list returned the exact imported VM64 account",
	"PASS: account_signData returned an exact-width, cryptographically valid ML-DSA-87 signature",
	"PASS: account_signTypedData signed the expected QRL typed-data digest with ML-DSA-87",
	"PASS: account_signTransaction returned a consistent signed body with the recovered 64-byte sender and recipient",
}

// PassAssertions returns the compatibility pass markers emitted after the
// complete scenario has been verified.
func PassAssertions() []string {
	return append([]string(nil), passAssertions...)
}

// Config describes one standalone Clef scenario. HTTPURL is primarily useful
// for deterministic API tests; normal runs derive it from Host and Port.
type Config struct {
	ClefPath     string
	Seed         string
	ArtifactDir  string
	Host         string
	Port         int
	ChainID      int64
	ReadyTimeout time.Duration
	PollInterval time.Duration
	HTTPClient   *http.Client
	HTTPURL      string

	runCommand      func(context.Context, process.Command) (process.Result, error)
	startCommand    func(context.Context, process.ManagedCommand) (managedProcess, error)
	secretGenerator func() (string, error)
	tempRoot        string
}

// Artifacts names every durable output produced by the suite. No path in this
// structure points at Clef's temporary config, keystore, passwords, or seed.
type Artifacts struct {
	SetupLog        string
	ClefLog         string
	VersionResponse string
	ListResponse    string
	DataRequest     string
	DataResponse    string
	TypedRequest    string
	TypedResponse   string
	TxRequest       string
	TxResponse      string
}

// Result is the structured result consumed by the E2E harness.
type Result struct {
	Name       string
	Account    string
	Version    string
	Assertions []string
	Artifacts  Artifacts
}

type managedProcess interface {
	Done() <-chan struct{}
	Wait(context.Context) error
	Stop(context.Context) error
}

type scenario struct {
	config          Config
	workspace       string
	artifacts       Artifacts
	setupLog        *os.File
	masterPassword  string
	accountPassword string
	secrets         []string
	client          *http.Client
	endpoint        string
}

// Run initializes a private Clef instance, imports the test account, starts a
// managed HTTP signer, exercises the complete standalone API scenario, and
// verifies all returned ML-DSA and VM64 transaction material.
func Run(ctx context.Context, config Config) (result Result, returnError error) {
	if ctx == nil {
		return Result{}, errors.New("clef suite context is nil")
	}
	normalized, err := normalizeConfig(config)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(normalized.ArtifactDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("create Clef artifact directory: %w", err)
	}
	if err := os.Chmod(normalized.ArtifactDir, 0o700); err != nil {
		return Result{}, fmt.Errorf("restrict Clef artifact directory: %w", err)
	}
	workspace, err := os.MkdirTemp(normalized.tempRoot, "vm64-clef-")
	if err != nil {
		return Result{}, fmt.Errorf("create private Clef workspace: %w", err)
	}
	if err := os.Chmod(workspace, 0o700); err != nil {
		_ = os.RemoveAll(workspace)
		return Result{}, fmt.Errorf("restrict private Clef workspace: %w", err)
	}
	defer func() {
		if err := os.RemoveAll(workspace); err != nil {
			returnError = errors.Join(returnError, fmt.Errorf("remove private Clef workspace: %w", err))
		}
	}()

	artifacts := artifactPaths(normalized.ArtifactDir)
	setupLog, err := os.OpenFile(artifacts.SetupLog, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return Result{}, fmt.Errorf("open Clef setup log: %w", err)
	}
	defer func() {
		if err := setupLog.Close(); err != nil {
			returnError = errors.Join(returnError, fmt.Errorf("close Clef setup log: %w", err))
		}
	}()

	masterPassword, err := normalized.secretGenerator()
	if err != nil {
		return Result{}, fmt.Errorf("generate Clef master password: %w", err)
	}
	accountPassword, err := normalized.secretGenerator()
	if err != nil {
		return Result{}, fmt.Errorf("generate Clef account password: %w", err)
	}
	suite := &scenario{
		config: normalized, workspace: workspace, artifacts: artifacts, setupLog: setupLog,
		masterPassword: masterPassword, accountPassword: accountPassword,
		secrets: []string{normalized.Seed, masterPassword, accountPassword},
		client:  normalized.HTTPClient, endpoint: normalized.HTTPURL,
	}
	return suite.run(ctx)
}

func normalizeConfig(config Config) (Config, error) {
	if strings.TrimSpace(config.ClefPath) == "" {
		return Config{}, errors.New("Clef executable path is required")
	}
	if strings.TrimSpace(config.Seed) == "" {
		return Config{}, errors.New("Clef account seed is required")
	}
	if strings.TrimSpace(config.ArtifactDir) == "" {
		return Config{}, errors.New("Clef artifact directory is required")
	}
	if config.Host == "" {
		config.Host = defaultHost
	}
	if !isLoopbackHost(config.Host) {
		return Config{}, fmt.Errorf("Clef HTTP host %q is not loopback", config.Host)
	}
	if config.Port == 0 {
		config.Port = defaultPort
	}
	if config.Port < 1 || config.Port > 65535 {
		return Config{}, fmt.Errorf("Clef HTTP port %d is outside 1..65535", config.Port)
	}
	if config.ChainID == 0 {
		config.ChainID = defaultChainID
	}
	if config.ChainID != expectedChainID {
		return Config{}, fmt.Errorf("Clef chain ID %d does not match scenario chain ID %d", config.ChainID, expectedChainID)
	}
	if config.ReadyTimeout == 0 {
		config.ReadyTimeout = defaultReadyWait
	}
	if config.ReadyTimeout < 0 {
		return Config{}, errors.New("Clef readiness timeout cannot be negative")
	}
	if config.PollInterval == 0 {
		config.PollInterval = defaultPollPeriod
	}
	if config.PollInterval < 0 {
		return Config{}, errors.New("Clef poll interval cannot be negative")
	}
	if config.HTTPURL == "" {
		config.HTTPURL = "http://" + net.JoinHostPort(config.Host, strconv.Itoa(config.Port)) + "/"
	}
	parsedEndpoint, err := url.Parse(config.HTTPURL)
	if err != nil || parsedEndpoint.Scheme == "" || parsedEndpoint.Host == "" {
		return Config{}, fmt.Errorf("invalid Clef HTTP URL %q", config.HTTPURL)
	}
	if parsedEndpoint.Scheme != "http" || parsedEndpoint.User != nil || parsedEndpoint.RawQuery != "" || parsedEndpoint.Fragment != "" {
		return Config{}, errors.New("Clef HTTP URL must be a plain HTTP endpoint without credentials, query, or fragment")
	}
	if !isLoopbackHost(parsedEndpoint.Hostname()) {
		return Config{}, fmt.Errorf("Clef HTTP URL host %q is not loopback", parsedEndpoint.Hostname())
	}
	if config.HTTPClient == nil {
		config.HTTPClient = &http.Client{Timeout: defaultRequestWait}
	}
	if config.runCommand == nil {
		config.runCommand = process.Run
	}
	if config.startCommand == nil {
		config.startCommand = func(ctx context.Context, command process.ManagedCommand) (managedProcess, error) {
			return process.Start(ctx, command)
		}
	}
	if config.secretGenerator == nil {
		config.secretGenerator = randomSecret
	}
	return config, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func randomSecret() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func artifactPaths(directory string) Artifacts {
	return Artifacts{
		SetupLog: filepath.Join(directory, "setup.log"), ClefLog: filepath.Join(directory, "clef.log"),
		VersionResponse: filepath.Join(directory, "version-response.json"),
		ListResponse:    filepath.Join(directory, "list-response.json"),
		DataRequest:     filepath.Join(directory, "data-request.json"), DataResponse: filepath.Join(directory, "data-response.json"),
		TypedRequest: filepath.Join(directory, "typed-request.json"), TypedResponse: filepath.Join(directory, "typed-response.json"),
		TxRequest: filepath.Join(directory, "tx-request.json"), TxResponse: filepath.Join(directory, "tx-response.json"),
	}
}

func (suite *scenario) run(ctx context.Context) (Result, error) {
	account, err := suite.initialize(ctx)
	if err != nil {
		return Result{}, err
	}
	managed, err := suite.start(ctx)
	if err != nil {
		return Result{}, err
	}

	result, runError := suite.exercise(ctx, managed, account)
	stopContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stopError := managed.Stop(stopContext)
	if runError != nil {
		if stopError != nil {
			return Result{}, errors.Join(runError, fmt.Errorf("stop managed Clef process: %w", stopError))
		}
		return Result{}, runError
	}
	if stopError != nil {
		return Result{}, fmt.Errorf("stop managed Clef process: %w", stopError)
	}
	return result, nil
}

func (suite *scenario) initialize(ctx context.Context) (string, error) {
	configDir := filepath.Join(suite.workspace, "config")
	keyStore := filepath.Join(suite.workspace, "keystore")
	seedPath := filepath.Join(suite.workspace, "seed.hex")
	passwordPath := filepath.Join(suite.workspace, "account-password.txt")
	rulesPath := filepath.Join(suite.workspace, "rules.js")
	if err := writePrivateFile(seedPath, []byte(suite.config.Seed+"\n")); err != nil {
		return "", fmt.Errorf("write private Clef seed file: %w", err)
	}
	if err := writePrivateFile(passwordPath, []byte(suite.accountPassword+"\n")); err != nil {
		return "", fmt.Errorf("write private Clef account-password file: %w", err)
	}
	if err := writePrivateFile(rulesPath, []byte(rulesSource)); err != nil {
		return "", fmt.Errorf("write Clef rules file: %w", err)
	}

	baseArgs := []string{"--suppress-bootwarn", "--lightkdf", "--configdir", configDir, "--keystore", keyStore}
	if _, err := suite.command(ctx, "init", append(append([]string(nil), baseArgs...), "init"),
		strings.NewReader(suite.masterPassword+"\n"+suite.masterPassword+"\n")); err != nil {
		return "", err
	}
	importResult, err := suite.command(ctx, "importraw",
		append(append([]string(nil), baseArgs...), "importraw", "--password", passwordPath, seedPath), nil)
	if err != nil {
		return "", err
	}
	account, err := parseImportedAccount(importResult.Stdout)
	if err != nil {
		return "", err
	}
	rulesHash := sha256.Sum256([]byte(rulesSource))
	if _, err := suite.command(ctx, "attest",
		append(append([]string(nil), baseArgs...), "attest", hex.EncodeToString(rulesHash[:])),
		strings.NewReader(suite.masterPassword+"\n")); err != nil {
		return "", err
	}
	setPasswordInput := suite.accountPassword + "\n" + suite.accountPassword + "\n" + suite.masterPassword + "\n"
	if _, err := suite.command(ctx, "setpw",
		append(append([]string(nil), baseArgs...), "setpw", account), strings.NewReader(setPasswordInput)); err != nil {
		return "", err
	}
	return account, nil
}

func writePrivateFile(path string, data []byte) error {
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}

func (suite *scenario) command(ctx context.Context, name string, args []string, stdin io.Reader) (process.Result, error) {
	if _, err := fmt.Fprintf(suite.setupLog, "[%s]\n", name); err != nil {
		return process.Result{}, fmt.Errorf("write Clef setup log: %w", err)
	}
	result, err := suite.config.runCommand(ctx, process.Command{
		Path: suite.config.ClefPath, Args: args, Stdin: stdin, Stdout: suite.setupLog, Stderr: suite.setupLog,
		Name: "clef " + name, Secrets: suite.secrets,
	})
	if err != nil {
		return result, fmt.Errorf("Clef %s: %w", name, err)
	}
	return result, nil
}

func parseImportedAccount(output []byte) (string, error) {
	for _, line := range strings.Split(string(output), "\n") {
		if strings.HasPrefix(line, "  Address ") {
			account := strings.TrimSpace(strings.TrimPrefix(line, "  Address "))
			if account != "" {
				return account, nil
			}
		}
	}
	return "", errors.New("could not parse imported Clef account; inspect setup.log")
}

func (suite *scenario) start(ctx context.Context) (managedProcess, error) {
	configDir := filepath.Join(suite.workspace, "config")
	keyStore := filepath.Join(suite.workspace, "keystore")
	rulesPath := filepath.Join(suite.workspace, "rules.js")
	args := []string{
		"--suppress-bootwarn", "--lightkdf", "--advanced", "--configdir", configDir,
		"--keystore", keyStore, "--chainid", strconv.FormatInt(suite.config.ChainID, 10), "--rules", rulesPath,
		"--http", "--http.addr", suite.config.Host, "--http.port", strconv.Itoa(suite.config.Port),
		"--http.vhosts", "*", "--ipcdisable", "--auditlog", "",
	}
	managed, err := suite.config.startCommand(ctx, process.ManagedCommand{
		Command: process.Command{
			Path: suite.config.ClefPath, Args: args, Stdin: strings.NewReader(suite.masterPassword + "\n"),
			Name: "clef signer", Secrets: suite.secrets,
		},
		LogPath: suite.artifacts.ClefLog,
	})
	if err != nil {
		return nil, fmt.Errorf("start managed Clef process: %w", err)
	}
	return managed, nil
}

func (suite *scenario) exercise(ctx context.Context, managed managedProcess, account string) (Result, error) {
	versionResponse, version, err := suite.waitReady(ctx, managed)
	if err != nil {
		return Result{}, err
	}
	if err := writePrivateFile(suite.artifacts.VersionResponse, versionResponse); err != nil {
		return Result{}, fmt.Errorf("write account_version response: %w", err)
	}

	listRequest := rpcRequestBytes("account_list", []any{}, 2)
	listResponse, err := suite.call(ctx, listRequest)
	if err != nil {
		return Result{}, fmt.Errorf("account_list RPC: %w", err)
	}
	if err := writePrivateFile(suite.artifacts.ListResponse, listResponse); err != nil {
		return Result{}, fmt.Errorf("write account_list response: %w", err)
	}

	dataRequest := rpcRequestBytes("account_signData", []any{
		"text/plain", account, "0x436c656620564d3634207369676e44617461",
	}, 3)
	dataResponse, err := suite.persistedCall(ctx, suite.artifacts.DataRequest, suite.artifacts.DataResponse, dataRequest)
	if err != nil {
		return Result{}, fmt.Errorf("account_signData RPC: %w", err)
	}
	typedRequest := typedDataRequest(account)
	typedResponse, err := suite.persistedCall(ctx, suite.artifacts.TypedRequest, suite.artifacts.TypedResponse, typedRequest)
	if err != nil {
		return Result{}, fmt.Errorf("account_signTypedData RPC: %w", err)
	}
	txRequest := transactionRequestBytes(account)
	txResponse, err := suite.persistedCall(ctx, suite.artifacts.TxRequest, suite.artifacts.TxResponse, txRequest)
	if err != nil {
		return Result{}, fmt.Errorf("account_signTransaction RPC: %w", err)
	}

	input := ScenarioInput{
		Seed: suite.config.Seed, Account: account,
		VersionResponse: versionResponse, ListResponse: listResponse,
		DataRequest: dataRequest, DataResponse: dataResponse,
		TypedRequest: typedRequest, TypedResponse: typedResponse,
		TxRequest: txRequest, TxResponse: txResponse,
	}
	if err := VerifyScenario(input); err != nil {
		return Result{}, fmt.Errorf("verify standalone Clef VM64 scenario: %w", err)
	}
	return Result{
		Name: Name, Account: account, Version: version,
		Assertions: PassAssertions(), Artifacts: suite.artifacts,
	}, nil
}

func (suite *scenario) waitReady(ctx context.Context, managed managedProcess) ([]byte, string, error) {
	readyContext, cancel := context.WithTimeout(ctx, suite.config.ReadyTimeout)
	defer cancel()
	request := rpcRequestBytes("account_version", []any{}, 1)
	var lastError error
	for {
		response, err := suite.call(readyContext, request)
		if err == nil {
			version, decodeError := decodeVersion(response)
			if decodeError == nil {
				return response, version, nil
			}
			err = decodeError
		}
		lastError = err
		select {
		case <-managed.Done():
			waitError := managed.Wait(context.Background())
			if waitError != nil {
				return nil, "", fmt.Errorf("Clef exited before account_version responded (log %s): %w", suite.artifacts.ClefLog, waitError)
			}
			return nil, "", fmt.Errorf("Clef exited before account_version responded; inspect %s", suite.artifacts.ClefLog)
		case <-readyContext.Done():
			if lastError != nil {
				return nil, "", fmt.Errorf("account_version readiness failed (log %s): %w", suite.artifacts.ClefLog, lastError)
			}
			return nil, "", fmt.Errorf("account_version readiness failed (log %s): %w", suite.artifacts.ClefLog, context.Cause(readyContext))
		case <-time.After(suite.config.PollInterval):
		}
	}
}

func decodeVersion(responseJSON []byte) (string, error) {
	raw, err := decodeRPCResponse(responseJSON, 1)
	if err != nil {
		return "", err
	}
	var version string
	if err := json.Unmarshal(raw, &version); err != nil {
		return "", err
	}
	if version == "" {
		return "", errors.New("account_version returned an empty version")
	}
	return version, nil
}

func (suite *scenario) persistedCall(ctx context.Context, requestPath, responsePath string, request []byte) ([]byte, error) {
	if err := writePrivateFile(requestPath, request); err != nil {
		return nil, fmt.Errorf("write RPC request artifact: %w", err)
	}
	response, err := suite.call(ctx, request)
	if err != nil {
		return nil, err
	}
	if err := writePrivateFile(responsePath, response); err != nil {
		return nil, fmt.Errorf("write RPC response artifact: %w", err)
	}
	return response, nil
}

func (suite *scenario) call(ctx context.Context, body []byte) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, suite.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build Clef HTTP request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := suite.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	limited := io.LimitReader(response.Body, maximumRPCBody+1)
	responseBody, err := io.ReadAll(limited)
	if err != nil {
		return nil, fmt.Errorf("read Clef HTTP response: %w", err)
	}
	if int64(len(responseBody)) > maximumRPCBody {
		return nil, fmt.Errorf("Clef HTTP response exceeds %d bytes", maximumRPCBody)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("Clef HTTP status %s", response.Status)
	}
	return responseBody, nil
}

func rpcRequestBytes(method string, params []any, id int) []byte {
	data, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "method": method, "params": params, "id": id})
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

func typedDataRequest(account string) []byte {
	typed := map[string]any{
		"types": map[string]any{
			"QRLTypedDataDomain": []map[string]string{{"name": "name", "type": "string"}, {"name": "version", "type": "string"}, {"name": "chainId", "type": "uint256"}, {"name": "verifyingContract", "type": "address"}},
			"Message":            []map[string]string{{"name": "sender", "type": "address"}, {"name": "contents", "type": "string"}, {"name": "value", "type": "uint256"}},
		},
		"primaryType": "Message",
		"domain":      map[string]any{"name": expectedTypedName, "version": expectedTypedVersion, "chainId": "1337", "verifyingContract": account},
		"message":     map[string]any{"sender": account, "contents": expectedTypedContents, "value": expectedTypedValue},
	}
	return rpcRequestBytes("account_signTypedData", []any{account, typed}, 4)
}

func transactionRequestBytes(account string) []byte {
	transaction := map[string]any{
		"from": account, "to": expectedRecipient, "gas": "0x9c40", "maxFeePerGas": "0x3b9aca00",
		"maxPriorityFeePerGas": "0x7", "value": "0x2a", "nonce": "0x9", "chainId": "0x539",
		"input": expectedTxInputHex, "accessList": []any{},
	}
	return rpcRequestBytes("account_signTransaction", []any{transaction}, 5)
}
