// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package apitypes

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/crypto/pqcrypto"
)

const (
	TypedDataVersion   = "1"
	TypedDataAlgorithm = "ML-DSA-87"
)

// TypedDataSignature is the JSON result of account_signTypedData. It contains
// the signer metadata needed to verify a supplied typed-data request because
// ML-DSA signatures do not recover their public key.
type TypedDataSignature struct {
	Version    string         `json:"version"`
	Algorithm  string         `json:"algorithm"`
	Address    common.Address `json:"address"`
	Digest     common.Hash    `json:"digest"`
	PublicKey  hexutil.Bytes  `json:"publicKey"`
	Descriptor hexutil.Bytes  `json:"descriptor"`
	Signature  hexutil.Bytes  `json:"signature"`
}

// Verify checks the envelope, derives its claimed address from the public key
// and descriptor, recomputes the typed-data digest, and verifies the signature.
func (sig *TypedDataSignature) Verify(typedData TypedData) error {
	if sig == nil {
		return errors.New("typed data signature is nil")
	}
	if sig.Version != TypedDataVersion {
		return fmt.Errorf("unsupported typed data signature version %q", sig.Version)
	}
	if sig.Algorithm != TypedDataAlgorithm {
		return fmt.Errorf("unsupported typed data signature algorithm %q", sig.Algorithm)
	}
	if len(sig.PublicKey) != pqcrypto.MLDSA87PublicKeyLength {
		return fmt.Errorf("invalid ML-DSA-87 public key length %d", len(sig.PublicKey))
	}
	if len(sig.Descriptor) != pqcrypto.DescriptorSize {
		return fmt.Errorf("invalid wallet descriptor length %d", len(sig.Descriptor))
	}
	if len(sig.Signature) != pqcrypto.MLDSA87SignatureLength {
		return fmt.Errorf("invalid ML-DSA-87 signature length %d", len(sig.Signature))
	}
	descriptor, err := pqcrypto.BytesToDescriptor(sig.Descriptor)
	if err != nil {
		return fmt.Errorf("invalid wallet descriptor: %w", err)
	}
	address, err := pqcrypto.PublicKeyAndDescriptorToAddress(sig.PublicKey, descriptor)
	if err != nil {
		return fmt.Errorf("derive signer address: %w", err)
	}
	if address != sig.Address {
		return fmt.Errorf("public key derives address %s, not claimed address %s", address, sig.Address)
	}
	digest, _, err := TypedDataAndHash(typedData)
	if err != nil {
		return err
	}
	if !bytes.Equal(digest, sig.Digest[:]) {
		return fmt.Errorf("typed data digest mismatch: have %s, want %s", sig.Digest, common.BytesToHash(digest))
	}
	valid, err := pqcrypto.MLDSA87VerifySignature(sig.Signature, digest, sig.PublicKey, descriptor)
	if err != nil {
		return fmt.Errorf("verify ML-DSA-87 signature: %w", err)
	}
	if !valid {
		return pqcrypto.ErrBadSignature
	}
	return nil
}
