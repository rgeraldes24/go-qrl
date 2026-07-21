// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Command depositcheck submits real VM64 validator deposits to the preloaded
// Hyperion deposit contract and proves that both Qrysm beacon nodes ingest them.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/suites/deposit"
)

func main() {
	log.SetFlags(0)
	cfg, err := deposit.ParseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "depositcheck:", err)
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := deposit.Run(ctx, cfg, deposit.Options{}); err != nil {
		fmt.Fprintln(os.Stderr, "depositcheck:", err)
		os.Exit(1)
	}
	log.Print("SUITE depositcheck: PASSED")
}
