// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package core_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/accounts"
	"github.com/theQRL/go-qrl/accounts/keystore"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/crypto/pqcrypto"
	"github.com/theQRL/go-qrl/event"
	"github.com/theQRL/go-qrl/rpc"
	"github.com/theQRL/go-qrl/signer/core"
	"github.com/theQRL/go-qrl/signer/core/apitypes"
	"github.com/theQRL/go-qrl/signer/fourbyte"
	"github.com/theQRL/go-qrl/signer/storage"
)

const (
	typedDataAddressA  = "Q0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f40"
	typedDataAddressB  = "Q403f3e3d3c3b3a393837363534333231302f2e2d2c2b2a292827262524232221201f1e1d1c1b1a191817161514131211100f0e0d0c0b0a090807060504030201"
	typedDataContract  = "Q11111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111"
	typedDataContract2 = "Q22222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222222"
	typedDataSalt      = "0x000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
)

type metadataLessWallet struct {
	accounts.Wallet
}

type staticWalletBackend struct {
	wallets []accounts.Wallet
}

func (backend *staticWalletBackend) Wallets() []accounts.Wallet {
	return backend.wallets
}

func (backend *staticWalletBackend) Subscribe(chan<- accounts.WalletEvent) event.Subscription {
	return event.NewSubscription(func(quit <-chan struct{}) error {
		<-quit
		return nil
	})
}

func qrlTypedDataFixture() apitypes.TypedData {
	return apitypes.TypedData{
		Types: apitypes.Types{
			apitypes.TypedDataDomainType: {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
				{Name: "salt", Type: "bytes32"},
			},
			"Approval": {
				{Name: "signer", Type: "address"},
				{Name: "approved", Type: "bool"},
			},
			"Transfer": {
				{Name: "from", Type: "address"},
				{Name: "to", Type: "address"},
				{Name: "amount256", Type: "uint256"},
				{Name: "amount512", Type: "uint512"},
				{Name: "adjustment", Type: "int512"},
				{Name: "reference", Type: "bytes32"},
				{Name: "fixedPayload", Type: "bytes64"},
				{Name: "data", Type: "bytes"},
				{Name: "memo", Type: "string"},
				{Name: "tags", Type: "string[]"},
				{Name: "approvals", Type: "Approval[2]"},
				{Name: "nonce", Type: "uint64"},
				{Name: "deadline", Type: "uint64"},
			},
		},
		PrimaryType: "Transfer",
		Domain: apitypes.TypedDataDomain{
			Name:              "QRL Wallet",
			Version:           "1",
			ChainId:           math.NewHexOrDecimal256(1337),
			VerifyingContract: typedDataContract,
			Salt:              typedDataSalt,
		},
		Message: apitypes.TypedDataMessage{
			"from":         typedDataAddressA,
			"to":           typedDataAddressB,
			"amount256":    "0xffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
			"amount512":    "0x" + strings.Repeat("f", 128),
			"adjustment":   "-1",
			"reference":    "0x" + strings.Repeat("ab", 32),
			"fixedPayload": "0x" + strings.Repeat("cd", 64),
			"data":         "0x0102030405",
			"memo":         "VM64 transfer",
			"tags":         []any{"wallet", "approval"},
			"approvals": []any{
				map[string]any{"signer": typedDataAddressA, "approved": true},
				map[string]any{"signer": typedDataAddressB, "approved": false},
			},
			"nonce":    "42",
			"deadline": "2000000000",
		},
	}
}

func TestSignData(t *testing.T) {
	t.Parallel()
	api, control := setup(t)
	createAccount(control, api, t)
	control.approveCh <- "A"
	list, err := api.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	address := common.NewMixedcaseAddress(list[0])

	control.approveCh <- "Y"
	control.inputCh <- "wrongpassword"
	signature, err := api.SignData(t.Context(), apitypes.TextPlain.Mime, address, hexutil.Encode([]byte("EHLO world")))
	if signature != nil || !errors.Is(err, keystore.ErrDecrypt) {
		t.Fatalf("wrong password: signature %x, error %v", signature, err)
	}

	control.approveCh <- "No way"
	signature, err = api.SignData(t.Context(), apitypes.TextPlain.Mime, address, hexutil.Encode([]byte("EHLO world")))
	if signature != nil || !errors.Is(err, core.ErrRequestDenied) {
		t.Fatalf("denied request: signature %x, error %v", signature, err)
	}

	control.approveCh <- "Y"
	control.inputCh <- "a_long_password"
	signature, err = api.SignData(t.Context(), apitypes.TextPlain.Mime, address, hexutil.Encode([]byte("EHLO world")))
	if err != nil {
		t.Fatal(err)
	}
	if len(signature) != pqcrypto.MLDSA87SignatureLength {
		t.Fatalf("signature length %d, want %d", len(signature), pqcrypto.MLDSA87SignatureLength)
	}
}

func TestSignQRLTypedData(t *testing.T) {
	t.Parallel()
	api, control := setup(t)
	createAccount(control, api, t)
	control.approveCh <- "A"
	accountsList, err := api.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	address := common.NewMixedcaseAddress(accountsList[0])
	typedData := qrlTypedDataFixture()

	control.approveCh <- "Y"
	control.inputCh <- "a_long_password"
	result, err := api.SignTypedData(t.Context(), address, typedData)
	if err != nil {
		t.Fatal(err)
	}
	if result.Address != address.Address() {
		t.Fatalf("signed address %s, want %s", result.Address, address.Address())
	}
	if len(result.Signature) != pqcrypto.MLDSA87SignatureLength ||
		len(result.PublicKey) != pqcrypto.MLDSA87PublicKeyLength ||
		len(result.Descriptor) != pqcrypto.DescriptorSize {
		t.Fatalf("invalid envelope lengths: signature=%d publicKey=%d descriptor=%d",
			len(result.Signature), len(result.PublicKey), len(result.Descriptor))
	}
	if err := result.Verify(typedData); err != nil {
		t.Fatalf("verify signed envelope: %v", err)
	}
	encodedResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var decodedResult apitypes.TypedDataSignature
	if err := json.Unmarshal(encodedResult, &decodedResult); err != nil {
		t.Fatal(err)
	}
	if err := decodedResult.Verify(typedData); err != nil {
		t.Fatalf("verify JSON round-trip envelope: %v", err)
	}
	if control.lastSignDataRequest.ContentType != accounts.MimetypeTypedData {
		t.Fatalf("content type %q, want %q", control.lastSignDataRequest.ContentType, accounts.MimetypeTypedData)
	}
	if !bytes.Equal(result.Digest[:], control.lastSignDataRequest.Hash) {
		t.Fatalf("envelope digest %x, UI digest %x", result.Digest, control.lastSignDataRequest.Hash)
	}
	tampered := qrlTypedDataFixture()
	tampered.Message["nonce"] = "43"
	if err := result.Verify(tampered); err == nil {
		t.Fatal("tampered message verified")
	}
	wrongAddress := *result
	wrongAddress.Address = common.MustParseAddress(typedDataAddressA)
	if wrongAddress.Address == result.Address {
		wrongAddress.Address[0] ^= 0xff
	}
	if err := wrongAddress.Verify(typedData); err == nil {
		t.Fatal("public key/address mismatch verified")
	}
	badSignature := *result
	badSignature.Signature = append(hexutil.Bytes(nil), result.Signature...)
	badSignature.Signature[0] ^= 0xff
	if err := badSignature.Verify(typedData); err == nil {
		t.Fatal("corrupted ML-DSA signature verified")
	}
}

func TestSignQRLTypedDataJSONRPC(t *testing.T) {
	t.Parallel()
	api, control := setup(t)
	createAccount(control, api, t)
	control.approveCh <- "A"
	accountsList, err := api.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	address := common.NewMixedcaseAddress(accountsList[0])
	typedData := qrlTypedDataFixture()

	server := rpc.NewServer()
	t.Cleanup(server.Stop)
	if err := server.RegisterName("account", api); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)

	control.approveCh <- "Y"
	control.inputCh <- "a_long_password"
	var result apitypes.TypedDataSignature
	if err := client.CallContext(t.Context(), &result, "account_signTypedData", address, typedData); err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(typedData); err != nil {
		t.Fatalf("verify RPC signing result: %v", err)
	}
}

func TestSignQRLTypedDataRejectsWalletWithoutMetadata(t *testing.T) {
	t.Parallel()
	password := "a_long_password"
	keyStore := keystore.NewKeyStore(
		tmpDirName(t),
		keystore.LightArgon2idT,
		keystore.LightArgon2idM,
		keystore.LightArgon2idP,
	)
	account, err := keyStore.NewAccount(password)
	if err != nil {
		t.Fatal(err)
	}
	wallets := keyStore.Wallets()
	if len(wallets) != 1 {
		t.Fatalf("wallet count %d, want 1", len(wallets))
	}
	backend := &staticWalletBackend{wallets: []accounts.Wallet{metadataLessWallet{Wallet: wallets[0]}}}
	manager := accounts.NewManager(backend)
	t.Cleanup(func() {
		if err := manager.Close(); err != nil {
			t.Errorf("close account manager: %v", err)
		}
	})
	db, err := fourbyte.New()
	if err != nil {
		t.Fatal(err)
	}
	control := &headlessUi{
		approveCh: make(chan string, 1),
		inputCh:   make(chan string, 1),
	}
	api := core.NewSignerAPI(manager, 1337, control, db, true, &storage.NoStorage{})
	control.approveCh <- "Y"
	control.inputCh <- password
	result, err := api.SignTypedData(
		t.Context(),
		common.NewMixedcaseAddress(account.Address),
		qrlTypedDataFixture(),
	)
	if result != nil {
		t.Fatalf("signing result %v, want nil", result)
	}
	if err == nil || !strings.Contains(err.Error(), "wallet cannot provide typed data verification metadata") {
		t.Fatalf("error %v, want unsupported wallet metadata error", err)
	}
}

func TestTypedDataRequiresDedicatedAPI(t *testing.T) {
	t.Parallel()
	api, _ := setup(t)
	address := common.NewMixedcaseAddress(common.Address{})
	result, err := api.SignData(t.Context(), accounts.MimetypeTypedData, address, "0x00")
	if result != nil || !errors.Is(err, core.ErrTypedDataRequiresDedicatedAPI) {
		t.Fatalf("result %x, error %v", result, err)
	}
}

func TestTypedDataSignerChainID(t *testing.T) {
	t.Parallel()
	api, _ := setup(t)
	typedData := qrlTypedDataFixture()
	typedData.Domain.ChainId = math.NewHexOrDecimal256(1)
	_, err := api.SignTypedData(t.Context(), common.NewMixedcaseAddress(common.Address{}), typedData)
	if err == nil || !strings.Contains(err.Error(), "chainId") {
		t.Fatalf("expected chain ID mismatch, got %v", err)
	}
}

func TestTypedDataSignerWideChainID(t *testing.T) {
	t.Parallel()
	api, control := setup(t)
	createAccount(control, api, t)
	control.approveCh <- "A"
	accountsList, err := api.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	wideChainID := new(big.Int).Lsh(big.NewInt(1), 200)
	encodedChainID := math.HexOrDecimal256(*wideChainID)
	if _, err := core.NewUIServerAPI(api).SetChainId(encodedChainID); err != nil {
		t.Fatal(err)
	}
	typedData := qrlTypedDataFixture()
	typedData.Domain.ChainId = &encodedChainID
	control.approveCh <- "Y"
	control.inputCh <- "a_long_password"
	result, err := api.SignTypedData(t.Context(), common.NewMixedcaseAddress(accountsList[0]), typedData)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Verify(typedData); err != nil {
		t.Fatalf("verify wide-chain-ID signature: %v", err)
	}
}

func TestQRLTypedDataGolden(t *testing.T) {
	t.Parallel()
	encoded, err := os.ReadFile("testdata/qrl_typed_data_v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var vector struct {
		TypedData apitypes.TypedData `json:"typedData"`
		Expected  struct {
			DomainType  string `json:"domainType"`
			MessageType string `json:"messageType"`
			TypeHash    string `json:"typeHash"`
			DomainHash  string `json:"domainHash"`
			MessageHash string `json:"messageHash"`
			Digest      string `json:"digest"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(encoded, &vector); err != nil {
		t.Fatal(err)
	}
	typedData := vector.TypedData
	if got := string(typedData.EncodeType(apitypes.TypedDataDomainType)); got != vector.Expected.DomainType {
		t.Errorf("domain type:\n have %s\n want %s", got, vector.Expected.DomainType)
	}
	if got := string(typedData.EncodeType(typedData.PrimaryType)); got != vector.Expected.MessageType {
		t.Errorf("message type:\n have %s\n want %s", got, vector.Expected.MessageType)
	}
	if got := hexutil.Encode(typedData.TypeHash(typedData.PrimaryType)); got != vector.Expected.TypeHash {
		t.Errorf("type hash: have %s, want %s", got, vector.Expected.TypeHash)
	}
	domainHash, err := typedData.HashStruct(apitypes.TypedDataDomainType, typedData.Domain.Map())
	if err != nil {
		t.Fatal(err)
	}
	if got := hexutil.Encode(domainHash); got != vector.Expected.DomainHash {
		t.Errorf("domain hash: have %s, want %s", got, vector.Expected.DomainHash)
	}
	messageHash, err := typedData.HashStruct(typedData.PrimaryType, typedData.Message)
	if err != nil {
		t.Fatal(err)
	}
	if got := hexutil.Encode(messageHash); got != vector.Expected.MessageHash {
		t.Errorf("message hash: have %s, want %s", got, vector.Expected.MessageHash)
	}
	digest, rawData, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		t.Fatal(err)
	}
	if got := hexutil.Encode(digest); got != vector.Expected.Digest {
		t.Errorf("digest: have %s, want %s", got, vector.Expected.Digest)
	}
	if !strings.HasPrefix(rawData, apitypes.TypedDataPrefix) {
		t.Fatalf("raw preimage does not start with %q", apitypes.TypedDataPrefix)
	}
	if len(rawData) != len(apitypes.TypedDataPrefix)+2*common.HashLength {
		t.Fatalf("raw preimage length %d", len(rawData))
	}
}

func TestQRLTypedDataDomainSeparation(t *testing.T) {
	t.Parallel()
	base := qrlTypedDataFixture()
	baseDigest, _, err := apitypes.TypedDataAndHash(base)
	if err != nil {
		t.Fatal(err)
	}
	mutations := []func(*apitypes.TypedData){
		func(data *apitypes.TypedData) { data.Domain.ChainId = math.NewHexOrDecimal256(1338) },
		func(data *apitypes.TypedData) { data.Domain.VerifyingContract = typedDataContract2 },
		func(data *apitypes.TypedData) { data.Message["nonce"] = "43" },
		func(data *apitypes.TypedData) { data.Message["deadline"] = "2000000001" },
	}
	for index, mutate := range mutations {
		candidate := qrlTypedDataFixture()
		mutate(&candidate)
		digest, _, err := apitypes.TypedDataAndHash(candidate)
		if err != nil {
			t.Fatalf("mutation %d: %v", index, err)
		}
		if bytes.Equal(baseDigest, digest) {
			t.Errorf("mutation %d did not change digest", index)
		}
	}
}

func TestQRLTypedDataApprovalFormatting(t *testing.T) {
	t.Parallel()
	typedData := qrlTypedDataFixture()
	formatted, err := typedData.Format()
	if err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	for _, value := range formatted {
		output.WriteString(value.Pprint(0))
	}
	text := output.String()
	for _, expected := range []string{
		"QRLTypedDataDomain [domain]",
		"Transfer [primary type]",
		"tags [string[]]",
		"[0]: \"wallet\"",
		"approvals [Approval[2]]",
		"signer [address]",
	} {
		if !strings.Contains(text, expected) {
			t.Errorf("approval output does not contain %q:\n%s", expected, text)
		}
	}
	if strings.Contains(text, "%!") {
		t.Fatalf("approval output contains a formatting error:\n%s", text)
	}
}

func TestQRLTypedDataJSONRoundTrip(t *testing.T) {
	t.Parallel()
	original := qrlTypedDataFixture()
	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var decoded apitypes.TypedData
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	originalDigest, _, err := apitypes.TypedDataAndHash(original)
	if err != nil {
		t.Fatal(err)
	}
	decodedDigest, _, err := apitypes.TypedDataAndHash(decoded)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalDigest, decodedDigest) {
		t.Fatalf("round-trip digest %x, want %x", decodedDigest, originalDigest)
	}
	if err := json.Unmarshal(append(encoded, []byte(` {}`)...), &decoded); err == nil {
		t.Fatal("multiple JSON values accepted")
	}
}

func TestQRLTypedDataJSONFixtures(t *testing.T) {
	t.Parallel()
	fixtures := []struct {
		name      string
		wantError string
	}{
		{name: "arrays-1.json"},
		{name: "custom_arraytype.json"},
		{name: "eip712.json"},
		{name: "expfail_arraytype_overload.json", wantError: "Person[]"},
		{name: "expfail_datamismatch_1.json", wantError: "contents"},
		{name: "expfail_extradata.json", wantError: "exactly 3 fields"},
		{name: "expfail_malformeddomainkeys.json", wantError: "domain"},
		{name: "expfail_nonexistant_type.json", wantError: "Blahonga"},
		{name: "expfail_nonexistant_type2.json", wantError: "chainId"},
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

func TestQRLTypedDataValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		mutate func(*apitypes.TypedData)
	}{
		{
			name: "missing domain salt",
			mutate: func(data *apitypes.TypedData) {
				data.Domain.Salt = ""
			},
		},
		{
			name: "wrong domain field order",
			mutate: func(data *apitypes.TypedData) {
				fields := data.Types[apitypes.TypedDataDomainType]
				fields[0], fields[1] = fields[1], fields[0]
			},
		},
		{
			name: "bare integer",
			mutate: func(data *apitypes.TypedData) {
				data.Types["Transfer"][2].Type = "uint"
			},
		},
		{
			name: "over-wide integer type",
			mutate: func(data *apitypes.TypedData) {
				data.Types["Transfer"][3].Type = "uint520"
			},
		},
		{
			name: "function type",
			mutate: func(data *apitypes.TypedData) {
				data.Types["Transfer"][8].Type = "function"
			},
		},
		{
			name: "recursive type",
			mutate: func(data *apitypes.TypedData) {
				data.Types["Node"] = []apitypes.Type{{Name: "next", Type: "Node"}}
			},
		},
		{
			name: "undefined type",
			mutate: func(data *apitypes.TypedData) {
				data.Types["Transfer"][8].Type = "Undefined"
			},
		},
		{
			name: "duplicate field",
			mutate: func(data *apitypes.TypedData) {
				data.Types["Transfer"][1].Name = "from"
			},
		},
		{
			name: "extra message field",
			mutate: func(data *apitypes.TypedData) {
				data.Message["unexpected"] = true
			},
		},
		{
			name: "missing message field",
			mutate: func(data *apitypes.TypedData) {
				delete(data.Message, "nonce")
			},
		},
		{
			name: "wrong static array length",
			mutate: func(data *apitypes.TypedData) {
				data.Message["approvals"] = []any{map[string]any{"signer": typedDataAddressA, "approved": true}}
			},
		},
		{
			name: "uint256 overflow",
			mutate: func(data *apitypes.TypedData) {
				data.Message["amount256"] = "0x1" + strings.Repeat("0", 64)
			},
		},
		{
			name: "invalid bytes64 length",
			mutate: func(data *apitypes.TypedData) {
				data.Message["fixedPayload"] = "0x01"
			},
		},
		{
			name: "negative chain ID",
			mutate: func(data *apitypes.TypedData) {
				data.Domain.ChainId = math.NewHexOrDecimal256(-1)
			},
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			data := qrlTypedDataFixture()
			test.mutate(&data)
			if _, _, err := apitypes.TypedDataAndHash(data); err == nil {
				t.Fatal("invalid typed data was accepted")
			}
		})
	}
}
