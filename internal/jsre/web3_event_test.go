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

package jsre

import (
	"bytes"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"testing"

	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/common"
)

func TestEmbeddedWeb3EventsUseVM64Topics(t *testing.T) {
	t.Parallel()

	const contractABI = `[
  {
    "anonymous": false,
    "inputs": [
      {"indexed": true, "name": "from", "type": "address"},
      {"indexed": true, "name": "label", "type": "string"},
      {"indexed": true, "name": "payload", "type": "bytes"},
      {"indexed": false, "name": "amount", "type": "uint512"}
    ],
    "name": "Transfer",
    "type": "event"
  }
]`

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	indexedAddress := "Q" + strings.Repeat("a", common.AddressLength*2)
	indexedAddressValue, err := common.NewAddressFromString(indexedAddress)
	if err != nil {
		t.Fatal(err)
	}
	parsedABI := mustParseABI(t, contractABI)
	event := parsedABI.Events["Transfer"]
	signatureTopic := common.HashToLogTopic(event.ID).Hex()
	indexedTopics, err := abi.MakeTopics(
		[]any{indexedAddressValue},
		[]any{"hello"},
		[]any{[]byte{0xab, 0xcd}},
	)
	if err != nil {
		t.Fatal(err)
	}
	addressTopic := indexedTopics[0][0].Hex()
	labelTopic := indexedTopics[1][0].Hex()
	payloadTopic := indexedTopics[2][0].Hex()
	emptyPayloadTopic := makeTopicHex(t, []byte{})
	amountData, err := event.Inputs.NonIndexed().Pack(big.NewInt(2))
	if err != nil {
		t.Fatal(err)
	}

	script := fmt.Sprintf(`
var filter = contract.Transfer({from: %q, label: "hello", payload: "0xabcd"});
contract.Transfer({payload: "0x"});
var log = {
  address: %q,
  topics: [%q, %q, %q, %q],
  data: "0x%s",
  blockNumber: "0x1",
  transactionIndex: "0x0",
  logIndex: "0x0"
};
var decoded = filter.formatter(JSON.parse(JSON.stringify(log)));
var allEventsDecoded = contract.allEvents().formatter(JSON.parse(JSON.stringify(log)));

JSON.stringify({
  topics: captured[0].topics,
  emptyPayloadTopic: captured[1].topics[3],
  event: decoded.event,
  from: decoded.args.from,
  label: decoded.args.label,
  payload: decoded.args.payload,
  amount: decoded.args.amount.toString(10),
  allEventsEvent: allEventsDecoded.event,
  signatureIsTopic: web3._extend.utils.isTopic(%q),
  eventIDIsTopic: web3._extend.utils.isTopic(%q)
});
`, indexedAddress, contractAddress, signatureTopic, addressTopic, labelTopic, payloadTopic, common.Bytes2Hex(amountData), signatureTopic, "0x"+common.Bytes2Hex(event.ID[:]))
	var got struct {
		Topics            []string `json:"topics"`
		EmptyPayloadTopic string   `json:"emptyPayloadTopic"`
		Event             string   `json:"event"`
		From              string   `json:"from"`
		Label             string   `json:"label"`
		Payload           string   `json:"payload"`
		Amount            string   `json:"amount"`
		AllEventsEvent    string   `json:"allEventsEvent"`
		SignatureIsTopic  bool     `json:"signatureIsTopic"`
		EventIDIsTopic    bool     `json:"eventIDIsTopic"`
	}
	runWeb3ContractJSON(t, re, web3FilterProvider, contractABI, contractAddress, script, &got)
	wantTopics := []string{signatureTopic, addressTopic, labelTopic, payloadTopic}
	if !slices.Equal(got.Topics, wantTopics) {
		t.Fatalf("event topics mismatch:\nhave %v\nwant %v", got.Topics, wantTopics)
	}
	if got.EmptyPayloadTopic != emptyPayloadTopic {
		t.Fatalf("empty bytes topic mismatch: have %s, want %s", got.EmptyPayloadTopic, emptyPayloadTopic)
	}
	if got.Event != "Transfer" || got.AllEventsEvent != "Transfer" || got.From != indexedAddress || got.Label != labelTopic || got.Payload != payloadTopic || got.Amount != "2" {
		t.Fatalf("decoded event mismatch: %+v", got)
	}
	if !got.SignatureIsTopic || got.EventIDIsTopic {
		t.Fatalf("topic width validation mismatch: full=%t eventID=%t", got.SignatureIsTopic, got.EventIDIsTopic)
	}
}

func TestEmbeddedWeb3EventTopicAlignment(t *testing.T) {
	t.Parallel()

	const contractABI = `[
  {
    "anonymous": false,
    "inputs": [{"indexed": true, "name": "value", "type": "uint512"}],
    "name": "Unsigned",
    "type": "event"
  },
  {
    "anonymous": false,
    "inputs": [{"indexed": true, "name": "value", "type": "int512"}],
    "name": "Signed",
    "type": "event"
  },
  {
    "anonymous": false,
    "inputs": [{"indexed": true, "name": "value", "type": "bool"}],
    "name": "Flag",
    "type": "event"
  },
  {
    "anonymous": false,
    "inputs": [{"indexed": true, "name": "value", "type": "bytes32"}],
    "name": "FixedBytes32",
    "type": "event"
  },
  {
    "anonymous": false,
    "inputs": [{"indexed": true, "name": "value", "type": "bytes64"}],
    "name": "FixedBytes64",
    "type": "event"
  }
]`

	re := newEmbeddedWeb3(t)
	contractAddress := "Q" + strings.Repeat("0", common.AddressLength*2)
	fixedBytes32 := strings.Repeat("ab", 32)
	fixedBytes64 := strings.Repeat("cd", common.LogTopicLength)

	script := fmt.Sprintf(`
contract.Unsigned({value: 2});
contract.Signed({value: 2});
contract.Signed({value: -2});
contract.Flag({value: true});
contract.Flag({value: false});
contract.FixedBytes32({value: "0x%s"});
contract.FixedBytes64({value: "0x%s"});

JSON.stringify(captured.map(function (filter) {
  return filter.topics[1];
}));
`, fixedBytes32, fixedBytes64)
	var got []string
	runWeb3ContractJSON(t, re, web3FilterProvider, contractABI, contractAddress, script, &got)
	want := []string{
		"0x" + abiWordHex(2),
		"0x" + abiWordHex(2),
		"0x" + strings.Repeat("f", common.LogTopicLength*2-1) + "e",
		"0x" + abiWordHex(1),
		"0x" + abiWordHex(0),
		"0x" + fixedBytes32 + strings.Repeat("0", (common.LogTopicLength-32)*2),
		"0x" + fixedBytes64,
	}
	var fixedBytes32Value [32]byte
	copy(fixedBytes32Value[:], bytes.Repeat([]byte{0xab}, len(fixedBytes32Value)))
	var fixedBytes64Value [64]byte
	copy(fixedBytes64Value[:], bytes.Repeat([]byte{0xcd}, len(fixedBytes64Value)))
	goWant := []string{
		makeTopicHex(t, big.NewInt(2)),
		makeTopicHex(t, big.NewInt(2)),
		makeTopicHex(t, big.NewInt(-2)),
		makeTopicHex(t, true),
		makeTopicHex(t, false),
		makeTopicHex(t, fixedBytes32Value),
		makeTopicHex(t, fixedBytes64Value),
	}
	if !slices.Equal(want, goWant) {
		t.Fatalf("explicit topic vectors disagree with Go ABI:\nhave %v\nwant %v", want, goWant)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("event topic alignment mismatch:\nhave %v\nwant %v", got, want)
	}
}

func makeTopicHex(t *testing.T, value any) string {
	t.Helper()

	topics, err := abi.MakeTopics([]any{value})
	if err != nil {
		t.Fatalf("make topic for %T: %v", value, err)
	}
	return topics[0][0].Hex()
}
