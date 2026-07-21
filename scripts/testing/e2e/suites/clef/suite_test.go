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

package clef

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
)

const (
	testMasterPassword  = "temporary-master-password"
	testAccountPassword = "temporary-account-password"
)

type recordedCommand struct {
	name    string
	args    []string
	stdin   string
	secrets []string
}

type fakeManaged struct {
	done      chan struct{}
	waitError error
	stopError error
	stopOnce  sync.Once
	stopped   bool
	mu        sync.Mutex
}

func newFakeManaged() *fakeManaged {
	return &fakeManaged{done: make(chan struct{})}
}

func (managed *fakeManaged) Done() <-chan struct{} { return managed.done }

func (managed *fakeManaged) Wait(ctx context.Context) error {
	select {
	case <-managed.done:
		return managed.waitError
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

func (managed *fakeManaged) Stop(context.Context) error {
	managed.mu.Lock()
	managed.stopped = true
	managed.mu.Unlock()
	managed.stopOnce.Do(func() { close(managed.done) })
	return managed.stopError
}

func (managed *fakeManaged) wasStopped() bool {
	managed.mu.Lock()
	defer managed.mu.Unlock()
	return managed.stopped
}

func TestRunExercisesStandaloneClefAndKeepsSecretsEphemeral(t *testing.T) {
	fixture := newTestFixture(t)
	server := newScenarioServer(t, fixture.input)
	defer server.Close()

	artifactDirectory := filepath.Join(t.TempDir(), "artifacts")
	temporaryRoot := t.TempDir()
	managed := newFakeManaged()
	var commands []recordedCommand
	var managedCommand process.ManagedCommand
	config := Config{
		ClefPath: "fake-clef", Seed: testSeed, ArtifactDir: artifactDirectory,
		HTTPURL: server.URL, ReadyTimeout: time.Second, PollInterval: time.Millisecond,
		tempRoot:        temporaryRoot,
		secretGenerator: sequentialSecrets(testMasterPassword, testAccountPassword),
		runCommand: func(_ context.Context, command process.Command) (process.Result, error) {
			var stdin []byte
			if command.Stdin != nil {
				var err error
				stdin, err = io.ReadAll(command.Stdin)
				if err != nil {
					t.Fatal(err)
				}
			}
			commands = append(commands, recordedCommand{
				name: command.Name, args: append([]string(nil), command.Args...), stdin: string(stdin),
				secrets: append([]string(nil), command.Secrets...),
			})
			result := process.Result{ExitCode: 0}
			if command.Name == "clef importraw" {
				result.Stdout = []byte("  Address " + fixture.input.Account + "\n")
				if _, err := command.Stdout.Write(result.Stdout); err != nil {
					t.Fatal(err)
				}
			}
			return result, nil
		},
		startCommand: func(_ context.Context, command process.ManagedCommand) (managedProcess, error) {
			managedCommand = command
			if err := os.WriteFile(command.LogPath, []byte("standalone Clef started\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return managed, nil
		},
	}

	result, err := Run(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if result.Name != Name || result.Account != fixture.input.Account || result.Version != "6.1.0" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if !reflect.DeepEqual(result.Assertions, PassAssertions()) {
		t.Fatalf("assertions = %v, want %v", result.Assertions, PassAssertions())
	}
	if !managed.wasStopped() {
		t.Fatal("managed Clef process was not stopped")
	}
	if got, want := commandNames(commands), []string{"clef init", "clef importraw", "clef attest", "clef setpw"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("setup command order = %v, want %v", got, want)
	}
	for _, command := range commands {
		for _, secret := range []string{testSeed, testMasterPassword, testAccountPassword} {
			if !contains(command.secrets, secret) {
				t.Fatalf("%s did not register a secret for output redaction", command.name)
			}
		}
	}
	if managedCommand.Command.Name != "clef signer" || managedCommand.LogPath != result.Artifacts.ClefLog {
		t.Fatalf("unexpected managed command: %#v", managedCommand)
	}
	for _, secret := range []string{testSeed, testMasterPassword, testAccountPassword} {
		if !contains(managedCommand.Command.Secrets, secret) {
			t.Fatalf("managed command did not register a secret for output redaction")
		}
	}
	assertArgumentsContain(t, managedCommand.Command.Args, "--http.addr", "127.0.0.1", "--http.port", "18550", "--chainid", "1337")
	assertArtifactsContainNoSecrets(t, artifactDirectory, testSeed, testMasterPassword, testAccountPassword)
	assertWorkspaceRemoved(t, temporaryRoot)
	assertArtifactModes(t, result.Artifacts)
}

func TestRunPreservesResponsesAndStopsManagedProcessOnVerificationFailure(t *testing.T) {
	fixture := newTestFixture(t)
	fixture.input.ListResponse = rpcResult(t, 2, []string{})
	server := newScenarioServer(t, fixture.input)
	defer server.Close()
	managed := newFakeManaged()
	artifactDirectory := filepath.Join(t.TempDir(), "artifacts")
	config := fakeConfig(t, fixture.input.Account, artifactDirectory, server.URL, managed)

	_, err := Run(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "account_list returned") {
		t.Fatalf("Run error = %v, want account-list verification failure", err)
	}
	if strings.Contains(err.Error(), testSeed) || strings.Contains(err.Error(), testMasterPassword) || strings.Contains(err.Error(), testAccountPassword) {
		t.Fatalf("Run error exposed a secret: %v", err)
	}
	if !managed.wasStopped() {
		t.Fatal("managed Clef process was not stopped after verification failure")
	}
	for _, name := range []string{"version-response.json", "list-response.json", "data-request.json", "data-response.json", "typed-request.json", "typed-response.json", "tx-request.json", "tx-response.json", "clef.log", "setup.log"} {
		if _, err := os.Stat(filepath.Join(artifactDirectory, name)); err != nil {
			t.Errorf("artifact %s was not preserved: %v", name, err)
		}
	}
}

func TestRunReportsVerificationAndManagedStopFailures(t *testing.T) {
	fixture := newTestFixture(t)
	fixture.input.ListResponse = rpcResult(t, 2, []string{})
	server := newScenarioServer(t, fixture.input)
	defer server.Close()
	stopFailure := errors.New("fake process-group stop failure")
	managed := newFakeManaged()
	managed.stopError = stopFailure
	config := fakeConfig(t, fixture.input.Account, filepath.Join(t.TempDir(), "artifacts"), server.URL, managed)

	_, err := Run(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "account_list returned") || !errors.Is(err, stopFailure) {
		t.Fatalf("Run error = %v, want verification and process-stop failures", err)
	}
}

func TestRunReportsManagedProcessExitDuringReadiness(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		http.Error(response, "not ready", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	managed := newFakeManaged()
	managed.waitError = errors.New("fake signer exited")
	close(managed.done)
	managed.stopOnce.Do(func() {})
	artifactDirectory := filepath.Join(t.TempDir(), "artifacts")
	config := fakeConfig(t, newTestFixture(t).input.Account, artifactDirectory, server.URL, managed)

	_, err := Run(context.Background(), config)
	if err == nil || !strings.Contains(err.Error(), "exited before account_version") || !strings.Contains(err.Error(), "clef.log") {
		t.Fatalf("Run error = %v, want managed-process readiness diagnostic", err)
	}
	if _, statError := os.Stat(filepath.Join(artifactDirectory, "clef.log")); statError != nil {
		t.Fatalf("Clef log was not preserved: %v", statError)
	}
}

func TestNormalizeConfigRejectsNonLoopbackClef(t *testing.T) {
	_, err := normalizeConfig(Config{ClefPath: "clef", Seed: testSeed, ArtifactDir: t.TempDir(), Host: "0.0.0.0"})
	if err == nil || !strings.Contains(err.Error(), "not loopback") {
		t.Fatalf("normalizeConfig error = %v, want loopback rejection", err)
	}
	_, err = normalizeConfig(Config{
		ClefPath: "clef", Seed: testSeed, ArtifactDir: t.TempDir(), HTTPURL: "http://example.invalid/",
	})
	if err == nil || !strings.Contains(err.Error(), "not loopback") {
		t.Fatalf("normalizeConfig HTTP URL error = %v, want loopback rejection", err)
	}
}

func fakeConfig(t *testing.T, account, artifactDirectory, endpoint string, managed *fakeManaged) Config {
	t.Helper()
	return Config{
		ClefPath: "fake-clef", Seed: testSeed, ArtifactDir: artifactDirectory,
		HTTPURL: endpoint, ReadyTimeout: time.Second, PollInterval: time.Millisecond,
		secretGenerator: sequentialSecrets(testMasterPassword, testAccountPassword),
		runCommand: func(_ context.Context, command process.Command) (process.Result, error) {
			if command.Name == "clef importraw" {
				output := []byte("  Address " + account + "\n")
				if _, err := command.Stdout.Write(output); err != nil {
					t.Fatal(err)
				}
				return process.Result{ExitCode: 0, Stdout: output}, nil
			}
			return process.Result{ExitCode: 0}, nil
		},
		startCommand: func(_ context.Context, command process.ManagedCommand) (managedProcess, error) {
			if err := os.WriteFile(command.LogPath, []byte("fake managed Clef log\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return managed, nil
		},
	}
}

func newScenarioServer(t *testing.T, input ScenarioInput) *httptest.Server {
	t.Helper()
	responses := map[string][]byte{
		"account_version":         input.VersionResponse,
		"account_list":            input.ListResponse,
		"account_signData":        input.DataResponse,
		"account_signTypedData":   input.TypedResponse,
		"account_signTransaction": input.TxResponse,
	}
	wantedRequests := map[string][]byte{
		"account_signData": rpcRequestBytes("account_signData", []any{
			"text/plain", input.Account, "0x436c656620564d3634207369676e44617461",
		}, 3),
		"account_signTypedData":   typedDataRequest(input.Account),
		"account_signTransaction": transactionRequestBytes(input.Account),
	}
	return httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer request.Body.Close()
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Error(err)
			response.WriteHeader(http.StatusInternalServerError)
			return
		}
		var envelope rpcRequest
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Errorf("decode RPC request: %v", err)
			response.WriteHeader(http.StatusBadRequest)
			return
		}
		if expected, present := wantedRequests[envelope.Method]; present {
			assertJSONEqual(t, body, expected)
		}
		payload, present := responses[envelope.Method]
		if !present {
			t.Errorf("unexpected RPC method %q", envelope.Method)
			response.WriteHeader(http.StatusNotFound)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		if _, err := response.Write(payload); err != nil {
			t.Error(err)
		}
	}))
}

func assertJSONEqual(t *testing.T, got, want []byte) {
	t.Helper()
	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Errorf("JSON request = %s, want %s", got, want)
	}
}

func sequentialSecrets(secrets ...string) func() (string, error) {
	index := 0
	return func() (string, error) {
		if index >= len(secrets) {
			return "", errors.New("no test secret available")
		}
		secret := secrets[index]
		index++
		return secret, nil
	}
}

func commandNames(commands []recordedCommand) []string {
	names := make([]string, len(commands))
	for index, command := range commands {
		names[index] = command.name
	}
	return names
}

func contains(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func assertArgumentsContain(t *testing.T, arguments []string, wanted ...string) {
	t.Helper()
	joined := strings.Join(arguments, "\x00")
	for _, value := range wanted {
		if !strings.Contains(joined, value) {
			t.Errorf("managed arguments do not contain %q: %v", value, arguments)
		}
	}
}

func assertArtifactsContainNoSecrets(t *testing.T, directory string, secrets ...string) {
	t.Helper()
	err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, secret := range secrets {
			if strings.Contains(string(data), secret) {
				t.Errorf("artifact %s contains a secret", filepath.Base(path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertWorkspaceRemoved(t *testing.T, temporaryRoot string) {
	t.Helper()
	entries, err := os.ReadDir(temporaryRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("private Clef workspace was not removed: %v", entries)
	}
}

func assertArtifactModes(t *testing.T, artifacts Artifacts) {
	t.Helper()
	for _, path := range []string{
		artifacts.SetupLog, artifacts.ClefLog, artifacts.VersionResponse, artifacts.ListResponse,
		artifacts.DataRequest, artifacts.DataResponse, artifacts.TypedRequest, artifacts.TypedResponse,
		artifacts.TxRequest, artifacts.TxResponse,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Errorf("stat artifact %s: %v", path, err)
			continue
		}
		if info.Mode().Perm()&0o077 != 0 {
			t.Errorf("artifact %s permissions = %o, want no group/other access", path, info.Mode().Perm())
		}
	}
}
