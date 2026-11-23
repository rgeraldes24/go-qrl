# Bringing go-qrl up to date with go-ethereum

go-qrl was last fully synced with go-ethereum at v1.13.1 (December 2023). Some packages were updated by hand since, so they sit at different points. This plan brings the parts that matter up to go-ethereum's latest release, **v1.17.10** (master `2a2cbe3` for anything newer), while keeping QRL's design.

It is based on a package-by-package comparison of go-qrl `main` (`8d15acf483`) with go-ethereum `master` (`2a2cbe3`), ignoring QRL's renames and design choices. The release-by-release list in [`UPSTREAM.md`](UPSTREAM.md) stays as the record of what each step covers.

## Where go-qrl stands

| Area | State |
|---|---|
| `log`, `event`, `console`, `common` | In sync or ahead (Go modernizations, QRL removals). Nothing to do. |
| `rlp`, `rpc`, `node`, `metrics`, `graphql`, `signer`, `accounts`, `internal`, `cmd` | Mostly current, with a tail of 2024–2026 fixes and features missing. |
| `qrldb` | Pebble v1 with the v1.13 interface and tuning. |
| `p2p` | Discovery still on the v1.13 routing table; dial, NAT and rlpx fixes missing. |
| `trie`, `trie/triedb`, `core/rawdb` | v1.13.1: missing the `triedb/` move, the pathdb rewrite, freezer durability fixes and several corruption fixes. |
| `core/state`, snapshot | v1.13.1 plus QRL changes: missing snapshot bug fixes and the 2024 performance rewrites. |
| `core`, `core/txpool`, `core/vm`, `miner` | Mostly v1.13.1 (`miner` from v1.14): chain rewind/repair, tx indexing, log index and txpool/VM performance a generation behind. |
| `qrl/` (`eth/`), `internal/qrlapi`, `qrlclient` | Protocol hardening, snap-sync fixes, RPC hardening and tracer fixes missing. |

Spot-checked against both trees: the rlpx handshake cap (#30029), pathdb duplicate-root layers (#34642), the snap-sync head hash (#34912), the miner db-error check (#35427), the tx fetcher truncation (#32210) and snapshot memory accounting (#33505) are all real gaps. Every other item is re-checked when it's ported.

## What stays QRL's

Not gaps, and not ported:

- **Naming:** QRL renames (`qrl`, `gqrl`, `QRVM`, `qnode`, `qrl_` RPCs, Q-addresses).
- **Data widths and signatures:** 512-bit words, storage keys and state/trie hashes; ML-DSA-87 signatures and transaction signing.
- **Chain model:** beacon-only sync, and QRL's own fork schedule (QIPs).
- **Removed features:** PoW, clique, blobs/KZG/EIP-4844, EIP-7702, BLS12-381 and BN256 precompiles, the light client and LES, era files, the `personal` namespace, USB wallets, verkle and binary tries, block access lists.

## Decisions needed first

1. **Upstream consensus changes.** None of these is in go-qrl; each needs a QRL decision (a QIP) before any code:
   - **Calldata and gas pricing:** EIP-7623 calldata cost (#30946), EIP-7976 and EIP-7981 (#34748, #34755), EIP-2780 (#35212).
   - **Gas limits and accounting:** EIP-7825 tx gas cap (#31824), EIP-7778 (#33593), EIP-7954 (#33832), EIP-8037/8038 (#33601, #35216).
   - **Other execution changes:** EIP-7708 (#33645), EIP-7997 (#35223).
   - **System contracts and requests:** EIP-2935 historical block hashes (#29465), EIP-4788, EIP-6110/7002/7251/7685 requests (#29431, #30571), EIP-8282 (#35175).
   - **New opcodes:** EIP-7843 SLOTNUM (#33589), EIP-8024 DUPN/SWAPN/EXCHANGE (#33095, #33928).
   - **Contract-collision rule:** EIP-7610 storage-not-empty. Only reachable through a genesis allocation in QRL.
2. **State history version.** go-qrl uses `stateHistoryVersion = 1` for its 512-bit format; upstream's v1 means "raw slot keys" (#30107). Renumber go-qrl's before porting any history work.
3. **rlpx handshake size.** Confirm go-qrl's handshake messages fit in 2 KB before porting the cap (#30029).
4. **discv5 default.** Off in go-qrl, on upstream since #30327: deliberate or not?
5. **Local transaction tracker** (issue rgeraldes24/go-qrl#88): port go-ethereum's `core/txpool/locals`, or not.

## How each step is done

- **Two ways in:**
  - **Catch up in one go:** lightly changed packages, taken to the latest version in one PR, listing go-qrl's intended differences.
  - **Change by change:** consensus, state, trie and sync code, in upstream order. A change already present in a newer form is marked superseded instead.
- **One `[UPSTREAM]` PR per upstream change** or a small coherent group, in cyyber/go-qrl, saying which upstream PRs it covers and what was adapted.
- **Checks:**
  - **Every step:** `go build ./...`, `go vet ./...`, golangci-lint and `go test ./...`.
  - **State, trie, sync, txpool or miner changes:** also the matching qrl-tests lanes on a devnet (`sync`, `resilience-network-restart`, `limits-full-blocks`, the limits lanes for the txpool and RPC).
- **`UPSTREAM.md`** is updated with each PR, so it always shows what's ported, superseded or not applicable.

## Phases

### Phase 1: security and DoS (small, independent fixes)

All small, independent and the highest value.

**Status (2026-11-23):** every item below has a draft `[UPSTREAM]` PR in cyyber/go-qrl, one per go-ethereum PR: #325–#358. The by-hash payload bodies cap stays in rgeraldes24/go-qrl#98.

- **p2p:**
  - **rlpx:** 2 KB handshake cap (#30029), after decision 3.
  - **Discovery:** discv5 FINDNODE liveness (#28686, #30512); bonded-only address prediction (#35861).
  - **Dialing:** dial scheduler race (#29235).
- **QRL protocol and fetcher:**
  - **Tx fetcher:** announcement truncation (#32210).
  - **Pooled transactions:** lookup cap (#35857); duplicate txs drop the peer (#32728).
  - **Message handling:** `msg.Discard` order (#35289); dispatcher hang on peer exit (#30918).
  - **Snap serving:** load accounting and storage-range abort (#35856, #35299).
- **RPC and API:**
  - **Request limits:**
    - `qrl_getProof` key cap (#34617).
    - Range and log-query limits (#33163, #34647, #31876); GraphQL body limit (#35034).
    - User HTTP body limit (#35224).
  - **Filters:** unsubscribe on error and subscription race (#32794, #32894, #33990).
  - **State access:** replay cancellation, release leak and unavailable-state errors (#36036, which includes #36035).
  - **Tracing:** struct-logger memory cap (#30806, #35349, #35360); this also covers issue #89.
  - **Connections:** cancel running calls on connection close (#35798); no timeout responses for notifications (#36019).
  - **Node:** vhost and Bearer case handling (#34693, #35022, #35408).
- **Core:**
  - **txpool:** EIP-2681 nonce check (#32726).
  - **miner:** no sealing after a db error (#35427).
  - **trie:** range-proof check (#33898).
  - **VM:** cold-access SafeAdd (#29023).
  - **consensus:** nil base-fee guard (#33860): not needed, go-qrl headers always carry a base fee.
  - **Payload bodies:** by-hash cap (rgeraldes24/go-qrl#98).

### Phase 2: data integrity, crash and race fixes

- **pathdb:**
  - **History writes:** fail on a history write error (#31781); sync the freezer before the journal (#32557).
  - **Layers and recovery:** duplicate-root layers (#34642); `Recoverable` panic (#29107, #29135).
- **hashdb:** dirties race (#28542).
- **Snapshot:**
  - **Memory:** accounting (#33505); fastcache leak (#28387).
  - **Concurrency:** shutdown race (#33540); locking (#30001, #30011, #30040).
- **Freezer:**
  - **Writes and syncing:** fsync and repair (#30392, #29792); fd leaks (#34735, #33147); truncation order (#34728); empty-table truncation (#35258, #35551).
  - **Freezing and read-only mode:** freeze at the finalized block (#28683); read-only state freezer (#29823, #33025).
  - **Tx unindexing:** loop on missing bodies (#33573).
- **Chain:**
  - **Snap sync and receipts:** head hash written after snap sync (#34912); receipt-chain gaps (#35190).
  - **Rewind and shutdown:** stale finalized block after rewind (#33486); prefetcher shutdown race (#35821).
  - **Payload events:** `blockProcFeed` for payload inserts (#31065).
- **txpool:** pending nonces across reorgs (#35654); reset goroutine leak (#31030).
- **Downloader:** bad bodies/receipts drop the peer (#34745); queue index (#34870); beacon ping and pivot races (#35515, #35405); skeleton cleanup and locking (#33481, #33428, #32222); sync-status unsubscribe deadlock (#36046).
- **Catalyst:** forkchoice rewind to an older canonical head (#34767, #35519, #35804); fetch an unknown head (#31928); attribute check order (#36190); dev-mode sealing stall (#33146).
- **Tracers:** shared block context race (#29238); `reason` race in native tracers (#34827).
- **Accounts and tools:** keystore event order, scan/delete race and timezone (#36099, #35846, #35245); jsre panic (#36202).
- **p2p:** 4-in-6 address unwrap (#29944); timeout loop exit (#34743); in-progress inbound dials (#33198); discv4 left running (#33005); FairMix leak (#35298); UPnP refresh (#30265, #31486, #31566); endpoint locality (#29801, #29827, #29844).

### Phase 3: snap sync and chain repair

One coherent area, checked together on a devnet (sync, full-outage and limits-full-blocks lanes, plus a new spec for a node rewound below its pivot).

- **Trie healing:** dangling and inconsistent nodes (#28163 done as cyyber/go-qrl#324, then #28595, #29485 with #28718, #28724).
- **Stack trie and gentrie:** the stacktrie rework (#28233, #28327, #28350, #28361); gentrie, which fixes a cause of snap-sync corruption (#29313).
- **Snap sync:** dangling-node cleanup (#30258); skip finished storages on resume (#29378); idle peers after a revert (#33790); unproven zero-origin range (#35722).
- **Chain rewinding:** #29196, #29245, #35252.
- **Missing genesis state (#28171):** with go-qrl's own recovery path. The beacon-driven sync restarts a snap sync when the head state is missing, instead of upstream's `modeAndLocalHead`.
- **Per-peer request tracking:** delayed decoding and response validation (#33835, #35862, #35904, with the `p2p/tracker` changes #33370, #33940).

### Phase 4: structural catch-up

Mechanical moves that make every later port simpler. Each lands as its own step with no behaviour change.

- **Trie layout:** `trie/triedb` → `triedb/`, removing trie's dependency on triedb (#28824); `triestate` into pathdb (#29861); NodeDatabase and StateReader interfaces (#29988, #30712, #29761).
- **qrldb:** interface catch-up (DeleteRange, SyncKeyValue, `Batch.Close`, ancient-store changes) and pebble v2 with upstream tuning.
- **p2p:** `netip` migration (#29891), the base for later p2p fixes.
- **metrics, rlp, rpc:** metrics API refactor (#30814, #31400); rlp `RawList` and iterator (#33755 and follow-ups); rpc JSON encoding path (#35518, #33969).
- **Live tracing:** `core/tracing` hooks (#29189, #30569). Large, but upstream's tracers, simulation and VM work since 2024 build on it.

### Phase 5: performance

- **pathdb rewrite:** background flush (#30464); flat state and snapshot generation inside pathdb (#30643, #30654, #30661, #31306, #30971); file journal (#32060); bloom and layer index (#35951, #35973); background history (#35949). Then the history indexer and historical `qrl_getProof` (#31156, #31161, #32727), after decision 2.
- **State:** prefetcher rework (#29519, #29919, #29807, #32134) and the in-block parallel prefetcher (#31557, #32071, #35404); parallel commit (#29681); journal and dirty-slot fixes (#29641, #29731, #29661, #28880, #30361); uint256 balances (#28598); smaller allocation cuts.
- **Chain:** `txindexer` (#28857, #28703, #29343, #31752); filtermaps log index replacing bloombits (#31079, #31080); reorg memory (#30600).
- **txpool and miner:** uint256 (#28606) and allocation cuts; queue split (#32270); builder prefetcher and timing (#33945, #33908, #33943).
- **VM:** memory reuse and uint64 indices (#30137, #30252); stack and push work (#30662, #31267, #33960, #35156, #35203); jumpdest cache (#32143, #34850, #35881); precompile result cache (#35388, with `common/lru` sizes), which matters for the ML-DSA precompiles; modexp (#32576), if Go's math/big still needs it.
- **Hashing and types:** trie commit and hashing (#30545, #31902, #30932, #30743, #32220); DeriveSha and blooms (#30747, #35738, #31129, #32882, #35234). Worth more with keccak-512.
- **Database and node:** pebble tuning (with Phase 4); cache defaults and GOGC/memory-limit tuning (#33975, #34851, #35363, #35407); no gzip on the engine API (#35057); faster newPayload decoding (#36029).
- **RPC:** gas estimator package (#28600, #28618, #31735); receipt lookup cost (#31752).
- **p2p:** discovery table revalidation rework (#29572 and follow-ups #29836, #29864, #29960, #30239); lookup `waitForNodes` (#32517, #32881, #32912, #34898, #35002); dial filtering and prefetch (#31944, #31779, #32572).

### Phase 6: features

- **RPC methods:** `qrl_simulateV1` with the override package (#27720, #34616); `qrl_getStorageValues` (#32591); `qrl_sendRawTransactionSync` (#32830); receipts subscription (#32697); `blockTimestamp` on txs and logs (#33709); estimateGas block overrides (#30695); `debug_clearTxpool`.
- **Clients and accounts:** matching `qrlclient` methods; abigen v2 and `bind/v2` (#31379 and follow-ups); ABI custom errors (#30738); EIP-712 arrays (#30175, #30620, #31049).
- **Tracers:** `txIndex`, prestate options, call-tracer log positions (#28460, #30648, #31855, #31427, #28389, #30497, #30539).
- **Node:** HTTP/2 for RPC (#33812, #33922); OpenTelemetry (#33452, #33484); `debug_setMemoryLimit` and memlimit (#31441, #34947).
- **Network:** NAT in TOML and STUN (#31041, #31192, #31064); static-node DNS re-resolution (#30822, #35880).
- **Tooling:**
  - **Metrics:** discovery metrics (#35673) and the remaining metrics features.
  - **Databases:** `db inspect-trie`, `inspect-history` and the other database tools (#28892).
  - **qrvm:** runners and flags (#33800, #36111, #32824, #34732, #34733); `devp2p` discovery commands (#35254).

### Phase 7: qrysm and Prysm

The same comparison for qrysm against Prysm, once go-qrl's phases 1–3 are done. Known items already in flight there: attestation rewards (cyyber/qrysm#114, #123), and the late-block cutoff, slow-peer sync and payload body cache (rgeraldes24/qrysm#74, #75, #76).

## Order and size

| Phase | Size | Depends on |
|---|---|---|
| Decisions | — | — |
| 1. Security and DoS | about 35 small PRs | decision 3 for the rlpx cap |
| 2. Integrity and crash fixes | about 40 small PRs | — |
| 3. Snap sync and chain repair | about 10 larger PRs | phases 1–2 in those areas |
| 4. Structural catch-up | about 8 large, mechanical PRs | — (best before 5) |
| 5. Performance | about 30 PRs, several large | phase 4; decision 2 for history |
| 6. Features | as needed | phase 4 for tracing-based ones |
| 7. qrysm | separate plan | go-qrl 1–3 |

Phases 1 and 2 can run in parallel, one package at a time. Phase 4 is best done before phase 5, since the pathdb rewrite and most performance work assume the new layout. Consensus EIPs only enter once decided.
