// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/app"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	application := app.New()
	if err := application.Execute(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "vm64e2e:", err)
		var usage *app.UsageError
		if errors.As(err, &usage) {
			os.Exit(2)
		}
		os.Exit(1)
	}
}
