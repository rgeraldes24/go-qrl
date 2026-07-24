// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package process

import (
	"bytes"
	"io"
	"sort"
	"strings"
	"sync"
)

var redaction = []byte("[REDACTED]")

type redactingWriter struct {
	mu      sync.Mutex
	dst     io.Writer
	secrets [][]byte
	maxLen  int
	pending []byte
	closed  bool
}

func newRedactingWriter(dst io.Writer, rawSecrets []string) *redactingWriter {
	unique := make(map[string]struct{}, len(rawSecrets))
	for _, secret := range rawSecrets {
		if secret != "" {
			unique[secret] = struct{}{}
		}
	}
	secrets := make([][]byte, 0, len(unique))
	maxLen := 0
	for secret := range unique {
		encoded := []byte(secret)
		secrets = append(secrets, encoded)
		if len(encoded) > maxLen {
			maxLen = len(encoded)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return &redactingWriter{dst: dst, secrets: secrets, maxLen: maxLen}
}

func (writer *redactingWriter) Write(data []byte) (int, error) {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return 0, io.ErrClosedPipe
	}
	originalLength := len(data)
	if len(writer.secrets) == 0 {
		_, err := writer.dst.Write(data)
		if err != nil {
			return 0, err
		}
		return originalLength, nil
	}
	combined := append(append([]byte(nil), writer.pending...), data...)
	cut := len(combined) - writer.maxLen + 1
	if cut < 0 {
		cut = 0
	}
	for _, secret := range writer.secrets {
		for offset := 0; offset < cut; {
			index := bytes.Index(combined[offset:], secret)
			if index < 0 {
				break
			}
			start := offset + index
			if start < cut && start+len(secret) > cut {
				cut = start
				break
			}
			offset = start + 1
		}
	}
	if cut > 0 {
		if _, err := writer.dst.Write(redactBytes(combined[:cut], writer.secrets)); err != nil {
			return 0, err
		}
	}
	writer.pending = append(writer.pending[:0], combined[cut:]...)
	return originalLength, nil
}

func (writer *redactingWriter) Close() error {
	writer.mu.Lock()
	defer writer.mu.Unlock()
	if writer.closed {
		return nil
	}
	writer.closed = true
	if len(writer.pending) == 0 {
		return nil
	}
	_, err := writer.dst.Write(redactBytes(writer.pending, writer.secrets))
	writer.pending = nil
	return err
}

func redactBytes(data []byte, secrets [][]byte) []byte {
	redacted := append([]byte(nil), data...)
	for _, secret := range secrets {
		redacted = bytes.ReplaceAll(redacted, secret, redaction)
	}
	return redacted
}

func redactString(value string, secrets []string) string {
	for _, secret := range secrets {
		if secret != "" {
			value = strings.ReplaceAll(value, secret, string(redaction))
		}
	}
	return value
}
