// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package finalize collects last-resort diagnostics and performs UUID-safe
// cleanup independently of the main lifecycle runner.
package finalize

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/report"
)

type Dumper interface {
	Dump(context.Context, string, string) ([]byte, error)
}

type CLIDumper struct {
	Binary string
}

func (dumper CLIDumper) Dump(ctx context.Context, enclaveUUID, destination string) ([]byte, error) {
	binary := dumper.Binary
	if binary == "" {
		binary = "kurtosis"
	}
	command := exec.CommandContext(ctx, binary, "enclave", "dump", enclaveUUID, destination)
	output, err := command.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("kurtosis enclave dump: %w", err)
	}
	return output, nil
}

type Options struct {
	OwnershipPath    string
	Writer           *report.Writer
	Client           kurtosis.Client
	Dumper           Dumper
	DestroyPreserved bool
	DestroyReserve   time.Duration
	Now              func() time.Time
}

const DefaultDestroyReserve = 5 * time.Minute

type Result struct {
	Enclave       lifecycle.EnclaveRef `json:"enclave"`
	AlreadyClean  bool                 `json:"already_clean"`
	Preserved     bool                 `json:"preserved"`
	DumpCollected bool                 `json:"dump_collected"`
	LogsCollected bool                 `json:"logs_collected"`
	Destroyed     bool                 `json:"destroyed"`
}

func Run(ctx context.Context, options Options) (Result, error) {
	if options.Writer == nil || options.Client == nil || options.OwnershipPath == "" {
		return Result{}, errors.New("finalizer requires writer, Kurtosis client, and ownership path")
	}
	if err := options.Writer.InvalidateManifest(); err != nil {
		return Result{}, err
	}
	if options.Dumper == nil {
		options.Dumper = CLIDumper{}
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	record, err := lifecycle.LoadOwnership(options.OwnershipPath)
	if err != nil {
		return Result{}, fmt.Errorf("load ownership record: %w", err)
	}
	var diagnosticErrors []error
	if err := options.Writer.WriteOwnership(record); err != nil {
		diagnosticErrors = append(diagnosticErrors, fmt.Errorf("copy ownership into artifacts: %w", err))
	}
	result := Result{}
	if record.DestroyedAt != nil {
		result.AlreadyClean = true
		return result, errors.Join(diagnosticErrors...)
	}
	if record.UUID == nil {
		reason := "ownership record has no captured enclave UUID; refusing name-based cleanup"
		_ = lifecycle.MarkOwnershipPreserved(options.OwnershipPath, reason)
		return Result{Preserved: true}, errors.Join(append(diagnosticErrors, errors.New(reason))...)
	}
	ref := lifecycle.EnclaveRef{Name: record.RequestedName, UUID: *record.UUID, Owned: true}
	result.Enclave = ref
	destroyPending := record.DestroyRequestedAt != nil
	if record.Preserved && !options.DestroyPreserved && !destroyPending {
		result.Preserved = true
		return result, errors.Join(diagnosticErrors...)
	}
	if destroyPending {
		return completeRequestedDestroy(ctx, options, result, ref, now, diagnosticErrors)
	}
	diagnosticContext, cancelDiagnostics := contextBeforeDestroy(ctx, options.DestroyReserve)
	defer cancelDiagnostics()
	current, inspectionErr := options.Client.GetEnclave(diagnosticContext, ref.UUID)
	if inspectionErr != nil {
		inspectionErr = fmt.Errorf("inspect owned enclave by UUID: %w", inspectionErr)
		if !diagnosticCutoffReached(ctx, diagnosticContext) {
			preserved, preserveErr := preserve(options, result, inspectionErr)
			return preserved, errors.Join(append(diagnosticErrors, preserveErr)...)
		}
		diagnosticErrors = append(diagnosticErrors, inspectionErr)
	} else {
		if current.Name != ref.Name || current.UUID != ref.UUID {
			preserved, preserveErr := preserve(options, result, fmt.Errorf("owned enclave identity mismatch: got %s/%s, want %s/%s", current.Name, current.UUID, ref.Name, ref.UUID))
			return preserved, errors.Join(append(diagnosticErrors, preserveErr)...)
		}

		services, servicesErr := options.Client.Services(diagnosticContext, ref)
		if servicesErr != nil {
			diagnosticErrors = append(diagnosticErrors, fmt.Errorf("list services: %w", servicesErr))
		} else {
			names := make([]string, 0, len(services))
			for _, service := range services {
				names = append(names, service.UUID)
			}
			logs, logsErr := options.Client.ServiceLogs(diagnosticContext, ref, names)
			if logsErr != nil {
				diagnosticErrors = append(diagnosticErrors, fmt.Errorf("collect service logs: %w", logsErr))
			} else {
				result.LogsCollected = true
				for name, data := range logs {
					if _, writeErr := options.Writer.WriteServiceLog(safeComponent(name), data); writeErr != nil {
						diagnosticErrors = append(diagnosticErrors, writeErr)
					}
				}
			}
		}
		dumpDestination := filepath.Join(options.Writer.Layout().Kurtosis, "enclave-dump")
		if mkdirErr := os.MkdirAll(filepath.Dir(dumpDestination), 0o755); mkdirErr != nil {
			diagnosticErrors = append(diagnosticErrors, mkdirErr)
		} else {
			output, dumpErr := options.Dumper.Dump(diagnosticContext, ref.UUID, dumpDestination)
			if _, writeErr := options.Writer.WriteKurtosisArtifact("enclave-dump.log", output); writeErr != nil {
				diagnosticErrors = append(diagnosticErrors, writeErr)
			}
			if dumpErr != nil {
				diagnosticErrors = append(diagnosticErrors, dumpErr)
			} else {
				result.DumpCollected = true
			}
		}
	}
	cancelDiagnostics()

	if err := lifecycle.MarkOwnershipDestroyRequested(options.OwnershipPath, ref.UUID, now()); err != nil {
		return preserve(options, result, errors.Join(append(diagnosticErrors, fmt.Errorf("journal owned enclave destruction: %w", err))...))
	}
	if updated, loadErr := lifecycle.LoadOwnership(options.OwnershipPath); loadErr == nil {
		if writeErr := options.Writer.WriteOwnership(updated); writeErr != nil {
			diagnosticErrors = append(diagnosticErrors, writeErr)
		}
	} else {
		diagnosticErrors = append(diagnosticErrors, loadErr)
	}
	return completeRequestedDestroy(ctx, options, result, ref, now, diagnosticErrors)
}

func completeRequestedDestroy(ctx context.Context, options Options, result Result, ref lifecycle.EnclaveRef, now func() time.Time, diagnosticErrors []error) (Result, error) {
	destroyErr := options.Client.DestroyEnclave(ctx, ref)
	if destroyErr != nil {
		exists, inspectErr := options.Client.EnclaveExists(ctx, ref.UUID)
		if inspectErr != nil || exists {
			cause := fmt.Errorf("destroy owned enclave: %w", destroyErr)
			if inspectErr != nil {
				cause = errors.Join(cause, fmt.Errorf("reconcile destroyed enclave existence: %w", inspectErr))
			} else {
				cause = errors.Join(cause, errors.New("owned enclave still exists after failed destruction"))
			}
			return preserve(options, result, errors.Join(append(diagnosticErrors, cause)...))
		}
		// The exact UUID is absent after a durable destruction request. Treat this
		// as a lost successful response and finish the local ownership record.
		result.AlreadyClean = true
	}
	result.Destroyed = true
	if err := lifecycle.MarkOwnershipDestroyed(options.OwnershipPath, ref.UUID, now()); err != nil {
		return result, errors.Join(append(diagnosticErrors, fmt.Errorf("mark ownership destroyed: %w", err))...)
	}
	updated, loadErr := lifecycle.LoadOwnership(options.OwnershipPath)
	if loadErr == nil {
		loadErr = options.Writer.WriteOwnership(updated)
	}
	return result, errors.Join(append(diagnosticErrors, loadErr)...)
}

func contextBeforeDestroy(parent context.Context, reserve time.Duration) (context.Context, context.CancelFunc) {
	if reserve <= 0 {
		reserve = DefaultDestroyReserve
	}
	deadline, ok := parent.Deadline()
	if !ok {
		return context.WithCancel(parent)
	}
	return context.WithDeadline(parent, deadline.Add(-reserve))
}

func diagnosticCutoffReached(parent, diagnostics context.Context) bool {
	return diagnostics.Err() != nil && parent.Err() == nil
}

func preserve(options Options, result Result, cause error) (Result, error) {
	result.Preserved = true
	markErr := lifecycle.MarkOwnershipPreserved(options.OwnershipPath, cause.Error())
	if updated, err := lifecycle.LoadOwnership(options.OwnershipPath); err == nil {
		markErr = errors.Join(markErr, options.Writer.WriteOwnership(updated))
	}
	return result, errors.Join(cause, markErr)
}

func safeComponent(name string) string {
	var result strings.Builder
	for _, character := range name {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("._-", character) {
			result.WriteRune(character)
		} else {
			result.WriteByte('_')
		}
	}
	if result.Len() == 0 || result.String() == "." || result.String() == ".." {
		return "service"
	}
	return result.String()
}
