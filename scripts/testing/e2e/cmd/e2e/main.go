// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Command e2e controls the separately managed E2E network.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/app"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	command := app.New()
	if err := command.Execute(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "e2e:", err)
		os.Exit(app.ExitCode(err))
	}
}
