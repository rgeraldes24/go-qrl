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

package apitypes

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	stdmath "math"
	"math/big"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/theQRL/go-qrl/accounts"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
)

var typedDataReferenceTypeRegexp = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*(\[[0-9]*\])*$`)

type ValidationInfo struct {
	Typ     string `json:"type"`
	Message string `json:"message"`
}
type ValidationMessages struct {
	Messages []ValidationInfo
}

const (
	WARN = "WARNING"
	CRIT = "CRITICAL"
	INFO = "Info"
)

func (vs *ValidationMessages) Crit(msg string) {
	vs.Messages = append(vs.Messages, ValidationInfo{CRIT, msg})
}
func (vs *ValidationMessages) Warn(msg string) {
	vs.Messages = append(vs.Messages, ValidationInfo{WARN, msg})
}
func (vs *ValidationMessages) Info(msg string) {
	vs.Messages = append(vs.Messages, ValidationInfo{INFO, msg})
}

// getWarnings returns an error with all messages of type WARN of above, or nil if no warnings were present
func (vs *ValidationMessages) GetWarnings() error {
	var messages []string
	for _, msg := range vs.Messages {
		if msg.Typ == WARN || msg.Typ == CRIT {
			messages = append(messages, msg.Message)
		}
	}
	if len(messages) > 0 {
		return fmt.Errorf("validation failed: %s", strings.Join(messages, ","))
	}
	return nil
}

// SendTxArgs represents the arguments to submit a transaction
// This struct is identical to qrlapi.TransactionArgs, except for the usage of
// common.MixedcaseAddress in From and To
type SendTxArgs struct {
	From                 common.MixedcaseAddress  `json:"from"`
	To                   *common.MixedcaseAddress `json:"to"`
	Gas                  hexutil.Uint64           `json:"gas"`
	MaxFeePerGas         *hexutil.Big             `json:"maxFeePerGas"`
	MaxPriorityFeePerGas *hexutil.Big             `json:"maxPriorityFeePerGas"`
	Value                hexutil.Big              `json:"value"`
	Nonce                hexutil.Uint64           `json:"nonce"`

	// We accept "data" and "input" for backwards-compatibility reasons.
	// "input" is the newer name and should be preferred by clients.
	// Issue detail: https://github.com/theQRL/go-qrl/issues/15628
	Data  *hexutil.Bytes `json:"data"`
	Input *hexutil.Bytes `json:"input,omitempty"`

	AccessList *types.AccessList `json:"accessList,omitempty"`
	ChainID    *hexutil.Big      `json:"chainId,omitempty"`
}

func (args SendTxArgs) String() string {
	s, err := json.Marshal(args)
	if err == nil {
		return string(s)
	}
	return err.Error()
}

// ToTransaction converts the arguments to a transaction.
func (args *SendTxArgs) ToTransaction() *types.Transaction {
	// Add the To-field, if specified
	var to *common.Address
	if args.To != nil {
		dstAddr := args.To.Address()
		to = &dstAddr
	}

	var input []byte
	if args.Input != nil {
		input = *args.Input
	} else if args.Data != nil {
		input = *args.Data
	}

	var data types.TxData
	switch {
	default:
		al := types.AccessList{}
		if args.AccessList != nil {
			al = *args.AccessList
		}
		data = &types.DynamicFeeTx{
			To:         to,
			ChainID:    (*big.Int)(args.ChainID),
			Nonce:      uint64(args.Nonce),
			Gas:        uint64(args.Gas),
			GasFeeCap:  (*big.Int)(args.MaxFeePerGas),
			GasTipCap:  (*big.Int)(args.MaxPriorityFeePerGas),
			Value:      (*big.Int)(&args.Value),
			Data:       input,
			AccessList: al,
		}
	}
	return types.NewTx(data)
}

type SigFormat struct {
	Mime        string
	ByteVersion byte
}

var (
	IntendedValidator = SigFormat{
		accounts.MimetypeDataWithValidator,
		0x00,
	}
	DataTyped = SigFormat{
		accounts.MimetypeTypedData,
		0x01,
	}
	TextPlain = SigFormat{
		accounts.MimetypeTextPlain,
		0x45,
	}
)

type ValidatorData struct {
	Address common.Address
	Message hexutil.Bytes
}

// TypedData is a QRL Typed Structured Data v1 message.
type TypedData struct {
	Types       Types            `json:"types"`
	PrimaryType string           `json:"primaryType"`
	Domain      TypedDataDomain  `json:"domain"`
	Message     TypedDataMessage `json:"message"`
}

// Type declares one named member of a QRL typed-data struct.
type Type struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

func (t *Type) isArray() bool {
	return strings.HasSuffix(t.Type, "]")
}

// typeName returns the base name of a type, stripping any array dimensions.
func (t *Type) typeName() string {
	return strings.SplitN(t.Type, "[", 2)[0]
}

type Types map[string][]Type

type TypePriority struct {
	Type  string
	Value uint
}

type TypedDataMessage = map[string]any

// TypedDataDomain separates a QRL typed-data signature by application,
// protocol version, chain, verifying contract, and application-defined salt.
type TypedDataDomain struct {
	Name              string                `json:"name"`
	Version           string                `json:"version"`
	ChainId           *math.HexOrDecimal256 `json:"chainId"`
	VerifyingContract string                `json:"verifyingContract"`
	Salt              string                `json:"salt"`
}

// TypedDataAndHash returns the QRL typed-data digest and its raw preimage.
//
// The digest is keccak256("QRL-TYPED-DATA-V1" || domainHash || messageHash).
func TypedDataAndHash(typedData TypedData) ([]byte, string, error) {
	domainSeparator, err := typedData.HashStruct(TypedDataDomainType, typedData.Domain.Map())
	if err != nil {
		return nil, "", err
	}
	typedDataHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return nil, "", err
	}
	rawData := fmt.Sprintf("%s%s%s", TypedDataPrefix, string(domainSeparator), string(typedDataHash))
	return crypto.Keccak256([]byte(rawData)), rawData, nil
}

// HashStruct returns keccak256 of a VM64 typed-data struct encoding.
func (typedData *TypedData) HashStruct(primaryType string, data TypedDataMessage) (hexutil.Bytes, error) {
	encodedData, err := typedData.EncodeData(primaryType, data, 1)
	if err != nil {
		return nil, err
	}
	return crypto.Keccak256(encodedData), nil
}

// Dependencies returns an array of custom types ordered by their hierarchical reference tree.
func (typedData *TypedData) Dependencies(primaryType string, found []string) []string {
	primaryType = strings.SplitN(primaryType, "[", 2)[0]
	includes := func(arr []string, str string) bool {
		return slices.Contains(arr, str)
	}

	if includes(found, primaryType) {
		return found
	}
	if typedData.Types[primaryType] == nil {
		return found
	}
	found = append(found, primaryType)
	for _, field := range typedData.Types[primaryType] {
		for _, dep := range typedData.Dependencies(field.Type, found) {
			if !includes(found, dep) {
				found = append(found, dep)
			}
		}
	}
	return found
}

// EncodeType generates the following encoding:
// `name ‖ "(" ‖ member₁ ‖ "," ‖ member₂ ‖ "," ‖ … ‖ memberₙ ")"`
//
// each member is written as `type ‖ " " ‖ name` encodings cascade down and are sorted by name
func (typedData *TypedData) EncodeType(primaryType string) hexutil.Bytes {
	// Get dependencies primary first, then alphabetical
	deps := typedData.Dependencies(primaryType, []string{})
	if len(deps) > 0 {
		slicedDeps := deps[1:]
		sort.Strings(slicedDeps)
		deps = append([]string{primaryType}, slicedDeps...)
	}

	// Format as a string with fields
	var buffer bytes.Buffer
	for _, dep := range deps {
		buffer.WriteString(dep)
		buffer.WriteString("(")
		for i, obj := range typedData.Types[dep] {
			if i > 0 {
				buffer.WriteString(",")
			}
			buffer.WriteString(obj.Type)
			buffer.WriteString(" ")
			buffer.WriteString(obj.Name)
		}
		buffer.WriteString(")")
	}
	return buffer.Bytes()
}

// TypeHash returns keccak256 of the canonical type description.
func (typedData *TypedData) TypeHash(primaryType string) hexutil.Bytes {
	return crypto.Keccak256(typedData.EncodeType(primaryType))
}

// EncodeData generates the following encoding:
// `bytes32Word(typeHash(type(value))) ‖ enc(value₁) ‖ … ‖ enc(valueₙ)`
//
// the type hash is left-aligned in the leading 64-byte VM word, and each
// encoded member occupies one additional 64-byte VM word
func (typedData *TypedData) EncodeData(primaryType string, data map[string]any, depth int) (hexutil.Bytes, error) {
	if err := typedData.validate(); err != nil {
		return nil, err
	}
	fields, exists := typedData.Types[primaryType]
	if !exists {
		return nil, fmt.Errorf("type %q is undefined", primaryType)
	}
	if len(data) != len(fields) {
		return nil, fmt.Errorf("type %q requires exactly %d fields, got %d", primaryType, len(fields), len(data))
	}

	var buffer bytes.Buffer
	buffer.Write(encodeTypedDataHashWord(typedData.TypeHash(primaryType)))

	// Add field contents. Structs and arrays have special handlers.
	for _, field := range fields {
		encType := field.Type
		encValue, exists := data[field.Name]
		if !exists {
			return nil, fmt.Errorf("type %q is missing field %q", primaryType, field.Name)
		}
		if field.isArray() {
			arrayValue, err := convertDataToSlice(encValue)
			if err != nil {
				return nil, fmt.Errorf("type %q field %q: %w", primaryType, field.Name, dataMismatchError(encType, encValue))
			}
			elementType, expectedLength, _, err := arrayElementType(encType)
			if err != nil {
				return nil, err
			}
			if expectedLength >= 0 && len(arrayValue) != expectedLength {
				return nil, fmt.Errorf("array length %d does not match %s", len(arrayValue), encType)
			}
			var arrayBuffer bytes.Buffer
			for index, item := range arrayValue {
				encodedItem, err := typedData.encodeValue(elementType, item, depth+1)
				if err != nil {
					return nil, fmt.Errorf("type %q field %q array element %d: %w", primaryType, field.Name, index, err)
				}
				arrayBuffer.Write(encodedItem)
			}
			buffer.Write(encodeTypedDataHashWord(crypto.Keccak256(arrayBuffer.Bytes())))
		} else if typedData.Types[encType] != nil {
			mapValue, ok := encValue.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("type %q field %q: %w", primaryType, field.Name, dataMismatchError(encType, encValue))
			}
			encodedData, err := typedData.EncodeData(encType, mapValue, depth+1)
			if err != nil {
				return nil, err
			}
			buffer.Write(encodeTypedDataHashWord(crypto.Keccak256(encodedData)))
		} else {
			byteValue, err := typedData.EncodePrimitiveValue(encType, encValue, depth)
			if err != nil {
				return nil, fmt.Errorf("type %q field %q: %w", primaryType, field.Name, err)
			}
			buffer.Write(byteValue)
		}
	}
	return buffer.Bytes(), nil
}

// Attempt to parse bytes in different formats: byte array, hex string, hexutil.Bytes.
func parseBytes(encType any) ([]byte, bool) {
	if encType == nil {
		return nil, false
	}
	// Handle array types. Copy one element at a time so named byte types work.
	val := reflect.ValueOf(encType)
	if val.Kind() == reflect.Array && val.Type().Elem().Kind() == reflect.Uint8 {
		result := make([]byte, val.Len())
		for index := range result {
			result[index] = byte(val.Index(index).Uint())
		}
		return result, true
	}

	switch v := encType.(type) {
	case []byte:
		return v, true
	case hexutil.Bytes:
		return v, true
	case string:
		bytes, err := hexutil.Decode(v)
		if err != nil {
			return nil, false
		}
		return bytes, true
	default:
		return nil, false
	}
}

func parseInteger(encType string, encValue any) (*big.Int, error) {
	signed := strings.HasPrefix(encType, "int")
	prefix := "uint"
	if signed {
		prefix = "int"
	}
	widthText, ok := strings.CutPrefix(encType, prefix)
	if !ok || widthText == "" || len(widthText) > 1 && widthText[0] == '0' {
		return nil, fmt.Errorf("invalid integer type %q", encType)
	}
	width, err := strconv.Atoi(widthText)
	if err != nil || width < 8 || width > uint512.WordBits || width%8 != 0 {
		return nil, fmt.Errorf("invalid integer type %q", encType)
	}

	var integer *big.Int
	switch value := encValue.(type) {
	case *math.HexOrDecimal256:
		if value != nil {
			integer = new(big.Int).Set((*big.Int)(value))
		}
	case math.HexOrDecimal256:
		integer = new(big.Int).Set((*big.Int)(&value))
	case *hexutil.U512:
		if value != nil {
			integer = new(big.Int).Set((*big.Int)(value))
		}
	case hexutil.U512:
		integer = new(big.Int).Set((*big.Int)(&value))
	case *big.Int:
		if value != nil {
			integer = new(big.Int).Set(value)
		}
	case big.Int:
		integer = new(big.Int).Set(&value)
	case json.Number:
		integer, err = parseIntegerString(value.String())
	case string:
		integer, err = parseIntegerString(value)
	case float64:
		if stdmath.IsNaN(value) || stdmath.IsInf(value, 0) {
			err = fmt.Errorf("invalid float value %v", value)
			break
		}
		integer, _ = new(big.Float).SetFloat64(value).Int(nil)
		if integer == nil || new(big.Float).SetInt(integer).Cmp(new(big.Float).SetFloat64(value)) != 0 {
			err = fmt.Errorf("invalid float value %v", value)
		}
	default:
		reflected := reflect.ValueOf(encValue)
		if reflected.IsValid() {
			switch reflected.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				integer = big.NewInt(reflected.Int())
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				integer = new(big.Int).SetUint64(reflected.Uint())
			}
		}
	}
	if err != nil || integer == nil {
		return nil, dataMismatchError(encType, encValue)
	}
	if signed {
		limit := new(big.Int).Lsh(big.NewInt(1), uint(width-1))
		minimum := new(big.Int).Neg(new(big.Int).Set(limit))
		maximum := new(big.Int).Sub(limit, big.NewInt(1))
		if integer.Cmp(minimum) < 0 || integer.Cmp(maximum) > 0 {
			return nil, fmt.Errorf("integer outside %s range", encType)
		}
	} else if integer.Sign() < 0 || integer.BitLen() > width {
		return nil, fmt.Errorf("integer outside %s range", encType)
	}
	return integer, nil
}

func parseIntegerString(input string) (*big.Int, error) {
	sign := ""
	if strings.HasPrefix(input, "-") {
		sign = "-"
		input = strings.TrimPrefix(input, "-")
	}
	base := 10
	if strings.HasPrefix(input, "0x") || strings.HasPrefix(input, "0X") {
		base = 16
		input = input[2:]
	}
	if input == "" {
		return nil, errors.New("empty integer")
	}
	integer, ok := new(big.Int).SetString(sign+input, base)
	if !ok {
		return nil, fmt.Errorf("invalid integer %q", sign+input)
	}
	return integer, nil
}

// EncodePrimitiveValue deals with the primitive values found
// while searching through the typed data
func (typedData *TypedData) EncodePrimitiveValue(encType string, encValue any, depth int) ([]byte, error) {
	if strings.HasSuffix(encType, "]") || !isPrimitiveTypeValid(encType) {
		return nil, fmt.Errorf("invalid primitive type %q", encType)
	}

	word := make([]byte, uint512.WordBytes)
	switch encType {
	case "address":
		address, ok := parseTypedDataAddress(encValue)
		if !ok {
			return nil, dataMismatchError(encType, encValue)
		}
		copy(word, address[:])
		return word, nil
	case "bool":
		boolean, ok := encValue.(bool)
		if !ok {
			return nil, dataMismatchError(encType, encValue)
		}
		if boolean {
			word[len(word)-1] = 1
		}
		return word, nil
	case "string":
		text, ok := encValue.(string)
		if !ok {
			return nil, dataMismatchError(encType, encValue)
		}
		return encodeTypedDataHashWord(crypto.Keccak256([]byte(text))), nil
	case "bytes":
		blob, ok := parseBytes(encValue)
		if !ok {
			return nil, dataMismatchError(encType, encValue)
		}
		return encodeTypedDataHashWord(crypto.Keccak256(blob)), nil
	}
	if strings.HasPrefix(encType, "bytes") {
		width, _ := strconv.Atoi(strings.TrimPrefix(encType, "bytes"))
		blob, ok := parseBytes(encValue)
		if !ok || len(blob) != width {
			return nil, dataMismatchError(encType, encValue)
		}
		copy(word, blob)
		return word, nil
	}
	if strings.HasPrefix(encType, "uint") || strings.HasPrefix(encType, "int") {
		integer, err := parseInteger(encType, encValue)
		if err != nil {
			return nil, err
		}
		if integer.Sign() < 0 {
			integer = new(big.Int).Add(integer, new(big.Int).Lsh(big.NewInt(1), uint512.WordBits))
		}
		integer.FillBytes(word)
		return word, nil
	}
	return nil, fmt.Errorf("unrecognized type %q", encType)
}

// dataMismatchError generates an error for a mismatch between
// the provided type and data
func dataMismatchError(encType string, encValue any) error {
	return fmt.Errorf("provided data '%v' doesn't match type '%s'", encValue, encType)
}

func convertDataToSlice(encValue any) ([]any, error) {
	var outEncValue []any
	if encValue == nil {
		return outEncValue, errors.New("nil is not an array")
	}
	rv := reflect.ValueOf(encValue)
	if rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array {
		for i := 0; i < rv.Len(); i++ {
			outEncValue = append(outEncValue, rv.Index(i).Interface())
		}
	} else {
		return outEncValue, fmt.Errorf("provided data '%v' is not an array", encValue)
	}
	return outEncValue, nil
}

// validate makes sure the types are sound
func (typedData *TypedData) validate() error {
	if typedData == nil {
		return errors.New("typed data is nil")
	}
	if err := typedData.Types.validate(); err != nil {
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
	return typedData.Domain.validate()
}

// Map generates a map version of the typed data
func (typedData *TypedData) Map() map[string]any {
	dataMap := map[string]any{
		"types":       typedData.Types,
		"domain":      typedData.Domain.Map(),
		"primaryType": typedData.PrimaryType,
		"message":     typedData.Message,
	}
	return dataMap
}

// Format returns a human-readable representation for signing approval UIs.
func (typedData *TypedData) Format() ([]*NameValueType, error) {
	if err := typedData.validate(); err != nil {
		return nil, err
	}
	domain, err := typedData.formatData(TypedDataDomainType, typedData.Domain.Map())
	if err != nil {
		return nil, err
	}
	ptype, err := typedData.formatData(typedData.PrimaryType, typedData.Message)
	if err != nil {
		return nil, err
	}
	var nvts []*NameValueType
	nvts = append(nvts, &NameValueType{
		Name:  TypedDataDomainType,
		Value: domain,
		Typ:   "domain",
	})
	nvts = append(nvts, &NameValueType{
		Name:  typedData.PrimaryType,
		Value: ptype,
		Typ:   "primary type",
	})
	return nvts, nil
}

func (typedData *TypedData) formatData(primaryType string, data map[string]any) ([]*NameValueType, error) {
	if _, err := typedData.EncodeData(primaryType, data, 1); err != nil {
		return nil, err
	}
	var output []*NameValueType

	// Add field contents. Structs and arrays have special handlers.
	for _, field := range typedData.Types[primaryType] {
		encName := field.Name
		encValue := data[encName]
		item := &NameValueType{
			Name: encName,
			Typ:  field.Type,
		}
		value, err := typedData.formatValue(field.Type, encValue)
		if err != nil {
			return nil, err
		}
		item.Value = value
		output = append(output, item)
	}
	return output, nil
}

func (typedData *TypedData) formatValue(encType string, value any) (any, error) {
	elementType, expectedLength, isArray, err := arrayElementType(encType)
	if err != nil {
		return nil, err
	}
	if isArray {
		values, err := convertDataToSlice(value)
		if err != nil {
			return nil, dataMismatchError(encType, value)
		}
		if expectedLength >= 0 && len(values) != expectedLength {
			return nil, fmt.Errorf("array length %d does not match %s", len(values), encType)
		}
		formatted := make([]any, len(values))
		for index, element := range values {
			formatted[index], err = typedData.formatValue(elementType, element)
			if err != nil {
				return nil, err
			}
		}
		return formatted, nil
	}
	if _, custom := typedData.Types[encType]; custom {
		message, ok := value.(map[string]any)
		if !ok {
			return nil, dataMismatchError(encType, value)
		}
		return typedData.formatData(encType, message)
	}
	return formatPrimitiveValue(encType, value)
}

func formatPrimitiveValue(encType string, encValue any) (string, error) {
	switch encType {
	case "address":
		address, ok := parseTypedDataAddress(encValue)
		if !ok {
			return "", dataMismatchError(encType, encValue)
		}
		return address.Hex(), nil
	case "bool":
		boolean, ok := encValue.(bool)
		if !ok {
			return "", dataMismatchError(encType, encValue)
		}
		return strconv.FormatBool(boolean), nil
	case "string":
		text, ok := encValue.(string)
		if !ok {
			return "", dataMismatchError(encType, encValue)
		}
		return text, nil
	case "bytes":
		blob, ok := parseBytes(encValue)
		if !ok {
			return "", dataMismatchError(encType, encValue)
		}
		return hexutil.Encode(blob), nil
	}
	if strings.HasPrefix(encType, "bytes") {
		blob, ok := parseBytes(encValue)
		if !ok {
			return "", dataMismatchError(encType, encValue)
		}
		return hexutil.Encode(blob), nil
	}
	if strings.HasPrefix(encType, "uint") || strings.HasPrefix(encType, "int") {
		integer, err := parseInteger(encType, encValue)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("%s (%#x)", integer, integer), nil
	}
	return "", fmt.Errorf("unhandled type %q", encType)
}

// Validate checks if the types object is conformant to the specs
func (t Types) validate() error {
	if len(t) == 0 {
		return errors.New("types are undefined")
	}
	for typeKey, typeArr := range t {
		baseType, err := baseTypeName(typeKey)
		if err != nil || baseType != typeKey {
			return fmt.Errorf("invalid type name %q", typeKey)
		}
		if isReservedTypeName(typeKey) {
			return fmt.Errorf("type name %q is reserved", typeKey)
		}
		for i, typeObj := range typeArr {
			if len(typeObj.Type) == 0 {
				return fmt.Errorf("type %q:%d: empty Type", typeKey, i)
			}
			if len(typeObj.Name) == 0 {
				return fmt.Errorf("type %q:%d: empty Name", typeKey, i)
			}
			if !typedDataReferenceTypeRegexp.MatchString(typeObj.Type) {
				return fmt.Errorf("type %q field %q: unknown reference type %q", typeKey, typeObj.Name, typeObj.Type)
			}
			baseType, err := baseTypeName(typeObj.Type)
			if err != nil {
				return err
			}
			if typeKey == baseType {
				return fmt.Errorf("type %q cannot reference itself", baseType)
			}
			if isPrimitiveTypeValid(typeObj.Type) {
				continue
			}
			if _, exist := t[typeObj.typeName()]; !exist {
				return fmt.Errorf("reference type %q is undefined", typeObj.Type)
			}
		}
	}
	return nil
}

// Checks if the primitive value is valid
func isPrimitiveTypeValid(primitiveType string) bool {
	baseType, err := baseTypeName(primitiveType)
	return err == nil && isPrimitiveBase(baseType)
}

// validate checks if the given domain is valid, i.e. contains at least
// the minimum viable keys and values
func (domain *TypedDataDomain) validate() error {
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
	salt, ok := parseBytes(domain.Salt)
	if !ok || len(salt) != common.HashLength {
		return fmt.Errorf("domain salt must be exactly %d bytes", common.HashLength)
	}
	return nil
}

// Map is a helper function to generate a map version of the domain
func (domain *TypedDataDomain) Map() map[string]any {
	dataMap := map[string]any{}

	if domain.ChainId != nil {
		dataMap["chainId"] = domain.ChainId
	}
	if len(domain.Name) > 0 {
		dataMap["name"] = domain.Name
	}
	if len(domain.Version) > 0 {
		dataMap["version"] = domain.Version
	}
	if len(domain.VerifyingContract) > 0 {
		dataMap["verifyingContract"] = domain.VerifyingContract
	}
	if len(domain.Salt) > 0 {
		dataMap["salt"] = domain.Salt
	}
	return dataMap
}

// NameValueType is a very simple struct with Name, Value and Type. It's meant for simple
// json structures used to communicate signing-info about typed data with the UI
type NameValueType struct {
	Name  string `json:"name"`
	Value any    `json:"value"`
	Typ   string `json:"type"`
}

// Pprint returns a pretty-printed version of nvt
func (nvt *NameValueType) Pprint(depth int) string {
	output := bytes.Buffer{}
	output.WriteString(strings.Repeat("\u00a0", depth*2))
	output.WriteString(fmt.Sprintf("%s [%s]: ", nvt.Name, nvt.Typ))
	pprintValue(&output, nvt.Value, depth)
	return output.String()
}

func pprintValue(output *bytes.Buffer, value any, depth int) {
	switch value := value.(type) {
	case []*NameValueType:
		output.WriteString("\n")
		for _, next := range value {
			sublevel := next.Pprint(depth + 1)
			output.WriteString(sublevel)
		}
	case []any:
		output.WriteString("\n")
		for index, element := range value {
			output.WriteString(strings.Repeat("\u00a0", (depth+1)*2))
			output.WriteString(fmt.Sprintf("[%d]: ", index))
			pprintValue(output, element, depth+1)
		}
	case nil:
		output.WriteString("\n")
	default:
		output.WriteString(fmt.Sprintf("%q\n", value))
	}
}

const (
	// TypedDataDomainType is the mandatory domain type for QRL typed data v1.
	TypedDataDomainType = "QRLTypedDataDomain"
	// TypedDataPrefix separates typed-data signatures from all other QRL signatures.
	TypedDataPrefix = "QRL-TYPED-DATA-V1"
)

var qrlTypedDataDomain = []Type{
	{Name: "name", Type: "string"},
	{Name: "version", Type: "string"},
	{Name: "chainId", Type: "uint256"},
	{Name: "verifyingContract", Type: "address"},
	{Name: "salt", Type: "bytes32"},
}

// UnmarshalJSON preserves JSON integer tokens as json.Number so values wider
// than 53 bits are not rounded through float64 before hashing.
func (typedData *TypedData) UnmarshalJSON(input []byte) error {
	type typedDataAlias TypedData
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	return decoder.Decode((*typedDataAlias)(typedData))
}

// arrayElementType returns the element type and optional fixed length of the
// outermost array dimension.
func arrayElementType(encType string) (string, int, bool, error) {
	if !strings.HasSuffix(encType, "]") {
		return encType, -1, false, nil
	}
	open := strings.LastIndexByte(encType, '[')
	if open <= 0 {
		return "", 0, false, fmt.Errorf("invalid array type %q", encType)
	}
	lengthText := encType[open+1 : len(encType)-1]
	length := -1
	if lengthText != "" {
		if len(lengthText) > 1 && lengthText[0] == '0' {
			return "", 0, false, fmt.Errorf("invalid array length %q", lengthText)
		}
		parsed, err := strconv.Atoi(lengthText)
		if err != nil || parsed < 1 {
			return "", 0, false, fmt.Errorf("invalid array length %q", lengthText)
		}
		length = parsed
	}
	return encType[:open], length, true, nil
}

func baseTypeName(encType string) (string, error) {
	if encType == "" || !typedDataReferenceTypeRegexp.MatchString(encType) {
		return "", fmt.Errorf("invalid type %q", encType)
	}
	for strings.HasSuffix(encType, "]") {
		elementType, _, _, err := arrayElementType(encType)
		if err != nil {
			return "", err
		}
		encType = elementType
	}
	return encType, nil
}

func isPrimitiveBase(encType string) bool {
	switch encType {
	case "address", "bool", "string", "bytes":
		return true
	}
	if widthText, ok := strings.CutPrefix(encType, "bytes"); ok {
		width, err := strconv.Atoi(widthText)
		return err == nil && widthText != "" && (len(widthText) == 1 || widthText[0] != '0') && width >= 1 && width <= uint512.WordBytes
	}
	for _, prefix := range []string{"int", "uint"} {
		if widthText, ok := strings.CutPrefix(encType, prefix); ok {
			width, err := strconv.Atoi(widthText)
			return err == nil && widthText != "" && (len(widthText) == 1 || widthText[0] != '0') && width >= 8 && width <= uint512.WordBits && width%8 == 0
		}
	}
	return false
}

func isReservedTypeName(name string) bool {
	switch name {
	case "address", "bool", "string", "bytes", "function", "int", "uint":
		return true
	default:
		return isPrimitiveBase(name)
	}
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

func (typedData *TypedData) encodeValue(encType string, value any, depth int) ([]byte, error) {
	elementType, expectedLength, isArray, err := arrayElementType(encType)
	if err != nil {
		return nil, err
	}
	if isArray {
		values, err := convertDataToSlice(value)
		if err != nil {
			return nil, dataMismatchError(encType, value)
		}
		if expectedLength >= 0 && len(values) != expectedLength {
			return nil, fmt.Errorf("array length %d does not match %s", len(values), encType)
		}
		var encoded bytes.Buffer
		for index, element := range values {
			word, err := typedData.encodeValue(elementType, element, depth+1)
			if err != nil {
				return nil, fmt.Errorf("array element %d: %w", index, err)
			}
			encoded.Write(word)
		}
		return encodeTypedDataHashWord(crypto.Keccak256(encoded.Bytes())), nil
	}
	if _, custom := typedData.Types[encType]; custom {
		message, ok := value.(map[string]any)
		if !ok {
			return nil, dataMismatchError(encType, value)
		}
		hash, err := typedData.HashStruct(encType, message)
		if err != nil {
			return nil, err
		}
		return encodeTypedDataHashWord(hash), nil
	}
	return typedData.EncodePrimitiveValue(encType, value, depth)
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

func encodeTypedDataHashWord(hash []byte) []byte {
	word := make([]byte, uint512.WordBytes)
	copy(word, hash)
	return word
}
