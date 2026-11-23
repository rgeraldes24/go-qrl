package crypto

import (
	"testing"

	"github.com/theQRL/go-qrl/common"
)

func TestKeccak512StateDigests(t *testing.T) {
	// Independent Keccak-512 vectors, including the RLP empty-trie value.
	vectors := []struct{ input, digest string }{
		{"", "0eab42de4c3ceb9235fc91acffe746b29c29a8c366b7c60e4e67c466f36a4304c00fa9caf9d87976ba469bcbe06713b435f091ef2769fb160cdab33d3670680e"},
		{"616263", "18587dc2ea106b9a1563e32b3312421ca164c7f1f07bc922a9c83d77cea3a1e5d0c69910739025372dc14ac9642629379540c17e2a65b19d77aa511a9d00bb96"},
		{"80", "a088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52e"},
	}
	hasher := NewKeccak512State()
	for _, vector := range vectors {
		input, want := common.FromHex(vector.input), common.HexToHash512(vector.digest)
		if got := Keccak512Hash(input); got != want {
			t.Fatalf("hash(%s): %x, want %x", vector.input, got, want)
		}
		if got := HashData512(hasher, input); got != want {
			t.Fatalf("reused hasher(%s): %x, want %x", vector.input, got, want)
		}
	}
}
