# End-to-end tests

This module contains the reusable live-network boundary for independently
selectable Ginkgo v2 suites. Kurtosis owns the real test network; Ginkgo owns
suite discovery, ordering, timeouts, progress, cleanup, and pass/fail.

Network lifecycle and tests are separate:

```bash
make network-start
make live-test E2E_SUITES=<suite>
make network-stop
```

Starting a network never runs tests. Running tests never creates or destroys a
network.

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
  make live-test E2E_SUITES=<suite>
E2E_NETWORK_DIR=/tmp/my-go-qrl-network make network-stop
```

`network-start` builds pinned images, creates one uniquely named enclave, runs
the pinned qrl-package revision, discovers endpoints, and records the exact
enclave and runtime identity. Repeating it with the same directory continues
an interrupted network setup rather than creating another network.

Inspect the network without running tests:

```bash
go -C scripts/testing/e2e run ./cmd/e2e network status \
  --network-dir /tmp/my-go-qrl-network
```

`network-stop` validates and destroys only the recorded enclave. It does not
stop the shared Kurtosis engine.

The network directory is private runtime state. Never upload `private/`, raw
qrl-package output, or raw enclave dumps. The root `network.json` is sanitized
lifecycle state, not a test report.

## Live runner

`E2E_SUITES` accepts comma-separated suite directory names and maps them to
`./suites/<name>` plus matching Ginkgo labels. The Make target uses the module's
pinned Ginkgo tool with:

- build tag `e2e`;
- one process;
- required, non-empty, non-pending suite selection;
- a matching `e2e && live && <suite>` label filter;
- a bounded timeout and periodic progress; and
- the generic `TestE2E` bootstrap.

The runner creates no JSON, JUnit, checkpoint, transaction journal, or custom
manifest. The result is Ginkgo's process exit status. Automatic retry is
intentionally disabled for chain-mutating tests.

Before the first spec, a suite acquires the network mutation lease and
authenticates the enclave, package inputs, images, execution binary, source,
endpoints, chain, genesis, and any requested funded wallet. The lease prevents
concurrent suite mutation and prevents network stop during a run.

## Adding a suite

Create `scripts/testing/e2e/suites/<suite>` with the generic `TestE2E`
bootstrap and label every live spec `e2e`, `live`, and `<suite>`. Then run:

```bash
make live-test E2E_SUITES=<suite>
```

Pass typed `network.Requirements` to
`suitekit.PrepareLiveEnvironment`. Use Ginkgo `BeforeAll`, `DeferCleanup`,
`Ordered`, `Serial`, `SpecContext`, and timeouts instead of adding another
runner. A suite may request signer, GraphQL, and WebSocket surfaces; additional
nodes or APIs belong in the shared network topology.

Keep state-changing scenarios rerunnable through fresh contracts and current
nonces. Do not start or stop the network from a suite hook.
