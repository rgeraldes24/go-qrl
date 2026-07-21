# Local-testnet system check

`systemcheck` validates the multi-process behavior that ABI-only and single-RPC
tests cannot cover. It targets the default two-participant topology in
`scripts/local_testnet/network_params.yaml` and verifies:

- both gqrl nodes expose the same full-width account from the topology's
  `signer-clef` service;
- `qrl_sendTransaction` originates independently from EL1 and EL2, invokes that
  Clef, produces a signature whose sender is recovered locally, and yields
  matching receipt, header, state-root, balance, and nonce transitions on both
  execution nodes;
- `qrl_createAccessList` on both execution nodes independently returns the
  exact full 64-byte contract address and known storage key without mutating
  state; topology Clef then signs a non-empty type-2 access-list transaction,
  whose body, sender, receipt roots, state roots, and complete 64-byte storage
  transition must agree on both nodes;
- every managed transaction's inclusion header uses qrl-package's full-width
  `Q0838...fbbe8` fee recipient, and its balance increase exactly reconciles
  all transaction tips and direct transfers in the inclusion block;
- new finalized canonical post-Zond blocks expose identical, header-root-verified
  withdrawal lists on both execution nodes, including an automatic partial
  withdrawal to the distinct full-width `Qa5ae...e11a8b` address whose balance
  delta exactly matches the summed withdrawal amount converted from Shor to planck;
- both execution and beacon networks have peers, each validator client reports
  exactly 64 unique active genesis-validator public keys, the two clients' key
  sets are disjoint, every key has both a successful-attestation counter and a
  nonzero last-attested slot, and each client has proposed; the canonical gate
  additionally requires zero process-lifetime duty failures before the check;
- validator metrics retain their public-key labels and process start time;
  endpoint loss, process replacement, counter regression, or a new failed duty
  fails at the next validator sample or forced phase-boundary check throughout
  the beacon-finality, execution-finality, and withdrawal waits, while full
  counter snapshots must still gain fresh attestations before fault injection;
- both beacon nodes expose identical effective specs with the mainnet field
  layout, five-second slots, and 128-slot epochs, and report zero sync distance;
- while Clef is stopped, the execution RPC remains demonstrably healthy and
  the otherwise-valid managed transaction fails with a signing-specific error;
  request cancellation is propagated through the external wallet, and after
  Clef restarts both nodes must produce two more blocks without any delayed
  nonce, pending-pool, or recipient-balance side effect before a fresh managed
  transaction proves that signing recovered;
- a transaction and additional blocks produced while participant two is
  offline are recovered after its EL, CL, and VC endpoints are all proved down
  and then restarted; each stopped service remains recovery-tracked until its
  endpoint is ready, EL2 converges to EL1's current latest number, hash, state
  root, and receipt root, and VC1 retains its pre-fault process/counter baseline
  throughout the outage; VC2 must expose a newer process start time, the exact
  same 64-key set, and a fresh zero-failure baseline, after which both clients
  gain aggregate attestation progress and every restarted VC2 key must attest
  without a reset or new duty failure before new finalized consensus is accepted;
- finalized execution heads on both ELs must match the execution payload in the
  finalized block reported by both CLs. Strict participant recovery additionally
  requires the EL finalized head to advance beyond its pre-fault number while
  preserving the pre-fault finalized block as a canonical ancestor.

Run it after the non-disruptive local-testnet suites:

```bash
go run ./scripts/testing/e2e/systemcheck \
  -enclave local-testnet \
  -timeout 115m
```

`-timeout` is one wall-clock budget for endpoint discovery and the complete
check, not a fresh budget for every polling phase. Individual eventual
conditions retain their own timeout guards, but the whole-run deadline caps
all of them. The 115-minute default accommodates 128-slot epochs at five
seconds per slot: the check reaches initial finality, finalizes independently
verified withdrawal evidence, then requires a further finalized checkpoint
after participant fault and recovery.

Qrysm's failed-duty metrics are process-lifetime counters. Standalone runs
baseline any value that predates the command and then fail on every increase,
reset, or regression. Pass `-require-zero-duty-history` for the canonical
full-run policy, which also rejects failures from earlier endpoint and deposit
phases. The Make target and GitHub workflow always enable that strict mode.

Each validator metrics response repeats full ML-DSA public keys and is therefore
substantially larger than an EL or CL health probe. `-validator-poll` controls
its independent cadence (30 seconds by default) while phase boundaries always
force a final sample before reporting success. It must be positive and shorter
than the two-minute aggregate-progress window.

The initial per-key attestation gate may span an epoch because it waits for
evidence from all 64 keys on each client. Recovery retains the two-minute
aggregate-progress gate for early liveness, then uses the remaining whole-run
budget to wait for all 64 VC2 keys to attest in the fresh validator process;
the configured budget should leave at least one full epoch for this gate.
Prevent host sleep for local five-second-slot runs; on macOS use the repository's
`local-testnet-e2e-awake` target or wrap the whole from-scratch lifecycle with
`caffeinate`. Caffeinate cannot protect against lid-close or critical-battery
sleep.

The full check intentionally stops and restarts ephemeral Kurtosis services.
It attempts to restart every service it stopped on all error and signal paths.
For a non-disruptive smoke run, use `-skip-restarts`; this omits the signer
failure and participant catch-up phases.

Participant recovery requires a newly finalized epoch by default. The
diagnostic-only `-require-finality-advance=false` mode still requires both
beacon nodes to converge beyond the pre-restart head slot, both CLs and ELs to
agree on the current finalized execution payload, and fresh per-key validator
activity without new duty failures, but it does not prove that finalization
resumed. Its output is explicitly labeled `NON-STRICT FINALITY` and should not
be used as the complete recovery gate.

This command tests restart catch-up with an existing node database. Run the
separate [`freshsync`](../freshsync/README.md) gate to add an execution service
with a fail-closed empty datadir and exercise both snap and full synchronization.
The `devp2p rlpx qrl-test` and `snap-test` conformance commands remain a
separate protocol gate. `go test ./...` runs those qrl/1 and snap/1 exchanges
over a real in-process TCP peer using the VM64 fixture; the Kurtosis topology
does not reuse that fixture chain.

The withdrawal gate is deterministic for the pinned qrl-package topology: its
validator-key generator assigns the configured `withdrawal_address` as the
execution withdrawal credential, the topology starts 128 validators, and the
post-Zond automatic sweep credits validator rewards above effective balance.
The gate scans only blocks created after it starts, waits for the discovered
withdrawal block to enter the CL-correlated EL finalized chain, and proves its
exact balance transition on both ELs while the block is fresh. After finality,
it re-reads the canonical block hash, withdrawal root/list, and amount without
requiring historical archive state. It fails closed within the command's
single whole-run `-timeout` budget.
