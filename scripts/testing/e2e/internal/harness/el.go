// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
	clefSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/clef"
	consoleSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/console"
	goABISuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/goabi"
)

const prefundedDeployerSeed = "010000f29f58aff0b00de2844f7e20bd9eeaacc379150043beeb328335817512b29fbb7184da84a092f842b2a06d72a24a5d28"

type consoleTransactionClient interface {
	TransactionByHash(context.Context, common.Hash) (*types.Transaction, bool, error)
	SendTransaction(context.Context, *types.Transaction) error
}

func (runtime *Runtime) elStage(index int) func(context.Context, *lifecycle.RunEnvironment) error {
	return func(ctx context.Context, environment *lifecycle.RunEnvironment) (stageErr error) {
		stageName := fmt.Sprintf("el%d", index+1)
		defer func() {
			stageErr = errors.Join(stageErr, runtime.writeStageSuiteMarker(stageName, stageErr))
		}()
		if runtime.Topology == nil || index < 0 || index >= len(runtime.Topology.Execution) {
			return errors.New("execution suite stage requires topology")
		}
		node := runtime.Topology.Execution[index]
		attempt := runtime.currentAttempt(stageName)
		attemptDir := filepath.Join(runtime.Writer.Layout().Logs, fmt.Sprintf("%s-suite-attempt-%d", stageName, attempt))
		if err := os.MkdirAll(attemptDir, 0o700); err != nil {
			return fmt.Errorf("create %s attempt directory: %w", stageName, err)
		}

		bytecode, err := deploymentBytecode(filepath.Join(runtime.Config.RepoRoot, "scripts", "testing", "e2e", "testdata", "contracts", "EventEmitter.bin"))
		if err != nil {
			return err
		}
		parameters, err := runtime.signConsoleDeployment(ctx, stageName, attempt, node.RPC.PublicURL, bytecode)
		if err != nil {
			return err
		}
		freshPrepared, err := consolePreparedTransaction(parameters)
		if err != nil {
			return err
		}
		lookup, err := qrlclient.Dial(node.RPC.PublicURL)
		if err != nil {
			return fmt.Errorf("dial console transaction reconciliation endpoint: %w", err)
		}
		defer lookup.Close()
		consoleSuiteLabel, prepared, submitted, err := selectConsoleTransaction(ctx, stageName, environment.State, freshPrepared, lookup,
			func(label string, transaction lifecycle.PreparedTransaction) error {
				return environment.State.RecordPreparedTransaction(runtime.Store, label, transaction.Hash, transaction.Raw, runtime.Dependencies.Now())
			},
			func(label, hash string) error {
				return environment.State.RecordTransaction(runtime.Store, label, hash, runtime.Dependencies.Now())
			})
		if err != nil {
			return err
		}
		parameters, err = appendPreparedConsoleTransaction(parameters, prepared, strings.TrimPrefix(consoleSuiteLabel, "console/"))
		if err != nil {
			return err
		}
		if submitted != "" {
			parameters, err = appendRecordedConsoleTransaction(parameters, submitted)
			if err != nil {
				return err
			}
		}
		javascriptRoot := filepath.Join(attemptDir, "js-runtime")
		if err := prepareConsoleWorkspace(filepath.Join(runtime.Config.RepoRoot, "scripts", "testing", "e2e", "testdata"), javascriptRoot, parameters); err != nil {
			return err
		}
		defer os.RemoveAll(javascriptRoot)

		recordTransaction := func(label, hash string) error {
			return environment.State.RecordTransaction(runtime.Store, stageName+"/"+label, hash, runtime.Dependencies.Now())
		}
		recordPreparedTransaction := func(label, hash, raw string) error {
			return environment.State.RecordPreparedTransaction(runtime.Store, stageName+"/"+label, hash, raw, runtime.Dependencies.Now())
		}
		consoleResults, consoleErr := consoleSuite.Run(ctx, consoleSuite.Config{
			GQRLPath:               filepath.Join(runtime.Config.RepoRoot, "build", "bin", "gqrl"),
			JSPath:                 javascriptRoot,
			RPCURL:                 node.RPC.PublicURL,
			Suites:                 consoleSuite.Definitions,
			AllowDisruptive:        true,
			OutputDir:              filepath.Join(attemptDir, "console"),
			TransactionLabelPrefix: "console/",
			TransactionRecorder:    consoleSuite.TransactionRecorderFunc(recordTransaction),
			ParametersScript:       "console/params.js",
			Runner:                 consoleSuite.CommandRunner(runtime.Dependencies.Process),
		})
		for _, suite := range consoleResults {
			result := suiteReport(node.Service.Name+"-"+suite.Name, stageName, reportStatus(suite.Status == "passed"), suite.StartedAt, suite.FinishedAt, suite.OutputTruncated)
			if suite.Status != "passed" {
				result.Details = "console suite did not report a passing structured result"
			}
			runtime.recordSuiteResult(result)
		}
		if consoleErr != nil {
			return consoleErr
		}

		graphQLURL := strings.TrimRight(node.RPC.PublicURL, "/") + "/graphql"
		started := runtime.Dependencies.Now()
		goABIError := goABISuite.RunWithOptions(ctx, goABISuite.Config{
			RPCURL: node.RPC.PublicURL, GraphQLURL: graphQLURL, WebSocketURL: node.WS.PublicURL,
			SeedHex: prefundedDeployerSeed, BinHex: bytecode,
		}, goABISuite.Options{
			TransactionRecorder:         goABISuite.TransactionRecorderFunc(recordTransaction),
			PreparedTransactionRecorder: goABISuite.PreparedTransactionRecorderFunc(recordPreparedTransaction),
			RecordedTransactions:        stageTransactions(environment.State.Transactions, stageName+"/", "goabi/"),
			PreparedTransactions:        stagePreparedTransactions(environment.State.PreparedTransactions, stageName+"/", "goabi/"),
		})
		finished := runtime.Dependencies.Now()
		runtime.recordSuite(node.Service.Name+"-go_abi", stageName, started, finished, goABIError)
		if goABIError != nil {
			return goABIError
		}

		// The standalone Clef scenario is node-independent and intentionally runs
		// only once, after EL1 endpoint coverage, matching the legacy lifecycle.
		if index == 0 {
			started = runtime.Dependencies.Now()
			_, clefError := clefSuite.Run(ctx, clefSuite.Config{
				ClefPath: filepath.Join(runtime.Config.RepoRoot, "build", "bin", "clef"),
				Seed:     prefundedDeployerSeed, ArtifactDir: filepath.Join(attemptDir, "clef_api"),
				Port: 18550,
			})
			finished = runtime.Dependencies.Now()
			runtime.recordSuite(node.Service.Name+"-"+clefSuite.Name, stageName, started, finished, clefError)
			if clefError != nil {
				return clefError
			}
		}
		return nil
	}
}

func consolePreparedTransaction(parameters []byte) (lifecycle.PreparedTransaction, error) {
	const prefix = "var PARAMS = "
	trimmed := strings.TrimSpace(string(parameters))
	if !strings.HasPrefix(trimmed, prefix) || !strings.HasSuffix(trimmed, ";") {
		return lifecycle.PreparedTransaction{}, errors.New("txsigner JavaScript parameters have an invalid envelope")
	}
	var values struct {
		Hash string `json:"txHash"`
		Raw  string `json:"rawTransaction"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSuffix(strings.TrimPrefix(trimmed, prefix), ";")), &values); err != nil {
		return lifecycle.PreparedTransaction{}, fmt.Errorf("decode txsigner JavaScript parameters: %w", err)
	}
	if values.Hash == "" || values.Raw == "" {
		return lifecycle.PreparedTransaction{}, errors.New("txsigner JavaScript parameters omit transaction hash or raw bytes")
	}
	return lifecycle.PreparedTransaction{Hash: values.Hash, Raw: values.Raw}, nil
}

func consoleProbeLabel(index int) string {
	if index == 0 {
		return "console/event-deploy"
	}
	return fmt.Sprintf("console/event-deploy/resume-%d", index)
}

func selectConsoleTransaction(
	ctx context.Context,
	stageName string,
	state *lifecycle.Checkpoint,
	fresh lifecycle.PreparedTransaction,
	lookup consoleTransactionClient,
	recordPrepared func(string, lifecycle.PreparedTransaction) error,
	recordRecovered func(string, string) error,
) (suiteLabel string, prepared lifecycle.PreparedTransaction, submitted string, err error) {
	if state == nil || lookup == nil || recordPrepared == nil || recordRecovered == nil {
		return "", lifecycle.PreparedTransaction{}, "", errors.New("console transaction reconciliation is not configured")
	}
	if err := validateConsoleJournal(stageName, state); err != nil {
		return "", lifecycle.PreparedTransaction{}, "", err
	}
	suiteLabel = consoleProbeLabel(0)
	label := stageName + "/" + suiteLabel
	prepared, exists := state.PreparedTransactions[label]
	submitted = state.Transactions[label]
	recovered := state.Transactions[label+"/recovered"]
	if !exists {
		if submitted != "" || recovered != "" {
			return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("console transaction evidence %q has no prepared raw transaction", label)
		}
		if err := recordPrepared(label, fresh); err != nil {
			return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("journal prepared console deployment %s: %w", label, err)
		}
		return suiteLabel, fresh, "", nil
	}
	if submitted != "" {
		if submitted != prepared.Hash {
			return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("recorded console transaction %s differs from prepared hash %s", submitted, prepared.Hash)
		}
		if err := validateConsoleTransactionSemantics(prepared, fresh, false); err != nil {
			return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("recorded console transaction %s: %w", label, err)
		}
		if err := ensureExactConsoleTransactionSubmitted(ctx, label, prepared, lookup); err != nil {
			return "", lifecycle.PreparedTransaction{}, "", err
		}
		return suiteLabel, prepared, submitted, nil
	}
	if recovered != "" {
		if recovered != prepared.Hash {
			return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("recovered console transaction %s differs from prepared hash %s", recovered, prepared.Hash)
		}
		if err := validateConsoleTransactionSemantics(prepared, fresh, false); err != nil {
			return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("recovered console transaction %s: %w", label, err)
		}
		if err := ensureExactConsoleTransactionSubmitted(ctx, label, prepared, lookup); err != nil {
			return "", lifecycle.PreparedTransaction{}, "", err
		}
		// A mined receipt and the deployment's state/log assertions are an
		// idempotent proof for the response-lost transaction. Pass this exact
		// hash to the console instead of creating a nonce+1 deployment.
		return suiteLabel, prepared, recovered, nil
	}
	// Validate the invariant mutation fields before asking the node about the
	// hash. The nonce is checked below only when the transaction is absent,
	// because an accepted transaction can legitimately advance the fresh
	// signer's pending nonce.
	if err := validateConsoleTransactionSemantics(prepared, fresh, false); err != nil {
		return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("prepared console transaction %s: %w", label, err)
	}
	var hash common.Hash
	if err := hash.UnmarshalText([]byte(prepared.Hash)); err != nil || hash == (common.Hash{}) {
		return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("prepared console transaction %q has invalid hash %q", label, prepared.Hash)
	}
	observed, _, lookupErr := lookup.TransactionByHash(ctx, hash)
	if errors.Is(lookupErr, qrl.NotFound) {
		if err := validateConsoleTransactionSemantics(prepared, fresh, true); err != nil {
			return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("prepared console transaction %s: %w", label, err)
		}
		return suiteLabel, prepared, "", nil
	}
	if lookupErr != nil {
		return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("look up prepared console transaction %s: %w", hash, lookupErr)
	}
	if observed == nil || observed.Hash() != hash {
		return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("prepared console transaction lookup returned a different transaction for %s", hash)
	}
	if err := recordRecovered(label+"/recovered", hash.Hex()); err != nil {
		return "", lifecycle.PreparedTransaction{}, "", fmt.Errorf("record recovered console transaction %s: %w", hash, err)
	}
	return suiteLabel, prepared, hash.Hex(), nil
}

func ensureExactConsoleTransactionSubmitted(ctx context.Context, label string, prepared lifecycle.PreparedTransaction, client consoleTransactionClient) error {
	tx, _, err := decodeConsoleTransaction(prepared)
	if err != nil {
		return fmt.Errorf("decode prepared console transaction %s: %w", label, err)
	}
	observed, _, lookupErr := client.TransactionByHash(ctx, tx.Hash())
	if lookupErr != nil && !errors.Is(lookupErr, qrl.NotFound) {
		return fmt.Errorf("look up prepared console transaction %s as %s: %w", tx.Hash(), label, lookupErr)
	}
	if lookupErr == nil && (observed == nil || observed.Hash() != tx.Hash()) {
		return fmt.Errorf("prepared console transaction lookup returned a different transaction for %s", tx.Hash())
	}
	if errors.Is(lookupErr, qrl.NotFound) {
		if err := client.SendTransaction(ctx, tx); err != nil {
			observed, _, verifyErr := client.TransactionByHash(ctx, tx.Hash())
			if verifyErr != nil || observed == nil || observed.Hash() != tx.Hash() {
				return fmt.Errorf("rebroadcast prepared console transaction %s as %s: %w", tx.Hash(), label, err)
			}
		}
	}
	return nil
}

func consoleProbeIndex(label string) (int, bool) {
	if label == "console/event-deploy" {
		return 0, true
	}
	const prefix = "console/event-deploy/resume-"
	if !strings.HasPrefix(label, prefix) {
		return 0, false
	}
	raw := strings.TrimPrefix(label, prefix)
	var index int
	if _, err := fmt.Sscanf(raw, "%d", &index); err != nil || index < 1 || raw != fmt.Sprintf("%d", index) {
		return 0, false
	}
	return index, true
}

func validateConsoleJournal(stageName string, state *lifecycle.Checkpoint) error {
	prefix := stageName + "/console/"
	baseLabel := stageName + "/console/event-deploy"
	for label := range state.PreparedTransactions {
		if !strings.HasPrefix(label, prefix) {
			continue
		}
		index, ok := consoleProbeIndex(strings.TrimPrefix(label, stageName+"/"))
		if !ok {
			return fmt.Errorf("prepared console transaction label %q is unknown", label)
		}
		if index != 0 {
			return fmt.Errorf("prepared console continuation journal %q is forbidden; console recovery must reuse %q", label, baseLabel)
		}
	}
	for label := range state.Transactions {
		if !strings.HasPrefix(label, prefix) {
			continue
		}
		recovered := strings.HasSuffix(label, "/recovered")
		base := strings.TrimSuffix(label, "/recovered")
		index, ok := consoleProbeIndex(strings.TrimPrefix(base, stageName+"/"))
		if !ok {
			return fmt.Errorf("recorded console transaction label %q is unknown", label)
		}
		if index != 0 {
			kind := "recorded"
			if recovered {
				kind = "recovered"
			}
			return fmt.Errorf("%s console continuation journal %q is forbidden; console recovery must reuse %q", kind, label, baseLabel)
		}
	}

	prepared, preparedExists := state.PreparedTransactions[baseLabel]
	validatedHash, validated := state.Transactions[baseLabel]
	recoveredLabel := baseLabel + "/recovered"
	recoveredHash, recovered := state.Transactions[recoveredLabel]
	if !preparedExists {
		if validated {
			return fmt.Errorf("console transaction evidence %q has no prepared sequence", baseLabel)
		}
		if recovered {
			return fmt.Errorf("console transaction evidence %q has no prepared sequence", recoveredLabel)
		}
		return nil
	}
	if validated && recovered {
		return fmt.Errorf("console probe %q is both response-validated and recovered", baseLabel)
	}
	if validated && validatedHash != prepared.Hash {
		return fmt.Errorf("recorded console transaction %q hash %s differs from prepared hash %s", baseLabel, validatedHash, prepared.Hash)
	}
	if recovered && recoveredHash != prepared.Hash {
		return fmt.Errorf("recorded console transaction %q hash %s differs from prepared hash %s", recoveredLabel, recoveredHash, prepared.Hash)
	}
	return nil
}

func decodeConsoleTransaction(value lifecycle.PreparedTransaction) (*types.Transaction, common.Address, error) {
	raw, err := hexutil.Decode(value.Raw)
	if err != nil {
		return nil, common.Address{}, err
	}
	tx := new(types.Transaction)
	if err := tx.UnmarshalBinary(raw); err != nil {
		return nil, common.Address{}, err
	}
	if tx.Hash().Hex() != value.Hash {
		return nil, common.Address{}, fmt.Errorf("raw transaction hash %s differs from %s", tx.Hash(), value.Hash)
	}
	sender, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
	return tx, sender, err
}

func validateConsoleTransactionSemantics(actual, expected lifecycle.PreparedTransaction, requireNonce bool) error {
	actualTx, actualSender, err := decodeConsoleTransaction(actual)
	if err != nil {
		return fmt.Errorf("decode actual transaction: %w", err)
	}
	expectedTx, expectedSender, err := decodeConsoleTransaction(expected)
	if err != nil {
		return fmt.Errorf("decode expected transaction: %w", err)
	}
	if actualSender != expectedSender || actualTx.ChainId().Cmp(expectedTx.ChainId()) != 0 || actualTx.To() != nil || expectedTx.To() != nil || actualTx.Value().Cmp(expectedTx.Value()) != 0 || !bytes.Equal(actualTx.Data(), expectedTx.Data()) {
		return errors.New("transaction changed sender, chain, recipient, value, or deployment bytecode")
	}
	if requireNonce && actualTx.Nonce() != expectedTx.Nonce() {
		return fmt.Errorf("transaction nonce is %d, want %d", actualTx.Nonce(), expectedTx.Nonce())
	}
	return nil
}

func appendPreparedConsoleTransaction(parameters []byte, prepared lifecycle.PreparedTransaction, label string) ([]byte, error) {
	if prepared.Hash == "" || prepared.Raw == "" {
		return nil, errors.New("prepared console transaction is incomplete")
	}
	if label == "" {
		return nil, errors.New("prepared console transaction label is empty")
	}
	result := append([]byte(nil), parameters...)
	result = append(result, []byte(fmt.Sprintf("\nPARAMS.txHash = %q;\nPARAMS.rawTransaction = %q;\nPARAMS.transactionLabel = %q;\n", prepared.Hash, prepared.Raw, label))...)
	return result, nil
}

func stagePreparedTransactions(transactions map[string]lifecycle.PreparedTransaction, stagePrefix, suitePrefix string) map[string]goABISuite.PreparedTransaction {
	result := make(map[string]goABISuite.PreparedTransaction)
	for label, transaction := range transactions {
		if !strings.HasPrefix(label, stagePrefix+suitePrefix) {
			continue
		}
		result[strings.TrimPrefix(label, stagePrefix)] = goABISuite.PreparedTransaction{
			Hash: transaction.Hash,
			Raw:  transaction.Raw,
		}
	}
	return result
}

func appendRecordedConsoleTransaction(parameters []byte, rawHash string) ([]byte, error) {
	var hash common.Hash
	if err := hash.UnmarshalText([]byte(rawHash)); err != nil || hash == (common.Hash{}) || hash.Hex() != strings.ToLower(rawHash) {
		return nil, fmt.Errorf("recorded console transaction has invalid canonical hash %q", rawHash)
	}
	result := append([]byte(nil), parameters...)
	result = append(result, []byte(fmt.Sprintf("\nPARAMS.recordedTransactionHash = %q;\n", hash.Hex()))...)
	return result, nil
}

func stageTransactions(transactions map[string]string, stagePrefix, suitePrefix string) map[string]string {
	result := make(map[string]string)
	for label, hash := range transactions {
		if !strings.HasPrefix(label, stagePrefix+suitePrefix) {
			continue
		}
		result[strings.TrimPrefix(label, stagePrefix)] = hash
	}
	return result
}

func deploymentBytecode(path string) (string, error) {
	payload, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read EventEmitter bytecode: %w", err)
	}
	value := strings.TrimSpace(string(payload))
	value = strings.TrimPrefix(value, "0x")
	if value == "" || len(value)%2 != 0 {
		return "", errors.New("EventEmitter bytecode is empty or has odd length")
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return "", errors.New("EventEmitter bytecode is not lowercase hexadecimal")
		}
	}
	return "0x" + value, nil
}

func (runtime *Runtime) signConsoleDeployment(ctx context.Context, stageName string, attempt int, rpcURL, bytecode string) ([]byte, error) {
	e2eRoot := filepath.Join(runtime.Config.RepoRoot, "scripts", "testing", "e2e")
	result, err := runtime.Dependencies.Process(ctx, process.Command{
		Path: "go", Dir: runtime.Config.RepoRoot, Name: stageName + "-txsigner",
		Args:    []string{"-C", e2eRoot, "run", "./txsigner", "-rpc", rpcURL, "-seed", prefundedDeployerSeed, "-data", bytecode, "-format", "js"},
		Secrets: []string{prefundedDeployerSeed}, Logger: runtime.Dependencies.Logger,
	})
	logName := fmt.Sprintf("%s-txsigner-attempt-%d", stageName, attempt)
	if _, writeErr := runtime.Writer.WriteSuiteLog(logName, result.Stderr); writeErr != nil {
		return nil, errors.Join(err, writeErr)
	}
	if err != nil {
		return nil, err
	}
	if result.StdoutTruncated || !strings.HasPrefix(string(result.Stdout), "var PARAMS = ") {
		return nil, errors.New("txsigner did not emit a complete JavaScript parameter document")
	}
	return result.Stdout, nil
}

func prepareConsoleWorkspace(sourceRoot, destinationRoot string, parameters []byte) error {
	consoleDir := filepath.Join(destinationRoot, "console")
	contractsDir := filepath.Join(destinationRoot, "contracts")
	if err := os.MkdirAll(consoleDir, 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(contractsDir, 0o700); err != nil {
		return err
	}
	for _, definition := range consoleSuite.Definitions {
		name := definition.Name + ".js"
		if err := copyTestAsset(filepath.Join(sourceRoot, "console", name), filepath.Join(consoleDir, name)); err != nil {
			return err
		}
	}
	if err := copyTestAsset(filepath.Join(sourceRoot, "contracts", "emitter.js"), filepath.Join(contractsDir, "emitter.js")); err != nil {
		return err
	}
	return writeExclusiveFile(filepath.Join(consoleDir, "params.js"), parameters, 0o600)
}

func copyTestAsset(source, destination string) error {
	payload, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writeExclusiveFile(destination, payload, 0o600)
}

func writeExclusiveFile(path string, payload []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(payload); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}

func (runtime *Runtime) recordSuite(name, stage string, started, finished time.Time, suiteError error) {
	status := report.StatusPassed
	if suiteError != nil {
		status = report.StatusFailed
	}
	result := suiteReport(name, stage, status, started, finished, false)
	if suiteError != nil {
		result.Details = suiteError.Error()
	}
	runtime.recordSuiteResult(result)
}
