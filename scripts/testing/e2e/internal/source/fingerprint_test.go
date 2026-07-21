package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestTreeIDChangesWithUntrackedContent(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{{"init"}, {"config", "user.email", "e2e@example.invalid"}, {"config", "user.name", "E2E"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	tracked := filepath.Join(root, "tracked")
	if err := os.WriteFile(tracked, []byte("tracked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "tracked"}, {"commit", "-m", "initial"}} {
		command := exec.Command("git", args...)
		command.Dir = root
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	first, err := TreeID(context.Background(), nil, root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "untracked"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := TreeID(context.Background(), nil, root)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("tree fingerprint ignored untracked content")
	}
	if err := os.WriteFile(filepath.Join(root, "untracked"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	third, err := TreeID(context.Background(), nil, root)
	if err != nil {
		t.Fatal(err)
	}
	if second == third {
		t.Fatal("tree fingerprint ignored changed untracked content")
	}
}
