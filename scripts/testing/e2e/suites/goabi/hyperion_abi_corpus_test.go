// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package goabi

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"

	qrlabi "github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
)

type hyperionCorpusEntry struct {
	Type            string                   `json:"type"`
	Name            string                   `json:"name"`
	StateMutability string                   `json:"stateMutability"`
	Anonymous       bool                     `json:"anonymous"`
	Inputs          []hyperionCorpusArgument `json:"inputs"`
	Outputs         []hyperionCorpusArgument `json:"outputs"`
}

type hyperionCorpusArgument struct {
	Name         string                   `json:"name"`
	Type         string                   `json:"type"`
	InternalType string                   `json:"internalType"`
	Indexed      bool                     `json:"indexed"`
	Components   []hyperionCorpusArgument `json:"components"`
}

type loadedHyperionCorpusFixture struct {
	file    string
	encoded []byte
	entries []hyperionCorpusEntry
	abi     qrlabi.ABI
}

func TestPortableHyperionABICorpusBindings(t *testing.T) {
	fixtures := loadHyperionABICorpus(t)
	var (
		bindingTypes []string
		bindingABIs  []string
		bytecodes    []string
	)
	for _, fixture := range fixtures {
		bindingTypes = append(bindingTypes, strings.TrimSuffix(fixture.file, ".abi"))
		bindingABIs = append(bindingABIs, string(fixture.encoded))
		bytecodes = append(bytecodes, "")
	}

	// Generate all wrappers together. This catches parser incompatibilities as
	// well as cross-contract tuple and top-level identifier collisions.
	code, err := bind.Bind(bindingTypes, bindingABIs, bytecodes, nil, "hyperioncorpus", nil, nil)
	if err != nil {
		t.Fatalf("generate bindings for pinned Hyperion ABI corpus: %v", err)
	}
	fileSet := token.NewFileSet()
	generated, err := parser.ParseFile(fileSet, "hyperion_corpus_binding.go", code, parser.AllErrors|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("generated Hyperion corpus binding is invalid Go: %v\n%s", err, code)
	}
	if diagnostics := generatedCorpusDeclarationErrors(fileSet, generated); len(diagnostics) != 0 {
		t.Fatalf("generated Hyperion corpus binding has semantic declaration collisions:\n%s", strings.Join(diagnostics, "\n"))
	}
	for _, symbol := range []string{
		"FilterE(",
		"FilterE0(",
		"Fallback",
		"Receive",
		"type LS struct",
		"type LT struct",
	} {
		if !strings.Contains(code, symbol) {
			t.Fatalf("generated Hyperion corpus binding is missing %q", symbol)
		}
	}
}

type corpusImportStub struct{}

func (corpusImportStub) Import(path string) (*types.Package, error) {
	name := strings.ReplaceAll(filepath.Base(path), "-", "_")
	return types.NewPackage(path, name), nil
}

// generatedCorpusDeclarationErrors performs semantic declaration checking
// while ignoring function bodies and imported APIs. Syntax parsing alone does
// not reject duplicate top-level declarations, struct fields, parameters, or
// field/method collisions produced when multiple ABI contracts share a package.
func generatedCorpusDeclarationErrors(fileSet *token.FileSet, file *ast.File) []string {
	var diagnostics []string
	config := types.Config{
		Importer:         corpusImportStub{},
		IgnoreFuncBodies: true,
		Error: func(err error) {
			message := err.Error()
			for _, marker := range []string{"redeclared", "already declared", "field and method with the same name"} {
				if strings.Contains(message, marker) {
					diagnostics = append(diagnostics, message)
					break
				}
			}
		},
	}
	_, _ = config.Check(file.Name.Name, fileSet, []*ast.File{file}, nil)
	return diagnostics
}

func TestPortableHyperionCorpusSemanticCheckerDetectsCollisions(t *testing.T) {
	fileSet := token.NewFileSet()
	file, err := parser.ParseFile(fileSet, "collision.go", `package collision
		type Record struct { Value int; Value string }
		func duplicate(value int, value string) {}`, parser.AllErrors|parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("semantic collision fixture must remain syntactically valid: %v", err)
	}
	if diagnostics := generatedCorpusDeclarationErrors(fileSet, file); len(diagnostics) < 2 {
		t.Fatalf("semantic checker missed generated declaration collisions: %v", diagnostics)
	}
}

func TestPortableHyperionABICorpusSemantics(t *testing.T) {
	fixtures := loadHyperionABICorpus(t)
	byFile := make(map[string]loadedHyperionCorpusFixture, len(fixtures))
	for _, fixture := range fixtures {
		byFile[fixture.file] = fixture
	}

	udvt := mustHyperionCorpusFixture(t, byFile, "user_defined_value_type.abi")
	assertMethodSignature(t, udvt.abi, "setMyAddress", "setMyAddress(address)")
	assertMethodSignature(t, udvt.abi, "setMyByte1", "setMyByte1(bytes1)")
	assertMethodSignature(t, udvt.abi, "setMyBytes32", "setMyBytes32(bytes32)")
	assertMethodSignature(t, udvt.abi, "setMyInt", "setMyInt(int256)")
	assertMethodSignature(t, udvt.abi, "setMyUInt8", "setMyUInt8(uint8)")
	assertRawArgument(t, udvt.entries, "function", "setMyAddress", true, 0, "C.MyAddress", "address")
	assertRawArgument(t, udvt.entries, "function", "setMyInt", true, 0, "MyInt", "int256")

	enumFixture := mustHyperionCorpusFixture(t, byFile, "enum_return.abi")
	if got := enumFixture.abi.Constructor.Inputs[0].Type.String(); got != "uint8" {
		t.Fatalf("enum constructor type = %q, want uint8", got)
	}
	if got := enumFixture.abi.Methods["ret"].Outputs[0].Type.String(); got != "uint8" {
		t.Fatalf("enum return type = %q, want uint8", got)
	}
	assertRawArgument(t, enumFixture.entries, "constructor", "", true, 0, "enum test.ActionChoices", "uint8")

	contractTypes := mustHyperionCorpusFixture(t, byFile, "contract_types.abi")
	contractOutput := contractTypes.abi.Methods["f"].Outputs
	if got := contractOutput[0].Type.String(); got != "(address[],address)" {
		t.Fatalf("contract struct lowering = %q, want (address[],address)", got)
	}
	if got := contractOutput[0].Type.TupleRawName; got != "CS" {
		t.Fatalf("contract struct tuple name = %q, want CS", got)
	}
	if got := contractOutput[1].Type.String(); got != "address" {
		t.Fatalf("contract return lowering = %q, want address", got)
	}
	assertRawArgument(t, contractTypes.entries, "function", "f", false, 1, "contract C", "address")

	addressPayable := mustHyperionCorpusFixture(t, byFile, "address_payable_lowering.abi")
	assertMethodSignature(t, addressPayable.abi, "m", "m(address)")
	assertMethodSignature(t, addressPayable.abi, "f", "f(address)")
	assertRawArgument(t, addressPayable.entries, "function", "f", true, 0, "address payable", "address")
	assertRawArgument(t, addressPayable.entries, "function", "f", false, 0, "address payable", "address")

	globalStruct := mustHyperionCorpusFixture(t, byFile, "global_struct.abi")
	assertMethodSignature(t, globalStruct.abi, "f", "f((uint256))")
	assertMethodSignature(t, globalStruct.abi, "g", "g((uint256))")
	for _, name := range []string{"f", "g"} {
		if got := globalStruct.abi.Methods[name].Inputs[0].Type.TupleRawName; got != "S" {
			t.Fatalf("global struct %s tuple name = %q, want S", name, got)
		}
	}

	nestedStruct := mustHyperionCorpusFixture(t, byFile, "nested_struct.abi")
	assertMethodSignature(t, nestedStruct.abi, "g", "g((uint256,(uint256[2])[],bytes))")
	nested := nestedStruct.abi.Methods["g"].Inputs[0].Type
	if nested.TupleRawName != "LS" || len(nested.TupleElems) != 3 {
		t.Fatalf("nested outer tuple = name %q fields %d, want LS/3", nested.TupleRawName, len(nested.TupleElems))
	}
	if inner := nested.TupleElems[1].Elem; inner == nil || inner.TupleRawName != "LT" || inner.String() != "(uint256[2])" {
		t.Fatalf("nested inner tuple = %#v, want LT(uint256[2])", inner)
	}

	mappings := mustHyperionCorpusFixture(t, byFile, "mapping_getters.abi")
	for name, signature := range map[string]string{
		"allowance": "allowance(address,address)",
		"commits":   "commits(bytes32)",
		"something": "something(bytes32)",
	} {
		assertMethodSignature(t, mappings.abi, name, signature)
		if !mappings.abi.Methods[name].IsConstant() {
			t.Fatalf("mapping getter %s is not view", name)
		}
	}

	events := mustHyperionCorpusFixture(t, byFile, "duplicate_events.abi").abi.Events
	if len(events) != 2 {
		t.Fatalf("duplicate event count = %d, want 2", len(events))
	}
	first, firstOK := events["e"]
	second, secondOK := events["e0"]
	if !firstOK || !secondOK || first.RawName != "e" || second.RawName != "e" ||
		first.Sig != "e()" || second.Sig != "e()" || first.ID != second.ID {
		t.Fatalf("duplicate events were not preserved deterministically: e=%+v e0=%+v", first, second)
	}

	errorsABI := mustHyperionCorpusFixture(t, byFile, "referenced_errors.abi").abi.Errors
	if len(errorsABI) != 4 {
		t.Fatalf("referenced error count = %d, want 4", len(errorsABI))
	}
	for key, signature := range map[string]string{
		"E1":  "E1()",
		"E2":  "E2()",
		"E3":  "E3(uint256)",
		"E30": "E3()",
	} {
		if got, ok := errorsABI[key]; !ok || got.Sig != signature {
			t.Fatalf("error %s = %+v, want signature %s", key, got, signature)
		}
	}

	constructorFixture := mustHyperionCorpusFixture(t, byFile, "payable_constructor.abi")
	constructor := constructorFixture.abi.Constructor
	if !constructor.IsPayable() || len(constructor.Inputs) != 3 {
		t.Fatalf("payable constructor = payable %t inputs %d, want true/3", constructor.IsPayable(), len(constructor.Inputs))
	}
	if got := constructor.Inputs[1].Type.String(); got != "address" {
		t.Fatalf("constructor contract parameter lowering = %q, want address", got)
	}
	assertRawArgument(t, constructorFixture.entries, "constructor", "", true, 1, "contract test", "address")

	special := mustHyperionCorpusFixture(t, byFile, "special_entrypoints.abi").abi
	if !special.HasFallback() || !special.HasReceive() {
		t.Fatalf("special entrypoints = fallback %t receive %t, want both", special.HasFallback(), special.HasReceive())
	}
	if special.Fallback.IsPayable() || !special.Receive.IsPayable() {
		t.Fatalf("special entrypoint mutability = fallback payable %t receive payable %t, want false/true",
			special.Fallback.IsPayable(), special.Receive.IsPayable())
	}
}

func loadHyperionABICorpus(t *testing.T) []loadedHyperionCorpusFixture {
	t.Helper()
	fixtureDir := hyperionABICorpusDir()
	paths, err := filepath.Glob(filepath.Join(fixtureDir, "*.abi"))
	if err != nil {
		t.Fatalf("enumerate Hyperion ABI fixtures: %v", err)
	}
	if len(paths) == 0 {
		t.Fatal("Hyperion ABI corpus has no fixtures")
	}

	fixtures := make([]loadedHyperionCorpusFixture, 0, len(paths))
	for _, path := range paths {
		file := filepath.Base(path)
		encoded, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read Hyperion ABI fixture %s: %v", file, err)
		}
		parsed, err := qrlabi.JSON(strings.NewReader(string(encoded)))
		if err != nil {
			t.Fatalf("parse Hyperion ABI fixture %s: %v", file, err)
		}
		var entries []hyperionCorpusEntry
		if err := json.Unmarshal(encoded, &entries); err != nil {
			t.Fatalf("decode raw Hyperion ABI fixture %s: %v", file, err)
		}
		fixtures = append(fixtures, loadedHyperionCorpusFixture{
			file: file, encoded: encoded, entries: entries, abi: parsed,
		})
	}
	return fixtures
}

func hyperionABICorpusDir() string {
	return filepath.Join("..", "..", "testdata", "contracts", "hyperion_abi_corpus")
}

func mustHyperionCorpusFixture(
	t *testing.T,
	fixtures map[string]loadedHyperionCorpusFixture,
	file string,
) loadedHyperionCorpusFixture {
	t.Helper()
	fixture, ok := fixtures[file]
	if !ok {
		t.Fatalf("Hyperion ABI corpus is missing %s", file)
	}
	return fixture
}

func assertMethodSignature(t *testing.T, parsed qrlabi.ABI, name, want string) {
	t.Helper()
	method, ok := parsed.Methods[name]
	if !ok {
		t.Fatalf("method %s is missing", name)
	}
	if method.Sig != want {
		t.Fatalf("method %s signature = %q, want %q", name, method.Sig, want)
	}
}

func assertRawArgument(
	t *testing.T,
	entries []hyperionCorpusEntry,
	entryType, entryName string,
	input bool,
	index int,
	internalType, abiType string,
) {
	t.Helper()
	for _, entry := range entries {
		if entry.Type != entryType || entry.Name != entryName {
			continue
		}
		arguments := entry.Outputs
		kind := "output"
		if input {
			arguments = entry.Inputs
			kind = "input"
		}
		if index >= len(arguments) {
			t.Fatalf("%s %s %s index %d is out of range", entryType, entryName, kind, index)
		}
		if got := arguments[index]; got.InternalType != internalType || got.Type != abiType {
			t.Fatalf("%s %s %s %d = internalType %q type %q, want %q/%q",
				entryType, entryName, kind, index, got.InternalType, got.Type, internalType, abiType)
		}
		return
	}
	t.Fatalf("ABI entry type=%q name=%q is missing", entryType, entryName)
}
