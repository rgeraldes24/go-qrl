// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
)

func fixtureState(t *testing.T, networkDir string) State {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(networkDir, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	state := State{
		ParamsSHA256:  strings.Repeat("2", 64),
		SourceCommit:  strings.Repeat("b", 40),
		WalletAddress: "Q" + strings.Repeat("c", 128),
		Topology: topology.Snapshot{
			Execution: topology.ServiceIdentity{Role: "execution", Name: "el", UUID: strings.Repeat("d", 32), Image: "local/el:network"},
			Required: []topology.ServiceIdentity{
				{Role: "consensus", Name: "cl", UUID: strings.Repeat("e", 32), Image: "local/cl:pinned"},
				{Role: "validator", Name: "vc", UUID: strings.Repeat("f", 32), Image: "local/vc:pinned"},
			},
			RPC:       topology.Endpoint{PortID: "rpc", URL: "http://127.0.0.1:18545"},
			WebSocket: topology.Endpoint{PortID: "ws", URL: "ws://127.0.0.1:18546"}, GraphQL: "http://127.0.0.1:18545/graphql",
		},
		Images: []ImageIdentity{
			{Role: "consensus", Ref: "local/cl:pinned", ID: "sha256:" + strings.Repeat("1", 64), Labels: map[string]string{"revision": "cl"}},
			{Role: "execution", Ref: "local/el:network", ID: "sha256:" + strings.Repeat("2", 64), Labels: map[string]string{"revision": "el"}},
			{Role: "genesis", Ref: "local/genesis:pinned", ID: "sha256:" + strings.Repeat("3", 64), Labels: map[string]string{"revision": "genesis"}},
			{Role: "validator", Ref: "local/vc:pinned", ID: "sha256:" + strings.Repeat("4", 64), Labels: map[string]string{"revision": "vc"}},
		},
		BinarySHA256: strings.Repeat("5", 64),
		GenesisHash:  "0x" + strings.Repeat("6", 64),
	}
	return state
}

func TestStatePersistsOnlyAuthenticationBaseline(t *testing.T) {
	state := fixtureState(t, t.TempDir())
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]json.RawMessage
	if err := json.Unmarshal(payload, &persisted); err != nil {
		t.Fatal(err)
	}
	for _, omitted := range []string{"schema_version", "backend", "network_dir", "enclave", "package", "fingerprint", "created_at"} {
		if _, exists := persisted[omitted]; exists {
			t.Fatalf("ready state unexpectedly persists %q", omitted)
		}
	}
	var images []map[string]json.RawMessage
	if err := json.Unmarshal(persisted["images"], &images); err != nil {
		t.Fatal(err)
	}
	for _, image := range images {
		if _, exists := image["labels"]; exists {
			t.Fatal("ready state unexpectedly persists image labels")
		}
	}
}

func TestStateDerivesExecutionImageIdentityFromImages(t *testing.T) {
	state := fixtureState(t, t.TempDir())
	state.Topology.Execution.Image = "local/el:different"
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "execution image") {
		t.Fatalf("execution image mismatch error = %v", err)
	}
}

func TestStateStoreRejectsUnknownFieldsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	state := fixtureState(t, dir)
	if err := writeState(dir, state); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(statePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	decoded["unexpected"] = true
	data, _ = json.Marshal(decoded)
	if err := os.WriteFile(statePath(dir), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(dir); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error = %v", err)
	}

	target := filepath.Join(dir, "target.json")
	if err := os.WriteFile(target, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(statePath(dir)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, statePath(dir)); err != nil {
		t.Fatal(err)
	}
	if _, err := loadState(dir); err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestStateRequiresExactImageRoleSet(t *testing.T) {
	base := fixtureState(t, t.TempDir())
	for name, mutate := range map[string]func([]ImageIdentity) []ImageIdentity{
		"missing": func(images []ImageIdentity) []ImageIdentity { return images[:len(images)-1] },
		"extra": func(images []ImageIdentity) []ImageIdentity {
			return append(images, ImageIdentity{Role: "helper", Ref: "local/helper:pinned", ID: "sha256:" + strings.Repeat("7", 64)})
		},
		"duplicate": func(images []ImageIdentity) []ImageIdentity {
			images[len(images)-1] = images[0]
			return images
		},
	} {
		t.Run(name, func(t *testing.T) {
			images := mutate(append([]ImageIdentity(nil), base.Images...))
			state := base
			state.Images = images
			if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "image") {
				t.Fatalf("State.Validate error = %v", err)
			}
		})
	}
}

func TestOwnershipRecordSeparatesCreationIntentFromExactOwnership(t *testing.T) {
	networkDir := t.TempDir()
	enclave := kurtosis.EnclaveRef{Name: "e2e", UUID: strings.Repeat("a", 32), Owned: true}
	record := OwnershipRecord{
		NetworkDir: networkDir,
		Name:       enclave.Name,
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := record.OwnedEnclave(); err == nil || !strings.Contains(err.Error(), "exact UUID") {
		t.Fatalf("creation intent exact-ownership error = %v", err)
	}
	record.UUID = enclave.UUID
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	record.UUID = "not-a-uuid"
	if err := record.Validate(); err == nil || !strings.Contains(err.Error(), "invalid") {
		t.Fatalf("invalid enclave error = %v", err)
	}
}

func TestEnsureNetworkDirectoryCanonicalizesSymlinkedParent(t *testing.T) {
	realParent := t.TempDir()
	linkRoot := t.TempDir()
	link := filepath.Join(linkRoot, "linked-parent")
	if err := os.Symlink(realParent, link); err != nil {
		t.Fatal(err)
	}
	canonicalParent, err := filepath.EvalSymlinks(realParent)
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(canonicalParent, "network")
	got, err := ensureNetworkDirectory(filepath.Join(link, "network"))
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("canonical network directory = %q, want %q", got, want)
	}
	if info, err := os.Lstat(got); err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		t.Fatalf("canonical directory info=%v err=%v", info, err)
	}
}
