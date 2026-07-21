// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package deposit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
)

func validDepositJSON(t *testing.T, withdrawal common.Address) []byte {
	t.Helper()
	records := make([]depositDataJSON, depositValidatorCount)
	for i := range records {
		records[i] = depositDataJSON{
			Pubkey:                "0x" + strings.Repeat(fmt.Sprintf("%02x", 0x11+i), publicKeyLength),
			Amount:                depositAmountShor,
			WithdrawalCredentials: "0x" + hex.EncodeToString(withdrawal.Bytes()),
			DepositDataRoot:       "0x" + strings.Repeat(fmt.Sprintf("%02x", 0x22+i), 32),
			Signature:             "0x" + strings.Repeat(fmt.Sprintf("%02x", 0x33+i), signatureLength),
			MessageRoot:           "0x" + strings.Repeat(fmt.Sprintf("%02x", 0x44+i), 32),
			ForkVersion:           defaultForkVersion,
			NetworkName:           "dev",
			DepositCLIVersion:     "vm64-test",
		}
	}
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestParseDepositDataPreservesFullWithdrawalAddress(t *testing.T) {
	withdrawal := common.MustParseAddress(defaultWithdrawal)
	data, err := parseDepositData(validDepositJSON(t, withdrawal), withdrawal, defaultForkVersion)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != depositValidatorCount {
		t.Fatalf("validator count = %d, want %d", len(data), depositValidatorCount)
	}
	for i := range data {
		if len(data[i].publicKey) != publicKeyLength || len(data[i].signature) != signatureLength {
			t.Fatalf("validator %d sizes: pubkey=%d signature=%d", i, len(data[i].publicKey), len(data[i].signature))
		}
		if got := common.BytesToAddress(data[i].withdrawalCredentials); got != withdrawal {
			t.Fatalf("validator %d withdrawal credentials = %s, want %s", i, got.Hex(), withdrawal.Hex())
		}
		if !upperHalfNonzero(common.BytesToAddress(data[i].withdrawalCredentials)) {
			t.Fatalf("validator %d withdrawal credentials lost upper-half coverage", i)
		}
	}
}

func TestParseDepositContractManifest(t *testing.T) {
	raw := []byte(`{"schema":1,"runtime_code_bytes":7970,"runtime_code_sha256":"` + strings.Repeat("ab", 32) + `","empty_deposit_root":"` + emptyDepositRoot.Hex() + `","storage_sha256":"` + strings.Repeat("cd", 32) + `","storage_layout":"vm64-packed-bytes32-pairs-v1"}`)
	manifest, err := parseDepositContractManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.RuntimeCodeBytes != 7970 || manifest.RuntimeCodeSHA256 != strings.Repeat("ab", 32) {
		t.Fatalf("manifest = %+v", manifest)
	}

	badRoot := strings.Replace(string(raw), emptyDepositRoot.Hex(), "0x"+strings.Repeat("00", 32), 1)
	if _, err := parseDepositContractManifest([]byte(badRoot)); err == nil || !strings.Contains(err.Error(), "empty root") {
		t.Fatalf("bad empty root error = %v", err)
	}
	unknown := strings.Replace(string(raw), `"schema":1`, `"schema":1,"unexpected":true`, 1)
	if _, err := parseDepositContractManifest([]byte(unknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("manifest schema drift error = %v", err)
	}
}

func TestExpectedDepositStorageMatchesGeneratorManifest(t *testing.T) {
	storage, digest, err := expectedDepositStorage()
	if err != nil {
		t.Fatal(err)
	}
	if len(storage) != depositZeroHashesSlotCount {
		t.Fatalf("storage slots = %d, want %d", len(storage), depositZeroHashesSlotCount)
	}
	if got, want := digest, "cb963975975c3e1ee9e66f0cb029100dcaf584a17be8cca80de9692cf94ba4f3"; got != want {
		t.Fatalf("storage digest = %s, want %s", got, want)
	}
	first := storage[storageKey(depositZeroHashesFirstSlot).Hex()]
	if got, want := first, "0xf5a5fd42d16a20302798ef6ed309979b43003d2320d9f0e8ea9831a92759fb4b"+strings.Repeat("00", 32); got != want {
		t.Fatalf("first packed storage word = %s, want %s", got, want)
	}
	if _, exists := storage[storageKey(legacyZeroHashesFirstSlot).Hex()]; exists {
		t.Fatalf("legacy slot %s unexpectedly populated", storageKey(legacyZeroHashesFirstSlot).Hex())
	}
}

func TestParseDepositDataRejectsPreVM64Credentials(t *testing.T) {
	withdrawal := common.MustParseAddress(defaultWithdrawal)
	var records []depositDataJSON
	if err := json.Unmarshal(validDepositJSON(t, withdrawal), &records); err != nil {
		t.Fatal(err)
	}
	records[0].WithdrawalCredentials = "0x" + strings.Repeat("aa", 32)
	raw, err := json.Marshal(records)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseDepositData(raw, withdrawal, defaultForkVersion); err == nil || !strings.Contains(err.Error(), "length = 32, want 64") {
		t.Fatalf("legacy withdrawal credentials error = %v", err)
	}
}

func TestParseDepositDataRejectsSchemaDriftAndWrongAddress(t *testing.T) {
	withdrawal := common.MustParseAddress(defaultWithdrawal)
	raw := strings.Replace(string(validDepositJSON(t, withdrawal)), `"deposit_cli_version":"vm64-test"`, `"deposit_cli_version":"vm64-test","unexpected":true`, 1)
	if _, err := parseDepositData([]byte(raw), withdrawal, defaultForkVersion); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("schema drift error = %v", err)
	}

	other := withdrawal
	other[0] ^= 0xff
	if _, err := parseDepositData(validDepositJSON(t, other), withdrawal, defaultForkVersion); err == nil || !strings.Contains(err.Error(), "want the full address") {
		t.Fatalf("wrong withdrawal address error = %v", err)
	}
}

func TestParseDepositDataRejectsWrongCountDuplicatesAndTrailingData(t *testing.T) {
	withdrawal := common.MustParseAddress(defaultWithdrawal)
	var records []depositDataJSON
	if err := json.Unmarshal(validDepositJSON(t, withdrawal), &records); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(records[:len(records)-1])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseDepositData(raw, withdrawal, defaultForkVersion); err == nil || !strings.Contains(err.Error(), "record count") {
		t.Fatalf("wrong record count error = %v", err)
	}

	duplicatePubkey := append([]depositDataJSON(nil), records...)
	duplicatePubkey[1].Pubkey = duplicatePubkey[0].Pubkey
	raw, err = json.Marshal(duplicatePubkey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseDepositData(raw, withdrawal, defaultForkVersion); err == nil || !strings.Contains(err.Error(), "repeats a validator public key") {
		t.Fatalf("duplicate public key error = %v", err)
	}

	duplicateRoot := append([]depositDataJSON(nil), records...)
	duplicateRoot[1].DepositDataRoot = duplicateRoot[0].DepositDataRoot
	raw, err = json.Marshal(duplicateRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseDepositData(raw, withdrawal, defaultForkVersion); err == nil || !strings.Contains(err.Error(), "repeats a deposit-data root") {
		t.Fatalf("duplicate root error = %v", err)
	}

	if _, err := parseDepositData(append(validDepositJSON(t, withdrawal), []byte(" true")...), withdrawal, defaultForkVersion); err == nil || !strings.Contains(err.Error(), "trailing data") {
		t.Fatalf("trailing data error = %v", err)
	}
}

func TestGenerateDepositDataRunsInspectedImageIDOffline(t *testing.T) {
	withdrawal := common.MustParseAddress(defaultWithdrawal)
	raw := validDepositJSON(t, withdrawal)
	parsed, err := parseDepositData(raw, withdrawal, defaultForkVersion)
	if err != nil {
		t.Fatal(err)
	}
	expectedHashes := make([]common.Hash, len(parsed))
	for i := range parsed {
		expectedHashes[i] = common.Hash(sha256.Sum256(parsed[i].publicKey))
	}
	runner := &fakeGeneratorRunner{
		t:       t,
		imageID: "sha256:" + strings.Repeat("ab", 32),
		raw:     raw,
	}
	data, imageID, err := generateDepositDataWithExpectedKeys(t.Context(), runner, "mutable/generator:tag", withdrawal, defaultForkVersion, expectedHashes)
	if err != nil {
		t.Fatal(err)
	}
	if imageID != runner.imageID || !runner.ranGenerator {
		t.Fatalf("image ID/run = %q/%t, want %q/true", imageID, runner.ranGenerator, runner.imageID)
	}
	if len(data) != depositValidatorCount {
		t.Fatalf("generated validators = %d, want %d", len(data), depositValidatorCount)
	}
	for i := range data {
		if !upperHalfNonzero(common.BytesToAddress(data[i].withdrawalCredentials)) {
			t.Fatalf("generated validator %d withdrawal credentials do not cover the upper half", i)
		}
	}
}

func TestVerifyDeterministicValidatorPublicKeysRejectsIndexDrift(t *testing.T) {
	withdrawal := common.MustParseAddress(defaultWithdrawal)
	data, err := parseDepositData(validDepositJSON(t, withdrawal), withdrawal, defaultForkVersion)
	if err != nil {
		t.Fatal(err)
	}
	expected := make([]common.Hash, len(data))
	for i := range data {
		expected[i] = common.Hash(sha256.Sum256(data[i].publicKey))
	}
	if err := verifyDeterministicValidatorPublicKeys(data, expected); err != nil {
		t.Fatal(err)
	}
	expected[1][0] ^= 1
	if err := verifyDeterministicValidatorPublicKeys(data, expected); err == nil || !strings.Contains(err.Error(), "validator index 4097") {
		t.Fatalf("derivation drift error = %v", err)
	}
}

func TestGeneratedDepositDataFileAgainstPinnedVectors(t *testing.T) {
	path := os.Getenv("VM64_DEPOSIT_DATA_FILE")
	if path == "" {
		t.Skip("set VM64_DEPOSIT_DATA_FILE to validate output from the pinned generator")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	withdrawal := common.MustParseAddress(defaultWithdrawal)
	data, err := parseDepositData(raw, withdrawal, defaultForkVersion)
	if err != nil {
		t.Fatal(err)
	}
	if err := verifyDeterministicValidatorPublicKeys(data, deterministicValidatorPublicKeyHashes[:]); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateDepositDataExplainsLegacyImageRejection(t *testing.T) {
	withdrawal := common.MustParseAddress(defaultWithdrawal)
	runner := &fakeGeneratorRunner{
		t:         t,
		imageID:   "sha256:" + strings.Repeat("cd", 32),
		legacyErr: true,
	}
	_, _, err := generateDepositData(t.Context(), runner, "legacy/generator:tag", withdrawal, defaultForkVersion)
	if err == nil || !strings.Contains(err.Error(), "pre-VM64 Qrysm") {
		t.Fatalf("legacy generator error = %v", err)
	}
}

type fakeGeneratorRunner struct {
	t            *testing.T
	imageID      string
	raw          []byte
	legacyErr    bool
	ranGenerator bool
}

func (runner *fakeGeneratorRunner) run(_ context.Context, name string, args ...string) (string, error) {
	runner.t.Helper()
	if name != "docker" {
		return "", fmt.Errorf("unexpected command %q", name)
	}
	if len(args) >= 2 && args[0] == "image" && args[1] == "inspect" {
		return runner.imageID + "\n", nil
	}
	if len(args) == 0 || args[0] != "run" {
		return "", fmt.Errorf("unexpected docker arguments: %v", args)
	}
	runner.ranGenerator = true
	joined := strings.Join(args, " ")
	joinedLower := strings.ToLower(joined)
	for _, required := range []string{"--pull=never", "--network=none", runner.imageID, defaultWithdrawal, "--num-validators " + fmt.Sprint(depositValidatorCount)} {
		if !strings.Contains(joinedLower, strings.ToLower(required)) {
			return "", fmt.Errorf("docker run is missing %q: %v", required, args)
		}
	}
	if strings.Contains(joined, "mutable/generator:tag") || strings.Contains(joined, "legacy/generator:tag") {
		return "", fmt.Errorf("docker run used a mutable tag: %v", args)
	}
	if runner.legacyErr {
		return "invalid address", errors.New("exit status 1")
	}
	var mount string
	for i, arg := range args {
		if arg == "--volume" && i+1 < len(args) {
			mount = args[i+1]
			break
		}
	}
	hostDir, _, ok := strings.Cut(mount, ":")
	if !ok || hostDir == "" {
		return "", fmt.Errorf("invalid output mount %q", mount)
	}
	if err := os.WriteFile(filepath.Join(hostDir, "deposit_data-test.json"), runner.raw, 0o600); err != nil {
		return "", err
	}
	return "generated", nil
}
