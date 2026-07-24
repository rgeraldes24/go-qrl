# End-to-end tests

This module contains independently selectable Ginkgo v2 suites and the small
QRL-specific libraries they share. Kurtosis owns the real test network; Ginkgo
owns suite discovery, ordering, timeouts, progress, cleanup, and the pass/fail
exit status.

Network lifecycle and tests are separate:

```bash
make network-start
make live-test
make network-stop
```

Starting a network never runs tests. Running tests never creates or destroys a
network.

## GoABI

Run the portable tests without Docker:

```bash
go -C scripts/testing/e2e test -count=1 ./suites/goabi
go test -count=1 ./accounts/abi/... ./signer/fourbyte
```

The package tests hold exhaustive ABI parser, type, packing, topic, malformed
input, collision, and generated-binding matrices. The external-package GoABI
tests keep representative VM64 smoke coverage and VM-backed contract behavior.
Regenerate the checked-in representative binding with:

```bash
go -C scripts/testing/e2e generate ./suites/goabi
```

The generated projection covers deploy, new, call, transact, session, raw,
filter, watch, parse, fallback, and receive APIs. Full EventEmitter and
AdvancedABI behavior uses their embedded ABI and bytecode with
`bind.BoundContract`.

The external ABI `function` value remains dependent on
[cyyber/go-qrl#87](https://github.com/cyyber/go-qrl/pull/87), which defines its
68-byte VM64 encoding. `fixed`, `ufixed`, and packed encoding are not supported.

## Live network

The built-in configuration runs a real qrl-package network in Kurtosis:

- one current-source go-qrl execution client;
- one pinned Qrysm beacon node;
- one pinned Qrysm validator client; and
- one pinned genesis generator.

It creates a fresh ML-DSA-87 wallet for prefunding and withdrawals. Local
requirements are Docker, Kurtosis CLI 1.20.0, Git, and Go 1.26. Set
`E2E_DOCKER_BIN` if the Docker CLI is not named `docker`.

Use one private directory for the lifecycle:

```bash
E2E_NETWORK_DIR=/tmp/my-go-qrl-network make network-start
E2E_NETWORK_DIR=/tmp/my-go-qrl-network \
  make live-test E2E_SUITES=goabi
E2E_NETWORK_DIR=/tmp/my-go-qrl-network make network-stop
```

`network-start` builds pinned images, creates one uniquely named enclave, runs
the pinned qrl-package revision, discovers endpoints, and records the exact
enclave and runtime identity. If provisioning is interrupted after enclave
creation, exact ownership is retained for `network-stop`; stop that incomplete
network before starting again. A lost create response leaves a name-only intent
that blocks replay but cannot authorize destruction without manual inspection.
Provisioning is never replayed automatically.

Inspect the network without running tests:

```bash
go -C scripts/testing/e2e run ./cmd/e2e network status \
  --network-dir /tmp/my-go-qrl-network
```

`network-stop` validates and destroys only the recorded enclave. It does not
stop the shared Kurtosis engine.

The network directory is private runtime state. Never upload `private/`, raw
qrl-package output, or raw enclave dumps. The root `network.json` exists only
for a ready network and contains sanitized runtime identity.

## Live runner

`make live-test` defaults to GoABI. `E2E_SUITES` accepts comma-separated suite
directory names and maps them to `./suites/<name>` plus matching Ginkgo labels.
The target runs the equivalent of:

```bash
E2E_REPO_ROOT="$PWD" \
E2E_NETWORK_DIR=/tmp/my-go-qrl-network \
go -C scripts/testing/e2e tool ginkgo \
  --tags=e2e \
  --ldflags='-s -w' \
  --procs=1 \
  --require-suite \
  --fail-on-empty \
  --fail-on-pending \
  --label-filter='e2e && live && goabi' \
  --timeout=25m \
  --poll-progress-after=30s \
  --poll-progress-interval=30s \
  ./suites/goabi \
  -- -test.run='^TestE2E$'
```

GoABI has one serial live spec with seven visible steps:

1. deployment, generated bindings, calls, errors, events, logs, filters, and
   compiler-produced ABI shapes;
2. 64-byte storage through RPC, calls, GraphQL, and verified inclusion and
   absence proofs;
3. upper-half 64-byte address isolation;
4. VM64 account, context, call, create, CREATE2, and rollback opcodes;
5. active precompiles, including valid and invalid ML-DSA-87 vectors;
6. exact raw-transaction submission through GraphQL; and
7. new-head, raw-log, and generated-binding subscriptions over WebSocket.

The canonical 64-byte calldata and output layout remains in the portable smoke
test because it does not require a network.

Every ordinary transaction must mine successfully. The explicit top-level
revert is the only transaction expected to fail.

The result is Ginkgo's process exit status. A failed rerun restarts the complete
spec at its first step and uses current nonces plus fresh contracts.

At the start of the spec, the suite acquires the network mutation lease and
authenticates the enclave, package inputs, images, execution binary, source,
endpoints, chain, genesis, and funded wallet. The lease prevents concurrent
suite mutation and prevents network stop during a run.

## Adding a suite

Create `scripts/testing/e2e/suites/<suite>` with the generic `TestE2E`
bootstrap and label every live spec `e2e`, `live`, and `<suite>`. Then run:

```bash
make live-test E2E_SUITES=<suite>
```

Call `suitekit.PrepareLiveEnvironment(ctx)` to authenticate the shared RPC,
signer, GraphQL, and WebSocket endpoints. Use one Ginkgo `Serial` spec with
`SpecContext`, `By`, `DeferCleanup`, and a timeout instead of adding another
runner. A suite can ignore endpoints it does not use; additional nodes or APIs
belong in the shared network topology.

Keep state-changing scenarios rerunnable through fresh contracts and current
nonces. Do not start or stop the network from a suite hook.
