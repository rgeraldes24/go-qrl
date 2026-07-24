package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestFindRepoRootFromNestedDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.invalid/go-qrl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "scripts", "testing", "e2e", "suites", "goabi")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := FindRepoRoot(nested)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.Abs(root)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("repository root = %s, want %s", got, want)
	}
}

func TestFindRepoRootRequiresModuleAndE2EDirectory(t *testing.T) {
	start := t.TempDir()
	if err := os.WriteFile(filepath.Join(start, "go.mod"), []byte("module example.invalid/not-go-qrl\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := FindRepoRoot(start)
	if err == nil || !strings.Contains(err.Error(), "could not find") {
		t.Fatalf("FindRepoRoot error = %v", err)
	}
}

func TestValidateE2EOnlyTreeDrift(t *testing.T) {
	tests := map[string]struct {
		allowed bool
		mutate  func(*testing.T, string)
	}{
		"clean": {allowed: true, mutate: func(*testing.T, string) {}},
		"tracked staged and untracked E2E": {
			allowed: true,
			mutate: func(t *testing.T, root string) {
				t.Helper()
				writeSourceTestFile(t, root, "scripts/testing/e2e/runner.go", "package e2e\n// repaired\n")
				writeSourceTestFile(t, root, "scripts/testing/e2e/staged_test.go", "package e2e\n")
				runSourceGit(t, root, "add", "scripts/testing/e2e/staged_test.go")
				writeSourceTestFile(t, root, "scripts/testing/e2e/untracked.json", "{}\n")
			},
		},
		"tracked accounts ABI": {
			mutate: func(t *testing.T, root string) {
				writeSourceTestFile(t, root, "accounts/abi/runtime.go", "package abi\n// changed\n")
			},
		},
		"staged network build script": {
			mutate: func(t *testing.T, root string) {
				writeSourceTestFile(t, root, "scripts/local_testnet/build.sh", "# changed\n")
				runSourceGit(t, root, "add", "scripts/local_testnet/build.sh")
			},
		},
		"untracked root test": {
			mutate: func(t *testing.T, root string) {
				writeSourceTestFile(t, root, "untracked_test.go", "package root\n")
			},
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			root := newSourceTestRepository(t)
			test.mutate(t, root)
			err := ValidateE2EOnlyTreeDrift(context.Background(), nil, root)
			if test.allowed && err != nil {
				t.Fatalf("allowed E2E-only drift rejected: %v", err)
			}
			if !test.allowed && err == nil {
				t.Fatal("runtime-affecting drift was accepted")
			}
		})
	}
}

func newSourceTestRepository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	runSourceGit(t, root, "init", "-q")
	runSourceGit(t, root, "config", "user.email", "e2e@example.invalid")
	runSourceGit(t, root, "config", "user.name", "E2E")
	for path, contents := range map[string]string{
		"accounts/abi/runtime.go":              "package abi\n",
		"scripts/local_testnet/build.sh":       "#!/usr/bin/env bash\n",
		"scripts/testing/e2e/runner.go":        "package e2e\n",
		"scripts/testing/e2e/existing_test.go": "package e2e\n",
	} {
		writeSourceTestFile(t, root, path, contents)
	}
	runSourceGit(t, root, "add", ".")
	runSourceGit(t, root, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial")
	return root
}

func writeSourceTestFile(t *testing.T, root, relative, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func runSourceGit(t *testing.T, root string, arguments ...string) {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", arguments, err, output)
	}
}
