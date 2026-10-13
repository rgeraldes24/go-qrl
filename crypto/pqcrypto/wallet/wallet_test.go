package wallet

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadSeedFromFile(t *testing.T) {
	seed := strings.Repeat("ab", SeedSizeBytes)
	tests := []struct {
		input string
		err   string
	}{
		{input: seed},
		{input: seed + "\n"},
		{input: seed + "\r\n"},
		{
			input: seed[1:],
			err:   fmt.Sprintf("seed too short, want %d hex characters", SeedSizeBytes*2),
		},
		{
			input: seed + "X",
			err:   "invalid character 'X' at end of key file",
		},
		{
			input: seed + "\n\n\n",
			err:   fmt.Sprintf("key file too long, want %d hex characters", SeedSizeBytes*2),
		},
	}
	for _, test := range tests {
		file := filepath.Join(t.TempDir(), "seed")
		if err := os.WriteFile(file, []byte(test.input), 0600); err != nil {
			t.Fatal(err)
		}
		have, err := ReadSeedFromFile(file)
		switch {
		case err != nil && test.err == "":
			t.Fatalf("unexpected error for input %q:\n  %v", test.input, err)
		case err != nil && err.Error() != test.err:
			t.Fatalf("wrong error for input %q:\n  %v", test.input, err)
		case err == nil && test.err != "":
			t.Fatalf("no error for input %q", test.input)
		case err == nil && have != seed:
			t.Fatalf("wrong seed for input %q: %q", test.input, have)
		}
	}
}
