# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: gqrl qrvm all test lint fmt clean devtools vm64-fixture-check \
	vm64-e2e-unit vm64-e2e-build vm64-e2e-doctor \
	vm64-e2e-sdk-smoke \
	vm64-network-prepare vm64-network-start vm64-network-stop \
	vm64-e2e-run vm64-e2e-test vm64-e2e-resume vm64-e2e-finalize \
	local-testnet-host-preflight local-testnet-start local-testnet-stop \
	local-testnet-e2e local-testnet-e2e-awake local-testnet-e2e-from-scratch-awake help

GOBIN = ./build/bin
GO ?= latest
GORUN = go run
LOCAL_TESTNET_ENCLAVE ?= local-testnet
LOCAL_TESTNET_GIT_COMMIT ?= $(shell git rev-parse HEAD)
LOCAL_TESTNET_NETWORK_PARAMS ?= scripts/local_testnet/network_params.yaml
LOCAL_TESTNET_DUMP_DIR ?= /tmp/$(LOCAL_TESTNET_ENCLAVE)-dump
LOCAL_TESTNET_PREPARATION_DIR ?= /tmp/$(LOCAL_TESTNET_ENCLAVE)-preparation
VM64_E2E_DIR = scripts/testing/e2e
VM64_E2E_RUNNER = go -C $(VM64_E2E_DIR) run ./cmd/vm64e2e
VM64_E2E_RESULTS_ARG = $(if $(strip $(VM64_E2E_RESULTS_DIR)),--results "$(abspath $(VM64_E2E_RESULTS_DIR))")
VM64_E2E_GLOBAL_TIMEOUT ?= 6h
VM64_E2E_CLEANUP_RESERVE ?= 1h
VM64_E2E_RUN_ARGS ?=
VM64_E2E_TEST_ARGS ?=
VM64_E2E_FINALIZE_ARGS ?=

#? gqrl: Build gqrl.
gqrl:
	$(GORUN) build/ci.go install ./cmd/gqrl
	@echo "Done building."
	@echo "Run \"$(GOBIN)/gqrl\" to launch gqrl."

#? qrvm: Build qrvm.
qrvm:
	$(GORUN) build/ci.go install ./cmd/qrvm
	@echo "Done building."
	@echo "Run \"$(GOBIN)/qrvm\" to launch qrvm."

#? all: Build all packages and executables.
all:
	$(GORUN) build/ci.go install

#? test: Run the tests.
test: all
	$(GORUN) build/ci.go test

#? lint: Run certain pre-selected linters.
lint: ## Run linters.
	$(GORUN) build/ci.go lint

#? fmt: Ensure consistent code formatting.
fmt:
	gofmt -s -w $(shell find . -name "*.go")

#? clean: Clean go cache, built executables, and the auto generated folder.
clean:
	go clean -cache
	rm -fr build/_workspace/pkg/ $(GOBIN)/*

# The devtools target installs tools required for 'go generate'.
# You need to put $GOBIN (or $GOPATH/bin) in your PATH to use 'go generate'.

#? devtools: Install recommended developer tools.
devtools:
	env GOBIN= go install golang.org/x/tools/cmd/stringer@latest
	env GOBIN= go install github.com/fjl/gencodec@latest
	env GOBIN= go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	env GOBIN= go install ./cmd/abigen
	@type "hypc" 2> /dev/null || echo 'Please install hypc'
	@type "protoc" 2> /dev/null || echo 'Please install protoc'

#? vm64-fixture-check: Recompile and verify VM64 Hyperion contract artifacts with the pinned toolchain.
vm64-fixture-check:
	./scripts/testing/e2e/testdata/contracts/verify_hyperion_fixture.sh

#? vm64-e2e-unit: Run the preparation boundary plus all nested E2E tests and vet checks.
vm64-e2e-unit:
	./scripts/local_testnet/prepare_local_testnet_test.sh
	go -C $(VM64_E2E_DIR) test -count=1 ./...
	go -C $(VM64_E2E_DIR) vet ./...

#? vm64-e2e-build: Build the unified VM64 E2E runner.
vm64-e2e-build:
	mkdir -p "$(abspath $(GOBIN))"
	go -C $(VM64_E2E_DIR) build -o "$(abspath $(GOBIN))/vm64e2e" ./cmd/vm64e2e

#? vm64-e2e-sdk-smoke: Test the SDK client against an already running Kurtosis engine.
vm64-e2e-sdk-smoke:
	VM64_E2E_REAL_ENGINE_SMOKE=1 go -C $(VM64_E2E_DIR) test -count=1 \
		-run '^TestSDKClientRealEngineSmoke$$' ./internal/kurtosis

#? local-testnet-host-preflight: Warm every host binary used by the live VM64 lifecycle.
local-testnet-host-preflight:
	go run build/ci.go install ./cmd/gqrl ./cmd/clef
	go -C $(VM64_E2E_DIR) build ./cmd/goabi ./cmd/depositcheck ./cmd/systemcheck ./cmd/freshsync ./cmd/vm64e2e

#? vm64-network-prepare: Build and attest local-testnet inputs without creating or changing an enclave.
vm64-network-prepare:
	SOURCE_SHA="$(LOCAL_TESTNET_GIT_COMMIT)" \
	EFFECTIVE_PARAMS_OUTPUT="$(LOCAL_TESTNET_PREPARATION_DIR)/network_params.effective.yaml" \
	PREPARATION_OUTPUT="$(LOCAL_TESTNET_PREPARATION_DIR)/preparation.json" \
	./scripts/local_testnet/prepare_local_testnet.sh \
		-e "$(LOCAL_TESTNET_ENCLAVE)" \
		-n "$(abspath $(LOCAL_TESTNET_NETWORK_PARAMS))"

#? vm64-network-start: Provision a local testnet without running any E2E suites.
vm64-network-start:
	SOURCE_SHA="$(LOCAL_TESTNET_GIT_COMMIT)" \
	./scripts/local_testnet/start_local_testnet.sh \
		-e "$(LOCAL_TESTNET_ENCLAVE)" \
		-n "$(abspath $(LOCAL_TESTNET_NETWORK_PARAMS))"

#? vm64-network-stop: Dump and stop the separately provisioned local testnet.
vm64-network-stop:
	./scripts/local_testnet/stop_local_testnet.sh \
		"$(LOCAL_TESTNET_ENCLAVE)" "$(LOCAL_TESTNET_DUMP_DIR)"

#? vm64-e2e-doctor: Validate tools and configuration; results use a unique directory unless explicitly set.
vm64-e2e-doctor:
	$(VM64_E2E_RUNNER) doctor \
		--repo-root "$(CURDIR)" \
		--source-sha "$(LOCAL_TESTNET_GIT_COMMIT)" \
		--network-params "$(abspath $(LOCAL_TESTNET_NETWORK_PARAMS))" $(VM64_E2E_RESULTS_ARG) \
		--global-timeout "$(VM64_E2E_GLOBAL_TIMEOUT)" \
		--cleanup-reserve "$(VM64_E2E_CLEANUP_RESERVE)"

#? vm64-e2e-run: Run an owned lifecycle with a unique results directory unless explicitly set.
vm64-e2e-run:
	$(VM64_E2E_RUNNER) run \
		--repo-root "$(CURDIR)" \
		--source-sha "$(LOCAL_TESTNET_GIT_COMMIT)" \
		--network-params "$(abspath $(LOCAL_TESTNET_NETWORK_PARAMS))" $(VM64_E2E_RESULTS_ARG) \
		--enclave "$(LOCAL_TESTNET_ENCLAVE)" \
		--global-timeout "$(VM64_E2E_GLOBAL_TIMEOUT)" \
		--cleanup-reserve "$(VM64_E2E_CLEANUP_RESERVE)" $(VM64_E2E_RUN_ARGS)

#? vm64-e2e-test: Test a borrowed network; results use a unique directory unless explicitly set.
vm64-e2e-test:
	$(VM64_E2E_RUNNER) test \
		--repo-root "$(CURDIR)" \
		--source-sha "$(LOCAL_TESTNET_GIT_COMMIT)" \
		--network-params "$(abspath $(LOCAL_TESTNET_NETWORK_PARAMS))" $(VM64_E2E_RESULTS_ARG) \
		--enclave-id "$(LOCAL_TESTNET_ENCLAVE)" \
		--global-timeout "$(VM64_E2E_GLOBAL_TIMEOUT)" \
		--cleanup-reserve "$(VM64_E2E_CLEANUP_RESERVE)" $(VM64_E2E_TEST_ARGS)

#? vm64-e2e-resume: Resume at the failed stage; requires explicit VM64_E2E_CHECKPOINT.
vm64-e2e-resume:
	@if [ -z "$(strip $(VM64_E2E_CHECKPOINT))" ]; then \
		echo "VM64_E2E_CHECKPOINT is required; set it to the checkpoint.json path for the run being resumed." >&2; \
		exit 2; \
	fi
	$(VM64_E2E_RUNNER) resume \
		--checkpoint "$(abspath $(VM64_E2E_CHECKPOINT))" \
		--repo-root "$(CURDIR)" \
		--source-sha "$(LOCAL_TESTNET_GIT_COMMIT)" \
		--global-timeout "$(VM64_E2E_GLOBAL_TIMEOUT)"

#? vm64-e2e-finalize: Finalize by UUID; requires explicit VM64_E2E_OWNERSHIP.
vm64-e2e-finalize:
	@if [ -z "$(strip $(VM64_E2E_OWNERSHIP))" ]; then \
		echo "VM64_E2E_OWNERSHIP is required; set it to the ownership.json path for the run being finalized." >&2; \
		exit 2; \
	fi
	$(VM64_E2E_RUNNER) finalize \
		--ownership "$(abspath $(VM64_E2E_OWNERSHIP))" $(VM64_E2E_RESULTS_ARG) $(VM64_E2E_FINALIZE_ARGS)

#? local-testnet-start: Compatibility alias for network-only provisioning.
local-testnet-start: vm64-network-start

#? local-testnet-stop: Compatibility alias for network-only dump and shutdown.
local-testnet-stop: vm64-network-stop

#? local-testnet-e2e: Compatibility alias for non-owning tests against a running network.
local-testnet-e2e: vm64-e2e-test

#? local-testnet-e2e-awake: Run non-owning tests while preventing macOS sleep.
local-testnet-e2e-awake:
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		exec caffeinate -dimsu $(MAKE) vm64-e2e-test; \
	else \
		exec $(MAKE) vm64-e2e-test; \
	fi

#? local-testnet-e2e-from-scratch-awake: Run the owned lifecycle under one keep-awake assertion.
local-testnet-e2e-from-scratch-awake:
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		exec caffeinate -dimsu $(MAKE) vm64-e2e-run; \
	else \
		exec $(MAKE) vm64-e2e-run; \
	fi

#? help: Get more info on make commands.
help: Makefile
	@echo ''
	@echo 'Usage:'
	@echo '  make [target]'
	@echo ''
	@echo 'Targets:'
	@sed -n 's/^#?//p' $< | column -t -s ':' |  sort | sed -e 's/^/ /'
