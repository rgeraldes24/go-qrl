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
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/params"
)

// precompiledTest defines the input/output pairs for precompiled contract tests.
type precompiledTest struct {
	Input, Expected string
	Gas             uint64
	Name            string
	NoBenchmark     bool // Benchmark primarily the worst-cases
}

// NOTE(rgeraldes24): unused at the moment
/*
// precompiledFailureTest defines the input/error pairs for precompiled
// contract failure tests.
type precompiledFailureTest struct {
	Input         string
	ExpectedError string
	Name          string
}
*/

// allPrecompiles deliberately aliases the production map. Test vectors must
// exercise the same addresses that CALL dispatches to in a running node.
var allPrecompiles = PrecompiledContractsZond

func TestPrecompiledAddressesZond(t *testing.T) {
	want := map[common.Address]bool{
		common.BytesToAddress([]byte{1}): true,
		common.BytesToAddress([]byte{2}): true,
		common.BytesToAddress([]byte{4}): true,
		common.BytesToAddress([]byte{5}): true,
	}
	if len(PrecompiledContractsZond) != len(want) {
		t.Fatalf("precompile count mismatch: have %d want %d", len(PrecompiledContractsZond), len(want))
	}
	for address := range PrecompiledContractsZond {
		if !want[address] {
			t.Errorf("unexpected production precompile at %s", address)
		}
	}
	if _, exists := PrecompiledContractsZond[common.BytesToAddress([]byte{3})]; exists {
		t.Error("legacy identity address 0x03 must not be active")
	}
}

func precompileAddress(n string) string {
	return "Q" + strings.Repeat("0", 2*common.AddressLength-len(n)) + n
}

const vm64DepositRootExpected = "0033398ac7d5822aba0b3f614e7728940a9597e122ddd462fe3b5c7c458a3d1a"

func vm64DepositRootInput() []byte {
	input := make([]byte, depositInputLength)
	for i := 0; i < depositPublicKeyLength; i++ {
		input[depositPublicKeyOffset+i] = byte(i*17 + 3)
	}
	// Both halves are intentionally populated so a 32-byte withdrawal-recipient
	// regression changes the root instead of silently preserving the low half.
	for i := 0; i < depositWithdrawalRecipientLength/2; i++ {
		input[depositWithdrawalRecipientOffset+i] = byte(0xa0 + i)
		input[depositWithdrawalRecipientOffset+depositWithdrawalRecipientLength/2+i] = byte(0x30 + i)
	}
	binary.LittleEndian.PutUint64(input[depositAmountOffset:depositSignatureOffset], 32_000_000_000)
	for i := 0; i < depositSignatureLength; i++ {
		input[depositSignatureOffset+i] = byte(i*31 + 7)
	}
	return input
}

func vm64DepositRootTest() precompiledTest {
	return precompiledTest{
		Input:    common.Bytes2Hex(vm64DepositRootInput()),
		Expected: vm64DepositRootExpected,
		Gas:      params.DepositrootGas,
		Name:     "vm64_with_nonzero_upper_withdrawal_recipient",
	}
}

func testPrecompiled(addr string, test precompiledTest, t *testing.T) {
	contractAddr := common.MustParseAddress(addr)
	p := allPrecompiles[contractAddr]
	in := common.Hex2Bytes(test.Input)
	gas := p.RequiredGas(in)
	t.Run(fmt.Sprintf("%s-Gas=%d", test.Name, gas), func(t *testing.T) {
		if res, _, err := RunPrecompiledContract(p, in, gas); err != nil {
			t.Error(err)
		} else if common.Bytes2Hex(res) != test.Expected {
			t.Errorf("Expected %v, got %v", test.Expected, common.Bytes2Hex(res))
		}
		if expGas := test.Gas; expGas != gas {
			t.Errorf("%v: gas wrong, expected %d, got %d", test.Name, expGas, gas)
		}
		// Verify that the precompile did not touch the input buffer
		exp := common.Hex2Bytes(test.Input)
		if !bytes.Equal(in, exp) {
			t.Errorf("Precompiled %v modified input data", addr)
		}
	})
}

func testPrecompiledOOG(addr string, test precompiledTest, t *testing.T) {
	contractAddr := common.MustParseAddress(addr)
	p := allPrecompiles[contractAddr]
	in := common.Hex2Bytes(test.Input)
	gas := p.RequiredGas(in) - 1

	t.Run(fmt.Sprintf("%s-Gas=%d", test.Name, gas), func(t *testing.T) {
		_, _, err := RunPrecompiledContract(p, in, gas)
		if err.Error() != "out of gas" {
			t.Errorf("Expected error [out of gas], got [%v]", err)
		}
		// Verify that the precompile did not touch the input buffer
		exp := common.Hex2Bytes(test.Input)
		if !bytes.Equal(in, exp) {
			t.Errorf("Precompiled %v modified input data", addr)
		}
	})
}

// NOTE(rgeraldes): unused at the moment
/*
func testPrecompiledFailure(addr string, test precompiledFailureTest, t *testing.T) {
	p := allPrecompiles[common.HexToAddress(addr)]
	in := common.Hex2Bytes(test.Input)
	gas := p.RequiredGas(in)
	t.Run(test.Name, func(t *testing.T) {
		_, _, err := RunPrecompiledContract(p, in, gas)
		if err.Error() != test.ExpectedError {
			t.Errorf("Expected error [%v], got [%v]", test.ExpectedError, err)
		}
		// Verify that the precompile did not touch the input buffer
		exp := common.Hex2Bytes(test.Input)
		if !bytes.Equal(in, exp) {
			t.Errorf("Precompiled %v modified input data", addr)
		}
	})
}
*/

func benchmarkPrecompiled(addr string, test precompiledTest, bench *testing.B) {
	if test.NoBenchmark {
		return
	}
	contractAddr := common.MustParseAddress(addr)
	p := allPrecompiles[contractAddr]
	in := common.Hex2Bytes(test.Input)
	reqGas := p.RequiredGas(in)

	var (
		res  []byte
		err  error
		data = make([]byte, len(in))
	)

	bench.Run(fmt.Sprintf("%s-Gas=%d", test.Name, reqGas), func(bench *testing.B) {
		bench.ReportAllocs()
		start := time.Now()
		for bench.Loop() {
			copy(data, in)
			res, _, err = RunPrecompiledContract(p, data, reqGas)
		}
		elapsed := max(uint64(time.Since(start)), 1)
		gasUsed := reqGas * uint64(bench.N)
		bench.ReportMetric(float64(reqGas), "gas/op")
		// Keep it as uint64, multiply 100 to get two digit float later
		mgasps := (100 * 1000 * gasUsed) / elapsed
		bench.ReportMetric(float64(mgasps)/100, "mgas/s")
		//Check if it is correct
		if err != nil {
			bench.Error(err)
			return
		}
		if common.Bytes2Hex(res) != test.Expected {
			bench.Errorf("Expected %v, got %v", test.Expected, common.Bytes2Hex(res))
			return
		}
	})
}

// Benchmarks the sample inputs from the DEPOSITROOT precompile.
func BenchmarkPrecompiledDepositroot(bench *testing.B) {
	benchmarkPrecompiled(precompileAddress("01"), vm64DepositRootTest(), bench)
}

// Benchmarks the sample inputs from the SHA256 precompile.
func BenchmarkPrecompiledSha256(bench *testing.B) {
	t := precompiledTest{
		Input:    "38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e000000000000000000000000000000000000000000000000000000000000001b38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e789d1dd423d25f0772d2748d60f7e4b81bb14d086eba8e8e8efb6dcff8a4ae02",
		Expected: "811c7003375852fabd0d362e40e68607a12bdabae61a7d068fe5fdd1dbbf2a5d",
		Name:     "128",
	}
	benchmarkPrecompiled("02", t, bench)
}

// Benchmarks the sample inputs from the identiy precompile.
func BenchmarkPrecompiledIdentity(bench *testing.B) {
	t := precompiledTest{
		Input:    "38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e000000000000000000000000000000000000000000000000000000000000001b38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e789d1dd423d25f0772d2748d60f7e4b81bb14d086eba8e8e8efb6dcff8a4ae02",
		Expected: "38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e000000000000000000000000000000000000000000000000000000000000001b38d18acb67d25c8bb9942764b62f18e17054f66a817bd4295423adf9ed98873e789d1dd423d25f0772d2748d60f7e4b81bb14d086eba8e8e8efb6dcff8a4ae02",
		Name:     "128",
	}
	benchmarkPrecompiled("04", t, bench)
}

// Tests the sample inputs from the ModExp.
func TestPrecompiledModExp(t *testing.T) {
	testJson("modexp", precompileAddress("05"), t)
}
func BenchmarkPrecompiledModExp(b *testing.B) {
	benchJson("modexp", precompileAddress("05"), b)
}

// Tests OOG
func TestPrecompiledModExpOOG(t *testing.T) {
	modexpTests, err := loadJson("modexp")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range modexpTests {
		testPrecompiledOOG(precompileAddress("05"), test, t)
	}
}

func TestPrecompiledDepositroot(t *testing.T) {
	testPrecompiled(precompileAddress("01"), vm64DepositRootTest(), t)

	valid := vm64DepositRootInput()
	legacyTests, err := loadJson("depositroot")
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyTests) != 1 {
		t.Fatalf("legacy deposit-root vector count = %d, want 1", len(legacyTests))
	}
	legacy := common.Hex2Bytes(legacyTests[0].Input)
	if len(legacy) != depositInputLength-64 {
		t.Fatalf("legacy deposit-root vector length = %d, want %d", len(legacy), depositInputLength-64)
	}
	testPrecompiled(precompileAddress("01"), legacyTests[0], t)

	tests := []struct {
		name      string
		input     []byte
		canonical []byte
	}{
		{name: "empty", input: nil, canonical: make([]byte, depositInputLength)},
		{name: "one_byte_short", input: append([]byte(nil), valid[:len(valid)-1]...), canonical: append(append([]byte(nil), valid[:len(valid)-1]...), 0)},
		{name: "legacy_32_byte_recipient_and_signature", input: legacy, canonical: append(append([]byte(nil), legacy...), make([]byte, depositInputLength-len(legacy))...)},
		{name: "one_byte_long", input: append(append([]byte(nil), valid...), 0xff), canonical: valid},
	}
	p := allPrecompiles[common.BytesToAddress([]byte{1})]
	for _, test := range tests {
		t.Run("compatibility_"+test.name, func(t *testing.T) {
			before := append([]byte(nil), test.input...)
			result, _, err := RunPrecompiledContract(p, test.input, p.RequiredGas(test.input))
			if err != nil {
				t.Fatalf("run input: %v", err)
			}
			want, _, err := RunPrecompiledContract(p, test.canonical, p.RequiredGas(test.canonical))
			if err != nil {
				t.Fatalf("run canonicalized input: %v", err)
			}
			if !bytes.Equal(result, want) {
				t.Fatalf("compatibility root = %x, want %x", result, want)
			}
			if !bytes.Equal(test.input, before) {
				t.Fatal("deposit-root precompile modified input")
			}
		})
	}
}

func testJson(name, addr string, t *testing.T) {
	tests, err := loadJson(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		testPrecompiled(addr, test, t)
	}
}

// NOTE(rgeraldes24): unused at the moment
/*
func testJsonFail(name, addr string, t *testing.T) {
	tests, err := loadJsonFail(name)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range tests {
		testPrecompiledFailure(addr, test, t)
	}
}
*/

func benchJson(name, addr string, b *testing.B) {
	tests, err := loadJson(name)
	if err != nil {
		b.Fatal(err)
	}
	for _, test := range tests {
		benchmarkPrecompiled(addr, test, b)
	}
}

// Failure tests

func loadJson(name string) ([]precompiledTest, error) {
	data, err := os.ReadFile(fmt.Sprintf("testdata/precompiles/%v.json", name))
	if err != nil {
		return nil, err
	}
	var testcases []precompiledTest
	err = json.Unmarshal(data, &testcases)
	return testcases, err
}

// NOTE(rgeraldes24): unused at the moment
/*
func loadJsonFail(name string) ([]precompiledFailureTest, error) {
	data, err := os.ReadFile(fmt.Sprintf("testdata/precompiles/fail-%v.json", name))
	if err != nil {
		return nil, err
	}
	var testcases []precompiledFailureTest
	err = json.Unmarshal(data, &testcases)
	return testcases, err
}
*/
