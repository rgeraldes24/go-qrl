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
	liveSetupTimeout = 5 * time.Minute
	liveSpecTimeout  = 25 * time.Minute
)

func TestE2E(t *testing.T) {
	gomega.RegisterFailHandler(ginkgo.Fail)
	ginkgo.RunSpecs(t, "GoABI live E2E suite")
}

var _ = ginkgo.Describe(
	"GoABI against a live qrl-package network",
	ginkgo.Ordered,
	ginkgo.Serial,
	ginkgo.Label("e2e", "live", "goabi", "mutates-chain"),
	func() {
		var session *suitekit.SigningSession

		ginkgo.BeforeAll(func(ctx ginkgo.SpecContext) {
			environment, networkLease, err := suitekit.PrepareLiveEnvironment(
				ctx,
				network.FullRequirements(),
			)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(networkLease.Close)

			session, err = suitekit.OpenSigningSession(ctx, environment)
			gomega.Expect(err).NotTo(gomega.HaveOccurred())
			ginkgo.DeferCleanup(session.Close)
		}, ginkgo.NodeTimeout(liveSetupTimeout))

		for _, definition := range liveStagePlan {
			definition := definition
			ginkgo.It(definition.description, func(ctx ginkgo.SpecContext) {
				ginkgo.By("running " + definition.name)
				gomega.Expect(definition.run(ctx, session)).To(gomega.Succeed())
			}, ginkgo.SpecTimeout(liveSpecTimeout))
		}
	},
)
