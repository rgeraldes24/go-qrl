// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package goabi

import (
	"math/big"
	"testing"

	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
	"github.com/theQRL/go-qrl/core"
	"github.com/theQRL/go-qrl/core/types"
	qrlwallet "github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
)

func TestAdvancedABICompilerRuntime(t *testing.T) {
	w, err := qrlwallet.Generate(qrlwallet.ML_DSA_87)
	if err != nil {
		t.Fatal(err)
	}
	auth, err := bind.NewKeyedTransactorWithChainID(w, big.NewInt(1337))
	if err != nil {
		t.Fatal(err)
	}
	sim := backends.NewSimulatedBackend(core.GenesisAlloc{
		auth.From: {Balance: new(big.Int).Mul(big.NewInt(1_000_000), big.NewInt(1e18))},
	}, 20_000_000)
	t.Cleanup(func() { _ = sim.Close() })

	initial := advancedABIInitialRecord()
	supported, err := checkAdvancedABIReadOnlyCreation(t.Context(), sim, auth.From, nil, initial)
	if err != nil {
		t.Fatalf("portable constructor decoder: %v", err)
	}
	if !supported {
		t.Fatal("simulated backend unexpectedly lacks creation-form eth_call")
	}

	artifact, err := loadAdvancedABIArtifact()
	if err != nil {
		t.Fatal(err)
	}
	address, tx, contract, err := bind.DeployContract(auth, artifact.ABI, artifact.Bytecode, sim, initial)
	if err != nil {
		t.Fatalf("deploy AdvancedABI through BoundContract: %v", err)
	}
	sim.Commit()
	receipt, err := bind.WaitMined(t.Context(), sim, tx)
	if err != nil {
		t.Fatalf("wait for AdvancedABI deployment: %v", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful || receipt.ContractAddress != address {
		t.Fatalf("AdvancedABI deployment receipt = %+v, want successful contract %s", receipt, address)
	}
	if err := checkAdvancedABIContract(
		t.Context(),
		sim,
		auth.From,
		address,
		contract,
		receipt.BlockNumber,
		receipt,
		initial,
	); err != nil {
		t.Fatal(err)
	}
}
