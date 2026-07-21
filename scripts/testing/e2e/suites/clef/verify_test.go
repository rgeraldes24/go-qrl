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

package clef

import (
	"encoding/json"
	"math/big"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/accounts"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/signer/core/apitypes"
)

const testSeed = "010000f29f58aff0b00de2844f7e20bd9eeaacc379150043beeb328335817512b29fbb7184da84a092f842b2a06d72a24a5d28"

type testFixture struct {
	input    ScenarioInput
	wallet   wallet.Wallet
	typed    apitypes.TypedData
	signedTx *types.Transaction
}

func TestVerifyScenario(t *testing.T) {
	fixture := newTestFixture(t)
	if err := VerifyScenario(fixture.input); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyScenarioRejectsCryptographicAndVM64Drift(t *testing.T) {
	fixture := newTestFixture(t)

	t.Run("data signature tamper", func(t *testing.T) {
		input := fixture.input
		sig := decodeTestSignature(t, input.DataResponse, 3)
		sig[len(sig)-1] ^= 0x01
		input.DataResponse = rpcResult(t, 3, hexutil.Bytes(sig))
		requireVerifyError(t, input, "verify account_signData ML-DSA-87 signature")
	})

	t.Run("typed signature width", func(t *testing.T) {
		input := fixture.input
		sig := decodeTestSignature(t, input.TypedResponse, 4)
		input.TypedResponse = rpcResult(t, 4, hexutil.Bytes(sig[:len(sig)-1]))
		requireVerifyError(t, input, "account_signTypedData signature width")
	})

	t.Run("legacy domain type", func(t *testing.T) {
		input := fixture.input
		typed := fixture.typed
		typed.Types = cloneTypes(typed.Types)
		typed.Types["EIP712Domain"] = typed.Types["QRLTypedDataDomain"]
		delete(typed.Types, "QRLTypedDataDomain")
		input.TypedRequest = typedRequest(t, input.Account, typed)
		requireVerifyError(t, input, "types do not match the VM64 scenario")
	})

	t.Run("signed recipient mismatch", func(t *testing.T) {
		input := fixture.input
		wrongTo := common.MustParseAddress("Q" + strings.Repeat("a5", common.AddressLength))
		wrongTx := signTestTransaction(t, fixture.wallet, wrongTo)
		input.TxResponse = transactionResponse(t, wrongTx, wrongTx)
		requireVerifyError(t, input, "signed transaction recipient")
	})

	t.Run("raw and JSON transaction mismatch", func(t *testing.T) {
		input := fixture.input
		wrongTo := common.MustParseAddress("Q" + strings.Repeat("5a", common.AddressLength))
		wrongTx := signTestTransaction(t, fixture.wallet, wrongTo)
		input.TxResponse = transactionResponse(t, fixture.signedTx, wrongTx)
		requireVerifyError(t, input, "raw and tx results encode different transactions")
	})
}

func newTestFixture(t *testing.T) testFixture {
	t.Helper()
	w, err := wallet.RestoreFromSeedHex(testSeed)
	if err != nil {
		t.Fatal(err)
	}
	account := common.Address(w.GetAddress()).Hex()
	typed := makeTypedData(t, account)

	dataSig, err := w.Sign(accounts.TextHash([]byte(expectedText)))
	if err != nil {
		t.Fatal(err)
	}
	typedDigest, _, err := apitypes.TypedDataAndHash(typed)
	if err != nil {
		t.Fatal(err)
	}
	typedSig, err := w.Sign(typedDigest)
	if err != nil {
		t.Fatal(err)
	}
	recipient := common.MustParseAddress(expectedRecipient)
	signedTx := signTestTransaction(t, w, recipient)

	input := ScenarioInput{
		Seed:            testSeed,
		Account:         account,
		VersionResponse: rpcResult(t, 1, "6.1.0"),
		ListResponse:    rpcResult(t, 2, []string{account}),
		DataRequest: rpcRequestJSON(t, "account_signData", 3,
			accounts.MimetypeTextPlain, account, hexutil.Encode([]byte(expectedText))),
		DataResponse:  rpcResult(t, 3, hexutil.Bytes(dataSig)),
		TypedRequest:  typedRequest(t, account, typed),
		TypedResponse: rpcResult(t, 4, hexutil.Bytes(typedSig)),
		TxRequest:     transactionRequest(t, account),
		TxResponse:    transactionResponse(t, signedTx, signedTx),
	}
	return testFixture{input: input, wallet: w, typed: typed, signedTx: signedTx}
}

func signTestTransaction(t *testing.T, w wallet.Wallet, to common.Address) *types.Transaction {
	t.Helper()
	input, err := hexutil.Decode(expectedTxInputHex)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := types.SignNewTx(w, types.LatestSignerForChainID(big.NewInt(expectedChainID)), &types.DynamicFeeTx{
		ChainID:    big.NewInt(expectedChainID),
		Nonce:      expectedNonce,
		GasTipCap:  big.NewInt(expectedTip),
		GasFeeCap:  big.NewInt(expectedFeeCap),
		Gas:        expectedGas,
		To:         &to,
		Value:      big.NewInt(expectedValue),
		Data:       input,
		AccessList: types.AccessList{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}

func makeTypedData(t *testing.T, account string) apitypes.TypedData {
	t.Helper()
	raw := []byte(`{
  "types": {
    "QRLTypedDataDomain": [
      {"name":"name","type":"string"},
      {"name":"version","type":"string"},
      {"name":"chainId","type":"uint256"},
      {"name":"verifyingContract","type":"address"}
    ],
    "Message": [
      {"name":"sender","type":"address"},
      {"name":"contents","type":"string"},
      {"name":"value","type":"uint256"}
    ]
  },
  "primaryType":"Message",
  "domain": {
    "name":"` + expectedTypedName + `",
    "version":"` + expectedTypedVersion + `",
    "chainId":"1337",
    "verifyingContract":"` + account + `"
  },
  "message": {
    "sender":"` + account + `",
    "contents":"` + expectedTypedContents + `",
    "value":"` + expectedTypedValue + `"
  }
}`)
	var typed apitypes.TypedData
	if err := json.Unmarshal(raw, &typed); err != nil {
		t.Fatal(err)
	}
	return typed
}

func transactionRequest(t *testing.T, account string) []byte {
	t.Helper()
	return rpcRequestJSON(t, "account_signTransaction", 5, map[string]any{
		"from":                 account,
		"to":                   expectedRecipient,
		"gas":                  hexutil.Uint64(expectedGas),
		"maxFeePerGas":         (*hexutil.Big)(big.NewInt(expectedFeeCap)),
		"maxPriorityFeePerGas": (*hexutil.Big)(big.NewInt(expectedTip)),
		"value":                (*hexutil.Big)(big.NewInt(expectedValue)),
		"nonce":                hexutil.Uint64(expectedNonce),
		"chainId":              (*hexutil.Big)(big.NewInt(expectedChainID)),
		"input":                hexutil.Bytes(hexutil.MustDecode(expectedTxInputHex)),
		"accessList":           types.AccessList{},
	})
}

func transactionResponse(t *testing.T, rawTx, jsonTx *types.Transaction) []byte {
	t.Helper()
	raw, err := rawTx.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return rpcResult(t, 5, signTransactionResult{Raw: raw, Tx: jsonTx})
}

func typedRequest(t *testing.T, account string, typed apitypes.TypedData) []byte {
	t.Helper()
	return rpcRequestJSON(t, "account_signTypedData", 4, account, typed)
}

func rpcRequestJSON(t *testing.T, method string, id int, params ...any) []byte {
	t.Helper()
	return mustJSON(t, map[string]any{"jsonrpc": "2.0", "method": method, "params": params, "id": id})
}

func rpcResult(t *testing.T, id int, result any) []byte {
	t.Helper()
	return mustJSON(t, map[string]any{"jsonrpc": "2.0", "result": result, "id": id})
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func decodeTestSignature(t *testing.T, response []byte, id int) []byte {
	t.Helper()
	sig, err := decodeSignatureResponse(response, id)
	if err != nil {
		t.Fatal(err)
	}
	return append([]byte(nil), sig...)
}

func cloneTypes(input apitypes.Types) apitypes.Types {
	out := make(apitypes.Types, len(input))
	for key, value := range input {
		out[key] = append([]apitypes.Type(nil), value...)
	}
	return out
}

func requireVerifyError(t *testing.T, input ScenarioInput, want string) {
	t.Helper()
	err := VerifyScenario(input)
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("verifyScenario error = %v, want substring %q", err, want)
	}
}
