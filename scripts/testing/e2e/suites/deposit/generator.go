// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package deposit

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/theQRL/go-qrl/common"
)

const (
	deterministicMnemonic = "veto waiter rail aroma aunt chess fiend than sahara unwary punk dawn belong agent sane reefy loyal from judas clean paste rho madam poor pay convoy duty circa hybrid circus exempt splash"
	deterministicIndex    = 4096
	depositValidatorCount = 3
	publicKeyLength       = 2592
	withdrawalLength      = common.AddressLength
	signatureLength       = 4627
	depositAmountShor     = uint64(40_000_000_000_000)
)

var deterministicValidatorPublicKeyHashes = [...]common.Hash{
	common.HexToHash("0x2aced0d1a76093e16511e8019fa5620f7c1cb12b00893f22c1a7bb0e1dfdbf0d"),
	common.HexToHash("0xed52ad60415a2b2dd81d0c8ce0d84028b635d74444f33770b292e7ae872f6b39"),
	common.HexToHash("0xfaa96d3bcaa1df15cf82b19c9c8a20445e3fafbc951826adc384556b729a2683"),
}

type depositDataJSON struct {
	Pubkey                string `json:"pubkey"`
	Amount                uint64 `json:"amount"`
	WithdrawalCredentials string `json:"withdrawal_credentials"`
	DepositDataRoot       string `json:"deposit_data_root"`
	Signature             string `json:"signature"`
	MessageRoot           string `json:"message_root"`
	ForkVersion           string `json:"fork_version"`
	NetworkName           string `json:"network_name"`
	DepositCLIVersion     string `json:"deposit_cli_version"`
}

type depositData struct {
	publicKey             []byte
	withdrawalCredentials []byte
	amount                uint64
	signature             []byte
	root                  [32]byte
	messageRoot           [32]byte
}

type depositContractManifest struct {
	Schema            uint64 `json:"schema"`
	RuntimeCodeBytes  uint64 `json:"runtime_code_bytes"`
	RuntimeCodeSHA256 string `json:"runtime_code_sha256"`
	EmptyDepositRoot  string `json:"empty_deposit_root"`
	StorageSHA256     string `json:"storage_sha256"`
	StorageLayout     string `json:"storage_layout"`
}

func loadDepositContractManifest(ctx context.Context, runner commandRunner, imageID string) (depositContractManifest, error) {
	out, err := runner.run(ctx, "docker", "run", "--rm", "--pull=never", "--network=none",
		"--entrypoint", "cat", imageID, "/apps/el-gen/vm64-deposit-manifest.json")
	if err != nil {
		return depositContractManifest{}, fmt.Errorf("read VM64 deposit manifest from generator image %q: %w", imageID, err)
	}
	return parseDepositContractManifest([]byte(out))
}

func parseDepositContractManifest(raw []byte) (depositContractManifest, error) {
	var manifest depositContractManifest
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&manifest); err != nil {
		return depositContractManifest{}, fmt.Errorf("decode VM64 deposit manifest: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return depositContractManifest{}, fmt.Errorf("decode VM64 deposit manifest trailing data: %v", err)
	}
	if manifest.Schema != 1 {
		return depositContractManifest{}, fmt.Errorf("VM64 deposit manifest schema = %d, want 1", manifest.Schema)
	}
	if manifest.RuntimeCodeBytes == 0 {
		return depositContractManifest{}, fmt.Errorf("VM64 deposit manifest has empty runtime code")
	}
	for name, value := range map[string]string{
		"runtime_code_sha256": manifest.RuntimeCodeSHA256,
		"storage_sha256":      manifest.StorageSHA256,
	} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size {
			return depositContractManifest{}, fmt.Errorf("VM64 deposit manifest %s is not a 32-byte hex digest", name)
		}
	}
	if manifest.EmptyDepositRoot != emptyDepositRoot.Hex() {
		return depositContractManifest{}, fmt.Errorf("VM64 deposit manifest empty root = %s, want %s", manifest.EmptyDepositRoot, emptyDepositRoot.Hex())
	}
	if manifest.StorageLayout != "vm64-packed-bytes32-pairs-v1" {
		return depositContractManifest{}, fmt.Errorf("VM64 deposit manifest storage layout = %q, want vm64-packed-bytes32-pairs-v1", manifest.StorageLayout)
	}
	return manifest, nil
}

func generateDepositData(ctx context.Context, runner commandRunner, image string, withdrawal common.Address, forkVersion string) ([]depositData, string, error) {
	return generateDepositDataWithExpectedKeys(ctx, runner, image, withdrawal, forkVersion, deterministicValidatorPublicKeyHashes[:])
}

func generateDepositDataWithExpectedKeys(ctx context.Context, runner commandRunner, image string, withdrawal common.Address, forkVersion string, expectedPublicKeyHashes []common.Hash) ([]depositData, string, error) {
	inspect, err := runner.run(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		return nil, "", fmt.Errorf("inspect generator image %q: %w", image, err)
	}
	imageID := strings.TrimSpace(inspect)
	if !strings.HasPrefix(imageID, "sha256:") || len(imageID) != len("sha256:")+64 {
		return nil, "", fmt.Errorf("generator image %q resolved to invalid image ID %q", image, imageID)
	}

	tempDir, err := os.MkdirTemp("", "go-qrl-vm64-deposit-")
	if err != nil {
		return nil, "", fmt.Errorf("create deposit-data directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	canonicalDir, err := filepath.EvalSymlinks(tempDir)
	if err != nil {
		return nil, "", fmt.Errorf("resolve deposit-data directory: %w", err)
	}

	args := []string{
		"run", "--rm", "--pull=never", "--network=none",
		"--volume", canonicalDir + ":/out",
		// Run the immutable ID returned above, not the mutable tag, so a
		// concurrent retag cannot change the artifact generator after preflight.
		"--entrypoint", "/usr/local/bin/deposit", imageID,
		"new-seed",
		"--validator-start-index", fmt.Sprint(deterministicIndex),
		"--num-validators", fmt.Sprint(depositValidatorCount),
		"--folder", "/out",
		"--chain-name", "dev",
		"--execution-address", withdrawal.Hex(),
		"--mnemonic", deterministicMnemonic,
		"--lightkdf",
		"--keystore-password-file", "/dev/null",
	}
	out, err := runner.run(ctx, "docker", args...)
	if err != nil {
		if strings.Contains(strings.ToLower(out+err.Error()), "invalid address") {
			return nil, imageID, fmt.Errorf("generator image %q rejected a 64-byte withdrawal address; it embeds pre-VM64 Qrysm: %w", image, err)
		}
		return nil, imageID, fmt.Errorf("generate deterministic validator deposit data: %w", err)
	}

	paths, err := filepath.Glob(filepath.Join(canonicalDir, "deposit_data-*.json"))
	if err != nil {
		return nil, imageID, fmt.Errorf("locate generated deposit data: %w", err)
	}
	if len(paths) != 1 {
		return nil, imageID, fmt.Errorf("generator produced %d deposit-data files, want exactly one", len(paths))
	}
	raw, err := os.ReadFile(paths[0])
	if err != nil {
		return nil, imageID, fmt.Errorf("read generated deposit data: %w", err)
	}
	data, err := parseDepositData(raw, withdrawal, forkVersion)
	if err != nil {
		return nil, imageID, fmt.Errorf("generator image %q is not VM64-compatible: %w", image, err)
	}
	if err := verifyDeterministicValidatorPublicKeys(data, expectedPublicKeyHashes); err != nil {
		return nil, imageID, fmt.Errorf("generator image %q derivation drift: %w", image, err)
	}
	return data, imageID, nil
}

func verifyDeterministicValidatorPublicKeys(data []depositData, expected []common.Hash) error {
	if len(data) != len(expected) {
		return fmt.Errorf("validator public-key vector count = %d, want %d", len(data), len(expected))
	}
	for i := range data {
		digest := sha256.Sum256(data[i].publicKey)
		if common.Hash(digest) != expected[i] {
			return fmt.Errorf("validator index %d public-key sha256 = 0x%x, want %s", deterministicIndex+i, digest, expected[i].Hex())
		}
	}
	return nil
}

func parseDepositData(raw []byte, withdrawal common.Address, forkVersion string) ([]depositData, error) {
	var records []depositDataJSON
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&records); err != nil {
		return nil, fmt.Errorf("decode deposit-data JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return nil, fmt.Errorf("decode deposit-data JSON trailing data: %v", err)
	}
	if len(records) != depositValidatorCount {
		return nil, fmt.Errorf("deposit-data record count = %d, want %d", len(records), depositValidatorCount)
	}
	data := make([]depositData, len(records))
	seenPubkeys := make(map[string]struct{}, len(records))
	seenRoots := make(map[[32]byte]struct{}, len(records))
	for i, record := range records {
		parsed, err := parseDepositRecord(record, withdrawal, forkVersion)
		if err != nil {
			return nil, fmt.Errorf("deposit-data record %d (validator index %d): %w", i, deterministicIndex+i, err)
		}
		pubkey := string(parsed.publicKey)
		if _, exists := seenPubkeys[pubkey]; exists {
			return nil, fmt.Errorf("deposit-data record %d repeats a validator public key", i)
		}
		if _, exists := seenRoots[parsed.root]; exists {
			return nil, fmt.Errorf("deposit-data record %d repeats a deposit-data root", i)
		}
		seenPubkeys[pubkey] = struct{}{}
		seenRoots[parsed.root] = struct{}{}
		data[i] = parsed
	}
	return data, nil
}

func parseDepositRecord(record depositDataJSON, withdrawal common.Address, forkVersion string) (depositData, error) {
	pubkey, err := decodeFixedHex("pubkey", record.Pubkey, publicKeyLength)
	if err != nil {
		return depositData{}, err
	}
	credentials, err := decodeFixedHex("withdrawal_credentials", record.WithdrawalCredentials, withdrawalLength)
	if err != nil {
		return depositData{}, err
	}
	if !bytes.Equal(credentials, withdrawal.Bytes()) {
		return depositData{}, fmt.Errorf("withdrawal credentials = 0x%x, want the full address 0x%x", credentials, withdrawal.Bytes())
	}
	if !upperHalfNonzero(common.BytesToAddress(credentials)) {
		return depositData{}, fmt.Errorf("withdrawal credentials lost the nonzero upper 32 bytes")
	}
	signature, err := decodeFixedHex("signature", record.Signature, signatureLength)
	if err != nil {
		return depositData{}, err
	}
	rootBytes, err := decodeFixedHex("deposit_data_root", record.DepositDataRoot, 32)
	if err != nil {
		return depositData{}, err
	}
	messageRootBytes, err := decodeFixedHex("message_root", record.MessageRoot, 32)
	if err != nil {
		return depositData{}, err
	}
	if record.Amount != depositAmountShor {
		return depositData{}, fmt.Errorf("amount = %d Shor, want %d", record.Amount, depositAmountShor)
	}
	if !strings.EqualFold(record.ForkVersion, forkVersion) {
		return depositData{}, fmt.Errorf("fork version = %q, want %q", record.ForkVersion, forkVersion)
	}
	if record.NetworkName != "dev" {
		return depositData{}, fmt.Errorf("network name = %q, want dev", record.NetworkName)
	}
	var root, messageRoot [32]byte
	copy(root[:], rootBytes)
	copy(messageRoot[:], messageRootBytes)
	return depositData{
		publicKey:             pubkey,
		withdrawalCredentials: credentials,
		amount:                record.Amount,
		signature:             signature,
		root:                  root,
		messageRoot:           messageRoot,
	}, nil
}

func decodeFixedHex(name, value string, size int) ([]byte, error) {
	if !strings.HasPrefix(value, "0x") {
		return nil, fmt.Errorf("%s is not 0x-prefixed", name)
	}
	decoded, err := hex.DecodeString(value[2:])
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", name, err)
	}
	if len(decoded) != size {
		return nil, fmt.Errorf("%s length = %d, want %d", name, len(decoded), size)
	}
	return decoded, nil
}
