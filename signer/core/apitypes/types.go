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
	"fmt"
	"math/big"
	"slices"
	"strings"

	"github.com/theQRL/go-qrl/accounts"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
)

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
	parsed, err := parseTypedDataType(t.Type)
	return err == nil && parsed.isArray()
}

// typeName returns the base name of a possibly nested array type.
func (t *Type) typeName() string {
	parsed, err := parseTypedDataType(t.Type)
	if err != nil {
		return t.Type
	}
	return parsed.base
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
	if err := typedData.validate(); err != nil {
		return nil, "", err
	}
	domainSeparator, err := typedDataHashStruct(&typedData, TypedDataDomainType, typedData.Domain.Map(), 1)
	if err != nil {
		return nil, "", err
	}
	typedDataHash, err := typedDataHashStruct(&typedData, typedData.PrimaryType, typedData.Message, 1)
	if err != nil {
		return nil, "", err
	}
	rawData := make([]byte, 0, len(TypedDataPrefix)+2*common.HashLength)
	rawData = append(rawData, TypedDataPrefix...)
	rawData = append(rawData, domainSeparator...)
	rawData = append(rawData, typedDataHash...)
	return crypto.Keccak256(rawData), string(rawData), nil
}

// HashStruct returns keccak256 of a VM64 typed-data struct encoding.
func (typedData *TypedData) HashStruct(primaryType string, data TypedDataMessage) (hexutil.Bytes, error) {
	if err := typedData.validate(); err != nil {
		return nil, err
	}
	return typedDataHashStruct(typedData, primaryType, data, 1)
}

// Dependencies returns primaryType followed by its referenced types.
func (typedData *TypedData) Dependencies(primaryType string, found []string) []string {
	dependencies, err := typedDataDependencies(typedData.Types, primaryType)
	if err != nil {
		return found
	}
	for _, dependency := range dependencies {
		if !slices.Contains(found, dependency) {
			found = append(found, dependency)
		}
	}
	return found
}

// EncodeType generates the following encoding:
// `name ‖ "(" ‖ member₁ ‖ "," ‖ member₂ ‖ "," ‖ … ‖ memberₙ ")"`
//
// each member is written as `type ‖ " " ‖ name` encodings cascade down and are sorted by name
func (typedData *TypedData) EncodeType(primaryType string) hexutil.Bytes {
	encoded, err := typedDataEncodeType(typedData.Types, primaryType)
	if err != nil {
		return nil
	}
	return encoded
}

// TypeHash returns keccak256 of the canonical type description.
func (typedData *TypedData) TypeHash(primaryType string) hexutil.Bytes {
	hash, err := typedDataTypeHash(typedData.Types, primaryType)
	if err != nil {
		return nil
	}
	return hash
}

// EncodeData generates the following encoding:
// `enc(value₁) ‖ enc(value₂) ‖ … ‖ enc(valueₙ)`
//
// each encoded member is one 64-byte VM word
func (typedData *TypedData) EncodeData(primaryType string, data map[string]any, depth int) (hexutil.Bytes, error) {
	if err := typedData.validate(); err != nil {
		return nil, err
	}
	if depth < 1 {
		depth = 1
	}
	return typedDataEncodeData(typedData, primaryType, data, depth)
}

// Attempt to parse bytes in different formats: byte array, hex string, hexutil.Bytes.
func parseBytes(encType any) ([]byte, bool) {
	return parseTypedDataBytes(encType)
}

func parseInteger(encType string, encValue any) (*big.Int, error) {
	prefix, widthText, ok := splitNumericType(encType, "uint", "int")
	if !ok || widthText == "" {
		return nil, fmt.Errorf("invalid integer type %q", encType)
	}
	width, err := parseTypeWidth(prefix, widthText, uint512.WordBits)
	if err != nil || width%8 != 0 {
		return nil, fmt.Errorf("invalid integer type %q", encType)
	}
	integer, err := parseTypedDataInteger(encValue)
	if err != nil {
		return nil, err
	}
	if err := validateTypedDataInteger(prefix == "int", width, integer); err != nil {
		return nil, err
	}
	return integer, nil
}

// EncodePrimitiveValue deals with the primitive values found
// while searching through the typed data
func (typedData *TypedData) EncodePrimitiveValue(encType string, encValue any, depth int) ([]byte, error) {
	parsed, err := parseTypedDataType(encType)
	if err != nil || parsed.isArray() {
		return nil, fmt.Errorf("invalid primitive type %q", encType)
	}
	if err := validateBaseType(parsed.base); err != nil {
		return nil, err
	}
	return typedDataEncodePrimitive(parsed.base, encValue)
}

// dataMismatchError generates an error for a mismatch between
// the provided type and data
func dataMismatchError(encType string, encValue any) error {
	return fmt.Errorf("provided data '%v' doesn't match type '%s'", encValue, encType)
}

func convertDataToSlice(encValue any) ([]any, error) {
	return typedDataSlice(encValue)
}

// validate makes sure the types are sound
func (typedData *TypedData) validate() error {
	return validateTypedData(typedData)
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
	return typedDataFormat(typedData)
}

func (typedData *TypedData) formatData(primaryType string, data map[string]any) ([]*NameValueType, error) {
	return typedDataFormatData(typedData, primaryType, data, 1)
}

func formatPrimitiveValue(encType string, encValue any) (string, error) {
	return typedDataFormatPrimitive(encType, encValue)
}

// Validate checks if the types object is conformant to the specs
func (t Types) validate() error {
	return validateTypedDataTypes(t)
}

// Checks if the primitive value is valid
func isPrimitiveTypeValid(primitiveType string) bool {
	parsed, err := parseTypedDataType(primitiveType)
	return err == nil && isPrimitiveBase(parsed.base)
}

// validate checks if the given domain is valid, i.e. contains at least
// the minimum viable keys and values
func (domain *TypedDataDomain) validate() error {
	return domain.validateV1()
}

// Map is a helper function to generate a map version of the domain
func (domain *TypedDataDomain) Map() map[string]any {
	return map[string]any{
		"name":              domain.Name,
		"version":           domain.Version,
		"chainId":           domain.ChainId,
		"verifyingContract": domain.VerifyingContract,
		"salt":              domain.Salt,
	}
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
