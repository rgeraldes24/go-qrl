// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package console executes the JavaScript suites that validate the embedded
// gqrl console. Suites run serially because some use shared chain state.
package console

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
)

const SentinelPrefix = "VM64_E2E_RESULT "

var suiteNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

type Definition struct {
	Name       string
	Disruptive bool
}

var Definitions = []Definition{
	{Name: "web3_sanity"},
	{Name: "api_surfaces"},
	{Name: "logs_topics"},
	{Name: "event_roundtrip", Disruptive: true},
	{Name: "abi_vm64"},
}

type Sentinel struct {
	Schema int    `json:"schema"`
	Suite  string `json:"suite"`
	Status string `json:"status"`
	Passed int    `json:"passed"`
	Failed int    `json:"failed"`
	Total  int    `json:"total"`
}

type Result struct {
	Name            string    `json:"name"`
	Status          string    `json:"status"`
	Passed          int       `json:"passed"`
	Failed          int       `json:"failed"`
	Total           int       `json:"total"`
	LegacyMarker    bool      `json:"legacy_marker"`
	Structured      bool      `json:"structured"`
	ExitCode        int       `json:"exit_code"`
	StartedAt       time.Time `json:"started_at"`
	FinishedAt      time.Time `json:"finished_at"`
	Output          []byte    `json:"-"`
	OutputTruncated bool      `json:"output_truncated,omitempty"`
}

type Config struct {
	GQRLPath               string
	JSPath                 string
	RPCURL                 string
	Suites                 []Definition
	ExpectedCommit         string
	AllowDisruptive        bool
	OutputDir              string
	MaxOutputBytes         int64
	TransactionLabelPrefix string
	TransactionRecorder    TransactionRecorder
	ParametersScript       string
	Runner                 CommandRunner
}

type CommandRunner func(context.Context, process.Command) (process.Result, error)

type TransactionRecorder interface {
	RecordTransaction(label, hash string) error
}

type TransactionRecorderFunc func(label, hash string) error

func (function TransactionRecorderFunc) RecordTransaction(label, hash string) error {
	return function(label, hash)
}

func ReadOnlyDefinitions() []Definition {
	result := make([]Definition, 0, len(Definitions))
	for _, definition := range Definitions {
		if !definition.Disruptive {
			result = append(result, definition)
		}
	}
	return result
}

func Run(ctx context.Context, config Config) ([]Result, error) {
	if config.GQRLPath == "" || config.JSPath == "" || config.RPCURL == "" {
		return nil, errors.New("console suite requires gqrl, JavaScript, and RPC paths")
	}
	if len(config.Suites) == 0 {
		config.Suites = Definitions
	}
	if config.OutputDir != "" {
		if err := os.MkdirAll(config.OutputDir, 0o755); err != nil {
			return nil, err
		}
	}
	results := make([]Result, 0, len(config.Suites))
	for _, definition := range config.Suites {
		if definition.Disruptive && !config.AllowDisruptive {
			return results, fmt.Errorf("console suite %s is disruptive and was not authorized", definition.Name)
		}
		result, err := RunOne(ctx, config, definition)
		results = append(results, result)
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

func RunOne(ctx context.Context, config Config, definition Definition) (Result, error) {
	if !suiteNamePattern.MatchString(definition.Name) {
		return Result{}, fmt.Errorf("invalid console suite name %q", definition.Name)
	}
	if definition.Disruptive && !config.AllowDisruptive {
		return Result{}, fmt.Errorf("console suite %s is disruptive and was not authorized", definition.Name)
	}
	transactionWriter := newTransactionWriter(config.TransactionLabelPrefix, config.TransactionRecorder)
	expression := "loadScript('console/" + definition.Name + ".js')"
	if config.ParametersScript != "" {
		if strings.ContainsAny(config.ParametersScript, "'\"\\\n\r") || filepath.IsAbs(config.ParametersScript) || strings.Contains(config.ParametersScript, "..") {
			return Result{}, errors.New("console parameters script path is unsafe")
		}
		expression = "var VM64_PARAMS_FILE='" + config.ParametersScript + "';" + expression
	}
	runner := config.Runner
	if runner == nil {
		runner = process.Run
	}
	commandResult, commandErr := runner(ctx, process.Command{
		Path: config.GQRLPath,
		Args: []string{"attach", "--jspath", config.JSPath, "--exec", expression, config.RPCURL},
		Name: "gqrl-console-" + definition.Name, MaxOutputBytes: config.MaxOutputBytes,
		Stdout: transactionWriter,
	})
	output := append(append([]byte(nil), commandResult.Stdout...), commandResult.Stderr...)
	result, parseErr := ParseResult(definition.Name, output)
	result.ExitCode = commandResult.ExitCode
	result.StartedAt = commandResult.StartedAt
	result.FinishedAt = commandResult.FinishedAt
	result.Output = output
	result.OutputTruncated = commandResult.StdoutTruncated || commandResult.StderrTruncated
	if config.OutputDir != "" {
		if err := writeLog(filepath.Join(config.OutputDir, definition.Name+".log"), output); err != nil {
			return result, err
		}
	}
	if recorderErr := transactionWriter.Finish(output); recorderErr != nil {
		return result, recorderErr
	}
	if commandErr != nil {
		return result, commandErr
	}
	if parseErr != nil {
		return result, parseErr
	}
	if result.Status != "passed" || result.Failed != 0 {
		return result, fmt.Errorf("console suite %s reported %d failed checks", definition.Name, result.Failed)
	}
	return result, nil
}

type transactionSentinel struct {
	Schema int    `json:"schema"`
	Label  string `json:"label"`
	Hash   string `json:"hash"`
}

type transactionWriter struct {
	prefix   string
	recorder TransactionRecorder
	buffer   []byte
	recorded map[string]string
	err      error
}

func newTransactionWriter(prefix string, recorder TransactionRecorder) *transactionWriter {
	return &transactionWriter{prefix: prefix, recorder: recorder, recorded: make(map[string]string)}
}

func (writer *transactionWriter) Write(payload []byte) (int, error) {
	writer.buffer = append(writer.buffer, payload...)
	for {
		newline := bytes.IndexByte(writer.buffer, '\n')
		if newline < 0 {
			break
		}
		writer.consume(strings.TrimSpace(string(writer.buffer[:newline])))
		writer.buffer = writer.buffer[newline+1:]
	}
	return len(payload), writer.err
}

func (writer *transactionWriter) Finish(output []byte) error {
	if len(writer.buffer) > 0 {
		writer.consume(strings.TrimSpace(string(writer.buffer)))
		writer.buffer = nil
	}
	// Some command runners used by tests may not stream Stdout. Re-scan the
	// captured output; recorded labels are idempotently de-duplicated.
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		writer.consume(strings.TrimSpace(string(line)))
	}
	return writer.err
}

func (writer *transactionWriter) consume(line string) {
	if writer.err != nil || !strings.HasPrefix(line, "VM64_E2E_TX ") {
		return
	}
	var sentinel transactionSentinel
	if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "VM64_E2E_TX ")), &sentinel); err != nil {
		writer.err = fmt.Errorf("decode transaction sentinel: %w", err)
		return
	}
	if sentinel.Schema != 1 || sentinel.Label == "" || !strings.HasPrefix(sentinel.Hash, "0x") {
		writer.err = fmt.Errorf("invalid transaction sentinel: %+v", sentinel)
		return
	}
	label := writer.prefix + sentinel.Label
	if previous, exists := writer.recorded[label]; exists {
		if previous != sentinel.Hash {
			writer.err = fmt.Errorf("transaction label %s changed from %s to %s", label, previous, sentinel.Hash)
		}
		return
	}
	writer.recorded[label] = sentinel.Hash
	if writer.recorder != nil {
		if err := writer.recorder.RecordTransaction(label, sentinel.Hash); err != nil {
			writer.err = fmt.Errorf("transaction %s as %s was submitted but could not be recorded: %w", sentinel.Hash, label, err)
		}
	}
}

func ParseResult(name string, output []byte) (Result, error) {
	result := Result{Name: name, ExitCode: -1}
	lines := bytes.Split(output, []byte{'\n'})
	var sentinels []Sentinel
	for _, raw := range lines {
		line := strings.TrimSpace(string(raw))
		if line == "SUITE "+name+": PASSED" || strings.HasPrefix(line, "SUITE "+name+": PASSED ") {
			result.LegacyMarker = true
		}
		if !strings.HasPrefix(line, SentinelPrefix) {
			continue
		}
		var sentinel Sentinel
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, SentinelPrefix)), &sentinel); err != nil {
			return result, fmt.Errorf("decode console suite %s result sentinel: %w", name, err)
		}
		sentinels = append(sentinels, sentinel)
	}
	if len(sentinels) > 1 {
		return result, fmt.Errorf("console suite %s emitted %d result sentinels", name, len(sentinels))
	}
	if len(sentinels) == 1 {
		sentinel := sentinels[0]
		if sentinel.Schema != 1 || sentinel.Suite != name || sentinel.Total != sentinel.Passed+sentinel.Failed || sentinel.Passed < 0 || sentinel.Failed < 0 || (sentinel.Status != "passed" && sentinel.Status != "failed") {
			return result, fmt.Errorf("console suite %s emitted an invalid result sentinel: %+v", name, sentinel)
		}
		result.Structured = true
		result.Status = sentinel.Status
		result.Passed = sentinel.Passed
		result.Failed = sentinel.Failed
		result.Total = sentinel.Total
		return result, nil
	}
	if result.LegacyMarker {
		result.Status = "passed"
		return result, nil
	}
	return result, fmt.Errorf("console suite %s emitted neither a structured result nor a legacy pass marker", name)
}

func writeLog(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	return file.Close()
}
