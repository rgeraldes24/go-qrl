// Copyright 2026 The go-qrl Authors
// This file is part of go-qrl.

package abi

import (
	"fmt"
	"math/big"
)

func validateIntegerSize(t string, size int) error {
	if size == 0 || size > abiSlotBits || size%8 != 0 {
		return fmt.Errorf("unsupported arg type: %s", t)
	}
	return nil
}

func integerOverflowError(t Type, value *big.Int) error {
	return fmt.Errorf("abi: cannot use %s as type %s: value overflows %d bits", value, t, t.Size)
}

func checkIntegerRange(t Type, value *big.Int) error {
	switch t.T {
	case UintTy:
		if value.Sign() < 0 {
			return errInvalidSign
		}
		if value.BitLen() > t.Size {
			return integerOverflowError(t, value)
		}
	case IntTy:
		min, max := signedIntegerBounds(t.Size)
		if value.Cmp(min) < 0 || value.Cmp(max) > 0 {
			return integerOverflowError(t, value)
		}
	}
	return nil
}

func signedIntegerBounds(bits int) (min, max *big.Int) {
	limit := new(big.Int).Lsh(big.NewInt(1), uint(bits-1))
	min = new(big.Int).Neg(limit)
	max = new(big.Int).Sub(new(big.Int).Set(limit), big.NewInt(1))
	return min, max
}

func twoPow(bits int) *big.Int {
	return new(big.Int).Lsh(big.NewInt(1), uint(bits))
}

func decodeSignedIntegerWord(size int, word *big.Int) (*big.Int, bool) {
	if size <= 0 || size > abiSlotBits {
		return nil, false
	}
	signBit := word.Bit(size - 1)
	if signBit == 0 {
		if word.BitLen() > size {
			return nil, false
		}
		return new(big.Int).Set(word), true
	}
	highBits := abiSlotBits - size
	high := new(big.Int).Rsh(new(big.Int).Set(word), uint(size))
	expectedHigh := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), uint(highBits)), big.NewInt(1))
	if high.Cmp(expectedHigh) != 0 {
		return nil, false
	}
	return new(big.Int).Sub(new(big.Int).Set(word), twoPow(abiSlotBits)), true
}

func badUintError(size int) error {
	switch size {
	case 8:
		return errBadUint8
	case 16:
		return errBadUint16
	case 32:
		return errBadUint32
	case 64:
		return errBadUint64
	default:
		return fmt.Errorf("abi: improperly encoded uint%d value", size)
	}
}

func badIntError(size int) error {
	switch size {
	case 8:
		return errBadInt8
	case 16:
		return errBadInt16
	case 32:
		return errBadInt32
	case 64:
		return errBadInt64
	default:
		return fmt.Errorf("abi: improperly encoded int%d value", size)
	}
}
