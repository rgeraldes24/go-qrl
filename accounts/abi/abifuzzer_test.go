// Copyright 2020 The go-ethereum Authors
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
	"fmt"
	"reflect"
	"strings"
	"testing"

	fuzz "github.com/google/gofuzz"
)

// TestReplicate can be used to replicate crashers from the fuzzing tests.
// Just replace testString with the data in .quoted
func TestReplicate(t *testing.T) {
	t.Parallel()
	//t.Skip("Test only useful for reproducing issues")
	fuzzAbi([]byte("\x20\x20\x20\x20\x20\x20\x20\x20\x80\x00\x00\x00\x20\x20\x20\x20\x00"))
	//fuzzAbi([]byte("asdfasdfkadsf;lasdf;lasd;lfk"))
}

// FuzzABI is the main entrypoint for fuzzing
func FuzzABI(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte) {
		fuzzAbi(data)
	})
}

var (
	names    = []string{"_name", "name", "NAME", "name_", "__", "_name_", "n"}
	stateMut = []string{"pure", "view", "payable"}
	pays     = []string{"true", "false"}
	vNames   = []string{"a", "b", "c", "d", "e", "f", "g"}
	varNames = append(vNames, names...)
	varTypes = buildFuzzerTypes()
)

func buildFuzzerTypes() []string {
	types := []string{"bool", "address", "bytes", "string"}
	for bits := 8; bits <= abiSlotBits; bits += 8 {
		types = append(types, fmt.Sprintf("uint%d", bits), fmt.Sprintf("int%d", bits))
	}
	for size := 1; size <= abiSlotBytes; size++ {
		types = append(types, fmt.Sprintf("bytes%d", size))
	}
	return types
}

func TestABIFuzzerTypeUniverseCoversVM64(t *testing.T) {
	t.Parallel()

	have := make(map[string]bool, len(varTypes))
	for _, typ := range varTypes {
		have[typ] = true
	}
	for _, typ := range []string{"uint264", "int264", "uint504", "int504", "uint512", "int512", "bytes64"} {
		if !have[typ] {
			t.Fatalf("fuzzer type universe missing %s", typ)
		}
	}
}

func unpackPack(abi ABI, method string, input []byte) ([]any, bool) {
	if out, err := abi.Unpack(method, input); err == nil {
		_, err := abi.Pack(method, out...)
		if err != nil {
			// We have some false positives as we can unpack these type successfully, but not pack them
			if err.Error() == "abi: cannot use []uint8 as type [0]int8 as argument" ||
				err.Error() == "abi: cannot use uint8 as type int8 as argument" {
				return out, false
			}
			panic(err)
		}
		return out, true
	}
	return nil, false
}

func packUnpack(abi ABI, method string, input *[]any) bool {
	if packed, err := abi.Pack(method, input); err == nil {
		outptr := reflect.New(reflect.TypeFor[*[]any]())
		err := abi.UnpackIntoInterface(outptr.Interface(), method, packed)
		if err != nil {
			panic(err)
		}
		out := outptr.Elem().Interface()
		if !reflect.DeepEqual(input, out) {
			panic(fmt.Sprintf("unpackPack is not equal, \ninput : %x\noutput: %x", input, out))
		}
		return true
	}
	return false
}

type arg struct {
	name string
	typ  string
}

func createABI(name string, stateMutability, payable *string, inputs []arg) (ABI, error) {
	var sig strings.Builder
	sig.WriteString(fmt.Sprintf(`[{ "type" : "function", "name" : "%v" `, name))
	if stateMutability != nil {
		sig.WriteString(fmt.Sprintf(`, "stateMutability": "%v" `, *stateMutability))
	}
	if payable != nil {
		sig.WriteString(fmt.Sprintf(`, "payable": %v `, *payable))
	}
	if len(inputs) > 0 {
		sig.WriteString(`, "inputs" : [ {`)
		for i, inp := range inputs {
			sig.WriteString(fmt.Sprintf(`"name" : "%v", "type" : "%v" `, inp.name, inp.typ))
			if i+1 < len(inputs) {
				sig.WriteString(",")
			}
		}
		sig.WriteString("} ]")
		sig.WriteString(`, "outputs" : [ {`)
		for i, inp := range inputs {
			sig.WriteString(fmt.Sprintf(`"name" : "%v", "type" : "%v" `, inp.name, inp.typ))
			if i+1 < len(inputs) {
				sig.WriteString(",")
			}
		}
		sig.WriteString("} ]")
	}
	sig.WriteString(`}]`)
	//fmt.Printf("sig: %s\n", sig)
	return JSON(strings.NewReader(sig.String()))
}

func fuzzAbi(input []byte) {
	var (
		fuzzer    = fuzz.NewFromGoFuzz(input)
		name      = oneOf(fuzzer, names)
		stateM    = oneOfOrNil(fuzzer, stateMut)
		payable   = oneOfOrNil(fuzzer, pays)
		arguments []arg
	)
	for i := 0; i < upTo(fuzzer, 10); i++ {
		argName := oneOf(fuzzer, varNames)
		argTyp := oneOf(fuzzer, varTypes)
		switch upTo(fuzzer, 10) {
		case 0: // 10% chance to make it a slice
			argTyp += "[]"
		case 1: // 10% chance to make it an array
			argTyp += fmt.Sprintf("[%d]", 1+upTo(fuzzer, 30))
		default:
		}
		arguments = append(arguments, arg{name: argName, typ: argTyp})
	}
	abi, err := createABI(name, stateM, payable, arguments)
	if err != nil {
		//fmt.Printf("err: %v\n", err)
		panic(err)
	}
	structs, _ := unpackPack(abi, name, input)
	_ = packUnpack(abi, name, &structs)
}

func upTo(fuzzer *fuzz.Fuzzer, max int) int {
	var i int
	fuzzer.Fuzz(&i)
	if i < 0 {
		return (-1 - i) % max
	}
	return i % max
}

func oneOf(fuzzer *fuzz.Fuzzer, options []string) string {
	return options[upTo(fuzzer, len(options))]
}

func oneOfOrNil(fuzzer *fuzz.Fuzzer, options []string) *string {
	if i := upTo(fuzzer, len(options)+1); i < len(options) {
		return &options[i]
	}
	return nil
}
