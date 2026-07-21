# VM64 deposit lifecycle check

`depositcheck` submits three real validator deposits (deterministic indices
4096 through 4098) to the preloaded Hyperion deposit contract and proves that
the complete 64-byte withdrawal address is preserved across the generator,
go-qrl execution, contract event/state, and Qrysm deposit-ingestion paths.

The check is deliberately one-shot and requires a fresh topology. Before it
mutates the chain, it verifies that:

- the exact local generator image resolves to a content-addressed image ID;
- its deposit CLI accepts a 64-byte execution withdrawal address and emits the
  VM64 sizes (2592-byte public key, 64-byte withdrawal credentials, and
  4627-byte signature);
- the three public-key SHA-256 vectors exactly match mnemonic indices 4096,
  4097, and 4098, catching ignored, reordered, or drifted derivation inputs;
- the upper 32 bytes of those credentials are nonzero and exactly match the
  requested address;
- both execution nodes expose identical deposit-contract code, with an empty
  root, zero count, and zero balance;
- both execution nodes expose the runtime byte length and SHA-256 from the
  immutable generator manifest, the exact packed zero-hash words in slots
  `0x11..0x20`, and zero values in the rejected legacy slots `0x22..0x40`; and
- neither beacon node already knows any of the deterministic validator public
  keys.

It then signs and sends three payable `deposit` calls through go-qrl. After
each transition it checks the successful receipt, transaction
sender/value/calldata, full `DepositEvent` payload and sequential event index,
cumulative contract balance/count, an independently computed sparse-tree root,
and all 16 packed mutable branch slots on both execution nodes. Three leaves
exercise both halves of the first VM64 storage word and the level-one carry.
Finally, it polls the Qrysm v1alpha1 validator-status endpoint for every key
until both beacon nodes report `DEPOSITED` with the exact execution block
number. `PENDING` (with the same block) or `ACTIVE` are also accepted if a
validator advances before a poll; those are stronger state-inclusion outcomes.
`DEPOSITED` proves each deposit log was decoded, signature-validated, and stored
in each beacon node's execution deposit cache; checking only execution receipts
would not prove that path.

Run it after the strict RPC suites and before any restart/fresh-sync phase:

```sh
go run ./scripts/testing/e2e/depositcheck \
  -enclave "$ENCLAVE_NAME" \
  -generator-image "$GENESIS_IMAGE"
```

`GENESIS_IMAGE` must be the same source-pinned VM64 image supplied to the
qrl-package topology. The mutable published `qrl-genesis-generator-latest`
image is intentionally not a fallback: older copies reject 64-byte addresses
and embed a pre-VM64 deposit contract.

The command is deliberately one-shot. Recovery depends on where a failure
occurred:

- before broadcast, rerun only if the contract is still fresh and the funding
  account's pending nonce still equals its confirmed nonce;
- while a transaction is pending, inspect and wait for that transaction; the
  nonce guard refuses to broadcast another;
- after execution inclusion (including a failure while waiting for the beacon
  nodes), there is no resume mode; preserve the dump for diagnosis and recreate
  the enclave before rerunning; and
- after any mined deposit or known validator, the fresh-state/unknown-validator
  guards reject another run by design.

Under the normal bounded CI timing the expected terminal status is
`DEPOSITED`, not beacon-state inclusion. For a practical runtime, set
`network_params.execution_follow_distance: 8`. Qrysm uses that field before it
considers new execution deposits, so the default mainnet value of 512 would add
roughly 42 minutes at five-second execution blocks.

The mandatory gate covers three deposits, including multi-deposit branch
progression and VM64 packed-branch storage. It does not test beacon-state
activation, a duty by one of these newly generated keys, or a withdrawal by one
of their validator indices. The attestations, proposals, finality, and
withdrawals checked by `systemcheck` belong to the topology's 128 genesis
validators.

Natural activation is not a practical per-PR gate with this mainnet-shaped
configuration. Its 128-slot epochs and 16-epoch execution voting period make an
epoch-zero deposit first vote-eligible at epoch 16, state-included around epoch
24 (about 4 hours 16 minutes), and active no earlier than epoch 32 (about 5
hours 41 minutes). Finality, queueing, and missed slots can delay it further.
The generator's temporary keystore is also removed after preflight. A future
opt-in lifecycle job should retain/import that exact keystore, use an
accelerated voting/seed-lookahead configuration, assert the full 64-byte
credentials in both beacon states, and require a target-key attestation. It
should not be represented as part of the current PR gate.
