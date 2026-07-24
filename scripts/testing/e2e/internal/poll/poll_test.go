// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package poll

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestUntilRetriesAndPreservesLastFailure(t *testing.T) {
	attempts := 0
	err := Until(context.Background(), time.Microsecond, func(context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("not ready")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("err=%v attempts=%d", err, attempts)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err = Until(ctx, time.Microsecond, func(context.Context) error { return errors.New("still starting") })
	if err == nil || !strings.Contains(err.Error(), "still starting") || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error = %v", err)
	}
}
