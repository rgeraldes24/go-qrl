# This Makefile is meant to be used by people that do not usually work
# with Go source code. If you know what GOPATH is then you probably
# don't need to bother with make.

.PHONY: gqrl qrvm all test lint fmt clean devtools vm64-fixture-check local-testnet-host-preflight local-testnet-e2e local-testnet-e2e-awake local-testnet-e2e-from-scratch-awake help

GOBIN = ./build/bin
GO ?= latest
GORUN = go run
LOCAL_TESTNET_ENCLAVE ?= local-testnet
LOCAL_TESTNET_GIT_COMMIT ?= $(shell git rev-parse HEAD)
LOCAL_TESTNET_GENESIS_IMAGE ?= $(shell /bin/bash -c 'source scripts/local_testnet/images.lock.env; printf "theqrl-dev/qrl-genesis-generator:%s-%s" "$${PINNED_GENERATOR_GIT_COMMIT:0:12}" "$${PINNED_QRYSM_GIT_COMMIT:0:12}"')
LOCAL_TESTNET_DUMP_DIR ?= /tmp/$(LOCAL_TESTNET_ENCLAVE)-dump

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
	./scripts/local_testnet/tests/fixtures/verify_hyperion_fixture.sh

#? local-testnet-host-preflight: Warm every host binary used by the live VM64 lifecycle before validators start.
local-testnet-host-preflight:
	go run build/ci.go install ./cmd/gqrl ./cmd/clef
	go build ./scripts/local_testnet/txsigner ./scripts/local_testnet/goabi ./scripts/local_testnet/clefverify ./scripts/local_testnet/depositcheck ./scripts/local_testnet/systemcheck ./scripts/local_testnet/freshsync

#? local-testnet-e2e: Run strict VM64, deposit, multi-node, and fresh-sync checks against a running testnet.
local-testnet-e2e:
	EXPECTED_GIT_COMMIT="$(LOCAL_TESTNET_GIT_COMMIT)" ./scripts/local_testnet/run_tests.sh -c -e "$(LOCAL_TESTNET_ENCLAVE)" -s el-1-gqrl-qrysm -o scripts/local_testnet/logs/test-results/el-1
	EXPECTED_GIT_COMMIT="$(LOCAL_TESTNET_GIT_COMMIT)" ./scripts/local_testnet/run_tests.sh -c -C -e "$(LOCAL_TESTNET_ENCLAVE)" -s el-2-gqrl-qrysm -o scripts/local_testnet/logs/test-results/el-2
	go run ./scripts/local_testnet/depositcheck -enclave "$(LOCAL_TESTNET_ENCLAVE)" -generator-image "$(LOCAL_TESTNET_GENESIS_IMAGE)"
	go run ./scripts/local_testnet/systemcheck -enclave "$(LOCAL_TESTNET_ENCLAVE)" -require-zero-duty-history
	go run ./scripts/local_testnet/freshsync -enclave "$(LOCAL_TESTNET_ENCLAVE)" -syncmode snap -fresh-el-service fresh-sync-el-snap -fresh-cl-service fresh-sync-cl-snap
	go run ./scripts/local_testnet/freshsync -enclave "$(LOCAL_TESTNET_ENCLAVE)" -syncmode full -fresh-el-service fresh-sync-el-full -fresh-cl-service fresh-sync-cl-full

#? local-testnet-e2e-awake: Run the complete testnet gate while preventing macOS sleep.
local-testnet-e2e-awake:
	@if [ "$$(uname -s)" = "Darwin" ]; then \
		exec caffeinate -dimsu $(MAKE) local-testnet-e2e; \
	else \
		exec $(MAKE) local-testnet-e2e; \
	fi

#? local-testnet-e2e-from-scratch-awake: Run preflight, start, live gates, dump, and cleanup under one keep-awake assertion.
local-testnet-e2e-from-scratch-awake:
	./scripts/local_testnet/run_e2e_from_scratch.sh -e "$(LOCAL_TESTNET_ENCLAVE)" -d "$(LOCAL_TESTNET_DUMP_DIR)"

#? help: Get more info on make commands.
help: Makefile
	@echo ''
	@echo 'Usage:'
	@echo '  make [target]'
	@echo ''
	@echo 'Targets:'
	@sed -n 's/^#?//p' $< | column -t -s ':' |  sort | sed -e 's/^/ /'
