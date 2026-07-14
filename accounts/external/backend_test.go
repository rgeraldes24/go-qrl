// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package external

import (
	"bytes"
	"math/big"
	"reflect"
	"testing"

	"github.com/theQRL/go-qrl/accounts"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/common/math"
	"github.com/theQRL/go-qrl/rpc"
	"github.com/theQRL/go-qrl/signer/core/apitypes"
)

type typedDataAccountAPI struct {
	address     common.Address
	primaryType string
	digest      []byte
	result      *apitypes.TypedDataSignature
}

func (api *typedDataAccountAPI) SignTypedData(address common.MixedcaseAddress, typedData apitypes.TypedData) (*apitypes.TypedDataSignature, error) {
	api.address = address.Address()
	api.primaryType = typedData.PrimaryType
	digest, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		return nil, err
	}
	api.digest = digest
	return api.result, nil
}

func TestExternalSignerSignTypedData(t *testing.T) {
	account := accounts.Account{Address: common.BytesToAddress([]byte{1})}
	amount := *new(big.Int).Lsh(big.NewInt(1), 400)
	nonce := math.HexOrDecimal256(*big.NewInt(42))
	typedData := apitypes.TypedData{
		Types: apitypes.Types{
			apitypes.TypedDataDomainType: {
				{Name: "name", Type: "string"},
				{Name: "version", Type: "string"},
				{Name: "chainId", Type: "uint256"},
				{Name: "verifyingContract", Type: "address"},
				{Name: "salt", Type: "bytes32"},
			},
			"Message": {
				{Name: "contents", Type: "string"},
				{Name: "payload", Type: "bytes"},
				{Name: "tag", Type: "bytes3"},
				{Name: "amount", Type: "uint512"},
				{Name: "nonce", Type: "uint256"},
			},
		},
		PrimaryType: "Message",
		Domain: apitypes.TypedDataDomain{
			Name:              "external signer test",
			Version:           "1",
			ChainId:           math.NewHexOrDecimal256(1),
			VerifyingContract: common.Address{}.Hex(),
			Salt:              hexutil.Encode(make([]byte, common.HashLength)),
		},
		Message: apitypes.TypedDataMessage{
			"contents": "hello",
			"payload":  []byte{0xaa, 0xbb},
			"tag":      [3]byte{0x01, 0x02, 0x03},
			"amount":   amount,
			"nonce":    nonce,
		},
	}
	wantDigest, _, err := apitypes.TypedDataAndHash(typedData)
	if err != nil {
		t.Fatal(err)
	}
	want := &apitypes.TypedDataSignature{
		Version:    apitypes.TypedDataVersion,
		Algorithm:  apitypes.TypedDataAlgorithm,
		Address:    account.Address,
		Digest:     common.BytesToHash([]byte{2}),
		PublicKey:  hexutil.Bytes{},
		Descriptor: hexutil.Bytes{},
		Signature:  hexutil.Bytes{3},
	}
	service := &typedDataAccountAPI{result: want}
	server := rpc.NewServer()
	t.Cleanup(server.Stop)
	if err := server.RegisterName("account", service); err != nil {
		t.Fatal(err)
	}
	client := rpc.DialInProc(server)
	t.Cleanup(client.Close)
	signer := &ExternalSigner{client: client}

	got, err := signer.SignTypedData(account, typedData)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("result %#v, want %#v", got, want)
	}
	if service.address != account.Address {
		t.Fatalf("signing address %s, want %s", service.address, account.Address)
	}
	if service.primaryType != typedData.PrimaryType {
		t.Fatalf("primary type %q, want %q", service.primaryType, typedData.PrimaryType)
	}
	if !bytes.Equal(service.digest, wantDigest) {
		t.Fatalf("remote digest %x, want %x", service.digest, wantDigest)
	}
}
