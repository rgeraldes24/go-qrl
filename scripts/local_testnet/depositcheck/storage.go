// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/qrlclient"
)

const (
	depositBranchFirstSlot     = uint64(0x00)
	depositBranchSlotCount     = 16
	depositZeroHashesFirstSlot = uint64(0x11)
	depositZeroHashesSlotCount = 16
	legacyZeroHashesFirstSlot  = uint64(0x22)
	legacyZeroHashesSlotCount  = 31
)

// expectedDepositStorage reconstructs the constructor-equivalent VM64 layout
// emitted by vm64_genesis_gqrl.py. Hyperion packs two bytes32 array elements in
// each 64-byte word, serialized as odd || even in the genesis JSON.
func expectedDepositStorage() (map[string]string, string, error) {
	zeroHashes := [32][32]byte{}
	for i := 1; i < len(zeroHashes); i++ {
		zeroHashes[i] = sha256.Sum256(append(zeroHashes[i-1][:], zeroHashes[i-1][:]...))
	}
	storage := make(map[string]string, depositZeroHashesSlotCount)
	for pair := 0; pair < depositZeroHashesSlotCount; pair++ {
		word := make([]byte, common.StorageValue64Length)
		copy(word[:32], zeroHashes[2*pair+1][:])
		copy(word[32:], zeroHashes[2*pair][:])
		storage[storageKey(depositZeroHashesFirstSlot+uint64(pair)).Hex()] = "0x" + hex.EncodeToString(word)
	}
	canonical, err := json.Marshal(storage)
	if err != nil {
		return nil, "", fmt.Errorf("encode expected VM64 deposit storage: %w", err)
	}
	digest := sha256.Sum256(canonical)
	return storage, hex.EncodeToString(digest[:]), nil
}

// verifyDepositBranchStorage proves the mutable sparse-tree branch is packed
// as two bytes32 values per VM64 word. The big-endian RPC representation is
// odd || even, matching the constructor storage layout reconstructed above.
func verifyDepositBranchStorage(ctx context.Context, client *qrlclient.Client, contract common.Address, leaves [][32]byte) error {
	branch := depositTreeBranch(leaves)
	for pair := 0; pair < depositBranchSlotCount; pair++ {
		want := packedDepositBranchWord(branch, pair)
		key := storageKey(depositBranchFirstSlot + uint64(pair))
		got, err := client.StorageAt(ctx, contract, key, nil)
		if err != nil {
			return fmt.Errorf("read packed deposit branch slot %s: %w", key.Hex(), err)
		}
		if !bytes.Equal(got, want[:]) {
			return fmt.Errorf("packed deposit branch slot %s = 0x%x, want 0x%x after %d deposits", key.Hex(), got, want, len(leaves))
		}
	}
	return nil
}

func packedDepositBranchWord(branch [depositTreeDepth][32]byte, pair int) [common.StorageValue64Length]byte {
	var word [common.StorageValue64Length]byte
	copy(word[:32], branch[2*pair+1][:])
	copy(word[32:], branch[2*pair][:])
	return word
}

func storageKey(slot uint64) common.Hash {
	var key common.Hash
	for i := len(key) - 1; slot != 0; i-- {
		key[i] = byte(slot)
		slot >>= 8
	}
	return key
}

func verifyDepositStorage(ctx context.Context, client *qrlclient.Client, contract common.Address, manifest depositContractManifest) error {
	expected, digest, err := expectedDepositStorage()
	if err != nil {
		return err
	}
	if digest != manifest.StorageSHA256 {
		return fmt.Errorf("generator storage manifest mismatch: reconstructed sha256=%s, manifest sha256=%s", digest, manifest.StorageSHA256)
	}
	for slot := depositZeroHashesFirstSlot; slot < depositZeroHashesFirstSlot+depositZeroHashesSlotCount; slot++ {
		key := storageKey(slot)
		got, err := client.StorageAt(ctx, contract, key, nil)
		if err != nil {
			return fmt.Errorf("read packed deposit storage slot %s: %w", key.Hex(), err)
		}
		want, err := hex.DecodeString(expected[key.Hex()][2:])
		if err != nil {
			return fmt.Errorf("decode expected packed deposit storage slot %s: %w", key.Hex(), err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("packed deposit storage slot %s = 0x%x, want %s", key.Hex(), got, expected[key.Hex()])
		}
	}
	zeroWord := make([]byte, common.StorageValue64Length)
	for slot := legacyZeroHashesFirstSlot; slot < legacyZeroHashesFirstSlot+legacyZeroHashesSlotCount; slot++ {
		key := storageKey(slot)
		got, err := client.StorageAt(ctx, contract, key, nil)
		if err != nil {
			return fmt.Errorf("read rejected legacy deposit storage slot %s: %w", key.Hex(), err)
		}
		if !bytes.Equal(got, zeroWord) {
			return fmt.Errorf("legacy deposit storage slot %s is populated: 0x%x", key.Hex(), got)
		}
	}
	return nil
}
