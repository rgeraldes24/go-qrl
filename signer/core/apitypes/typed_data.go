// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package apitypes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto"
)

const (
	// TypedDataDomainType is the mandatory domain type for QRL typed data v1.
	TypedDataDomainType = "QRLTypedDataDomain"
	// TypedDataVersion identifies the envelope and hashing rules in this file.
	TypedDataVersion = "1"
	// TypedDataAlgorithm is the only signature algorithm supported by v1.
	TypedDataAlgorithm = "ML-DSA-87"
	// TypedDataPrefix separates typed-data signatures from all other QRL signatures.
	TypedDataPrefix = "QRL-TYPED-DATA-V1"

	maxTypedDataDepth       = 32
	maxTypedDataArrayLength = 1024
	maxTypedDataTypes       = 256
	maxTypedDataFields      = 256
	dynamicArrayLength      = -1
)

var qrlTypedDataDomain = []Type{
	{Name: "name", Type: "string"},
	{Name: "version", Type: "string"},
	{Name: "chainId", Type: "uint256"},
	{Name: "verifyingContract", Type: "address"},
	{Name: "salt", Type: "bytes32"},
}

// TypedDataSignature is the self-contained JSON result of account_signTypedData.
// ML-DSA signatures do not recover their public key, so verification requires
// the public key and descriptor as part of the result.
type TypedDataSignature struct {
	Version    string         `json:"version"`
	Algorithm  string         `json:"algorithm"`
	Address    common.Address `json:"address"`
	Digest     common.Hash    `json:"digest"`
	PublicKey  hexutil.Bytes  `json:"publicKey"`
	Descriptor hexutil.Bytes  `json:"descriptor"`
	Signature  hexutil.Bytes  `json:"signature"`
}

// Verify checks the envelope, derives its claimed address from the public key
// and descriptor, recomputes the typed-data digest, and verifies the signature.
func (sig *TypedDataSignature) Verify(typedData TypedData) error {
	if sig == nil {
		return errors.New("typed data signature is nil")
	}
	if sig.Version != TypedDataVersion {
		return fmt.Errorf("unsupported typed data signature version %q", sig.Version)
	}
	if sig.Algorithm != TypedDataAlgorithm {
		return fmt.Errorf("unsupported typed data signature algorithm %q", sig.Algorithm)
	}
	if len(sig.PublicKey) != pqcrypto.MLDSA87PublicKeyLength {
		return fmt.Errorf("invalid ML-DSA-87 public key length %d", len(sig.PublicKey))
	}
	if len(sig.Descriptor) != pqcrypto.DescriptorSize {
		return fmt.Errorf("invalid wallet descriptor length %d", len(sig.Descriptor))
	}
	if len(sig.Signature) != pqcrypto.MLDSA87SignatureLength {
		return fmt.Errorf("invalid ML-DSA-87 signature length %d", len(sig.Signature))
	}
	descriptor, err := pqcrypto.BytesToDescriptor(sig.Descriptor)
	if err != nil {
		return fmt.Errorf("invalid wallet descriptor: %w", err)
	}
	address, err := pqcrypto.PublicKeyAndDescriptorToAddress(sig.PublicKey, descriptor)
	if err != nil {
		return fmt.Errorf("derive signer address: %w", err)
	}
	if address != sig.Address {
		return fmt.Errorf("public key derives address %s, not claimed address %s", address, sig.Address)
	}
	digest, _, err := TypedDataAndHash(typedData)
	if err != nil {
		return err
	}
	if !bytes.Equal(digest, sig.Digest[:]) {
		return fmt.Errorf("typed data digest mismatch: have %s, want %s", sig.Digest, common.BytesToHash(digest))
	}
	valid, err := pqcrypto.MLDSA87VerifySignature(sig.Signature, digest, sig.PublicKey, descriptor)
	if err != nil {
		return fmt.Errorf("verify ML-DSA-87 signature: %w", err)
	}
	if !valid {
		return pqcrypto.ErrBadSignature
	}
	return nil
}

// MarshalJSON converts schema-aware Go values to the canonical typed-data wire
// representation before encoding them as JSON.
func (typedData TypedData) MarshalJSON() ([]byte, error) {
	if _, _, err := TypedDataAndHash(typedData); err != nil {
		return nil, err
	}
	message, err := typedDataJSONValue(&typedData, parsedTypedDataType{base: typedData.PrimaryType}, typedData.Message)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Types       Types           `json:"types"`
		PrimaryType string          `json:"primaryType"`
		Domain      TypedDataDomain `json:"domain"`
		Message     any             `json:"message"`
	}{
		Types:       typedData.Types,
		PrimaryType: typedData.PrimaryType,
		Domain:      typedData.Domain,
		Message:     message,
	})
}

func typedDataJSONValue(typedData *TypedData, typ parsedTypedDataType, value any) (any, error) {
	if typ.isArray() {
		values, _ := typedDataSlice(value)
		elements := make([]any, len(values))
		for index, element := range values {
			encoded, err := typedDataJSONValue(typedData, typ.elementType(), element)
			if err != nil {
				return nil, err
			}
			elements[index] = encoded
		}
		return elements, nil
	}
	if fields, custom := typedData.Types[typ.base]; custom {
		message := value.(map[string]any)
		encoded := make(map[string]any, len(fields))
		for _, field := range fields {
			fieldType, _ := parseTypedDataType(field.Type)
			fieldValue, err := typedDataJSONValue(typedData, fieldType, message[field.Name])
			if err != nil {
				return nil, err
			}
			encoded[field.Name] = fieldValue
		}
		return encoded, nil
	}
	if _, _, integerType := splitNumericType(typ.base, "uint", "int"); integerType {
		integer, err := parseTypedDataInteger(value)
		if err != nil {
			return nil, err
		}
		return integer.String(), nil
	}
	switch {
	case typ.base == "address":
		address, _ := parseTypedDataAddress(value)
		return address.Hex(), nil
	case typ.base == "bytes" || strings.HasPrefix(typ.base, "bytes"):
		blob, _ := parseTypedDataBytes(value)
		return hexutil.Encode(blob), nil
	default:
		return value, nil
	}
}

// UnmarshalJSON preserves JSON integer tokens as json.Number. This avoids the
// lossy float64 conversion performed when decoding into map[string]any.
func (typedData *TypedData) UnmarshalJSON(input []byte) error {
	if err := rejectDuplicateJSONKeys(input); err != nil {
		return err
	}
	if err := validateTypedDataJSONShape(input); err != nil {
		return err
	}
	type typedDataAlias TypedData
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var decoded typedDataAlias
	if err := decoder.Decode(&decoded); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("multiple JSON values in typed data")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	*typedData = TypedData(decoded)
	return nil
}

func validateTypedDataJSONShape(input []byte) error {
	topLevel, err := decodeExactJSONObject(input, "typed data", "types", "primaryType", "domain", "message")
	if err != nil {
		return err
	}
	types, err := decodeJSONObject(topLevel["types"], "types")
	if err != nil {
		return err
	}
	for name, declaration := range types {
		var fields []json.RawMessage
		if err := json.Unmarshal(declaration, &fields); err != nil || fields == nil {
			return fmt.Errorf("type %q declaration must be an array", name)
		}
		for index, field := range fields {
			if _, err := decodeExactJSONObject(field, fmt.Sprintf("type %q field %d", name, index), "name", "type"); err != nil {
				return err
			}
		}
	}
	if _, err := decodeExactJSONObject(topLevel["domain"], "domain", "name", "version", "chainId", "verifyingContract", "salt"); err != nil {
		return err
	}
	if _, err := decodeJSONObject(topLevel["message"], "message"); err != nil {
		return err
	}
	return nil
}

func decodeExactJSONObject(input []byte, label string, required ...string) (map[string]json.RawMessage, error) {
	object, err := decodeJSONObject(input, label)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(required))
	for _, name := range required {
		allowed[name] = struct{}{}
	}
	for name := range object {
		if _, exists := allowed[name]; !exists {
			return nil, fmt.Errorf("%s has unknown property %q", label, name)
		}
	}
	for _, name := range required {
		if _, exists := object[name]; !exists {
			return nil, fmt.Errorf("%s is missing required property %q", label, name)
		}
	}
	return object, nil
}

func decodeJSONObject(input []byte, label string) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(input, &object); err != nil || object == nil {
		return nil, fmt.Errorf("%s must be an object", label)
	}
	return object, nil
}

func rejectDuplicateJSONKeys(input []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values in typed data")
		}
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			keys[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("invalid JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("invalid JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

type parsedTypedDataType struct {
	base       string
	dimensions []int
}

func parseTypedDataType(input string) (parsedTypedDataType, error) {
	if input == "" {
		return parsedTypedDataType{}, errors.New("empty type")
	}
	baseEnd := strings.IndexByte(input, '[')
	if baseEnd == -1 {
		baseEnd = len(input)
	}
	parsed := parsedTypedDataType{base: input[:baseEnd]}
	if !isIdentifier(parsed.base) {
		return parsedTypedDataType{}, fmt.Errorf("invalid type name %q", input)
	}
	for offset := baseEnd; offset < len(input); {
		if input[offset] != '[' {
			return parsedTypedDataType{}, fmt.Errorf("invalid array type %q", input)
		}
		closeOffset := strings.IndexByte(input[offset:], ']')
		if closeOffset == -1 {
			return parsedTypedDataType{}, fmt.Errorf("invalid array type %q", input)
		}
		closeOffset += offset
		lengthText := input[offset+1 : closeOffset]
		length := dynamicArrayLength
		if lengthText != "" {
			if !isCanonicalDecimal(lengthText) {
				return parsedTypedDataType{}, fmt.Errorf("invalid array length %q", lengthText)
			}
			value, err := strconv.ParseUint(lengthText, 10, 31)
			if err != nil || value == 0 || value > maxTypedDataArrayLength {
				return parsedTypedDataType{}, fmt.Errorf("invalid array length %q", lengthText)
			}
			length = int(value)
		}
		parsed.dimensions = append(parsed.dimensions, length)
		offset = closeOffset + 1
	}
	return parsed, nil
}

func (typ parsedTypedDataType) isArray() bool {
	return len(typ.dimensions) != 0
}

func (typ parsedTypedDataType) elementType() parsedTypedDataType {
	element := parsedTypedDataType{base: typ.base}
	element.dimensions = append(element.dimensions, typ.dimensions[:len(typ.dimensions)-1]...)
	return element
}

func (typ parsedTypedDataType) String() string {
	var out strings.Builder
	out.WriteString(typ.base)
	for _, length := range typ.dimensions {
		out.WriteByte('[')
		if length != dynamicArrayLength {
			out.WriteString(strconv.Itoa(length))
		}
		out.WriteByte(']')
	}
	return out.String()
}

func isIdentifier(input string) bool {
	if input == "" || !isIdentifierStart(input[0]) {
		return false
	}
	for i := 1; i < len(input); i++ {
		if !isIdentifierPart(input[i]) {
			return false
		}
	}
	return true
}

func isIdentifierStart(ch byte) bool {
	return ch == '_' || ch >= 'A' && ch <= 'Z' || ch >= 'a' && ch <= 'z'
}

func isIdentifierPart(ch byte) bool {
	return isIdentifierStart(ch) || ch >= '0' && ch <= '9'
}

func isCanonicalDecimal(input string) bool {
	if input == "" || len(input) > 1 && input[0] == '0' {
		return false
	}
	for i := range input {
		if input[i] < '0' || input[i] > '9' {
			return false
		}
	}
	return true
}

func validateBaseType(base string) error {
	switch base {
	case "address", "bool", "string", "bytes":
		return nil
	case "function":
		return errors.New("typed data function values are not supported in v1")
	case "int", "uint":
		return fmt.Errorf("integer type %q must declare an explicit width", base)
	}
	if prefix, widthText, ok := splitNumericType(base, "uint", "int"); ok {
		width, err := parseTypeWidth(prefix, widthText, uint512.WordBits)
		if err != nil {
			return err
		}
		if width%8 != 0 {
			return fmt.Errorf("integer width %d is not a multiple of 8", width)
		}
		return nil
	}
	if _, widthText, ok := splitNumericType(base, "bytes"); ok {
		width, err := parseTypeWidth("bytes", widthText, uint512.WordBytes)
		if err != nil {
			return err
		}
		if width == 0 {
			return errors.New("bytes0 is not a valid typed data type")
		}
		return nil
	}
	return nil // Custom types are checked against the Types map.
}

func splitNumericType(input string, prefixes ...string) (string, string, bool) {
	for _, prefix := range prefixes {
		if width, ok := strings.CutPrefix(input, prefix); ok && isDecimal(width) {
			return prefix, width, true
		}
	}
	return "", "", false
}

func isDecimal(input string) bool {
	if input == "" {
		return false
	}
	for index := range input {
		if input[index] < '0' || input[index] > '9' {
			return false
		}
	}
	return true
}

func parseTypeWidth(prefix, input string, maximum int) (int, error) {
	if !isCanonicalDecimal(input) {
		return 0, fmt.Errorf("invalid %s width %q", prefix, input)
	}
	width, err := strconv.Atoi(input)
	if err != nil || width < 1 || width > maximum {
		return 0, fmt.Errorf("invalid %s width %q", prefix, input)
	}
	return width, nil
}

func isPrimitiveBase(base string) bool {
	if base == "address" || base == "bool" || base == "string" || base == "bytes" {
		return true
	}
	if _, _, ok := splitNumericType(base, "bytes", "uint", "int"); ok {
		return validateBaseType(base) == nil
	}
	return false
}

func isReservedTypeName(name string) bool {
	if name == "address" || name == "bool" || name == "string" || name == "bytes" ||
		name == "function" || name == "int" || name == "uint" {
		return true
	}
	_, _, reserved := splitNumericType(name, "bytes", "int", "uint")
	return reserved
}

func validateTypedDataTypes(types Types) error {
	if len(types) == 0 {
		return errors.New("types are undefined")
	}
	if len(types) > maxTypedDataTypes {
		return fmt.Errorf("too many typed data types: %d", len(types))
	}
	for name, fields := range types {
		if !isIdentifier(name) {
			return fmt.Errorf("invalid type name %q", name)
		}
		if isReservedTypeName(name) {
			return fmt.Errorf("type name %q is reserved", name)
		}
		if len(fields) > maxTypedDataFields {
			return fmt.Errorf("type %q has too many fields: %d", name, len(fields))
		}
		fieldNames := make(map[string]struct{}, len(fields))
		for index, field := range fields {
			if !isIdentifier(field.Name) {
				return fmt.Errorf("type %q field %d has invalid name %q", name, index, field.Name)
			}
			if _, exists := fieldNames[field.Name]; exists {
				return fmt.Errorf("type %q has duplicate field %q", name, field.Name)
			}
			fieldNames[field.Name] = struct{}{}
			parsed, err := parseTypedDataType(field.Type)
			if err != nil {
				return fmt.Errorf("type %q field %q: %w", name, field.Name, err)
			}
			if err := validateBaseType(parsed.base); err != nil {
				return fmt.Errorf("type %q field %q: %w", name, field.Name, err)
			}
			if !isPrimitiveBase(parsed.base) {
				if _, exists := types[parsed.base]; !exists {
					return fmt.Errorf("reference type %q is undefined", parsed.base)
				}
			}
		}
	}
	if err := validateTypeCycles(types); err != nil {
		return err
	}
	return nil
}

func validateTypeCycles(types Types) error {
	const (
		unvisited = iota
		visiting
		visited
	)
	state := make(map[string]int, len(types))
	var visit func(string) error
	visit = func(name string) error {
		switch state[name] {
		case visiting:
			return fmt.Errorf("recursive typed data reference involving %q", name)
		case visited:
			return nil
		}
		state[name] = visiting
		for _, field := range types[name] {
			parsed, _ := parseTypedDataType(field.Type)
			if !isPrimitiveBase(parsed.base) {
				if err := visit(parsed.base); err != nil {
					return err
				}
			}
		}
		state[name] = visited
		return nil
	}
	for name := range types {
		if err := visit(name); err != nil {
			return err
		}
	}
	return nil
}

func validateTypedData(typedData *TypedData) error {
	if typedData == nil {
		return errors.New("typed data is nil")
	}
	if err := validateTypedDataTypes(typedData.Types); err != nil {
		return err
	}
	if typedData.PrimaryType == TypedDataDomainType {
		return errors.New("domain type cannot be the primary type")
	}
	if _, exists := typedData.Types[typedData.PrimaryType]; !exists {
		return fmt.Errorf("primary type %q is undefined", typedData.PrimaryType)
	}
	if err := validateDomainType(typedData.Types[TypedDataDomainType]); err != nil {
		return err
	}
	return typedData.Domain.validateV1()
}

func validateDomainType(fields []Type) error {
	if len(fields) != len(qrlTypedDataDomain) {
		return fmt.Errorf("%s must contain exactly %d fields", TypedDataDomainType, len(qrlTypedDataDomain))
	}
	for index := range fields {
		if fields[index] != qrlTypedDataDomain[index] {
			return fmt.Errorf("invalid %s field %d: have %s %s, want %s %s",
				TypedDataDomainType, index, fields[index].Type, fields[index].Name,
				qrlTypedDataDomain[index].Type, qrlTypedDataDomain[index].Name)
		}
	}
	return nil
}

func (domain *TypedDataDomain) validateV1() error {
	if domain == nil {
		return errors.New("domain is undefined")
	}
	if domain.Name == "" {
		return errors.New("domain name is required")
	}
	if domain.Version == "" {
		return errors.New("domain version is required")
	}
	if domain.ChainId == nil || (*big.Int)(domain.ChainId).Sign() < 0 {
		return errors.New("domain chainId must be a non-negative uint256")
	}
	if _, err := common.NewAddressFromString(domain.VerifyingContract); err != nil {
		return fmt.Errorf("invalid domain verifyingContract: %w", err)
	}
	salt, ok := parseTypedDataBytes(domain.Salt)
	if !ok || len(salt) != common.HashLength {
		return fmt.Errorf("domain salt must be exactly %d bytes", common.HashLength)
	}
	return nil
}

func typedDataDependencies(types Types, primaryType string) ([]string, error) {
	parsed, err := parseTypedDataType(primaryType)
	if err != nil {
		return nil, err
	}
	primaryType = parsed.base
	if _, exists := types[primaryType]; !exists {
		return nil, fmt.Errorf("type %q is undefined", primaryType)
	}
	found := make(map[string]struct{})
	var visit func(string)
	visit = func(name string) {
		if _, exists := found[name]; exists {
			return
		}
		found[name] = struct{}{}
		for _, field := range types[name] {
			fieldType, _ := parseTypedDataType(field.Type)
			if _, exists := types[fieldType.base]; exists {
				visit(fieldType.base)
			}
		}
	}
	visit(primaryType)
	dependencies := make([]string, 0, len(found))
	for name := range found {
		if name != primaryType {
			dependencies = append(dependencies, name)
		}
	}
	sort.Strings(dependencies)
	return append([]string{primaryType}, dependencies...), nil
}

func typedDataEncodeType(types Types, primaryType string) ([]byte, error) {
	dependencies, err := typedDataDependencies(types, primaryType)
	if err != nil {
		return nil, err
	}
	var output strings.Builder
	for _, dependency := range dependencies {
		output.WriteString(dependency)
		output.WriteByte('(')
		for index, field := range types[dependency] {
			if index != 0 {
				output.WriteByte(',')
			}
			output.WriteString(field.Type)
			output.WriteByte(' ')
			output.WriteString(field.Name)
		}
		output.WriteByte(')')
	}
	return []byte(output.String()), nil
}

func typedDataTypeHash(types Types, primaryType string) ([]byte, error) {
	encoded, err := typedDataEncodeType(types, primaryType)
	if err != nil {
		return nil, err
	}
	return crypto.Keccak256(encoded), nil
}

func typedDataHashStruct(typedData *TypedData, primaryType string, data TypedDataMessage, depth int) ([]byte, error) {
	encoded, err := typedDataEncodeData(typedData, primaryType, data, depth)
	if err != nil {
		return nil, err
	}
	return crypto.Keccak256(encoded), nil
}

func typedDataEncodeData(typedData *TypedData, primaryType string, data TypedDataMessage, depth int) ([]byte, error) {
	if depth > maxTypedDataDepth {
		return nil, fmt.Errorf("typed data exceeds maximum depth %d", maxTypedDataDepth)
	}
	fields, exists := typedData.Types[primaryType]
	if !exists {
		return nil, fmt.Errorf("type %q is undefined", primaryType)
	}
	if len(data) != len(fields) {
		return nil, fmt.Errorf("type %q requires exactly %d fields, got %d", primaryType, len(fields), len(data))
	}
	typeHash, err := typedDataTypeHash(typedData.Types, primaryType)
	if err != nil {
		return nil, err
	}
	encoded := make([]byte, 0, (len(fields)+1)*uint512.WordBytes)
	encoded = append(encoded, encodeTypedDataHashWord(typeHash)...)
	for _, field := range fields {
		value, exists := data[field.Name]
		if !exists {
			return nil, fmt.Errorf("type %q is missing field %q", primaryType, field.Name)
		}
		parsed, _ := parseTypedDataType(field.Type)
		fieldEncoding, err := typedDataEncodeValue(typedData, parsed, value, depth)
		if err != nil {
			return nil, fmt.Errorf("type %q field %q: %w", primaryType, field.Name, err)
		}
		encoded = append(encoded, fieldEncoding...)
	}
	return encoded, nil
}

func typedDataEncodeValue(typedData *TypedData, typ parsedTypedDataType, value any, depth int) ([]byte, error) {
	if depth > maxTypedDataDepth {
		return nil, fmt.Errorf("typed data exceeds maximum depth %d", maxTypedDataDepth)
	}
	if typ.isArray() {
		values, err := typedDataSlice(value)
		if err != nil {
			return nil, dataMismatchError(typ.String(), value)
		}
		if len(values) > maxTypedDataArrayLength {
			return nil, fmt.Errorf("array length %d exceeds maximum %d", len(values), maxTypedDataArrayLength)
		}
		expectedLength := typ.dimensions[len(typ.dimensions)-1]
		if expectedLength != dynamicArrayLength && len(values) != expectedLength {
			return nil, fmt.Errorf("array length %d does not match %s", len(values), typ)
		}
		elementType := typ.elementType()
		var encoded bytes.Buffer
		for index, element := range values {
			word, err := typedDataEncodeValue(typedData, elementType, element, depth+1)
			if err != nil {
				return nil, fmt.Errorf("array element %d: %w", index, err)
			}
			encoded.Write(word)
		}
		return encodeTypedDataHashWord(crypto.Keccak256(encoded.Bytes())), nil
	}
	if _, custom := typedData.Types[typ.base]; custom {
		message, ok := value.(map[string]any)
		if !ok {
			return nil, dataMismatchError(typ.String(), value)
		}
		hash, err := typedDataHashStruct(typedData, typ.base, message, depth+1)
		if err != nil {
			return nil, err
		}
		return encodeTypedDataHashWord(hash), nil
	}
	return typedDataEncodePrimitive(typ.base, value)
}

func typedDataEncodePrimitive(encType string, value any) ([]byte, error) {
	word := make([]byte, uint512.WordBytes)
	switch encType {
	case "address":
		address, ok := parseTypedDataAddress(value)
		if !ok {
			return nil, dataMismatchError(encType, value)
		}
		copy(word, address[:])
		return word, nil
	case "bool":
		boolean, ok := value.(bool)
		if !ok {
			return nil, dataMismatchError(encType, value)
		}
		if boolean {
			word[len(word)-1] = 1
		}
		return word, nil
	case "string":
		text, ok := value.(string)
		if !ok {
			return nil, dataMismatchError(encType, value)
		}
		return encodeTypedDataHashWord(crypto.Keccak256([]byte(text))), nil
	case "bytes":
		blob, ok := parseTypedDataBytes(value)
		if !ok {
			return nil, dataMismatchError(encType, value)
		}
		return encodeTypedDataHashWord(crypto.Keccak256(blob)), nil
	}
	if strings.HasPrefix(encType, "bytes") {
		width, err := parseTypeWidth("bytes", strings.TrimPrefix(encType, "bytes"), uint512.WordBytes)
		if err != nil {
			return nil, err
		}
		blob, ok := parseTypedDataBytes(value)
		if !ok || len(blob) != width {
			return nil, dataMismatchError(encType, value)
		}
		copy(word, blob)
		return word, nil
	}
	if prefix, widthText, ok := splitNumericType(encType, "uint", "int"); ok {
		width, err := parseTypeWidth(prefix, widthText, uint512.WordBits)
		if err != nil {
			return nil, err
		}
		integer, err := parseTypedDataInteger(value)
		if err != nil {
			return nil, dataMismatchError(encType, value)
		}
		if err := validateTypedDataInteger(prefix == "int", width, integer); err != nil {
			return nil, fmt.Errorf("%s value %s: %w", encType, integer, err)
		}
		if integer.Sign() < 0 {
			integer = new(big.Int).Add(integer, new(big.Int).Lsh(big.NewInt(1), uint512.WordBits))
		}
		integer.FillBytes(word)
		return word, nil
	}
	return nil, fmt.Errorf("unrecognized type %q", encType)
}

func parseTypedDataAddress(value any) (common.Address, bool) {
	switch value := value.(type) {
	case common.Address:
		return value, true
	case *common.Address:
		if value != nil {
			return *value, true
		}
	case string:
		address, err := common.NewAddressFromString(value)
		return address, err == nil
	case []byte:
		if len(value) == common.AddressLength {
			var address common.Address
			copy(address[:], value)
			return address, true
		}
	case [common.AddressLength]byte:
		return common.Address(value), true
	}
	return common.Address{}, false
}

func parseTypedDataBytes(value any) ([]byte, bool) {
	if value == nil {
		return nil, false
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() == reflect.Array && reflected.Type().Elem().Kind() == reflect.Uint8 {
		result := make([]byte, reflected.Len())
		reflect.Copy(reflect.ValueOf(result), reflected)
		return result, true
	}
	switch value := value.(type) {
	case []byte:
		return value, true
	case hexutil.Bytes:
		return value, true
	case string:
		decoded, err := hexutil.Decode(value)
		if err != nil {
			return nil, false
		}
		return decoded, true
	default:
		return nil, false
	}
}

func parseTypedDataInteger(value any) (*big.Int, error) {
	switch value := value.(type) {
	case *math.HexOrDecimal256:
		if value != nil {
			return new(big.Int).Set((*big.Int)(value)), nil
		}
	case math.HexOrDecimal256:
		return new(big.Int).Set((*big.Int)(&value)), nil
	case *hexutil.U512:
		if value != nil {
			return new(big.Int).Set((*big.Int)(value)), nil
		}
	case hexutil.U512:
		return new(big.Int).Set((*big.Int)(&value)), nil
	case *big.Int:
		if value != nil {
			return new(big.Int).Set(value), nil
		}
	case big.Int:
		return new(big.Int).Set(&value), nil
	case json.Number:
		return parseTypedDataIntegerString(value.String())
	case string:
		return parseTypedDataIntegerString(value)
	case int:
		return big.NewInt(int64(value)), nil
	case int8:
		return big.NewInt(int64(value)), nil
	case int16:
		return big.NewInt(int64(value)), nil
	case int32:
		return big.NewInt(int64(value)), nil
	case int64:
		return big.NewInt(value), nil
	case uint:
		return new(big.Int).SetUint64(uint64(value)), nil
	case uint8:
		return new(big.Int).SetUint64(uint64(value)), nil
	case uint16:
		return new(big.Int).SetUint64(uint64(value)), nil
	case uint32:
		return new(big.Int).SetUint64(uint64(value)), nil
	case uint64:
		return new(big.Int).SetUint64(value), nil
	case float64:
		if value != float64(int64(value)) {
			return nil, fmt.Errorf("non-integral number %v", value)
		}
		return big.NewInt(int64(value)), nil
	}
	return nil, fmt.Errorf("invalid integer value %v", value)
}

func parseTypedDataIntegerString(input string) (*big.Int, error) {
	base := 10
	value := input
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign = "-"
		value = strings.TrimPrefix(value, "-")
	}
	if strings.HasPrefix(value, "0x") || strings.HasPrefix(value, "0X") {
		base = 16
		value = value[2:]
	}
	if value == "" {
		return nil, fmt.Errorf("invalid integer %q", input)
	}
	parsed, ok := new(big.Int).SetString(sign+value, base)
	if !ok {
		return nil, fmt.Errorf("invalid integer %q", input)
	}
	return parsed, nil
}

func validateTypedDataInteger(signed bool, width int, value *big.Int) error {
	if signed {
		limit := new(big.Int).Lsh(big.NewInt(1), uint(width-1))
		minimum := new(big.Int).Neg(new(big.Int).Set(limit))
		maximum := new(big.Int).Sub(limit, big.NewInt(1))
		if value.Cmp(minimum) < 0 || value.Cmp(maximum) > 0 {
			return fmt.Errorf("outside signed %d-bit range", width)
		}
		return nil
	}
	if value.Sign() < 0 || value.BitLen() > width {
		return fmt.Errorf("outside unsigned %d-bit range", width)
	}
	return nil
}

func encodeTypedDataHashWord(hash []byte) []byte {
	word := make([]byte, uint512.WordBytes)
	copy(word, hash)
	return word
}

func typedDataSlice(value any) ([]any, error) {
	if value == nil {
		return nil, errors.New("nil is not an array")
	}
	reflected := reflect.ValueOf(value)
	if reflected.Kind() != reflect.Array && reflected.Kind() != reflect.Slice {
		return nil, fmt.Errorf("%T is not an array", value)
	}
	result := make([]any, reflected.Len())
	for index := range result {
		result[index] = reflected.Index(index).Interface()
	}
	return result, nil
}

func typedDataFormat(typedData *TypedData) ([]*NameValueType, error) {
	if err := validateTypedData(typedData); err != nil {
		return nil, err
	}
	domain, err := typedDataFormatData(typedData, TypedDataDomainType, typedData.Domain.Map(), 1)
	if err != nil {
		return nil, err
	}
	message, err := typedDataFormatData(typedData, typedData.PrimaryType, typedData.Message, 1)
	if err != nil {
		return nil, err
	}
	return []*NameValueType{
		{Name: TypedDataDomainType, Value: domain, Typ: "domain"},
		{Name: typedData.PrimaryType, Value: message, Typ: "primary type"},
	}, nil
}

func typedDataFormatData(typedData *TypedData, primaryType string, data TypedDataMessage, depth int) ([]*NameValueType, error) {
	if _, err := typedDataEncodeData(typedData, primaryType, data, depth); err != nil {
		return nil, err
	}
	fields := typedData.Types[primaryType]
	formatted := make([]*NameValueType, 0, len(fields))
	for _, field := range fields {
		parsed, _ := parseTypedDataType(field.Type)
		value, err := typedDataFormatValue(typedData, parsed, data[field.Name], depth)
		if err != nil {
			return nil, err
		}
		formatted = append(formatted, &NameValueType{Name: field.Name, Value: value, Typ: field.Type})
	}
	return formatted, nil
}

func typedDataFormatValue(typedData *TypedData, typ parsedTypedDataType, value any, depth int) (any, error) {
	if typ.isArray() {
		values, err := typedDataSlice(value)
		if err != nil {
			return nil, err
		}
		formatted := make([]any, len(values))
		for index, element := range values {
			formatted[index], err = typedDataFormatValue(typedData, typ.elementType(), element, depth+1)
			if err != nil {
				return nil, err
			}
		}
		return formatted, nil
	}
	if _, custom := typedData.Types[typ.base]; custom {
		message, ok := value.(map[string]any)
		if !ok {
			return nil, dataMismatchError(typ.String(), value)
		}
		return typedDataFormatData(typedData, typ.base, message, depth+1)
	}
	return typedDataFormatPrimitive(typ.base, value)
}

func typedDataFormatPrimitive(encType string, value any) (string, error) {
	switch encType {
	case "address":
		address, ok := parseTypedDataAddress(value)
		if !ok {
			return "", dataMismatchError(encType, value)
		}
		return address.Hex(), nil
	case "bool":
		boolean, ok := value.(bool)
		if !ok {
			return "", dataMismatchError(encType, value)
		}
		return strconv.FormatBool(boolean), nil
	case "string":
		text, ok := value.(string)
		if !ok {
			return "", dataMismatchError(encType, value)
		}
		return text, nil
	case "bytes":
		blob, ok := parseTypedDataBytes(value)
		if !ok {
			return "", dataMismatchError(encType, value)
		}
		return hexutil.Encode(blob), nil
	}
	if strings.HasPrefix(encType, "bytes") {
		blob, ok := parseTypedDataBytes(value)
		if !ok {
			return "", dataMismatchError(encType, value)
		}
		return hexutil.Encode(blob), nil
	}
	if strings.HasPrefix(encType, "uint") || strings.HasPrefix(encType, "int") {
		integer, err := parseTypedDataInteger(value)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s (%#x)", integer, integer), nil
	}
	return "", fmt.Errorf("unhandled type %q", encType)
}
