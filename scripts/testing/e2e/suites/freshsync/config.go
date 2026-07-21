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

package freshsync

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

type Config struct {
	Enclave string

	ReferenceService  string
	ELTemplateService string
	CLTemplateService string
	FreshELService    string
	FreshCLService    string
	ReferenceRPC      string
	SyncMode          string

	SignerAddress   common.Address
	Recipient       common.Address
	DepositContract common.Address
	TransferValue   uint64

	Timeout      time.Duration
	PollInterval time.Duration

	KeepServices     bool
	CleanupOnFailure bool
	Checkpoint       string
}

func ParseConfig(args []string) (Config, error) {
	var cfg Config
	fs := flag.NewFlagSet("freshsync", flag.ContinueOnError)
	fs.StringVar(&cfg.Enclave, "enclave", "local-testnet", "Kurtosis enclave name or full UUID")
	fs.StringVar(&cfg.ReferenceService, "reference-service", "el-1-gqrl-qrysm", "healthy execution service used for comparison and transaction submission")
	fs.StringVar(&cfg.ELTemplateService, "el-template-service", "el-2-gqrl-qrysm", "execution service whose inspected config supplies genesis, JWT, bootnodes, image, and flags")
	fs.StringVar(&cfg.CLTemplateService, "cl-template-service", "cl-2-qrysm-gqrl", "beacon service whose inspected config supplies genesis, JWT, bootstrap nodes, image, and flags")
	fs.StringVar(&cfg.FreshELService, "fresh-el-service", "fresh-sync-el", "name for the temporary empty-datadir execution service")
	fs.StringVar(&cfg.FreshCLService, "fresh-cl-service", "fresh-sync-cl", "name for the temporary beacon service that drives post-merge execution sync")
	fs.StringVar(&cfg.ReferenceRPC, "reference-rpc", "", "reference execution HTTP RPC URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.SyncMode, "syncmode", "snap", "fresh execution sync mode: snap or full")

	signer := fs.String("signer-address", defaultSignerAddress, "account managed by the topology Clef")
	recipient := fs.String("recipient", defaultRecipient, "recipient for the post-catch-up VM64 transfer")
	depositContract := fs.String("deposit-contract", defaultDepositContract, "VM64 deposit contract whose finalized storage and proofs must be reproduced")
	fs.Uint64Var(&cfg.TransferValue, "value", 1, "transfer value in planck")
	fs.DurationVar(&cfg.Timeout, "timeout", 50*time.Minute, "maximum duration for the complete fresh-sync check (also caps each eventual condition)")
	fs.DurationVar(&cfg.PollInterval, "poll", 2*time.Second, "poll interval")
	fs.BoolVar(&cfg.KeepServices, "keep-services", false, "keep temporary services after a successful check")
	fs.BoolVar(&cfg.CleanupOnFailure, "cleanup-on-failure", false, "remove temporary services on failure instead of preserving them for diagnostics")
	fs.StringVar(&cfg.Checkpoint, "checkpoint", "", "lifecycle checkpoint used to durably journal temporary services and managed transaction resume evidence")
	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}
	if fs.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	if cfg.Enclave == "" {
		return Config{}, fmt.Errorf("enclave is required")
	}
	for label, name := range map[string]string{
		"reference-service":   cfg.ReferenceService,
		"el-template-service": cfg.ELTemplateService,
		"cl-template-service": cfg.CLTemplateService,
		"fresh-el-service":    cfg.FreshELService,
		"fresh-cl-service":    cfg.FreshCLService,
	} {
		if len(name) > 63 || !serviceNamePattern.MatchString(name) {
			return Config{}, fmt.Errorf("%s %q is not an RFC 1035 Kurtosis service name", label, name)
		}
	}
	if cfg.FreshELService == cfg.FreshCLService || cfg.FreshELService == cfg.ReferenceService || cfg.FreshELService == cfg.ELTemplateService || cfg.FreshCLService == cfg.CLTemplateService {
		return Config{}, fmt.Errorf("temporary service names must be distinct from each other and their source services")
	}
	if cfg.SyncMode != "snap" && cfg.SyncMode != "full" {
		return Config{}, fmt.Errorf("syncmode must be snap or full, got %q", cfg.SyncMode)
	}
	var err error
	if cfg.SignerAddress, err = common.NewAddressFromString(*signer); err != nil {
		return Config{}, fmt.Errorf("invalid signer address: %w", err)
	}
	if cfg.Recipient, err = common.NewAddressFromString(*recipient); err != nil {
		return Config{}, fmt.Errorf("invalid recipient address: %w", err)
	}
	if cfg.DepositContract, err = common.NewAddressFromString(*depositContract); err != nil {
		return Config{}, fmt.Errorf("invalid deposit contract address: %w", err)
	}
	if cfg.SignerAddress == cfg.Recipient {
		return Config{}, fmt.Errorf("recipient must differ from signer address")
	}
	if cfg.TransferValue == 0 {
		return Config{}, fmt.Errorf("transfer value must be positive")
	}
	if cfg.Timeout <= 0 {
		return Config{}, fmt.Errorf("timeout must be positive")
	}
	if cfg.PollInterval <= 0 {
		return Config{}, fmt.Errorf("poll interval must be positive")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// Validate permits importers to construct a Config directly while preserving
// the command's fail-closed service-name and lifecycle checks.
func (cfg Config) Validate() error {
	if cfg.Enclave == "" {
		return fmt.Errorf("enclave is required")
	}
	for label, name := range map[string]string{
		"reference-service":   cfg.ReferenceService,
		"el-template-service": cfg.ELTemplateService,
		"cl-template-service": cfg.CLTemplateService,
		"fresh-el-service":    cfg.FreshELService,
		"fresh-cl-service":    cfg.FreshCLService,
	} {
		if len(name) > 63 || !serviceNamePattern.MatchString(name) {
			return fmt.Errorf("%s %q is not an RFC 1035 Kurtosis service name", label, name)
		}
	}
	if cfg.FreshELService == cfg.FreshCLService || cfg.FreshELService == cfg.ReferenceService || cfg.FreshELService == cfg.ELTemplateService || cfg.FreshCLService == cfg.CLTemplateService {
		return fmt.Errorf("temporary service names must be distinct from each other and their source services")
	}
	if cfg.SyncMode != "snap" && cfg.SyncMode != "full" {
		return fmt.Errorf("syncmode must be snap or full, got %q", cfg.SyncMode)
	}
	if cfg.SignerAddress == cfg.Recipient {
		return fmt.Errorf("recipient must differ from signer address")
	}
	if cfg.TransferValue == 0 {
		return fmt.Errorf("transfer value must be positive")
	}
	if cfg.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if cfg.PollInterval <= 0 {
		return fmt.Errorf("poll interval must be positive")
	}
	return nil
}
