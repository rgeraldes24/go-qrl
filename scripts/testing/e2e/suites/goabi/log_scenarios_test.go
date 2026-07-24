// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package goabi

import (
	"bytes"
	"context"
	"reflect"
	"testing"

	qrl "github.com/theQRL/go-qrl"
	qrlabi "github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/event"
)

type portableLogFilterer struct {
	filtered []qrl.FilterQuery
	watched  []qrl.FilterQuery
}

func (filterer *portableLogFilterer) FilterLogs(_ context.Context, query qrl.FilterQuery) ([]types.Log, error) {
	filterer.filtered = append(filterer.filtered, query)
	return nil, nil
}

func (filterer *portableLogFilterer) SubscribeFilterLogs(_ context.Context, query qrl.FilterQuery, _ chan<- types.Log) (qrl.Subscription, error) {
	filterer.watched = append(filterer.watched, query)
	return event.NewSubscription(func(quit <-chan struct{}) error {
		<-quit
		return nil
	}), nil
}

func mustPortableType(t *testing.T, name string) qrlabi.Type {
	t.Helper()
	typ, err := qrlabi.NewType(name, "", nil)
	if err != nil {
		t.Fatalf("NewType(%q): %v", name, err)
	}
	return typ
}

// TestPortableIndexedEventFilterAndLog is the external-consumer smoke test for
// the complete event path. Exhaustive type, error, topic-budget, filter, and
// Hyperion topic-golden matrices live beside accounts/abi and accounts/abi/bind.
func TestPortableIndexedEventFilterAndLog(t *testing.T) {
	addressType := mustPortableType(t, "address")
	labelsType := mustPortableType(t, "string[]")
	payloadType := mustPortableType(t, "bytes")
	eventDef := qrlabi.NewEvent("Observed", "Observed", false, qrlabi.Arguments{
		{Name: "account", Type: addressType, Indexed: true},
		{Name: "labels", Type: labelsType, Indexed: true},
		{Name: "payload", Type: payloadType},
	})
	if err := eventDef.Validate(); err != nil {
		t.Fatal(err)
	}

	filterer := new(portableLogFilterer)
	contractAddress := common.Address{0: 0xc7, 63: 0x01}
	contract := bind.NewBoundContract(
		contractAddress,
		qrlabi.ABI{Events: map[string]qrlabi.Event{"Observed": eventDef}},
		nil,
		nil,
		filterer,
	)
	var account common.Address
	for i := range account {
		account[i] = byte(i*29 + 7)
	}
	labels := []string{"a", "bc"}
	labelsTopic, err := qrlabi.MakeTopic(labelsType, labels)
	if err != nil {
		t.Fatal(err)
	}

	start, end := uint64(17), uint64(31)
	_, filterSub, err := contract.FilterLogs(
		&bind.FilterOpts{Start: start, End: &end, Context: t.Context()},
		"Observed",
		[]any{account},
		[]any{labelsTopic},
	)
	if err != nil {
		t.Fatal(err)
	}
	filterSub.Unsubscribe()
	wantTopics := [][]common.LogTopic{
		{common.HashToLogTopic(eventDef.ID)},
		{common.LogTopic(account)},
		{labelsTopic},
	}
	if len(filterer.filtered) != 1 {
		t.Fatalf("FilterLogs calls = %d, want 1", len(filterer.filtered))
	}
	filtered := filterer.filtered[0]
	if !reflect.DeepEqual(filtered.Addresses, []common.Address{contractAddress}) ||
		!reflect.DeepEqual(filtered.Topics, wantTopics) ||
		filtered.FromBlock.Uint64() != start ||
		filtered.ToBlock.Uint64() != end {
		t.Fatalf("FilterLogs query = %+v, want topics %#v and range %d..%d", filtered, wantTopics, start, end)
	}

	_, watchSub, err := contract.WatchLogs(
		&bind.WatchOpts{Start: &start, Context: t.Context()},
		"Observed",
		[]any{account},
		[]any{labelsTopic},
	)
	if err != nil {
		t.Fatal(err)
	}
	watchSub.Unsubscribe()
	if len(filterer.watched) != 1 ||
		!reflect.DeepEqual(filterer.watched[0].Topics, wantTopics) ||
		filterer.watched[0].FromBlock.Uint64() != start {
		t.Fatalf("WatchLogs query = %+v, want topics %#v and start %d", filterer.watched, wantTopics, start)
	}

	payload := append(bytes.Repeat([]byte{0xa5}, 64), 0x00)
	data, err := eventDef.Inputs.NonIndexed().Pack(payload)
	if err != nil {
		t.Fatal(err)
	}
	decoded := make(map[string]any)
	if err := contract.UnpackLogIntoMap(decoded, "Observed", types.Log{
		Topics: []common.LogTopic{
			common.HashToLogTopic(eventDef.ID),
			common.LogTopic(account),
			labelsTopic,
		},
		Data: data,
	}); err != nil {
		t.Fatal(err)
	}
	if got := decoded["account"].(common.Address); got != account {
		t.Fatalf("decoded account = %s, want %s", got, account)
	}
	if got := decoded["labels"].(common.Hash); got != common.BytesToHash(labelsTopic[:common.HashLength]) {
		t.Fatalf("decoded indexed labels = %s, want %x", got, labelsTopic[:common.HashLength])
	}
	if got := decoded["payload"].([]byte); !bytes.Equal(got, payload) {
		t.Fatalf("decoded payload = %x, want %x", got, payload)
	}
}
