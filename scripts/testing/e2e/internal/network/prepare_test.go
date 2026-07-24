// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltInNetworkParameters(t *testing.T) {
	address := "Q" + strings.Repeat("a", 128)
	refs := map[string]string{
		"execution": "local/el:e2e-a",
		"consensus": "local/cl:e2e-a",
		"validator": "local/vc:e2e-a",
		"genesis":   "local/genesis:e2e-a",
	}
	parameters, err := effectiveParameters(address, refs)
	if err != nil {
		t.Fatal(err)
	}
	var effective map[string]any
	if err := json.Unmarshal([]byte(parameters), &effective); err != nil {
		t.Fatal(err)
	}
	participants := effective["participants"].([]any)
	participant := participants[0].(map[string]any)
	for role, key := range map[string]string{"execution": "el_image", "consensus": "cl_image", "validator": "vc_image"} {
		if participant[key] != refs[role] {
			t.Fatalf("%s image = %v, want %q", role, participant[key], refs[role])
		}
	}
	if len(participants) != 1 || participant["count"] != float64(1) || participant["use_remote_signer"] != false {
		t.Fatalf("effective participant = %#v", participant)
	}
	network := effective["network_params"].(map[string]any)
	prefund := network["prefunded_accounts"].(map[string]any)[address].(map[string]any)
	if network["network_id"] != "1337" || network["withdrawal_address"] != address || prefund["balance"] != prefundBalance {
		t.Fatalf("effective network parameters = %#v", network)
	}
	generator := effective["qrl_genesis_generator_params"].(map[string]any)
	if generator["image"] != refs["genesis"] {
		t.Fatalf("genesis image = %v", generator["image"])
	}
	if packageLocator != "github.com/rgeraldes24/qrl-package@1f31cd03dbe2061225701ea79d956cfeceaf91db" ||
		packageID != "github.com/rgeraldes24/qrl-package" {
		t.Fatalf("built-in package identity = %q / %q", packageLocator, packageID)
	}
}

func TestPrepareNetworkRejectsWalletSeedInParameters(t *testing.T) {
	networkDir, err := ensureNetworkDirectory(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	address := "Q" + strings.Repeat("a", 128)
	if err := os.WriteFile(walletSeedPath(networkDir), []byte(address+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = prepareNetwork(
		context.Background(),
		nil,
		StartRequest{NetworkDir: networkDir, BuildTool: "unused"},
		SourceIdentity{Commit: strings.Repeat("b", 40)},
		WalletIdentity{Address: address},
		nil,
		nil,
	)
	if err == nil || !strings.Contains(err.Error(), "containing wallet seed") {
		t.Fatalf("wallet-seed leakage error = %v", err)
	}
}

func TestIsolatedLocalImageRefsBindCommitAndNetworkDirectory(t *testing.T) {
	source := SourceIdentity{Commit: strings.Repeat("a", 40)}
	firstDir, secondDir := t.TempDir(), t.TempDir()
	first, err := isolatedLocalImageRefs(localImageTemplates, source, firstDir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := isolatedLocalImageRefs(localImageTemplates, source, firstDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := isolatedLocalImageRefs(localImageTemplates, source, secondDir)
	if err != nil {
		t.Fatal(err)
	}
	otherCommit, err := isolatedLocalImageRefs(
		localImageTemplates,
		SourceIdentity{Commit: strings.Repeat("b", 40)},
		firstDir,
	)
	if err != nil {
		t.Fatal(err)
	}
	for role := range requiredImageRoles {
		if first[role] != again[role] || first[role] == second[role] ||
			first[role] == otherCommit[role] ||
			strings.Contains(first[role], "@") || !strings.Contains(first[role], ":e2e-") {
			t.Fatalf(
				"%s isolated refs: first=%q again=%q second=%q other commit=%q",
				role,
				first[role],
				again[role],
				second[role],
				otherCommit[role],
			)
		}
	}
}

func TestBuildEnvironmentContainsDerivedRefsAndImmutablePins(t *testing.T) {
	refs := map[string]string{
		"execution": "local/el:e2e-a", "consensus": "local/cl:e2e-a",
		"validator": "local/vc:e2e-a", "genesis": "local/genesis:e2e-a",
	}
	environment, err := isolatedImageBuildEnvironment("/bin", refs)
	if err != nil {
		t.Fatal(err)
	}
	environment = append(environment, pinnedBuildEnvironment()...)
	joined := strings.Join(environment, "\n")
	for _, needle := range []string{
		"PATH=/bin",
		"E2E_LOCAL_EL_IMAGE=" + refs["execution"],
		"E2E_PINNED_QRYSM_GIT_COMMIT=" + qrysmCommit,
		"E2E_PINNED_GENERATOR_GIT_COMMIT=" + genesisCommit,
		"E2E_PINNED_ALPINE_RUNTIME_IMAGE=alpine:latest@sha256:",
	} {
		if !strings.Contains(joined, needle) {
			t.Fatalf("build environment missing %q: %v", needle, environment)
		}
	}
}

func networkTestRepoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../../../../..")
	if err != nil {
		t.Fatal(err)
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
