package common

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestHash512Encoding(t *testing.T) {
	var h Hash512
	h[0], h[31], h[63] = 0x80, 0x01, 0xff
	b, err := json.Marshal(h)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Hash512
	if err := json.Unmarshal(b, &decoded); err != nil || decoded != h {
		t.Fatalf("round trip: %v, %x", err, decoded)
	}
	for _, width := range []int{32, 63, 65} {
		if err := decoded.UnmarshalText([]byte("0x" + strings.Repeat("00", width))); err == nil {
			t.Fatalf("accepted %d-byte digest", width)
		}
	}
	decoded.SetBytes([]byte{1})
	if decoded != (Hash512{63: 1}) {
		t.Fatal("SetBytes retained high bytes from previous value")
	}
	decoded = h
	decoded.SetBytes(decoded[32:])
	if decoded != (Hash512{63: 0xff}) {
		t.Fatal("SetBytes corrupted an overlapping input slice")
	}
}
