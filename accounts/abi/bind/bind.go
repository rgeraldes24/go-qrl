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

// Package bind generates QRL/Hyperion contract Go bindings.
package bind

import (
	"bytes"
	"encoding/hex"
	"errors"
	"fmt"
	"go/format"
	"regexp"
	"strings"
	"text/template"
	"unicode"

	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/crypto"
)

var (
	intRegex = regexp.MustCompile(`(u)?int([0-9]*)`)

	libraryPlaceholderPatternHexLength = common.AddressLength*2 - len(libraryPlaceholderPrefix) - len(libraryPlaceholderSuffix)
	vm64LibraryPlaceholderRegex        = regexp.MustCompile(fmt.Sprintf(`__\$[0-9a-fA-F]{%d}\$__`, libraryPlaceholderPatternHexLength))
	legacyHashLibraryPlaceholderRegex  = regexp.MustCompile(`__\$[0-9a-fA-F]{34}\$__`)

	// ErrUnsupportedLibraryLinking is returned when bytecode still contains a
	// legacy Solidity library link placeholder. Those placeholders reserve 20 bytes
	// for an Ethereum address and cannot be rewritten with a 64-byte QRL address
	// without changing code layout.
	ErrUnsupportedLibraryLinking = errors.New("bind: legacy 20-byte Solidity library link placeholders are unsupported in VM64")

	// ErrUnresolvedLibraryLink is returned when VM64 bytecode contains a link
	// placeholder, but no library metadata was provided to resolve it.
	ErrUnresolvedLibraryLink = errors.New("bind: unresolved VM64 library link placeholder")
)

const (
	libraryPlaceholderPrefix = "__$"
	libraryPlaceholderSuffix = "$__"

	legacyLibraryPlaceholderLength = 40
)

// LibraryLinkPattern derives the VM64 library link pattern for a fully qualified
// library name. The returned string excludes the "__$" and "$__" delimiters and
// is sized so the complete placeholder has the same width as a 64-byte address
// encoded as hex.
func LibraryLinkPattern(qualifiedName string) string {
	hash := hex.EncodeToString(crypto.Keccak512([]byte(qualifiedName)))
	return hash[:libraryPlaceholderPatternHexLength]
}

func isKeyWord(arg string) bool {
	switch arg {
	case "break":
	case "case":
	case "chan":
	case "const":
	case "continue":
	case "default":
	case "defer":
	case "else":
	case "fallthrough":
	case "for":
	case "func":
	case "go":
	case "goto":
	case "if":
	case "import":
	case "interface":
	case "iota":
	case "map":
	case "make":
	case "new":
	case "package":
	case "range":
	case "return":
	case "select":
	case "struct":
	case "switch":
	case "type":
	case "var":
	default:
		return false
	}

	return true
}

// Bind generates a Go wrapper around a contract ABI. This wrapper isn't meant
// to be used as is in client code, but rather as an intermediate struct which
// enforces compile time type safety and naming convention as opposed to having to
// manually maintain hard coded strings that break on runtime.
func Bind(types []string, abis []string, bytecodes []string, fsigs []map[string]string, pkg string, libs map[string]string, aliases map[string]string) (string, error) {
	var (
		// contracts is the map of each individual contract requested binding
		contracts = make(map[string]*tmplContract)

		// structs is the map of all redeclared structs shared by passed contracts.
		structs = make(map[string]*tmplStruct)
	)
	for i := range types {
		// Parse the actual ABI to generate the binding for
		qrvmABI, err := abi.JSON(strings.NewReader(abis[i]))
		if err != nil {
			return "", err
		}
		if err := validateNoFunctionTypes(types[i], qrvmABI); err != nil {
			return "", err
		}
		if err := validateNoLegacyLibraryPlaceholders(types[i], bytecodes[i]); err != nil {
			return "", err
		}
		// Strip any whitespace from the JSON ABI
		strippedABI := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, abis[i])

		// Extract the call and transact methods; events, struct definitions; and sort them alphabetically
		var (
			calls     = make(map[string]*tmplMethod)
			transacts = make(map[string]*tmplMethod)
			events    = make(map[string]*tmplEvent)
			fallback  *tmplMethod
			receive   *tmplMethod

			// identifiers are used to detect duplicated identifiers of functions
			// and events. For all calls, transacts and events, abigen will generate
			// corresponding bindings. However we have to ensure there is no
			// identifier collisions in the bindings of these categories.
			callIdentifiers     = make(map[string]bool)
			transactIdentifiers = make(map[string]bool)
			eventIdentifiers    = make(map[string]bool)
		)

		for _, input := range qrvmABI.Constructor.Inputs {
			if hasStruct(input.Type) {
				bindStructType(input.Type, structs)
			}
		}

		for _, original := range qrvmABI.Methods {
			// Normalize the method for capital cases and non-anonymous inputs/outputs
			normalized := original
			normalizedName := abi.ToCamelCase(alias(aliases, original.Name))
			// Ensure there is no duplicated identifier
			var identifiers = callIdentifiers
			if !original.IsConstant() {
				identifiers = transactIdentifiers
			}
			// Name shouldn't start with a digit. It will make the generated code invalid.
			if len(normalizedName) > 0 && unicode.IsDigit(rune(normalizedName[0])) {
				normalizedName = fmt.Sprintf("M%s", normalizedName)
				normalizedName = abi.ResolveNameConflict(normalizedName, func(name string) bool {
					_, ok := identifiers[name]
					return ok
				})
			}
			if identifiers[normalizedName] {
				return "", fmt.Errorf("duplicated identifier \"%s\"(normalized \"%s\"), use --alias for renaming", original.Name, normalizedName)
			}
			identifiers[normalizedName] = true

			normalized.Name = normalizedName
			normalized.Inputs = make([]abi.Argument, len(original.Inputs))
			copy(normalized.Inputs, original.Inputs)
			for j, input := range normalized.Inputs {
				if input.Name == "" || isKeyWord(input.Name) {
					normalized.Inputs[j].Name = fmt.Sprintf("arg%d", j)
				}
				if hasStruct(input.Type) {
					bindStructType(input.Type, structs)
				}
			}
			normalized.Outputs = make([]abi.Argument, len(original.Outputs))
			copy(normalized.Outputs, original.Outputs)
			for j, output := range normalized.Outputs {
				if output.Name != "" {
					normalized.Outputs[j].Name = abi.ToCamelCase(output.Name)
				}
				if hasStruct(output.Type) {
					bindStructType(output.Type, structs)
				}
			}
			// Append the methods to the call or transact lists
			if original.IsConstant() {
				calls[original.Name] = &tmplMethod{Original: original, Normalized: normalized, Structured: structured(original.Outputs)}
			} else {
				transacts[original.Name] = &tmplMethod{Original: original, Normalized: normalized, Structured: structured(original.Outputs)}
			}
		}
		for _, original := range qrvmABI.Events {
			// Skip anonymous events as they don't support explicit filtering
			if original.Anonymous {
				continue
			}
			// Normalize the event for capital cases and non-anonymous outputs
			normalized := original

			// Ensure there is no duplicated identifier
			normalizedName := abi.ToCamelCase(alias(aliases, original.Name))
			// Name shouldn't start with a digit. It will make the generated code invalid.
			if len(normalizedName) > 0 && unicode.IsDigit(rune(normalizedName[0])) {
				normalizedName = fmt.Sprintf("E%s", normalizedName)
				normalizedName = abi.ResolveNameConflict(normalizedName, func(name string) bool {
					_, ok := eventIdentifiers[name]
					return ok
				})
			}
			if eventIdentifiers[normalizedName] {
				return "", fmt.Errorf("duplicated identifier \"%s\"(normalized \"%s\"), use --alias for renaming", original.Name, normalizedName)
			}
			eventIdentifiers[normalizedName] = true
			normalized.Name = normalizedName

			used := make(map[string]bool)
			normalized.Inputs = make([]abi.Argument, len(original.Inputs))
			copy(normalized.Inputs, original.Inputs)
			for j, input := range normalized.Inputs {
				if input.Name == "" || isKeyWord(input.Name) {
					normalized.Inputs[j].Name = fmt.Sprintf("arg%d", j)
				}
				// Event is a bit special, we need to define event struct in binding,
				// ensure there is no camel-case-style name conflict.
				for index := 0; ; index++ {
					if !used[abi.ToCamelCase(normalized.Inputs[j].Name)] {
						used[abi.ToCamelCase(normalized.Inputs[j].Name)] = true
						break
					}
					normalized.Inputs[j].Name = fmt.Sprintf("%s%d", normalized.Inputs[j].Name, index)
				}
				if hasStruct(input.Type) {
					bindStructType(input.Type, structs)
				}
			}
			// Append the event to the accumulator list
			events[original.Name] = &tmplEvent{Original: original, Normalized: normalized}
		}
		// Add two special fallback functions if they exist
		if qrvmABI.HasFallback() {
			fallback = &tmplMethod{Original: qrvmABI.Fallback}
		}
		if qrvmABI.HasReceive() {
			receive = &tmplMethod{Original: qrvmABI.Receive}
		}
		contracts[types[i]] = &tmplContract{
			Type:        abi.ToCamelCase(types[i]),
			InputABI:    strings.ReplaceAll(strippedABI, "\"", "\\\""),
			InputBin:    strings.TrimPrefix(strings.TrimSpace(bytecodes[i]), "0x"),
			Constructor: qrvmABI.Constructor,
			Calls:       calls,
			Transacts:   transacts,
			Fallback:    fallback,
			Receive:     receive,
			Events:      events,
			Libraries:   make(map[string]string),
		}
		// Function 4-byte signatures are stored in the same sequence
		// as types, if available.
		if len(fsigs) > i {
			contracts[types[i]].FuncSigs = fsigs[i]
		}
		if err := resolveLibraryPlaceholders(types[i], contracts[types[i]].InputBin, libs, contracts[types[i]].Libraries); err != nil {
			return "", err
		}
	}
	// Generate the contract template data content and render it
	data := &tmplData{
		Package:   pkg,
		Contracts: contracts,
		Structs:   structs,
	}
	buffer := new(bytes.Buffer)

	funcs := map[string]any{
		"bindtype":      bindType,
		"bindtopictype": bindTopicType,
		"capitalise":    abi.ToCamelCase,
		"decapitalise":  decapitalise,
	}
	tmpl := template.Must(template.New("").Funcs(funcs).Parse(tmplSource))
	if err := tmpl.Execute(buffer, data); err != nil {
		return "", err
	}
	// Pass the code through gofmt to clean it up
	code, err := format.Source(buffer.Bytes())
	if err != nil {
		return "", fmt.Errorf("%v\n%s", err, buffer)
	}
	return string(code), nil
}

func validateNoLegacyLibraryPlaceholders(contract string, bytecode string) error {
	if bytecode == "" {
		return nil
	}
	bin := strings.TrimPrefix(strings.TrimSpace(bytecode), "0x")
	placeholder := findLegacyLibraryPlaceholder(bin)
	if placeholder == "" {
		return nil
	}
	return fmt.Errorf("%w: contract %s bytecode contains %q; compile and link with Hyperion VM64 output or pass already-linked bytecode", ErrUnsupportedLibraryLinking, contract, placeholder)
}

func findLegacyLibraryPlaceholder(bytecode string) string {
	if placeholder := legacyHashLibraryPlaceholderRegex.FindString(bytecode); placeholder != "" {
		return placeholder
	}
	for i := 0; i+legacyLibraryPlaceholderLength <= len(bytecode); i++ {
		placeholder := bytecode[i : i+legacyLibraryPlaceholderLength]
		if isLegacyNameLibraryPlaceholder(placeholder) {
			return placeholder
		}
	}
	return ""
}

func isLegacyNameLibraryPlaceholder(placeholder string) bool {
	if len(placeholder) != legacyLibraryPlaceholderLength ||
		!strings.HasPrefix(placeholder, "__") ||
		strings.HasPrefix(placeholder, libraryPlaceholderPrefix) {
		return false
	}
	body := placeholder[len("__"):]
	lastNameChar := strings.LastIndexFunc(body, func(r rune) bool { return r != '_' })
	if lastNameChar < 0 || lastNameChar == len(body)-1 {
		return false
	}
	name := body[:lastNameChar+1]
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '_', r == '.', r == '/', r == ':', r == '-':
		default:
			return false
		}
	}
	return true
}

func resolveLibraryPlaceholders(contract string, bytecode string, libs map[string]string, linked map[string]string) error {
	if bytecode == "" {
		return nil
	}
	unresolved := make(map[string]struct{})
	for _, placeholder := range vm64LibraryPlaceholderRegex.FindAllString(bytecode, -1) {
		unresolved[placeholder] = struct{}{}
	}
	for pattern, name := range libs {
		if len(pattern) != libraryPlaceholderPatternHexLength {
			continue
		}
		placeholder := libraryPlaceholder(pattern)
		if strings.Contains(bytecode, placeholder) {
			linked[placeholder] = name
			delete(unresolved, placeholder)
		}
	}
	for placeholder := range unresolved {
		return fmt.Errorf("%w: contract %s bytecode contains %q but no matching library was provided", ErrUnresolvedLibraryLink, contract, placeholder)
	}
	return nil
}

func libraryPlaceholder(pattern string) string {
	return libraryPlaceholderPrefix + pattern + libraryPlaceholderSuffix
}

func validateNoFunctionTypes(contract string, qrvmABI abi.ABI) error {
	if err := validateNoFunctionTypeArgs(contract, "constructor input", qrvmABI.Constructor.Inputs); err != nil {
		return err
	}
	for _, method := range qrvmABI.Methods {
		name := method.RawName
		if name == "" {
			name = method.Name
		}
		if err := validateNoFunctionTypeArgs(contract+"."+name, "method input", method.Inputs); err != nil {
			return err
		}
		if err := validateNoFunctionTypeArgs(contract+"."+name, "method output", method.Outputs); err != nil {
			return err
		}
	}
	for _, event := range qrvmABI.Events {
		name := event.RawName
		if name == "" {
			name = event.Name
		}
		if err := validateNoFunctionTypeArgs(contract+"."+name, "event input", event.Inputs); err != nil {
			return err
		}
	}
	for _, errABI := range qrvmABI.Errors {
		if err := validateNoFunctionTypeArgs(contract+"."+errABI.Name, "error input", errABI.Inputs); err != nil {
			return err
		}
	}
	return nil
}

func validateNoFunctionTypeArgs(scope string, role string, args abi.Arguments) error {
	for i, arg := range args {
		if containsFunctionType(arg.Type) {
			name := arg.Name
			if name == "" {
				name = fmt.Sprintf("arg%d", i)
			}
			return fmt.Errorf("%w: %s %s %q uses ABI type %s", abi.ErrUnsupportedFunctionType, scope, role, name, arg.Type)
		}
	}
	return nil
}

func containsFunctionType(kind abi.Type) bool {
	switch kind.T {
	case abi.FunctionTy:
		return true
	case abi.ArrayTy, abi.SliceTy:
		return containsFunctionType(*kind.Elem)
	case abi.TupleTy:
		for _, elem := range kind.TupleElems {
			if containsFunctionType(*elem) {
				return true
			}
		}
	}
	return false
}

// bindBasicType converts basic hyperion types(except array, slice and tuple) to Go ones.
func bindBasicType(kind abi.Type) string {
	switch kind.T {
	case abi.AddressTy:
		return "common.Address"
	case abi.IntTy, abi.UintTy:
		parts := intRegex.FindStringSubmatch(kind.String())
		switch parts[2] {
		case "8", "16", "32", "64":
			return fmt.Sprintf("%sint%s", parts[1], parts[2])
		}
		return "*big.Int"
	case abi.FixedBytesTy:
		return fmt.Sprintf("[%d]byte", kind.Size)
	case abi.BytesTy:
		return "[]byte"
	case abi.FunctionTy:
		return "unsupportedFunctionType"
	default:
		// string, bool types
		return kind.String()
	}
}

// bindType converts hyperion types to Go ones. Since there is no clear mapping
// from all Hyperion types to Go ones (e.g. uint24), those that cannot be exactly
// mapped will use an upscaled type (e.g. BigDecimal).
func bindType(kind abi.Type, structs map[string]*tmplStruct) string {
	switch kind.T {
	case abi.TupleTy:
		return structs[kind.TupleRawName].Name
	case abi.ArrayTy:
		return fmt.Sprintf("[%d]", kind.Size) + bindType(*kind.Elem, structs)
	case abi.SliceTy:
		return "[]" + bindType(*kind.Elem, structs)
	default:
		return bindBasicType(kind)
	}
}

// bindTopicType converts a Hyperion topic type to the Go type reconstructed by
// abi.ParseTopics. Indexed hash-only values are not recoverable from logs; the
// generated event exposes the full 64-byte topic that contains their hash.
func bindTopicType(kind abi.Type, structs map[string]*tmplStruct) string {
	switch kind.T {
	case abi.StringTy, abi.BytesTy, abi.SliceTy, abi.ArrayTy, abi.TupleTy:
		return "common.LogTopic"
	default:
		return bindType(kind, structs)
	}
}

// bindStructType converts a Hyperion tuple type to a Go one and records the mapping
// in the given map. Notably, this function will resolve and record nested struct
// recursively.
func bindStructType(kind abi.Type, structs map[string]*tmplStruct) string {
	switch kind.T {
	case abi.TupleTy:
		id := kind.TupleRawName
		if s, exist := structs[id]; exist {
			return s.Name
		}
		var (
			names  = make(map[string]bool)
			fields []*tmplField
		)
		for i, elem := range kind.TupleElems {
			name := abi.ToCamelCase(kind.TupleRawNames[i])
			name = abi.ResolveNameConflict(name, func(s string) bool { return names[s] })
			names[name] = true
			fields = append(fields, &tmplField{
				Type:    bindStructType(*elem, structs),
				Name:    name,
				SolKind: *elem,
			})
		}
		name := kind.TupleRawName
		if name == "" {
			name = fmt.Sprintf("Struct%d", len(structs))
		}
		name = abi.ToCamelCase(name)

		structs[id] = &tmplStruct{
			Name:   name,
			Fields: fields,
		}
		return name
	case abi.ArrayTy:
		return fmt.Sprintf("[%d]", kind.Size) + bindStructType(*kind.Elem, structs)
	case abi.SliceTy:
		return "[]" + bindStructType(*kind.Elem, structs)
	default:
		return bindBasicType(kind)
	}
}

// alias returns an alias of the given string based on the aliasing rules
// or returns itself if no rule is matched.
func alias(aliases map[string]string, n string) string {
	if alias, exist := aliases[n]; exist {
		return alias
	}
	return n
}

// decapitalise makes a camel-case string which starts with a lower case character.
func decapitalise(input string) string {
	if len(input) == 0 {
		return input
	}
	goForm := abi.ToCamelCase(input)
	return strings.ToLower(goForm[:1]) + goForm[1:]
}

// structured checks whether a list of ABI data types has enough information to
// operate through a proper Go struct or if flat returns are needed.
func structured(args abi.Arguments) bool {
	if len(args) < 2 {
		return false
	}
	exists := make(map[string]bool)
	for _, out := range args {
		// If the name is anonymous, we can't organize into a struct
		if out.Name == "" {
			return false
		}
		// If the field name is empty when normalized or collides (var, Var, _var, _Var),
		// we can't organize into a struct
		field := abi.ToCamelCase(out.Name)
		if field == "" || exists[field] {
			return false
		}
		exists[field] = true
	}
	return true
}

// hasStruct returns an indicator whether the given type is struct, struct slice
// or struct array.
func hasStruct(t abi.Type) bool {
	switch t.T {
	case abi.SliceTy:
		return hasStruct(*t.Elem)
	case abi.ArrayTy:
		return hasStruct(*t.Elem)
	case abi.TupleTy:
		return true
	default:
		return false
	}
}
