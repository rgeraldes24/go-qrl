package console

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
)

func TestParseStructuredResult(t *testing.T) {
	output := []byte("PASS: one\n" + SentinelPrefix + `{"schema":1,"suite":"web3_sanity","status":"passed","passed":7,"failed":0,"total":7}` + "\nSUITE web3_sanity: PASSED (7/7 checks)\n")
	result, err := ParseResult("web3_sanity", output)
	if err != nil {
		t.Fatal(err)
	}
	if !result.Structured || !result.LegacyMarker || result.Passed != 7 || result.Status != "passed" {
		t.Fatalf("result = %+v", result)
	}
}

func TestParseRejectsDuplicateOrMalformedSentinel(t *testing.T) {
	valid := SentinelPrefix + `{"schema":1,"suite":"abi_vm64","status":"passed","passed":1,"failed":0,"total":1}`
	if _, err := ParseResult("abi_vm64", []byte(valid+"\n"+valid+"\n")); err == nil || !strings.Contains(err.Error(), "2 result sentinels") {
		t.Fatalf("duplicate error = %v", err)
	}
	bad := SentinelPrefix + `{"schema":1,"suite":"abi_vm64","status":"passed","passed":1,"failed":1,"total":1}`
	if _, err := ParseResult("abi_vm64", []byte(bad)); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("malformed error = %v", err)
	}
}

func TestParseRetainsLegacyCompatibility(t *testing.T) {
	result, err := ParseResult("logs_topics", []byte("SUITE logs_topics: PASSED (9/9 checks)\n"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Structured || !result.LegacyMarker || result.Status != "passed" {
		t.Fatalf("result = %+v", result)
	}
}

func TestReadOnlyDefinitionsExcludeMutation(t *testing.T) {
	for _, definition := range ReadOnlyDefinitions() {
		if definition.Disruptive || definition.Name == "event_roundtrip" {
			t.Fatalf("read-only definitions contain %+v", definition)
		}
	}
}

func TestDefinitionsPreserveHistoricalSerializedOrder(t *testing.T) {
	want := []string{"web3_sanity", "api_surfaces", "logs_topics", "event_roundtrip", "abi_vm64"}
	if len(Definitions) != len(want) {
		t.Fatalf("definitions = %v", Definitions)
	}
	for index, definition := range Definitions {
		if definition.Name != want[index] {
			t.Fatalf("definition %d = %q, want %q", index, definition.Name, want[index])
		}
	}
}

func TestRunOneUsesInjectedRunnerAndSafeParametersPath(t *testing.T) {
	called := false
	result, err := RunOne(t.Context(), Config{
		GQRLPath: "/build/bin/gqrl", JSPath: "/testdata", RPCURL: "http://127.0.0.1:8545",
		AllowDisruptive: true, ParametersScript: "console/params.js",
		Runner: func(_ context.Context, command process.Command) (process.Result, error) {
			called = true
			if command.Path != "/build/bin/gqrl" || len(command.Args) != 6 {
				t.Fatalf("command = %#v", command)
			}
			if got := command.Args[4]; got != "var VM64_PARAMS_FILE='console/params.js';loadScript('console/event_roundtrip.js')" {
				t.Fatalf("expression = %q", got)
			}
			now := time.Now().UTC()
			output := []byte(SentinelPrefix + `{"schema":1,"suite":"event_roundtrip","status":"passed","passed":1,"failed":0,"total":1}` + "\n")
			return process.Result{ExitCode: 0, Stdout: output, StartedAt: now, FinishedAt: now}, nil
		},
	}, Definition{Name: "event_roundtrip", Disruptive: true})
	if err != nil {
		t.Fatal(err)
	}
	if !called || result.Status != "passed" {
		t.Fatalf("called=%t result=%#v", called, result)
	}

	for _, unsafe := range []string{"../params.js", "/tmp/params.js", "console/'params.js"} {
		if _, err := RunOne(t.Context(), Config{
			GQRLPath: "/gqrl", JSPath: "/testdata", RPCURL: "http://127.0.0.1:8545",
			AllowDisruptive: true, ParametersScript: unsafe,
			Runner: func(context.Context, process.Command) (process.Result, error) {
				t.Fatal("runner called for unsafe parameter path")
				return process.Result{}, nil
			},
		}, Definition{Name: "event_roundtrip", Disruptive: true}); err == nil {
			t.Fatalf("unsafe parameters path %q accepted", unsafe)
		}
	}
}

func TestTransactionWriterRecordsStreamingSentinelOnce(t *testing.T) {
	var labels []string
	var hashes []string
	writer := newTransactionWriter("el1-", TransactionRecorderFunc(func(label, hash string) error {
		labels = append(labels, label)
		hashes = append(hashes, hash)
		return nil
	}))
	line := `VM64_E2E_TX {"schema":1,"label":"event-deploy","hash":"0xabc"}` + "\n"
	if _, err := writer.Write([]byte(line[:17])); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte(line[17:])); err != nil {
		t.Fatal(err)
	}
	if err := writer.Finish([]byte(line)); err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0] != "el1-event-deploy" || hashes[0] != "0xabc" {
		t.Fatalf("recorded labels=%v hashes=%v", labels, hashes)
	}
}
