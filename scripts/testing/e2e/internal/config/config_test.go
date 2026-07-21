package config

import (
	"strings"
	"testing"
	"time"
)

func TestRunConfigValidation(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	valid := New(ModeRun, now)
	valid.SourceSHA = strings.Repeat("a", 40)
	valid.PackageRevision = strings.Repeat("b", 40)
	valid.EnclaveName = "vm64-test"
	valid.ResultsDir = t.TempDir()
	if err := valid.Validate(now); err != nil {
		t.Fatal(err)
	}

	borrowed := valid
	borrowed.Mode = ModeTest
	borrowed.EnclaveIdentifier = "existing-enclave"
	borrowed.EnclaveName = ""
	if err := borrowed.Validate(now); err != nil {
		t.Fatal(err)
	}

	resume := valid
	resume.Mode = ModeResume
	resume.CheckpointPath = "checkpoint.json"
	if err := resume.Validate(now); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultBudgetLeavesFiveHoursBeforeCleanup(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	configuration := New(ModeRun, now)
	stageDeadline := configuration.GlobalDeadline.Add(-configuration.CleanupReserve)
	if want := now.Add(5 * time.Hour); !stageDeadline.Equal(want) {
		t.Fatalf("default stage deadline = %s, want %s", stageDeadline, want)
	}
}

func TestRunConfigRejectsUnsafeInputs(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	base := New(ModeRun, now)
	base.SourceSHA = strings.Repeat("a", 40)
	base.PackageRevision = strings.Repeat("b", 40)
	base.EnclaveName = "vm64-test"
	base.ResultsDir = t.TempDir()

	tests := []struct {
		name string
		edit func(*RunConfig)
		want string
	}{
		{name: "short source", edit: func(c *RunConfig) { c.SourceSHA = "abc" }, want: "source SHA"},
		{name: "unpinned package", edit: func(c *RunConfig) { c.PackageRevision = "main" }, want: "package revision"},
		{name: "no cleanup budget", edit: func(c *RunConfig) { c.GlobalDeadline = now.Add(c.CleanupReserve) }, want: "cleanup reserve"},
		{name: "borrowed without identifier", edit: func(c *RunConfig) { c.Mode = ModeTest }, want: "enclave identifier"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := base
			test.edit(&got)
			if err := got.Validate(now); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("got %v, want error containing %q", err, test.want)
			}
		})
	}
}

func TestDigestExcludesOutputLocationAndDeadline(t *testing.T) {
	now := time.Unix(1_700_000_000, 0).UTC()
	first := New(ModeRun, now)
	first.SourceSHA = strings.Repeat("a", 40)
	first.PackageRevision = strings.Repeat("b", 40)
	first.ResultsDir = "/tmp/one"
	second := first
	second.ResultsDir = "/tmp/two"
	second.GlobalDeadline = first.GlobalDeadline.Add(time.Hour)
	a, err := first.Digest()
	if err != nil {
		t.Fatal(err)
	}
	b, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Fatalf("operational output paths changed configuration digest: %s != %s", a, b)
	}
	second.AllowDisruptive = true
	c, err := second.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if c == a {
		t.Fatal("disruptive-policy change did not change configuration digest")
	}
}
