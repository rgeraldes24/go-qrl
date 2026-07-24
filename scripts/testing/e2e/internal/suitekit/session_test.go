// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package suitekit

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
)

func TestOpenSigningSessionComposesOneRPCSessionAndWallet(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	defer server.Close()
	environment := sessionEnvironment(t, server.URL)

	session, err := OpenSigningSession(context.Background(), environment)
	if err != nil {
		t.Fatal(err)
	}
	if session.Client == nil || session.Wallet == nil {
		t.Fatal("signing session did not retain exactly one RPC session and wallet")
	}
	if session.Environment != environment {
		t.Fatalf("session environment = %+v, want %+v", session.Environment, environment)
	}
	expectedWallet, err := wallet.RestoreFromSeedHex(testSeed)
	if err != nil {
		t.Fatal(err)
	}
	if want := common.Address(expectedWallet.GetAddress()); session.Sender != want {
		t.Fatalf("sender = %s, want %s", session.Sender.Hex(), want.Hex())
	}

	session.Close()
	session.Close()
	var nilSession *SigningSession
	nilSession.Close()
}

func TestOpenSigningSessionRequiresSignerBeforeConnecting(t *testing.T) {
	environment := sessionEnvironment(t, "http://127.0.0.1:1")
	environment.SeedFile = ""
	if _, err := OpenSigningSession(context.Background(), environment); err == nil || !strings.Contains(err.Error(), seedFileVariable) {
		t.Fatalf("missing signer error = %v", err)
	}
	if _, err := OpenSigningSession(nil, environment); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("nil context error = %v", err)
	}
}

func TestOpenSigningSessionRequiresRPCBeforeConnecting(t *testing.T) {
	environment := sessionEnvironment(t, "")
	if _, err := OpenSigningSession(context.Background(), environment); err == nil || !strings.Contains(err.Error(), rpcURLVariable) {
		t.Fatalf("missing RPC URL error = %v", err)
	}
}

func sessionEnvironment(t *testing.T, rpcURL string) Environment {
	t.Helper()
	root := t.TempDir()
	seedPath := filepath.Join(root, "wallet.seed")
	if err := os.WriteFile(seedPath, []byte(testSeed+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return Environment{
		RPCURL:       rpcURL,
		GraphQLURL:   rpcURL + "/graphql",
		WebSocketURL: "ws" + strings.TrimPrefix(rpcURL, "http") + "/ws",
		SeedFile:     seedPath,
	}
}
