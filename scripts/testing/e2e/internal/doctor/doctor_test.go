package doctor

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	paths   map[string]string
	outputs map[string][]byte
	errors  map[string]error
}

func (runner fakeRunner) LookPath(name string) (string, error) {
	path, ok := runner.paths[name]
	if !ok {
		return "", errors.New("missing")
	}
	return path, nil
}

func (runner fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	key := name + " " + strings.Join(args, " ")
	return runner.outputs[key], runner.errors[key]
}

func TestRunRequiresExactCLIAndEngineVersions(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	params := filepath.Join(root, "params.yaml")
	if err := os.WriteFile(params, []byte("participants: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	runner := fakeRunner{
		paths: map[string]string{"git": "git", "kurtosis": "kurtosis"},
		outputs: map[string][]byte{
			"git -C " + root + " rev-parse HEAD": []byte(sha + "\n"),
			"kurtosis version":                   []byte("CLI Version:   1.20.0\n"),
			"kurtosis engine status":             []byte("A Kurtosis engine is running with the following info:\nVersion:   1.19.0\n"),
		},
		errors: map[string]error{},
	}
	report := Run(context.Background(), runner, Options{
		RepoRoot: root, SourceSHA: sha, NetworkParams: params, RequireEngine: true,
		RequiredTools: []string{"git"}, KurtosisBinary: "kurtosis",
	})
	err := report.Validate()
	if err == nil || !strings.Contains(err.Error(), "1.19.0") {
		t.Fatalf("version error = %v", err)
	}
	runner.outputs["kurtosis engine status"] = []byte("A Kurtosis engine is running with the following info:\nVersion:   1.20.0\n")
	report = Run(context.Background(), runner, Options{
		RepoRoot: root, SourceSHA: sha, NetworkParams: params, RequireEngine: true,
		RequiredTools: []string{"git"}, KurtosisBinary: "kurtosis",
	})
	if err := report.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRunRejectsUnpinnedImagesAndMissingBinary(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	params := filepath.Join(root, "params.yaml")
	if err := os.WriteFile(params, []byte("participants: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	runner := fakeRunner{
		paths: map[string]string{"git": "git", "kurtosis": "kurtosis"},
		outputs: map[string][]byte{
			"git -C " + root + " rev-parse HEAD": []byte(sha + "\n"),
			"kurtosis version":                   []byte("CLI Version: 1.20.0\n"),
			"kurtosis engine status":             []byte("Version: 1.20.0\n"),
		},
		errors: map[string]error{},
	}
	report := Run(context.Background(), runner, Options{
		RepoRoot: root, SourceSHA: sha, NetworkParams: params, RequireEngine: true,
		RequiredTools: []string{"git"}, KurtosisBinary: "kurtosis",
		PinnedImages: []string{"image:latest"}, BuiltBinaries: []string{filepath.Join(root, "missing")},
	})
	err := report.Validate()
	if err == nil || !strings.Contains(err.Error(), "not pinned") || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("doctor error = %v", err)
	}
}
