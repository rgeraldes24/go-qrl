// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package goabi

import (
	"bytes"
	"math/big"
	"testing"

	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	qrlwallet "github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
)

func TestGeneratedVM64BindingRuntime(t *testing.T) {
	wallet, err := qrlwallet.Generate(qrlwallet.ML_DSA_87)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
	if err != nil {
		t.Fatal(err)
	}
	sim := backends.NewSimulatedBackend(core.GenesisAlloc{
		auth.From: {Balance: new(big.Int).Mul(big.NewInt(1_000_000), big.NewInt(1e18))},
	}, 10_000_000)
	t.Cleanup(func() { _ = sim.Close() })

	address, deployment, contract, err := DeployEventEmitter(auth, sim)
	if err != nil {
		t.Fatalf("deploy generated VM64 binding: %v", err)
	}
	sim.Commit()
	deploymentReceipt, err := bind.WaitMined(t.Context(), sim, deployment)
	if err != nil {
		t.Fatalf("wait for generated-binding deployment: %v", err)
	}
	if deploymentReceipt.Status != types.ReceiptStatusSuccessful || deploymentReceipt.ContractAddress != address {
		t.Fatalf("deployment receipt = %+v, want successful contract %s", deploymentReceipt, address)
	}

	amount := new(big.Int).Lsh(big.NewInt(1), 400)
	amount.Add(amount, big.NewInt(12345))
	delta := new(big.Int).Neg(new(big.Int).Lsh(big.NewInt(1), 399))
	var tag [64]byte
	for i := range tag {
		tag[i] = byte(i*13 + 7)
	}
	var recipient common.Address
	for i := range recipient {
		recipient[i] = byte(i + 1)
	}
	payload := []byte("vm64 generated binding runtime")
	note := "512-bit ABI and 64-byte address"

	gotAmount, gotDelta, gotTag, gotRecipient, gotPayload, gotNote, gotEnabled, err := contract.Echo(
		&bind.CallOpts{Context: t.Context()}, amount, delta, tag, recipient, payload, note, true,
	)
	if err != nil {
		t.Fatalf("call generated VM64 binding: %v", err)
	}
	if gotAmount.Cmp(amount) != 0 || gotDelta.Cmp(delta) != 0 || gotTag != tag || gotRecipient != recipient ||
		!bytes.Equal(gotPayload, payload) || gotNote != note || !gotEnabled {
		t.Fatalf("generated VM64 binding call mismatch")
	}

	storeTx, err := contract.Store(auth, amount, delta, tag, recipient, payload, note, true)
	if err != nil {
		t.Fatalf("transact through generated VM64 binding: %v", err)
	}
	sim.Commit()
	storeReceipt, err := bind.WaitMined(t.Context(), sim, storeTx)
	if err != nil {
		t.Fatalf("wait for generated-binding transaction: %v", err)
	}
	if storeReceipt.Status != types.ReceiptStatusSuccessful {
		t.Fatalf("generated-binding transaction status = %d, want successful", storeReceipt.Status)
	}
	if len(storeReceipt.Logs) < 2 {
		t.Fatalf("generated-binding transaction logs = %d, want Stored and Dynamic events", len(storeReceipt.Logs))
	}
	dynamic, err := contract.ParseDynamic(*storeReceipt.Logs[1])
	if err != nil {
		t.Fatalf("parse generated-binding dynamic indexed event: %v", err)
	}
	payloadHash := crypto.Keccak256Hash(payload)
	noteHash := crypto.Keccak256Hash([]byte(note))
	if dynamic.Payload != payloadHash || dynamic.Note != noteHash || dynamic.Amount.Cmp(amount) != 0 {
		t.Fatalf("generated-binding dynamic indexed event = %+v, want payload %s, note %s, amount %s", dynamic, payloadHash, noteHash, amount)
	}
	end := storeReceipt.BlockNumber.Uint64()
	it, err := contract.FilterStored(&bind.FilterOpts{Start: end, End: &end, Context: t.Context()}, []common.Address{recipient}, []*big.Int{amount}, []*big.Int{delta})
	if err != nil {
		t.Fatalf("filter generated-binding event: %v", err)
	}
	defer it.Close()
	if !it.Next() || it.Event.Raw.TxHash != storeTx.Hash() || it.Event.Recipient != recipient ||
		it.Event.Amount.Cmp(amount) != 0 || it.Event.Delta.Cmp(delta) != 0 || it.Event.Tag != tag ||
		!bytes.Equal(it.Event.Payload, payload) || it.Event.Note != note || !it.Event.Enabled {
		if err := it.Error(); err != nil {
			t.Fatalf("iterate generated-binding event: %v", err)
		}
		t.Fatalf("generated-binding event mismatch")
	}
	if it.Next() {
		t.Fatal("generated-binding event filter returned more than one event")
	}
	if err := it.Error(); err != nil {
		t.Fatalf("finish generated-binding event iterator: %v", err)
	}
}
