// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package core_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/signer/core/apitypes"
)

func TestQRLTypedDataJSONFixtures(t *testing.T) {
	t.Parallel()
	fixtures := []struct {
		name      string
		wantError string
	}{
		{name: "arrays-1.json"},
		{name: "custom_arraytype.json"},
		{name: "eip712.json"},
		{name: "expfail_arraytype_overload.json"},
		{name: "expfail_datamismatch_1.json", wantError: "doesn't match type 'Person'"},
		{name: "expfail_extradata.json", wantError: "extra data"},
		{name: "expfail_malformeddomainkeys.json", wantError: "domain"},
		{name: "expfail_nonexistant_type.json", wantError: "Blahonga"},
		{name: "expfail_nonexistant_type2.json", wantError: "uint256 ..."},
		{name: "expfail_toolargeuint.json", wantError: "uint8"},
		{name: "expfail_toolargeuint2.json", wantError: "uint8"},
		{name: "expfail_unconvertiblefloat.json", wantError: "uint8"},
		{name: "expfail_unconvertiblefloat2.json", wantError: "uint8"},
	}
	for _, fixture := range fixtures {
		fixture := fixture
		t.Run(fixture.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := os.ReadFile(filepath.Join("testdata", fixture.name))
			if err != nil {
				t.Fatal(err)
			}
			var typedData apitypes.TypedData
			err = json.Unmarshal(encoded, &typedData)
			if err == nil {
				_, _, err = apitypes.TypedDataAndHash(typedData)
			}
			if fixture.wantError != "" {
				if err == nil {
					t.Fatal("invalid fixture was accepted")
				}
				if !strings.Contains(err.Error(), fixture.wantError) {
					t.Fatalf("error %q does not contain %q", err, fixture.wantError)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			canonical, err := json.Marshal(typedData)
			if err != nil {
				t.Fatal(err)
			}
			var roundTrip apitypes.TypedData
			if err := json.Unmarshal(canonical, &roundTrip); err != nil {
				t.Fatal(err)
			}
			originalDigest, _, err := apitypes.TypedDataAndHash(typedData)
			if err != nil {
				t.Fatal(err)
			}
			roundTripDigest, _, err := apitypes.TypedDataAndHash(roundTrip)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(originalDigest, roundTripDigest) {
				t.Fatalf("round-trip digest %x, want %x", roundTripDigest, originalDigest)
			}
		})
	}
}

func TestTypedDataFuzzRegressionCorpus(t *testing.T) {
	t.Parallel()
	valid := map[string]bool{
		"36fb987a774011dc675e1b5246ac5c1d44d84d92": true,
		"f658340af009dd4a35abe645a00a7b732bc30921": true,
	}
	directory := filepath.Join("testdata", "fuzzing")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		entry := entry
		t.Run(entry.Name(), func(t *testing.T) {
			t.Parallel()
			encoded, err := os.ReadFile(filepath.Join(directory, entry.Name()))
			if err != nil {
				t.Fatal(err)
			}
			var typedData apitypes.TypedData
			if err := json.Unmarshal(encoded, &typedData); err != nil {
				if valid[entry.Name()] {
					t.Fatalf("valid corpus entry did not decode: %v", err)
				}
				return
			}
			_, _, hashErr := apitypes.TypedDataAndHash(typedData)
			_, formatErr := typedData.Format()
			if valid[entry.Name()] {
				if hashErr != nil {
					t.Fatalf("valid corpus entry did not hash: %v", hashErr)
				}
				if formatErr != nil {
					t.Fatalf("valid corpus entry did not format: %v", formatErr)
				}
				return
			}
			if hashErr == nil {
				t.Fatal("invalid corpus entry was accepted")
			}
		})
	}
}
