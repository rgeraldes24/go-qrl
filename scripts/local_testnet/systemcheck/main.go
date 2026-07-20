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
// both execution, beacon, and validator pairs. Its restart phase is destructive
// to those ephemeral services while it runs, but always attempts to start every
// service it stopped before returning.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	log.SetFlags(0)
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "systemcheck:", err)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := runSystemCheck(ctx, cfg, execRunner{}); err != nil {
		fmt.Fprintln(os.Stderr, "systemcheck:", err)
		os.Exit(1)
	}
	log.Print("SUITE systemcheck: PASSED")
}

func runSystemCheck(ctx context.Context, cfg config, runner commandRunner) error {
	runCtx, cancel := context.WithTimeout(ctx, cfg.timeout)
	defer cancel()

	check, err := newSystemCheck(runCtx, cfg, runner)
	if err != nil {
		return err
	}
	defer check.close()
	return check.run(runCtx)
}
