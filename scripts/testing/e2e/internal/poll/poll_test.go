// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package poll

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestUntilRunsImmediatelyAndCompletes(t *testing.T) {
	var calls atomic.Int32
	started := time.Now()
	err := Until(t.Context(), time.Millisecond, func(context.Context) (bool, error) {
		return calls.Add(1) == 2, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("calls = %d, want 2", calls.Load())
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("poll took unexpectedly long: %s", elapsed)
	}
}

func TestDoTimeoutRetainsLastRetryableError(t *testing.T) {
	want := errors.New("endpoint warming up")
	err := Do(t.Context(), Options{
		Interval:    time.Millisecond,
		Timeout:     20 * time.Millisecond,
		RetryErrors: true,
	}, func(context.Context) (bool, error) {
		return false, want
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
	var pollError *Error
	if !errors.As(err, &pollError) || !errors.Is(pollError.LastError, want) {
		t.Fatalf("error = %#v, want last observation %v", err, want)
	}
	if !strings.Contains(err.Error(), want.Error()) {
		t.Fatalf("error %q does not include last observation", err)
	}
}

func TestUntilReturnsObservationErrorWithoutRetry(t *testing.T) {
	want := errors.New("malformed response")
	var calls atomic.Int32
	err := Until(t.Context(), time.Millisecond, func(context.Context) (bool, error) {
		calls.Add(1)
		return false, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if calls.Load() != 1 {
		t.Fatalf("calls = %d, want 1", calls.Load())
	}
}

func TestUntilPropagatesCancellationIntoCheck(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	entered := make(chan struct{})
	returned := make(chan error, 1)
	go func() {
		returned <- Until(ctx, time.Hour, func(checkContext context.Context) (bool, error) {
			close(entered)
			<-checkContext.Done()
			return false, checkContext.Err()
		})
	}()
	<-entered
	cancel()
	select {
	case err := <-returned:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("poll did not stop after cancellation")
	}
}

func TestDoValidatesConfiguration(t *testing.T) {
	check := func(context.Context) (bool, error) { return true, nil }
	for _, test := range []struct {
		name    string
		ctx     context.Context
		options Options
		check   Check
	}{
		{name: "nil context", options: Options{Interval: time.Second}, check: check},
		{name: "nil check", ctx: t.Context(), options: Options{Interval: time.Second}},
		{name: "zero interval", ctx: t.Context(), check: check},
		{name: "negative timeout", ctx: t.Context(), options: Options{Interval: time.Second, Timeout: -1}, check: check},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := Do(test.ctx, test.options, test.check); err == nil {
				t.Fatal("Do succeeded, want validation error")
			}
		})
	}
}
