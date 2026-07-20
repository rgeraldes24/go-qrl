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

package main

import (
	"flag"
	"fmt"
	"regexp"
	"time"

	"github.com/theQRL/go-qrl/common"
)

const (
	defaultSignerAddress   = "Q738a3bdbcd5e0924d4923ebedfc378dd9111c977bb36116394e900761c741636cb1b9eff5fba549af1f624fed38d628aa1cc1d8f158def9e58eae84d3645a7a7"
	defaultRecipient       = "Qd5812f6cf4a0f645aa620cd57319a0ed649dd8f5519a9dde7770ae5b0e49e547985f35eb972a2a07041561aa39c65a3991478f9b1e6749e05277dcf58a9a8b72"
	defaultDepositContract = "Q42424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242424242"
)

var serviceNamePattern = regexp.MustCompile(`^[a-z](?:[a-z0-9-]*[a-z0-9])?$`)

type config struct {
	enclave string

	referenceService  string
	elTemplateService string
	clTemplateService string
	freshELService    string
	freshCLService    string
	referenceRPC      string
	syncMode          string

	signerAddress   common.Address
	recipient       common.Address
	depositContract common.Address
	transferValue   uint64

	timeout      time.Duration
	pollInterval time.Duration

	keepServices     bool
	cleanupOnFailure bool
}

func parseConfig(args []string) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("freshsync", flag.ContinueOnError)
	fs.StringVar(&cfg.enclave, "enclave", "local-testnet", "Kurtosis enclave name")
	fs.StringVar(&cfg.referenceService, "reference-service", "el-1-gqrl-qrysm", "healthy execution service used for comparison and transaction submission")
	fs.StringVar(&cfg.elTemplateService, "el-template-service", "el-2-gqrl-qrysm", "execution service whose inspected config supplies genesis, JWT, bootnodes, image, and flags")
	fs.StringVar(&cfg.clTemplateService, "cl-template-service", "cl-2-qrysm-gqrl", "beacon service whose inspected config supplies genesis, JWT, bootstrap nodes, image, and flags")
	fs.StringVar(&cfg.freshELService, "fresh-el-service", "fresh-sync-el", "name for the temporary empty-datadir execution service")
	fs.StringVar(&cfg.freshCLService, "fresh-cl-service", "fresh-sync-cl", "name for the temporary beacon service that drives post-merge execution sync")
	fs.StringVar(&cfg.referenceRPC, "reference-rpc", "", "reference execution HTTP RPC URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.syncMode, "syncmode", "snap", "fresh execution sync mode: snap or full")

	signer := fs.String("signer-address", defaultSignerAddress, "account managed by the topology Clef")
	recipient := fs.String("recipient", defaultRecipient, "recipient for the post-catch-up VM64 transfer")
	depositContract := fs.String("deposit-contract", defaultDepositContract, "VM64 deposit contract whose finalized storage and proofs must be reproduced")
	fs.Uint64Var(&cfg.transferValue, "value", 1, "transfer value in planck")
	fs.DurationVar(&cfg.timeout, "timeout", 50*time.Minute, "maximum duration for the complete fresh-sync check (also caps each eventual condition)")
	fs.DurationVar(&cfg.pollInterval, "poll", 2*time.Second, "poll interval")
	fs.BoolVar(&cfg.keepServices, "keep-services", false, "keep temporary services after a successful check")
	fs.BoolVar(&cfg.cleanupOnFailure, "cleanup-on-failure", false, "remove temporary services on failure instead of preserving them for diagnostics")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if cfg.enclave == "" {
		return config{}, fmt.Errorf("enclave is required")
	}
	for label, name := range map[string]string{
		"reference-service":   cfg.referenceService,
		"el-template-service": cfg.elTemplateService,
		"cl-template-service": cfg.clTemplateService,
		"fresh-el-service":    cfg.freshELService,
		"fresh-cl-service":    cfg.freshCLService,
	} {
		if len(name) > 63 || !serviceNamePattern.MatchString(name) {
			return config{}, fmt.Errorf("%s %q is not an RFC 1035 Kurtosis service name", label, name)
		}
	}
	if cfg.freshELService == cfg.freshCLService || cfg.freshELService == cfg.referenceService || cfg.freshELService == cfg.elTemplateService || cfg.freshCLService == cfg.clTemplateService {
		return config{}, fmt.Errorf("temporary service names must be distinct from each other and their source services")
	}
	if cfg.syncMode != "snap" && cfg.syncMode != "full" {
		return config{}, fmt.Errorf("syncmode must be snap or full, got %q", cfg.syncMode)
	}
	var err error
	if cfg.signerAddress, err = common.NewAddressFromString(*signer); err != nil {
		return config{}, fmt.Errorf("invalid signer address: %w", err)
	}
	if cfg.recipient, err = common.NewAddressFromString(*recipient); err != nil {
		return config{}, fmt.Errorf("invalid recipient address: %w", err)
	}
	if cfg.depositContract, err = common.NewAddressFromString(*depositContract); err != nil {
		return config{}, fmt.Errorf("invalid deposit contract address: %w", err)
	}
	if cfg.signerAddress == cfg.recipient {
		return config{}, fmt.Errorf("recipient must differ from signer address")
	}
	if cfg.transferValue == 0 {
		return config{}, fmt.Errorf("transfer value must be positive")
	}
	if cfg.timeout <= 0 {
		return config{}, fmt.Errorf("timeout must be positive")
	}
	if cfg.pollInterval <= 0 {
		return config{}, fmt.Errorf("poll interval must be positive")
	}
	return cfg, nil
}
