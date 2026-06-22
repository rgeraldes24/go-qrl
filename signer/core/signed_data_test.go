// Copyright 2019 The go-ethereum Authors
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

package core_test

import (
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/accounts/keystore"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/signer/core"
	"github.com/theQRL/go-qrl/signer/core/apitypes"
)

func TestSignData(t *testing.T) {
	t.Parallel()
	api, control := setup(t)
	//Create two accounts
	createAccount(control, api, t)
	createAccount(control, api, t)
	control.approveCh <- "1"
	list, err := api.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	a := common.NewMixedcaseAddress(list[0])

	control.approveCh <- "Y"
	control.inputCh <- "wrongpassword"
	signature, err := api.SignData(t.Context(), apitypes.TextPlain.Mime, a, hexutil.Encode([]byte("EHLO world")))
	if signature != nil {
		t.Errorf("Expected nil-data, got %x", signature)
	}
	if err != keystore.ErrDecrypt {
		t.Errorf("Expected ErrDecrypt! '%v'", err)
	}
	control.approveCh <- "No way"
	signature, err = api.SignData(t.Context(), apitypes.TextPlain.Mime, a, hexutil.Encode([]byte("EHLO world")))
	if signature != nil {
		t.Errorf("Expected nil-data, got %x", signature)
	}
	if err != core.ErrRequestDenied {
		t.Errorf("Expected ErrRequestDenied! '%v'", err)
	}
	// text/plain
	control.approveCh <- "Y"
	control.inputCh <- "a_long_password"
	signature, err = api.SignData(t.Context(), apitypes.TextPlain.Mime, a, hexutil.Encode([]byte("EHLO world")))
	if err != nil {
		t.Fatal(err)
	}
	if signature == nil || len(signature) != 4627 {
		t.Errorf("Expected 4627 byte ML-DSA-87 signature (got %d bytes)", len(signature))
	}
	if signature, err = api.SignData(t.Context(), "data/typed", a, "0x7b7d"); err == nil {
		t.Fatal("expected data/typed signing to be unsupported")
	} else if signature != nil {
		t.Errorf("expected nil signature for unsupported typed data, got %x", signature)
	} else if !strings.Contains(err.Error(), "typed data signing is not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}
