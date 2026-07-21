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

// freshsync proves that a third execution client can start without any seeded
// chain database, synchronize through the production QRL/Snap protocols under
// a real beacon client, and follow a new 64-byte-address state transition.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/suites/freshsync"
)

func main() {
	cfg, err := freshsync.ParseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "freshsync:", err)
		os.Exit(2)
	}
	if cfg.Checkpoint == "" {
		fmt.Fprintln(os.Stderr, "freshsync: -checkpoint is required so managed transaction intent and retry boundaries survive interruption")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client, err := kurtosis.NewSDKClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "freshsync:", err)
		os.Exit(1)
	}
	options := freshsync.Options{Client: client}
	if cfg.Checkpoint != "" {
		state, store, err := freshsync.OpenCheckpoint(cfg.Checkpoint)
		if err != nil {
			fmt.Fprintln(os.Stderr, "freshsync:", err)
			os.Exit(1)
		}
		options.Enclave = state.Enclave
		options.RecordedServices = state.TemporaryServices
		options.RecordedTransactions = state.Transactions
		options.RecordedManagedTransactionIntents = state.ManagedTransactionIntents
		options.ManagedTransactionInitialAttempts = state.ManagedTransactionInitialAttempts
		options.ManagedTransactionResubmits = state.ManagedTransactionResubmits
		recorder := freshsync.CheckpointRecorder{Store: store}
		options.Recorder = recorder
		options.TransactionRecorder = recorder
		options.ManagedTransactionRecorder = recorder
	}
	if err := freshsync.Run(ctx, cfg, options); err != nil {
		fmt.Fprintln(os.Stderr, "freshsync:", err)
		os.Exit(1)
	}
	fmt.Printf("SUITE fresh-%s: PASSED\n", cfg.SyncMode)
}
