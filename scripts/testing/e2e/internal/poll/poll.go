// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package poll provides context-aware polling for idempotent observations.
// Checks run immediately and are never allowed to outlive the supplied
// context. Callers must not use this package to retry state-changing work.
package poll

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Check returns true when the observed condition is satisfied. An error is
// terminal unless Options.RetryErrors is set.
type Check func(context.Context) (bool, error)

// Options controls a polling loop. Timeout is optional; when it is zero the
// caller's context remains the only bound. Interval must always be positive.
type Options struct {
	Interval    time.Duration
	Timeout     time.Duration
	RetryErrors bool
}

// Error reports cancellation or timeout and retains the last retryable
// observation error. It unwraps to the context error, so errors.Is continues
// to work with context.Canceled and context.DeadlineExceeded.
type Error struct {
	Cause     error
	LastError error
}

func (e *Error) Error() string {
	if e.LastError == nil {
		return fmt.Sprintf("polling stopped: %v", e.Cause)
	}
	return fmt.Sprintf("polling stopped: %v (last observation: %v)", e.Cause, e.LastError)
}

func (e *Error) Unwrap() error { return e.Cause }

// Until polls until check succeeds, check returns an error, or ctx ends.
func Until(ctx context.Context, interval time.Duration, check Check) error {
	return Do(ctx, Options{Interval: interval}, check)
}

// Retry polls an idempotent observation and treats observation errors as
// transient. The most recent error is retained if ctx ends first.
func Retry(ctx context.Context, interval time.Duration, check Check) error {
	return Do(ctx, Options{Interval: interval, RetryErrors: true}, check)
}

// Do runs check immediately, then at Interval after each unsuccessful check.
func Do(parent context.Context, options Options, check Check) error {
	if parent == nil {
		return errors.New("poll context is nil")
	}
	if check == nil {
		return errors.New("poll check is nil")
	}
	if options.Interval <= 0 {
		return errors.New("poll interval must be positive")
	}
	if options.Timeout < 0 {
		return errors.New("poll timeout cannot be negative")
	}

	ctx := parent
	cancel := func() {}
	if options.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, options.Timeout)
	}
	defer cancel()

	var lastError error
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return &Error{Cause: context.Cause(ctx), LastError: lastError}
		case <-timer.C:
		}

		done, err := check(ctx)
		if err != nil {
			if !options.RetryErrors {
				return err
			}
			lastError = err
		} else {
			lastError = nil
			if done {
				select {
				case <-ctx.Done():
					return &Error{Cause: context.Cause(ctx)}
				default:
					return nil
				}
			}
		}

		timer.Reset(options.Interval)
	}
}
