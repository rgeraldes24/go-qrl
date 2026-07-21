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
	"crypto/sha256"
	"encoding/binary"
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
	"github.com/theQRL/go-qrl/crypto/pqcrypto"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
	"github.com/theQRL/go-qrl/qrlclient/gqrlclient"
	"github.com/theQRL/go-qrl/qrldb/memorydb"
	"github.com/theQRL/go-qrl/rlp"
	"github.com/theQRL/go-qrl/trie"
)

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
	// This suite intentionally serializes multiple mined transactions so each
	// assertion has an unambiguous historical state root. Leave enough room for
	// transient missed slots without turning a stalled chain into an endless CI
	// job.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
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
	if err := checkAddressUpperHalfIsolation(ctx, client, w, from); err != nil {
		return err
	}
	if err := checkLivePrecompiles(ctx, client, from); err != nil {
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
	if err := checkLiveVM64Contract(ctx, client, w, from, deployment); err != nil {
		return err
	}
	return nil
}

func checkLiveVM64Contract(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address, deployment *eventDeployment) error {
	amount := new(big.Int).Lsh(big.NewInt(1), 511)
	amount.Add(amount, big.NewInt(0x1234))
	delta := new(big.Int).Lsh(big.NewInt(1), 510)
	delta.Neg(delta)
	delta.Add(delta, big.NewInt(42))

	var tag [64]byte
	for i := range tag {
		tag[i] = byte(0x80 + i)
	}
	payload := make([]byte, 129)
	for i := range payload {
		payload[i] = byte((i*29 + 7) & 0xff)
	}
	note := "VM64 string crosses the 64-byte ABI word boundary: 0123456789abcdef0123456789abcdef"
	callOpts := &bind.CallOpts{Context: ctx, BlockNumber: deployment.receipt.BlockNumber}

	echoAmount, echoDelta, echoTag, echoRecipient, echoPayload, echoNote, echoEnabled, err := deployment.binding.Echo(
		callOpts, amount, delta, tag, from, payload, note, true,
	)
	if err != nil {
		return fmt.Errorf("VM64 echo through generated binding: %w", err)
	}
	if echoAmount.Cmp(amount) != 0 || echoDelta.Cmp(delta) != 0 || echoTag != tag || echoRecipient != from ||
		!bytes.Equal(echoPayload, payload) || echoNote != note || !echoEnabled {
		return fmt.Errorf("VM64 echo mismatch: amount=%x delta=%x tag=%x recipient=%s payload=%x note=%q enabled=%t",
			echoAmount, echoDelta, echoTag, echoRecipient, echoPayload, echoNote, echoEnabled)
	}

	value1 := [1]byte{0xa5}
	var value32 [32]byte
	var value33 [33]byte
	for i := range value32 {
		value32[i] = byte(i + 1)
	}
	for i := range value33 {
		value33[i] = byte(0x40 + i)
	}
	fixed1, fixed32, fixed33, fixed64, err := deployment.binding.EchoFixed(callOpts, value1, value32, value33, tag)
	if err != nil {
		return fmt.Errorf("VM64 fixed-bytes echo through generated binding: %w", err)
	}
	if fixed1 != value1 || fixed32 != value32 || fixed33 != value33 || fixed64 != tag {
		return fmt.Errorf("VM64 fixed-bytes echo mismatch")
	}

	arrayValues := []*big.Int{big.NewInt(0), big.NewInt(1), amount}
	arrayTags := [2][64]byte{tag, {}}
	copy(arrayTags[1][:], payload[:64])
	gotValues, gotTags, err := deployment.binding.EchoArrays(callOpts, arrayValues, arrayTags)
	if err != nil {
		return fmt.Errorf("VM64 array echo through generated binding: %w", err)
	}
	if len(gotValues) != len(arrayValues) || gotTags != arrayTags {
		return fmt.Errorf("VM64 array echo shape mismatch: values=%d tags=%x", len(gotValues), gotTags)
	}
	for i := range arrayValues {
		if gotValues[i].Cmp(arrayValues[i]) != 0 {
			return fmt.Errorf("VM64 array echo value %d mismatch: have %x want %x", i, gotValues[i], arrayValues[i])
		}
	}

	record := EventEmitterRecord{Amount: amount, Recipient: from, Tag: tag}
	gotRecord, err := deployment.binding.EchoRecord(callOpts, record)
	if err != nil {
		return fmt.Errorf("VM64 tuple echo through generated binding: %w", err)
	}
	if gotRecord.Amount.Cmp(record.Amount) != 0 || gotRecord.Recipient != record.Recipient || gotRecord.Tag != record.Tag {
		return fmt.Errorf("VM64 tuple echo mismatch: %+v", gotRecord)
	}

	auth, err := newTransactor(ctx, client, w, from)
	if err != nil {
		return err
	}
	tx, err := deployment.binding.Store(auth, amount, delta, tag, from, payload, note, true)
	if err != nil {
		return fmt.Errorf("VM64 store through generated binding: %w", err)
	}
	receipt, err := waitReceipt(ctx, client, tx.Hash())
	if err != nil {
		return err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("VM64 store failed with status %d", receipt.Status)
	}
	if len(receipt.Logs) != 3 {
		return fmt.Errorf("VM64 store emitted %d logs, want 3", len(receipt.Logs))
	}

	read, err := deployment.binding.Read(&bind.CallOpts{Context: ctx, BlockNumber: receipt.BlockNumber})
	if err != nil {
		return fmt.Errorf("VM64 read through generated binding: %w", err)
	}
	if read.Amount.Cmp(amount) != 0 || read.Delta.Cmp(delta) != 0 || read.Tag != tag || read.Recipient != from ||
		!bytes.Equal(read.Payload, payload) || read.Note != note || !read.Enabled {
		return fmt.Errorf("VM64 stored state mismatch: %+v", read)
	}
	if err := checkVM64StorageSlots(ctx, client, deployment.address, receipt.BlockNumber, amount, delta, tag, from); err != nil {
		return err
	}
	if err := checkVM64EventLogs(ctx, deployment.binding, receipt, from, amount, delta, tag, payload, note); err != nil {
		return err
	}

	auth, err = newTransactor(ctx, client, w, from)
	if err != nil {
		return err
	}
	clearTx, err := deployment.binding.Clear(auth)
	if err != nil {
		return fmt.Errorf("VM64 clear through generated binding: %w", err)
	}
	clearReceipt, err := waitReceipt(ctx, client, clearTx.Hash())
	if err != nil {
		return err
	}
	if clearReceipt.Status != types.ReceiptStatusSuccessful {
		return fmt.Errorf("VM64 clear failed with status %d", clearReceipt.Status)
	}
	cleared, err := deployment.binding.Read(&bind.CallOpts{Context: ctx, BlockNumber: clearReceipt.BlockNumber})
	if err != nil {
		return fmt.Errorf("VM64 read after clear: %w", err)
	}
	if cleared.Amount.Sign() != 0 || cleared.Delta.Sign() != 0 || cleared.Tag != ([64]byte{}) ||
		cleared.Recipient != (common.Address{}) || len(cleared.Payload) != 0 || cleared.Note != "" || cleared.Enabled {
		return fmt.Errorf("VM64 clear left non-zero state: %+v", cleared)
	}
	return nil
}

func checkVM64StorageSlots(ctx context.Context, client *qrlclient.Client, contract common.Address, block *big.Int, amount, delta *big.Int, tag [64]byte, recipient common.Address) error {
	want := []common.StorageValue64{
		common.BytesToStorageValue64(unsignedWord(amount)),
		common.BytesToStorageValue64(signedWord(delta)),
		common.StorageValue64(tag),
		common.StorageValue64(recipient),
	}
	for i, expected := range want {
		slot := common.BigToHash(big.NewInt(int64(i)))
		value, err := client.StorageAt(ctx, contract, slot, block)
		if err != nil {
			return fmt.Errorf("read VM64 storage slot %d: %w", i, err)
		}
		if !bytes.Equal(value, expected[:]) {
			return fmt.Errorf("VM64 storage slot %d mismatch:\nhave %x\nwant %x", i, value, expected)
		}
	}
	return nil
}

func checkVM64EventLogs(ctx context.Context, binding *EventEmitter, receipt *types.Receipt, recipient common.Address, amount, delta *big.Int, tag [64]byte, payload []byte, note string) error {
	parsed, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return fmt.Errorf("parse VM64 event ABI: %w", err)
	}
	storedLog, dynamicLog, anonymousLog := receipt.Logs[0], receipt.Logs[1], receipt.Logs[2]

	storedTopic := hashTopic(parsed.Events["Stored"].ID)
	if len(storedLog.Topics) != 4 || storedLog.Topics[0] != storedTopic ||
		storedLog.Topics[1] != bytesTopic(recipient[:]) ||
		storedLog.Topics[2] != bytesTopic(unsignedWord(amount)) ||
		storedLog.Topics[3] != bytesTopic(signedWord(delta)) {
		return fmt.Errorf("VM64 Stored topics mismatch: %v", storedLog.Topics)
	}
	stored, err := binding.ParseStored(*storedLog)
	if err != nil {
		return fmt.Errorf("parse VM64 Stored event through generated binding: %w", err)
	}
	if stored.Recipient != recipient || stored.Amount.Cmp(amount) != 0 || stored.Delta.Cmp(delta) != 0 ||
		stored.Tag != tag || !bytes.Equal(stored.Payload, payload) || stored.Note != note || !stored.Enabled {
		return fmt.Errorf("decoded VM64 Stored event mismatch: %+v", stored)
	}
	storedIt, err := binding.FilterStored(&bind.FilterOpts{
		Start: receipt.BlockNumber.Uint64(), End: uint64Ptr(receipt.BlockNumber.Uint64()), Context: ctx,
	}, []common.Address{recipient}, []*big.Int{amount}, []*big.Int{delta})
	if err != nil {
		return fmt.Errorf("filter VM64 Stored event through generated binding: %w", err)
	}
	defer storedIt.Close()
	if !storedIt.Next() || storedIt.Event.Raw.TxHash != receipt.TxHash || storedIt.Event.Delta.Cmp(delta) != 0 {
		if err := storedIt.Error(); err != nil {
			return fmt.Errorf("iterate VM64 Stored event: %w", err)
		}
		return fmt.Errorf("filtered VM64 Stored event mismatch")
	}
	if storedIt.Next() {
		return fmt.Errorf("VM64 Stored filter returned more than one event")
	}
	if err := storedIt.Error(); err != nil {
		return fmt.Errorf("finish VM64 Stored event iterator: %w", err)
	}

	dynamicTopic := hashTopic(parsed.Events["Dynamic"].ID)
	payloadHash := crypto.Keccak256Hash(payload)
	noteHash := crypto.Keccak256Hash([]byte(note))
	if len(dynamicLog.Topics) != 3 || dynamicLog.Topics[0] != dynamicTopic ||
		dynamicLog.Topics[1] != hashTopic(payloadHash) || dynamicLog.Topics[2] != hashTopic(noteHash) {
		return fmt.Errorf("VM64 Dynamic topics mismatch: %v", dynamicLog.Topics)
	}
	dynamic, err := binding.ParseDynamic(*dynamicLog)
	if err != nil {
		return fmt.Errorf("parse VM64 Dynamic event through generated binding: %w", err)
	}
	if dynamic.Payload != payloadHash || dynamic.Note != noteHash || dynamic.Amount.Cmp(amount) != 0 {
		return fmt.Errorf("decoded VM64 Dynamic event mismatch: %+v", dynamic)
	}
	dynamicIt, err := binding.FilterDynamic(&bind.FilterOpts{
		Start: receipt.BlockNumber.Uint64(), End: uint64Ptr(receipt.BlockNumber.Uint64()), Context: ctx,
	}, [][]byte{payload}, []string{note})
	if err != nil {
		return fmt.Errorf("filter VM64 Dynamic event through generated binding: %w", err)
	}
	defer dynamicIt.Close()
	if !dynamicIt.Next() || dynamicIt.Event.Raw.TxHash != receipt.TxHash {
		if err := dynamicIt.Error(); err != nil {
			return fmt.Errorf("iterate VM64 Dynamic event: %w", err)
		}
		return fmt.Errorf("filtered VM64 Dynamic event mismatch")
	}
	if dynamicIt.Next() {
		return fmt.Errorf("VM64 Dynamic filter returned more than one event")
	}
	if err := dynamicIt.Error(); err != nil {
		return fmt.Errorf("finish VM64 Dynamic event iterator: %w", err)
	}

	wantAnonymous := []common.LogTopic{
		bytesTopic(recipient[:]),
		bytesTopic(unsignedWord(amount)),
		bytesTopic(tag[:]),
		bytesTopic(unsignedWord(big.NewInt(1))),
	}
	if len(anonymousLog.Topics) != len(wantAnonymous) {
		return fmt.Errorf("VM64 anonymous event topic count mismatch: have %d want %d", len(anonymousLog.Topics), len(wantAnonymous))
	}
	for i := range wantAnonymous {
		if anonymousLog.Topics[i] != wantAnonymous[i] {
			return fmt.Errorf("VM64 anonymous event topic %d mismatch: have %s want %s", i, anonymousLog.Topics[i], wantAnonymous[i])
		}
	}
	if len(anonymousLog.Data) != 0 {
		return fmt.Errorf("VM64 anonymous event unexpectedly has data: %x", anonymousLog.Data)
	}
	return nil
}

func newTransactor(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address) (*bind.TransactOpts, error) {
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
	return auth, nil
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
	absentSlot := common.BigToHash(big.NewInt(0xdead))
	proof, err := gqrlclient.New(client.Client()).GetProof(ctx, receipt.ContractAddress, []string{slot.Hex(), absentSlot.Hex()}, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("qrl_getProof through gqrlclient: %w", err)
	}
	if len(proof.StorageProof) != 2 {
		return fmt.Errorf("storage proof length mismatch: have %d want 2", len(proof.StorageProof))
	}
	if proof.StorageProof[0].Key != slot.Hex() {
		return fmt.Errorf("storage proof key mismatch: have %s want %s", proof.StorageProof[0].Key, slot.Hex())
	}
	if proof.StorageProof[0].Value.Cmp(new(big.Int).SetBytes(value[:])) != 0 {
		return fmt.Errorf("storage proof value mismatch: have %s want 0x%x", proof.StorageProof[0].Value.Text(16), value)
	}
	if proof.StorageProof[1].Key != absentSlot.Hex() || proof.StorageProof[1].Value.Sign() != 0 {
		return fmt.Errorf("absent storage proof mismatch: %+v", proof.StorageProof[1])
	}

	header, err := client.HeaderByNumber(ctx, receipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("header for proof block: %w", err)
	}
	accountLeaf, err := verifyProofNodes(header.Root, crypto.Keccak256(receipt.ContractAddress.Bytes()), proof.AccountProof)
	if err != nil {
		return fmt.Errorf("verify account proof: %w", err)
	}
	if accountLeaf == nil {
		return fmt.Errorf("verified account proof returned an absent contract")
	}
	var account types.StateAccount
	if err := rlp.DecodeBytes(accountLeaf, &account); err != nil {
		return fmt.Errorf("decode account proof leaf: %w", err)
	}
	if account.Nonce != proof.Nonce || account.Balance.Cmp(proof.Balance) != 0 || account.Root != proof.StorageHash ||
		!bytes.Equal(account.CodeHash, proof.CodeHash[:]) {
		return fmt.Errorf("verified account leaf differs from RPC result: account=%+v proof=%+v", account, proof)
	}

	storageLeaf, err := verifyProofNodes(proof.StorageHash, crypto.Keccak256(slot.Bytes()), proof.StorageProof[0].Proof)
	if err != nil {
		return fmt.Errorf("verify storage inclusion proof: %w", err)
	}
	if storageLeaf == nil {
		return fmt.Errorf("verified storage proof returned an absent populated slot")
	}
	var trimmedStorage []byte
	if err := rlp.DecodeBytes(storageLeaf, &trimmedStorage); err != nil {
		return fmt.Errorf("decode storage proof leaf: %w", err)
	}
	if got := common.BytesToStorageValue64(trimmedStorage); got != common.StorageValue64(value) {
		return fmt.Errorf("verified storage leaf mismatch: have %x want %x", got, value)
	}

	absentLeaf, err := verifyProofNodes(proof.StorageHash, crypto.Keccak256(absentSlot.Bytes()), proof.StorageProof[1].Proof)
	if err != nil {
		return fmt.Errorf("verify storage absence proof: %w", err)
	}
	if absentLeaf != nil {
		return fmt.Errorf("verified storage absence proof returned value %x", absentLeaf)
	}
	tampered := append([]string(nil), proof.AccountProof...)
	if len(tampered) == 0 {
		return fmt.Errorf("account proof unexpectedly has no nodes")
	}
	tamperedNode, err := hexutil.Decode(tampered[0])
	if err != nil || len(tamperedNode) == 0 {
		return fmt.Errorf("decode account proof node for tamper check: %w", err)
	}
	tamperedNode[len(tamperedNode)/2] ^= 0x01
	tampered[0] = hexutil.Encode(tamperedNode)
	if _, err := verifyProofNodes(header.Root, crypto.Keccak256(receipt.ContractAddress.Bytes()), tampered); err == nil {
		return fmt.Errorf("tampered account proof unexpectedly verified")
	}
	if graphqlURL != "" {
		if err := checkGraphQLStorage(ctx, graphqlURL, receipt.ContractAddress, receipt.BlockNumber, from, slot, value); err != nil {
			return err
		}
	}
	return nil
}

func verifyProofNodes(root common.Hash, key []byte, nodes []string) ([]byte, error) {
	db := memorydb.New()
	defer db.Close()
	for i, encoded := range nodes {
		node, err := hexutil.Decode(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode proof node %d: %w", i, err)
		}
		if err := db.Put(crypto.Keccak256(node), node); err != nil {
			return nil, fmt.Errorf("store proof node %d: %w", i, err)
		}
	}
	return trie.VerifyProof(root, key, db)
}

func checkAddressUpperHalfIsolation(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from common.Address) error {
	// Derive fresh collision fixtures from the sender's current nonce. A fixed
	// pair makes this otherwise-valid state test fail on every second run against
	// the same developer network because both recipients are already funded.
	// Advancing the sender nonce also makes a retry after any submitted transfer
	// select a new pair while retaining deterministic, reproducible derivation.
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return fmt.Errorf("upper-half address fixture nonce: %w", err)
	}
	first, second := upperHalfIsolationAddresses(from, nonce)
	if bytes.Equal(first[:common.AddressLength/2], second[:common.AddressLength/2]) ||
		!bytes.Equal(first[common.AddressLength/2:], second[common.AddressLength/2:]) {
		return fmt.Errorf("invalid upper-half address collision fixture")
	}
	if crypto.Keccak256Hash(first.Bytes()) == crypto.Keccak256Hash(second.Bytes()) {
		return fmt.Errorf("full-width address trie keys unexpectedly collide")
	}

	firstValue := big.NewInt(1111)
	secondValue := big.NewInt(2222)
	firstReceipt, err := sendValue(ctx, client, w, from, first, firstValue)
	if err != nil {
		return fmt.Errorf("fund first upper-half address: %w", err)
	}
	firstHeader, err := client.HeaderByNumber(ctx, firstReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("header after first upper-half address funding: %w", err)
	}
	firstAtFirstBlock, err := client.BalanceAt(ctx, first, firstReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("first upper-half address balance: %w", err)
	}
	secondAtFirstBlock, err := client.BalanceAt(ctx, second, firstReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("second upper-half address pre-funding balance: %w", err)
	}
	if firstAtFirstBlock.Cmp(firstValue) != 0 || secondAtFirstBlock.Sign() != 0 {
		return fmt.Errorf("upper-half address isolation failed before second funding: first=%s second=%s", firstAtFirstBlock, secondAtFirstBlock)
	}

	proofClient := gqrlclient.New(client.Client())
	firstProof, err := proofClient.GetProof(ctx, first, nil, firstReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("first upper-half address proof: %w", err)
	}
	secondAbsentProof, err := proofClient.GetProof(ctx, second, nil, firstReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("second upper-half address absence proof: %w", err)
	}
	if err := verifyAccountProof(firstHeader.Root, first, firstProof, firstValue, true); err != nil {
		return fmt.Errorf("verify first upper-half address proof: %w", err)
	}
	if err := verifyAccountProof(firstHeader.Root, second, secondAbsentProof, new(big.Int), false); err != nil {
		return fmt.Errorf("verify second upper-half address absence proof: %w", err)
	}

	secondReceipt, err := sendValue(ctx, client, w, from, second, secondValue)
	if err != nil {
		return fmt.Errorf("fund second upper-half address: %w", err)
	}
	secondHeader, err := client.HeaderByNumber(ctx, secondReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("header after second upper-half address funding: %w", err)
	}
	if secondHeader.Root == firstHeader.Root {
		return fmt.Errorf("state root did not change after funding the second upper-half address")
	}
	firstBalance, err := client.BalanceAt(ctx, first, secondReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("first upper-half address final balance: %w", err)
	}
	secondBalance, err := client.BalanceAt(ctx, second, secondReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("second upper-half address final balance: %w", err)
	}
	if firstBalance.Cmp(firstValue) != 0 || secondBalance.Cmp(secondValue) != 0 {
		return fmt.Errorf("64-byte addresses with equal low halves aliased: first=%s second=%s", firstBalance, secondBalance)
	}

	firstProof, err = proofClient.GetProof(ctx, first, nil, secondReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("first upper-half address final proof: %w", err)
	}
	secondProof, err := proofClient.GetProof(ctx, second, nil, secondReceipt.BlockNumber)
	if err != nil {
		return fmt.Errorf("second upper-half address final proof: %w", err)
	}
	if err := verifyAccountProof(secondHeader.Root, first, firstProof, firstValue, true); err != nil {
		return fmt.Errorf("verify first upper-half address final proof: %w", err)
	}
	if err := verifyAccountProof(secondHeader.Root, second, secondProof, secondValue, true); err != nil {
		return fmt.Errorf("verify second upper-half address final proof: %w", err)
	}
	return nil
}

func upperHalfIsolationAddresses(from common.Address, nonce uint64) (first, second common.Address) {
	var encodedNonce [8]byte
	binary.BigEndian.PutUint64(encodedNonce[:], nonce)
	seed := crypto.Keccak256(
		[]byte("go-qrl/vm64-address-isolation/v1"),
		from.Bytes(),
		encodedNonce[:],
	)
	firstUpper := crypto.Keccak256(seed, []byte("first-upper"))
	secondUpper := crypto.Keccak256(seed, []byte("second-upper"))
	sharedLower := crypto.Keccak256(seed, []byte("shared-lower"))
	copy(first[:common.AddressLength/2], firstUpper)
	copy(second[:common.AddressLength/2], secondUpper)
	copy(first[common.AddressLength/2:], sharedLower)
	copy(second[common.AddressLength/2:], sharedLower)
	return first, second
}

func verifyAccountProof(root common.Hash, address common.Address, proof *gqrlclient.AccountResult, wantBalance *big.Int, wantExists bool) error {
	if proof.Address != address || proof.Balance.Cmp(wantBalance) != 0 {
		return fmt.Errorf("RPC proof identity mismatch: address=%s balance=%s", proof.Address, proof.Balance)
	}
	leaf, err := verifyProofNodes(root, crypto.Keccak256(address.Bytes()), proof.AccountProof)
	if err != nil {
		return err
	}
	if !wantExists {
		if leaf != nil {
			return fmt.Errorf("absence proof returned account leaf %x", leaf)
		}
		return nil
	}
	if leaf == nil {
		return fmt.Errorf("inclusion proof returned no account leaf")
	}
	var account types.StateAccount
	if err := rlp.DecodeBytes(leaf, &account); err != nil {
		return fmt.Errorf("decode account leaf: %w", err)
	}
	if account.Balance.Cmp(wantBalance) != 0 || account.Nonce != proof.Nonce || account.Root != proof.StorageHash ||
		!bytes.Equal(account.CodeHash, proof.CodeHash[:]) {
		return fmt.Errorf("account leaf differs from RPC proof: account=%+v proof=%+v", account, proof)
	}
	return nil
}

func sendValue(ctx context.Context, client *qrlclient.Client, w wallet.Wallet, from, to common.Address, value *big.Int) (*types.Receipt, error) {
	signed, err := signDynamicFeeTx(ctx, client, w, from, &to, value, nil)
	if err != nil {
		return nil, err
	}
	if err := client.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("send tx %s: %w", signed.Hash().Hex(), err)
	}
	receipt, err := waitReceipt(ctx, client, signed.Hash())
	if err != nil {
		return nil, err
	}
	if receipt.Status != types.ReceiptStatusSuccessful {
		return nil, fmt.Errorf("value transfer %s failed with status %d", signed.Hash(), receipt.Status)
	}
	return receipt, nil
}

const vm64DepositRootExpected = "0033398ac7d5822aba0b3f614e7728940a9597e122ddd462fe3b5c7c458a3d1a"

func vm64DepositRootInput() []byte {
	const amountLength = 8
	publicKeyOffset := 0
	withdrawalRecipientOffset := publicKeyOffset + pqcrypto.MLDSA87PublicKeyLength
	amountOffset := withdrawalRecipientOffset + common.AddressLength
	signatureOffset := amountOffset + amountLength
	input := make([]byte, signatureOffset+pqcrypto.MLDSA87SignatureLength)

	for i := 0; i < pqcrypto.MLDSA87PublicKeyLength; i++ {
		input[publicKeyOffset+i] = byte(i*17 + 3)
	}
	// Populate both halves. In particular, the nonzero first half makes this
	// vector detect a withdrawal-recipient regression back to 32 bytes.
	for i := 0; i < common.AddressLength/2; i++ {
		input[withdrawalRecipientOffset+i] = byte(0xa0 + i)
		input[withdrawalRecipientOffset+common.AddressLength/2+i] = byte(0x30 + i)
	}
	binary.LittleEndian.PutUint64(input[amountOffset:signatureOffset], 32_000_000_000)
	for i := 0; i < pqcrypto.MLDSA87SignatureLength; i++ {
		input[signatureOffset+i] = byte(i*31 + 7)
	}
	return input
}

func legacyDepositRootInput() []byte {
	const (
		legacyWithdrawalRecipientLength = 32
		legacySignatureLength           = pqcrypto.MLDSA87SignatureLength - 32
		amountLength                    = 8
	)
	valid := vm64DepositRootInput()
	withdrawalRecipientOffset := pqcrypto.MLDSA87PublicKeyLength
	amountOffset := withdrawalRecipientOffset + common.AddressLength
	signatureOffset := amountOffset + amountLength
	return concat(
		valid[:withdrawalRecipientOffset],
		valid[withdrawalRecipientOffset+common.AddressLength-legacyWithdrawalRecipientLength:amountOffset],
		valid[amountOffset:signatureOffset],
		valid[signatureOffset:signatureOffset+legacySignatureLength],
	)
}

func checkLivePrecompiles(ctx context.Context, client *qrlclient.Client, from common.Address) error {
	call := func(address byte, input []byte) ([]byte, error) {
		to := common.BytesToAddress([]byte{address})
		return client.CallContract(ctx, qrl.CallMsg{From: from, To: &to, Data: input}, nil)
	}

	depositInput := vm64DepositRootInput()
	got, err := call(1, depositInput)
	if err != nil {
		return fmt.Errorf("live VM64 deposit-root precompile at 0x01: %w", err)
	}
	wantDepositRoot := common.Hex2Bytes(vm64DepositRootExpected)
	if !bytes.Equal(got, wantDepositRoot) {
		return fmt.Errorf("live VM64 deposit-root precompile mismatch: have %x want %x", got, wantDepositRoot)
	}
	legacyInput := legacyDepositRootInput()
	legacyRoot, err := call(1, legacyInput)
	if err != nil {
		return fmt.Errorf("live legacy-width deposit-root compatibility call: %w", err)
	}
	paddedLegacy := append(append([]byte(nil), legacyInput...), make([]byte, len(depositInput)-len(legacyInput))...)
	paddedLegacyRoot, err := call(1, paddedLegacy)
	if err != nil {
		return fmt.Errorf("live padded legacy-width deposit-root call: %w", err)
	}
	if !bytes.Equal(legacyRoot, paddedLegacyRoot) {
		return fmt.Errorf("live legacy-width compatibility root mismatch: have %x want padded %x", legacyRoot, paddedLegacyRoot)
	}
	extendedRoot, err := call(1, append(append([]byte(nil), depositInput...), 0xff))
	if err != nil {
		return fmt.Errorf("live extended deposit-root compatibility call: %w", err)
	}
	if !bytes.Equal(extendedRoot, got) {
		return fmt.Errorf("live extended deposit-root root mismatch: have %x want canonical %x", extendedRoot, got)
	}

	input := make([]byte, 129)
	for i := range input {
		input[i] = byte((i*17 + 3) & 0xff)
	}
	wantSHA := sha256.Sum256(input)
	got, err = call(2, input)
	if err != nil {
		return fmt.Errorf("live SHA-256 precompile at 0x02: %w", err)
	}
	if !bytes.Equal(got, wantSHA[:]) {
		return fmt.Errorf("live SHA-256 precompile mismatch: have %x want %x", got, wantSHA)
	}

	got, err = call(4, input)
	if err != nil {
		return fmt.Errorf("live identity precompile at 0x04: %w", err)
	}
	if !bytes.Equal(got, input) {
		return fmt.Errorf("live identity precompile mismatch: have %x want %x", got, input)
	}
	legacy, err := call(3, input)
	if err != nil {
		return fmt.Errorf("call inactive legacy precompile address 0x03: %w", err)
	}
	if len(legacy) != 0 {
		return fmt.Errorf("inactive legacy precompile address 0x03 returned %x", legacy)
	}

	modExpInput := concat(
		common.LeftPadBytes([]byte{1}, 32),
		common.LeftPadBytes([]byte{1}, 32),
		common.LeftPadBytes([]byte{1}, 32),
		[]byte{2, 5, 13},
	)
	got, err = call(5, modExpInput)
	if err != nil {
		return fmt.Errorf("live modular-exponentiation precompile at 0x05: %w", err)
	}
	if !bytes.Equal(got, []byte{6}) {
		return fmt.Errorf("live modular-exponentiation precompile mismatch: have %x want 06", got)
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

func bytesTopic(input []byte) common.LogTopic {
	var topic common.LogTopic
	copy(topic[:], input)
	return topic
}

func unsignedWord(value *big.Int) []byte {
	return common.LeftPadBytes(value.Bytes(), common.LogTopicLength)
}

func signedWord(value *big.Int) []byte {
	encoded := new(big.Int).Set(value)
	if encoded.Sign() < 0 {
		encoded.Add(encoded, new(big.Int).Lsh(big.NewInt(1), common.LogTopicLength*8))
	}
	return unsignedWord(encoded)
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
