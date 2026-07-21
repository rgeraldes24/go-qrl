# Fresh execution synchronization gate

`freshsync` proves that VM64 state can be reconstructed by a genuinely new
execution client rather than merely retained across a service restart. It:

1. inspects the existing second EL and CL with Kurtosis's documented
   `service inspect --output json` API;
2. preserves their exact image, ports, genesis artifact, JWT artifact,
   bootnodes, bootstrap nodes, and other read-only configuration;
3. removes public-port reuse and any datadir artifact, rejects privileged,
   bind-mounted, parent-mounted, or unknown persistent state, and inserts a
   shell guard that exits before `gqrl init` if the execution datadir already
   exists;
4. adds a temporary EL and a validator-free CL that points to it;
5. captures the finalized deposit-contract account and its mutable 64-byte
   packed `branch[1] || branch[0]` word at slot `0x00`, requiring both halves
   to be nonzero after the three-deposit lifecycle gate,
   verifies its `qrl_getProof` account and storage paths against the finalized
   header state root, and executes `get_deposit_root` and
   `get_deposit_count` at that exact block;
6. waits for the EL to reproduce the captured header, prefunded 64-byte account
   state, deposit storage leaf, independently verified proofs, and identical
   contract-call/SLOAD results, then requires snap runs to log both their
   state-download cycle and committed pivot (and full runs to log no snap-path
   marker); and
7. submits a new topology-Clef transaction and compares the recovered sender,
   receipt, header, state/receipt roots, recipient balance, and signer nonce on
   the reference and fresh nodes.

The companion CL is required. In the post-merge go-qrl path, the downloader is
started by authenticated Engine `newPayload`/`forkchoiceUpdated` traffic; a
standalone third EL does not autonomously start chain synchronization merely
because it has an execution peer.

The storage gate is deliberately independent of root equality alone. Both the
reference and fresh nodes must return the full 64-byte word through
`qrl_getStorageAt`; `qrl_getProof` must return the same U512 value and valid
account/storage Merkle paths; and VM execution must decode a nonzero deposit
count and a post-deposit root through the contract getters. Missing historical
state, truncated 32-byte storage, an unverifiable proof, empty contract code,
or a call result that differs from the captured finalized target fails the run.
Use `-deposit-contract` only when the topology deploys the VM64 deposit contract
at an address other than the standard `Q4242...4242` address.

Run snap and full modes after `systemcheck` has established a healthy and
finalized two-participant network:

```bash
go run ./scripts/testing/e2e/freshsync \
  -enclave local-testnet \
  -syncmode snap \
  -fresh-el-service fresh-sync-el-snap \
  -fresh-cl-service fresh-sync-cl-snap

go run ./scripts/testing/e2e/freshsync \
  -enclave local-testnet \
  -syncmode full \
  -fresh-el-service fresh-sync-el-full \
  -fresh-cl-service fresh-sync-cl-full
```

`-timeout` is one wall-clock budget for endpoint discovery, service creation,
synchronization, and verification. Individual polling phases retain their own
timeout guards, but the whole-run deadline caps all of them. Cleanup requested
with `-cleanup-on-failure` uses a separate bounded context and still runs after
the whole-run deadline expires.

Successful runs remove only the two temporary services they added. Failures
preserve them so the subsequent enclave dump contains their logs; pass
`-cleanup-on-failure` for local runs that prefer automatic cleanup, or
`-keep-services` to inspect successful services.

CI gives each mode a 35-minute tool budget inside a 38-minute workflow step and
uses `-keep-services`, so both successful temporary pairs are present in the
final Kurtosis dump. The Make target uses the default successful cleanup to
limit local disk consumption.

## Kurtosis package limitation

The pinned qrl-package has a single whole-network entrypoint. Re-running it in
an existing enclave attempts to recreate participant names beginning at
`el-1`/`cl-1`; it does not expose a supported add-one-participant entrypoint.
This gate therefore uses Kurtosis 1.20's documented JSON inspect/add
round-trip. The inspect schema intentionally returns files artifacts but not a
`Directory(persistent_key=...)`; consequently the clone cannot inherit the
source service's persistent volume. The independent pre-init guard remains the
fail-closed proof that neither an image nor a future configuration change
seeded `/data/gqrl/execution-data`.
