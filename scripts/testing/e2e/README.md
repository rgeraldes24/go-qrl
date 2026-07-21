# VM64 End-to-End Tests

The E2E harness and the local network are deliberately separate. Commands in
[`../../local_testnet`](../../local_testnet) can prepare, start, inspect, and
stop a development network without executing a test.

The canonical harness is the nested Go module in this directory. Its
`vm64e2e` command owns lifecycle checkpoints, Kurtosis SDK calls, topology
discovery, serialized state-changing suites, diagnostics, reporting, resume,
and full-UUID cleanup.

## Test an existing network

Start a network independently, run the non-disruptive endpoint checks, and
stop it when you are finished:

```bash
make vm64-network-start LOCAL_TESTNET_ENCLAVE=local-testnet
make vm64-e2e-test LOCAL_TESTNET_ENCLAVE=local-testnet
make vm64-network-stop LOCAL_TESTNET_ENCLAVE=local-testnet
```

`vm64e2e test` never takes ownership of the enclave. By default it runs
read-only topology, readiness, HTTP, WebSocket, GraphQL, and console checks
against both execution nodes. State-changing checks are explicit:

```bash
make vm64-e2e-test \
  LOCAL_TESTNET_ENCLAVE=local-testnet \
  VM64_E2E_TEST_ARGS=--allow-disruptive
```

Even with that flag, the borrowed enclave is never destroyed by the harness.
The caller remains responsible for stopping it.

`vm64-e2e-test`, `vm64-e2e-run`, and `vm64-e2e-doctor` do not reuse a fixed
artifact directory by default. The Go command creates a unique
`/tmp/vm64e2e-<run-id>` directory for each invocation. Set
`VM64_E2E_RESULTS_DIR` only when an explicit location is needed, and do not
share that location between runs. The command's JSON output identifies the
exact `ResultsDir`, `Checkpoint`, and `Ownership` paths; use those values for
follow-up commands rather than guessing a latest run.

## Run an isolated owned lifecycle

The owned runner prepares immutable inputs, creates a run-specific enclave,
executes every gate in order, collects diagnostics, and removes only the full
UUID recorded in `ownership.json`:

```bash
make vm64-e2e-run \
  LOCAL_TESTNET_ENCLAVE=vm64-e2e
```

For a lifecycle that may need an explicit resume or finalization command,
choose and retain a dedicated directory:

```bash
make vm64-e2e-run \
  LOCAL_TESTNET_ENCLAVE=vm64-e2e \
  VM64_E2E_RESULTS_DIR=/tmp/vm64-e2e-results
```

On a local failure, preservation is enabled by default. Fix the cause and
resume from the recorded failed stage instead of restarting completed work:

```bash
make vm64-e2e-resume \
  VM64_E2E_CHECKPOINT=/tmp/vm64-e2e-results/checkpoint.json
```

Resume validates the source revision, configuration digest, current tree ID,
full enclave UUID, and ownership record. Completed stages are skipped. Safe
read-only failures retry the failed stage. State-changing stages first inspect
durable transaction, temporary-service, restart, endpoint, and pass-marker
evidence. Package invocation intent is persisted before Kurtosis is called; if
the terminal package response is lost, resume verifies Kurtosis's retained
package/parameters and reconstructs the output only from the exact live
services plus matching EL/CL network metadata. Managed-account requests retain
their exact nonce and arguments with monotonic initial-attempt and one-replay
markers. The runner never blindly repeats a submission or topology mutation.
If the external state cannot be reconciled, resume fails closed and preserves
the enclave for diagnosis.

The Make resume target has no inferred checkpoint default: an explicit
`VM64_E2E_CHECKPOINT` is required so one run cannot accidentally resume
another run's state.

An independent finalizer is available after cancellation, timeout, or a hard
runner exit:

```bash
make vm64-e2e-finalize \
  VM64_E2E_OWNERSHIP=/tmp/vm64-e2e-results/ownership.json
```

It collects service logs and a Kurtosis dump, then cleans up only a captured,
matching full UUID. A null, ambiguous, missing, or mismatched identity is
preserved rather than deleted. The command has an 18-minute default timeout
and reserves its final five minutes for UUID-based destruction, so stalled
diagnostics cannot consume the cleanup window; use `--timeout` only when a
larger external budget is also available.

The Make finalizer requires an explicit `VM64_E2E_OWNERSHIP`; by default it
writes beside that file. `VM64_E2E_RESULTS_DIR` remains an optional explicit
artifact-directory override.

On macOS, `make local-testnet-e2e-from-scratch-awake` wraps the owned command
with `caffeinate`. `make local-testnet-e2e-awake` does the same for borrowed
tests. Keep laptops on AC power: neither command can prevent lid-close or
critical-battery sleep, and suspending a five-second-slot network invalidates
timing-sensitive validator evidence.

## What the live lifecycle proves

The serialized live stages cover:

- both execution clients and distinct HTTP, WebSocket, GraphQL, and embedded
  console paths;
- 64-byte addresses across ABI calldata and return data, contract deployment,
  events, indexed topics, storage values, proofs, access lists, transactions,
  and precompiles, plus live ADDRESS/ORIGIN/CALLER/COINBASE/SELFBALANCE,
  BALANCE/EXTCODE*, CALL/DELEGATECALL/STATICCALL, exact-address warm/cold
  accounting, internal CREATE/CREATE2, and nested/top-level REVERT rollback;
- standalone Clef cryptographic/API behavior and topology-Clef signing from
  both execution nodes;
- real VM64 validator deposits, receipts, events, contract roots and counts,
  and ingestion by both consensus nodes;
- validator duties with zero new failures, finality progression, signer and
  participant restart recovery with durable pre-fault, outage, and recovered
  baselines, automatic withdrawals, and finalized-block consistency; and
- genuinely empty-datadir snap and full synchronization, including a new
  full-width state transition after sync.

The Hyperion fixture is checked independently with
`make vm64-fixture-check`. Generated bindings and contract artifacts remain
checked in under [`suites/goabi`](suites/goabi) and
[`testdata/contracts`](testdata/contracts). JavaScript remains under
[`testdata/console`](testdata/console) where the embedded console itself is the
behavior under test.

## Harness validation

Run the preparation boundary, all nested-module unit tests, and vet checks:

```bash
make vm64-e2e-unit
```

Build the unified command with `make vm64-e2e-build`. Against an already
running Kurtosis 1.20.0 engine, exercise the real SDK create/discover/list,
durable package-invocation metadata, service stop/start, and destroy boundaries
before expensive image preparation with:

```bash
make vm64-e2e-sdk-smoke
```

The ordinary unit suite skips that opt-in smoke so it cannot mutate a
developer's engine. SDK behavior otherwise uses a project-owned fake, while
RPC, beacon, GraphQL, WebSocket, and Clef boundaries use local test servers.

## Artifacts

Each run writes a deterministic artifact tree containing the ownership and
effective-configuration records, topology, schema-v1 checkpoint, aggregate
results, JUnit, timeline, per-stage JSON, suite and service logs, a classified
diagnostic reason, Kurtosis evidence, and a checksum manifest. Transaction
intents, exact signed bytes, submission-attempt markers, package invocation and
response-recovery evidence, hashes, temporary service UUIDs, and ordered system
fault/recovery observations are persisted before receipt, ingestion, sync, or
service-transition waits so interruption does not erase mutation evidence.
There is no
implicit latest-run lookup: resume and finalization consume exact paths from
the run being continued.

## Compatibility entrypoints

`run_tests.sh`, `run_e2e_from_scratch.sh`, `run_live_stages.sh`, the Python
schema-v1 state tool, and the thin `cmd/goabi`, `cmd/depositcheck`,
`cmd/systemcheck`, and `cmd/freshsync` commands remain temporarily for existing
automation. They are compatibility paths, not the canonical lifecycle. The
legacy driver now passes its same additive schema-v1 state file into the system
and fresh-sync commands, so those phases retain transaction journals,
full-UUID service transitions, and fault observations without creating a
second checkpoint format.

`cmd/systemcheck` additionally requires an explicit `base`, `signer-restart`,
or `participant-restart` phase; its compatibility path rejects `all` because
no single running checkpoint stage can own it. Its EL console/GoABI and deposit
entrypoints predate
the unified raw-transaction outbox and therefore fail closed rather than retry
an interrupted mutating stage. They are not acceptable evidence for the
intentional-interruption gate; only `vm64e2e run` plus `vm64e2e resume` covers
every mutating stage without replaying the completed lifecycle prefix.

Legacy orchestration is retained only while parity is checked. Its deletion
gate is satisfied by the canonical runner: one complete local lifecycle, two
consecutive complete CI lifecycles, an intentional canonical interruption
followed by successful same-checkpoint resume, an intentional failure proving
diagnostics and UUID-safe cleanup, artifact parity, and representation of every
existing live behavioral gate. The legacy driver itself is explicitly excluded
from resumability certification.

## Continuous integration

The `VM64 end-to-end` workflow keeps root unit/race/fuzz coverage, nested E2E
unit/race/vet coverage, and pinned Hyperion fixture verification in separate
jobs. The live job installs Kurtosis 1.20.0, starts the matching engine, runs
the real-engine SDK smoke, builds and doctors the runner, executes the owned
lifecycle, always invokes the independent finalizer, writes a concise result
summary, and unconditionally uploads the artifact directory.
If the owned lifecycle returns an error after creating its checkpoint, CI makes
one bounded `resume` attempt against that exact checkpoint and enclave; it never
starts a replacement lifecycle to retry the failure.

The six-hour job records one absolute deadline and retains its final hour for
diagnostics, cleanup, finalization, and artifact upload, leaving at most five
hours before cleanup regardless of setup duration. Image,
toolchain, package, and source revisions are immutable and recorded in the
artifacts; a same-source rerun does not follow a moved tag.
