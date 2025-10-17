package types

import (
	"bytes"
	"math/big"

	"github.com/theQRL/go-zond/common"
	"github.com/theQRL/go-zond/crypto/pqcrypto"
	"github.com/theQRL/go-zond/rlp"
)

//go:generate go run github.com/fjl/gencodec -type AccessTuple -out gen_access_tuple.go

// AccessList is an EIP-2930 access list.
type AccessList []AccessTuple

// AccessTuple is the element type of an access list.
type AccessTuple struct {
	Address     common.Address `json:"address"     gencodec:"required"`
	StorageKeys []common.Hash  `json:"storageKeys" gencodec:"required"`
}

// StorageKeys returns the total number of storage keys in the access list.
func (al AccessList) StorageKeys() int {
	sum := 0
	for _, tuple := range al {
		sum += len(tuple.StorageKeys)
	}
	return sum
}

// DynFeeExec is the EIP-1559 execution envelope (without auth).
type DynFeeExec struct {
	ChainID    *big.Int
	Nonce      uint64
	GasTipCap  *big.Int // a.k.a. maxPriorityFeePerGas
	GasFeeCap  *big.Int // a.k.a. maxFeePerGas
	Gas        uint64
	To         *common.Address `rlp:"nil"` // nil means contract creation
	Value      *big.Int
	Data       []byte
	AccessList AccessList
}

// accessors for innerTx.
func (df *DynFeeExec) chainID() *big.Int      { return df.ChainID }
func (df *DynFeeExec) accessList() AccessList { return df.AccessList }
func (df *DynFeeExec) data() []byte           { return df.Data }
func (df *DynFeeExec) gas() uint64            { return df.Gas }
func (df *DynFeeExec) gasFeeCap() *big.Int    { return df.GasFeeCap }
func (df *DynFeeExec) gasTipCap() *big.Int    { return df.GasTipCap }
func (df *DynFeeExec) gasPrice() *big.Int     { return df.GasFeeCap }
func (df *DynFeeExec) value() *big.Int        { return df.Value }
func (df *DynFeeExec) nonce() uint64          { return df.Nonce }
func (df *DynFeeExec) to() *common.Address    { return df.To }

// copy creates a deep copy of the transaction data and initializes all fields.
func (tx *DynFeeExec) copy() *DynFeeExec {
	cpy := &DynFeeExec{
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
	return cpy
}

func (tx *DynFeeExec) effectiveGasPrice(dst *big.Int, baseFee *big.Int) *big.Int {
	if baseFee == nil {
		return dst.Set(tx.GasFeeCap)
	}
	tip := dst.Sub(tx.GasFeeCap, baseFee)
	if tip.Cmp(tx.GasTipCap) > 0 {
		tip.Set(tx.GasTipCap)
	}
	return tip.Add(tip, baseFee)
}

// PQAuth holds the algorithm-specific pubkey and signature that authenticate
// the transaction.
type PQAuth struct {
	PubKey []byte
	Sig    []byte
}

type SPHINCS256sTx struct {
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
func (tx *SPHINCS256sTx) copy() TxData {
	cpy := &SPHINCS256sTx{
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
		PublicKey:  make([]byte, pqcrypto.PublicKeyLengthSPHINCS256s),
		Signature:  make([]byte, pqcrypto.SignatureLengthSPHINCS256s),
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
		copy(cpy.PublicKey[:pqcrypto.PublicKeyLengthSPHINCS256s], tx.PublicKey)
	}
	if tx.Signature != nil {
		copy(cpy.Signature[:pqcrypto.SignatureLengthSPHINCS256s], tx.Signature)
	}
	return cpy
}

// accessors for innerTx.
func (tx *SPHINCS256sTx) txType() byte           { return TxTypeSPHINCS256s }
func (tx *SPHINCS256sTx) chainID() *big.Int      { return tx.ChainID }
func (tx *SPHINCS256sTx) accessList() AccessList { return tx.AccessList }
func (tx *SPHINCS256sTx) data() []byte           { return tx.Data }
func (tx *SPHINCS256sTx) gas() uint64            { return tx.Gas }
func (tx *SPHINCS256sTx) gasFeeCap() *big.Int    { return tx.GasFeeCap }
func (tx *SPHINCS256sTx) gasTipCap() *big.Int    { return tx.GasTipCap }
func (tx *SPHINCS256sTx) gasPrice() *big.Int     { return tx.GasFeeCap }
func (tx *SPHINCS256sTx) value() *big.Int        { return tx.Value }
func (tx *SPHINCS256sTx) nonce() uint64          { return tx.Nonce }
func (tx *SPHINCS256sTx) to() *common.Address    { return tx.To }

// TODO(rgeraldes24)
func (tx *SPHINCS256sTx) effectiveGasPrice(dst *big.Int, baseFee *big.Int) *big.Int {
	if baseFee == nil {
		return dst.Set(tx.GasFeeCap)
	}
	tip := dst.Sub(tx.GasFeeCap, baseFee)
	if tip.Cmp(tx.GasTipCap) > 0 {
		tip.Set(tx.GasTipCap)
	}
	return tip.Add(tip, baseFee)
}

func (tx *SPHINCS256sTx) rawSignatureValue() (signature []byte) {
	return tx.Signature
}

func (tx *SPHINCS256sTx) rawPublicKeyValue() (publicKey []byte) {
	return tx.PublicKey
}

func (tx *SPHINCS256sTx) setSignatureAndPublicKeyValues(chainID *big.Int, signature, publicKey []byte) {
	tx.ChainID, tx.PublicKey, tx.Signature = chainID, publicKey, signature
}

func (tx *SPHINCS256sTx) encode(b *bytes.Buffer) error {
	return rlp.Encode(b, tx)
}

func (tx *SPHINCS256sTx) decode(input []byte) error {
	return rlp.DecodeBytes(input, tx)
}
