// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package report

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCollectDiagnosticsRunsConcurrentlyAndRepresentsErrors(t *testing.T) {
	var clockMu sync.Mutex
	clockTick := 0
	writer, err := NewWithClock(filepath.Join(t.TempDir(), "artifacts"), func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		value := testTime.Add(time.Duration(clockTick) * time.Millisecond)
		clockTick++
		return value
	})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 4)
	release := make(chan struct{})
	task := func(name string, body []byte, taskErr error, panicValue any) DiagnosticTask {
		return DiagnosticTask{Name: name, Collect: func(context.Context) ([]byte, error) {
			started <- name
			<-release
			if panicValue != nil {
				panic(panicValue)
			}
			return body, taskErr
		}}
	}
	tasks := []DiagnosticTask{
		task("success", []byte("complete"), nil, nil),
		task("partial", []byte("partial output"), errors.New("log stream ended"), nil),
		task("canceled", nil, context.Canceled, nil),
		task("panic", nil, nil, "boom"),
	}
	type response struct {
		collection DiagnosticCollection
		err        error
	}
	result := make(chan response, 1)
	go func() {
		collection, err := writer.CollectDiagnostics(context.Background(), tasks)
		result <- response{collection: collection, err: err}
	}()
	seen := make(map[string]bool)
	for range tasks {
		select {
		case name := <-started:
			seen[name] = true
		case <-time.After(2 * time.Second):
			t.Fatal("collectors did not all start before release; collection is not concurrent")
		}
	}
	close(release)
	var got response
	select {
	case got = <-result:
	case <-time.After(5 * time.Second):
		t.Fatal("diagnostic collection did not finish")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	if len(seen) != len(tasks) || len(got.collection.Entries) != len(tasks) {
		t.Fatalf("started=%v collection=%#v", seen, got.collection)
	}
	wantStatuses := []DiagnosticStatus{DiagnosticCollected, DiagnosticFailed, DiagnosticCanceled, DiagnosticFailed}
	for index, entry := range got.collection.Entries {
		if entry.Name != tasks[index].Name || entry.Status != wantStatuses[index] || !entry.Written {
			t.Errorf("entry %d=%#v", index, entry)
		}
		if entry.Path != "diagnostics/"+entry.Name+".log" || entry.SHA256 == "" {
			t.Errorf("entry %d lacks stable path/hash: %#v", index, entry)
		}
	}
	if got.collection.Entries[0].Error != "" || !strings.Contains(got.collection.Entries[1].Error, "log stream ended") || !strings.Contains(got.collection.Entries[2].Error, "context canceled") || !strings.Contains(got.collection.Entries[3].Error, "collector panic: boom") {
		t.Fatalf("collection errors not represented: %#v", got.collection.Entries)
	}
	if data := readTestFile(t, filepath.Join(writer.Root(), "diagnostics", "partial.log")); string(data) != "partial output" {
		t.Fatalf("partial evidence=%q", data)
	}
	var persisted DiagnosticCollection
	if err := json.Unmarshal(readTestFile(t, filepath.Join(writer.Root(), "diagnostics", DiagnosticIndexFilename)), &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Entries) != len(tasks) || persisted.Entries[3].Status != DiagnosticFailed {
		t.Fatalf("persisted collection=%#v", persisted)
	}
}

func TestDiagnosticArtifactWriteFailureIsRepresented(t *testing.T) {
	writer := newTestWriter(t)
	blocked := filepath.Join(writer.Root(), DiagnosticsDirectory, "blocked.log")
	if err := os.Mkdir(blocked, 0o755); err != nil {
		t.Fatal(err)
	}
	collection, err := writer.CollectDiagnostics(context.Background(), []DiagnosticTask{{
		Name: "blocked", Collect: func(context.Context) ([]byte, error) { return []byte("evidence"), nil },
	}})
	if err != nil {
		t.Fatalf("collection index should survive an individual write failure: %v", err)
	}
	entry := collection.Entries[0]
	if entry.Status != DiagnosticFailed || entry.Written || entry.Error == "" || entry.SizeBytes != int64(len("evidence")) || entry.SHA256 == "" {
		t.Fatalf("write failure not represented: %#v", entry)
	}
	if _, err := os.Stat(filepath.Join(writer.Root(), DiagnosticsDirectory, DiagnosticIndexFilename)); err != nil {
		t.Fatalf("collection index missing: %v", err)
	}
}

func TestDiagnosticValidationIsAllOrNothing(t *testing.T) {
	writer := newTestWriter(t)
	called := false
	collector := func(context.Context) ([]byte, error) {
		called = true
		return nil, nil
	}
	tests := []struct {
		name  string
		tasks []DiagnosticTask
	}{
		{"unsafe", []DiagnosticTask{{Name: "../escape", Collect: collector}}},
		{"nil", []DiagnosticTask{{Name: "nil"}}},
		{"duplicate", []DiagnosticTask{{Name: "same", Collect: collector}, {Name: "same", Collect: collector}}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			called = false
			if _, err := writer.CollectDiagnostics(context.Background(), test.tasks); err == nil {
				t.Fatal("invalid diagnostic task list accepted")
			}
			if called {
				t.Fatal("collector ran before all tasks were validated")
			}
		})
	}
}

func TestEmptyDiagnosticCollectionStillProducesIndex(t *testing.T) {
	writer := newTestWriter(t)
	collection, err := writer.CollectDiagnostics(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if collection.Schema != SchemaVersion || collection.Entries == nil || len(collection.Entries) != 0 {
		t.Fatalf("empty collection=%#v", collection)
	}
	data := readTestFile(t, filepath.Join(writer.Root(), DiagnosticsDirectory, DiagnosticIndexFilename))
	if !strings.Contains(string(data), `"entries": []`) {
		t.Fatalf("empty entries encoded as null: %s", data)
	}
}
