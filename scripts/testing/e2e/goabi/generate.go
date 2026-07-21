// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package main

// Regenerate the checked-in Go binding from the deterministic Hyperion
// artifacts. This command does not require hypc:
//
//     go generate ./scripts/testing/e2e/goabi
//
//go:generate go run ../../../../cmd/abigen --abi ../tests/fixtures/EventEmitter.abi --bin ../tests/fixtures/EventEmitter.bin --pkg main --type EventEmitter --out emitter_binding.go
