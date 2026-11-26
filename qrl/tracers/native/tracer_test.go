// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package native_test

import (
	"errors"
	"math/big"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/qrl/tracers"
)

// TestTracerStopRace exercises the concurrent Stop / GetResult path that the
// trace RPC handler uses: a timeout watchdog goroutine calls Stop while the
// main goroutine is still running the trace and will eventually call
// GetResult. Under -race, writes to the interruption reason field must not
// race with reads, for every tracer that implements it.
//
// flatCallTracer's GetResult fails on an empty top-level call frame before
// loading the reason. For it the test populates the top-level call frame via
// CaptureStart so GetResult reaches the reason.Load() path where the race can
// be observed under -race.
func TestTracerStopRace(t *testing.T) {
	type setup struct {
		name       string
		needsFrame bool // whether GetResult requires a top-level call frame
	}
	cases := []setup{
		{"callTracer", false},
		{"flatCallTracer", true},
		{"4byteTracer", false},
		{"prestateTracer", false},
	}
	for _, s := range cases {
		t.Run(s.name, func(t *testing.T) {
			tr, err := tracers.DefaultDirectory.New(s.name, &tracers.Context{}, nil)
			require.NoError(t, err)

			if s.needsFrame {
				// Populate the top-level call frame so GetResult doesn't
				// short-circuit before reading the interruption reason.
				tr.CaptureStart(nil, common.Address{}, common.Address{}, false, nil, 0, big.NewInt(0))
			}

			stopErr := errors.New("execution timeout")
			var wg sync.WaitGroup
			wg.Add(2)
			go func() {
				defer wg.Done()
				tr.Stop(stopErr)
			}()
			go func() {
				defer wg.Done()
				_, _ = tr.GetResult()
			}()
			wg.Wait()
		})
	}
}
