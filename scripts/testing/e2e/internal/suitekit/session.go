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

// Session is one RPC connection shared by a live suite.
type Session struct {
	Environment Environment
	Client      *qrlclient.Client

	closeOnce sync.Once
}

// SigningSession composes one RPC Session with one restored wallet identity.
type SigningSession struct {
	*Session
	Wallet wallet.Wallet
	Sender common.Address
}

// OpenSession opens one RPC connection. Network authentication has already
// checked the chain and client identity before the suite reaches this point.
func OpenSession(ctx context.Context, environment Environment) (*Session, error) {
	if ctx == nil {
		return nil, errors.New("open live E2E session: context is nil")
	}
	if strings.TrimSpace(environment.RPCURL) == "" {
		return nil, fmt.Errorf("%s is empty", rpcURLVariable)
	}
	client, err := qrlclient.DialContext(ctx, environment.RPCURL)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", rpcURLVariable, err)
	}
	return &Session{
		Environment: environment,
		Client:      client,
	}, nil
}

// OpenSigningSession restores the invocation wallet exactly once and opens
// exactly one RPC Session.
func OpenSigningSession(ctx context.Context, environment Environment) (*SigningSession, error) {
	if ctx == nil {
		return nil, errors.New("open live E2E signing session: context is nil")
	}
	if strings.TrimSpace(environment.SeedFile) == "" {
		return nil, fmt.Errorf("%s is empty", seedFileVariable)
	}
	_, restored, err := readSeedAndWallet(environment.SeedFile)
	if err != nil {
		return nil, err
	}
	session, err := OpenSession(ctx, environment)
	if err != nil {
		return nil, err
	}
	return &SigningSession{
		Session: session,
		Wallet:  restored,
		Sender:  common.Address(restored.GetAddress()),
	}, nil
}

// Close closes the composed RPC session. It is nil-safe and idempotent.
func (session *SigningSession) Close() {
	if session == nil {
		return
	}
	session.Session.Close()
}

// Close closes the session's RPC connection. It is nil-safe and idempotent.
func (session *Session) Close() {
	if session == nil {
		return
	}
	session.closeOnce.Do(func() {
		if session.Client != nil {
			session.Client.Close()
		}
	})
}
