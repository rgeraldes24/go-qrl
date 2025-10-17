package types

import (
	"bytes"
	"math/big"

	"github.com/theQRL/go-zond/common"
	"github.com/theQRL/go-zond/crypto/pqcrypto"
	"github.com/theQRL/go-zond/rlp"
)

type MLDSA87Tx struct {
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int // a.k.a. maxPriorityFeePerGas
	GasFeeCap  *big.Int // a.k.a. maxFeePerGas
	Gas        uint64
	To         *common.Address `rlp:"nil"` // nil means contract creation
	Value      *big.Int
	Data       []byte
	AccessList AccessList

	PublicKey []byte
	Signature []byte
}

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *MLDSA87Tx) copy() TxData {
	cpy := &MLDSA87Tx{
		Nonce: tx.Nonce,
		To:    copyAddressPtr(tx.To),
		Data:  common.CopyBytes(tx.Data),
		Gas:   tx.Gas,
		// These are copied below.
		AccessList: make(AccessList, len(tx.AccessList)),
		Value:      new(big.Int),
		ChainID:    new(big.Int),
		GasTipCap:  new(big.Int),
		GasFeeCap:  new(big.Int),
		PublicKey:  make([]byte, pqcrypto.PublicKeyLengthMLDSA87),
		Signature:  make([]byte, pqcrypto.SignatureLengthMLDSA87),
	}
	copy(cpy.AccessList, tx.AccessList)
	if tx.Value != nil {
		cpy.Value.Set(tx.Value)
	}
	if tx.ChainID != nil {
		cpy.ChainID.Set(tx.ChainID)
	}
	if tx.GasTipCap != nil {
		cpy.GasTipCap.Set(tx.GasTipCap)
	}
	if tx.GasFeeCap != nil {
		cpy.GasFeeCap.Set(tx.GasFeeCap)
	}
	if tx.PublicKey != nil {
		copy(cpy.PublicKey[:pqcrypto.PublicKeyLengthMLDSA87], tx.PublicKey)
	}
	if tx.Signature != nil {
		copy(cpy.Signature[:pqcrypto.SignatureLengthMLDSA87], tx.Signature)
	}
	return cpy
}

// accessors for innerTx.
func (tx *MLDSA87Tx) txType() byte           { return TxTypeMLDSA87 }
func (tx *MLDSA87Tx) chainID() *big.Int      { return tx.ChainID }
func (tx *MLDSA87Tx) accessList() AccessList { return tx.AccessList }
func (tx *MLDSA87Tx) data() []byte           { return tx.Data }
func (tx *MLDSA87Tx) gas() uint64            { return tx.Gas }
func (tx *MLDSA87Tx) gasFeeCap() *big.Int    { return tx.GasFeeCap }
func (tx *MLDSA87Tx) gasTipCap() *big.Int    { return tx.GasTipCap }
func (tx *MLDSA87Tx) gasPrice() *big.Int     { return tx.GasFeeCap }
func (tx *MLDSA87Tx) value() *big.Int        { return tx.Value }
func (tx *MLDSA87Tx) nonce() uint64          { return tx.Nonce }
func (tx *MLDSA87Tx) to() *common.Address    { return tx.To }

// TODO(rgeraldes24)
func (tx *MLDSA87Tx) effectiveGasPrice(dst *big.Int, baseFee *big.Int) *big.Int {
	if baseFee == nil {
		return dst.Set(tx.GasFeeCap)
	}
	tip := dst.Sub(tx.GasFeeCap, baseFee)
	if tip.Cmp(tx.GasTipCap) > 0 {
		tip.Set(tx.GasTipCap)
	}
	return tip.Add(tip, baseFee)
}

func (tx *MLDSA87Tx) rawSignatureValue() (signature []byte) {
	return tx.Signature
}

func (tx *MLDSA87Tx) rawPublicKeyValue() (publicKey []byte) {
	return tx.PublicKey
}

func (tx *MLDSA87Tx) setSignatureAndPublicKeyValues(chainID *big.Int, signature, publicKey []byte) {
	tx.ChainID, tx.PublicKey, tx.Signature = chainID, publicKey, signature
}

func (tx *MLDSA87Tx) encode(b *bytes.Buffer) error {
	return rlp.Encode(b, tx)
}

func (tx *MLDSA87Tx) decode(input []byte) error {
	return rlp.DecodeBytes(input, tx)
}
