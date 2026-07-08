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

// MakeTopics converts a filter query argument list into a filter topic set.
// Topics are 64-byte ABI slots: numeric values are right-aligned, while
// fixed bytes and Keccak hash topics are left-aligned like ABI bytes32 values.
// Use common.LogTopic for already-formed full topics.
func MakeTopics(query ...[]any) ([][]common.LogTopic, error) {
	topics := make([][]common.LogTopic, len(query))
	for i, filter := range query {
		for _, rule := range filter {
			var topic common.LogTopic

			// Try to generate the topic based on simple types
			switch rule := rule.(type) {
			case common.LogTopic:
				copy(topic[:], rule[:])
			case common.Hash:
				topic = common.HashToLogTopic(rule)
			case common.Address:
				copy(topic[common.LogTopicLength-common.AddressLength:], rule[:])
			case *big.Int:
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
				topic = common.HashToLogTopic(crypto.Keccak256Hash([]byte(rule)))
			case []byte:
				topic = common.HashToLogTopic(crypto.Keccak256Hash(rule))

			default:
				// Indexed dynamic values are stored as the Keccak256 hash of their
				// ABI encoding. Strings and bytes can be hashed from their preimage
				// here; arrays and tuples use precomputed common.LogTopic values in
				// generated binding filter rules.

				// Attempt to generate the topic from funky types
				val := reflect.ValueOf(rule)
				switch {
				// static byte array
				case val.Kind() == reflect.Array && reflect.TypeOf(rule).Elem().Kind() == reflect.Uint8:
					reflect.Copy(reflect.ValueOf(topic[:val.Len()]), val)
				default:
					return nil, fmt.Errorf("unsupported indexed type: %T", rule)
				}
			}
			topics[i] = append(topics[i], topic)
		}
	}
	return topics, nil
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
	return parseTopics(out, fields, topics, nil)
}

// ParseTopicsWithKnownFields converts indexed topic fields while allowing struct
// tags for non-indexed fields from the same event.
func ParseTopicsWithKnownFields(out any, fields Arguments, topics []common.LogTopic, knownFields Arguments) error {
	allowedMissingTags := make(map[string]struct{})
	for _, arg := range knownFields {
		if !arg.Indexed {
			allowedMissingTags[arg.Name] = struct{}{}
		}
	}
	return parseTopics(out, fields, topics, allowedMissingTags)
}

func parseTopics(out any, fields Arguments, topics []common.LogTopic, allowedMissingTags map[string]struct{}) error {
	if len(fields) != len(topics) {
		return errors.New("topic/field count mismatch")
	}
	value := reflect.ValueOf(out).Elem()
	argNames := make([]string, len(fields))
	for i, arg := range fields {
		argNames[i] = arg.Name
	}
	abi2struct, err := mapArgNamesToStructFields(argNames, value, allowedMissingTags)
	if err != nil {
		return err
	}
	return parseTopicWithSetter(fields, topics,
		func(arg Argument, reconstr any) error {
			field := value.FieldByName(abi2struct[arg.Name])
			if !field.IsValid() {
				return fmt.Errorf("abi: field %s can't be found in the given value", arg.Name)
			}
			return set(field, reflect.ValueOf(reconstr))
		})
}

// ParseTopicsIntoMap converts the indexed topic field-value pairs into map key-value pairs.
func ParseTopicsIntoMap(out map[string]any, fields Arguments, topics []common.LogTopic) error {
	return parseTopicWithSetter(fields, topics,
		func(arg Argument, reconstr any) error {
			out[arg.Name] = reconstr
			return nil
		})
}

// parseTopicWithSetter converts the indexed topic field-value pairs and stores them using the
// provided set function.
//
// Note, dynamic types cannot be reconstructed since they get mapped to Keccak256
// hashes as the topic value!
func parseTopicWithSetter(fields Arguments, topics []common.LogTopic, setter func(Argument, any) error) error {
	// Sanity check that the fields and topics match up
	if len(fields) != len(topics) {
		return errors.New("topic/field count mismatch")
	}
	// Iterate over all the fields and reconstruct them from topics
	for i, arg := range fields {
		if !arg.Indexed {
			return errors.New("non-indexed field in topic reconstruction")
		}
		var reconstr any
		switch arg.Type.T {
		case TupleTy, StringTy, BytesTy, SliceTy, ArrayTy:
			// Dynamic indexed values and tuple values have their keccak256 hashes
			// stored in the topic — returned verbatim.
			reconstr = topics[i]
		default:
			// Topic is already the width of an ABI slot (64 bytes); decode directly.
			var err error
			reconstr, err = toGoType(0, arg.Type, topics[i][:])
			if err != nil {
				return err
			}
		}
		// Use the setter function to store the value
		if err := setter(arg, reconstr); err != nil {
			return err
		}
	}

	return nil
}
