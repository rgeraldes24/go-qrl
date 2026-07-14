# Local Testnet E2E Tests

These suites run against the network created by the local-testnet tooling in
PR #96. From `scripts/local_testnet`, run:

```bash
./run_tests.sh
```

The runner resolves the execution RPC endpoints from Kurtosis, waits for block
production, prepares signed deployment transactions, and executes:

- `web3_sanity.js`: block production, block round trips, console namespaces,
  and QIP-55 address checks.
- `api_surfaces.js`: RPC and console coverage for blocks, transactions,
  receipts, fee history, balances, nonces, and txpool methods.
- `logs_topics.js`: raw VM64 log-topic filtering, wildcard and OR filters, and
  malformed topic rejection.
- `event_roundtrip.js`: contract deployment, emitted event decoding, receipt
  logs, and exact event-signature topic filtering.
- `abi_vm64.js`: embedded web3 ABI calldata, return-data, address, fixed-bytes,
  and dynamic-value encoding.
- `goabi`: Go ABI, generated bindings, qrlclient logs, storage proofs, GraphQL,
  and WebSocket subscription checks.
- `clef_api`: Clef account, data-signing, typed-data, and transaction-signing
  checks against a temporary HTTP signer.

The suite can also target an existing node directly:

```bash
../run_tests.sh -r http://127.0.0.1:8547 -w ws://127.0.0.1:8548
```
