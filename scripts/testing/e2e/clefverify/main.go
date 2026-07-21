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

// clefverify is the temporary command wrapper for the importable Clef suite.
// Use `clefverify run` for the complete scenario. The legacy verifier-only
// flags remain available until all external callers use vm64e2e.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/suites/clef"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "run" {
		if err := run(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "clefverify: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := verify(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "clefverify: %v\n", err)
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("clefverify run", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var config clef.Config
	var seedEnvironment string
	flags.StringVar(&config.ClefPath, "clef", "", "path to the Clef executable")
	flags.StringVar(&seedEnvironment, "seed-env", "DEPLOYER_SEED", "environment variable containing the private test seed")
	flags.StringVar(&config.ArtifactDir, "artifacts", "", "directory for durable Clef suite artifacts")
	flags.StringVar(&config.Host, "host", "127.0.0.1", "loopback address for the standalone Clef HTTP API")
	flags.IntVar(&config.Port, "port", 18550, "port for the standalone Clef HTTP API")
	flags.Int64Var(&config.ChainID, "chain-id", 1337, "VM64 test-network chain ID")
	flags.DurationVar(&config.ReadyTimeout, "ready-timeout", 30*time.Second, "maximum time to wait for account_version")
	flags.DurationVar(&config.PollInterval, "poll-interval", time.Second, "account_version readiness poll interval")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	seed, present := os.LookupEnv(seedEnvironment)
	if !present || seed == "" {
		return fmt.Errorf("seed environment variable %s is empty or unset", seedEnvironment)
	}
	config.Seed = seed
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	result, err := clef.Run(ctx, config)
	if err != nil {
		return err
	}
	for _, assertion := range result.Assertions {
		fmt.Println(assertion)
	}
	fmt.Printf("SUITE %s: PASSED\n", result.Name)
	return nil
}

func verify(arguments []string) error {
	flags := flag.NewFlagSet("clefverify", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	var paths clef.ScenarioPaths
	flags.StringVar(&paths.Seed, "seed", "", "hex-encoded ML-DSA-87 extended seed imported into Clef")
	flags.StringVar(&paths.Account, "account", "", "Q-prefixed account returned by Clef importraw")
	flags.StringVar(&paths.VersionResponse, "version-response", "", "account_version JSON-RPC response")
	flags.StringVar(&paths.ListResponse, "list-response", "", "account_list JSON-RPC response")
	flags.StringVar(&paths.DataRequest, "data-request", "", "account_signData JSON-RPC request")
	flags.StringVar(&paths.DataResponse, "data-response", "", "account_signData JSON-RPC response")
	flags.StringVar(&paths.TypedRequest, "typed-request", "", "account_signTypedData JSON-RPC request")
	flags.StringVar(&paths.TypedResponse, "typed-response", "", "account_signTypedData JSON-RPC response")
	flags.StringVar(&paths.TxRequest, "tx-request", "", "account_signTransaction JSON-RPC request")
	flags.StringVar(&paths.TxResponse, "tx-response", "", "account_signTransaction JSON-RPC response")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	if err := clef.VerifyScenarioFiles(paths); err != nil {
		return err
	}
	for _, assertion := range clef.PassAssertions() {
		fmt.Println(assertion)
	}
	fmt.Printf("SUITE %s: PASSED\n", clef.Name)
	return nil
}
