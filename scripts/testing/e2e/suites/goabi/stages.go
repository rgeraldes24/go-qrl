// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

// Package goabi runs Go ABI/qrlclient checks against a live VM64 network.
package goabi

import (
	"context"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/suitekit"
)

type stageSpec struct {
	name        string
	description string
	run         func(context.Context, *suitekit.SigningSession) error
}

var liveStagePlan = []stageSpec{
	{
		name:        "ABI layout",
		description: "uses the canonical VM64 ABI layout",
		run: func(_ context.Context, session *suitekit.SigningSession) error {
			return checkGoABILayout(session.Sender)
		},
	},
	{
		name:        "events",
		description: "round-trips generated bindings and every live event shape",
		run: func(ctx context.Context, session *suitekit.SigningSession) error {
			return checkLiveEventRoundTrip(
				ctx,
				session.Client,
				session.Wallet,
				session.Sender,
				session.Environment.GraphQLURL,
			)
		},
	},
	{
		name:        "storage",
		description: "reads VM64 storage through every supported API",
		run: func(ctx context.Context, session *suitekit.SigningSession) error {
			return checkStorageAPIs(
				ctx,
				session.Client,
				session.Wallet,
				session.Sender,
				session.Environment.GraphQLURL,
			)
		},
	},
	{
		name:        "address isolation",
		description: "keeps addresses with distinct upper halves isolated",
		run: func(ctx context.Context, session *suitekit.SigningSession) error {
			return checkAddressUpperHalfIsolation(
				ctx,
				session.Client,
				session.Wallet,
				session.Sender,
			)
		},
	},
	{
		name:        "VM64 opcodes",
		description: "executes the VM64 account, call, create, and rollback opcodes",
		run: func(ctx context.Context, session *suitekit.SigningSession) error {
			return checkLiveVM64Opcodes(
				ctx,
				session.Client,
				session.Wallet,
				session.Sender,
			)
		},
	},
	{
		name:        "precompiles",
		description: "executes the VM64 precompile vectors",
		run: func(ctx context.Context, session *suitekit.SigningSession) error {
			return checkLivePrecompiles(ctx, session.Client, session.Sender)
		},
	},
	{
		name:        "GraphQL",
		description: "submits and verifies an exact transaction through GraphQL",
		run: func(ctx context.Context, session *suitekit.SigningSession) error {
			return checkGraphQLSendRawTransaction(
				ctx,
				session.Environment.GraphQLURL,
				session.Client,
				session.Wallet,
				session.Sender,
			)
		},
	},
	{
		name:        "WebSocket",
		description: "observes exact transaction and log events over WebSocket",
		run: func(ctx context.Context, session *suitekit.SigningSession) error {
			return checkWebSocketSubscriptions(
				ctx,
				session.Environment.WebSocketURL,
				session.Client,
				session.Wallet,
				session.Sender,
			)
		},
	},
}
