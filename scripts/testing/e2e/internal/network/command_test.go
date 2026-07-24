// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import "testing"

func TestCappedBufferAcceptsAndBoundsOutput(t *testing.T) {
	buffer := cappedBuffer{limit: 4}
	input := []byte("123456789")
	written, err := buffer.Write(input)
	if err != nil {
		t.Fatal(err)
	}
	if written != len(input) {
		t.Fatalf("accepted output = %d bytes; want %d", written, len(input))
	}
	if got := string(buffer.Bytes()); got != "1234" {
		t.Fatalf("captured output = %q; want %q", got, "1234")
	}
}
