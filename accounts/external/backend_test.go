// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package external

import (
	"context"
	"errors"
	"math/big"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/accounts"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/rpc"
	"github.com/theQRL/go-qrl/signer/core/apitypes"
)

type testAccountService struct {
	result signTransactionResult
}

func (s *testAccountService) SignTransaction(context.Context, apitypes.SendTxArgs) (signTransactionResult, error) {
	return s.result, nil
}

type cancelableAccountService struct {
	started  chan struct{}
	canceled chan struct{}
	release  chan struct{}
}

func (s *cancelableAccountService) SignTransaction(ctx context.Context, _ apitypes.SendTxArgs) (signTransactionResult, error) {
	close(s.started)
	select {
	case <-ctx.Done():
		close(s.canceled)
		return signTransactionResult{}, ctx.Err()
	case <-s.release:
		return signTransactionResult{}, errors.New("test released signer request")
	}
}

func TestExternalSignerSignTxContextCancellation(t *testing.T) {
	service := &cancelableAccountService{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	server := rpc.NewServer()
	if err := server.RegisterName("account", service); err != nil {
		t.Fatalf("register account service: %v", err)
	}
	httpServer := httptest.NewServer(server)
	t.Cleanup(func() {
		close(service.release)
		httpServer.Close()
		server.Stop()
	})
	client, err := rpc.Dial(httpServer.URL)
	if err != nil {
		t.Fatalf("dial account service: %v", err)
	}
	t.Cleanup(client.Close)

	w, err := wallet.Generate(wallet.ML_DSA_87)
	if err != nil {
		t.Fatalf("generate wallet: %v", err)
	}
	chainID := big.NewInt(1337)
	signer := &ExternalSigner{client: client, endpoint: httpServer.URL}
	ctx, cancel := context.WithCancel(t.Context())
	errCh := make(chan error, 1)
	go func() {
		_, err := signer.SignTxContext(ctx, accounts.Account{Address: common.Address(w.GetAddress())}, testDynamicFeeTx(chainID), chainID)
		errCh <- err
	}()

	select {
	case <-service.started:
	case <-time.After(5 * time.Second):
		t.Fatal("external signing request did not reach the service")
	}
	cancel()
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SignTxContext error = %v, want context canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SignTxContext did not return after cancellation")
	}
	select {
	case <-service.canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("external signer service did not observe cancellation")
	}
}

func TestExternalSignerRejectsUnexpectedSignedTransaction(t *testing.T) {
	requestedWallet, err := wallet.Generate(wallet.ML_DSA_87)
	if err != nil {
		t.Fatalf("generate requested wallet: %v", err)
	}
	otherWallet, err := wallet.Generate(wallet.ML_DSA_87)
	if err != nil {
		t.Fatalf("generate other wallet: %v", err)
	}
	requestedAccount := accounts.Account{Address: common.Address(requestedWallet.GetAddress())}
	chainID := big.NewInt(1337)
	unsigned := testDynamicFeeTx(chainID)

	tests := []struct {
		name             string
		wallet           wallet.Wallet
		signChain        *big.Int
		value            *big.Int
		mutateAccessList bool
		wantErr          string
		wantSender       common.Address
	}{
		{
			name:       "accepts requested account and chain",
			wallet:     requestedWallet,
			signChain:  chainID,
			value:      big.NewInt(3),
			wantSender: requestedAccount.Address,
		},
		{
			name:      "rejects another account",
			wallet:    otherWallet,
			signChain: chainID,
			value:     big.NewInt(3),
			wantErr:   "want " + requestedAccount.Address.Hex(),
		},
		{
			name:      "rejects another chain",
			wallet:    requestedWallet,
			signChain: big.NewInt(7331),
			value:     big.NewInt(3),
			wantErr:   "want 1337",
		},
		{
			name:      "rejects changed transaction body",
			wallet:    requestedWallet,
			signChain: chainID,
			value:     big.NewInt(4),
			wantErr:   "changed transaction: value 4, want 3",
		},
		{
			name:             "rejects changed access-list upper address half",
			wallet:           requestedWallet,
			signChain:        chainID,
			value:            big.NewInt(3),
			mutateAccessList: true,
			wantErr:          "changed transaction: access list",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			accessList := testDynamicFeeAccessList()
			if test.mutateAccessList {
				// Change only the most-significant byte of the 64-byte address.
				// The low 32 bytes and storage key remain identical, so a legacy
				// 32-byte comparison would incorrectly accept this response.
				accessList[0].Address[0] ^= 0x01
			}
			txToSign := testDynamicFeeTxWithValueAndAccessList(test.signChain, test.value, accessList)
			signed, err := types.SignTx(txToSign, types.LatestSignerForChainID(test.signChain), test.wallet)
			if err != nil {
				t.Fatalf("sign response transaction: %v", err)
			}
			raw, err := signed.MarshalBinary()
			if err != nil {
				t.Fatalf("marshal response transaction: %v", err)
			}
			signer := newTestExternalSigner(t, signTransactionResult{Raw: hexutil.Bytes(raw), Tx: signed})

			got, err := signer.SignTx(requestedAccount, unsigned, chainID)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("SignTx error = %v, want substring %q", err, test.wantErr)
				}
				if got != nil {
					t.Fatalf("SignTx returned transaction on error: %v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SignTx: %v", err)
			}
			sender, err := types.Sender(types.LatestSignerForChainID(got.ChainId()), got)
			if err != nil {
				t.Fatalf("recover sender: %v", err)
			}
			if sender != test.wantSender {
				t.Fatalf("sender = %s, want %s", sender, test.wantSender)
			}
		})
	}
}

func TestExternalSignerRejectsMissingTransaction(t *testing.T) {
	w, err := wallet.Generate(wallet.ML_DSA_87)
	if err != nil {
		t.Fatal(err)
	}
	chainID := big.NewInt(1337)
	signer := newTestExternalSigner(t, signTransactionResult{})
	got, err := signer.SignTx(accounts.Account{Address: common.Address(w.GetAddress())}, testDynamicFeeTx(chainID), chainID)
	if err == nil || !strings.Contains(err.Error(), "returned no transaction") {
		t.Fatalf("SignTx error = %v, want missing transaction error", err)
	}
	if got != nil {
		t.Fatalf("SignTx returned transaction on error: %v", got)
	}
}

func newTestExternalSigner(t *testing.T, result signTransactionResult) *ExternalSigner {
	t.Helper()
	server := rpc.NewServer()
	if err := server.RegisterName("account", &testAccountService{result: result}); err != nil {
		t.Fatalf("register account service: %v", err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(func() {
		client.Close()
		server.Stop()
	})
	return &ExternalSigner{client: client, endpoint: "inproc"}
}

func testDynamicFeeTx(chainID *big.Int) *types.Transaction {
	return testDynamicFeeTxWithValue(chainID, big.NewInt(3))
}

func testDynamicFeeTxWithValue(chainID, value *big.Int) *types.Transaction {
	return testDynamicFeeTxWithValueAndAccessList(chainID, value, testDynamicFeeAccessList())
}

func testDynamicFeeTxWithValueAndAccessList(chainID, value *big.Int, accessList types.AccessList) *types.Transaction {
	to := common.Address{common.AddressLength - 1: 0x42}
	return types.NewTx(&types.DynamicFeeTx{
		ChainID:    new(big.Int).Set(chainID),
		Nonce:      7,
		GasTipCap:  big.NewInt(2),
		GasFeeCap:  big.NewInt(20),
		Gas:        25000,
		To:         &to,
		Value:      new(big.Int).Set(value),
		Data:       []byte{0xaa, 0xbb},
		AccessList: accessList,
	})
}

func testDynamicFeeAccessList() types.AccessList {
	var address common.Address
	address[0] = 0x80
	address[common.AddressLength/2-1] = 0x11
	address[common.AddressLength/2] = 0x22
	address[common.AddressLength-1] = 0x42
	return types.AccessList{{
		Address:     address,
		StorageKeys: []common.Hash{common.HexToHash("0x1234")},
	}}
}
