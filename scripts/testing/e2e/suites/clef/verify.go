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
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"reflect"
	"strings"

	"github.com/theQRL/go-qrl/accounts"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/signer/core/apitypes"
)

const (
	expectedText                 = "Clef VM64 signData"
	expectedRecipient            = "Qd5812f6cf4a0f645aa620cd57319a0ed649dd8f5519a9dde7770ae5b0e49e547985f35eb972a2a07041561aa39c65a3991478f9b1e6749e05277dcf58a9a8b72"
	expectedTypedName            = "Local Testnet VM64"
	expectedTypedVersion         = "1"
	expectedTypedContents        = "Clef VM64 typed data"
	expectedTypedValue           = "340282366920938463463374607431768211457"
	expectedTxInputHex           = "0x000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f"
	expectedChainID       int64  = 1337
	expectedNonce         uint64 = 9
	expectedGas           uint64 = 40000
	expectedTip                  = 7
	expectedFeeCap               = 1_000_000_000
	expectedValue                = 42
)

type ScenarioPaths struct {
	Seed            string
	Account         string
	VersionResponse string
	ListResponse    string
	DataRequest     string
	DataResponse    string
	TypedRequest    string
	TypedResponse   string
	TxRequest       string
	TxResponse      string
}

type ScenarioInput struct {
	Seed            string
	Account         string
	VersionResponse []byte
	ListResponse    []byte
	DataRequest     []byte
	DataResponse    []byte
	TypedRequest    []byte
	TypedResponse   []byte
	TxRequest       []byte
	TxResponse      []byte
}

type rpcRequest struct {
	JSONRPC string            `json:"jsonrpc"`
	Method  string            `json:"method"`
	Params  []json.RawMessage `json:"params"`
	ID      int               `json:"id"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	Result  json.RawMessage `json:"result"`
	Error   *rpcError       `json:"error"`
	ID      int             `json:"id"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type signTransactionResult struct {
	Raw hexutil.Bytes      `json:"raw"`
	Tx  *types.Transaction `json:"tx"`
}

func VerifyScenarioFiles(paths ScenarioPaths) error {
	if paths.Seed == "" || paths.Account == "" {
		return errors.New("-seed and -account are required")
	}
	files := []struct {
		name string
		path string
		dst  *[]byte
	}{
		{"version response", paths.VersionResponse, nil},
		{"list response", paths.ListResponse, nil},
		{"data request", paths.DataRequest, nil},
		{"data response", paths.DataResponse, nil},
		{"typed request", paths.TypedRequest, nil},
		{"typed response", paths.TypedResponse, nil},
		{"transaction request", paths.TxRequest, nil},
		{"transaction response", paths.TxResponse, nil},
	}
	input := ScenarioInput{Seed: paths.Seed, Account: paths.Account}
	files[0].dst = &input.VersionResponse
	files[1].dst = &input.ListResponse
	files[2].dst = &input.DataRequest
	files[3].dst = &input.DataResponse
	files[4].dst = &input.TypedRequest
	files[5].dst = &input.TypedResponse
	files[6].dst = &input.TxRequest
	files[7].dst = &input.TxResponse
	for _, file := range files {
		if file.path == "" {
			return fmt.Errorf("%s path is required", file.name)
		}
		data, err := os.ReadFile(file.path)
		if err != nil {
			return fmt.Errorf("read %s: %w", file.name, err)
		}
		*file.dst = data
	}
	return VerifyScenario(input)
}

func VerifyScenario(input ScenarioInput) error {
	w, err := wallet.RestoreFromSeedHex(strings.TrimPrefix(input.Seed, "0x"))
	if err != nil {
		return fmt.Errorf("restore expected wallet: %w", err)
	}
	account, err := common.NewAddressFromString(input.Account)
	if err != nil {
		return fmt.Errorf("parse imported account: %w", err)
	}
	if len(account.Bytes()) != common.AddressLength {
		return fmt.Errorf("imported account width: got %d, want %d", len(account.Bytes()), common.AddressLength)
	}
	walletAddress := common.Address(w.GetAddress())
	if account != walletAddress {
		return fmt.Errorf("imported account %s does not match seed address %s", account.Hex(), walletAddress.Hex())
	}
	if err := verifyVersionAndList(input.VersionResponse, input.ListResponse, account); err != nil {
		return err
	}
	dataSig, err := verifyDataSignature(input.DataRequest, input.DataResponse, account, w)
	if err != nil {
		return err
	}
	typedSig, err := verifyTypedSignature(input.TypedRequest, input.TypedResponse, account, w)
	if err != nil {
		return err
	}
	if bytes.Equal(dataSig, typedSig) {
		return errors.New("data and typed-data requests unexpectedly returned the same signature")
	}
	return verifyTransaction(input.TxRequest, input.TxResponse, account, w)
}

func verifyVersionAndList(versionJSON, listJSON []byte, account common.Address) error {
	versionRaw, err := decodeRPCResponse(versionJSON, 1)
	if err != nil {
		return fmt.Errorf("account_version: %w", err)
	}
	var version string
	if err := json.Unmarshal(versionRaw, &version); err != nil {
		return fmt.Errorf("decode account_version result: %w", err)
	}
	if version == "" {
		return errors.New("account_version result is an empty string")
	}
	listRaw, err := decodeRPCResponse(listJSON, 2)
	if err != nil {
		return fmt.Errorf("account_list: %w", err)
	}
	var listed []common.Address
	if err := json.Unmarshal(listRaw, &listed); err != nil {
		return fmt.Errorf("decode account_list result: %w", err)
	}
	if len(listed) != 1 || listed[0] != account {
		return fmt.Errorf("account_list returned %v, want exactly [%s]", listed, account.Hex())
	}
	return nil
}

func verifyDataSignature(requestJSON, responseJSON []byte, account common.Address, w wallet.Wallet) ([]byte, error) {
	req, err := decodeRPCRequest(requestJSON, "account_signData", 3, 3)
	if err != nil {
		return nil, fmt.Errorf("account_signData request: %w", err)
	}
	var mime, requestedAccount, encodedData string
	if err := decodeParams(req.Params, &mime, &requestedAccount, &encodedData); err != nil {
		return nil, fmt.Errorf("account_signData params: %w", err)
	}
	if mime != accounts.MimetypeTextPlain {
		return nil, fmt.Errorf("account_signData MIME: got %q, want %q", mime, accounts.MimetypeTextPlain)
	}
	if err := requireAccount(requestedAccount, account, "account_signData"); err != nil {
		return nil, err
	}
	data, err := hexutil.Decode(encodedData)
	if err != nil {
		return nil, fmt.Errorf("decode account_signData body: %w", err)
	}
	if string(data) != expectedText {
		return nil, fmt.Errorf("account_signData body: got %q, want %q", data, expectedText)
	}
	sig, err := decodeSignatureResponse(responseJSON, 3)
	if err != nil {
		return nil, fmt.Errorf("account_signData response: %w", err)
	}
	if err := verifyMLDSASignature("account_signData", sig, accounts.TextHash(data), w); err != nil {
		return nil, err
	}
	return sig, nil
}

func verifyTypedSignature(requestJSON, responseJSON []byte, account common.Address, w wallet.Wallet) ([]byte, error) {
	req, err := decodeRPCRequest(requestJSON, "account_signTypedData", 4, 2)
	if err != nil {
		return nil, fmt.Errorf("account_signTypedData request: %w", err)
	}
	var requestedAccount string
	var typed apitypes.TypedData
	if err := decodeParams(req.Params, &requestedAccount, &typed); err != nil {
		return nil, fmt.Errorf("account_signTypedData params: %w", err)
	}
	if err := requireAccount(requestedAccount, account, "account_signTypedData"); err != nil {
		return nil, err
	}
	if err := requireExpectedTypedData(typed, account); err != nil {
		return nil, err
	}
	digest, _, err := apitypes.TypedDataAndHash(typed)
	if err != nil {
		return nil, fmt.Errorf("hash account_signTypedData body: %w", err)
	}
	sig, err := decodeSignatureResponse(responseJSON, 4)
	if err != nil {
		return nil, fmt.Errorf("account_signTypedData response: %w", err)
	}
	if err := verifyMLDSASignature("account_signTypedData", sig, digest, w); err != nil {
		return nil, err
	}
	return sig, nil
}

func requireExpectedTypedData(typed apitypes.TypedData, account common.Address) error {
	wantTypes := apitypes.Types{
		"QRLTypedDataDomain": {
			{Name: "name", Type: "string"},
			{Name: "version", Type: "string"},
			{Name: "chainId", Type: "uint256"},
			{Name: "verifyingContract", Type: "address"},
		},
		"Message": {
			{Name: "sender", Type: "address"},
			{Name: "contents", Type: "string"},
			{Name: "value", Type: "uint256"},
		},
	}
	if !reflect.DeepEqual(typed.Types, wantTypes) {
		return fmt.Errorf("account_signTypedData types do not match the VM64 scenario: got %#v", typed.Types)
	}
	if typed.PrimaryType != "Message" {
		return fmt.Errorf("account_signTypedData primaryType: got %q, want Message", typed.PrimaryType)
	}
	if typed.Domain.Name != expectedTypedName || typed.Domain.Version != expectedTypedVersion || typed.Domain.Salt != "" {
		return fmt.Errorf("account_signTypedData domain metadata does not match the VM64 scenario")
	}
	if typed.Domain.ChainId == nil || (*big.Int)(typed.Domain.ChainId).Cmp(big.NewInt(expectedChainID)) != 0 {
		return fmt.Errorf("account_signTypedData domain chainId: got %v, want %d", typed.Domain.ChainId, expectedChainID)
	}
	if err := requireAccount(typed.Domain.VerifyingContract, account, "typed-data verifyingContract"); err != nil {
		return err
	}
	if len(typed.Message) != 3 {
		return fmt.Errorf("account_signTypedData message has %d fields, want 3", len(typed.Message))
	}
	sender, ok := typed.Message["sender"].(string)
	if !ok {
		return fmt.Errorf("account_signTypedData sender has type %T, want string", typed.Message["sender"])
	}
	if err := requireAccount(sender, account, "typed-data sender"); err != nil {
		return err
	}
	if typed.Message["contents"] != expectedTypedContents || typed.Message["value"] != expectedTypedValue {
		return fmt.Errorf("account_signTypedData message does not match the VM64 scenario: got %#v", typed.Message)
	}
	return nil
}

func verifyTransaction(requestJSON, responseJSON []byte, account common.Address, w wallet.Wallet) error {
	req, err := decodeRPCRequest(requestJSON, "account_signTransaction", 5, 1)
	if err != nil {
		return fmt.Errorf("account_signTransaction request: %w", err)
	}
	var args apitypes.SendTxArgs
	if err := decodeParams(req.Params, &args); err != nil {
		return fmt.Errorf("account_signTransaction params: %w", err)
	}
	wantTo, wantInput, err := requireExpectedTransactionArgs(args, account)
	if err != nil {
		return err
	}
	rawResult, err := decodeRPCResponse(responseJSON, 5)
	if err != nil {
		return fmt.Errorf("account_signTransaction response: %w", err)
	}
	var result signTransactionResult
	if err := json.Unmarshal(rawResult, &result); err != nil {
		return fmt.Errorf("decode account_signTransaction result: %w", err)
	}
	if len(result.Raw) == 0 || result.Tx == nil {
		return errors.New("account_signTransaction result must contain both raw and tx")
	}
	var decoded types.Transaction
	if err := decoded.UnmarshalBinary(result.Raw); err != nil {
		return fmt.Errorf("decode raw signed transaction: %w", err)
	}
	jsonTxRaw, err := result.Tx.MarshalBinary()
	if err != nil {
		return fmt.Errorf("encode JSON signed transaction: %w", err)
	}
	if !bytes.Equal(jsonTxRaw, result.Raw) {
		return errors.New("account_signTransaction raw and tx results encode different transactions")
	}
	if decoded.Type() != types.DynamicFeeTxType {
		return fmt.Errorf("signed transaction type: got %d, want %d", decoded.Type(), types.DynamicFeeTxType)
	}
	if decoded.ChainId().Cmp(big.NewInt(expectedChainID)) != 0 {
		return fmt.Errorf("signed transaction chain ID: got %s, want %d", decoded.ChainId(), expectedChainID)
	}
	if decoded.Nonce() != expectedNonce || decoded.Gas() != expectedGas {
		return fmt.Errorf("signed transaction nonce/gas: got %d/%d, want %d/%d", decoded.Nonce(), decoded.Gas(), expectedNonce, expectedGas)
	}
	if decoded.GasTipCap().Cmp(big.NewInt(expectedTip)) != 0 || decoded.GasFeeCap().Cmp(big.NewInt(expectedFeeCap)) != 0 {
		return fmt.Errorf("signed transaction fee caps do not match request")
	}
	if decoded.Value().Cmp(big.NewInt(expectedValue)) != 0 || !bytes.Equal(decoded.Data(), wantInput) {
		return fmt.Errorf("signed transaction value/body do not match request")
	}
	if decoded.To() == nil || *decoded.To() != wantTo {
		return fmt.Errorf("signed transaction recipient: got %v, want %s", decoded.To(), wantTo.Hex())
	}
	if len(decoded.To().Bytes()) != common.AddressLength {
		return fmt.Errorf("signed transaction recipient width: got %d, want %d", len(decoded.To().Bytes()), common.AddressLength)
	}
	if len(decoded.AccessList()) != 0 {
		return fmt.Errorf("signed transaction access list is not empty: %#v", decoded.AccessList())
	}
	if len(decoded.RawSignatureValue()) != pqcrypto.MLDSA87SignatureLength {
		return fmt.Errorf("signed transaction signature width: got %d, want %d", len(decoded.RawSignatureValue()), pqcrypto.MLDSA87SignatureLength)
	}
	if len(decoded.RawPublicKeyValue()) != pqcrypto.MLDSA87PublicKeyLength {
		return fmt.Errorf("signed transaction public-key width: got %d, want %d", len(decoded.RawPublicKeyValue()), pqcrypto.MLDSA87PublicKeyLength)
	}
	if !bytes.Equal(decoded.RawPublicKeyValue(), w.GetPK()) || !bytes.Equal(decoded.Descriptor(), w.GetDescriptor().ToBytes()) {
		return errors.New("signed transaction public key or descriptor does not match the imported seed")
	}
	if len(decoded.ExtraParams()) != 0 {
		return fmt.Errorf("signed transaction extraParams must be empty, got %x", decoded.ExtraParams())
	}
	signer := types.LatestSignerForChainID(big.NewInt(expectedChainID))
	sender, err := types.Sender(signer, &decoded)
	if err != nil {
		return fmt.Errorf("cryptographically recover signed transaction sender: %w", err)
	}
	if len(sender.Bytes()) != common.AddressLength || sender != account {
		return fmt.Errorf("signed transaction sender: got %s (%d bytes), want %s (%d bytes)", sender.Hex(), len(sender.Bytes()), account.Hex(), common.AddressLength)
	}
	return nil
}

func requireExpectedTransactionArgs(args apitypes.SendTxArgs, account common.Address) (common.Address, []byte, error) {
	if args.From.Address() != account {
		return common.Address{}, nil, fmt.Errorf("account_signTransaction from: got %s, want %s", args.From.Address().Hex(), account.Hex())
	}
	to, err := common.NewAddressFromString(expectedRecipient)
	if err != nil {
		return common.Address{}, nil, err
	}
	if args.To == nil || args.To.Address() != to {
		return common.Address{}, nil, fmt.Errorf("account_signTransaction recipient: got %v, want %s", args.To, to.Hex())
	}
	if len(args.To.Address().Bytes()) != common.AddressLength {
		return common.Address{}, nil, fmt.Errorf("account_signTransaction recipient is not %d bytes", common.AddressLength)
	}
	if uint64(args.Nonce) != expectedNonce || uint64(args.Gas) != expectedGas {
		return common.Address{}, nil, fmt.Errorf("account_signTransaction nonce/gas do not match scenario")
	}
	if args.ChainID == nil || (*big.Int)(args.ChainID).Cmp(big.NewInt(expectedChainID)) != 0 {
		return common.Address{}, nil, fmt.Errorf("account_signTransaction chainId: got %v, want %d", args.ChainID, expectedChainID)
	}
	if args.MaxPriorityFeePerGas == nil || (*big.Int)(args.MaxPriorityFeePerGas).Cmp(big.NewInt(expectedTip)) != 0 {
		return common.Address{}, nil, fmt.Errorf("account_signTransaction priority fee does not match scenario")
	}
	if args.MaxFeePerGas == nil || (*big.Int)(args.MaxFeePerGas).Cmp(big.NewInt(expectedFeeCap)) != 0 {
		return common.Address{}, nil, fmt.Errorf("account_signTransaction fee cap does not match scenario")
	}
	if (*big.Int)(&args.Value).Cmp(big.NewInt(expectedValue)) != 0 {
		return common.Address{}, nil, fmt.Errorf("account_signTransaction value does not match scenario")
	}
	if args.Data != nil || args.Input == nil {
		return common.Address{}, nil, errors.New("account_signTransaction must use the input field exactly once")
	}
	wantInput, err := hexutil.Decode(expectedTxInputHex)
	if err != nil {
		return common.Address{}, nil, err
	}
	if !bytes.Equal(*args.Input, wantInput) {
		return common.Address{}, nil, fmt.Errorf("account_signTransaction input: got %x, want %x", *args.Input, wantInput)
	}
	if args.AccessList == nil || len(*args.AccessList) != 0 {
		return common.Address{}, nil, errors.New("account_signTransaction accessList must be present and empty")
	}
	return to, wantInput, nil
}

func verifyMLDSASignature(label string, sig, digest []byte, w wallet.Wallet) error {
	if len(sig) != pqcrypto.MLDSA87SignatureLength {
		return fmt.Errorf("%s signature width: got %d, want %d", label, len(sig), pqcrypto.MLDSA87SignatureLength)
	}
	if len(digest) != pqcrypto.DigestLength {
		return fmt.Errorf("%s digest width: got %d, want %d", label, len(digest), pqcrypto.DigestLength)
	}
	ok, err := pqcrypto.MLDSA87VerifySignature(sig, digest, w.GetPK(), w.GetDescriptor())
	if err != nil {
		return fmt.Errorf("verify %s ML-DSA-87 signature: %w", label, err)
	}
	if !ok {
		return fmt.Errorf("verify %s ML-DSA-87 signature: verification failed", label)
	}
	return nil
}

func requireAccount(input string, want common.Address, label string) error {
	got, err := common.NewAddressFromString(input)
	if err != nil {
		return fmt.Errorf("%s address: %w", label, err)
	}
	if got != want {
		return fmt.Errorf("%s address: got %s, want %s", label, got.Hex(), want.Hex())
	}
	if len(got.Bytes()) != common.AddressLength {
		return fmt.Errorf("%s address width: got %d, want %d", label, len(got.Bytes()), common.AddressLength)
	}
	return nil
}

func decodeRPCRequest(data []byte, method string, id, paramCount int) (rpcRequest, error) {
	var req rpcRequest
	if err := decodeJSON(data, &req); err != nil {
		return req, err
	}
	if req.JSONRPC != "2.0" || req.Method != method || req.ID != id || len(req.Params) != paramCount {
		return req, fmt.Errorf("got jsonrpc=%q method=%q id=%d params=%d, want 2.0/%s/%d/%d", req.JSONRPC, req.Method, req.ID, len(req.Params), method, id, paramCount)
	}
	return req, nil
}

func decodeRPCResponse(data []byte, id int) (json.RawMessage, error) {
	var response rpcResponse
	if err := decodeJSON(data, &response); err != nil {
		return nil, err
	}
	if response.JSONRPC != "2.0" || response.ID != id {
		return nil, fmt.Errorf("got jsonrpc=%q id=%d, want 2.0/%d", response.JSONRPC, response.ID, id)
	}
	if response.Error != nil {
		return nil, fmt.Errorf("RPC error %d: %s", response.Error.Code, response.Error.Message)
	}
	if len(response.Result) == 0 || bytes.Equal(response.Result, []byte("null")) {
		return nil, errors.New("missing result")
	}
	return response.Result, nil
}

func decodeSignatureResponse(data []byte, id int) ([]byte, error) {
	raw, err := decodeRPCResponse(data, id)
	if err != nil {
		return nil, err
	}
	var sig hexutil.Bytes
	if err := json.Unmarshal(raw, &sig); err != nil {
		return nil, fmt.Errorf("decode signature: %w", err)
	}
	return sig, nil
}

func decodeParams(params []json.RawMessage, out ...any) error {
	if len(params) != len(out) {
		return fmt.Errorf("got %d params, want %d", len(params), len(out))
	}
	for i := range params {
		if err := json.Unmarshal(params[i], out[i]); err != nil {
			return fmt.Errorf("param %d: %w", i, err)
		}
	}
	return nil
}

func decodeJSON(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return fmt.Errorf("trailing JSON: %w", err)
	}
	return nil
}
