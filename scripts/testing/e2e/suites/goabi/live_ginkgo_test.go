// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build e2e

package goabi

import (
	"testing"
	"time"

	ginkgo "github.com/onsi/ginkgo/v2"
	gomega "github.com/onsi/gomega"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/network"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/suitekit"
)

const (
	liveSpecTimeout = 25 * time.Minute
)

func TestE2E(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "GoABI live E2E suite")
}

var _ = ginkgo.It(
	"exercises Go ABI against a live qrl-package network",
	ginkgo.Serial,
	ginkgo.Label("e2e", "live", "goabi", "mutates-chain"),
	func(ctx ginkgo.SpecContext) {
		environment, networkLease, err := suitekit.PrepareLiveEnvironment(
			ctx,
			network.FullRequirements(),
		)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(networkLease.Close)

		session, err := suitekit.OpenSigningSession(ctx, environment)
		gomega.Expect(err).NotTo(gomega.HaveOccurred())
		ginkgo.DeferCleanup(session.Close)

		ginkgo.By("round-tripping generated bindings and every live event shape")
		gomega.Expect(checkLiveEventRoundTrip(
			ctx,
			session.Client,
			session.Wallet,
			session.Sender,
			session.Environment.GraphQLURL,
		)).To(gomega.Succeed())

		ginkgo.By("reading VM64 storage through every supported API")
		gomega.Expect(checkStorageAPIs(
			ctx,
			session.Client,
			session.Wallet,
			session.Sender,
			session.Environment.GraphQLURL,
		)).To(gomega.Succeed())

		ginkgo.By("keeping addresses with distinct upper halves isolated")
		gomega.Expect(checkAddressUpperHalfIsolation(
			ctx,
			session.Client,
			session.Wallet,
			session.Sender,
		)).To(gomega.Succeed())

		ginkgo.By("executing the VM64 account, call, create, and rollback opcodes")
		gomega.Expect(checkLiveVM64Opcodes(
			ctx,
			session.Client,
			session.Wallet,
			session.Sender,
		)).To(gomega.Succeed())

		ginkgo.By("executing the VM64 precompile vectors")
		gomega.Expect(checkLivePrecompiles(
			ctx,
			session.Client,
			session.Sender,
		)).To(gomega.Succeed())

		ginkgo.By("submitting and verifying an exact transaction through GraphQL")
		gomega.Expect(checkGraphQLSendRawTransaction(
			ctx,
			session.Environment.GraphQLURL,
			session.Client,
			session.Wallet,
			session.Sender,
		)).To(gomega.Succeed())

		ginkgo.By("observing exact transaction and log events over WebSocket")
		gomega.Expect(checkWebSocketSubscriptions(
			ctx,
			session.Environment.WebSocketURL,
			session.Client,
			session.Wallet,
			session.Sender,
		)).To(gomega.Succeed())
	},
	ginkgo.SpecTimeout(liveSpecTimeout),
)
