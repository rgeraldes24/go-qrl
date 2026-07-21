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

package system

import (
	"flag"
	"fmt"
	"time"

	"github.com/theQRL/go-qrl/common"
)

const (
	defaultSignerAddress        = "Q738a3bdbcd5e0924d4923ebedfc378dd9111c977bb36116394e900761c741636cb1b9eff5fba549af1f624fed38d628aa1cc1d8f158def9e58eae84d3645a7a7"
	defaultRecipient            = "Qd5812f6cf4a0f645aa620cd57319a0ed649dd8f5519a9dde7770ae5b0e49e547985f35eb972a2a07041561aa39c65a3991478f9b1e6749e05277dcf58a9a8b72"
	expectedFeeRecipientAddress = "Q0838a121a6e4dd8a51e7437b152fabbc76a173f077132f2c2ed021c7b0991e70da4dba44e9ec00984a90f28dfb0aabbda1ddc9e98a76ab0acb6644c5e76fbbe8"
	expectedWithdrawalAddress   = "Qa5aedb928f8300de98c66bb4bb66b9bb137e9a17e9d41039d98a664671b7c8a34bf63d49800d4ff8f4fd28aef583920e018988d994651b6f0f5966b1dbe11a8b"
)

// Phase is one serialized system-suite boundary. Keeping these boundaries
// explicit lets the lifecycle checkpoint and reconcile a disruptive phase
// without replaying the phases that already passed.
type Phase string

const (
	PhaseAll                Phase = "all"
	PhaseBase               Phase = "base"
	PhaseSignerRestart      Phase = "signer-restart"
	PhaseParticipantRestart Phase = "participant-restart"
)

// Config describes the exact two-participant topology and the protocol
// invariants exercised by the suite. Use DefaultConfig or ParseConfig rather
// than relying on zero values.
type Config struct {
	Enclave string
	Phase   Phase
	// Checkpoint is the schema-v1 lifecycle state used by the compatibility
	// command. The importable suite receives its recorders through Options.
	Checkpoint string

	ELServices    [2]string
	CLServices    [2]string
	VCServices    [2]string
	SignerService string

	RPCURLs       [2]string
	CLURLs        [2]string
	VCMetricsURLs [2]string
	SignerURL     string

	SignerAddress       common.Address
	Recipient           common.Address
	FeeRecipient        common.Address
	WithdrawalRecipient common.Address
	TransferValue       uint64

	Timeout               time.Duration
	PollInterval          time.Duration
	ValidatorPollInterval time.Duration
	CatchupBlocks         uint64

	SkipRestarts           bool
	RequireFinalityAdvance bool
	RequireZeroDutyHistory bool
}

type config struct {
	enclave    string
	phase      string
	checkpoint string

	elServices [2]string
	clServices [2]string
	vcServices [2]string
	signerSvc  string

	rpcURLs       [2]string
	clURLs        [2]string
	vcMetricsURLs [2]string
	signerURL     string

	rpcURLsFromKurtosis       [2]bool
	clURLsFromKurtosis        [2]bool
	vcMetricsURLsFromKurtosis [2]bool
	signerURLFromKurtosis     bool

	signerAddress       common.Address
	recipient           common.Address
	feeRecipient        common.Address
	withdrawalRecipient common.Address
	transferValue       uint64

	timeout               time.Duration
	pollInterval          time.Duration
	validatorPollInterval time.Duration
	catchupBlocks         uint64

	skipRestarts           bool
	requireFinalityAdvance bool
	requireZeroDutyHistory bool
}

func parseConfig(args []string) (config, error) {
	var cfg config
	fs := flag.NewFlagSet("systemcheck", flag.ContinueOnError)
	fs.StringVar(&cfg.enclave, "enclave", "local-testnet", "Kurtosis enclave name")
	fs.StringVar(&cfg.phase, "phase", "", "required serialized compatibility phase: base, signer-restart, or participant-restart")
	fs.StringVar(&cfg.checkpoint, "checkpoint", "", "schema-v1 lifecycle checkpoint used to journal transactions, service transitions, and resume evidence")
	fs.StringVar(&cfg.elServices[0], "el1-service", "el-1-gqrl-qrysm", "first execution service")
	fs.StringVar(&cfg.elServices[1], "el2-service", "el-2-gqrl-qrysm", "second execution service")
	fs.StringVar(&cfg.clServices[0], "cl1-service", "cl-1-qrysm-gqrl", "first beacon service")
	fs.StringVar(&cfg.clServices[1], "cl2-service", "cl-2-qrysm-gqrl", "second beacon service")
	fs.StringVar(&cfg.vcServices[0], "vc1-service", "vc-1-gqrl-qrysm", "first validator service")
	fs.StringVar(&cfg.vcServices[1], "vc2-service", "vc-2-gqrl-qrysm", "second validator service")
	fs.StringVar(&cfg.signerSvc, "signer-service", "signer-clef", "Clef service")

	fs.StringVar(&cfg.rpcURLs[0], "rpc1", "", "first execution HTTP RPC URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.rpcURLs[1], "rpc2", "", "second execution HTTP RPC URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.clURLs[0], "cl1", "", "first beacon HTTP URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.clURLs[1], "cl2", "", "second beacon HTTP URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.vcMetricsURLs[0], "vc1-metrics", "", "first validator metrics URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.vcMetricsURLs[1], "vc2-metrics", "", "second validator metrics URL (resolved from Kurtosis when empty)")
	fs.StringVar(&cfg.signerURL, "signer", "", "Clef HTTP URL (resolved from Kurtosis when empty)")

	signer := fs.String("signer-address", defaultSignerAddress, "expected account managed by the topology Clef")
	recipient := fs.String("recipient", defaultRecipient, "recipient for integrated signer transfers")
	fs.Uint64Var(&cfg.transferValue, "value", 1, "transfer value in planck")
	fs.DurationVar(&cfg.timeout, "timeout", 115*time.Minute, "maximum duration for the complete system check (also caps each eventual condition)")
	fs.DurationVar(&cfg.pollInterval, "poll", 2*time.Second, "poll interval")
	fs.DurationVar(&cfg.validatorPollInterval, "validator-poll", 30*time.Second, "poll interval for heavyweight validator metrics")
	fs.Uint64Var(&cfg.catchupBlocks, "catchup-blocks", 2, "additional blocks produced while participant two is stopped")
	fs.BoolVar(&cfg.skipRestarts, "skip-restarts", false, "legacy programmatic PhaseAll option; use -phase base for a non-disruptive compatibility check")
	fs.BoolVar(&cfg.requireFinalityAdvance, "require-finality-advance", true, "require a new finalized epoch after participant recovery")
	fs.BoolVar(&cfg.requireZeroDutyHistory, "require-zero-duty-history", false, "require validator failure counters to be zero before the check starts")
	if err := fs.Parse(args); err != nil {
		return config{}, err
	}
	if fs.NArg() != 0 {
		return config{}, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	var err error
	if cfg.signerAddress, err = common.NewAddressFromString(*signer); err != nil {
		return config{}, fmt.Errorf("invalid signer address: %w", err)
	}
	if cfg.recipient, err = common.NewAddressFromString(*recipient); err != nil {
		return config{}, fmt.Errorf("invalid recipient address: %w", err)
	}
	if cfg.feeRecipient, err = common.NewAddressFromString(expectedFeeRecipientAddress); err != nil {
		return config{}, fmt.Errorf("invalid expected fee-recipient address: %w", err)
	}
	if cfg.withdrawalRecipient, err = common.NewAddressFromString(expectedWithdrawalAddress); err != nil {
		return config{}, fmt.Errorf("invalid expected withdrawal address: %w", err)
	}
	if err := validateConfig(cfg); err != nil {
		return config{}, err
	}
	return cfg, nil
}

// ParseConfig parses the compatibility systemcheck flags into an importable
// suite configuration.
func ParseConfig(args []string) (Config, error) {
	cfg, err := parseConfig(args)
	if err != nil {
		return Config{}, err
	}
	if cfg.phase == string(PhaseAll) {
		return Config{}, fmt.Errorf("-phase %s is not a serialized compatibility phase; choose base, signer-restart, or participant-restart", PhaseAll)
	}
	return exportConfig(cfg), nil
}

// DefaultConfig returns fully validated suite defaults. The base phase is a
// safe concrete default for importable callers; the compatibility command
// still requires callers to select its checkpoint-owned serialized phase.
func DefaultConfig() Config {
	cfg, err := parseConfig([]string{"-phase", string(PhaseBase)})
	if err != nil {
		panic(fmt.Sprintf("invalid system suite defaults: %v", err))
	}
	return exportConfig(cfg)
}

func exportConfig(cfg config) Config {
	return Config{
		Enclave: cfg.enclave, Phase: Phase(cfg.phase), Checkpoint: cfg.checkpoint,
		ELServices: cfg.elServices, CLServices: cfg.clServices, VCServices: cfg.vcServices, SignerService: cfg.signerSvc,
		RPCURLs: cfg.rpcURLs, CLURLs: cfg.clURLs, VCMetricsURLs: cfg.vcMetricsURLs, SignerURL: cfg.signerURL,
		SignerAddress: cfg.signerAddress, Recipient: cfg.recipient, FeeRecipient: cfg.feeRecipient, WithdrawalRecipient: cfg.withdrawalRecipient,
		TransferValue: cfg.transferValue, Timeout: cfg.timeout, PollInterval: cfg.pollInterval, ValidatorPollInterval: cfg.validatorPollInterval,
		CatchupBlocks: cfg.catchupBlocks, SkipRestarts: cfg.skipRestarts, RequireFinalityAdvance: cfg.requireFinalityAdvance,
		RequireZeroDutyHistory: cfg.requireZeroDutyHistory,
	}
}

func (cfg Config) internal() (config, error) {
	result := config{
		enclave: cfg.Enclave, phase: string(cfg.Phase),
		elServices: cfg.ELServices, clServices: cfg.CLServices, vcServices: cfg.VCServices, signerSvc: cfg.SignerService,
		rpcURLs: cfg.RPCURLs, clURLs: cfg.CLURLs, vcMetricsURLs: cfg.VCMetricsURLs, signerURL: cfg.SignerURL,
		signerAddress: cfg.SignerAddress, recipient: cfg.Recipient, feeRecipient: cfg.FeeRecipient, withdrawalRecipient: cfg.WithdrawalRecipient,
		transferValue: cfg.TransferValue, timeout: cfg.Timeout, pollInterval: cfg.PollInterval, validatorPollInterval: cfg.ValidatorPollInterval,
		catchupBlocks: cfg.CatchupBlocks, skipRestarts: cfg.SkipRestarts, requireFinalityAdvance: cfg.RequireFinalityAdvance,
		requireZeroDutyHistory: cfg.RequireZeroDutyHistory,
	}
	if err := validateConfig(result); err != nil {
		return config{}, err
	}
	return result, nil
}

func validateConfig(cfg config) error {
	if cfg.phase == "" {
		return fmt.Errorf("-phase is required; choose base, signer-restart, or participant-restart")
	}
	switch cfg.phase {
	case string(PhaseAll), string(PhaseBase), string(PhaseSignerRestart), string(PhaseParticipantRestart):
	default:
		return fmt.Errorf("invalid phase %q; want all, base, signer-restart, or participant-restart", cfg.phase)
	}
	if cfg.phase != string(PhaseAll) && cfg.skipRestarts {
		return fmt.Errorf("-skip-restarts is unavailable to serialized compatibility phases; use -phase base for a non-disruptive compatibility check")
	}
	for label, address := range map[string]common.Address{
		"fee-recipient": cfg.feeRecipient,
		"withdrawal":    cfg.withdrawalRecipient,
	} {
		upperHalfNonzero := false
		for _, value := range address[:common.AddressLength/2] {
			upperHalfNonzero = upperHalfNonzero || value != 0
		}
		if !upperHalfNonzero {
			return fmt.Errorf("expected %s address does not exercise the upper 32 VM64 bytes", label)
		}
	}
	if cfg.feeRecipient == cfg.withdrawalRecipient {
		return fmt.Errorf("fee-recipient and withdrawal addresses must differ for independent accounting")
	}
	if cfg.signerAddress == cfg.recipient {
		return fmt.Errorf("recipient must differ from signer address")
	}
	if cfg.transferValue == 0 {
		return fmt.Errorf("transfer value must be positive")
	}
	if cfg.timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}
	if cfg.pollInterval <= 0 {
		return fmt.Errorf("poll interval must be positive")
	}
	if cfg.validatorPollInterval <= 0 {
		return fmt.Errorf("validator poll interval must be positive")
	}
	if cfg.validatorPollInterval >= validatorDutyObservationTimeout {
		return fmt.Errorf("validator poll interval must be shorter than %s", validatorDutyObservationTimeout)
	}
	if cfg.catchupBlocks == 0 {
		return fmt.Errorf("catchup-blocks must be positive")
	}
	if !cfg.skipRestarts && cfg.enclave == "" {
		return fmt.Errorf("enclave is required for restart checks")
	}
	return nil
}
