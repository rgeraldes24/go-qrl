// Copyright 2017 The go-ethereum Authors
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

package abi

import (
	"errors"
	"fmt"
	"math/big"
	"reflect"

	"github.com/theQRL/go-qrl/common"
)

var (
	// MaxUint256 is the maximum value that can be represented by a uint256.
	MaxUint256 = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 256), common.Big1)
	// MaxInt256 is the maximum value that can be represented by a int256.
	MaxInt256 = new(big.Int).Sub(new(big.Int).Lsh(common.Big1, 255), common.Big1)
)

// ReadInteger reads the integer based on its kind and returns the appropriate value.
func ReadInteger(typ Type, b []byte) (any, error) {
	ret := new(big.Int).SetBytes(b)

	if typ.T == UintTy {
		if ret.BitLen() > typ.Size {
			return nil, badUintError(typ.Size)
		}
		u64 := ret.Uint64()
		switch typ.Size {
		case 8:
			return byte(u64), nil
		case 16:
			return uint16(u64), nil
		case 32:
			return uint32(u64), nil
		case 64:
			return u64, nil
		default:
			return ret, nil
		}
	}

	// Signed integers must be sign-extended from their declared width to the
	// full ABI slot. This rejects values that fit the 64-byte slot but not the
	// declared intN type.
	var ok bool
	ret, ok = decodeSignedIntegerWord(typ.Size, ret)
	if !ok {
		return nil, badIntError(typ.Size)
	}
	i64, isi64 := ret.Int64(), ret.IsInt64()
	switch typ.Size {
	case 8:
		return int8(i64), nil
	case 16:
		return int16(i64), nil
	case 32:
		return int32(i64), nil
	case 64:
		if !isi64 {
			return nil, errBadInt64
		}
		return i64, nil
	default:
		return ret, nil
	}
}

// readBool reads a bool.
func readBool(word []byte) (bool, error) {
	for _, b := range word[:abiSlotBytes-1] {
		if b != 0 {
			return false, errBadBool
		}
	}
	switch word[abiSlotBytes-1] {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, errBadBool
	}
}

// ReadFixedBytes uses reflection to create a fixed array to be read from.
func ReadFixedBytes(t Type, word []byte) (any, error) {
	if t.T != FixedBytesTy {
		return nil, errors.New("abi: invalid type in call to make fixed byte array")
	}
	// convert
	array := reflect.New(t.GetType()).Elem()

	reflect.Copy(array, reflect.ValueOf(word[0:t.Size]))
	return array.Interface(), nil
}

// forEachUnpack iteratively unpack elements.
func forEachUnpack(t Type, output []byte, start, size int) (any, error) {
	if size < 0 {
		return nil, fmt.Errorf("cannot marshal input to array, size is negative (%d)", size)
	}
	if start+abiSlotBytes*size > len(output) {
		return nil, fmt.Errorf("abi: cannot marshal into go array: offset %d would go over slice boundary (len=%d)", len(output), start+abiSlotBytes*size)
	}

	// this value will become our slice or our array, depending on the type
	var refSlice reflect.Value

	switch t.T {
	case SliceTy:
		// declare our slice
		refSlice = reflect.MakeSlice(t.GetType(), size, size)
	case ArrayTy:
		// declare our array
		refSlice = reflect.New(t.GetType()).Elem()
	default:
		return nil, errors.New("abi: invalid type in array/slice unpacking stage")
	}

	// Arrays have packed elements, resulting in longer unpack steps.
	// Slices have one ABI slot per element (pointing to the contents).
	elemSize := getTypeSize(*t.Elem)

	for i, j := start, 0; j < size; i, j = i+elemSize, j+1 {
		inter, err := toGoType(i, *t.Elem, output)
		if err != nil {
			return nil, err
		}

		// append the item to our reflect slice
		refSlice.Index(j).Set(reflect.ValueOf(inter))
	}

	// return the interface
	return refSlice.Interface(), nil
}

func forTupleUnpack(t Type, output []byte) (any, error) {
	retval := reflect.New(t.GetType()).Elem()
	virtualArgs := 0
	for index, elem := range t.TupleElems {
		marshalledValue, err := toGoType((index+virtualArgs)*abiSlotBytes, *elem, output)
		if err != nil {
			return nil, err
		}
		if elem.T == ArrayTy && !isDynamicType(*elem) {
			// If we have a static array, like [3]uint256, these are coded as
			// just like uint256,uint256,uint256.
			// This means that we need to add two 'virtual' arguments when
			// we count the index from now on.
			//
			// Array values nested multiple levels deep are also encoded inline:
			// [2][3]uint256: uint256,uint256,uint256,uint256,uint256,uint256
			//
			// Calculate the full array size to get the correct offset for the next argument.
			// Decrement it by 1, as the normal index increment is still applied.
			virtualArgs += getTypeSize(*elem)/abiSlotBytes - 1
		} else if elem.T == TupleTy && !isDynamicType(*elem) {
			// If we have a static tuple, like (uint256, bool, uint256), these are
			// coded as just like uint256,bool,uint256
			virtualArgs += getTypeSize(*elem)/abiSlotBytes - 1
		}
		retval.Field(index).Set(reflect.ValueOf(marshalledValue))
	}
	return retval.Interface(), nil
}

// toGoType parses the output bytes and recursively assigns the value of these bytes
// into a go type with accordance with the ABI spec.
func toGoType(index int, t Type, output []byte) (any, error) {
	if index+abiSlotBytes > len(output) {
		return nil, fmt.Errorf("abi: cannot marshal in to go type: length insufficient %d require %d", len(output), index+abiSlotBytes)
	}

	var (
		returnOutput  []byte
		begin, length int
		err           error
	)

	// if we require a length prefix, find the beginning word and size returned.
	if t.requiresLengthPrefix() {
		begin, length, err = lengthPrefixPointsTo(index, output)
		if err != nil {
			return nil, err
		}
	} else {
		returnOutput = output[index : index+abiSlotBytes]
	}

	switch t.T {
	case TupleTy:
		if isDynamicType(t) {
			begin, err := tuplePointsTo(index, output)
			if err != nil {
				return nil, err
			}
			return forTupleUnpack(t, output[begin:])
		}
		return forTupleUnpack(t, output[index:])
	case SliceTy:
		return forEachUnpack(t, output[begin:], 0, length)
	case ArrayTy:
		if isDynamicType(*t.Elem) {
			offset, err := readABIWordAsInt(returnOutput, "array offset")
			if err != nil {
				return nil, err
			}
			if offset > len(output) {
				return nil, fmt.Errorf("abi: toGoType offset greater than output length: offset: %d, len(output): %d", offset, len(output))
			}
			return forEachUnpack(t, output[offset:], 0, t.Size)
		}
		return forEachUnpack(t, output[index:], 0, t.Size)
	case StringTy: // variable arrays are written at the end of the return bytes
		return string(output[begin : begin+length]), nil
	case IntTy, UintTy:
		return ReadInteger(t, returnOutput)
	case BoolTy:
		return readBool(returnOutput)
	case AddressTy:
		return common.BytesToAddress(returnOutput), nil
	case HashTy:
		return common.BytesToHash(returnOutput), nil
	case BytesTy:
		return output[begin : begin+length], nil
	case FixedBytesTy:
		return ReadFixedBytes(t, returnOutput)
	case FunctionTy:
		return nil, ErrUnsupportedFunctionType
	default:
		return nil, fmt.Errorf("abi: unknown type %v", t.T)
	}
}

func readABIWordAsInt(word []byte, context string) (int, error) {
	if len(word) != abiSlotBytes {
		return 0, fmt.Errorf("abi: invalid %s word length %d", context, len(word))
	}
	n := new(big.Int).SetBytes(word)
	maxInt := int(^uint(0) >> 1)
	if n.Cmp(big.NewInt(int64(maxInt))) > 0 {
		return 0, fmt.Errorf("abi: %s larger than int: %v", context, n)
	}
	return int(n.Int64()), nil
}

// lengthPrefixPointsTo interprets an ABI slot as an offset and then determines which indices to look to decode the type.
func lengthPrefixPointsTo(index int, output []byte) (start int, length int, err error) {
	offset, err := readABIWordAsInt(output[index:index+abiSlotBytes], "offset")
	if err != nil {
		return 0, 0, err
	}
	if offset > len(output)-abiSlotBytes {
		return 0, 0, fmt.Errorf("abi: cannot marshal in to go slice: offset %d would go over slice boundary (len=%d)", offset, len(output))
	}
	offsetEnd := offset + abiSlotBytes
	length, err = readABIWordAsInt(output[offset:offsetEnd], "length")
	if err != nil {
		return 0, 0, err
	}
	if length > len(output)-offsetEnd {
		return 0, 0, fmt.Errorf("abi: cannot marshal in to go type: length %d would go over slice boundary (len=%d)", length, len(output))
	}
	return offsetEnd, length, nil
}

// tuplePointsTo resolves the location reference for dynamic tuple.
func tuplePointsTo(index int, output []byte) (start int, err error) {
	offset, err := readABIWordAsInt(output[index:index+abiSlotBytes], "tuple offset")
	if err != nil {
		return 0, err
	}
	if offset > len(output) {
		return 0, fmt.Errorf("abi: cannot marshal in to go slice: offset %d would go over slice boundary (len=%d)", offset, len(output))
	}
	return offset, nil
}
