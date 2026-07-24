// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package poll provides context-bound retry for eventually consistent network
// discovery and readiness checks.
package poll

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func Until(ctx context.Context, interval time.Duration, operation func(context.Context) error) error {
	if ctx == nil || operation == nil || interval <= 0 {
		return errors.New("poll requires a context, operation, and positive interval")
	}
	var last error
	for {
		if err := operation(ctx); err == nil {
			return nil
		} else {
			last = err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return fmt.Errorf("poll deadline reached after: %v: %w", last, ctx.Err())
		case <-timer.C:
		}
	}
}
