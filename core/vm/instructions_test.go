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

package vm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/state"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/params"
)

type TwoOperandTestcase struct {
	X        string
	Y        string
	Expected string
}

type twoOperandParams struct {
	x string
	y string
}

var alphabetSoup = "ABCDEF090807060504030201ffffffffffffffffffffffffffffffffffffffff"
var commonParams []*twoOperandParams
var twoOpMethods map[string]executionFunc

func init() {
	// Params is a list of common edgecases that should be used for some common tests
	params := []string{
		"0000000000000000000000000000000000000000000000000000000000000000", // 0
		"0000000000000000000000000000000000000000000000000000000000000001", // +1
		"0000000000000000000000000000000000000000000000000000000000000005", // +5
		"7ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe", // + max -1
		"7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", // + max
		"8000000000000000000000000000000000000000000000000000000000000000", // - max
		"8000000000000000000000000000000000000000000000000000000000000001", // - max+1
		"fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffb", // - 5
		"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", // - 1
	}
	// Params are combined so each param is used on each 'side'
	commonParams = make([]*twoOperandParams, len(params)*len(params))
	for i, x := range params {
		for j, y := range params {
			commonParams[i*len(params)+j] = &twoOperandParams{x, y}
		}
	}
	twoOpMethods = map[string]executionFunc{
		"add":     opAdd,
		"sub":     opSub,
		"mul":     opMul,
		"div":     opDiv,
		"sdiv":    opSdiv,
		"mod":     opMod,
		"smod":    opSmod,
		"exp":     opExp,
		"signext": opSignExtend,
		"lt":      opLt,
		"gt":      opGt,
		"slt":     opSlt,
		"sgt":     opSgt,
		"eq":      opEq,
		"and":     opAnd,
		"or":      opOr,
		"xor":     opXor,
		"byte":    opByte,
		"shl":     opSHL,
		"shr":     opSHR,
		"sar":     opSAR,
	}
}

func testTwoOperandOp(t *testing.T, tests []TwoOperandTestcase, opFn executionFunc, name string) {
	var (
		env             = NewQRVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		stack           = newstack()
		pc              = uint64(0)
		qrvmInterpreter = env.interpreter
	)

	for i, test := range tests {
		x := new(uint512.Int).SetBytes(common.Hex2Bytes(test.X))
		y := new(uint512.Int).SetBytes(common.Hex2Bytes(test.Y))
		expected := new(uint512.Int).SetBytes(common.Hex2Bytes(test.Expected))
		stack.push(x)
		stack.push(y)
		opFn(&pc, qrvmInterpreter, &ScopeContext{nil, stack, nil})
		if len(stack.data) != 1 {
			t.Errorf("Expected one item on stack after %v, got %d: ", name, len(stack.data))
		}
		actual := stack.pop()

		if actual.Cmp(expected) != 0 {
			t.Errorf("Testcase %v %d, %v(%x, %x): expected  %x, got %x", name, i, name, x, y, expected, actual)
		}
	}
}

func TestByteOp(t *testing.T) {
	// BYTE indexes are relative to the 64-byte VM word. The 32-byte input hex
	// below is right-aligned by SetBytes, so the meaningful bytes sit at
	// positions 0x20..0x3F; indexes 0x00..0x1F always yield zero and 0x40+
	// saturates to zero.
	tests := []TwoOperandTestcase{
		{"ABCDEF0908070605040302010000000000000000000000000000000000000000", "20", "AB"},
		{"ABCDEF0908070605040302010000000000000000000000000000000000000000", "21", "CD"},
		{"00CDEF090807060504030201ffffffffffffffffffffffffffffffffffffffff", "20", "00"},
		{"00CDEF090807060504030201ffffffffffffffffffffffffffffffffffffffff", "21", "CD"},
		{"0000000000000000000000000000000000000000000000000000000000102030", "3F", "30"},
		{"0000000000000000000000000000000000000000000000000000000000102030", "3E", "20"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "40", "00"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "FFFFFFFFFFFFFFFF", "00"},
	}
	testTwoOperandOp(t, tests, opByte, "byte")
}

func TestSHL(t *testing.T) {
	// SHL on the 512-bit VM word: the shift limit is 512 and shifts that
	// previously wrapped past the 256-bit boundary now settle in the upper
	// half of the word, so the 64-byte expected values expose the full
	// 512-bit result.
	tests := []TwoOperandTestcase{
		{"0000000000000000000000000000000000000000000000000000000000000001", "01", "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000002"},
		{"0000000000000000000000000000000000000000000000000000000000000001", "ff", "00000000000000000000000000000000000000000000000000000000000000008000000000000000000000000000000000000000000000000000000000000000"},
		{"0000000000000000000000000000000000000000000000000000000000000001", "0100", "00000000000000000000000000000000000000000000000000000000000000010000000000000000000000000000000000000000000000000000000000000000"},
		{"0000000000000000000000000000000000000000000000000000000000000001", "0101", "00000000000000000000000000000000000000000000000000000000000000020000000000000000000000000000000000000000000000000000000000000000"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "00", "0000000000000000000000000000000000000000000000000000000000000000ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "01", "0000000000000000000000000000000000000000000000000000000000000001fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "ff", "7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff8000000000000000000000000000000000000000000000000000000000000000"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "0100", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff0000000000000000000000000000000000000000000000000000000000000000"},
		{"0000000000000000000000000000000000000000000000000000000000000000", "01", "00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000"},
		{"7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "01", "0000000000000000000000000000000000000000000000000000000000000000fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe"},
	}
	testTwoOperandOp(t, tests, opSHL, "shl")
}

func TestSHR(t *testing.T) {
	// Testcases from https://github.com/ethereum/EIPs/blob/master/EIPS/eip-145.md#shr-logical-shift-right
	tests := []TwoOperandTestcase{
		{"0000000000000000000000000000000000000000000000000000000000000001", "00", "0000000000000000000000000000000000000000000000000000000000000001"},
		{"0000000000000000000000000000000000000000000000000000000000000001", "01", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"8000000000000000000000000000000000000000000000000000000000000000", "01", "4000000000000000000000000000000000000000000000000000000000000000"},
		{"8000000000000000000000000000000000000000000000000000000000000000", "ff", "0000000000000000000000000000000000000000000000000000000000000001"},
		{"8000000000000000000000000000000000000000000000000000000000000000", "0100", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"8000000000000000000000000000000000000000000000000000000000000000", "0101", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "00", "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "01", "7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "ff", "0000000000000000000000000000000000000000000000000000000000000001"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "0100", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"0000000000000000000000000000000000000000000000000000000000000000", "01", "0000000000000000000000000000000000000000000000000000000000000000"},
	}
	testTwoOperandOp(t, tests, opSHR, "shr")
}

func TestSAR(t *testing.T) {
	// SAR on the 512-bit VM word: the sign bit is at position 511, so the
	// classic "negative" 32-byte fixtures (byte 0 high-bit set) actually
	// represent positive numbers in the right-aligned 512-bit word, and the
	// shift inputs that previously saturated to -1 now produce 0.
	tests := []TwoOperandTestcase{
		{"0000000000000000000000000000000000000000000000000000000000000001", "00", "0000000000000000000000000000000000000000000000000000000000000001"},
		{"0000000000000000000000000000000000000000000000000000000000000001", "01", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"8000000000000000000000000000000000000000000000000000000000000000", "01", "00000000000000000000000000000000000000000000000000000000000000004000000000000000000000000000000000000000000000000000000000000000"},
		{"8000000000000000000000000000000000000000000000000000000000000000", "ff", "0000000000000000000000000000000000000000000000000000000000000001"},
		{"8000000000000000000000000000000000000000000000000000000000000000", "0100", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"8000000000000000000000000000000000000000000000000000000000000000", "0101", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "00", "0000000000000000000000000000000000000000000000000000000000000000ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "01", "00000000000000000000000000000000000000000000000000000000000000007fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "ff", "0000000000000000000000000000000000000000000000000000000000000001"},
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "0100", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"0000000000000000000000000000000000000000000000000000000000000000", "01", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"4000000000000000000000000000000000000000000000000000000000000000", "fe", "0000000000000000000000000000000000000000000000000000000000000001"},
		{"7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "f8", "000000000000000000000000000000000000000000000000000000000000007f"},
		{"7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "fe", "0000000000000000000000000000000000000000000000000000000000000001"},
		{"7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "ff", "0000000000000000000000000000000000000000000000000000000000000000"},
		{"7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff", "0100", "0000000000000000000000000000000000000000000000000000000000000000"},
	}

	testTwoOperandOp(t, tests, opSAR, "sar")
}

func TestCLZ(t *testing.T) {
	// CLZ counts the leading zero bits of the 512-bit VM word. A value below
	// 2^256 therefore gives 256 more than the EIP-7939 result, and zero gives
	// 512.
	var (
		env             = NewQRVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		stack           = newstack()
		pc              = uint64(0)
		qrvmInterpreter = env.interpreter
	)
	check := func(x *uint512.Int, want uint64) {
		t.Helper()
		stack.push(x)
		opCLZ(&pc, qrvmInterpreter, &ScopeContext{nil, stack, nil})
		if len(stack.data) != 1 {
			t.Fatalf("clz(%x): expected one item on stack, got %d", x, len(stack.data))
		}
		if have := stack.pop(); !have.IsUint64() || have.Uint64() != want {
			t.Errorf("clz(%x): have %d, want %d", x, have, want)
		}
	}
	for _, tc := range []struct {
		x    string
		want uint64
	}{
		{"00", 512},
		{"01", 511},
		{"80" + strings.Repeat("00", 31), 256}, // 2^255
		{"01" + strings.Repeat("00", 32), 255}, // 2^256
		{"80" + strings.Repeat("00", 63), 0},   // 2^511
		{strings.Repeat("ff", 64), 0},          // 2^512 - 1
	} {
		check(new(uint512.Int).SetBytes(common.Hex2Bytes(tc.x)), tc.want)
	}
	// For every bit position i, 2^i is the smallest value whose top bit is i
	// and 2^(i+1) - 1 is the largest.
	one := uint512.NewInt(1)
	for i := uint(0); i < WordBits; i++ {
		want := uint64(WordBits - 1 - i)
		low := new(uint512.Int).Lsh(one, i)
		check(low, want)
		high := new(uint512.Int).Lsh(one, i+1)
		check(high.Sub(high, one), want)
	}
}

func TestAddMod(t *testing.T) {
	var (
		env             = NewQRVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		stack           = newstack()
		qrvmInterpreter = NewQRVMInterpreter(env)
		pc              = uint64(0)
	)
	tests := []struct {
		x        string
		y        string
		z        string
		expected string
	}{
		{"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			"fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe",
			"ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			"fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffe",
		},
	}
	// x + y = 0x1fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffd
	// in 256 bit repr, fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffd

	for i, test := range tests {
		x := new(uint512.Int).SetBytes(common.Hex2Bytes(test.x))
		y := new(uint512.Int).SetBytes(common.Hex2Bytes(test.y))
		z := new(uint512.Int).SetBytes(common.Hex2Bytes(test.z))
		expected := new(uint512.Int).SetBytes(common.Hex2Bytes(test.expected))
		stack.push(z)
		stack.push(y)
		stack.push(x)
		opAddmod(&pc, qrvmInterpreter, &ScopeContext{nil, stack, nil})
		actual := stack.pop()
		if actual.Cmp(expected) != 0 {
			t.Errorf("Testcase %d, expected  %x, got %x", i, expected, actual)
		}
	}
}

// utility function to fill the json-file with testcases
// Enable this test to generate the 'testcases_xx.json' files
func TestWriteExpectedValues(t *testing.T) {
	if os.Getenv("REGEN") == "" {
		t.Skip("Enable this test to create json test cases.")
	}

	// getResult is a convenience function to generate the expected values
	getResult := func(args []*twoOperandParams, opFn executionFunc) []TwoOperandTestcase {
		var (
			env         = NewQRVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
			stack       = newstack()
			pc          = uint64(0)
			interpreter = env.interpreter
		)
		result := make([]TwoOperandTestcase, len(args))
		for i, param := range args {
			x := new(uint512.Int).SetBytes(common.Hex2Bytes(param.x))
			y := new(uint512.Int).SetBytes(common.Hex2Bytes(param.y))
			stack.push(x)
			stack.push(y)
			opFn(&pc, interpreter, &ScopeContext{nil, stack, nil})
			actual := stack.pop()
			result[i] = TwoOperandTestcase{param.x, param.y, fmt.Sprintf("%0128x", actual)}
		}
		return result
	}

	for name, method := range twoOpMethods {
		data, err := json.Marshal(getResult(commonParams, method))
		if err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(fmt.Sprintf("testdata/testcases_%v.json", name), data, 0644)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestJsonTestcases runs through all the testcases defined as json-files
func TestJsonTestcases(t *testing.T) {
	for name := range twoOpMethods {
		data, err := os.ReadFile(fmt.Sprintf("testdata/testcases_%v.json", name))
		if err != nil {
			t.Fatal("Failed to read file", err)
		}
		var testcases []TwoOperandTestcase
		json.Unmarshal(data, &testcases)
		testTwoOperandOp(t, testcases, twoOpMethods[name], name)
	}
}

func opBenchmark(bench *testing.B, op executionFunc, args ...string) {
	var (
		env             = NewQRVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		stack           = newstack()
		scope           = &ScopeContext{nil, stack, nil}
		qrvmInterpreter = NewQRVMInterpreter(env)
	)

	env.interpreter = qrvmInterpreter
	// convert args
	intArgs := make([]*uint512.Int, len(args))
	for i, arg := range args {
		intArgs[i] = new(uint512.Int).SetBytes(common.Hex2Bytes(arg))
	}
	pc := uint64(0)
	for bench.Loop() {
		for _, arg := range intArgs {
			stack.push(arg)
		}
		op(&pc, qrvmInterpreter, scope)
		stack.pop()
	}

	for i, arg := range args {
		want := new(uint512.Int).SetBytes(common.Hex2Bytes(arg))
		if have := intArgs[i]; !want.Eq(have) {
			bench.Fatalf("input #%d mutated, have %x want %x", i, have, want)
		}
	}
}

func BenchmarkOpAdd64(b *testing.B) {
	x := "ffffffff"
	y := "fd37f3e2bba2c4f"

	opBenchmark(b, opAdd, x, y)
}

func BenchmarkOpAdd128(b *testing.B) {
	x := "ffffffffffffffff"
	y := "f5470b43c6549b016288e9a65629687"

	opBenchmark(b, opAdd, x, y)
}

func BenchmarkOpAdd256(b *testing.B) {
	x := "0802431afcbce1fc194c9eaa417b2fb67dc75a95db0bc7ec6b1c8af11df6a1da9"
	y := "a1f5aac137876480252e5dcac62c354ec0d42b76b0642b6181ed099849ea1d57"

	opBenchmark(b, opAdd, x, y)
}

func BenchmarkOpSub64(b *testing.B) {
	x := "51022b6317003a9d"
	y := "a20456c62e00753a"

	opBenchmark(b, opSub, x, y)
}

func BenchmarkOpSub128(b *testing.B) {
	x := "4dde30faaacdc14d00327aac314e915d"
	y := "9bbc61f5559b829a0064f558629d22ba"

	opBenchmark(b, opSub, x, y)
}

func BenchmarkOpSub256(b *testing.B) {
	x := "4bfcd8bb2ac462735b48a17580690283980aa2d679f091c64364594df113ea37"
	y := "97f9b1765588c4e6b69142eb00d20507301545acf3e1238c86c8b29be227d46e"

	opBenchmark(b, opSub, x, y)
}

func BenchmarkOpMul(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opMul, x, y)
}

func BenchmarkOpDiv256(b *testing.B) {
	x := "ff3f9014f20db29ae04af2c2d265de17"
	y := "fe7fb0d1f59dfe9492ffbf73683fd1e870eec79504c60144cc7f5fc2bad1e611"
	opBenchmark(b, opDiv, x, y)
}

func BenchmarkOpDiv128(b *testing.B) {
	x := "fdedc7f10142ff97"
	y := "fbdfda0e2ce356173d1993d5f70a2b11"
	opBenchmark(b, opDiv, x, y)
}

func BenchmarkOpDiv64(b *testing.B) {
	x := "fcb34eb3"
	y := "f97180878e839129"
	opBenchmark(b, opDiv, x, y)
}

func BenchmarkOpSdiv(b *testing.B) {
	x := "ff3f9014f20db29ae04af2c2d265de17"
	y := "fe7fb0d1f59dfe9492ffbf73683fd1e870eec79504c60144cc7f5fc2bad1e611"

	opBenchmark(b, opSdiv, x, y)
}

func BenchmarkOpMod(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opMod, x, y)
}

func BenchmarkOpSmod(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opSmod, x, y)
}

func BenchmarkOpExp(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opExp, x, y)
}

func BenchmarkOpSignExtend(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opSignExtend, x, y)
}

func BenchmarkOpLt(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opLt, x, y)
}

func BenchmarkOpGt(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opGt, x, y)
}

func BenchmarkOpSlt(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opSlt, x, y)
}

func BenchmarkOpSgt(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opSgt, x, y)
}

func BenchmarkOpEq(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opEq, x, y)
}
func BenchmarkOpEq2(b *testing.B) {
	x := "FBCDEF090807060504030201ffffffffFBCDEF090807060504030201ffffffff"
	y := "FBCDEF090807060504030201ffffffffFBCDEF090807060504030201fffffffe"
	opBenchmark(b, opEq, x, y)
}
func BenchmarkOpAnd(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opAnd, x, y)
}

func BenchmarkOpOr(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opOr, x, y)
}

func BenchmarkOpXor(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opXor, x, y)
}

func BenchmarkOpByte(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup

	opBenchmark(b, opByte, x, y)
}

func BenchmarkOpAddmod(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup
	z := alphabetSoup

	opBenchmark(b, opAddmod, x, y, z)
}

func BenchmarkOpMulmod(b *testing.B) {
	x := alphabetSoup
	y := alphabetSoup
	z := alphabetSoup

	opBenchmark(b, opMulmod, x, y, z)
}

func BenchmarkOpSHL(b *testing.B) {
	x := "FBCDEF090807060504030201ffffffffFBCDEF090807060504030201ffffffff"
	y := "ff"

	opBenchmark(b, opSHL, x, y)
}
func BenchmarkOpSHR(b *testing.B) {
	x := "FBCDEF090807060504030201ffffffffFBCDEF090807060504030201ffffffff"
	y := "ff"

	opBenchmark(b, opSHR, x, y)
}
func BenchmarkOpSAR(b *testing.B) {
	x := "FBCDEF090807060504030201ffffffffFBCDEF090807060504030201ffffffff"
	y := "ff"

	opBenchmark(b, opSAR, x, y)
}
func BenchmarkOpIsZero(b *testing.B) {
	x := "FBCDEF090807060504030201ffffffffFBCDEF090807060504030201ffffffff"
	opBenchmark(b, opIszero, x)
}

func TestOpMstore(t *testing.T) {
	var (
		env             = NewQRVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		stack           = newstack()
		mem             = NewMemory()
		qrvmInterpreter = NewQRVMInterpreter(env)
	)

	env.interpreter = qrvmInterpreter
	mem.Resize(64)
	pc := uint64(0)
	// MSTORE now writes a full 64-byte VM word. The 32-byte fixture is
	// right-aligned by SetBytes, so the expected memory contents have a
	// 32-byte zero prefix followed by the original hex.
	v := "abcdef00000000000000abba000000000deaf000000c0de00100000000133700"
	stack.push(new(uint512.Int).SetBytes(common.Hex2Bytes(v)))
	stack.push(new(uint512.Int))
	opMstore(&pc, qrvmInterpreter, &ScopeContext{mem, stack, nil})
	wantFirst := "0000000000000000000000000000000000000000000000000000000000000000" + v
	if got := common.Bytes2Hex(mem.GetCopy(0, 64)); got != wantFirst {
		t.Fatalf("Mstore fail, got %v, expected %v", got, wantFirst)
	}
	stack.push(new(uint512.Int).SetUint64(0x1))
	stack.push(new(uint512.Int))
	opMstore(&pc, qrvmInterpreter, &ScopeContext{mem, stack, nil})
	wantOverwrite := "0000000000000000000000000000000000000000000000000000000000000000" +
		"0000000000000000000000000000000000000000000000000000000000000001"
	if got := common.Bytes2Hex(mem.GetCopy(0, 64)); got != wantOverwrite {
		t.Fatalf("Mstore failed to overwrite previous value, got %v", got)
	}
}

func TestOpTstore(t *testing.T) {
	var (
		statedb, _      = state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
		env             = NewQRVM(BlockContext{}, TxContext{}, statedb, params.TestChainConfig, Config{})
		stack           = newstack()
		mem             = NewMemory()
		qrvmInterpreter = NewQRVMInterpreter(env)
		caller          = common.Address{}
		to              = common.Address{1}
		contract        = NewContract(AccountRef(caller), AccountRef(to), new(big.Int), 0)
		scopeContext    = ScopeContext{mem, stack, contract}
		// A full 64-byte VM word, so both halves of the value are covered.
		value = common.Hex2Bytes("abcdef00000000000000abba000000000deaf000000c0de00100000000133700" +
			"00deadbeef00000000000000000000000000000000000000000000000000cafe")
	)

	// Add a stateObject for the caller and the contract being called
	statedb.CreateAccount(caller)
	statedb.CreateAccount(to)

	env.interpreter = qrvmInterpreter
	pc := uint64(0)
	// push the value to the stack
	stack.push(new(uint512.Int).SetBytes(value))
	// push the location to the stack
	stack.push(new(uint512.Int))
	opTstore(&pc, qrvmInterpreter, &scopeContext)
	// there should be no elements on the stack after TSTORE
	if stack.len() != 0 {
		t.Fatal("stack wrong size")
	}
	// push the location to the stack
	stack.push(new(uint512.Int))
	opTload(&pc, qrvmInterpreter, &scopeContext)
	// there should be one element on the stack after TLOAD
	if stack.len() != 1 {
		t.Fatal("stack wrong size")
	}
	val := stack.peek()
	if !bytes.Equal(val.Bytes(), value) {
		t.Fatal("incorrect element read from transient storage")
	}

	// TSTORE must be rejected in a static call and leave the value untouched
	qrvmInterpreter.readOnly = true
	stack.push(new(uint512.Int).SetUint64(1))
	stack.push(new(uint512.Int))
	if _, err := opTstore(&pc, qrvmInterpreter, &scopeContext); err != ErrWriteProtection {
		t.Fatalf("TSTORE in a static call: have error %v, want %v", err, ErrWriteProtection)
	}
	if got := statedb.GetTransientState(to, common.Hash512{}); !bytes.Equal(got.Bytes(), value) {
		t.Fatal("transient storage modified in a static call")
	}
}

func TestOpMCopy(t *testing.T) {
	// The cases follow https://eips.ethereum.org/EIPS/eip-5656#test-cases,
	// rescaled to the 64-byte VM word: memory grows, and copy gas is charged,
	// per 64 bytes.
	var (
		ascending = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" +
			"202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f"
		zeroWord = strings.Repeat("00", 64)
	)
	for i, tc := range []struct {
		dst, src, len string
		pre           string
		want          string
		wantGas       uint64
		wantOverflow  bool
	}{
		{ // MCOPY 0 64 64 - copy 64 bytes from offset 64 to offset 0.
			dst: "0x0", src: "0x40", len: "0x40",
			pre:     zeroWord + ascending,
			want:    ascending + ascending,
			wantGas: 6,
		},
		{ // MCOPY 0 0 64 - copy 64 bytes from offset 0 to offset 0.
			dst: "0x0", src: "0x0", len: "0x40",
			pre:     strings.Repeat("01", 64),
			want:    strings.Repeat("01", 64),
			wantGas: 6,
		},
		{ // MCOPY 0 1 8 - copy 8 bytes from offset 1 to offset 0 (overlapping).
			dst: "0x0", src: "0x1", len: "0x8",
			pre:     "000102030405060708" + strings.Repeat("00", 55),
			want:    "010203040506070808" + strings.Repeat("00", 55),
			wantGas: 6,
		},
		{ // MCOPY 1 0 8 - copy 8 bytes from offset 0 to offset 1 (overlapping).
			dst: "0x1", src: "0x0", len: "0x8",
			pre:     "000102030405060708" + strings.Repeat("00", 55),
			want:    "000001020304050607" + strings.Repeat("00", 55),
			wantGas: 6,
		},
		// The cases below are not in the EIP.
		{ // MCOPY 0xFFFFFFFFFFFF 0xFFFFFFFFFFFF 0 - copy zero bytes from out-of-bounds index (overlapping).
			dst: "0xFFFFFFFFFFFF", src: "0xFFFFFFFFFFFF", len: "0x0",
			pre:     "11",
			want:    "11",
			wantGas: 3,
		},
		{ // MCOPY 0xFFFFFFFFFFFF 0 0 - copy zero bytes from start of mem to out-of-bounds.
			dst: "0xFFFFFFFFFFFF", src: "0x0", len: "0x0",
			pre:     "11",
			want:    "11",
			wantGas: 3,
		},
		{ // MCOPY 0 0xFFFFFFFFFFFF 0 - copy zero bytes from out-of-bounds to start of mem.
			dst: "0x0", src: "0xFFFFFFFFFFFF", len: "0x0",
			pre:     "11",
			want:    "11",
			wantGas: 3,
		},
		{ // MCOPY - copy 1 from space outside of uint64 space.
			dst: "0x0", src: "0x10000000000000000", len: "0x1",
			pre:          "00",
			wantOverflow: true,
		},
		{ // MCOPY - copy 1 from 0 to space outside of uint64.
			dst: "0x10000000000000000", src: "0x0", len: "0x1",
			pre:          "00",
			wantOverflow: true,
		},
		{ // MCOPY - copy a length outside of uint64.
			dst: "0x0", src: "0x0", len: "0x10000000000000000",
			pre:          "00",
			wantOverflow: true,
		},
		{ // MCOPY - copy nothing from 0 to space outside of uint64.
			dst: "0x10000000000000000", src: "0x0", len: "0x0",
			pre:     "",
			want:    "",
			wantGas: 3,
		},
		{ // MCOPY - copy 1 from 0x40 to 0x10, with no prior allocated mem: the source needs two words.
			dst: "0x10", src: "0x40", len: "0x1",
			pre:     "",
			want:    zeroWord + zeroWord,
			wantGas: 12,
		},
		{ // MCOPY - copy 1 from 0x20 to 0x10, with no prior allocated mem: one word holds both.
			dst: "0x10", src: "0x20", len: "0x1",
			pre:     "",
			want:    zeroWord,
			wantGas: 9,
		},
		{ // MCOPY - copy 65 bytes, with no prior allocated mem: two words expanded, two words copied.
			dst: "0x0", src: "0x0", len: "0x41",
			pre:     "",
			want:    zeroWord + zeroWord,
			wantGas: 15,
		},
	} {
		var (
			env             = NewQRVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
			stack           = newstack()
			pc              = uint64(0)
			qrvmInterpreter = NewQRVMInterpreter(env)
		)
		data := common.FromHex(tc.pre)
		// Set pre
		mem := NewMemory()
		mem.Resize(uint64(len(data)))
		mem.Set(0, uint64(len(data)), data)
		// Push stack args
		stack.push(new(uint512.Int).SetBytes(common.FromHex(tc.len)))
		stack.push(new(uint512.Int).SetBytes(common.FromHex(tc.src)))
		stack.push(new(uint512.Int).SetBytes(common.FromHex(tc.dst)))
		// Calc mem expansion
		memSize, overflow := memoryMcopy(stack)
		if overflow != tc.wantOverflow {
			t.Errorf("case %d: memory size overflow: have %v want %v", i, overflow, tc.wantOverflow)
		}
		if overflow {
			continue
		}
		memorySize, overflow := math.SafeMul(toWordSize(memSize), uint64(WordBytes))
		if overflow {
			t.Errorf("case %d: %v", i, ErrGasUintOverflow)
		}
		// and the dynamic cost
		var haveGas uint64
		if dynamicCost, err := gasMcopy(env, nil, stack, mem, memorySize); err != nil {
			t.Errorf("case %d: %v", i, err)
		} else {
			haveGas = GasFastestStep + dynamicCost
		}
		// Expand mem
		if memorySize > 0 {
			mem.Resize(memorySize)
		}
		// Do the copy
		opMcopy(&pc, qrvmInterpreter, &ScopeContext{mem, stack, nil})
		want := common.FromHex(tc.want)
		if have := mem.store; !bytes.Equal(want, have) {
			t.Errorf("case %d: \nwant: %#x\nhave: %#x\n", i, want, have)
		}
		if haveGas != tc.wantGas {
			t.Errorf("case %d: gas wrong, want %d have %d\n", i, tc.wantGas, haveGas)
		}
		if stack.len() != 0 {
			t.Errorf("case %d: stack not empty after MCOPY, have %d items", i, stack.len())
		}
	}
}

func BenchmarkOpMstore(bench *testing.B) {
	var (
		env             = NewQRVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		stack           = newstack()
		mem             = NewMemory()
		qrvmInterpreter = NewQRVMInterpreter(env)
	)

	env.interpreter = qrvmInterpreter
	mem.Resize(64)
	pc := uint64(0)
	memStart := new(uint512.Int)
	value := new(uint512.Int).SetUint64(0x1337)

	for bench.Loop() {
		stack.push(value)
		stack.push(memStart)
		opMstore(&pc, qrvmInterpreter, &ScopeContext{mem, stack, nil})
	}
}

func BenchmarkOpKeccak256(bench *testing.B) {
	var (
		env             = NewQRVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		stack           = newstack()
		mem             = NewMemory()
		qrvmInterpreter = NewQRVMInterpreter(env)
	)
	env.interpreter = qrvmInterpreter
	mem.Resize(32)
	pc := uint64(0)
	start := new(uint512.Int)

	for bench.Loop() {
		stack.push(uint512.NewInt(32))
		stack.push(start)
		opKeccak256(&pc, qrvmInterpreter, &ScopeContext{mem, stack, nil})
	}
}

func TestOpKeccak256Bytes32ResultIsRightAligned(t *testing.T) {
	var (
		env             = NewQRVM(BlockContext{}, TxContext{}, nil, params.TestChainConfig, Config{})
		stack           = newstack()
		mem             = NewMemory()
		qrvmInterpreter = NewQRVMInterpreter(env)
	)
	env.interpreter = qrvmInterpreter
	mem.Resize(32)
	pc := uint64(0)

	input := common.Hex2Bytes("0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
	mem.Set(0, 32, input)
	stack.push(uint512.NewInt(32))
	stack.push(new(uint512.Int))

	opKeccak256(&pc, qrvmInterpreter, &ScopeContext{mem, stack, nil})

	got := stack.peek().Bytes64()
	want := crypto.Keccak256(input)
	if !bytes.Equal(got[:32], make([]byte, 32)) {
		t.Fatalf("expected keccak256 high half to be zero, got %x", got[:32])
	}
	if !bytes.Equal(got[32:], want) {
		t.Fatalf("expected keccak256 low half %x, got %x", want, got[32:])
	}
}

func TestCreate2Addresses(t *testing.T) {
	// Expected addresses are the 64-byte CREATE2 derivation output for each
	// fixture's (origin, salt, codeHash) triple. Origins are given in full
	// 64-byte hex (without the Q prefix) so NewAddressFromString is no
	// longer on the critical path.
	type testcase struct {
		origin   string // 128-char hex, no Q prefix
		salt     string
		code     string
		expected string
	}

	for i, tt := range []testcase{
		{
			origin:   "000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
			salt:     "0x0000000000000000000000000000000000000000",
			code:     "0x00",
			expected: "Q5aed9ff5130a68c3ef67d87c5c6caef10e0508e8f89f1a123fbff221e1b1f6f54d7015aa15407162599c1d67a8e6ff2f8c68c9a4d75126be0716c812e17d1246",
		},
		{
			origin:   "deadbeef00000000000000000000000000000000deadbeef000000000000000000000000000000000000000000000000",
			salt:     "0x0000000000000000000000000000000000000000",
			code:     "0x00",
			expected: "Q1e1902257084d14390dbb1d77931e9567ffe253e148259554412d0ed99e886bb99ed4b97170401a76ef24f60151c35d37277ca267399264a4ded2eee3b9b21e1",
		},
		{
			origin:   "deadbeef00000000000000000000000000000000deadbeef000000000000000000000000000000000000000000000000",
			salt:     "0xfeed000000000000000000000000000000000000",
			code:     "0x00",
			expected: "Q05e8f1cb9310a38664d80b00a992d5587b31c72b14f002cc69012fc11e4842bae40efcf4ea4b30017f25b4b2cde79eca39a720dc59fa512319de762c75d541ca",
		},
		{
			origin:   "000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
			salt:     "0x0000000000000000000000000000000000000000",
			code:     "0xdeadbeef",
			expected: "Q3564a0c067c7db4aefc403b819c832f595de307c54b5e7c1f8c6d039407441f148a8595889e75d6ed588c6217dbc32d4d4c559938650762a0b43091ac6c9fbae",
		},
		{
			origin:   "00000000000000000000000000000000deadbeef00000000000000000000000000000000deadbeef0000000000000000",
			salt:     "0xcafebabe",
			code:     "0xdeadbeef",
			expected: "Q2a6bbb2315bcf3cf74202245b4eab8a3df64f45605dde360a6a717bdcb06e197b73881c9c3b5255935b22cc4b0ed54a16894d94e6626b21d501bc2f350bd3352",
		},
		{
			origin:   "00000000000000000000000000000000deadbeef00000000000000000000000000000000deadbeef0000000000000000",
			salt:     "0xcafebabe",
			code:     "0xdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeefdeadbeef",
			expected: "Q1a2c1f373f9d76f63e8d60f933848e4398f9c8ac2cb7826f5bad7e14d66a176b6f0e6576617f1bea5f0bcefb5fefc44e8f06351d05b53fd338dce6eee51ae2cf",
		},
		{
			origin:   "000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
			salt:     "0x0000000000000000000000000000000000000000",
			code:     "0x",
			expected: "Q363a565ec52ae6a64188fa6f0988de8cd33d1e0fa8821c5ad6982533d6cd9a02a47a96bbfeb56bd7b71cc2c18b0fddf16df1afa73118e8020f92940ffcb0ade1",
		},
	} {
		origin := common.BytesToAddress(common.FromHex(tt.origin))
		// Left-pad the (<=32-byte) salt from the fixture into the 64-byte
		// CREATE2 salt word used by the VM.
		saltBytes := common.FromHex(tt.salt)
		var salt [64]byte
		if len(saltBytes) > 64 {
			saltBytes = saltBytes[len(saltBytes)-64:]
		}
		copy(salt[64-len(saltBytes):], saltBytes)
		code := common.FromHex(tt.code)
		codeHash := crypto.Keccak512Hash(code)
		address := crypto.CreateAddress2(origin, salt, codeHash)
		/*
			stack          := newstack()
			// salt, but we don't need that for this test
			stack.push(big.NewInt(int64(len(code)))) //size
			stack.push(big.NewInt(0)) // memstart
			stack.push(big.NewInt(0)) // value
			gas, _ := gasCreate2(params.GasTable{}, nil, nil, stack, nil, 0)
			fmt.Printf("Example %d\n* address `0x%x`\n* salt `0x%x`\n* init_code `0x%x`\n* gas (assuming no mem expansion): `%v`\n* result: `%s`\n\n", i,origin, salt, code, gas, address.String())
		*/
		expected := common.MustParseAddress(tt.expected)
		if !bytes.Equal(expected.Bytes(), address.Bytes()) {
			t.Errorf("test %d: expected %s, got %s", i, expected.String(), address.String())
		}
	}
}

func TestRandom(t *testing.T) {
	type testcase struct {
		name   string
		random common.Hash
	}

	for _, tt := range []testcase{
		{name: "empty hash", random: common.Hash{}},
		{name: "1", random: common.Hash{0}},
		{name: "emptyCodeHash", random: crypto.Keccak256Hash(nil)},
		{name: "hash(0x010203)", random: crypto.Keccak256Hash([]byte{0x01, 0x02, 0x03})},
	} {
		var (
			env             = NewQRVM(BlockContext{Random: &tt.random}, TxContext{}, nil, params.TestChainConfig, Config{})
			stack           = newstack()
			pc              = uint64(0)
			qrvmInterpreter = env.interpreter
		)
		opRandom(&pc, qrvmInterpreter, &ScopeContext{nil, stack, nil})
		if len(stack.data) != 1 {
			t.Errorf("Expected one item on stack after %v, got %d: ", tt.name, len(stack.data))
		}
		actual := stack.pop()
		expected, overflow := uint512.FromBig(new(big.Int).SetBytes(tt.random.Bytes()))
		if overflow {
			t.Errorf("Testcase %v: invalid overflow", tt.name)
		}
		if actual.Cmp(expected) != 0 {
			t.Errorf("Testcase %v: expected  %x, got %x", tt.name, expected, actual)
		}
	}
}
