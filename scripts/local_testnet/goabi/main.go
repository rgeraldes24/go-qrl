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
// You should have received a copy of the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

// goabi runs Go ABI/qrlclient E2E checks against the local testnet.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/core/vm"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/qrlclient/gqrlclient"
)

const emitterABI = `[{"inputs":[],"stateMutability":"nonpayable","type":"constructor"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"uint256","name":"value","type":"uint256"}],"name":"Deployed","type":"event"}]`

const vm64ABI = `[
	{"name":"store","type":"function","inputs":[
		{"name":"amount","type":"uint512"},
		{"name":"tag","type":"bytes4"},
		{"name":"recipient","type":"address"},
		{"name":"payload","type":"bytes"}
	],"outputs":[]},
	{"name":"read","type":"function","inputs":[],"outputs":[
		{"name":"amount","type":"uint512"},
		{"name":"tag","type":"bytes4"},
		{"name":"recipient","type":"address"},
		{"name":"payload","type":"bytes"}
	]},
	{"name":"acceptBytes64","type":"function","inputs":[{"name":"value","type":"bytes64"}],"outputs":[]}
]`

func main() {
	var (
		rpcURL     = flag.String("rpc", "", "HTTP RPC endpoint of the node")
		graphqlURL = flag.String("graphql", "", "GraphQL endpoint; skipped when empty")
		wsURL      = flag.String("ws", "", "WebSocket RPC endpoint; subscription checks are skipped when empty")
		seed       = flag.String("seed", "", "hex encoded ML-DSA-87 wallet seed")
		bin        = flag.String("bin", "", "hex encoded EventEmitter deployment bytecode")
	)
	flag.Parse()
	if *rpcURL == "" || *seed == "" || *bin == "" {
		fmt.Fprintln(os.Stderr, "usage: goabi -rpc <url> -seed <hexseed> -bin <deployment bytecode> [-graphql <url>] [-ws <url>]")
		os.Exit(2)
	}

	if err := run(*rpcURL, *graphqlURL, *wsURL, *seed, *bin); err != nil {
		fmt.Fprintf(os.Stderr, "SUITE go_abi: FAILED -- %v\n", err)
		os.Exit(1)
	}
	fmt.Println("SUITE go_abi: PASSED")
}

func run(rpcURL, graphqlURL, wsURL, seedHex, binHex string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	w, err := wallet.RestoreFromSeedHex(strings.TrimPrefix(seedHex, "0x"))
	if err != nil {
		return fmt.Errorf("restore wallet: %w", err)
	}
	from := common.Address(w.GetAddress())

	client, err := qrlclient.Dial(rpcURL)
	if err != nil {
		return fmt.Errorf("dial %s: %w", rpcURL, err)
	}
	defer client.Close()

	if err := checkGoABILayout(from); err != nil {
		return err
	}
	if err := checkLiveEventRoundTrip(ctx, client, w, from, binHex, graphqlURL); err != nil {
		return err
	}
	if err := checkStorageAPIs(ctx, graphqlURL, client, w, from); err != nil {
		return err
	}
	if graphqlURL != "" {
		if err := checkGraphQLSendRawTransaction(ctx, graphqlURL, client, w, from); err != nil {
			return err
		}
	}
	if err := checkWebSocketSubscriptions(ctx, wsURL, client, w, from); err != nil {
		return err
	}
	return nil
}

func checkGoABILayout(addr common.Address) error {
	parsed, err := abi.JSON(strings.NewReader(vm64ABI))
	if err != nil {
		return fmt.Errorf("parse VM64 ABI: %w", err)
	}

	tag := [4]byte{1, 2, 3, 4}
	packed, err := parsed.Pack("store", big.NewInt(1337), tag, addr, []byte{0xab, 0xcd})
	if err != nil {
		return fmt.Errorf("pack VM64 calldata: %w", err)
	}
	expected := concat(
		methodID("store(uint512,bytes4,address,bytes)"),
		word("539"),
		fixedBytes("01020304"),
		addr[:],
		word("100"),
		word("2"),
		fixedBytes("abcd"),
	)
	if !bytes.Equal(packed, expected) {
		return fmt.Errorf("Go ABI calldata mismatch:\nhave %x\nwant %x", packed, expected)
	}

	var b64 [64]byte
	for i := range b64 {
		b64[i] = 0xab
	}
	packed, err = parsed.Pack("acceptBytes64", b64)
	if err != nil {
		return fmt.Errorf("pack bytes64: %w", err)
	}
	expected = append(methodID("acceptBytes64(bytes64)"), b64[:]...)
	if !bytes.Equal(packed, expected) {
		return fmt.Errorf("Go ABI bytes64 mismatch:\nhave %x\nwant %x", packed, expected)
	}

	output := concat(
		word("539"),
		fixedBytes("01020304"),
		addr[:],
		word("100"),
		word("2"),
		fixedBytes("abcd"),
	)
	values, err := parsed.Unpack("read", output)
	if err != nil {
		return fmt.Errorf("unpack VM64 output: %w", err)
	}
	if len(values) != 4 {
		return fmt.Errorf("unpack returned %d values", len(values))
	}
	if values[0].(*big.Int).Cmp(big.NewInt(1337)) != 0 {
		return fmt.Errorf("decoded amount mismatch: %v", values[0])
	}
	if values[1].([4]byte) != tag {
		return fmt.Errorf("decoded bytes4 mismatch: %x", values[1])
	}
	if values[2].(common.Address) != addr {
		return fmt.Errorf("decoded address mismatch: %s", values[2])
	}
	if !bytes.Equal(values[3].([]byte), []byte{0xab, 0xcd}) {
		return fmt.Errorf("decoded bytes mismatch: %x", values[3])
	}
	return nil
}

type eventDeployment struct {
	address common.Address
	tx      *types.Transaction
	receipt *types.Receipt
	event   abi.Event
	topic   common.LogTopic
	binding *EventEmitter
}

func checkLiveEventRoundTrip(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address, binHex, graphqlURL string) error {
	if normalizeHex(EventEmitterBin) != normalizeHex(binHex) {
		return fmt.Errorf("generated binding bytecode differs from JS fixture")
	}
	deployment, err := deployEventEmitter(ctx, client, w, from)
	if err != nil {
		return err
	}
	receipt := deployment.receipt

	parsed, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return fmt.Errorf("parse emitter ABI from generated binding: %w", err)
	}
	log := receipt.Logs[0]
	values, err := parsed.Unpack("Deployed", log.Data)
	if err != nil {
		return fmt.Errorf("decode deployment log data: %w", err)
	}
	if len(values) != 1 || values[0].(*big.Int).Cmp(big.NewInt(1337)) != 0 {
		return fmt.Errorf("decoded deployment value mismatch: %v", values)
	}

	parsedEvent, err := deployment.binding.ParseDeployed(*log)
	if err != nil {
		return fmt.Errorf("parse deployment log through generated binding: %w", err)
	}
	if parsedEvent.Value.Cmp(big.NewInt(1337)) != 0 || parsedEvent.Raw.TxHash != receipt.TxHash {
		return fmt.Errorf("generated binding parsed event mismatch: %+v", parsedEvent)
	}

	it, err := deployment.binding.FilterDeployed(&bind.FilterOpts{
		Start:   receipt.BlockNumber.Uint64(),
		End:     uint64Ptr(receipt.BlockNumber.Uint64()),
		Context: ctx,
	})
	if err != nil {
		return fmt.Errorf("filter through generated binding: %w", err)
	}
	defer it.Close()
	if !it.Next() {
		if err := it.Error(); err != nil {
			return fmt.Errorf("generated binding event iterator: %w", err)
		}
		return fmt.Errorf("generated binding event iterator returned no events")
	}
	if it.Event.Value.Cmp(big.NewInt(1337)) != 0 || it.Event.Raw.TxHash != receipt.TxHash {
		return fmt.Errorf("generated binding filtered event mismatch: %+v", it.Event)
	}
	if it.Next() {
		return fmt.Errorf("generated binding filter returned more than one event")
	}
	if err := it.Error(); err != nil {
		return fmt.Errorf("generated binding event iterator final error: %w", err)
	}

	topics, err := abi.MakeTopics([]any{deployment.event.ID})
	if err != nil {
		return fmt.Errorf("make event filter topic: %w", err)
	}
	logs, err := client.FilterLogs(ctx, qrl.FilterQuery{
		FromBlock: receipt.BlockNumber,
		ToBlock:   receipt.BlockNumber,
		Addresses: []common.Address{receipt.ContractAddress},
		Topics:    topics,
	})
	if err != nil {
		return fmt.Errorf("filter deployment logs: %w", err)
	}
	if len(logs) != 1 || logs[0].TxHash != receipt.TxHash || logs[0].Topics[0] != deployment.topic {
		return fmt.Errorf("filtered log mismatch: %+v", logs)
	}
	if graphqlURL != "" {
		if err := checkGraphQLEventLog(ctx, graphqlURL, deployment); err != nil {
			return err
		}
	}
	return nil
}

func deployEventEmitter(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address) (*eventDeployment, error) {
	parsed, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return nil, fmt.Errorf("parse emitter ABI from generated binding: %w", err)
	}
	deployed := parsed.Events["Deployed"]
	expectedTopic := hashTopic(deployed.ID)

	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("chain id: %w", err)
	}
	auth, err := bind.NewKeyedTransactorWithChainID(w, chainID)
	if err != nil {
		return nil, fmt.Errorf("generated binding transactor: %w", err)
	}
	auth.Context = ctx
	auth.From = from

	contractAddress, tx, contract, err := DeployEventEmitter(auth, client)
	if err != nil {
		return nil, fmt.Errorf("deploy through generated binding: %w", err)
	}

	receipt, err := waitReceipt(ctx, client, tx.Hash())
	if err != nil {
		return nil, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("deployment failed with status %d", receipt.Status)
	}
	if receipt.ContractAddress != contractAddress {
		return nil, fmt.Errorf("contract address mismatch: receipt %s binding %s", receipt.ContractAddress, contractAddress)
	}
	if len(receipt.Logs) != 1 {
		return nil, fmt.Errorf("expected one deployment log, got %d", len(receipt.Logs))
	}
	log := receipt.Logs[0]
	if len(log.Topics) != 1 || log.Topics[0] != expectedTopic {
		return nil, fmt.Errorf("event topic mismatch: have %v want %s", log.Topics, expectedTopic.Hex())
	}
	return &eventDeployment{
		address: contractAddress,
		tx:      tx,
		receipt: receipt,
		event:   deployed,
		topic:   expectedTopic,
		binding: contract,
	}, nil
}

func checkStorageAPIs(ctx context.Context, graphqlURL string, client *qrlclient.Client, w wallet.Wallet, from common.Address) error {
	var value [common.StorageValue64Length]byte
	for i := range value {
		value[i] = byte(i + 1)
	}
	receipt, err := deployRaw(ctx, client, w, from, storageContractCode(value))
	if err != nil {
		return fmt.Errorf("deploy storage contract: %w", err)
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("storage contract deployment failed with status %d", receipt.Status)
	}
	slot := common.Hash{}
	storage, err := client.StorageAt(ctx, receipt.ContractAddress, slot, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("qrl_getStorageAt through qrlclient: %w", err)
	}
	if !bytes.Equal(storage, value[:]) {
		return fmt.Errorf("storage value mismatch:\nhave %x\nwant %x", storage, value)
	}
	output, err := client.CallContract(ctx, qrl.CallMsg{
		From: from,
		To:   &receipt.ContractAddress,
		Data: nil,
	}, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("qrl_call through qrlclient: %w", err)
	}
	if !bytes.Equal(output, value[:]) {
		return fmt.Errorf("contract call output mismatch:\nhave %x\nwant %x", output, value)
	}
	proof, err := gqrlclient.New(client.Client()).GetProof(ctx, receipt.ContractAddress, []string{slot.Hex()}, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("qrl_getProof through gqrlclient: %w", err)
	}
	if len(proof.StorageProof) != 1 {
		return fmt.Errorf("storage proof length mismatch: have %d want 1", len(proof.StorageProof))
	}
	if proof.StorageProof[0].Key != slot.Hex() {
		return fmt.Errorf("storage proof key mismatch: have %s want %s", proof.StorageProof[0].Key, slot.Hex())
	}
	if proof.StorageProof[0].Value.Cmp(new(big.Int).SetBytes(value[:])) != 0 {
		return fmt.Errorf("storage proof value mismatch: have %s want 0x%x", proof.StorageProof[0].Value.Text(16), value)
	}
	if graphqlURL != "" {
		if err := checkGraphQLStorage(ctx, graphqlURL, receipt.ContractAddress, receipt.BlockNumber, from, slot, value); err != nil {
			return err
		}
	}
	return nil
}

func checkWebSocketSubscriptions(ctx context.Context, wsURL string, httpClient *qrlclient.Client, w wallet.Wallet, from common.Address) error {
	if wsURL == "" {
		return nil
	}
	wsClient, err := qrlclient.DialContext(ctx, wsURL)
	if err != nil {
		return fmt.Errorf("dial websocket %s: %w", wsURL, err)
	}
	defer wsClient.Close()

	headers := make(chan *types.Header, 4)
	headSub, err := wsClient.SubscribeNewHead(ctx, headers)
	if err != nil {
		return fmt.Errorf("subscribe new heads: %w", err)
	}
	defer headSub.Unsubscribe()

	parsed, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return fmt.Errorf("parse emitter ABI from generated binding: %w", err)
	}
	expectedTopic := hashTopic(parsed.Events["Deployed"].ID)
	events := make(chan types.Log, 4)
	logSub, err := wsClient.SubscribeFilterLogs(ctx, qrl.FilterQuery{
		Topics: [][]common.LogTopic{{expectedTopic}},
	}, events)
	if err != nil {
		return fmt.Errorf("subscribe logs: %w", err)
	}
	defer logSub.Unsubscribe()

	deployment, err := deployEventEmitter(ctx, httpClient, w, from)
	if err != nil {
		return err
	}

	deadline := time.After(90 * time.Second)
	var gotHead, gotLog bool
	for !gotHead || !gotLog {
		select {
		case header := <-headers:
			if header != nil && header.Number != nil && header.Number.Cmp(deployment.receipt.BlockNumber) >= 0 {
				gotHead = true
			}
		case log := <-events:
			if log.TxHash == deployment.receipt.TxHash && len(log.Topics) == 1 && log.Topics[0] == deployment.topic {
				gotLog = true
			}
		case err := <-headSub.Err():
			return fmt.Errorf("new head subscription: %w", err)
		case err := <-logSub.Err():
			return fmt.Errorf("log subscription: %w", err)
		case <-deadline:
			return fmt.Errorf("timed out waiting for websocket events: head=%t log=%t", gotHead, gotLog)
		case <-ctx.Done():
			return fmt.Errorf("wait websocket subscriptions: %w", ctx.Err())
		}
	}
	return nil
}

func deployRaw(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address, payload []byte) (*types.Receipt, error) {
	signed, err := signDynamicFeeTx(ctx, client, w, from, nil, big.NewInt(0), payload)
	if err != nil {
		return nil, err
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("send tx %s: %w", signed.Hash().Hex(), err)
	}
	return waitReceipt(ctx, client, signed.Hash())
}

func signDynamicFeeTx(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address, to *common.Address, value *big.Int, payload []byte) (*types.Transaction, error) {
	chainID, err := client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("chain id: %w", err)
	}
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("nonce of %s: %w", from.Hex(), err)
	}
	gasFeeCap, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("gas price: %w", err)
	}
	gasTipCap, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		return nil, fmt.Errorf("gas tip: %w", err)
	}
	gasFeeCap = new(big.Int).Mul(gasFeeCap, big.NewInt(4))
	if gasFeeCap.Cmp(gasTipCap) < 0 {
		gasFeeCap = gasTipCap
	}
	gas, err := client.EstimateGas(ctx, qrl.CallMsg{
		From:  from,
		To:    to,
		Value: value,
		Data:  payload,
	})
	if err != nil {
		return nil, fmt.Errorf("estimate gas: %w", err)
	}
	gas += gas / 5

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gas,
		To:        to,
		Value:     value,
		Data:      payload,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), w)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}
	return signed, nil
}

func storageContractCode(value [common.StorageValue64Length]byte) []byte {
	runtime := []byte{
		byte(vm.PUSH1), 0x00,
		byte(vm.SLOAD),
		byte(vm.PUSH1), 0x00,
		byte(vm.MSTORE),
		byte(vm.PUSH1), common.StorageValue64Length,
		byte(vm.PUSH1), 0x00,
		byte(vm.RETURN),
	}
	code := []byte{byte(vm.PUSH64)}
	code = append(code, value[:]...)
	code = append(code, byte(vm.PUSH1), 0x00, byte(vm.SSTORE))
	code = append(code, byte(vm.PUSH1)+byte(len(runtime))-1)
	code = append(code, runtime...)
	code = append(code,
		byte(vm.PUSH1), 0x00,
		byte(vm.MSTORE),
		byte(vm.PUSH1), byte(len(runtime)),
		byte(vm.PUSH1), byte(common.StorageValue64Length-len(runtime)),
		byte(vm.RETURN),
	)
	return code
}

func checkGraphQLEventLog(ctx context.Context, graphqlURL string, deployment *eventDeployment) error {
	blockNumber := deployment.receipt.BlockNumber.String()
	query := fmt.Sprintf(`{
		logs(filter:{fromBlock:"%s",toBlock:"%s",addresses:["%s"],topics:[["%s"]]}) {
			account { address }
			topics
			data
			transaction { hash }
		}
	}`, blockNumber, blockNumber, deployment.address.Hex(), deployment.topic.Hex())
	var response struct {
		Logs []struct {
			Account struct {
				Address string `json:"address"`
			} `json:"account"`
			Topics      []string `json:"topics"`
			Data        string   `json:"data"`
			Transaction struct {
				Hash string `json:"hash"`
			} `json:"transaction"`
		} `json:"logs"`
	}
	if err := postGraphQL(ctx, graphqlURL, query, &response); err != nil {
		return fmt.Errorf("graphql logs query: %w", err)
	}
	if len(response.Logs) != 1 {
		return fmt.Errorf("graphql logs length mismatch: have %d want 1", len(response.Logs))
	}
	log := response.Logs[0]
	if log.Account.Address != deployment.address.Hex() {
		return fmt.Errorf("graphql log address mismatch: have %s want %s", log.Account.Address, deployment.address.Hex())
	}
	if len(log.Topics) != 1 || log.Topics[0] != deployment.topic.Hex() {
		return fmt.Errorf("graphql log topics mismatch: have %v want %s", log.Topics, deployment.topic.Hex())
	}
	wantData := hexutil.Encode(deployment.receipt.Logs[0].Data)
	if log.Data != wantData {
		return fmt.Errorf("graphql log data mismatch: have %s want %s", log.Data, wantData)
	}
	if log.Transaction.Hash != deployment.tx.Hash().Hex() {
		return fmt.Errorf("graphql log tx hash mismatch: have %s want %s", log.Transaction.Hash, deployment.tx.Hash().Hex())
	}
	return nil
}

func checkGraphQLStorage(ctx context.Context, graphqlURL string, contract common.Address, block *big.Int, from common.Address, slot common.Hash, value [common.StorageValue64Length]byte) error {
	query := fmt.Sprintf(`{
		block(number:"%s") {
			account(address:"%s") {
				storage(slot:"%s")
			}
			call(data:{from:"%s",to:"%s",data:"0x"}) {
				data
				status
			}
		}
	}`, block.String(), contract.Hex(), slot.Hex(), from.Hex(), contract.Hex())
	var response struct {
		Block struct {
			Account struct {
				Storage string `json:"storage"`
			} `json:"account"`
			Call struct {
				Data   string `json:"data"`
				Status string `json:"status"`
			} `json:"call"`
		} `json:"block"`
	}
	if err := postGraphQL(ctx, graphqlURL, query, &response); err != nil {
		return fmt.Errorf("graphql storage query: %w", err)
	}
	want := hexutil.Encode(value[:])
	if response.Block.Account.Storage != want {
		return fmt.Errorf("graphql storage mismatch: have %s want %s", response.Block.Account.Storage, want)
	}
	if response.Block.Call.Status != "0x1" {
		return fmt.Errorf("graphql call status mismatch: have %s want 0x1", response.Block.Call.Status)
	}
	if response.Block.Call.Data != want {
		return fmt.Errorf("graphql call data mismatch: have %s want %s", response.Block.Call.Data, want)
	}
	return nil
}

func checkGraphQLSendRawTransaction(ctx context.Context, graphqlURL string, client *qrlclient.Client, w wallet.Wallet, from common.Address) error {
	signed, err := signDynamicFeeTx(ctx, client, w, from, &from, big.NewInt(0), nil)
	if err != nil {
		return fmt.Errorf("sign graphql raw transaction: %w", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		return fmt.Errorf("marshal graphql raw transaction: %w", err)
	}
	query := fmt.Sprintf(`mutation {
		sendRawTransaction(data:"%s")
	}`, hexutil.Encode(raw))
	var response struct {
		SendRawTransaction string `json:"sendRawTransaction"`
	}
	if err := postGraphQL(ctx, graphqlURL, query, &response); err != nil {
		return fmt.Errorf("graphql sendRawTransaction mutation: %w", err)
	}
	if response.SendRawTransaction != signed.Hash().Hex() {
		return fmt.Errorf("graphql sendRawTransaction hash mismatch: have %s want %s", response.SendRawTransaction, signed.Hash().Hex())
	}
	receipt, err := waitReceipt(ctx, client, signed.Hash())
	if err != nil {
		return err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("graphql sendRawTransaction failed with status %d", receipt.Status)
	}
	return nil
}

func postGraphQL(ctx context.Context, endpoint, query string, out any) error {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": nil,
	})
	if err != nil {
		return err
	}
	url := strings.TrimRight(endpoint, "/")
	if !strings.HasSuffix(url, "/graphql") {
		url += "/graphql"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %s: %s", resp.Status, responseBody)
	}
	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return err
	}
	if len(envelope.Errors) != 0 {
		return fmt.Errorf("graphql errors: %+v", envelope.Errors)
	}
	if len(envelope.Data) == 0 {
		return fmt.Errorf("graphql response missing data: %s", responseBody)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return err
	}
	return nil
}

func normalizeHex(input string) string {
	return strings.TrimPrefix(strings.ToLower(input), "0x")
}

func uint64Ptr(v uint64) *uint64 {
	return &v
}

func waitReceipt(ctx context.Context, client *qrlclient.Client, txHash common.Hash) (*types.Receipt, error) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		receipt, err := client.TransactionReceipt(ctx, txHash)
		if err == nil && receipt != nil && receipt.BlockNumber != nil {
			return receipt, nil
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("wait for receipt %s: %w", txHash.Hex(), ctx.Err())
		case <-ticker.C:
		}
	}
}

func methodID(signature string) []byte {
	return crypto.Keccak256([]byte(signature))[:4]
}

func concat(parts ...[]byte) []byte {
	var size int
	for _, part := range parts {
		size += len(part)
	}
	out := make([]byte, 0, size)
	for _, part := range parts {
		out = append(out, part...)
	}
	return out
}

func hashTopic(hash common.Hash) common.LogTopic {
	var topic common.LogTopic
	copy(topic[:common.HashLength], hash[:])
	return topic
}

func word(hex string) []byte {
	raw := common.FromHex(hex)
	out := make([]byte, common.LogTopicLength)
	copy(out[len(out)-len(raw):], raw)
	return out
}

func fixedBytes(hex string) []byte {
	raw := common.FromHex(hex)
	out := make([]byte, common.LogTopicLength)
	copy(out, raw)
	return out
}
