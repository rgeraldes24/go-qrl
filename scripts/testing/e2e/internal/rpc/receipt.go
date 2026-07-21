// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package rpc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/poll"
)

const DefaultReceiptMethod = "qrl_getTransactionReceipt"

// WaitForTransactionReceipt polls the QRL receipt method. Receipt reads are
// idempotent, so transient read errors are retried until ctx ends. Transaction
// submission itself is intentionally outside this helper.
func (c *Client) WaitForTransactionReceipt(ctx context.Context, transactionHash string, interval time.Duration, out any) error {
	return c.WaitForReceipt(ctx, DefaultReceiptMethod, transactionHash, interval, out)
}

// WaitForReceipt is the configurable form used by compatible RPC namespaces.
func (c *Client) WaitForReceipt(ctx context.Context, method, transactionHash string, interval time.Duration, out any) error {
	if method == "" || transactionHash == "" {
		return errors.New("receipt method and transaction hash are required")
	}
	if out == nil {
		return errors.New("receipt output is required")
	}
	var encoded json.RawMessage
	err := poll.Retry(ctx, interval, func(checkContext context.Context) (bool, error) {
		var raw json.RawMessage
		if err := c.Call(checkContext, method, []string{transactionHash}, &raw); err != nil {
			return false, err
		}
		if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return false, nil
		}
		encoded = append(encoded[:0], raw...)
		return true, nil
	})
	if err != nil {
		return fmt.Errorf("wait for transaction receipt: %w", err)
	}
	if err := json.Unmarshal(encoded, out); err != nil {
		return fmt.Errorf("decode transaction receipt: %w", err)
	}
	return nil
}
