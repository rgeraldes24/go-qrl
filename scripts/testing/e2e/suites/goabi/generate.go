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

package goabi

// Regenerate the representative checked-in Go binding from the deterministic
// Hyperion artifact. Full compiler coverage uses bind.BoundContract; this
// projected binding exercises every generated wrapper category without
// checking in a copy of the template for every ABI entry. This command does
// not require hypc:
//
//     go -C scripts/testing/e2e generate ./suites/goabi
//
//go:generate go -C ../../../../.. run ./cmd/abigen --abi scripts/testing/e2e/testdata/contracts/EventEmitterBindingSmoke.abi --bin scripts/testing/e2e/testdata/contracts/EventEmitter.bin --pkg goabi --type EventEmitterBindingSmoke --out scripts/testing/e2e/suites/goabi/event_emitter_smoke_binding.go
