# VM64 End-to-End Tests

The network lifecycle is intentionally separate from this test harness. The
scripts in [`../../local_testnet`](../../local_testnet) can start and stop a
development network without running any tests.

## Running against an existing network

From the repository root, start the network and run the endpoint suites:

```bash
scripts/local_testnet/start_local_testnet.sh
scripts/testing/e2e/run_tests.sh
```

Strict mode rebuilds the host clients, requires HTTP, GraphQL, and WebSocket
coverage, and preserves each suite's output and command status:

```bash
scripts/testing/e2e/run_tests.sh \
  -c \
  -o scripts/testing/e2e/logs/test-results
```

The `make local-testnet-e2e` target and the GitHub workflow run those
endpoint-dependent suites independently against both execution nodes. The EL1
run also performs the standalone Clef cryptographic/API suite; the EL2 run uses
`-C` to skip only that node-independent repetition. The subsequent
`systemcheck` gate still proves topology Clef signing from both execution nodes.
The target expects an already running network and does not stop it when the
tests finish.

From the repository root, `make vm64-fixture-check` verifies the pinned
Hyperion compiler output before a network is started. With the network running,
`make local-testnet-e2e` then verifies:

- strict VM64 RPC, console, ABI, storage, proof, event, and transaction behavior
  against both execution nodes;
- a real 64-byte validator deposit through both EL and CL nodes;
- the required two-node, topology-Clef, non-empty access-list, finality, and
  restart behavior; and
- genuinely empty-datadir snap and full synchronization.

See [`depositcheck`](depositcheck/README.md) for the deposit assertions,
[`freshsync`](freshsync/README.md) for the temporary third EL/CL topology and
its cleanup behavior, and [`tests`](tests/README.md) for the JavaScript and
generated-contract suites.

The canonical target requires zero validator-duty failure history, so a failure
during an earlier endpoint or deposit phase cannot be hidden by a later
baseline. A standalone `systemcheck` invocation instead baselines
process-cumulative Qrysm counters and fails on every subsequent increase; pass
`-require-zero-duty-history` to opt into the canonical policy.

The automatic-withdrawal gate proves the exact recipient balance delta on both
execution nodes while the withdrawal block is fresh. After consensus finality
advances, it re-reads that block from both nodes and revalidates its hash,
withdrawal root, exact body, full-width recipient, and amount. This finalized
check deliberately does not require archive state, so the topology continues to
exercise ordinary pruning full nodes.

## Complete isolated lifecycle

The deposit gate is one-shot. The safest complete run uses a fresh enclave and
one keep-awake lifecycle that performs the compiler preflight, warms host
binaries, starts the network, runs every live gate, writes a dump, and removes
only the enclave it created:

```bash
make local-testnet-e2e-from-scratch-awake \
  LOCAL_TESTNET_ENCLAVE=vm64-e2e \
  LOCAL_TESTNET_DUMP_DIR=/tmp/vm64-e2e-dump
```

The from-scratch runner refuses to replace an existing enclave. On macOS it
wraps the lifecycle with `caffeinate`; on other operating systems it performs
the same steps directly. Its `local-testnet-host-preflight` step warms strict
client builds and every Go helper before validators begin five-second duties.
Use that target before manually provisioning a network too.

`local-testnet-e2e-awake` remains available when a network is already running,
but strict zero-history evidence begins when its validator processes start, so
protect provisioning from sleep too. Keep laptops on AC power when possible:
suspending a five-second-slot network makes expired validator duties replay on
wake and invalidates timing-sensitive finality evidence. `caffeinate` cannot
prevent lid-close or critical-battery sleep.

Keep the Hyperion compiler preflight outside the live-network window; its image
is intentionally large and can compete with validators for Docker disk or CPU.

If startup used a `GENESIS_IMAGE` override, pass the same image reference (tag
or image ID) as `LOCAL_TESTNET_GENESIS_IMAGE`; the deposit checker resolves it
to an immutable image ID before validation. The pinned network source revisions
are recorded in
[`../../local_testnet/images.lock.env`](../../local_testnet/images.lock.env).

## Continuous integration

The `VM64 end-to-end` workflow runs the isolated lifecycle in a run-specific
enclave. It pulls digest-pinned base and toolchain images, builds
source-revision-tagged final images, and uploads effective network parameters,
image metadata, per-suite results, the deposit manifest, and the Kurtosis dump
even when a live gate fails. Digest changes in the network lock file are
explicit and reviewable; a same-source workflow rerun never follows a moved tag.

The workflow also runs actionlint, pinned ShellCheck, JavaScript syntax checks,
generated-artifact drift checks, all Go tests and vet, race-sensitive packages,
and bounded native fuzzing before the live lifecycle.
