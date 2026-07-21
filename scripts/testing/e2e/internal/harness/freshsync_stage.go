// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package harness

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
	freshSyncSuite "github.com/theQRL/go-qrl/scripts/testing/e2e/suites/freshsync"
)

func (runtime *Runtime) runFreshSyncSuite(ctx context.Context, environment *lifecycle.RunEnvironment, mode string) error {
	if environment == nil || environment.State == nil {
		return errors.New("fresh-sync suite requires lifecycle checkpoint state")
	}
	if runtime.Writer == nil {
		return errors.New("fresh-sync suite requires an artifact writer")
	}
	stageName, elName, clName, err := freshSyncNames(mode)
	if err != nil {
		return err
	}
	configuration, err := freshSyncSuite.ParseConfig([]string{
		"-enclave", runtime.Enclave.UUID,
		"-syncmode", mode,
		"-fresh-el-service", elName,
		"-fresh-cl-service", clName,
	})
	if err != nil {
		return err
	}
	now := runtime.Dependencies.Now
	if now == nil {
		now = time.Now
	}
	recorder := freshSyncSuite.CheckpointRecorder{Store: runtime.Store, Now: now}
	started := now().UTC()
	runner := runtime.Dependencies.FreshSync
	if runner == nil {
		runner = freshSyncSuite.Run
	}
	runErr := runner(ctx, configuration, freshSyncSuite.Options{
		Client: runtime.Dependencies.Client, Enclave: runtime.Enclave, Recorder: recorder,
		TransactionRecorder:               recorder,
		ManagedTransactionRecorder:        recorder,
		RecordedServices:                  environment.State.TemporaryServices,
		RecordedServiceCreationIntents:    environment.State.TemporaryServiceCreationIntents,
		RecordedTransactions:              environment.State.Transactions,
		RecordedManagedTransactionIntents: environment.State.ManagedTransactionIntents,
		ManagedTransactionInitialAttempts: environment.State.ManagedTransactionInitialAttempts,
		ManagedTransactionResubmits:       environment.State.ManagedTransactionResubmits,
		Now:                               now,
	})
	refreshErr := runtime.refreshFreshSyncCheckpoint(environment)
	finished := now().UTC()
	status := reportStatus(runErr == nil && refreshErr == nil)
	runtime.recordSuiteResult(suiteReport(stageName, stageName, status, started, finished, false))
	message := fmt.Sprintf("SUITE %s: PASSED\n", stageName)
	if runErr != nil || refreshErr != nil {
		message = fmt.Sprintf("SUITE %s: FAILED -- %v\n", stageName, errors.Join(runErr, refreshErr))
	}
	attempt := runtime.currentAttempt(stageName)
	_, writeErr := runtime.Writer.WriteSuiteLog(fmt.Sprintf("%s-attempt-%d", stageName, attempt), []byte(message))
	return errors.Join(runErr, refreshErr, writeErr)
}

func (runtime *Runtime) reconcileFreshSyncSuite(ctx context.Context, environment *lifecycle.RunEnvironment, mode string) (lifecycle.ReconcileAction, error) {
	stageName, elName, clName, err := freshSyncNames(mode)
	if err != nil {
		return "", err
	}
	passed := runtime.stageLogPassed(stageName)
	if environment == nil || environment.State == nil {
		return "", errors.New("fresh-sync reconciliation requires lifecycle checkpoint state")
	}
	recovery, err := freshSyncSuite.RecoverTemporaryServiceCreations(
		ctx, runtime.Dependencies.Client, runtime.Enclave, environment.State.TemporaryServices,
		environment.State.TemporaryServiceCreationIntents, elName, clName,
	)
	if err != nil {
		return "", err
	}
	now := runtime.Dependencies.Now
	if now == nil {
		now = time.Now
	}
	recorder := freshSyncSuite.CheckpointRecorder{Store: runtime.Store, Now: now}
	if err := freshSyncSuite.PersistTemporaryServiceCreationRecovery(ctx, recorder, recovery); err != nil {
		return "", err
	}
	removals := append([]freshSyncSuite.TemporaryService(nil), recovery.LegacyRemovals...)
	if passed {
		removals = append(removals, recovery.Reusable...)
	}
	if len(removals) != 0 {
		if err := freshSyncSuite.CleanupTemporaryServices(ctx, runtime.Dependencies.Client, runtime.Enclave, removals); err != nil {
			return "", err
		}
		if err := recorder.ReconcileTemporaryServices(ctx, removals); err != nil {
			return "", err
		}
	}
	if err := runtime.refreshFreshSyncCheckpoint(environment); err != nil {
		return "", err
	}
	if passed {
		return lifecycle.ReconcileComplete, nil
	}
	return lifecycle.ReconcileRetry, nil
}

func (runtime *Runtime) refreshFreshSyncCheckpoint(environment *lifecycle.RunEnvironment) error {
	updated, err := runtime.Store.Load()
	if err != nil {
		return err
	}
	*environment.State = updated
	return nil
}

func freshSyncNames(mode string) (stageName, elName, clName string, err error) {
	if mode != "snap" && mode != "full" {
		return "", "", "", fmt.Errorf("fresh-sync mode must be snap or full, got %q", mode)
	}
	return "fresh-" + mode, "fresh-sync-el-" + mode, "fresh-sync-cl-" + mode, nil
}
