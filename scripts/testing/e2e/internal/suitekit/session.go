// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package suitekit

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

// SigningSession is one RPC connection and restored wallet shared by a live
// suite.
type SigningSession struct {
	Environment Environment
	Client      *qrlclient.Client
	Wallet      wallet.Wallet
	Sender      common.Address

	closeOnce sync.Once
}

// OpenSigningSession restores the invocation wallet exactly once and opens
// exactly one RPC connection.
func OpenSigningSession(ctx context.Context, environment Environment) (*SigningSession, error) {
	if ctx == nil {
		return nil, errors.New("open live E2E signing session: context is nil")
	}
	if strings.TrimSpace(environment.SeedFile) == "" {
		return nil, fmt.Errorf("%s is empty", seedFileVariable)
	}
	restored, err := readWallet(environment.SeedFile)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(environment.RPCURL) == "" {
		return nil, fmt.Errorf("%s is empty", rpcURLVariable)
	}
	client, err := qrlclient.DialContext(ctx, environment.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", rpcURLVariable, err)
	}
	return &SigningSession{
		Environment: environment,
		Client:      client,
		Wallet:      restored,
		Sender:      common.Address(restored.GetAddress()),
	}, nil
}

// Close closes the RPC connection. It is nil-safe and idempotent.
func (session *SigningSession) Close() {
	if session == nil {
		return
	}
	session.closeOnce.Do(func() {
		if session.Client != nil {
			session.Client.Close()
		}
	})
}
