// Copyright 2018 The go-ethereum Authors
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
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/crypto"
)

// MakeTopics converts a raw filter query argument list into a filter topic set.
// Topics are common.LogTopicLength-byte values; scalar arguments are right-aligned (big-endian).
//
// MakeTopics does not know ABI-declared integer widths. Use MakeTopicsWithTypes
// or MakeTopicsForEvent when building topics for ABI-typed indexed arguments.
func MakeTopics(query ...[]any) ([][]common.LogTopic, error) {
	topics := make([][]common.LogTopic, len(query))
	for i, filter := range query {
		for _, rule := range filter {
			topic, err := makeTopic(rule)
			if err != nil {
				return nil, err
			}
			topics[i] = append(topics[i], topic)
		}
	}
	return topics, nil
}

// MakeTopicsWithTypes converts ABI-typed indexed argument filters into a topic
// set. Integer filters are checked against their declared ABI width before being
// encoded into a 64-byte topic.
func MakeTopicsWithTypes(fields Arguments, query ...[]any) ([][]common.LogTopic, error) {
	if len(query) > len(fields) {
		return nil, fmt.Errorf("abi: too many topic filters: have %d, want at most %d", len(query), len(fields))
	}
	topics := make([][]common.LogTopic, len(query))
	for i, filter := range query {
		for _, rule := range filter {
			topic, err := makeTopicWithType(fields[i].Type, rule)
			if err != nil {
				return nil, err
			}
			topics[i] = append(topics[i], topic)
		}
	}
	return topics, nil
}

// MakeTopicsForEvent converts ABI-typed indexed event filters into a topic set.
// Non-anonymous events include the event signature topic at position 0.
func MakeTopicsForEvent(event Event, query ...[]any) ([][]common.LogTopic, error) {
	var indexed Arguments
	for _, arg := range event.Inputs {
		if arg.Indexed {
			indexed = append(indexed, arg)
		}
	}
	topics, err := MakeTopicsWithTypes(indexed, query...)
	if err != nil {
		return nil, err
	}
	if event.Anonymous {
		return topics, nil
	}
	return append([][]common.LogTopic{{event.Topic()}}, topics...), nil
}

func makeTopic(rule any) (common.LogTopic, error) {
	var topic common.LogTopic

	// Try to generate the topic based on simple types
	switch rule := rule.(type) {
	case common.LogTopic:
		copy(topic[:], rule[:])
	case common.Hash:
		copy(topic[common.LogTopicLength-common.HashLength:], rule[:])
	case common.Address:
		copy(topic[common.LogTopicLength-common.AddressLength:], rule[:])
	case *big.Int:
		if rule == nil {
			return common.LogTopic{}, errors.New("abi: nil *big.Int topic")
		}
		blob := math.U512Bytes(new(big.Int).Set(rule))
		copy(topic[common.LogTopicLength-len(blob):], blob)
	case bool:
		if rule {
			topic[common.LogTopicLength-1] = 1
		}
	case int8:
		copy(topic[:], genIntType(int64(rule), 1))
	case int16:
		copy(topic[:], genIntType(int64(rule), 2))
	case int32:
		copy(topic[:], genIntType(int64(rule), 4))
	case int64:
		copy(topic[:], genIntType(rule, 8))
	case uint8:
		blob := new(big.Int).SetUint64(uint64(rule)).Bytes()
		copy(topic[common.LogTopicLength-len(blob):], blob)
	case uint16:
		blob := new(big.Int).SetUint64(uint64(rule)).Bytes()
		copy(topic[common.LogTopicLength-len(blob):], blob)
	case uint32:
		blob := new(big.Int).SetUint64(uint64(rule)).Bytes()
		copy(topic[common.LogTopicLength-len(blob):], blob)
	case uint64:
		blob := new(big.Int).SetUint64(rule).Bytes()
		copy(topic[common.LogTopicLength-len(blob):], blob)
	case string:
		hash := crypto.Keccak256Hash([]byte(rule))
		copy(topic[common.LogTopicLength-common.HashLength:], hash[:])
	case []byte:
		hash := crypto.Keccak256Hash(rule)
		copy(topic[common.LogTopicLength-common.HashLength:], hash[:])

	default:
		// todo(rjl493456442) according to hyperion documentation, indexed event
		// parameters that are not value types i.e. arrays and structs are not
		// stored directly but instead a keccak256-hash of an encoding is stored.
		//
		// We only convert stringS and bytes to hash, still need to deal with
		// array(both fixed-size and dynamic-size) and struct.

		// Attempt to generate the topic from funky types
		val := reflect.ValueOf(rule)
		switch {
		// static byte array
		case val.Kind() == reflect.Array && reflect.TypeOf(rule).Elem().Kind() == reflect.Uint8:
			if val.Len() == common.AddressLength+4 {
				return common.LogTopic{}, ErrUnsupportedFunctionType
			}
			if val.Len() > common.LogTopicLength {
				return common.LogTopic{}, fmt.Errorf("abi: fixed byte array of length %d does not fit in a %d-byte topic", val.Len(), common.LogTopicLength)
			}
			reflect.Copy(reflect.ValueOf(topic[:val.Len()]), val)
		default:
			return common.LogTopic{}, fmt.Errorf("unsupported indexed type: %T", rule)
		}
	}
	return topic, nil
}

func makeTopicWithType(t Type, rule any) (common.LogTopic, error) {
	if isHashOnlyIndexedType(t) {
		if topic, ok := rule.(common.LogTopic); ok {
			return topic, nil
		}
		return common.LogTopic{}, fmt.Errorf("abi: indexed %s filters require common.LogTopic, got %T", t, rule)
	}
	if t.T != IntTy && t.T != UintTy {
		return makeTopic(rule)
	}
	if topic, ok := rule.(common.LogTopic); ok {
		return topic, nil
	}
	n, ok := topicIntegerValue(rule)
	if !ok {
		return common.LogTopic{}, fmt.Errorf("abi: cannot use %T as type %s", rule, t)
	}
	if err := checkIntegerRange(t, n); err != nil {
		return common.LogTopic{}, err
	}
	var topic common.LogTopic
	blob := math.U512Bytes(n)
	copy(topic[common.LogTopicLength-len(blob):], blob)
	return topic, nil
}

func isHashOnlyIndexedType(t Type) bool {
	switch t.T {
	case StringTy, BytesTy, SliceTy, ArrayTy, TupleTy:
		return true
	default:
		return false
	}
}

func topicIntegerValue(rule any) (*big.Int, bool) {
	switch rule := rule.(type) {
	case *big.Int:
		if rule == nil {
			return nil, false
		}
		return new(big.Int).Set(rule), true
	case int:
		return big.NewInt(int64(rule)), true
	case int8:
		return big.NewInt(int64(rule)), true
	case int16:
		return big.NewInt(int64(rule)), true
	case int32:
		return big.NewInt(int64(rule)), true
	case int64:
		return big.NewInt(rule), true
	case uint:
		return new(big.Int).SetUint64(uint64(rule)), true
	case uint8:
		return new(big.Int).SetUint64(uint64(rule)), true
	case uint16:
		return new(big.Int).SetUint64(uint64(rule)), true
	case uint32:
		return new(big.Int).SetUint64(uint64(rule)), true
	case uint64:
		return new(big.Int).SetUint64(rule), true
	default:
		return nil, false
	}
}

func genIntType(rule int64, size uint) []byte {
	var topic [common.LogTopicLength]byte
	if rule < 0 {
		// if a rule is negative, we need to put it into two's complement,
		// extended to common.LogTopicLength bytes.
		for i := range topic {
			topic[i] = 0xff
		}
	}
	for i := range size {
		topic[common.LogTopicLength-i-1] = byte(rule >> (i * 8))
	}
	return topic[:]
}

// ParseTopics converts the indexed topic fields into actual log field values.
func ParseTopics(out any, fields Arguments, topics []common.LogTopic) error {
	return parseTopicWithSetter(fields, topics,
		func(arg Argument, reconstr any) {
			field := reflect.ValueOf(out).Elem().FieldByName(ToCamelCase(arg.Name))
			field.Set(reflect.ValueOf(reconstr))
		})
}

// ParseTopicsIntoMap converts the indexed topic field-value pairs into map key-value pairs.
func ParseTopicsIntoMap(out map[string]any, fields Arguments, topics []common.LogTopic) error {
	return parseTopicWithSetter(fields, topics,
		func(arg Argument, reconstr any) {
			out[arg.Name] = reconstr
		})
}

// parseTopicWithSetter converts the indexed topic field-value pairs and stores them using the
// provided set function.
//
// Note, dynamic types cannot be reconstructed since they get mapped to Keccak256
// hashes as the topic value!
func parseTopicWithSetter(fields Arguments, topics []common.LogTopic, setter func(Argument, any)) error {
	// Sanity check that the fields and topics match up
	if len(fields) != len(topics) {
		return errors.New("topic/field count mismatch")
	}
	// Iterate over all the fields and reconstruct them from topics
	for i, arg := range fields {
		if !arg.Indexed {
			return errors.New("non-indexed field in topic reconstruction")
		}
		if containsFunctionType(arg.Type) {
			return ErrUnsupportedFunctionType
		}
		var reconstr any
		switch arg.Type.T {
		case StringTy, BytesTy, SliceTy, ArrayTy, TupleTy:
			// Hash-only indexed types have their encoded hashes stored in the topic; return the full topic verbatim.
			reconstr = topics[i]
		case FunctionTy:
			return ErrUnsupportedFunctionType
		default:
			// Topic is already the width of an ABI slot; decode directly.
			var err error
			reconstr, err = toGoType(0, arg.Type, topics[i][:])
			if err != nil {
				return err
			}
		}
		// Use the setter function to store the value
		setter(arg, reconstr)
	}

	return nil
}
