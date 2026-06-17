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
	"errors"
	gomath "math"
	"math/big"
	"reflect"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/crypto"
)

func TestMakeTopics(t *testing.T) {
	t.Parallel()

	// intTopic returns the 64-byte two's-complement encoding of v for signed
	// integers. Negative numbers get 0xff sign extension; positive numbers
	// are zero-padded in the upper bytes.
	intTopic := func(v int64) common.LogTopic {
		var t common.LogTopic
		bi := new(big.Int).SetInt64(v)
		data := math.U512Bytes(bi)
		copy(t[:], data)
		return t
	}
	uintTopic := func(v uint64) common.LogTopic {
		var t common.LogTopic
		bi := new(big.Int).SetUint64(v)
		blob := bi.Bytes()
		copy(t[common.LogTopicLength-len(blob):], blob)
		return t
	}

	type args struct {
		query [][]any
	}
	tests := []struct {
		name    string
		args    args
		want    [][]common.LogTopic
		wantErr bool
	}{
		{
			"support fixed byte types, left-packed into the 64-byte topic",
			args{[][]any{{[5]byte{1, 2, 3, 4, 5}}}},
			[][]common.LogTopic{{common.LogTopic{1, 2, 3, 4, 5}}},
			false,
		},
		{
			"support common hash types in topics",
			args{[][]any{{common.Hash{1, 2, 3, 4, 5}}}},
			// Hash right-aligned in the 64-byte slot: low 32 bytes hold the
			// hash, upper 32 are zero.
			[][]common.LogTopic{{func() common.LogTopic {
				var t common.LogTopic
				h := common.Hash{1, 2, 3, 4, 5}
				copy(t[common.LogTopicLength-common.HashLength:], h[:])
				return t
			}()}},
			false,
		},
		{
			"support address types in topics",
			args{[][]any{{common.Address{1, 2, 3, 4, 5}}}},
			// Address right-aligned in the 64-byte slot.
			[][]common.LogTopic{{func() common.LogTopic {
				var t common.LogTopic
				addr := common.Address{1, 2, 3, 4, 5}
				copy(t[common.LogTopicLength-common.AddressLength:], addr[:])
				return t
			}()}},
			false,
		},
		{
			"support positive *big.Int types in topics",
			args{[][]any{
				{big.NewInt(1)},
				{new(big.Int).Lsh(big.NewInt(2), 254)},
				{new(big.Int).Lsh(big.NewInt(1), 256)},
			}},
			[][]common.LogTopic{
				{uintTopic(1)},
				// 2 << 254 is 2^255, which sits in the high bit of the low
				// 32 bytes of the slot.
				{func() common.LogTopic {
					var t common.LogTopic
					t[common.LogTopicLength-common.HashLength] = 0x80
					return t
				}()},
				// 2^256 must remain representable in a 64-byte topic instead
				// of being reduced to zero modulo 2^256.
				{func() common.LogTopic {
					var t common.LogTopic
					t[common.LogTopicLength-common.HashLength-1] = 0x01
					return t
				}()},
			},
			false,
		},
		{
			"support negative *big.Int types in topics",
			args{[][]any{
				{big.NewInt(-1)},
				{big.NewInt(gomath.MinInt64)},
			}},
			[][]common.LogTopic{
				{intTopic(-1)},
				{intTopic(gomath.MinInt64)},
			},
			false,
		},
		{
			"support boolean types in topics",
			args{[][]any{
				{true},
				{false},
			}},
			[][]common.LogTopic{
				{uintTopic(1)},
				{common.LogTopic{}},
			},
			false,
		},
		{
			"support int/uint(8/16/32/64) types in topics",
			args{[][]any{
				{int8(-2)},
				{int16(-3)},
				{int32(-4)},
				{int64(-5)},
				{int8(1)},
				{int16(256)},
				{int32(65536)},
				{int64(4294967296)},
				{uint8(1)},
				{uint16(256)},
				{uint32(65536)},
				{uint64(4294967296)},
			}},
			[][]common.LogTopic{
				{intTopic(-2)},
				{intTopic(-3)},
				{intTopic(-4)},
				{intTopic(-5)},
				{intTopic(1)},
				{intTopic(256)},
				{intTopic(65536)},
				{intTopic(4294967296)},
				{uintTopic(1)},
				{uintTopic(256)},
				{uintTopic(65536)},
				{uintTopic(4294967296)},
			},
			false,
		},
		{
			"support string types in topics",
			args{[][]any{{"hello world"}}},
			// Keccak256 hash right-aligned in the slot (low 32 bytes).
			[][]common.LogTopic{{common.BytesToLogTopic(crypto.Keccak256([]byte("hello world")))}},
			false,
		},
		{
			"support byte slice types in topics",
			args{[][]any{{[]byte{1, 2, 3}}}},
			[][]common.LogTopic{{common.BytesToLogTopic(crypto.Keccak256([]byte{1, 2, 3}))}},
			false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := MakeTopics(tt.args.query...)
			if (err != nil) != tt.wantErr {
				t.Errorf("makeTopics() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("makeTopics() = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("does not mutate big.Int", func(t *testing.T) {
		t.Parallel()
		want := [][]common.LogTopic{{intTopic(-1)}}

		in := big.NewInt(-1)
		got, err := MakeTopics([]any{in})
		if err != nil {
			t.Fatalf("makeTopics() error = %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("makeTopics() = %v, want %v", got, want)
		}
		if orig := big.NewInt(-1); in.Cmp(orig) != 0 {
			t.Fatalf("makeTopics() mutated an input parameter from %v to %v", orig, in)
		}
	})
}

func TestMakeTopicsRejectsUnsupportedFunctionValue(t *testing.T) {
	t.Parallel()

	var value [common.AddressLength + 4]byte
	_, err := MakeTopics([]any{value})
	if !errors.Is(err, ErrUnsupportedFunctionType) {
		t.Fatalf("MakeTopics function value error = %v, want %v", err, ErrUnsupportedFunctionType)
	}
}

func TestMakeTopicsRejectsOversizedFixedByteArray(t *testing.T) {
	t.Parallel()

	var value [common.LogTopicLength + 1]byte
	_, err := MakeTopics([]any{value})
	if err == nil {
		t.Fatal("expected oversized fixed byte array topic to be rejected")
	}
}

func TestEventSignatureTopicMatchesHashTopicConstruction(t *testing.T) {
	t.Parallel()

	eventID := crypto.Keccak256Hash([]byte("Called()"))
	topics, err := MakeTopics([]any{eventID})
	if err != nil {
		t.Fatal(err)
	}
	if len(topics) != 1 || len(topics[0]) != 1 {
		t.Fatalf("unexpected topic shape: %v", topics)
	}
	if got, want := topics[0][0], common.BytesToEventSignatureLogTopic(eventID.Bytes()); got != want {
		t.Fatalf("event signature topic mismatch: got %x want %x", got, want)
	}
}

func TestMakeTopicsWithTypesEnforcesIntegerWidths(t *testing.T) {
	t.Parallel()

	uint256Ty, _ := NewType("uint256", "", nil)
	int256Ty, _ := NewType("int256", "", nil)
	uint512Ty, _ := NewType("uint512", "", nil)

	topicFromBig := func(n *big.Int) common.LogTopic {
		var topic common.LogTopic
		blob := math.U512Bytes(new(big.Int).Set(n))
		copy(topic[:], blob)
		return topic
	}

	maxUint256 := new(big.Int).Sub(twoPow(256), common.Big1)
	topics, err := MakeTopicsWithTypes(Arguments{{Type: uint256Ty, Indexed: true}}, []any{maxUint256})
	if err != nil {
		t.Fatalf("MakeTopicsWithTypes uint256 max: %v", err)
	}
	if got, want := topics[0][0], topicFromBig(maxUint256); got != want {
		t.Fatalf("uint256 max topic mismatch: got %x want %x", got, want)
	}
	if _, err := MakeTopicsWithTypes(Arguments{{Type: uint256Ty, Indexed: true}}, []any{twoPow(256)}); err == nil {
		t.Fatalf("MakeTopicsWithTypes accepted uint256 overflow")
	}
	if _, err := MakeTopicsWithTypes(Arguments{{Type: uint256Ty, Indexed: true}}, []any{big.NewInt(-1)}); err == nil {
		t.Fatalf("MakeTopicsWithTypes accepted negative uint256")
	}

	maxInt256 := new(big.Int).Sub(twoPow(255), common.Big1)
	minInt256 := new(big.Int).Neg(twoPow(255))
	if _, err := MakeTopicsWithTypes(Arguments{{Type: int256Ty, Indexed: true}}, []any{maxInt256}); err != nil {
		t.Fatalf("MakeTopicsWithTypes int256 max: %v", err)
	}
	if _, err := MakeTopicsWithTypes(Arguments{{Type: int256Ty, Indexed: true}}, []any{minInt256}); err != nil {
		t.Fatalf("MakeTopicsWithTypes int256 min: %v", err)
	}
	if _, err := MakeTopicsWithTypes(Arguments{{Type: int256Ty, Indexed: true}}, []any{twoPow(255)}); err == nil {
		t.Fatalf("MakeTopicsWithTypes accepted int256 positive overflow")
	}
	if _, err := MakeTopicsWithTypes(Arguments{{Type: int256Ty, Indexed: true}}, []any{new(big.Int).Sub(minInt256, common.Big1)}); err == nil {
		t.Fatalf("MakeTopicsWithTypes accepted int256 negative overflow")
	}

	if _, err := MakeTopicsWithTypes(Arguments{{Type: uint512Ty, Indexed: true}}, []any{math.MaxBig512}); err != nil {
		t.Fatalf("MakeTopicsWithTypes uint512 max: %v", err)
	}
	if _, err := MakeTopicsWithTypes(Arguments{{Type: uint512Ty, Indexed: true}}, []any{twoPow(512)}); err == nil {
		t.Fatalf("MakeTopicsWithTypes accepted uint512 overflow")
	}
}

func TestMakeTopicsForEventUsesTypedIndexedFields(t *testing.T) {
	t.Parallel()

	uint256Ty, _ := NewType("uint256", "", nil)
	event := NewEvent("Observed", "Observed", false, Arguments{
		{Name: "value", Type: uint256Ty, Indexed: true},
	})

	topics, err := MakeTopicsForEvent(event, []any{big.NewInt(1)})
	if err != nil {
		t.Fatalf("MakeTopicsForEvent valid uint256: %v", err)
	}
	if len(topics) != 2 || len(topics[0]) != 1 || len(topics[1]) != 1 {
		t.Fatalf("unexpected topic shape: %v", topics)
	}
	if got, want := topics[0][0], event.Topic(); got != want {
		t.Fatalf("event selector topic mismatch: got %x want %x", got, want)
	}

	if _, err := MakeTopicsForEvent(event, []any{twoPow(256)}); err == nil {
		t.Fatalf("MakeTopicsForEvent accepted uint256 overflow")
	}
}

func TestMakeTopicsForEventRequiresRawTopicsForHashOnlyIndexedTypes(t *testing.T) {
	t.Parallel()

	stringTy, _ := NewType("string", "", nil)
	bytesTy, _ := NewType("bytes", "", nil)
	sliceTy, _ := NewType("address[]", "", nil)
	arrayTy, _ := NewType("address[2]", "", nil)
	tupleTy, err := NewType("tuple", "struct Observed.Point", []ArgumentMarshaling{
		{Name: "x", Type: "uint256"},
		{Name: "y", Type: "uint256"},
	})
	if err != nil {
		t.Fatal(err)
	}

	rawTopic := common.LogTopic{0xab}
	tests := []struct {
		name string
		typ  Type
		bad  any
	}{
		{"string", stringTy, "hello"},
		{"bytes", bytesTy, []byte{1, 2, 3}},
		{"slice", sliceTy, []common.Address{{}}},
		{"array", arrayTy, [2]common.Address{}},
		{"tuple", tupleTy, struct{ X, Y *big.Int }{big.NewInt(1), big.NewInt(2)}},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := NewEvent("Observed", "Observed", false, Arguments{
				{Name: "value", Type: tt.typ, Indexed: true},
			})
			topics, err := MakeTopicsForEvent(event, []any{rawTopic})
			if err != nil {
				t.Fatalf("MakeTopicsForEvent raw topic: %v", err)
			}
			if got := topics[1][0]; got != rawTopic {
				t.Fatalf("raw topic mismatch: got %x want %x", got, rawTopic)
			}
			if _, err := MakeTopicsForEvent(event, []any{tt.bad}); err == nil || !strings.Contains(err.Error(), "require common.LogTopic") {
				t.Fatalf("MakeTopicsForEvent hash-only value error = %v, want common.LogTopic requirement", err)
			}
		})
	}
}

type args struct {
	createObj func() any
	resultObj func() any
	resultMap func() map[string]any
	fields    Arguments
	topics    []common.LogTopic
}

type bytesStruct struct {
	StaticBytes [5]byte
}
type bytes64Struct struct {
	StaticBytes [64]byte
}
type int8Struct struct {
	Int8Value int8
}
type int256Struct struct {
	Int256Value *big.Int
}

// hashStruct receives the indexed keccak256 hash of a dynamic type. Topics
// are 64 bytes; the reconstructed value uses the full LogTopic slot (hash
// right-aligned in the low 32 bytes).
type hashStruct struct {
	HashValue common.LogTopic
}

type tupleTopicStruct struct {
	TupleValue common.LogTopic
}

// funcStruct mirrors the Solidity `function` type, which is address followed
// by a 4-byte selector. With 64-byte addresses it no longer fits in one
// 64-byte ABI word and is rejected by encoder/decoder paths.
type funcStruct struct {
	FuncValue [common.AddressLength + 4]byte
}

type topicTest struct {
	name    string
	args    args
	wantErr bool
}

// allOnesTopic is the 64-byte topic whose every byte is 0xff — the canonical
// two's-complement encoding of -1 sign-extended across the whole slot.
var allOnesTopic = func() common.LogTopic {
	var t common.LogTopic
	for i := range t {
		t[i] = 0xff
	}
	return t
}()

func setupTopicsTests() []topicTest {
	bytesType, _ := NewType("bytes5", "", nil)
	bytes64Type, _ := NewType("bytes64", "", nil)
	int8Type, _ := NewType("int8", "", nil)
	int256Type, _ := NewType("int256", "", nil)
	tupleType, _ := NewType("tuple(int256,int8)", "", nil)
	stringType, _ := NewType("string", "", nil)
	funcType, _ := NewType("function", "", nil)

	tests := []topicTest{
		{
			name: "support fixed byte types, left-packed into the 64-byte topic",
			args: args{
				createObj: func() any { return &bytesStruct{} },
				resultObj: func() any { return &bytesStruct{StaticBytes: [5]byte{1, 2, 3, 4, 5}} },
				resultMap: func() map[string]any {
					return map[string]any{"staticBytes": [5]byte{1, 2, 3, 4, 5}}
				},
				fields: Arguments{Argument{
					Name:    "staticBytes",
					Type:    bytesType,
					Indexed: true,
				}},
				topics: []common.LogTopic{
					{1, 2, 3, 4, 5},
				},
			},
			wantErr: false,
		},
		{
			name: "support bytes64 fixed byte topics",
			args: args{
				createObj: func() any { return &bytes64Struct{} },
				resultObj: func() any {
					var value [64]byte
					for i := range value {
						value[i] = byte(i + 1)
					}
					return &bytes64Struct{StaticBytes: value}
				},
				resultMap: func() map[string]any {
					var value [64]byte
					for i := range value {
						value[i] = byte(i + 1)
					}
					return map[string]any{"staticBytes": value}
				},
				fields: Arguments{Argument{
					Name:    "staticBytes",
					Type:    bytes64Type,
					Indexed: true,
				}},
				topics: []common.LogTopic{
					func() common.LogTopic {
						var topic common.LogTopic
						for i := range topic {
							topic[i] = byte(i + 1)
						}
						return topic
					}(),
				},
			},
			wantErr: false,
		},
		{
			name: "int8 with negative value",
			args: args{
				createObj: func() any { return &int8Struct{} },
				resultObj: func() any { return &int8Struct{Int8Value: -1} },
				resultMap: func() map[string]any {
					return map[string]any{"int8Value": int8(-1)}
				},
				fields: Arguments{Argument{
					Name:    "int8Value",
					Type:    int8Type,
					Indexed: true,
				}},
				// Two's complement -1 sign-extended to the 64-byte ABI slot.
				topics: []common.LogTopic{allOnesTopic},
			},
			wantErr: false,
		},
		{
			name: "int256 with negative value",
			args: args{
				createObj: func() any { return &int256Struct{} },
				resultObj: func() any { return &int256Struct{Int256Value: big.NewInt(-1)} },
				resultMap: func() map[string]any {
					return map[string]any{"int256Value": big.NewInt(-1)}
				},
				fields: Arguments{Argument{
					Name:    "int256Value",
					Type:    int256Type,
					Indexed: true,
				}},
				topics: []common.LogTopic{allOnesTopic},
			},
			wantErr: false,
		},
		{
			name: "hash type",
			args: args{
				createObj: func() any { return &hashStruct{} },
				resultObj: func() any {
					return &hashStruct{common.BytesToLogTopic(crypto.Keccak256([]byte("stringtopic")))}
				},
				resultMap: func() map[string]any {
					return map[string]any{"hashValue": common.BytesToLogTopic(crypto.Keccak256([]byte("stringtopic")))}
				},
				fields: Arguments{Argument{
					Name:    "hashValue",
					Type:    stringType,
					Indexed: true,
				}},
				topics: []common.LogTopic{
					common.BytesToLogTopic(crypto.Keccak256([]byte("stringtopic"))),
				},
			},
			wantErr: false,
		},
		{
			name: "function type",
			args: args{
				createObj: func() any { return &funcStruct{} },
				resultObj: func() any { return &funcStruct{} },
				resultMap: func() map[string]any {
					return map[string]any{}
				},
				fields: Arguments{Argument{
					Name:    "funcValue",
					Type:    funcType,
					Indexed: true,
				}},
				// 64-byte addresses leave no room for the extra 4-byte selector,
				// so ABI function values are rejected.
				topics: []common.LogTopic{func() common.LogTopic {
					var t common.LogTopic
					for i := range t {
						t[i] = 0xff
					}
					return t
				}()},
			},
			wantErr: true,
		},
		{
			name: "error on topic/field count mismatch",
			args: args{
				createObj: func() any { return nil },
				resultObj: func() any { return nil },
				resultMap: func() map[string]any { return make(map[string]any) },
				fields: Arguments{Argument{
					Name:    "tupletype",
					Type:    tupleType,
					Indexed: true,
				}},
				topics: []common.LogTopic{},
			},
			wantErr: true,
		},
		{
			name: "error on unindexed arguments",
			args: args{
				createObj: func() any { return &int256Struct{} },
				resultObj: func() any { return &int256Struct{} },
				resultMap: func() map[string]any { return make(map[string]any) },
				fields: Arguments{Argument{
					Name:    "int256Value",
					Type:    int256Type,
					Indexed: false,
				}},
				topics: []common.LogTopic{allOnesTopic},
			},
			wantErr: true,
		},
		{
			name: "support tuple hash topics",
			args: args{
				createObj: func() any { return &tupleTopicStruct{} },
				resultObj: func() any { return &tupleTopicStruct{TupleValue: common.LogTopic{0x42}} },
				resultMap: func() map[string]any {
					return map[string]any{"tupleValue": common.LogTopic{0x42}}
				},
				fields: Arguments{Argument{
					Name:    "tupleValue",
					Type:    tupleType,
					Indexed: true,
				}},
				topics: []common.LogTopic{{0x42}},
			},
			wantErr: false,
		},
		{
			name: "error on improper encoded function",
			args: args{
				createObj: func() any { return &funcStruct{} },
				resultObj: func() any { return &funcStruct{} },
				resultMap: func() map[string]any {
					return make(map[string]any)
				},
				fields: Arguments{Argument{
					Name:    "funcValue",
					Type:    funcType,
					Indexed: true,
				}},
				// 64-byte addresses leave no room for the extra 4-byte selector,
				// so ABI function values are rejected.
				topics: []common.LogTopic{func() common.LogTopic {
					var t common.LogTopic
					for i := range t {
						t[i] = 0xff
					}
					return t
				}()},
			},
			wantErr: true,
		},
	}

	return tests
}

func TestParseTopics(t *testing.T) {
	t.Parallel()
	tests := setupTopicsTests()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			createObj := tt.args.createObj()
			if err := ParseTopics(createObj, tt.args.fields, tt.args.topics); (err != nil) != tt.wantErr {
				t.Errorf("parseTopics() error = %v, wantErr %v", err, tt.wantErr)
			}
			resultObj := tt.args.resultObj()
			if !reflect.DeepEqual(createObj, resultObj) {
				t.Errorf("parseTopics() = %v, want %v", createObj, resultObj)
			}
		})
	}
}

func TestParseTopicsRejectsUnsupportedFunctionType(t *testing.T) {
	t.Parallel()

	funcType, err := NewType("function", "", nil)
	if err != nil {
		t.Fatalf("build function type: %v", err)
	}
	funcArrayType, err := NewType("function[]", "", nil)
	if err != nil {
		t.Fatalf("build function array type: %v", err)
	}

	tests := []struct {
		name string
		typ  Type
		out  any
	}{
		{name: "direct", typ: funcType, out: &funcStruct{}},
		{name: "array", typ: funcArrayType, out: new(struct{})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := ParseTopics(tt.out, Arguments{{
				Name:    "funcValue",
				Type:    tt.typ,
				Indexed: true,
			}}, []common.LogTopic{{}})
			if !errors.Is(err, ErrUnsupportedFunctionType) {
				t.Fatalf("ParseTopics function type error = %v, want %v", err, ErrUnsupportedFunctionType)
			}
		})
	}
}

func TestParseTopicsIntoMap(t *testing.T) {
	t.Parallel()
	tests := setupTopicsTests()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			outMap := make(map[string]any)
			if err := ParseTopicsIntoMap(outMap, tt.args.fields, tt.args.topics); (err != nil) != tt.wantErr {
				t.Errorf("parseTopicsIntoMap() error = %v, wantErr %v", err, tt.wantErr)
			}
			resultMap := tt.args.resultMap()
			if !reflect.DeepEqual(outMap, resultMap) {
				t.Errorf("parseTopicsIntoMap() = %v, want %v", outMap, resultMap)
			}
		})
	}
}
