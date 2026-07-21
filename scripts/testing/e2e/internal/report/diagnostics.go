// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package report

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"runtime/debug"
	"sync"
	"time"
)

type DiagnosticStatus string

const (
	DiagnosticCollected DiagnosticStatus = "collected"
	DiagnosticFailed    DiagnosticStatus = "failed"
	DiagnosticCanceled  DiagnosticStatus = "canceled"
)

// DiagnosticTask returns the evidence it could collect and an optional error.
// Non-empty partial evidence is preserved even when Collect returns an error.
type DiagnosticTask struct {
	Name    string
	Collect func(context.Context) ([]byte, error)
}

type DiagnosticEntry struct {
	Name       string           `json:"name"`
	Path       string           `json:"path"`
	Status     DiagnosticStatus `json:"status"`
	StartedAt  time.Time        `json:"started_at"`
	FinishedAt time.Time        `json:"finished_at"`
	SizeBytes  int64            `json:"size_bytes"`
	SHA256     string           `json:"sha256,omitempty"`
	Written    bool             `json:"written"`
	Error      string           `json:"error,omitempty"`
}

type DiagnosticCollection struct {
	Schema     int               `json:"schema"`
	StartedAt  time.Time         `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Entries    []DiagnosticEntry `json:"entries"`
}

// CollectDiagnostics runs every collector concurrently, writes each result to
// diagnostics/<name>.log, and emits diagnostics/collection.json in input order.
// Collector failures and panics are represented in the returned report rather
// than preventing independent evidence from being collected. The returned
// error is reserved for validating or writing the collection artifact itself.
func (writer *Writer) CollectDiagnostics(ctx context.Context, tasks []DiagnosticTask) (DiagnosticCollection, error) {
	seen := make(map[string]struct{}, len(tasks))
	for index, task := range tasks {
		if err := validateComponent(task.Name); err != nil {
			return DiagnosticCollection{}, fmt.Errorf("diagnostic task %d: %w", index, err)
		}
		if task.Collect == nil {
			return DiagnosticCollection{}, fmt.Errorf("diagnostic task %q has no collector", task.Name)
		}
		if _, duplicate := seen[task.Name]; duplicate {
			return DiagnosticCollection{}, fmt.Errorf("diagnostic task %q is duplicated", task.Name)
		}
		seen[task.Name] = struct{}{}
	}

	collection := DiagnosticCollection{
		Schema:    SchemaVersion,
		StartedAt: writer.currentTime(),
		Entries:   make([]DiagnosticEntry, len(tasks)),
	}
	var wait sync.WaitGroup
	wait.Add(len(tasks))
	for index := range tasks {
		index := index
		go func() {
			defer wait.Done()
			collection.Entries[index] = writer.collectDiagnostic(ctx, tasks[index])
		}()
	}
	wait.Wait()
	collection.FinishedAt = writer.currentTime()
	if err := writer.writeJSON(filepath.Join(DiagnosticsDirectory, DiagnosticIndexFilename), collection); err != nil {
		return collection, err
	}
	return collection, nil
}

func (writer *Writer) collectDiagnostic(ctx context.Context, task DiagnosticTask) (entry DiagnosticEntry) {
	entry = DiagnosticEntry{
		Name:      task.Name,
		Path:      filepath.ToSlash(filepath.Join(DiagnosticsDirectory, task.Name+".log")),
		Status:    DiagnosticCollected,
		StartedAt: writer.currentTime(),
	}
	var data []byte
	var collectErr error
	func() {
		defer func() {
			if recovered := recover(); recovered != nil {
				collectErr = fmt.Errorf("collector panic: %v\n%s", recovered, debug.Stack())
			}
		}()
		data, collectErr = task.Collect(ctx)
	}()

	digest := sha256.Sum256(data)
	entry.SizeBytes = int64(len(data))
	entry.SHA256 = hex.EncodeToString(digest[:])
	writeErr := writer.writeBytes(filepath.FromSlash(entry.Path), data, 0o644)
	entry.Written = writeErr == nil
	combined := errors.Join(collectErr, writeErr)
	if combined != nil {
		entry.Error = combined.Error()
		entry.Status = DiagnosticFailed
		if writeErr == nil && (errors.Is(collectErr, context.Canceled) || errors.Is(collectErr, context.DeadlineExceeded)) {
			entry.Status = DiagnosticCanceled
		}
	}
	entry.FinishedAt = writer.currentTime()
	return entry
}
