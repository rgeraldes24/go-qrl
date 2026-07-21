# Local Testnet E2E Tests

These suites run against the network created by
`scripts/local_testnet/start_local_testnet.sh`. From `scripts/testing/e2e`, run:

```bash
./run_tests.sh
```

The runner resolves the execution RPC endpoints from Kurtosis, waits for block
production, prepares signed deployment transactions, and executes:

- `web3_sanity.js`: block production, block round trips, console namespaces,
  and QIP-55 address checks.
- `api_surfaces.js`: RPC and console coverage for blocks, transactions,
  receipts, fee history, balances, nonces, txpool methods, and malformed
  20/63/65-byte address rejection.
- `logs_topics.js`: raw VM64 log-topic filtering, wildcard and OR filters, and
  malformed topic rejection.
- `event_roundtrip.js`: a checked-in deterministic Hyperion contract exercising
  live VM64 integers, fixed/dynamic bytes, addresses, arrays, storage, emitted
  event decoding, receipt logs, and exact event-signature topic filtering.
- `abi_vm64.js`: embedded web3 ABI calldata, return-data, address, fixed-bytes,
  and dynamic-value encoding.
- `goabi`: Go ABI, generated bindings, qrlclient logs, independently verified
  account/storage proofs, upper-half address anti-aliasing, live precompiles
  (including the exact VM64 deposit root for a nonzero 64-byte withdrawal
  recipient and rejection of the 7,227-byte legacy layout), GraphQL, and
  WebSocket subscription checks.
- `clef_api`: exact JSON-RPC and cryptographic checks against a temporary HTTP
  signer. The suite verifies the imported 64-byte account, ML-DSA-87 signature
  widths and digests for text and QRL typed data, then decodes the signed
  transaction and checks its full 64-byte sender/recipient, body, fees, nonce,
  value, chain ID, public key, descriptor, and raw/JSON consistency.

The suite can also target an existing node directly:

```bash
./run_tests.sh -r http://127.0.0.1:8547 -w ws://127.0.0.1:8548
```

CI uses strict mode, which makes GraphQL and WebSocket availability mandatory,
rebuilds the host clients from the checkout, and writes a TSV result summary
plus one log per suite. The full workflow runs these endpoint-dependent checks
against both execution services with isolated result directories:

```bash
./run_tests.sh -c -o ./logs/test-results
```

For a secondary execution-node run after the standalone Clef suite has already
passed, `-C` skips only that node-independent temporary-signer suite. It does
not skip any console, RPC, GraphQL, WebSocket, ABI, storage, proof, transaction,
or event check:

```bash
./run_tests.sh -c -C -s el-2-gqrl-qrysm -o ./logs/test-results/el-2
```

The first run retains `clef_api`, and the later `systemcheck` separately proves
that both topology execution nodes can transact through the deployed Clef.

## Regenerating the Hyperion fixture

The full drift gate downloads an exact, checksummed Hyperion source archive,
builds commit `f2e6ae7a59e8dafc23a2f34164fdd26180cec2dd` in the immutable Linux build
image pinned by Hyperion's own CI, recompiles the fixture without CBOR metadata,
and regenerates the JavaScript and Go representations in a temporary directory:

```bash
make vm64-fixture-check
```

The default requires Docker and network access. For a local compiler build, the
same gate accepts an exact-commit binary and rejects any other version:

```bash
HYPERION_FIXTURE_HYPC=/path/to/hypc make vm64-fixture-check
```

The reproducibility inputs are pinned in `verify_hyperion_fixture.sh`:

- codeload archive SHA-256
  `d743cf3d6eb5482a425d9bf75bc9aa9778256b4ad3ac84e90373d3f7bbdb76a4`;
- Hyperion's Ubuntu 22.04 CI build image
  `solbuildpackpusher/solidity-buildpack-deps@sha256:4df420b7ccd96f540a4300a4fae0fcac2f4d3f23ffff9e3777c1f2d7c37ef901`.

Both values are checked before compilation: the archive hash is verified
directly, while Docker resolves the build environment by immutable digest.

To update the fixture intentionally, compile with the same pinned VM64 Hyperion
revision recorded in `emitter.js`:

```bash
cd tests/fixtures
hypc --bin --abi --storage-layout --no-cbor-metadata -o . --overwrite EventEmitter.hyp
go run generate_emitter_js.go
cd ../../../../..
go generate ./scripts/testing/e2e/goabi
go test ./scripts/testing/e2e/goabi
```

The fast Go test fails if the checked-in ABI, bytecode, storage layout,
JavaScript fixture, or generated binding drift apart. The full gate additionally
proves that those artifacts are the output of the pinned Hyperion source and
build environment rather than merely being mutually consistent.
