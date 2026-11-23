// Copyright 2015 The go-ethereum Authors
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

package common

import (
	"bytes"
	"database/sql/driver"
	"encoding/hex"
	"fmt"
	"math/big"
	"math/rand"
	"reflect"

	"github.com/theQRL/go-qrl/common/hexutil"
)

// Hash512Length is the byte length of state hashes and storage keys.
const Hash512Length = 64

var (
	hash512T   = reflect.TypeFor[Hash512]()
	MaxHash512 = HexToHash512("0x" + "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff")
)

// StorageKey is a full 512-bit storage slot identifier. It need not be a hash.
type StorageKey = Hash512

// StateHash is a Keccak-512 trie, account-path, storage-path, or code digest.
type StateHash = Hash512

// Hash512 represents the 64-byte Keccak-512 hash of arbitrary data.
type Hash512 [Hash512Length]byte

// BytesToHash512 sets b to hash.
// If b is larger than len(h), b will be cropped from the left.
func BytesToHash512(b []byte) Hash512 {
	var h Hash512
	h.SetBytes(b)
	return h
}

// BigToHash512 sets byte representation of b to hash.
// If b is larger than len(h), b will be cropped from the left.
func BigToHash512(b *big.Int) Hash512 { return BytesToHash512(b.Bytes()) }

// HexToHash512 sets byte representation of s to hash.
// If b is larger than len(h), b will be cropped from the left.
func HexToHash512(s string) Hash512 { return BytesToHash512(FromHex(s)) }

// Cmp compares two hashes.
func (h Hash512) Cmp(other Hash512) int {
	return bytes.Compare(h[:], other[:])
}

// Bytes gets the byte representation of the underlying hash.
func (h Hash512) Bytes() []byte { return h[:] }

// Big converts a hash to a big integer.
func (h Hash512) Big() *big.Int { return new(big.Int).SetBytes(h[:]) }

// Hex converts a hash to a hex string.
func (h Hash512) Hex() string { return hexutil.Encode(h[:]) }

// TerminalString implements log.TerminalStringer, formatting a string for console
// output during logging.
func (h Hash512) TerminalString() string {
	return fmt.Sprintf("%x..%x", h[:3], h[len(h)-3:])
}

// String implements the stringer interface and is used also by the logger when
// doing full logging into a file.
func (h Hash512) String() string {
	return h.Hex()
}

// Format implements fmt.Formatter.
// Hash512 supports the %v, %s, %q, %x, %X and %d format verbs.
func (h Hash512) Format(s fmt.State, c rune) {
	hexb := make([]byte, 2+len(h)*2)
	copy(hexb, "0x")
	hex.Encode(hexb[2:], h[:])

	switch c {
	case 'x', 'X':
		if !s.Flag('#') {
			hexb = hexb[2:]
		}
		if c == 'X' {
			hexb = bytes.ToUpper(hexb)
		}
		fallthrough
	case 'v', 's':
		s.Write(hexb)
	case 'q':
		q := []byte{'"'}
		s.Write(q)
		s.Write(hexb)
		s.Write(q)
	case 'd':
		fmt.Fprint(s, ([len(h)]byte)(h))
	default:
		fmt.Fprintf(s, "%%!%c(hash=%x)", c, h)
	}
}

// UnmarshalText parses a hash in hex syntax.
func (h *Hash512) UnmarshalText(input []byte) error {
	return hexutil.UnmarshalFixedText("Hash512", input, h[:])
}

// UnmarshalJSON parses a hash in hex syntax.
func (h *Hash512) UnmarshalJSON(input []byte) error {
	return hexutil.UnmarshalFixedJSON(hash512T, input, h[:])
}

// MarshalText returns the hex representation of h.
func (h Hash512) MarshalText() ([]byte, error) {
	return hexutil.Bytes(h[:]).MarshalText()
}

// SetBytes sets the hash to the value of b.
// If b is larger than len(h), b will be cropped from the left.
func (h *Hash512) SetBytes(b []byte) {
	if len(b) > len(h) {
		b = b[len(b)-Hash512Length:]
	}

	var value Hash512
	copy(value[Hash512Length-len(b):], b)
	*h = value
}

// Generate implements testing/quick.Generator.
func (h Hash512) Generate(rand *rand.Rand, size int) reflect.Value {
	m := rand.Intn(len(h))
	for i := len(h) - 1; i > m; i-- {
		h[i] = byte(rand.Uint32())
	}
	return reflect.ValueOf(h)
}

// Scan implements Scanner for database/sql.
func (h *Hash512) Scan(src any) error {
	srcB, ok := src.([]byte)
	if !ok {
		return fmt.Errorf("can't scan %T into Hash512", src)
	}
	if len(srcB) != Hash512Length {
		return fmt.Errorf("can't scan []byte of len %d into Hash512, want %d", len(srcB), Hash512Length)
	}
	copy(h[:], srcB)
	return nil
}

// Value implements valuer for database/sql.
func (h Hash512) Value() (driver.Value, error) {
	return h[:], nil
}

// ImplementsGraphQLType returns true if Hash512 implements the specified GraphQL type.
func (Hash512) ImplementsGraphQLType(name string) bool { return name == "Bytes64" }

// UnmarshalGraphQL unmarshals the provided GraphQL query data.
func (h *Hash512) UnmarshalGraphQL(input any) error {
	var err error
	switch input := input.(type) {
	case string:
		err = h.UnmarshalText([]byte(input))
	default:
		err = fmt.Errorf("unexpected type %T for Hash512", input)
	}
	return err
}

// UnprefixedHash512 allows marshaling a Hash512 without 0x prefix.
type UnprefixedHash512 Hash512

// UnmarshalText decodes the hash from hex. The 0x prefix is optional.
func (h *UnprefixedHash512) UnmarshalText(input []byte) error {
	return hexutil.UnmarshalFixedUnprefixedText("UnprefixedHash512", input, h[:])
}

// MarshalText encodes the hash as hex.
func (h UnprefixedHash512) MarshalText() ([]byte, error) {
	return []byte(hex.EncodeToString(h[:])), nil
}

// IsHexEncodedHash512 reports whether s encodes a 64-byte state hash.
func IsHexEncodedHash512(s string) bool {
	if has0xPrefix(s) {
		s = s[2:]
	}
	return len(s) == 2*Hash512Length && isHex(s)
}
