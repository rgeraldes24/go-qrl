// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Package contracts embeds the compiler artifacts used by the E2E suites.
package contracts

import (
	"embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/theQRL/go-qrl/accounts/abi"
)

//go:embed *.abi *.bin
var artifacts embed.FS

// Artifact is a parsed contract ABI and its deployment bytecode.
type Artifact struct {
	ABI      abi.ABI
	Bytecode []byte
}

// Load returns the embedded ABI and bytecode for name.
func Load(name string) (*Artifact, error) {
	abiJSON, err := artifacts.ReadFile(name + ".abi")
	if err != nil {
		return nil, fmt.Errorf("read %s ABI: %w", name, err)
	}
	parsed, err := abi.JSON(strings.NewReader(string(abiJSON)))
	if err != nil {
		return nil, fmt.Errorf("parse %s ABI: %w", name, err)
	}
	bin, err := artifacts.ReadFile(name + ".bin")
	if err != nil {
		return nil, fmt.Errorf("read %s bytecode: %w", name, err)
	}
	encoded := strings.TrimPrefix(strings.TrimSpace(string(bin)), "0x")
	bytecode, err := hex.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode %s bytecode: %w", name, err)
	}
	if len(bytecode) == 0 {
		return nil, fmt.Errorf("decode %s bytecode: empty artifact", name)
	}
	return &Artifact{ABI: parsed, Bytecode: bytecode}, nil
}
