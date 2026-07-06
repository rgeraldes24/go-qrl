// Copyright 2016 The go-ethereum Authors
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

package bind

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/common"
)

const (
	qrvmNoopBytecode          = "00"
	useLibraryMathPlaceholder = "19602d14acfdf1c8f04515d25ae0ffed7a19e56fe260950b488efb9ba564a4385987da4ce51629eafa9395cd3c4c35b7bf32dcd8432f3c4406d8c5bdee"
)

var bindTests = []struct {
	name     string
	contract string
	bytecode []string
	abi      []string
	imports  string
	tester   string
	fsigs    []map[string]string
	libs     map[string]string
	aliases  map[string]string
	types    []string
}{

	// Test that the binding is available in combined and separate forms too
	{
		`Empty`,
		`contract NilContract {}`,
		[]string{qrvmNoopBytecode},
		[]string{`[]`},
		`
			"github.com/theQRL/go-qrl/common"
		`,
		`
			if b, err := NewEmpty(common.Address{}, nil); b == nil || err != nil {
				t.Fatalf("combined binding (%v) nil or error (%v) not nil", b, nil)
			}
			if b, err := NewEmptyCaller(common.Address{}, nil); b == nil || err != nil {
				t.Fatalf("caller binding (%v) nil or error (%v) not nil", b, nil)
			}
			if b, err := NewEmptyTransactor(common.Address{}, nil); b == nil || err != nil {
				t.Fatalf("transactor binding (%v) nil or error (%v) not nil", b, nil)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Test that named and anonymous inputs are handled correctly
	{
		`InputChecker`, ``, []string{``},
		[]string{`[{"type":"function","name":"noInput","stateMutability":"view","inputs":[],"outputs":[]},{"type":"function","name":"namedInput","stateMutability":"view","inputs":[{"name":"str","type":"string"}],"outputs":[]},{"type":"function","name":"anonInput","stateMutability":"view","inputs":[{"name":"","type":"string"}],"outputs":[]},{"type":"function","name":"namedInputs","stateMutability":"view","inputs":[{"name":"str1","type":"string"},{"name":"str2","type":"string"}],"outputs":[]},{"type":"function","name":"anonInputs","stateMutability":"view","inputs":[{"name":"","type":"string"},{"name":"","type":"string"}],"outputs":[]},{"type":"function","name":"mixedInputs","stateMutability":"view","inputs":[{"name":"","type":"string"},{"name":"str","type":"string"}],"outputs":[]}]`},
		`
			"fmt"

			"github.com/theQRL/go-qrl/common"
		`,
		`
			if b, err := NewInputChecker(common.Address{}, nil); b == nil || err != nil {
				t.Fatalf("binding (%v) nil or error (%v) not nil", b, nil)
			} else if false { // Don't run, just compile and test types
				var err error

				err = b.NoInput(nil)
				err = b.NamedInput(nil, "")
				err = b.AnonInput(nil, "")
				err = b.NamedInputs(nil, "", "")
				err = b.AnonInputs(nil, "", "")
				err = b.MixedInputs(nil, "", "")

				fmt.Println(err)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Test that named and anonymous outputs are handled correctly
	{
		`OutputChecker`, ``, []string{``},
		[]string{`[{"type":"function","name":"noOutput","stateMutability":"view","inputs":[],"outputs":[]},{"type":"function","name":"namedOutput","stateMutability":"view","inputs":[],"outputs":[{"name":"str","type":"string"}]},{"type":"function","name":"anonOutput","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"string"}]},{"type":"function","name":"namedOutputs","stateMutability":"view","inputs":[],"outputs":[{"name":"str1","type":"string"},{"name":"str2","type":"string"}]},{"type":"function","name":"collidingOutputs","stateMutability":"view","inputs":[],"outputs":[{"name":"str","type":"string"},{"name":"Str","type":"string"}]},{"type":"function","name":"anonOutputs","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"string"},{"name":"","type":"string"}]},{"type":"function","name":"mixedOutputs","stateMutability":"view","inputs":[],"outputs":[{"name":"","type":"string"},{"name":"str","type":"string"}]}]`},
		`
			"fmt"

			"github.com/theQRL/go-qrl/common"
		`,
		`
			if b, err := NewOutputChecker(common.Address{}, nil); b == nil || err != nil {
				t.Fatalf("binding (%v) nil or error (%v) not nil", b, nil)
			} else if false { // Don't run, just compile and test types
				var str1, str2 string
				var err error

				err              = b.NoOutput(nil)
				str1, err        = b.NamedOutput(nil)
				str1, err        = b.AnonOutput(nil)
				res, _          := b.NamedOutputs(nil)
				str1, str2, err  = b.CollidingOutputs(nil)
				str1, str2, err  = b.AnonOutputs(nil)
				str1, str2, err  = b.MixedOutputs(nil)

				fmt.Println(str1, str2, res.Str1, res.Str2, err)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Tests that named, anonymous and indexed events are handled correctly
	{
		`EventChecker`, ``, []string{``},
		[]string{`[{"type":"event","name":"empty","inputs":[]},{"type":"event","name":"indexed","inputs":[{"name":"addr","type":"address","indexed":true},{"name":"num","type":"int256","indexed":true}]},{"type":"event","name":"mixed","inputs":[{"name":"addr","type":"address","indexed":true},{"name":"num","type":"int256"}]},{"type":"event","name":"anonymous","anonymous":true,"inputs":[]},{"type":"event","name":"dynamic","inputs":[{"name":"idxStr","type":"string","indexed":true},{"name":"idxDat","type":"bytes","indexed":true},{"name":"str","type":"string"},{"name":"dat","type":"bytes"}]},{"type":"event","name":"unnamed","inputs":[{"name":"","type":"uint256","indexed": true},{"name":"","type":"uint256","indexed":true}]}]`},
		`
			"fmt"
			"math/big"
			"reflect"

			"github.com/theQRL/go-qrl/common"
		`,
		`
			if e, err := NewEventChecker(common.Address{}, nil); e == nil || err != nil {
				t.Fatalf("binding (%v) nil or error (%v) not nil", e, nil)
			} else if false { // Don't run, just compile and test types
				var (
					err  error
				res  bool
					str  string
					dat  []byte
					topic common.LogTopic
				)
				_, err = e.FilterEmpty(nil)
				_, err = e.FilterIndexed(nil, []common.Address{}, []*big.Int{})

				mit, err := e.FilterMixed(nil, []common.Address{})

				res = mit.Next()  // Make sure the iterator has a Next method
				err = mit.Error() // Make sure the iterator has an Error method
				err = mit.Close() // Make sure the iterator has a Close method

				fmt.Println(mit.Event.Raw.BlockHash) // Make sure the raw log is contained within the results
				fmt.Println(mit.Event.Num)           // Make sure the unpacked non-indexed fields are present
				fmt.Println(mit.Event.Addr)          // Make sure the reconstructed indexed fields are present

				dit, err := e.FilterDynamic(nil, []string{}, [][]byte{})

				str  = dit.Event.Str    // Make sure non-indexed strings retain their type
				dat  = dit.Event.Dat    // Make sure non-indexed bytes retain their type
				topic = dit.Event.IdxStr // Make sure indexed strings turn into topics containing hashes
				topic = dit.Event.IdxDat // Make sure indexed bytes turn into topics containing hashes

				sink := make(chan *EventCheckerMixed)
				sub, err := e.WatchMixed(nil, sink, []common.Address{})
				defer sub.Unsubscribe()

				event := <-sink
				fmt.Println(event.Raw.BlockHash) // Make sure the raw log is contained within the results
				fmt.Println(event.Num)           // Make sure the unpacked non-indexed fields are present
				fmt.Println(event.Addr)          // Make sure the reconstructed indexed fields are present

				fmt.Println(res, str, dat, topic, err)

				oit, err := e.FilterUnnamed(nil, []*big.Int{}, []*big.Int{})

				arg0  := oit.Event.Arg0    // Make sure unnamed arguments are handled correctly
				arg1  := oit.Event.Arg1    // Make sure unnamed arguments are handled correctly
				fmt.Println(arg0, arg1)
			}
			// Run a tiny reflection test to ensure disallowed methods don't appear
			if _, ok := reflect.TypeOf(&EventChecker{}).MethodByName("FilterAnonymous"); ok {
			t.Errorf("binding has disallowed method (FilterAnonymous)")
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Test that contract interactions (deploy, transact and call) generate working code
	{
		`Interactor`,
		`
			contract Interactor {
				string public deployString;
				string public transactString;

				constructor(string memory str) public {
					deployString = str;
				}

				function transact(string memory str) public {
					transactString = str;
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[{"internalType":"string","name":"str","type":"string"}],"stateMutability":"nonpayable","type":"constructor"},{"inputs":[],"name":"deployString","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"string","name":"str","type":"string"}],"name":"transact","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"transactString","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// Deploy an interaction tester contract and call a transaction on it
			_, _, interactor, err := DeployInteractor(auth, sim, "Deploy string")
			if err != nil {
				t.Fatalf("Failed to deploy interactor contract: %v", err)
			}
			if _, err := interactor.Transact(auth, "Transact string"); err != nil {
				t.Fatalf("Failed to transact with interactor contract: %v", err)
			}
			// Commit all pending transactions in the simulator and check the contract state
			sim.Commit()

			if str, err := interactor.DeployString(nil); err != nil {
				t.Fatalf("Failed to retrieve deploy string: %v", err)
			} else if str != "Deploy string" {
				t.Fatalf("Deploy string mismatch: have '%s', want 'Deploy string'", str)
			}
			if str, err := interactor.TransactString(nil); err != nil {
				t.Fatalf("Failed to retrieve transact string: %v", err)
			} else if str != "Transact string" {
				t.Fatalf("Transact string mismatch: have '%s', want 'Transact string'", str)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Tests that plain values can be properly returned and deserialized
	{
		`Getter`,
		`
			contract Getter {
				function getter() public pure returns (string memory, int, bytes32) {
					return ("Hi", 1, keccak256(""));
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[],"name":"getter","outputs":[{"internalType":"string","name":"","type":"string"},{"internalType":"int256","name":"","type":"int256"},{"internalType":"bytes32","name":"","type":"bytes32"}],"stateMutability":"pure","type":"function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// Deploy a tuple tester contract and execute a structured call on it
			_, _, getter, err := DeployGetter(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy getter contract: %v", err)
			}
			sim.Commit()

			if str, num, _, err := getter.Getter(nil); err != nil {
				t.Fatalf("Failed to call anonymous field retriever: %v", err)
			} else if str != "Hi" || num.Cmp(big.NewInt(1)) != 0 {
				t.Fatalf("Retrieved value mismatch: have %v/%v, want %v/%v", str, num, "Hi", 1)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Tests that tuples can be properly returned and deserialized
	{
		`Tupler`,
		`
			contract Tupler {
				function tuple() public pure returns (string memory a, int b, bytes32 c) {
					return ("Hi", 1, keccak256(""));
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[],"name":"tuple","outputs":[{"internalType":"string","name":"a","type":"string"},{"internalType":"int256","name":"b","type":"int256"},{"internalType":"bytes32","name":"c","type":"bytes32"}],"stateMutability":"pure","type":"function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// Deploy a tuple tester contract and execute a structured call on it
			_, _, tupler, err := DeployTupler(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy tupler contract: %v", err)
			}
			sim.Commit()

			if res, err := tupler.Tuple(nil); err != nil {
				t.Fatalf("Failed to call structure retriever: %v", err)
			} else if res.A != "Hi" || res.B.Cmp(big.NewInt(1)) != 0 {
				t.Fatalf("Retrieved value mismatch: have %v/%v, want %v/%v", res.A, res.B, "Hi", 1)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},

	// Tests that arrays/slices can be properly returned and deserialized.
	// Only addresses are tested, remainder just compiled to keep the test small.
	{
		`Slicer`,
		`
			contract Slicer {
				function echoAddresses(address[] memory input) public pure returns (address[] memory output) {
					return input;
				}
				function echoInts(int[] memory input) public pure returns (int[] memory output) {
					return input;
				}
				function echoFancyInts(uint24[23] memory input) public pure returns (uint24[23] memory output) {
					return input;
				}
				function echoBools(bool[] memory input) public pure returns (bool[] memory output) {
					return input;
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[{"internalType":"address[]","name":"input","type":"address[]"}],"name":"echoAddresses","outputs":[{"internalType":"address[]","name":"output","type":"address[]"}],"stateMutability":"pure","type":"function"},{"inputs":[{"internalType":"bool[]","name":"input","type":"bool[]"}],"name":"echoBools","outputs":[{"internalType":"bool[]","name":"output","type":"bool[]"}],"stateMutability":"pure","type":"function"},{"inputs":[{"internalType":"uint24[23]","name":"input","type":"uint24[23]"}],"name":"echoFancyInts","outputs":[{"internalType":"uint24[23]","name":"output","type":"uint24[23]"}],"stateMutability":"pure","type":"function"},{"inputs":[{"internalType":"int256[]","name":"input","type":"int256[]"}],"name":"echoInts","outputs":[{"internalType":"int256[]","name":"output","type":"int256[]"}],"stateMutability":"pure","type":"function"}]`},
		`
			"math/big"
			"reflect"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/common"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// Deploy a slice tester contract and execute a n array call on it
			_, _, slicer, err := DeploySlicer(auth, sim)
			if err != nil {
					t.Fatalf("Failed to deploy slicer contract: %v", err)
			}
			sim.Commit()

			if out, err := slicer.EchoAddresses(nil, []common.Address{auth.From, common.Address{}}); err != nil {
					t.Fatalf("Failed to call slice echoer: %v", err)
			} else if !reflect.DeepEqual(out, []common.Address{auth.From, common.Address{}}) {
					t.Fatalf("Slice return mismatch: have %v, want %v", out, []common.Address{auth.From, common.Address{}})
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Tests that structs are correctly unpacked
	{

		`Structs`,
		`
			pragma experimental ABIEncoderV2;
			contract Structs {
				struct A {
					bytes32 B;
				}

				function F() public view returns (A[] memory a, uint256[] memory c, bool[] memory d) {
					A[] memory a = new A[](2);
					a[0].B = bytes32(uint256(1234) << 96);
					uint256[] memory c;
					bool[] memory d;
					return (a, c, d);
				}

				function G() public view returns (A[] memory a) {
					A[] memory a = new A[](2);
					a[0].B = bytes32(uint256(1234) << 96);
					return a;
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[],"name":"F","outputs":[{"components":[{"internalType":"bytes32","name":"B","type":"bytes32"}],"internalType":"struct Structs.A[]","name":"a","type":"tuple[]"},{"internalType":"uint256[]","name":"c","type":"uint256[]"},{"internalType":"bool[]","name":"d","type":"bool[]"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"G","outputs":[{"components":[{"internalType":"bytes32","name":"B","type":"bytes32"}],"internalType":"struct Structs.A[]","name":"a","type":"tuple[]"}],"stateMutability":"view","type":"function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// Deploy a structs method invoker contract and execute its default method
			_, _, structs, err := DeployStructs(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy defaulter contract: %v", err)
			}
			sim.Commit()
			opts := bind.CallOpts{}
			if _, err := structs.F(&opts); err != nil {
				t.Fatalf("Failed to invoke F method: %v", err)
			}
			if _, err := structs.G(&opts); err != nil {
				t.Fatalf("Failed to invoke G method: %v", err)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Tests that non-existent contracts are reported as such (though only simulator test)
	{
		`NonExistent`,
		`
			contract NonExistent {
				function String() public pure returns(string memory) {
					return "I don't exist";
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[],"name":"String","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"pure","type":"function"}]`},
		`
			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/common"
			"github.com/theQRL/go-qrl/core"
		`,
		`
			// Create a simulator and wrap a non-deployed contract

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{}, uint64(10000000000))
			defer sim.Close()

			nonexistent, err := NewNonExistent(common.Address{}, sim)
			if err != nil {
				t.Fatalf("Failed to access non-existent contract: %v", err)
			}
			// Ensure that contract calls fail with the appropriate error
			if res, err := nonexistent.String(nil); err == nil {
				t.Fatalf("Call succeeded on non-existent contract: %v", res)
			} else if (err != bind.ErrNoCode) {
				t.Fatalf("Error mismatch: have %v, want %v", err, bind.ErrNoCode)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	{
		`NonExistentStruct`,
		`
			contract NonExistentStruct {
				function Struct() public pure returns(uint256 a, uint256 b) {
					return (10, 10);
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[],"name":"Struct","outputs":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256","name":"b","type":"uint256"}],"stateMutability":"pure","type":"function"}]`},
		`
			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/common"
			"github.com/theQRL/go-qrl/core"
		`,
		`
			// Create a simulator and wrap a non-deployed contract

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{}, uint64(10000000000))
			defer sim.Close()

			nonexistent, err := NewNonExistentStruct(common.Address{}, sim)
			if err != nil {
				t.Fatalf("Failed to access non-existent contract: %v", err)
			}
			// Ensure that contract calls fail with the appropriate error
			if res, err := nonexistent.Struct(nil); err == nil {
				t.Fatalf("Call succeeded on non-existent contract: %v", res)
			} else if (err != bind.ErrNoCode) {
				t.Fatalf("Error mismatch: have %v, want %v", err, bind.ErrNoCode)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Tests that gas estimation works for contracts with weird gas mechanics too.
	{
		`FunkyGasPattern`,
		`
			contract FunkyGasPattern {
				string public field;

				function SetField(string memory value) public {
					// This check will screw gas estimation! Good, good!
					if (gasleft() < 100000) {
						revert();
					}
					field = value;
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[{"internalType":"string","name":"value","type":"string"}],"name":"SetField","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"field","outputs":[{"internalType":"string","name":"","type":"string"}],"stateMutability":"view","type":"function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// Deploy a funky gas pattern contract
			_, _, limiter, err := DeployFunkyGasPattern(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy funky contract: %v", err)
			}
			sim.Commit()

			// Set the field with automatic estimation and check that it succeeds
			if _, err := limiter.SetField(auth, "automatic"); err != nil {
				t.Fatalf("Failed to call automatically gased transaction: %v", err)
			}
			sim.Commit()

			if field, _ := limiter.Field(nil); field != "automatic" {
				t.Fatalf("Field mismatch: have %v, want %v", field, "automatic")
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Test that constant functions can be called from an (optional) specified address
	{
		`CallFrom`,
		`
			contract CallFrom {
				function callFrom() public view returns(address) {
					return msg.sender;
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[],"name":"callFrom","outputs":[{"internalType":"address","name":"","type":"address"}],"stateMutability":"view","type":"function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/common"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// Deploy a sender tester contract and execute a structured call on it
			_, _, callfrom, err := DeployCallFrom(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy sender contract: %v", err)
			}
			sim.Commit()

			if res, err := callfrom.CallFrom(nil); err != nil {
				t.Errorf("Failed to call constant function: %v", err)
			} else if res != (common.Address{}) {
				t.Errorf("Invalid address returned, want: %x, got: %x", (common.Address{}), res)
			}

			for _, addr := range []common.Address{common.Address{}, common.Address{1}, common.Address{2}} {
				if res, err := callfrom.CallFrom(&bind.CallOpts{From: addr}); err != nil {
					t.Fatalf("Failed to call constant function: %v", err)
				} else if res != addr {
					t.Fatalf("Invalid address returned, want: %x, got: %x", addr, res)
				}
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Tests that methods and returns with underscores inside work correctly.
	{
		`Underscorer`,
		`
			contract Underscorer {
				function UnderscoredOutput() public pure returns (int _int, string memory _string) {
					return (314, "pi");
				}
				function LowerLowerCollision() public pure returns (int _res, int res) {
					return (1, 2);
				}
				function LowerUpperCollision() public pure returns (int _res, int Res) {
					return (1, 2);
				}
				function UpperLowerCollision() public pure returns (int _Res, int res) {
					return (1, 2);
				}
				function UpperUpperCollision() public pure returns (int _Res, int Res) {
					return (1, 2);
				}
				function _under_scored_func() public pure returns (int _int) {
					return 0;
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[],"name":"LowerLowerCollision","outputs":[{"internalType":"int256","name":"_res","type":"int256"},{"internalType":"int256","name":"res","type":"int256"}],"stateMutability":"pure","type":"function"},{"inputs":[],"name":"LowerUpperCollision","outputs":[{"internalType":"int256","name":"_res","type":"int256"},{"internalType":"int256","name":"Res","type":"int256"}],"stateMutability":"pure","type":"function"},{"inputs":[],"name":"UnderscoredOutput","outputs":[{"internalType":"int256","name":"_int","type":"int256"},{"internalType":"string","name":"_string","type":"string"}],"stateMutability":"pure","type":"function"},{"inputs":[],"name":"UpperLowerCollision","outputs":[{"internalType":"int256","name":"_Res","type":"int256"},{"internalType":"int256","name":"res","type":"int256"}],"stateMutability":"pure","type":"function"},{"inputs":[],"name":"UpperUpperCollision","outputs":[{"internalType":"int256","name":"_Res","type":"int256"},{"internalType":"int256","name":"Res","type":"int256"}],"stateMutability":"pure","type":"function"},{"inputs":[],"name":"_under_scored_func","outputs":[{"internalType":"int256","name":"_int","type":"int256"}],"stateMutability":"pure","type":"function"}]`},
		`
			"fmt"
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// Deploy a underscorer tester contract and execute a structured call on it
			_, _, underscorer, err := DeployUnderscorer(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy underscorer contract: %v", err)
			}
			sim.Commit()

			// Verify that underscored return values correctly parse into structs
			if res, err := underscorer.UnderscoredOutput(nil); err != nil {
				t.Errorf("Failed to call constant function: %v", err)
			} else if res.Int.Cmp(big.NewInt(314)) != 0 || res.String != "pi" {
				t.Errorf("Invalid result, want: {314, \"pi\"}, got: %+v", res)
			}
			// Verify that underscored and non-underscored name collisions force tuple outputs
			var a, b *big.Int

			a, b, _ = underscorer.LowerLowerCollision(nil)
			a, b, _ = underscorer.LowerUpperCollision(nil)
			a, b, _ = underscorer.UpperLowerCollision(nil)
			a, b, _ = underscorer.UpperUpperCollision(nil)
			a, _ = underscorer.UnderScoredFunc(nil)

			fmt.Println(a, b, err)
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Tests that logs can be successfully filtered and decoded.
	{
		`Eventer`,
		`
			contract Eventer {
				event SimpleEvent (
					address indexed Addr,
					bytes32 indexed Id,
					bool    indexed Flag,
					uint    Value
				);
				function raiseSimpleEvent(address addr, bytes32 id, bool flag, uint value) {
					SimpleEvent(addr, id, flag, value);
				}

				event NodataEvent (
					uint   indexed Number,
					int16  indexed Short,
					uint32 indexed Long
				);
				function raiseNodataEvent(uint number, int16 short, uint32 long) {
					NodataEvent(number, short, long);
				}

				event DynamicEvent (
					string indexed IndexedString,
					bytes  indexed IndexedBytes,
					string NonIndexedString,
					bytes  NonIndexedBytes
				);
				function raiseDynamicEvent(string str, bytes blob) {
					DynamicEvent(str, blob, str, blob);
				}

				event FixedBytesEvent (
					bytes24 indexed IndexedBytes,
					bytes24 NonIndexedBytes
				);
				function raiseFixedBytesEvent(bytes24 blob) {
					FixedBytesEvent(blob, blob);
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"anonymous":false,"inputs":[{"indexed":true,"internalType":"string","name":"IndexedString","type":"string"},{"indexed":true,"internalType":"bytes","name":"IndexedBytes","type":"bytes"},{"indexed":false,"internalType":"string","name":"NonIndexedString","type":"string"},{"indexed":false,"internalType":"bytes","name":"NonIndexedBytes","type":"bytes"}],"name":"DynamicEvent","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"bytes24","name":"IndexedBytes","type":"bytes24"},{"indexed":false,"internalType":"bytes24","name":"NonIndexedBytes","type":"bytes24"}],"name":"FixedBytesEvent","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256","name":"Number","type":"uint256"},{"indexed":true,"internalType":"int16","name":"Short","type":"int16"},{"indexed":true,"internalType":"uint32","name":"Long","type":"uint32"}],"name":"NodataEvent","type":"event"},{"anonymous":false,"inputs":[{"indexed":true,"internalType":"address","name":"Addr","type":"address"},{"indexed":true,"internalType":"bytes32","name":"Id","type":"bytes32"},{"indexed":true,"internalType":"bool","name":"Flag","type":"bool"},{"indexed":false,"internalType":"uint256","name":"Value","type":"uint256"}],"name":"SimpleEvent","type":"event"},{"inputs":[{"internalType":"string","name":"str","type":"string"},{"internalType":"bytes","name":"blob","type":"bytes"}],"name":"raiseDynamicEvent","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"bytes24","name":"blob","type":"bytes24"}],"name":"raiseFixedBytesEvent","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"number","type":"uint256"},{"internalType":"int16","name":"short","type":"int16"},{"internalType":"uint32","name":"long","type":"uint32"}],"name":"raiseNodataEvent","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"address","name":"addr","type":"address"},{"internalType":"bytes32","name":"id","type":"bytes32"},{"internalType":"bool","name":"flag","type":"bool"},{"internalType":"uint256","name":"value","type":"uint256"}],"name":"raiseSimpleEvent","outputs":[],"stateMutability":"nonpayable","type":"function"}]`},
		`
			"math/big"
			"time"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/common"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// Deploy an eventer contract
			_, _, eventer, err := DeployEventer(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy eventer contract: %v", err)
			}
			sim.Commit()

			// Inject a few events into the contract, gradually more in each block
			for i := 1; i <= 3; i++ {
				for j := 1; j <= i; j++ {
					if _, err := eventer.RaiseSimpleEvent(auth, common.Address{byte(j)}, [32]byte{byte(j)}, true, big.NewInt(int64(10*i+j))); err != nil {
						t.Fatalf("block %d, event %d: raise failed: %v", i, j, err)
					}
				}
				sim.Commit()
			}
			// Test filtering for certain events and ensure they can be found
			sit, err := eventer.FilterSimpleEvent(nil, []common.Address{common.Address{1}, common.Address{3}}, [][32]byte{{byte(1)}, {byte(2)}, {byte(3)}}, []bool{true})
			if err != nil {
				t.Fatalf("failed to filter for simple events: %v", err)
			}
			defer sit.Close()

			sit.Next()
			if sit.Event.Value.Uint64() != 11 || !sit.Event.Flag {
				t.Errorf("simple log content mismatch: have %v, want {11, true}", sit.Event)
			}
			sit.Next()
			if sit.Event.Value.Uint64() != 21 || !sit.Event.Flag {
				t.Errorf("simple log content mismatch: have %v, want {21, true}", sit.Event)
			}
			sit.Next()
			if sit.Event.Value.Uint64() != 31 || !sit.Event.Flag {
				t.Errorf("simple log content mismatch: have %v, want {31, true}", sit.Event)
			}
			sit.Next()
			if sit.Event.Value.Uint64() != 33 || !sit.Event.Flag {
				t.Errorf("simple log content mismatch: have %v, want {33, true}", sit.Event)
			}

			if sit.Next() {
				t.Errorf("unexpected simple event found: %+v", sit.Event)
			}
			if err = sit.Error(); err != nil {
				t.Fatalf("simple event iteration failed: %v", err)
			}
			// Test raising and filtering for an event with no data component
			if _, err := eventer.RaiseNodataEvent(auth, big.NewInt(314), 141, 271); err != nil {
				t.Fatalf("failed to raise nodata event: %v", err)
			}
			sim.Commit()

			nit, err := eventer.FilterNodataEvent(nil, []*big.Int{big.NewInt(314)}, []int16{140, 141, 142}, []uint32{271})
			if err != nil {
				t.Fatalf("failed to filter for nodata events: %v", err)
			}
			defer nit.Close()

			if !nit.Next() {
				t.Fatalf("nodata log not found: %v", nit.Error())
			}
			if nit.Event.Number.Uint64() != 314 {
				t.Errorf("nodata log content mismatch: have %v, want 314", nit.Event.Number)
			}
			if nit.Next() {
				t.Errorf("unexpected nodata event found: %+v", nit.Event)
			}
			if err = nit.Error(); err != nil {
				t.Fatalf("nodata event iteration failed: %v", err)
			}
			// Test raising and filtering for events with dynamic indexed components
			if _, err := eventer.RaiseDynamicEvent(auth, "Hello", []byte("World")); err != nil {
				t.Fatalf("failed to raise dynamic event: %v", err)
			}
			sim.Commit()

			dit, err := eventer.FilterDynamicEvent(nil, []string{"Hi", "Hello", "Bye"}, [][]byte{[]byte("World")})
			if err != nil {
				t.Fatalf("failed to filter for dynamic events: %v", err)
			}
			defer dit.Close()

			if !dit.Next() {
				t.Fatalf("dynamic log not found: %v", dit.Error())
			}
			if dit.Event.NonIndexedString != "Hello" || string(dit.Event.NonIndexedBytes) != "World" || dit.Event.IndexedString != common.HexToLogTopic("0x06b3dfaec148fb1bb2b066f10ec285e7c9bf402ab32aa78a5d38e34566810cd2") || dit.Event.IndexedBytes != common.HexToLogTopic("0xf2208c967df089f60420785795c0a9ba8896b0f6f1867fa7f1f12ad6f79c1a18") {
				t.Errorf("dynamic log content mismatch: have %v, want {'0x06b3dfaec148fb1bb2b066f10ec285e7c9bf402ab32aa78a5d38e34566810cd2, '0xf2208c967df089f60420785795c0a9ba8896b0f6f1867fa7f1f12ad6f79c1a18', 'Hello', 'World'}", dit.Event)
			}
			if dit.Next() {
				t.Errorf("unexpected dynamic event found: %+v", dit.Event)
			}
			if err = dit.Error(); err != nil {
				t.Fatalf("dynamic event iteration failed: %v", err)
			}
			// Test raising and filtering for events with fixed bytes components
			var fblob [24]byte
			copy(fblob[:], []byte("Fixed Bytes"))

			if _, err := eventer.RaiseFixedBytesEvent(auth, fblob); err != nil {
				t.Fatalf("failed to raise fixed bytes event: %v", err)
			}
			sim.Commit()

			fit, err := eventer.FilterFixedBytesEvent(nil, [][24]byte{fblob})
			if err != nil {
				t.Fatalf("failed to filter for fixed bytes events: %v", err)
			}
			defer fit.Close()

			if !fit.Next() {
				t.Fatalf("fixed bytes log not found: %v", fit.Error())
			}
			if fit.Event.NonIndexedBytes != fblob || fit.Event.IndexedBytes != fblob {
				t.Errorf("fixed bytes log content mismatch: have %v, want {'%x', '%x'}", fit.Event, fblob, fblob)
			}
			if fit.Next() {
				t.Errorf("unexpected fixed bytes event found: %+v", fit.Event)
			}
			if err = fit.Error(); err != nil {
				t.Fatalf("fixed bytes event iteration failed: %v", err)
			}
			// Test subscribing to an event and raising it afterwards
			ch := make(chan *EventerSimpleEvent, 16)
			sub, err := eventer.WatchSimpleEvent(nil, ch, nil, nil, nil)
			if err != nil {
				t.Fatalf("failed to subscribe to simple events: %v", err)
			}
			if _, err := eventer.RaiseSimpleEvent(auth, common.Address{255}, [32]byte{255}, true, big.NewInt(255)); err != nil {
				t.Fatalf("failed to raise subscribed simple event: %v", err)
			}
			sim.Commit()

			select {
			case event := <-ch:
				if event.Value.Uint64() != 255 {
					t.Errorf("simple log content mismatch: have %v, want 255", event)
				}
			case <-time.After(250 * time.Millisecond):
				t.Fatalf("subscribed simple event didn't arrive")
			}
			// Unsubscribe from the event and make sure we're not delivered more
			sub.Unsubscribe()

			if _, err := eventer.RaiseSimpleEvent(auth, common.Address{254}, [32]byte{254}, true, big.NewInt(254)); err != nil {
				t.Fatalf("failed to raise subscribed simple event: %v", err)
			}
			sim.Commit()

			select {
			case event := <-ch:
				t.Fatalf("unsubscribed simple event arrived: %v", event)
			case <-time.After(250 * time.Millisecond):
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	{
		`DeeplyNestedArray`,
		`
		contract DeeplyNestedArray {
			uint64[3][4][5] public deepUint64Array;
			function storeDeepUintArray(uint64[3][4][5] memory arr) public {
				deepUint64Array = arr;
			}
			function retrieveDeepArray() public view returns (uint64[3][4][5] memory) {
				return deepUint64Array;
			}
		}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[{"internalType":"uint256","name":"","type":"uint256"},{"internalType":"uint256","name":"","type":"uint256"},{"internalType":"uint256","name":"","type":"uint256"}],"name":"deepUint64Array","outputs":[{"internalType":"uint64","name":"","type":"uint64"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"retrieveDeepArray","outputs":[{"internalType":"uint64[3][4][5]","name":"","type":"uint64[3][4][5]"}],"stateMutability":"view","type":"function"},{"inputs":[{"internalType":"uint64[3][4][5]","name":"arr","type":"uint64[3][4][5]"}],"name":"storeDeepUintArray","outputs":[],"stateMutability":"nonpayable","type":"function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			//deploy the test contract
			_, _, testContract, err := DeployDeeplyNestedArray(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy test contract: %v", err)
			}

			// Finish deploy.
			sim.Commit()

			//Create coordinate-filled array, for testing purposes.
			testArr := [5][4][3]uint64{}
			for i := range 5 {
				testArr[i] = [4][3]uint64{}
				for j := range 4 {
					testArr[i][j] = [3]uint64{}
					for k := range 3 {
						//pack the coordinates, each array value will be unique, and can be validated easily.
						testArr[i][j][k] = uint64(i) << 16 | uint64(j) << 8 | uint64(k)
					}
				}
			}

			if _, err := testContract.StoreDeepUintArray(&bind.TransactOpts{
				From: auth.From,
				Signer: auth.Signer,
			}, testArr); err != nil {
				t.Fatalf("Failed to store nested array in test contract: %v", err)
			}

			sim.Commit()

			retrievedArr, err := testContract.RetrieveDeepArray(&bind.CallOpts{
				From: auth.From,
				Pending: false,
			})
			if err != nil {
				t.Fatalf("Failed to retrieve nested array from test contract: %v", err)
			}

			//quick check to see if contents were copied
			// (See accounts/abi/unpack_test.go for more extensive testing)
			if retrievedArr[4][3][2] != testArr[4][3][2] {
				t.Fatalf("Retrieved value does not match expected value! got: %d, expected: %d. %v", retrievedArr[4][3][2], testArr[4][3][2], err)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	{
		`Tuple`,
		`
		pragma experimental ABIEncoderV2;

		contract Tuple {
			struct S { uint a; uint[] b; T[] c; }
			struct T { uint x; uint y; }
			struct P { uint8 x; uint8 y; }
			struct Q { uint16 x; uint16 y; }
			event TupleEvent(S a, T[2][] b, T[][2] c, S[] d, uint[] e);
			event TupleEvent2(P[]);

			function func1(S memory a, T[2][] memory b, T[][2] memory c, S[] memory d, uint[] memory e) public pure returns (S memory, T[2][] memory, T[][2] memory, S[] memory, uint[] memory) {
				return (a, b, c, d, e);
			}
			function func2(S memory a, T[2][] memory b, T[][2] memory c, S[] memory d, uint[] memory e) public {
				emit TupleEvent(a, b, c, d, e);
			}
			function func3(Q[] memory) public pure {} // call function, nothing to return
		}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"anonymous":false,"inputs":[{"components":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256[]","name":"b","type":"uint256[]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[]","name":"c","type":"tuple[]"}],"indexed":false,"internalType":"struct Tuple.S","name":"a","type":"tuple"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"indexed":false,"internalType":"struct Tuple.T[2][]","name":"b","type":"tuple[2][]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"indexed":false,"internalType":"struct Tuple.T[][2]","name":"c","type":"tuple[][2]"},{"components":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256[]","name":"b","type":"uint256[]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[]","name":"c","type":"tuple[]"}],"indexed":false,"internalType":"struct Tuple.S[]","name":"d","type":"tuple[]"},{"indexed":false,"internalType":"uint256[]","name":"e","type":"uint256[]"}],"name":"TupleEvent","type":"event"},{"anonymous":false,"inputs":[{"components":[{"internalType":"uint8","name":"x","type":"uint8"},{"internalType":"uint8","name":"y","type":"uint8"}],"indexed":false,"internalType":"struct Tuple.P[]","name":"","type":"tuple[]"}],"name":"TupleEvent2","type":"event"},{"inputs":[{"components":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256[]","name":"b","type":"uint256[]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[]","name":"c","type":"tuple[]"}],"internalType":"struct Tuple.S","name":"a","type":"tuple"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[2][]","name":"b","type":"tuple[2][]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[][2]","name":"c","type":"tuple[][2]"},{"components":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256[]","name":"b","type":"uint256[]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[]","name":"c","type":"tuple[]"}],"internalType":"struct Tuple.S[]","name":"d","type":"tuple[]"},{"internalType":"uint256[]","name":"e","type":"uint256[]"}],"name":"func1","outputs":[{"components":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256[]","name":"b","type":"uint256[]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[]","name":"c","type":"tuple[]"}],"internalType":"struct Tuple.S","name":"","type":"tuple"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[2][]","name":"","type":"tuple[2][]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[][2]","name":"","type":"tuple[][2]"},{"components":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256[]","name":"b","type":"uint256[]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[]","name":"c","type":"tuple[]"}],"internalType":"struct Tuple.S[]","name":"","type":"tuple[]"},{"internalType":"uint256[]","name":"","type":"uint256[]"}],"stateMutability":"pure","type":"function"},{"inputs":[{"components":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256[]","name":"b","type":"uint256[]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[]","name":"c","type":"tuple[]"}],"internalType":"struct Tuple.S","name":"a","type":"tuple"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[2][]","name":"b","type":"tuple[2][]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[][2]","name":"c","type":"tuple[][2]"},{"components":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256[]","name":"b","type":"uint256[]"},{"components":[{"internalType":"uint256","name":"x","type":"uint256"},{"internalType":"uint256","name":"y","type":"uint256"}],"internalType":"struct Tuple.T[]","name":"c","type":"tuple[]"}],"internalType":"struct Tuple.S[]","name":"d","type":"tuple[]"},{"internalType":"uint256[]","name":"e","type":"uint256[]"}],"name":"func2","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"components":[{"internalType":"uint16","name":"x","type":"uint16"},{"internalType":"uint16","name":"y","type":"uint16"}],"internalType":"struct Tuple.Q[]","name":"","type":"tuple[]"}],"name":"func3","outputs":[],"stateMutability":"pure","type":"function"}]`},
		`
			"math/big"
			"reflect"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			_, _, contract, err := DeployTuple(auth, sim)
			if err != nil {
				t.Fatalf("deploy contract failed %v", err)
			}
			sim.Commit()

			check := func(a, b any, errMsg string) {
				if !reflect.DeepEqual(a, b) {
					t.Fatal(errMsg)
				}
			}

			a := TupleS{
				A: big.NewInt(1),
				B: []*big.Int{big.NewInt(2), big.NewInt(3)},
				C: []TupleT{
					{
						X: big.NewInt(4),
						Y: big.NewInt(5),
					},
					{
						X: big.NewInt(6),
						Y: big.NewInt(7),
					},
				},
			}

			b := [][2]TupleT{
				{
					{
						X: big.NewInt(8),
						Y: big.NewInt(9),
					},
					{
						X: big.NewInt(10),
						Y: big.NewInt(11),
					},
				},
			}

			c := [2][]TupleT{
				{
					{
						X: big.NewInt(12),
						Y: big.NewInt(13),
					},
					{
						X: big.NewInt(14),
						Y: big.NewInt(15),
					},
				},
				{
					{
						X: big.NewInt(16),
						Y: big.NewInt(17),
					},
				},
			}

			d := []TupleS{a}

			e := []*big.Int{big.NewInt(18), big.NewInt(19)}
			ret1, ret2, ret3, ret4, ret5, err := contract.Func1(nil, a, b, c, d, e)
			if err != nil {
				t.Fatalf("invoke contract failed, err %v", err)
			}
			check(ret1, a, "ret1 mismatch")
			check(ret2, b, "ret2 mismatch")
			check(ret3, c, "ret3 mismatch")
			check(ret4, d, "ret4 mismatch")
			check(ret5, e, "ret5 mismatch")

			_, err = contract.Func2(auth, a, b, c, d, e)
			if err != nil {
				t.Fatalf("invoke contract failed, err %v", err)
			}
			sim.Commit()

			iter, err := contract.FilterTupleEvent(nil)
			if err != nil {
				t.Fatalf("failed to create event filter, err %v", err)
			}
			defer iter.Close()

			iter.Next()
			check(iter.Event.A, a, "field1 mismatch")
			check(iter.Event.B, b, "field2 mismatch")
			check(iter.Event.C, c, "field3 mismatch")
			check(iter.Event.D, d, "field4 mismatch")
			check(iter.Event.E, e, "field5 mismatch")

			err = contract.Func3(nil, nil)
			if err != nil {
				t.Fatalf("failed to call function which has no return, err %v", err)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	{
		`UseLibrary`,
		`
		library Math {
			function add(uint a, uint b) public view returns(uint) {
				return a + b;
			}
		}

		contract UseLibrary {
			function add (uint c, uint d) public view returns(uint) {
				return Math.add(c,d);
			}
		}
			`,
		[]string{
			// UseLibrary keeps a PUSH64 address-width placeholder so the
			// generated linker path is still covered.
			`9f__$` + useLibraryMathPlaceholder + `$__00`,
			// Bytecode for the Math contract
			qrvmNoopBytecode,
		},
		[]string{
			`[{"inputs":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256","name":"b","type":"uint256"}],"name":"add","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`,
			`[{"inputs":[{"internalType":"uint256","name":"c","type":"uint256"},{"internalType":"uint256","name":"d","type":"uint256"}],"name":"add","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`,
		},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			//deploy the test contract
			_, _, testContract, err := DeployUseLibrary(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy test contract: %v", err)
			}

			// Finish deploy.
			sim.Commit()

			// Check that the library contract has been deployed
			// by calling the contract's add function.
			res, err := testContract.Add(&bind.CallOpts{
				From: auth.From,
				Pending: false,
			}, big.NewInt(1), big.NewInt(2))
			if err != nil {
				t.Fatalf("Failed to call linked contract: %v", err)
			}
			if res.Cmp(big.NewInt(3)) != 0 {
				t.Fatalf("Add did not return the correct result: %d != %d", res, 3)
			}
		`,
		nil,
		map[string]string{
			useLibraryMathPlaceholder: "Math",
		},
		nil,
		[]string{"UseLibrary", "Math"},
	},
	{
		"Overload",
		`
		contract overload {
			mapping(address => uint256) balances;

			event bar(uint256 i);
			event bar(uint256 i, uint256 j);

			function foo(uint256 i) public {
				emit bar(i);
			}
			function foo(uint256 i, uint256 j) public {
				emit bar(i, j);
			}
		}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"anonymous":false,"inputs":[{"indexed":false,"internalType":"uint256","name":"i","type":"uint256"}],"name":"bar","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"uint256","name":"i","type":"uint256"},{"indexed":false,"internalType":"uint256","name":"j","type":"uint256"}],"name":"bar","type":"event"},{"inputs":[{"internalType":"uint256","name":"i","type":"uint256"},{"internalType":"uint256","name":"j","type":"uint256"}],"name":"foo","outputs":[],"stateMutability":"nonpayable","type":"function"},{"inputs":[{"internalType":"uint256","name":"i","type":"uint256"}],"name":"foo","outputs":[],"stateMutability":"nonpayable","type":"function"}]`},
		`
			"math/big"
			"time"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Initialize test accounts
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// deploy the test contract
			_, _, contract, err := DeployOverload(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy contract: %v", err)
			}
			// Finish deploy.
			sim.Commit()

			resCh, stopCh := make(chan uint64), make(chan struct{})

			go func() {
				barSink := make(chan *OverloadBar)
				sub, _ := contract.WatchBar(nil, barSink)
				defer sub.Unsubscribe()

				bar0Sink := make(chan *OverloadBar0)
				sub0, _ := contract.WatchBar0(nil, bar0Sink)
				defer sub0.Unsubscribe()

				for {
					select {
					case ev := <-barSink:
						resCh <- ev.I.Uint64()
					case ev := <-bar0Sink:
						resCh <- ev.I.Uint64() + ev.J.Uint64()
					case <-stopCh:
						return
					}
				}
			}()
			contract.Foo(auth, big.NewInt(1), big.NewInt(2))
			sim.Commit()
			select {
			case n := <-resCh:
				if n != 3 {
					t.Fatalf("Invalid bar0 event")
				}
			case <-time.NewTimer(3 * time.Second).C:
				t.Fatalf("Wait bar0 event timeout")
			}

			contract.Foo0(auth, big.NewInt(1))
			sim.Commit()
			select {
			case n := <-resCh:
				if n != 1 {
					t.Fatalf("Invalid bar event")
				}
			case <-time.NewTimer(3 * time.Second).C:
				t.Fatalf("Wait bar event timeout")
			}
			close(stopCh)
		`,
		nil,
		nil,
		nil,
		nil,
	},
	{
		"IdentifierCollision",
		`
		contract IdentifierCollision {
			uint public _myVar;

			function MyVar() public view returns (uint) {
				return _myVar;
			}
		}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[],"name":"MyVar","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"_myVar","outputs":[{"internalType":"uint256","name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
			"github.com/theQRL/go-qrl/core"
		`,
		`
			// Initialize test accounts
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			addr := wallet.GetAddress()

			// Deploy registrar contract
			sim := backends.NewSimulatedBackend(core.GenesisAlloc{addr: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			transactOpts, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
			_, _, _, err := DeployIdentifierCollision(transactOpts, sim)
			if err != nil {
				t.Fatalf("failed to deploy contract: %v", err)
			}
		`,
		nil,
		nil,
		map[string]string{"_myVar": "pubVar"}, // alias MyVar to PubVar
		nil,
	},
	{
		"MultiContracts",
		`
		pragma experimental ABIEncoderV2;

		library ExternalLib {
			struct SharedStruct{
				uint256 f1;
				bytes32 f2;
			}
		}

		contract ContractOne {
			function foo(ExternalLib.SharedStruct memory s) pure public {
				// Do stuff
			}
		}

		contract ContractTwo {
			function bar(ExternalLib.SharedStruct memory s) pure public {
				// Do stuff
			}
		}
			`,
		[]string{
			qrvmNoopBytecode,
			qrvmNoopBytecode,
			qrvmNoopBytecode,
		},
		[]string{
			`[{"inputs":[{"components":[{"internalType":"uint256","name":"f1","type":"uint256"},{"internalType":"bytes32","name":"f2","type":"bytes32"}],"internalType":"struct ExternalLib.SharedStruct","name":"s","type":"tuple"}],"name":"foo","outputs":[],"stateMutability":"pure","type":"function"}]`,
			`[{"inputs":[{"components":[{"internalType":"uint256","name":"f1","type":"uint256"},{"internalType":"bytes32","name":"f2","type":"bytes32"}],"internalType":"struct ExternalLib.SharedStruct","name":"s","type":"tuple"}],"name":"bar","outputs":[],"stateMutability":"pure","type":"function"}]`,
			`[]`,
		},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
			"github.com/theQRL/go-qrl/core"
		`,
		`
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			addr := wallet.GetAddress()

			// Deploy registrar contract
			sim := backends.NewSimulatedBackend(core.GenesisAlloc{addr: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			transactOpts, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
			_, _, c1, err := DeployContractOne(transactOpts, sim)
			if err != nil {
				t.Fatal("Failed to deploy contract")
			}
			sim.Commit()
			err = c1.Foo(nil, ExternalLibSharedStruct{
				F1: big.NewInt(100),
				F2: [32]byte{0x01, 0x02, 0x03},
			})
			if err != nil {
				t.Fatal("Failed to invoke function")
			}
			_, _, c2, err := DeployContractTwo(transactOpts, sim)
			if err != nil {
				t.Fatal("Failed to deploy contract")
			}
			sim.Commit()
			err = c2.Bar(nil, ExternalLibSharedStruct{
				F1: big.NewInt(100),
				F2: [32]byte{0x01, 0x02, 0x03},
			})
			if err != nil {
				t.Fatal("Failed to invoke function")
			}
		`,
		nil,
		nil,
		nil,
		[]string{"ContractOne", "ContractTwo", "ExternalLib"},
	},
	// Test the existence of the free retrieval calls
	{
		`PureAndView`,
		`
		contract PureAndView {
			function PureFunc() public pure returns (uint) {
				return 42;
			}
			function ViewFunc() public view returns (uint) {
				return block.number;
			}
		}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs": [],"name": "PureFunc","outputs": [{"internalType": "uint256","name": "","type": "uint256"}],"stateMutability": "pure","type": "function"},{"inputs": [],"name": "ViewFunc","outputs": [{"internalType": "uint256","name": "","type": "uint256"}],"stateMutability": "view","type": "function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			// Generate a new random account and a funded simulator
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			auth, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{auth.From: {Balance: big.NewInt(9000000000000000000)}}, 10000000)
			defer sim.Close()

			// Deploy a tester contract and execute a structured call on it
			_, _, pav, err := DeployPureAndView(auth, sim)
			if err != nil {
				t.Fatalf("Failed to deploy PureAndView contract: %v", err)
			}
			sim.Commit()

			// This test the existence of the free retreiver call for view and pure functions
			if num, err := pav.PureFunc(nil); err != nil {
				t.Fatalf("Failed to call anonymous field retriever: %v", err)
			} else if num.Cmp(big.NewInt(42)) != 0 {
				t.Fatalf("Retrieved value mismatch: have %v, want %v", num, 42)
			}
			if num, err := pav.ViewFunc(nil); err != nil {
				t.Fatalf("Failed to call anonymous field retriever: %v", err)
			} else if num.Cmp(big.NewInt(1)) != 0 {
				t.Fatalf("Retrieved value mismatch: have %v, want %v", num, 1)
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Test fallback separation
	{
		`NewFallbacks`,
		`
		contract NewFallbacks {
			event Fallback(bytes data);
			fallback() external {
				emit Fallback(msg.data);
			}

			event Received(address addr, uint value);
			receive() external payable {
				emit Received(msg.sender, msg.value);
			}
		}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"anonymous":false,"inputs":[{"indexed":false,"internalType":"bytes","name":"data","type":"bytes"}],"name":"Fallback","type":"event"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"addr","type":"address"},{"indexed":false,"internalType":"uint256","name":"value","type":"uint256"}],"name":"Received","type":"event"},{"stateMutability":"nonpayable","type":"fallback"},{"stateMutability":"payable","type":"receive"}]`},
		`
			"bytes"
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
		`,
		`
			wallet, _ := wallet.Generate(wallet.ML_DSA_87)
			addr := wallet.GetAddress()

			sim := backends.NewSimulatedBackend(core.GenesisAlloc{addr: {Balance: big.NewInt(9000000000000000000)}}, 1000000)
			defer sim.Close()

			opts, _ := bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
			_, _, c, err := DeployNewFallbacks(opts, sim)
			if err != nil {
				t.Fatalf("Failed to deploy contract: %v", err)
			}
			sim.Commit()

			// Test receive function
			opts.Value = big.NewInt(100)
			c.Receive(opts)
			sim.Commit()

			var gotEvent bool
			iter, _ := c.FilterReceived(nil)
			defer iter.Close()
			for iter.Next() {
				if iter.Event.Addr != addr {
					t.Fatal("Msg.sender mismatch")
				}
				if iter.Event.Value.Uint64() != 100 {
					t.Fatal("Msg.value mismatch")
				}
				gotEvent = true
				break
			}
			if !gotEvent {
				t.Fatal("Expect to receive event emitted by receive")
			}

			// Test fallback function
			gotEvent = false
			opts.Value = nil
			calldata := []byte{0x01, 0x02, 0x03}
			c.Fallback(opts, calldata)
			sim.Commit()

			iter2, _ := c.FilterFallback(nil)
			defer iter2.Close()
			for iter2.Next() {
				if !bytes.Equal(iter2.Event.Data, calldata) {
					t.Fatal("calldata mismatch")
				}
				gotEvent = true
				break
			}
			if !gotEvent {
				t.Fatal("Expect to receive event emitted by fallback")
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Test resolving single struct argument
	{
		`NewSingleStructArgument`,
		`
			contract NewSingleStructArgument {
				struct MyStruct{
					uint256 a;
					uint256 b;
				}
				event StructEvent(MyStruct s);
				function TestEvent() public {
					emit StructEvent(MyStruct({a: 1, b: 2}));
				}
			}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"anonymous":false,"inputs":[{"components":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256","name":"b","type":"uint256"}],"indexed":false,"internalType":"struct NewSingleStructArgument.MyStruct","name":"s","type":"tuple"}],"name":"StructEvent","type":"event"},{"inputs":[],"name":"TestEvent","outputs":[],"stateMutability":"nonpayable","type":"function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
			"github.com/theQRL/go-qrl/qrl/qrlconfig"
		`,
		`
			var (
				wallet, _  = wallet.Generate(wallet.ML_DSA_87)
				user, _ = bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
				sim     = backends.NewSimulatedBackend(core.GenesisAlloc{user.From: {Balance: big.NewInt(1000000000000000000)}}, qrlconfig.Defaults.Miner.GasCeil)
			)
			defer sim.Close()

			_, _, d, err := DeployNewSingleStructArgument(user, sim)
			if err != nil {
				t.Fatalf("Failed to deploy contract %v", err)
			}
			sim.Commit()

			_, err = d.TestEvent(user)
			if err != nil {
				t.Fatalf("Failed to call contract %v", err)
			}
			sim.Commit()

			it, err := d.FilterStructEvent(nil)
			if err != nil {
				t.Fatalf("Failed to filter contract event %v", err)
			}
			var count int
			for it.Next() {
				if it.Event.S.A.Cmp(big.NewInt(1)) != 0 {
					t.Fatal("Unexpected contract event")
				}
				if it.Event.S.B.Cmp(big.NewInt(2)) != 0 {
					t.Fatal("Unexpected contract event")
				}
				count += 1
			}
			if count != 1 {
				t.Fatal("Unexpected contract event number")
			}
		`,
		nil,
		nil,
		nil,
		nil,
	},
	// Test errors
	{
		`NewErrors`,
		`
		contract NewErrors {
			error MyError(uint256);
			error MyError1(uint256);
			error MyError2(uint256, uint256);
			error MyError3(uint256 a, uint256 b, uint256 c);
			function Error() public pure {
				revert MyError3(1,2,3);
			}
		}
		`,
		[]string{qrvmNoopBytecode},
		[]string{`[{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"MyError","type":"error"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"}],"name":"MyError1","type":"error"},{"inputs":[{"internalType":"uint256","name":"","type":"uint256"},{"internalType":"uint256","name":"","type":"uint256"}],"name":"MyError2","type":"error"},{"inputs":[{"internalType":"uint256","name":"a","type":"uint256"},{"internalType":"uint256","name":"b","type":"uint256"},{"internalType":"uint256","name":"c","type":"uint256"}],"name":"MyError3","type":"error"},{"inputs":[],"name":"Error","outputs":[],"stateMutability":"pure","type":"function"}]`},
		`
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
			"github.com/theQRL/go-qrl/qrl/qrlconfig"
		`,
		`
			var (
				wallet, _  = wallet.Generate(wallet.ML_DSA_87)
				user, _ = bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
				sim     = backends.NewSimulatedBackend(core.GenesisAlloc{user.From: {Balance: big.NewInt(1000000000000000000)}}, qrlconfig.Defaults.Miner.GasCeil)
			)
			defer sim.Close()

			_, tx, contract, err := DeployNewErrors(user, sim)
			if err != nil {
				t.Fatal(err)
			}
			sim.Commit()
			_, err = bind.WaitDeployed(nil, sim, tx)
			if err != nil {
				t.Error(err)
			}
			if err := contract.Error(new(bind.CallOpts)); err == nil {
				t.Fatalf("expected contract to throw error")
			}
			// TODO (MariusVanDerWijden unpack error using abigen
			// once that is implemented
		`,
		nil,
		nil,
		nil,
		nil,
	},
	{
		name: `ConstructorWithStructParam`,
		contract: `
		contract ConstructorWithStructParam {
			struct StructType {
				uint256 field;
			}

			constructor(StructType memory st) {}
		}
		`,
		bytecode: []string{qrvmNoopBytecode},
		abi:      []string{`[{"inputs":[{"components":[{"internalType":"uint256","name":"field","type":"uint256"}],"internalType":"struct ConstructorWithStructParam.StructType","name":"st","type":"tuple"}],"stateMutability":"nonpayable","type":"constructor"}]`},
		imports: `
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
			"github.com/theQRL/go-qrl/qrl/qrlconfig"
		`,
		tester: `
			var (
				wallet, _  = wallet.Generate(wallet.ML_DSA_87)
				user, _ = bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
				sim     = backends.NewSimulatedBackend(core.GenesisAlloc{user.From: {Balance: big.NewInt(1000000000000000000)}}, qrlconfig.Defaults.Miner.GasCeil)
			)
			defer sim.Close()

			_, tx, _, err := DeployConstructorWithStructParam(user, sim, ConstructorWithStructParamStructType{Field: big.NewInt(42)})
			if err != nil {
				t.Fatalf("DeployConstructorWithStructParam() got err %v; want nil err", err)
			}
			sim.Commit()

			if _, err = bind.WaitDeployed(nil, sim, tx); err != nil {
				t.Logf("Deployment tx: %+v", tx)
				t.Errorf("bind.WaitDeployed(nil, %T, <deployment tx>) got err %v; want nil err", sim, err)
			}
		`,
	},
	{
		name: `NameConflict`,
		contract: `
		contract oracle {
			struct request {
					bytes data;
					bytes _data;
			}
			event log (int msg, int _msg);
			function addRequest(request memory req) public pure {}
			function getRequest() pure public returns (request memory) {
					return request("", "");
			}
		}
		`,
		bytecode: []string{qrvmNoopBytecode},
		abi:      []string{`[{"anonymous":false,"inputs":[{"indexed":false,"internalType":"int256","name":"msg","type":"int256"},{"indexed":false,"internalType":"int256","name":"_msg","type":"int256"}],"name":"log","type":"event"},{"inputs":[{"components":[{"internalType":"bytes","name":"data","type":"bytes"},{"internalType":"bytes","name":"_data","type":"bytes"}],"internalType":"struct oracle.request","name":"req","type":"tuple"}],"name":"addRequest","outputs":[],"stateMutability":"pure","type":"function"},{"inputs":[],"name":"getRequest","outputs":[{"components":[{"internalType":"bytes","name":"data","type":"bytes"},{"internalType":"bytes","name":"_data","type":"bytes"}],"internalType":"struct oracle.request","name":"","type":"tuple"}],"stateMutability":"pure","type":"function"}]`},
		imports: `
			"math/big"
			"reflect"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
			"github.com/theQRL/go-qrl/qrl/qrlconfig"
		`,
		tester: `
			var (
				wallet, _  = wallet.Generate(wallet.ML_DSA_87)
				user, _ = bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
				sim     = backends.NewSimulatedBackend(core.GenesisAlloc{user.From: {Balance: big.NewInt(1000000000000000000)}}, qrlconfig.Defaults.Miner.GasCeil)
			)
			defer sim.Close()

			_, tx, _, err := DeployNameConflict(user, sim)
			if err != nil {
				t.Fatalf("DeployNameConflict() got err %v; want nil err", err)
			}
			sim.Commit()

			if _, err = bind.WaitDeployed(nil, sim, tx); err != nil {
				t.Logf("Deployment tx: %+v", tx)
				t.Errorf("bind.WaitDeployed(nil, %T, <deployment tx>) got err %v; want nil err", sim, err)
			}

			eventType := reflect.TypeOf(NameConflictLog{})
			if tag := eventType.Field(0).Tag.Get("abi"); tag != "msg" {
				t.Fatalf("NameConflictLog.Msg abi tag = %q, want msg", tag)
			}
			if tag := eventType.Field(1).Tag.Get("abi"); tag != "_msg" {
				t.Fatalf("NameConflictLog.Msg0 abi tag = %q, want _msg", tag)
			}
		`,
	},
	{
		name: "RangeKeyword",
		contract: `
		contract keywordcontract {
			function functionWithKeywordParameter(uint256 range) public pure {}
		}
		`,
		bytecode: []string{qrvmNoopBytecode},
		abi:      []string{`[{"inputs":[{"internalType":"uint256","name":"range","type":"uint256"}],"name":"functionWithKeywordParameter","outputs":[],"stateMutability":"pure","type":"function"}]`},
		imports: `
			"math/big"

			"github.com/theQRL/go-qrl/accounts/abi/bind"
			"github.com/theQRL/go-qrl/accounts/abi/bind/backends"
			"github.com/theQRL/go-qrl/core"
			"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
			"github.com/theQRL/go-qrl/qrl/qrlconfig"
		`,
		tester: `
			var (
				wallet, _  = wallet.Generate(wallet.ML_DSA_87)
				user, _ = bind.NewKeyedTransactorWithChainID(wallet, big.NewInt(1337))
				sim     = backends.NewSimulatedBackend(core.GenesisAlloc{user.From: {Balance: big.NewInt(1000000000000000000)}}, qrlconfig.Defaults.Miner.GasCeil)
			)
			_, tx, _, err := DeployRangeKeyword(user, sim)
			if err != nil {
				t.Fatalf("error deploying contract: %v", err)
			}
			sim.Commit()

			if _, err = bind.WaitDeployed(nil, sim, tx); err != nil {
				t.Errorf("error deploying the contract: %v", err)
			}
		`,
	},
	{
		name: "NumericMethodName",
		contract: `
		contract NumericMethodName {
			event _1TestEvent(address _param);
			function _1test() public pure {}
			function __1test() public pure {}
			function __2test() public pure {}
		}
		`,
		bytecode: []string{qrvmNoopBytecode},
		abi:      []string{`[{"anonymous":false,"inputs":[{"indexed":false,"internalType":"address","name":"_param","type":"address"}],"name":"_1TestEvent","type":"event"},{"inputs":[],"name":"_1test","outputs":[],"stateMutability":"pure","type":"function"},{"inputs":[],"name":"__1test","outputs":[],"stateMutability":"pure","type":"function"},{"inputs":[],"name":"__2test","outputs":[],"stateMutability":"pure","type":"function"}]`},
		imports: `
			"github.com/theQRL/go-qrl/common"
		`,
		tester: `
			if b, err := NewNumericMethodName(common.Address{}, nil); b == nil || err != nil {
				t.Fatalf("combined binding (%v) nil or error (%v) not nil", b, nil)
			}
			`,
	},
	{
		name:     "ArrayTopics",
		bytecode: []string{qrvmNoopBytecode},
		abi:      []string{`[{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256[]","name":"nums","type":"uint256[]"},{"indexed":true,"internalType":"address[2]","name":"addrs","type":"address[2]"},{"components":[{"internalType":"uint256","name":"id","type":"uint256"},{"internalType":"address","name":"owner","type":"address"}],"indexed":true,"internalType":"struct ArrayTopics.Meta","name":"meta","type":"tuple"},{"indexed":true,"internalType":"string","name":"name","type":"string"},{"indexed":true,"internalType":"bytes","name":"data","type":"bytes"}],"name":"Indexed","type":"event"}]`},
		imports: `
				"github.com/theQRL/go-qrl/accounts/abi/bind"
				"github.com/theQRL/go-qrl/common"
			`,
		tester: `
				if b, err := NewArrayTopics(common.Address{}, nil); b == nil || err != nil {
					t.Fatalf("binding (%v) nil or error (%v) not nil", b, nil)
				} else if false { // Don't run, just compile and test types.
						var err error
						var sink chan<- *ArrayTopicsIndexed
						_, err = b.FilterIndexed(&bind.FilterOpts{}, []common.LogTopic{}, []common.LogTopic{}, []common.LogTopic{}, []string{}, [][]byte{})
						_, err = b.WatchIndexed(&bind.WatchOpts{}, sink, []common.LogTopic{}, []common.LogTopic{}, []common.LogTopic{}, []string{}, [][]byte{})
						_ = err
					}
				`,
	},
}

func TestBindRejectsUnsupportedFunctionType(t *testing.T) {
	tests := []struct {
		name string
		abi  string
	}{
		{
			name: "method input",
			abi:  `[{"inputs":[{"internalType":"function (uint256) external","name":"callback","type":"function"}],"name":"test","outputs":[],"stateMutability":"nonpayable","type":"function"}]`,
		},
		{
			name: "method output",
			abi:  `[{"inputs":[],"name":"test","outputs":[{"internalType":"function (uint256) external","name":"callback","type":"function"}],"stateMutability":"view","type":"function"}]`,
		},
		{
			name: "constructor input",
			abi:  `[{"inputs":[{"internalType":"function (uint256) external","name":"callback","type":"function"}],"stateMutability":"nonpayable","type":"constructor"}]`,
		},
		{
			name: "event input",
			abi:  `[{"anonymous":false,"inputs":[{"indexed":true,"internalType":"function (uint256) external","name":"callback","type":"function"}],"name":"Callback","type":"event"}]`,
		},
		{
			name: "error input",
			abi:  `[{"inputs":[{"internalType":"function (uint256) external","name":"callback","type":"function"}],"name":"CallbackError","type":"error"}]`,
		},
		{
			name: "nested array input",
			abi:  `[{"inputs":[{"internalType":"function (uint256) external[]","name":"callbacks","type":"function[]"}],"name":"test","outputs":[],"stateMutability":"nonpayable","type":"function"}]`,
		},
		{
			name: "nested tuple input",
			abi:  `[{"inputs":[{"components":[{"internalType":"function (uint256) external","name":"callback","type":"function"}],"internalType":"struct Callback","name":"callback","type":"tuple"}],"name":"test","outputs":[],"stateMutability":"nonpayable","type":"function"}]`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Bind([]string{"FunctionType"}, []string{tt.abi}, []string{"0x"}, nil, "bindtest", nil, nil)
			if !errors.Is(err, abi.ErrUnsupportedFunctionType) {
				t.Fatalf("Bind error = %v, want %v", err, abi.ErrUnsupportedFunctionType)
			}
		})
	}
}

func TestBindTopicTypeUsesLogTopicForIndexedDynamicTypes(t *testing.T) {
	tests := []struct {
		name       string
		typ        string
		components []abi.ArgumentMarshaling
	}{
		{name: "string", typ: "string"},
		{name: "bytes", typ: "bytes"},
		{name: "string[]", typ: "string[]"},
		{name: "address[2]", typ: "address[2]"},
		{
			name: "tuple",
			typ:  "tuple",
			components: []abi.ArgumentMarshaling{
				{Name: "value", Type: "uint256"},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, err := abi.NewType(tt.typ, "", tt.components)
			if err != nil {
				t.Fatalf("NewType(%q): %v", tt.typ, err)
			}
			if got := bindTopicType(kind, nil); got != "common.LogTopic" {
				t.Fatalf("bindTopicType(%q) = %q, want common.LogTopic", tt.name, got)
			}
		})
	}
}

func TestBindTopicRuleTypeUsesLogTopicForIndexedArrays(t *testing.T) {
	tests := []struct {
		name       string
		typ        string
		components []abi.ArgumentMarshaling
		want       string
	}{
		{name: "string", typ: "string", want: "string"},
		{name: "bytes", typ: "bytes", want: "[]byte"},
		{name: "uint256[]", typ: "uint256[]", want: "common.LogTopic"},
		{name: "address[2]", typ: "address[2]", want: "common.LogTopic"},
		{
			name: "tuple",
			typ:  "tuple",
			components: []abi.ArgumentMarshaling{
				{Name: "value", Type: "uint256"},
			},
			want: "common.LogTopic",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			kind, err := abi.NewType(tt.typ, "", tt.components)
			if err != nil {
				t.Fatalf("NewType(%q): %v", tt.typ, err)
			}
			if got := bindTopicRuleType(kind, nil); got != tt.want {
				t.Fatalf("bindTopicRuleType(%q) = %q, want %q", tt.typ, got, tt.want)
			}
		})
	}
}

func TestBindIndexedCompositeFilterRulesUseLogTopic(t *testing.T) {
	code, err := Bind(
		[]string{"ArrayTopics"},
		[]string{`[{"anonymous":false,"inputs":[{"indexed":true,"internalType":"uint256[]","name":"nums","type":"uint256[]"},{"indexed":true,"internalType":"address[2]","name":"addrs","type":"address[2]"},{"components":[{"internalType":"uint256","name":"id","type":"uint256"},{"internalType":"address","name":"owner","type":"address"}],"indexed":true,"internalType":"struct ArrayTopics.Meta","name":"meta","type":"tuple"},{"indexed":true,"internalType":"string","name":"name","type":"string"},{"indexed":true,"internalType":"bytes","name":"data","type":"bytes"}],"name":"Indexed","type":"event"}]`},
		[]string{qrvmNoopBytecode},
		nil,
		"bindtest",
		nil,
		nil,
	)
	if err != nil {
		t.Fatalf("failed to generate binding: %v", err)
	}
	for _, want := range []string{
		`(?m)\bNums\s+common\.LogTopic\b`,
		`(?m)\bAddrs\s+common\.LogTopic\b`,
		`(?m)\bMeta\s+common\.LogTopic\b`,
		`(?m)\bName\s+common\.LogTopic\b`,
		`(?m)\bData\s+common\.LogTopic\b`,
		`FilterIndexed\(opts \*bind\.FilterOpts, nums \[\]common\.LogTopic, addrs \[\]common\.LogTopic, meta \[\]common\.LogTopic, name \[\]string, data \[\]\[\]byte\)`,
		`WatchIndexed\(opts \*bind\.WatchOpts, sink chan<- \*ArrayTopicsIndexed, nums \[\]common\.LogTopic, addrs \[\]common\.LogTopic, meta \[\]common\.LogTopic, name \[\]string, data \[\]\[\]byte\)`,
	} {
		if !regexp.MustCompile(want).MatchString(code) {
			t.Fatalf("generated binding missing pattern %q", want)
		}
	}
}

func TestBindAddressWidthLibraryPlaceholder(t *testing.T) {
	pattern := strings.Repeat("a", 2*common.AddressLength-len("__$")-len("$__"))
	code, err := Bind(
		[]string{"UseLibrary", "Math"},
		[]string{"[]", "[]"},
		[]string{"9f__$" + pattern + "$__00", qrvmNoopBytecode},
		nil,
		"bindtest",
		map[string]string{pattern: "Math"},
		nil,
	)
	if err != nil {
		t.Fatalf("failed to generate binding: %v", err)
	}
	if !strings.Contains(code, `strings.ReplaceAll(UseLibraryBin, "__$`+pattern+`$__", mathAddr.String()[1:])`) {
		t.Fatalf("generated binding does not replace address-width library placeholder")
	}
}

func TestUseLibraryFixtureUsesAddressWidthPlaceholder(t *testing.T) {
	if len(useLibraryMathPlaceholder) != 2*common.AddressLength-len("__$")-len("$__") {
		t.Fatalf("unexpected Math placeholder length: have %d", len(useLibraryMathPlaceholder))
	}
	for _, tt := range bindTests {
		if tt.name != "UseLibrary" {
			continue
		}
		if tt.libs[useLibraryMathPlaceholder] != "Math" {
			t.Fatalf("UseLibrary fixture does not map address-width Math placeholder")
		}
		if !strings.Contains(tt.bytecode[0], "9f__$"+useLibraryMathPlaceholder+"$__") {
			t.Fatalf("UseLibrary bytecode does not use PUSH64 address-width Math placeholder")
		}
		if strings.Contains(tt.bytecode[0], "5f73__$") {
			t.Fatalf("UseLibrary bytecode still contains legacy PUSH20 library placeholder shape")
		}
		return
	}
	t.Fatalf("UseLibrary fixture not found")
}

// Tests that packages generated by the binder can be successfully compiled and
// the requested tester run against it.
func TestGolangBindings(t *testing.T) {
	// Skip the test if no Go command can be found
	gocmd := runtime.GOROOT() + "/bin/go"
	if !common.FileExist(gocmd) {
		t.Skip("go sdk not found for testing")
	}
	// Create a temporary workspace for the test suite
	ws := t.TempDir()

	pkg := filepath.Join(ws, "bindtest")
	if err := os.MkdirAll(pkg, 0700); err != nil {
		t.Fatalf("failed to create package: %v", err)
	}
	// Generate the test suite for all the contracts
	for i, tt := range bindTests {
		t.Run(tt.name, func(t *testing.T) {
			var types []string
			if tt.types != nil {
				types = tt.types
			} else {
				types = []string{tt.name}
			}
			// Generate the binding and create a Go source file in the workspace
			bind, err := Bind(types, tt.abi, tt.bytecode, tt.fsigs, "bindtest", tt.libs, tt.aliases)
			if err != nil {
				t.Fatalf("test %d: failed to generate binding: %v", i, err)
			}
			if err = os.WriteFile(filepath.Join(pkg, strings.ToLower(tt.name)+".go"), []byte(bind), 0600); err != nil {
				t.Fatalf("test %d: failed to write binding: %v", i, err)
			}
			// Generate the test file with the injected test code
			code := fmt.Sprintf(`
			package bindtest

			import (
				"testing"
				%s
			)

			func Test%s(t *testing.T) {
				%s
			}
		`, tt.imports, tt.name, tt.tester)
			if err := os.WriteFile(filepath.Join(pkg, strings.ToLower(tt.name)+"_test.go"), []byte(code), 0600); err != nil {
				t.Fatalf("test %d: failed to write tests: %v", i, err)
			}
		})
	}
	// Convert the package to go modules and use the current source for go-ethereum
	moder := exec.Command(gocmd, "mod", "init", "bindtest")
	moder.Dir = pkg
	if out, err := moder.CombinedOutput(); err != nil {
		t.Fatalf("failed to convert binding test to modules: %v\n%s", err, out)
	}
	pwd, _ := os.Getwd()
	replacer := exec.Command(gocmd, "mod", "edit", "-x", "-require", "github.com/theQRL/go-qrl@v0.0.0", "-replace", "github.com/theQRL/go-qrl="+filepath.Join(pwd, "..", "..", "..")) // Repo root
	replacer.Dir = pkg
	if out, err := replacer.CombinedOutput(); err != nil {
		t.Fatalf("failed to replace binding test dependency to current source tree: %v\n%s", err, out)
	}
	tidier := exec.Command(gocmd, "mod", "tidy")
	tidier.Dir = pkg
	if out, err := tidier.CombinedOutput(); err != nil {
		t.Fatalf("failed to tidy Go module file: %v\n%s", err, out)
	}
	// Verify the generated bindings compile cleanly (go vet performs a
	// full type-check + compilation without running anything). The bytecode
	// fixtures are minimal QRVM-safe inputs because this test proves the
	// binder still emits valid Go against the current types (common.Address
	// etc.), not that old Solidity runtime blobs still execute.
	cmd := exec.Command(gocmd, "vet", "./...")
	cmd.Dir = pkg
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("failed to vet generated bindings: %v\n%s", err, out)
	}
}
