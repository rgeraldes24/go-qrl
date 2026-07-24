// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
)

func fixtureState(t *testing.T, networkDir string) State {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(networkDir, "private"), 0o700); err != nil {
		t.Fatal(err)
	}
	state := State{
		SchemaVersion: StateSchemaVersion, Backend: BackendKurtosis,
		NetworkDir: networkDir,
		Enclave:    kurtosis.EnclaveRef{Name: "e2e", UUID: strings.Repeat("a", 32), Owned: true},
		Package:    PackageIdentity{Locator: "example/package@" + strings.Repeat("a", 40), ID: "example/package", ParamsSHA256: strings.Repeat("2", 64)},
		Source:     SourceIdentity{Commit: strings.Repeat("b", 40)},
		Wallet:     WalletIdentity{Address: "Q" + strings.Repeat("c", 128)},
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
		Execution: ExecutionIdentity{BinaryPath: "/usr/local/bin/gqrl", BinarySHA256: strings.Repeat("5", 64)},
		Chain:     ChainIdentity{ChainID: "0x539", GenesisHash: "0x" + strings.Repeat("6", 64)}, CreatedAt: time.Unix(10, 0).UTC(),
	}
	var err error
	state.Fingerprint, err = state.IdentityFingerprint()
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func TestIdentityFingerprintBindsImmutableFieldsAndIgnoresCreationTime(t *testing.T) {
	state := fixtureState(t, t.TempDir())
	first := state.Fingerprint
	createdLater := state
	createdLater.CreatedAt = time.Unix(100, 0).UTC()
	second, err := createdLater.IdentityFingerprint()
	if err != nil || second != first {
		t.Fatalf("creation-time-only fingerprint = %q, %v; want %q", second, err, first)
	}

	mutations := map[string]func(*State){
		"enclave UUID":    func(value *State) { value.Enclave.UUID = strings.Repeat("9", 32) },
		"service UUID":    func(value *State) { value.Topology.Execution.UUID = strings.Repeat("8", 32) },
		"image ID":        func(value *State) { value.Images[1].ID = "sha256:" + strings.Repeat("7", 64) },
		"package locator": func(value *State) { value.Package.Locator = "example/other@" + strings.Repeat("a", 40) },
		"package ID":      func(value *State) { value.Package.ID = "example/other" },
		"package params":  func(value *State) { value.Package.ParamsSHA256 = strings.Repeat("8", 64) },
		"source commit":   func(value *State) { value.Source.Commit = strings.Repeat("9", 40) },
		"genesis":         func(value *State) { value.Chain.GenesisHash = "0x" + strings.Repeat("a", 64) },
		"wallet":          func(value *State) { value.Wallet.Address = "Q" + strings.Repeat("d", 128) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			copy := state
			copy.Images = append([]ImageIdentity(nil), state.Images...)
			copy.Topology.Required = append([]topology.ServiceIdentity(nil), state.Topology.Required...)
			mutate(&copy)
			fingerprint, err := copy.IdentityFingerprint()
			if err != nil || fingerprint == first {
				t.Fatalf("fingerprint = %q, %v", fingerprint, err)
			}
		})
	}
}

func TestStatePersistsOnlyEssentialIdentityFields(t *testing.T) {
	state := fixtureState(t, t.TempDir())
	payload, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	var persisted map[string]json.RawMessage
	if err := json.Unmarshal(payload, &persisted); err != nil {
		t.Fatal(err)
	}
	for section, wantKeys := range map[string][]string{
		"wallet":    {"address"},
		"execution": {"binary_path", "binary_sha256"},
		"source":    {"commit"},
	} {
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(persisted[section], &fields); err != nil {
			t.Fatal(err)
		}
		if len(fields) != len(wantKeys) {
			t.Fatalf("%s persisted fields = %v, want %v", section, fields, wantKeys)
		}
		for _, key := range wantKeys {
			if _, exists := fields[key]; !exists {
				t.Fatalf("%s is missing persisted field %q", section, key)
			}
		}
	}
}

func TestStateDerivesExecutionImageIdentityFromImages(t *testing.T) {
	state := fixtureState(t, t.TempDir())
	state.Topology.Execution.Image = "local/el:different"
	state.Fingerprint, _ = state.IdentityFingerprint()
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "execution image") {
		t.Fatalf("execution image mismatch error = %v", err)
	}
}

func TestStateStoreRejectsUnknownFieldsAndSymlinks(t *testing.T) {
	dir := t.TempDir()
	state := fixtureState(t, dir)
	if err := writeState(state); err != nil {
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

func TestStateRejectsUnknownBackend(t *testing.T) {
	state := fixtureState(t, t.TempDir())
	state.Backend = "unknown"
	state.Fingerprint, _ = state.IdentityFingerprint()
	if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "backend") {
		t.Fatalf("backend error = %v", err)
	}
}

func TestStateRequiresBothPackageLocatorAndRetainedID(t *testing.T) {
	base := fixtureState(t, t.TempDir())
	for name, mutate := range map[string]func(*State){
		"locator": func(state *State) { state.Package.Locator = "" },
		"ID":      func(state *State) { state.Package.ID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			state := base
			mutate(&state)
			state.Fingerprint, _ = state.IdentityFingerprint()
			if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "package identity") {
				t.Fatalf("missing package %s error = %v", name, err)
			}
		})
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
			state.Fingerprint, _ = state.IdentityFingerprint()
			if err := state.Validate(); err == nil || !strings.Contains(err.Error(), "image") {
				t.Fatalf("State.Validate error = %v", err)
			}
		})
	}
}

func TestOwnershipRecordSeparatesCreationIntentFromExactOwnership(t *testing.T) {
	state := fixtureState(t, t.TempDir())
	record := OwnershipRecord{
		SchemaVersion: OwnershipSchemaVersion,
		NetworkDir:    state.NetworkDir,
		RequestedName: state.Enclave.Name,
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := record.OwnedEnclave(); err == nil || !strings.Contains(err.Error(), "exact UUID") {
		t.Fatalf("creation intent exact-ownership error = %v", err)
	}
	record.Enclave = &state.Enclave
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	record.Enclave.Owned = false
	if err := record.Validate(); err == nil || !strings.Contains(err.Error(), "not owned") {
		t.Fatalf("unowned enclave error = %v", err)
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
