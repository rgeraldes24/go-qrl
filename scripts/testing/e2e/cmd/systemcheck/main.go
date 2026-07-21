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

// Command systemcheck validates the multi-node local-testnet topology created
// by scripts/local_testnet/start_local_testnet.sh. Unlike the standalone ABI
// checks, this command deliberately uses the topology's shared Clef service and
// both execution, beacon, and validator pairs. Callers must select one
// checkpoint-owned serialized phase explicitly. Restart phases are destructive
// to those ephemeral services while they run, but always attempt to start every
// service they stopped before returning.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/suites/system"
)

func main() {
	log.SetFlags(0)
	cfg, err := system.ParseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "systemcheck:", err)
		os.Exit(2)
	}
	if cfg.Checkpoint == "" {
		fmt.Fprintln(os.Stderr, "systemcheck: -checkpoint is required so transactions, service transitions, fault observations, and retry boundaries survive interruption")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	client, err := kurtosis.NewSDKClient()
	if err != nil {
		fmt.Fprintln(os.Stderr, "systemcheck:", err)
		os.Exit(1)
	}
	runtime, err := system.OpenCompatibilityCheckpoint(ctx, cfg.Checkpoint, cfg, client)
	if err != nil {
		fmt.Fprintln(os.Stderr, "systemcheck:", err)
		os.Exit(1)
	}
	if err := system.Run(ctx, cfg, runtime.Options); err != nil {
		fmt.Fprintln(os.Stderr, "systemcheck:", err)
		os.Exit(1)
	}
	log.Print("SUITE systemcheck: PASSED")
}
