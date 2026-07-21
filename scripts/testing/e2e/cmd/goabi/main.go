// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Command goabi runs the importable VM64 Go ABI suite.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/suites/goabi"
)

func main() {
	var cfg goabi.Config
	flag.StringVar(&cfg.RPCURL, "rpc", "", "HTTP RPC endpoint of the node")
	flag.StringVar(&cfg.GraphQLURL, "graphql", "", "GraphQL endpoint; skipped when empty")
	flag.StringVar(&cfg.WebSocketURL, "ws", "", "WebSocket RPC endpoint; subscription checks are skipped when empty")
	flag.StringVar(&cfg.SeedHex, "seed", "", "hex encoded ML-DSA-87 wallet seed")
	flag.StringVar(&cfg.BinHex, "bin", "", "hex encoded EventEmitter deployment bytecode")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintf(os.Stderr, "goabi: unexpected arguments: %v\n", flag.Args())
		os.Exit(2)
	}
	if err := cfg.Validate(); err != nil {
		fmt.Fprintln(os.Stderr, "usage: goabi -rpc <url> -seed <hexseed> -bin <deployment bytecode> [-graphql <url>] [-ws <url>]")
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := goabi.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "SUITE go_abi: FAILED -- %v\n", err)
		os.Exit(1)
	}
	fmt.Println("SUITE go_abi: PASSED")
}
