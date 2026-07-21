package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const exactSHA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type fakeRunner struct {
	run func(command string, args, environment []string) error
}

func (runner fakeRunner) Run(_ context.Context, command string, args, environment []string, _, _ io.Writer) error {
	return runner.run(command, args, environment)
}

func TestPrepareLoadsAndVerifiesEffectiveConfiguration(t *testing.T) {
	directory := t.TempDir()
	effective := filepath.Join(directory, "effective.yaml")
	metadata := filepath.Join(directory, "preparation.json")
	options := Options{
		RepoRoot: directory, NetworkParams: filepath.Join(directory, "input.yaml"), EffectiveOutput: effective,
		PreparationOutput: metadata, SourceSHA: exactSHA, CI: true,
	}
	runner := fakeRunner{run: func(command string, args, environment []string) error {
		if !strings.HasSuffix(command, "scripts/local_testnet/prepare_local_testnet.sh") {
			t.Fatalf("unexpected command %s", command)
		}
		if err := os.WriteFile(effective, []byte("participants: []\n"), 0o600); err != nil {
			return err
		}
		payload, err := os.ReadFile(effective)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(payload)
		preparation := validPreparation(effective, hex.EncodeToString(digest[:]))
		raw, err := json.Marshal(preparation)
		if err != nil {
			return err
		}
		return os.WriteFile(metadata, raw, 0o600)
	}}
	preparation, err := Prepare(context.Background(), runner, options, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if preparation.PackageLocator() != "github.com/theqrl/qrl-package@"+exactSHA {
		t.Fatalf("package locator = %s", preparation.PackageLocator())
	}
	serialized, err := preparation.SerializedParams()
	if err != nil || serialized != "participants: []\n" {
		t.Fatalf("serialized params = %q, err=%v", serialized, err)
	}
}

func TestPreparationRejectsDirtyCertifiedRunAndTampering(t *testing.T) {
	path := filepath.Join(t.TempDir(), "effective.yaml")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("one\n"))
	preparation := validPreparation(path, hex.EncodeToString(digest[:]))
	preparation.WorktreeDirty = true
	if err := preparation.Validate(exactSHA, true); err == nil || !strings.Contains(err.Error(), "dirty") {
		t.Fatalf("dirty certification error = %v", err)
	}
	preparation.WorktreeDirty = false
	if err := os.WriteFile(path, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preparation.Validate(exactSHA, false); err == nil || !strings.Contains(err.Error(), "digest") {
		t.Fatalf("tamper error = %v", err)
	}
}

func validPreparation(path, digest string) Preparation {
	images := make(map[string]Image)
	for _, role := range []string{"execution", "alltools", "consensus", "validator", "genesis"} {
		images[role] = Image{Name: role + ":test", ID: "sha256:" + strings.Repeat("b", 64)}
	}
	return Preparation{
		Schema: 1, SourceSHA: exactSHA,
		QRLPackage:      Source{Repository: "github.com/theqrl/qrl-package", Revision: exactSHA},
		Qrysm:           Source{Repository: "github.com/theqrl/qrysm", Revision: exactSHA},
		Generator:       Source{Repository: "github.com/theqrl/genesis", Revision: exactSHA},
		EffectiveParams: EffectiveParams{Path: path, SHA256: digest}, Images: images,
		Versions: map[string]string{"docker": "Docker 1", "kurtosis": "1.20.0", "yq": "v4", "go": "go1.26"},
	}
}
