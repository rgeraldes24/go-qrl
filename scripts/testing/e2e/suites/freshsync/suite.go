// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package freshsync proves that a third execution client can start from a
// genuinely empty datadir, synchronize VM64 state through snap or full sync,
// and remain consistent with finalized execution and consensus state.
package freshsync

import (
	"context"
	"errors"
	"fmt"

	kurtosisapi "github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/lifecycle"
)

// Run executes one snap or full fresh-sync suite. Service configuration is
// still cloned through the proven CLI JSON round trip; immutable identity,
// endpoint refresh, reconciliation, and cleanup use the project SDK adapter.
func Run(ctx context.Context, cfg Config, options Options) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	client := options.Client
	if client == nil {
		var err error
		client, err = kurtosisapi.NewSDKClient()
		if err != nil {
			return fmt.Errorf("connect to Kurtosis engine: %w", err)
		}
	}
	enclave, err := resolveEnclave(ctx, client, cfg.Enclave, options.Enclave)
	if err != nil {
		return err
	}
	// Every compatibility CLI operation is scoped by the immutable full UUID,
	// never by an enclave name that may have been reused.
	cfg.Enclave = enclave.UUID
	if provider, ok := options.Recorder.(interface {
		ActiveFreshSyncStage(context.Context) (string, error)
	}); ok {
		stage, err := provider.ActiveFreshSyncStage(ctx)
		if err != nil {
			return fmt.Errorf("validate active fresh-sync checkpoint stage: %w", err)
		}
		if want := "fresh-" + cfg.SyncMode; stage != want {
			return fmt.Errorf("fresh-sync mode %s does not match active checkpoint stage %s", cfg.SyncMode, stage)
		}
	}
	transactionLabel := transferTransactionLabel(cfg.SyncMode)
	recordedTransaction := ""
	if options.RecordedTransactions != nil {
		recordedTransaction = options.RecordedTransactions[transactionLabel]
	}
	var recordedIntent *lifecycle.ManagedTransactionIntent
	if intent, ok := options.RecordedManagedTransactionIntents[transactionLabel]; ok {
		copy := intent
		recordedIntent = &copy
	}
	_, initialAttempt := options.ManagedTransactionInitialAttempts[transactionLabel]
	_, resubmitted := options.ManagedTransactionResubmits[transactionLabel]
	if (initialAttempt || resubmitted) && recordedIntent == nil {
		return fmt.Errorf("managed transaction %s has an attempt marker without immutable intent", transactionLabel)
	}
	if resubmitted && !initialAttempt {
		return fmt.Errorf("managed transaction %s has a resubmit marker without an initial-attempt marker", transactionLabel)
	}
	if recordedTransaction != "" && recordedIntent == nil {
		return fmt.Errorf("managed transaction %s has a submitted hash without immutable intent", transactionLabel)
	}
	if recordedTransaction != "" && !initialAttempt {
		return fmt.Errorf("managed transaction %s has a submitted hash and immutable intent without an initial-attempt marker", transactionLabel)
	}
	if recordedTransaction == "" && (options.TransactionRecorder == nil || options.ManagedTransactionRecorder == nil) {
		return fmt.Errorf("managed transaction %s requires durable intent, attempt, and hash recorders before the fresh-sync run starts", transactionLabel)
	}
	recordedServices := cloneServiceUUIDMap(options.RecordedServices)
	creationIntents := cloneCreationIntentMap(options.RecordedServiceCreationIntents)
	if provider, ok := options.Recorder.(interface {
		TemporaryServiceCreationState(context.Context) (map[string]lifecycle.TemporaryServiceCreationIntent, map[string]string, error)
	}); ok && (options.RecordedServices == nil || options.RecordedServiceCreationIntents == nil) {
		loadedIntents, loadedServices, err := provider.TemporaryServiceCreationState(ctx)
		if err != nil {
			return fmt.Errorf("load temporary-service creation state: %w", err)
		}
		if options.RecordedServices == nil {
			recordedServices = loadedServices
		}
		if options.RecordedServiceCreationIntents == nil {
			creationIntents = loadedIntents
		}
	}
	recovery, err := RecoverTemporaryServiceCreations(
		ctx, client, enclave, recordedServices, creationIntents, cfg.FreshELService, cfg.FreshCLService,
	)
	if err != nil {
		return fmt.Errorf("recover previous fresh-sync service creation: %w", err)
	}
	if err := validateTemporaryServiceRecoveryRecorder(options.Recorder, recovery); err != nil {
		return err
	}
	if err := PersistTemporaryServiceCreationRecovery(ctx, options.Recorder, recovery); err != nil {
		return err
	}
	for _, identity := range recovery.Bound {
		recordedServices[identity.Name] = identity.UUID
	}
	for _, identity := range recovery.Absent {
		delete(recordedServices, identity.Name)
	}
	for _, intent := range recovery.AbandonedIntents {
		delete(creationIntents, intent.Name)
	}
	if len(recovery.LegacyRemovals) != 0 {
		if err := CleanupTemporaryServices(ctx, client, enclave, recovery.LegacyRemovals); err != nil {
			return fmt.Errorf("remove legacy temporary services before retry: %w", err)
		}
		reconciler := options.Recorder.(TemporaryServiceReconciler)
		if err := reconciler.ReconcileTemporaryServices(ctx, recovery.LegacyRemovals); err != nil {
			return fmt.Errorf("persist legacy temporary-service reconciliation: %w", err)
		}
		for _, identity := range recovery.LegacyRemovals {
			delete(recordedServices, identity.Name)
		}
	}
	reusableServices := make(map[string]TemporaryService, len(recovery.Reusable))
	for _, identity := range recovery.Reusable {
		reusableServices[identity.Name] = identity
	}
	return runFreshSyncWithLifecycle(
		ctx, cfg, execRunner{}, client, enclave, options.Recorder, options.TransactionRecorder,
		options.ManagedTransactionRecorder, recordedTransaction, recordedIntent, initialAttempt, resubmitted, options.Now,
		creationIntents, reusableServices,
	)
}

func validateTemporaryServiceRecoveryRecorder(recorder TemporaryServiceRecorder, recovery TemporaryServiceCreationRecovery) error {
	if len(recovery.Bound) != 0 && recorder == nil {
		return errors.New("temporary service creation recovery requires a UUID recorder")
	}
	if len(recovery.Absent) != 0 {
		if _, ok := recorder.(TemporaryServiceReconciler); !ok {
			return errors.New("absent temporary services require a recorder that can reconcile UUIDs")
		}
	}
	if len(recovery.AbandonedIntents) != 0 {
		if _, ok := recorder.(TemporaryServiceCreationReconciler); !ok {
			return errors.New("absent temporary-service creations require an intent reconciler")
		}
	}
	if len(recovery.LegacyRemovals) != 0 {
		if _, ok := recorder.(TemporaryServiceReconciler); !ok {
			return errors.New("legacy temporary services require a recorder that can safely reconcile UUIDs")
		}
	}
	return nil
}

func resolveEnclave(ctx context.Context, client kurtosisapi.Client, identifier string, provided lifecycle.EnclaveRef) (lifecycle.EnclaveRef, error) {
	if client == nil {
		return lifecycle.EnclaveRef{}, errors.New("Kurtosis client is required")
	}
	lookup := identifier
	if provided.UUID != "" || provided.Name != "" {
		if err := provided.Validate(); err != nil {
			return lifecycle.EnclaveRef{}, fmt.Errorf("provided enclave identity: %w", err)
		}
		if identifier != provided.Name && identifier != provided.UUID {
			return lifecycle.EnclaveRef{}, fmt.Errorf("configured enclave %q does not match provided identity %s/%s", identifier, provided.Name, provided.UUID)
		}
		lookup = provided.UUID
	}
	current, err := client.GetEnclave(ctx, lookup)
	if err != nil {
		return lifecycle.EnclaveRef{}, fmt.Errorf("resolve enclave %q: %w", lookup, err)
	}
	if err := current.Validate(); err != nil {
		return lifecycle.EnclaveRef{}, err
	}
	if provided.UUID != "" && (current.UUID != provided.UUID || current.Name != provided.Name) {
		return lifecycle.EnclaveRef{}, fmt.Errorf("enclave identity changed: current=%s/%s provided=%s/%s", current.Name, current.UUID, provided.Name, provided.UUID)
	}
	current.Owned = provided.Owned
	return current, nil
}
