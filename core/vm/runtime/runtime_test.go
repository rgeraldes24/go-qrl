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

package runtime

import (
	"fmt"
	"math/big"
	"os"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/consensus"
	"github.com/theQRL/go-qrl/core"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/state"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/params"
	"github.com/theQRL/go-qrl/qrl/tracers"
	"github.com/theQRL/go-qrl/qrl/tracers/logger"

	// force-load js tracers to trigger registration
	_ "github.com/theQRL/go-qrl/qrl/tracers/js"
)

func TestDefaults(t *testing.T) {
	cfg := new(Config)
	setDefaults(cfg)

	if cfg.GasLimit == 0 {
		t.Error("didn't expect gaslimit to be zero")
	}
	if cfg.GasPrice == nil {
		t.Error("expected time to be non nil")
	}
	if cfg.Value == nil {
		t.Error("expected time to be non nil")
	}
	if cfg.GetHashFn == nil {
		t.Error("expected time to be non nil")
	}
	if cfg.BlockNumber == nil {
		t.Error("expected block number to be non nil")
	}
}

func TestQRVM(t *testing.T) {
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("crashed with: %v", r)
		}
	}()

	Execute([]byte{
		byte(vm.RANDOM),
		byte(vm.PREVRANDAO),
		byte(vm.TIMESTAMP),
		byte(vm.GASLIMIT),
		byte(vm.PUSH1),
		byte(vm.ORIGIN),
		byte(vm.BLOCKHASH),
		byte(vm.COINBASE),
	}, nil, nil)
}

func TestExecute(t *testing.T) {
	// MSTORE writes a 64-byte word to memory with the 512-bit stack entry
	// right-aligned, so RETURN must cover bytes [32:64] to observe the low 32 bytes.
	ret, _, err := Execute([]byte{
		byte(vm.PUSH1), 10,
		byte(vm.PUSH1), 0,
		byte(vm.MSTORE),
		byte(vm.PUSH1), 32,
		byte(vm.PUSH1), 32,
		byte(vm.RETURN),
	}, nil, nil)
	if err != nil {
		t.Fatal("didn't expect error", err)
	}

	num := new(big.Int).SetBytes(ret)
	if num.Cmp(big.NewInt(10)) != 0 {
		t.Error("Expected 10, got", num)
	}
}

func TestCall(t *testing.T) {
	state, _ := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	address := common.MustParseAddress("Q0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000a")
	// Same low-word extraction as TestExecute: MSTORE writes a 64-byte word
	// with the value right-aligned, so RETURN reads bytes [32:64].
	state.SetCode(address, []byte{
		byte(vm.PUSH1), 10,
		byte(vm.PUSH1), 0,
		byte(vm.MSTORE),
		byte(vm.PUSH1), 32,
		byte(vm.PUSH1), 32,
		byte(vm.RETURN),
	})

	ret, _, err := Call(address, nil, &Config{State: state})
	if err != nil {
		t.Fatal("didn't expect error", err)
	}

	num := new(big.Int).SetBytes(ret)
	if num.Cmp(big.NewInt(10)) != 0 {
		t.Error("Expected 10, got", num)
	}
}

func BenchmarkCall(b *testing.B) {
	var definition = `[{"constant":false,"inputs":[],"name":"confirmPurchase","outputs":[],"type":"function"},{"constant":false,"inputs":[],"name":"confirmReceived","outputs":[],"type":"function"},{"constant":false,"inputs":[],"name":"refund","outputs":[],"type":"function"}]`

	code := []byte{
		byte(vm.PUSH1), 0x00,
		byte(vm.CALLDATALOAD),
		byte(vm.PUSH1), 0x00,
		byte(vm.MSTORE),
		byte(vm.PUSH1), byte(uint512.WordBytes),
		byte(vm.PUSH1), 0x00,
		byte(vm.RETURN),
	}

	abi, err := abi.JSON(strings.NewReader(definition))
	if err != nil {
		b.Fatal(err)
	}

	cpurchase, err := abi.Pack("confirmPurchase")
	if err != nil {
		b.Fatal(err)
	}
	creceived, err := abi.Pack("confirmReceived")
	if err != nil {
		b.Fatal(err)
	}
	refund, err := abi.Pack("refund")
	if err != nil {
		b.Fatal(err)
	}
	inputs := [][]byte{cpurchase, creceived, refund}

	for _, input := range inputs {
		ret, _, err := Execute(code, input, nil)
		if err != nil {
			b.Fatal(err)
		}
		if len(ret) != uint512.WordBytes {
			b.Fatalf("unexpected return length: have %d want %d", len(ret), uint512.WordBytes)
		}
	}
	b.ResetTimer()
	for b.Loop() {
		for range 400 {
			for _, input := range inputs {
				if _, _, err := Execute(code, input, nil); err != nil {
					b.Fatal(err)
				}
			}
		}
	}
}
func benchmarkQRVM_Create(bench *testing.B, code string) {
	var (
		statedb, _ = state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
		sender     = common.BytesToAddress([]byte("sender"))
		receiver   = common.BytesToAddress([]byte("receiver"))
	)

	statedb.CreateAccount(sender)
	statedb.SetCode(receiver, common.FromHex(code))
	runtimeConfig := Config{
		Origin:      sender,
		State:       statedb,
		GasLimit:    10000000,
		Time:        0,
		Coinbase:    common.Address{},
		BlockNumber: new(big.Int).SetUint64(1),
		ChainConfig: &params.ChainConfig{
			ChainID: big.NewInt(1),
		},
		QRVMConfig: vm.Config{},
	}
	// Warm up the intpools and stuff
	for bench.Loop() {
		Call(receiver, []byte{}, &runtimeConfig)
	}
}

func BenchmarkQRVM_CREATE_500(bench *testing.B) {
	// initcode size 500K, repeatedly calls CREATE and then modifies the mem contents
	benchmarkQRVM_Create(bench, "5b6207a1206000a0f0600152600056")
}
func BenchmarkQRVM_CREATE2_500(bench *testing.B) {
	// initcode size 500K, repeatedly calls CREATE2 and then modifies the mem contents
	benchmarkQRVM_Create(bench, "5b586207a1206000a0f5600152600056")
}
func BenchmarkQRVM_CREATE_1200(bench *testing.B) {
	// initcode size 1200K, repeatedly calls CREATE and then modifies the mem contents
	benchmarkQRVM_Create(bench, "5b62124f806000a0f0600152600056")
}
func BenchmarkQRVM_CREATE2_1200(bench *testing.B) {
	// initcode size 1200K, repeatedly calls CREATE2 and then modifies the mem contents
	benchmarkQRVM_Create(bench, "5b5862124f806000a0f5600152600056")
}

func fakeHeader(n uint64, parentHash common.Hash) *types.Header {
	coinbase := common.MustParseAddress("Q000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000deadbeef")
	header := types.Header{
		Coinbase:   coinbase,
		Number:     big.NewInt(int64(n)),
		ParentHash: parentHash,
		Time:       1000,
		Extra:      []byte{},
		GasLimit:   100000,
	}
	return &header
}

type dummyChain struct {
	counter int
}

// Engine retrieves the chain's consensus engine.
func (d *dummyChain) Engine() consensus.Engine {
	return nil
}

// GetHeader returns the hash corresponding to their hash.
func (d *dummyChain) GetHeader(h common.Hash, n uint64) *types.Header {
	d.counter++
	parentHash := common.Hash{}
	s := common.LeftPadBytes(big.NewInt(int64(n-1)).Bytes(), 32)
	copy(parentHash[:], s)

	//parentHash := common.Hash{byte(n - 1)}
	//fmt.Printf("GetHeader(%x, %d) => header with parent %x\n", h, n, parentHash)
	return fakeHeader(n, parentHash)
}

// TestBlockhash tests the blockhash operation. It's a bit special, since it internally
// requires access to a chain reader.
func TestBlockhash(t *testing.T) {
	// Current head
	n := uint64(1000)
	parentHash := common.Hash{}
	s := common.LeftPadBytes(big.NewInt(int64(n-1)).Bytes(), 32)
	copy(parentHash[:], s)
	header := fakeHeader(n, parentHash)

	// Hand-rolled bytecode (replaces the prior Solidity fixture which depended
	// on DUP/SWAP/LOG opcodes that shifted after the 512-bit VM migration).
	//
	// Emits three 32-byte return values packed into 96 bytes:
	//   [0:32]  = blockhash(1000)  → zero       (request at head)
	//   [32:64] = blockhash(999)   → parent     (served from cache, no GetHeader)
	//   [64:96] = blockhash(744)   → oldest in-range hash (exactly 255 GetHeader calls)
	//
	// For each slot, BLOCKHASH pushes the 32-byte hash in the low half of the
	// 512-bit stack word; SHL by 256 moves it into the high half so MSTORE's
	// 64-byte write lands the hash in the top 32 bytes at the requested offset.
	data := common.Hex2Bytes(
		"6103e8406101001b600052" + // BLOCKHASH(1000); (h<<256); MSTORE(0)
			"6103e7406101001b602052" + // BLOCKHASH(999);  (h<<256); MSTORE(32)
			"6102e8406101001b604052" + // BLOCKHASH(744);  (h<<256); MSTORE(64)
			"60606000f3") //               RETURN(0, 96)
	input := []byte(nil)
	chain := &dummyChain{}
	ret, _, err := Execute(data, input, &Config{
		GetHashFn:   core.GetHashFn(header, chain),
		BlockNumber: new(big.Int).Set(header.Number),
	})
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(ret) != 96 {
		t.Fatalf("expected returndata to be 96 bytes, got %d", len(ret))
	}

	zero := new(big.Int).SetBytes(ret[0:32])
	first := new(big.Int).SetBytes(ret[32:64])
	last := new(big.Int).SetBytes(ret[64:96])
	if zero.BitLen() != 0 {
		t.Fatalf("expected zeroes, got %x", ret[0:32])
	}
	if first.Uint64() != 999 {
		t.Fatalf("second block should be 999, got %d (%x)", first, ret[32:64])
	}
	if last.Uint64() != 744 {
		t.Fatalf("last block should be 744, got %d (%x)", last, ret[64:96])
	}
	if exp, got := 255, chain.counter; exp != got {
		t.Errorf("suboptimal; too much chain iteration, expected %d, got %d", exp, got)
	}
}

// benchmarkNonModifyingCode benchmarks code, but if the code modifies the
// state, this should not be used, since it does not reset the state between runs.
func benchmarkNonModifyingCode(gas uint64, code []byte, name string, tracerCode string, b *testing.B) {
	cfg := new(Config)
	setDefaults(cfg)
	cfg.State, _ = state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	cfg.GasLimit = gas
	if len(tracerCode) > 0 {
		tracer, err := tracers.DefaultDirectory.New(tracerCode, new(tracers.Context), nil)
		if err != nil {
			b.Fatal(err)
		}
		cfg.QRVMConfig = vm.Config{
			Tracer: tracer,
		}
	}
	var (
		destination = common.BytesToAddress([]byte("contract"))
		vmenv       = NewEnv(cfg)
		sender      = vm.AccountRef(cfg.Origin)
	)
	cfg.State.CreateAccount(destination)
	eoa := common.MustParseAddress("Q000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000E0")
	{
		cfg.State.CreateAccount(eoa)
		cfg.State.SetNonce(eoa, 100)
	}
	reverting := common.MustParseAddress("Q000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000EE")
	{
		cfg.State.CreateAccount(reverting)
		cfg.State.SetCode(reverting, []byte{
			byte(vm.PUSH1), 0x00,
			byte(vm.PUSH1), 0x00,
			byte(vm.REVERT),
		})
	}

	//cfg.State.CreateAccount(cfg.Origin)
	// set the receiver's (the executing contract) code for execution.
	cfg.State.SetCode(destination, code)
	vmenv.Call(sender, destination, nil, gas, cfg.Value)

	b.Run(name, func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			vmenv.Call(sender, destination, nil, gas, cfg.Value)
		}
	})
}

// BenchmarkSimpleLoop test a pretty simple loop which loops until OOG
// 55 ms
func BenchmarkSimpleLoop(b *testing.B) {
	staticCallIdentity := []byte{
		byte(vm.JUMPDEST), //  [ count ]
		// push args for the call
		byte(vm.PUSH1), 0, // out size
		byte(vm.DUP1),       // out offset
		byte(vm.DUP1),       // out insize
		byte(vm.DUP1),       // in offset
		byte(vm.PUSH1), 0x4, // address of identity
		byte(vm.GAS), // gas
		byte(vm.STATICCALL),
		byte(vm.POP),      // pop return value
		byte(vm.PUSH1), 0, // jumpdestination
		byte(vm.JUMP),
	}

	callIdentity := []byte{
		byte(vm.JUMPDEST), //  [ count ]
		// push args for the call
		byte(vm.PUSH1), 0, // out size
		byte(vm.DUP1),       // out offset
		byte(vm.DUP1),       // out insize
		byte(vm.DUP1),       // in offset
		byte(vm.DUP1),       // value
		byte(vm.PUSH1), 0x4, // address of identity
		byte(vm.GAS), // gas
		byte(vm.CALL),
		byte(vm.POP),      // pop return value
		byte(vm.PUSH1), 0, // jumpdestination
		byte(vm.JUMP),
	}

	callInexistant := []byte{
		byte(vm.JUMPDEST), //  [ count ]
		// push args for the call
		byte(vm.PUSH1), 0, // out size
		byte(vm.DUP1),        // out offset
		byte(vm.DUP1),        // out insize
		byte(vm.DUP1),        // in offset
		byte(vm.DUP1),        // value
		byte(vm.PUSH1), 0xff, // address of existing contract
		byte(vm.GAS), // gas
		byte(vm.CALL),
		byte(vm.POP),      // pop return value
		byte(vm.PUSH1), 0, // jumpdestination
		byte(vm.JUMP),
	}

	callEOA := []byte{
		byte(vm.JUMPDEST), //  [ count ]
		// push args for the call
		byte(vm.PUSH1), 0, // out size
		byte(vm.DUP1),        // out offset
		byte(vm.DUP1),        // out insize
		byte(vm.DUP1),        // in offset
		byte(vm.DUP1),        // value
		byte(vm.PUSH1), 0xE0, // address of EOA
		byte(vm.GAS), // gas
		byte(vm.CALL),
		byte(vm.POP),      // pop return value
		byte(vm.PUSH1), 0, // jumpdestination
		byte(vm.JUMP),
	}

	loopingCode := []byte{
		byte(vm.JUMPDEST), //  [ count ]
		// push args for the call
		byte(vm.PUSH1), 0, // out size
		byte(vm.DUP1),       // out offset
		byte(vm.DUP1),       // out insize
		byte(vm.DUP1),       // in offset
		byte(vm.PUSH1), 0x4, // address of identity
		byte(vm.GAS), // gas

		byte(vm.POP), byte(vm.POP), byte(vm.POP), byte(vm.POP), byte(vm.POP), byte(vm.POP),
		byte(vm.PUSH1), 0, // jumpdestination
		byte(vm.JUMP),
	}

	callRevertingContractWithInput := []byte{
		byte(vm.JUMPDEST), //
		// push args for the call
		byte(vm.PUSH1), 0, // out size
		byte(vm.DUP1),        // out offset
		byte(vm.PUSH1), 0x20, // in size
		byte(vm.PUSH1), 0x00, // in offset
		byte(vm.PUSH1), 0x00, // value
		byte(vm.PUSH1), 0xEE, // address of reverting contract
		byte(vm.GAS), // gas
		byte(vm.CALL),
		byte(vm.POP),      // pop return value
		byte(vm.PUSH1), 0, // jumpdestination
		byte(vm.JUMP),
	}

	//tracer := logger.NewJSONLogger(nil, os.Stdout)
	//Execute(loopingCode, nil, &Config{
	//	QRVMConfig: vm.Config{
	//		Debug:  true,
	//		Tracer: tracer,
	//	}})
	// 100M gas
	benchmarkNonModifyingCode(100000000, staticCallIdentity, "staticcall-identity-100M", "", b)
	benchmarkNonModifyingCode(100000000, callIdentity, "call-identity-100M", "", b)
	benchmarkNonModifyingCode(100000000, loopingCode, "loop-100M", "", b)
	benchmarkNonModifyingCode(100000000, callInexistant, "call-nonexist-100M", "", b)
	benchmarkNonModifyingCode(100000000, callEOA, "call-EOA-100M", "", b)
	benchmarkNonModifyingCode(100000000, callRevertingContractWithInput, "call-reverting-100M", "", b)

	//benchmarkNonModifyingCode(10000000, staticCallIdentity, "staticcall-identity-10M", b)
	//benchmarkNonModifyingCode(10000000, loopingCode, "loop-10M", b)
}

// TestEip2929Cases contains various testcases that are used for
// EIP-2929 about gas repricings
func TestEip2929Cases(t *testing.T) {
	t.Skip("Test only useful for generating documentation")
	id := 1
	prettyPrint := func(comment string, code []byte) {
		fmt.Printf("### Case %d\n\n", id)
		id++
		fmt.Printf("%v\n\nBytecode: \n```\n%#x\n```\n", comment, code)
		Execute(code, nil, &Config{
			QRVMConfig: vm.Config{
				Tracer:    logger.NewMarkdownLogger(nil, os.Stdout),
				ExtraQips: []int{2929},
			},
		})
	}

	{ // First eip testcase
		code := []byte{
			// Three checks against a precompile
			byte(vm.PUSH1), 1, byte(vm.EXTCODEHASH), byte(vm.POP),
			byte(vm.PUSH1), 2, byte(vm.EXTCODESIZE), byte(vm.POP),
			byte(vm.PUSH1), 3, byte(vm.BALANCE), byte(vm.POP),
			// Three checks against a non-precompile
			byte(vm.PUSH1), 0xf1, byte(vm.EXTCODEHASH), byte(vm.POP),
			byte(vm.PUSH1), 0xf2, byte(vm.EXTCODESIZE), byte(vm.POP),
			byte(vm.PUSH1), 0xf3, byte(vm.BALANCE), byte(vm.POP),
			// Same three checks (should be cheaper)
			byte(vm.PUSH1), 0xf2, byte(vm.EXTCODEHASH), byte(vm.POP),
			byte(vm.PUSH1), 0xf3, byte(vm.EXTCODESIZE), byte(vm.POP),
			byte(vm.PUSH1), 0xf1, byte(vm.BALANCE), byte(vm.POP),
			// Check the origin, and the 'this'
			byte(vm.ORIGIN), byte(vm.BALANCE), byte(vm.POP),
			byte(vm.ADDRESS), byte(vm.BALANCE), byte(vm.POP),

			byte(vm.STOP),
		}
		prettyPrint("This checks `EXT`(codehash,codesize,balance) of precompiles, which should be `100`, "+
			"and later checks the same operations twice against some non-precompiles. "+
			"Those are cheaper second time they are accessed. Lastly, it checks the `BALANCE` of `origin` and `this`.", code)
	}

	{ // EXTCODECOPY
		code := []byte{
			// extcodecopy( 0xff,0,0,0,0)
			byte(vm.PUSH1), 0x00, byte(vm.PUSH1), 0x00, byte(vm.PUSH1), 0x00, //length, codeoffset, memoffset
			byte(vm.PUSH1), 0xff, byte(vm.EXTCODECOPY),
			// extcodecopy( 0xff,0,0,0,0)
			byte(vm.PUSH1), 0x00, byte(vm.PUSH1), 0x00, byte(vm.PUSH1), 0x00, //length, codeoffset, memoffset
			byte(vm.PUSH1), 0xff, byte(vm.EXTCODECOPY),
			// extcodecopy( this,0,0,0,0)
			byte(vm.PUSH1), 0x00, byte(vm.PUSH1), 0x00, byte(vm.PUSH1), 0x00, //length, codeoffset, memoffset
			byte(vm.ADDRESS), byte(vm.EXTCODECOPY),

			byte(vm.STOP),
		}
		prettyPrint("This checks `extcodecopy( 0xff,0,0,0,0)` twice, (should be expensive first time), "+
			"and then does `extcodecopy( this,0,0,0,0)`.", code)
	}

	{ // SLOAD + SSTORE
		code := []byte{

			// Add slot `0x1` to access list
			byte(vm.PUSH1), 0x01, byte(vm.SLOAD), byte(vm.POP), // SLOAD( 0x1) (add to access list)
			// Write to `0x1` which is already in access list
			byte(vm.PUSH1), 0x11, byte(vm.PUSH1), 0x01, byte(vm.SSTORE), // SSTORE( loc: 0x01, val: 0x11)
			// Write to `0x2` which is not in access list
			byte(vm.PUSH1), 0x11, byte(vm.PUSH1), 0x02, byte(vm.SSTORE), // SSTORE( loc: 0x02, val: 0x11)
			// Write again to `0x2`
			byte(vm.PUSH1), 0x11, byte(vm.PUSH1), 0x02, byte(vm.SSTORE), // SSTORE( loc: 0x02, val: 0x11)
			// Read slot in access list (0x2)
			byte(vm.PUSH1), 0x02, byte(vm.SLOAD), // SLOAD( 0x2)
			// Read slot in access list (0x1)
			byte(vm.PUSH1), 0x01, byte(vm.SLOAD), // SLOAD( 0x1)
		}
		prettyPrint("This checks `sload( 0x1)` followed by `sstore(loc: 0x01, val:0x11)`, then 'naked' sstore:"+
			"`sstore(loc: 0x02, val:0x11)` twice, and `sload(0x2)`, `sload(0x1)`. ", code)
	}
	{ // Call variants
		code := []byte{
			// identity precompile
			byte(vm.PUSH1), 0x0, byte(vm.DUP1), byte(vm.DUP1), byte(vm.DUP1), byte(vm.DUP1),
			byte(vm.PUSH1), 0x04, byte(vm.PUSH1), 0x0, byte(vm.CALL), byte(vm.POP),

			// random account - call 1
			byte(vm.PUSH1), 0x0, byte(vm.DUP1), byte(vm.DUP1), byte(vm.DUP1), byte(vm.DUP1),
			byte(vm.PUSH1), 0xff, byte(vm.PUSH1), 0x0, byte(vm.CALL), byte(vm.POP),

			// random account - call 2
			byte(vm.PUSH1), 0x0, byte(vm.DUP1), byte(vm.DUP1), byte(vm.DUP1), byte(vm.DUP1),
			byte(vm.PUSH1), 0xff, byte(vm.PUSH1), 0x0, byte(vm.STATICCALL), byte(vm.POP),
		}
		prettyPrint("This calls the `identity`-precompile (cheap), then calls an account (expensive) and `staticcall`s the same"+
			"account (cheap)", code)
	}
}

// TestColdAccountAccessCost test that the cold account access cost is reported
// correctly
// see: https://github.com/theQRL/go-qrl/issues/22649
func TestColdAccountAccessCost(t *testing.T) {
	for i, tc := range []struct {
		code []byte
		step int
		want uint64
	}{
		{ // EXTCODEHASH(0xff)
			code: []byte{byte(vm.PUSH1), 0xFF, byte(vm.EXTCODEHASH), byte(vm.POP)},
			step: 1,
			want: 2600,
		},
		{ // BALANCE(0xff)
			code: []byte{byte(vm.PUSH1), 0xFF, byte(vm.BALANCE), byte(vm.POP)},
			step: 1,
			want: 2600,
		},
		{ // CALL(0xff)
			code: []byte{
				byte(vm.PUSH1), 0x0,
				byte(vm.DUP1), byte(vm.DUP1), byte(vm.DUP1), byte(vm.DUP1),
				byte(vm.PUSH1), 0xff, byte(vm.DUP1), byte(vm.CALL), byte(vm.POP),
			},
			step: 7,
			want: 2855,
		},
		{ // DELEGATECALL(0xff)
			code: []byte{
				byte(vm.PUSH1), 0x0,
				byte(vm.DUP1), byte(vm.DUP1), byte(vm.DUP1),
				byte(vm.PUSH1), 0xff, byte(vm.DUP1), byte(vm.DELEGATECALL), byte(vm.POP),
			},
			step: 6,
			want: 2855,
		},
		{ // STATICCALL(0xff)
			code: []byte{
				byte(vm.PUSH1), 0x0,
				byte(vm.DUP1), byte(vm.DUP1), byte(vm.DUP1),
				byte(vm.PUSH1), 0xff, byte(vm.DUP1), byte(vm.STATICCALL), byte(vm.POP),
			},
			step: 6,
			want: 2855,
		},
	} {
		tracer := logger.NewStructLogger(nil)
		Execute(tc.code, nil, &Config{
			QRVMConfig: vm.Config{
				Tracer: tracer,
			},
		})
		have := tracer.StructLogs()[tc.step].GasCost
		if want := tc.want; have != want {
			for ii, op := range tracer.StructLogs() {
				t.Logf("%d: %v %d", ii, op.OpName(), op.GasCost)
			}
			t.Fatalf("testcase %d, gas report wrong, step %d, have %d want %d", i, tc.step, have, want)
		}
	}
}

func TestRuntimeJSTracer(t *testing.T) {
	jsTracers := []string{
		`{enters: 0, exits: 0, enterGas: 0, gasUsed: 0, steps:0,
	step: function() { this.steps++},
	fault: function() {},
	result: function() {
		return [this.enters, this.exits,this.enterGas,this.gasUsed, this.steps].join(",")
	},
	enter: function(frame) {
		this.enters++;
		this.enterGas = frame.getGas();
	},
	exit: function(res) {
		this.exits++;
		this.gasUsed = res.getGasUsed();
	}}`,
		`{enters: 0, exits: 0, enterGas: 0, gasUsed: 0, steps:0,
	fault: function() {},
	result: function() {
		return [this.enters, this.exits,this.enterGas,this.gasUsed, this.steps].join(",")
	},
	enter: function(frame) {
		this.enters++;
		this.enterGas = frame.getGas();
	},
	exit: function(res) {
		this.exits++;
		this.gasUsed = res.getGasUsed();
	}}`}
	tests := []struct {
		code []byte
		// One result per tracer
		results []string
	}{
		{
			// CREATE
			code: []byte{
				// Store initcode in memory at 0x00, right-aligned in a 64-byte MSTORE word
				byte(vm.PUSH5),
				// Init code: PUSH1 0, PUSH1 0, RETURN (3 steps)
				byte(vm.PUSH1), 0, byte(vm.PUSH1), 0, byte(vm.RETURN),
				byte(vm.PUSH1), 0,
				byte(vm.MSTORE),
				// length, offset, value
				byte(vm.PUSH1), 5, byte(vm.PUSH1), 59, byte(vm.PUSH1), 0,
				byte(vm.CREATE),
				byte(vm.POP),
			},
			results: []string{`"1,1,952853,6,12"`, `"1,1,952853,6,0"`},
		},
		{
			// CREATE2
			code: []byte{
				// Store initcode in memory at 0x00, right-aligned in a 64-byte MSTORE word
				byte(vm.PUSH5),
				// Init code: PUSH1 0, PUSH1 0, RETURN (3 steps)
				byte(vm.PUSH1), 0, byte(vm.PUSH1), 0, byte(vm.RETURN),
				byte(vm.PUSH1), 0,
				byte(vm.MSTORE),
				// salt, length, offset, value
				byte(vm.PUSH1), 1, byte(vm.PUSH1), 5, byte(vm.PUSH1), 59, byte(vm.PUSH1), 0,
				byte(vm.CREATE2),
				byte(vm.POP),
			},
			results: []string{`"1,1,952844,6,13"`, `"1,1,952844,6,0"`},
		},
		{
			// CALL
			code: []byte{
				// outsize, outoffset, insize, inoffset
				byte(vm.PUSH1), 0, byte(vm.PUSH1), 0, byte(vm.PUSH1), 0, byte(vm.PUSH1), 0,
				byte(vm.PUSH1), 0, // value
				byte(vm.PUSH1), 0xbb, //address
				byte(vm.GAS), // gas
				byte(vm.CALL),
				byte(vm.POP),
			},
			results: []string{`"1,1,981796,6,13"`, `"1,1,981796,6,0"`},
		},
		{
			// STATICCALL
			code: []byte{
				// outsize, outoffset, insize, inoffset
				byte(vm.PUSH1), 0, byte(vm.PUSH1), 0, byte(vm.PUSH1), 0, byte(vm.PUSH1), 0,
				byte(vm.PUSH1), 0xdd, //address
				byte(vm.GAS), // gas
				byte(vm.STATICCALL),
				byte(vm.POP),
			},
			results: []string{`"1,1,981799,6,12"`, `"1,1,981799,6,0"`},
		},
		{
			// DELEGATECALL
			code: []byte{
				// outsize, outoffset, insize, inoffset
				byte(vm.PUSH1), 0, byte(vm.PUSH1), 0, byte(vm.PUSH1), 0, byte(vm.PUSH1), 0,
				byte(vm.PUSH1), 0xee, //address
				byte(vm.GAS), // gas
				byte(vm.DELEGATECALL),
				byte(vm.POP),
			},
			results: []string{`"1,1,981799,6,12"`, `"1,1,981799,6,0"`},
		},
	}
	calleeCode := []byte{
		byte(vm.PUSH1), 0,
		byte(vm.PUSH1), 0,
		byte(vm.RETURN),
	}
	// The runtime CALL / STATICCALL / DELEGATECALL opcodes pop the target
	// address from the full 64-byte stack word, so each
	// fixture hex ends with the classic single-byte marker at the very end.
	main := common.BytesToAddress([]byte{0xaa})
	address0 := common.BytesToAddress([]byte{0xbb})
	address1 := common.BytesToAddress([]byte{0xcc})
	address2 := common.BytesToAddress([]byte{0xdd})
	address3 := common.BytesToAddress([]byte{0xee})
	for i, jsTracer := range jsTracers {
		for j, tc := range tests {
			statedb, _ := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
			statedb.SetCode(main, tc.code)
			statedb.SetCode(address0, calleeCode)
			statedb.SetCode(address1, calleeCode)
			statedb.SetCode(address2, calleeCode)
			statedb.SetCode(address3, calleeCode)

			tracer, err := tracers.DefaultDirectory.New(jsTracer, new(tracers.Context), nil)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = Call(main, nil, &Config{
				GasLimit: 1000000,
				State:    statedb,
				QRVMConfig: vm.Config{
					Tracer: tracer,
				}})
			if err != nil {
				t.Fatal("didn't expect error", err)
			}
			res, err := tracer.GetResult()
			if err != nil {
				t.Fatal(err)
			}
			if have, want := string(res), tc.results[i]; have != want {
				t.Errorf("wrong result for tracer %d testcase %d, have \n%v\nwant\n%v\n", i, j, have, want)
			}
		}
	}
}

func TestJSTracerCreateTx(t *testing.T) {
	jsTracer := `
	{enters: 0, exits: 0,
	step: function() {},
	fault: function() {},
	result: function() { return [this.enters, this.exits].join(",") },
	enter: function(frame) { this.enters++ },
	exit: function(res) { this.exits++ }}`
	code := []byte{byte(vm.PUSH1), 0, byte(vm.PUSH1), 0, byte(vm.RETURN)}

	statedb, _ := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	tracer, err := tracers.DefaultDirectory.New(jsTracer, new(tracers.Context), nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = Create(code, &Config{
		State: statedb,
		QRVMConfig: vm.Config{
			Tracer: tracer,
		}})
	if err != nil {
		t.Fatal(err)
	}

	res, err := tracer.GetResult()
	if err != nil {
		t.Fatal(err)
	}
	if have, want := string(res), `"0,0"`; have != want {
		t.Errorf("wrong result for tracer, have \n%v\nwant\n%v\n", have, want)
	}
}

func BenchmarkTracerStepVsCallFrame(b *testing.B) {
	// Simply pushes and pops some values in a loop
	code := []byte{
		byte(vm.JUMPDEST),
		byte(vm.PUSH1), 0,
		byte(vm.PUSH1), 0,
		byte(vm.POP),
		byte(vm.POP),
		byte(vm.PUSH1), 0, // jumpdestination
		byte(vm.JUMP),
	}

	stepTracer := `
	{
	step: function() {},
	fault: function() {},
	result: function() {},
	}`
	callFrameTracer := `
	{
	enter: function() {},
	exit: function() {},
	fault: function() {},
	result: function() {},
	}`

	benchmarkNonModifyingCode(10000000, code, "tracer-step-10M", stepTracer, b)
	benchmarkNonModifyingCode(10000000, code, "tracer-call-frame-10M", callFrameTracer, b)
}
