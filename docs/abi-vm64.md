# Hyperion VM64 ABI Model

This document describes the current QRL Hyperion ABI model used by the Go ABI
library, generated bindings, embedded web3.js runtime, and RPC-facing log/topic
surfaces.

The key rule is that ABI words are 64 bytes wide. This is a VM64 ABI model for
calldata, return data, event logs, generated bindings, and API surfaces. It does
not redefine Solidity persistent storage packing.

## Word Size

- ABI word size: 64 bytes / 512 bits.
- Function selector size: 4 bytes.
- QRL address size: 64 bytes.
- Log topic size: 64 bytes.
- Event selector hash size: 32 bytes Keccak-256, carried inside a 64-byte log
  topic.

## Encoding Rules

The ABI layout follows the Hyperion ABI layout and alignment model with VM64
word sizes.

- Integers, booleans, addresses, offsets, and lengths are encoded as big-endian
  values in the low-order bytes of a 64-byte word.
- Fixed bytes values, `bytesN`, are placed in the high-order bytes of a 64-byte
  word and right-padded with zeroes.
- Dynamic `bytes` and `string` values encode as:
  - a 64-byte length word,
  - the raw payload bytes,
  - zero right-padding to the next 64-byte boundary.
- Dynamic values in tuples, arrays, and multi-argument payloads are referenced by
  byte offsets. Offsets must be 64-byte aligned and must point outside the
  current head region.
- Decoding is strict: short words, invalid hex, non-zero padding, invalid bool
  values, out-of-range integers, and malformed dynamic offsets are rejected.

## Supported ABI Types

The Go ABI library supports:

- `address`: a 64-byte QRL address.
- `bool`.
- `uintN` and `intN` where `N` is explicit, between 8 and 512 inclusive, and a
  multiple of 8.
- `bytesN` where `N` is between 1 and 64 inclusive.
- Dynamic `bytes`.
- `string`.
- Static arrays.
- Dynamic arrays.
- Nested arrays.
- Tuples/structs, including nested tuple and array combinations.
- Contract types in ABI JSON, treated as `address`.

For Go values:

- `uint8`, `uint16`, `uint32`, `uint64`, `int8`, `int16`, `int32`, and `int64`
  map to their matching ABI widths.
- Integer widths above 64 bits use `*big.Int`.
- Fixed bytes use Go byte arrays.
- Dynamic bytes use `[]byte`.
- Addresses use `common.Address`.

## Integer Widths

VM64 widens the ABI slot, but it does not make every declared integer a 512-bit
integer.

- `uint256` and `int256` remain 256-bit ABI types.
- `uint512` and `int512` may use the full 64-byte VM64 word.
- Smaller integer types must still fit their declared width.
- Signed integers are sign-extended across the full 64-byte ABI word.
- Unsigned integers must have zero high-order padding above their declared
  width.

## Fixed And Dynamic Bytes

- `bytes1` through `bytes64` are supported.
- Fixed bytes are left-aligned and right-padded.
- Dynamic `bytes` is length-prefixed and padded to a 64-byte boundary.
- Fixed bytes decoding rejects non-zero right padding.
- Dynamic bytes decoding rejects non-zero right padding.

## Unsupported ABI Types

The following are rejected:

- Bare `uint` and `int` without an explicit width.
- Integer widths outside `8..512`.
- Integer widths that are not multiples of 8.
- `bytes0`.
- `bytes65` and wider fixed bytes.
- Fixed-point types.
- Hash pseudo-types that are not represented as ABI fixed bytes.
- ABI `function` values at packing, unpacking, topic, or binding use sites.

ABI `function` values are still parseable from JSON ABI metadata so callers can
inspect ABI definitions. They are rejected when used because a QRL external
function value would be:

```text
address64 + selector4 = 68 bytes
```

That cannot fit in one 64-byte ABI word.

## Events And Log Topics

Event topics are full `common.LogTopic` values and are always 64 bytes.

Non-anonymous event topic0:

- The event signature hash remains a 32-byte Keccak-256 value.
- QRVM logs carry that value in a 64-byte topic.
- The 32-byte hash is right-aligned in the low half of the topic.

Indexed event arguments:

- Indexed scalar values use their ABI word encoding in a 64-byte topic.
- Indexed `address` values are right-aligned.
- Indexed integers and booleans are right-aligned.
- Indexed fixed bytes are left-aligned and right-padded.
- Indexed dynamic `string` and `bytes` values are Keccak-hashed and the 32-byte
  hash is right-aligned inside the 64-byte topic.
- Indexed tuple, array, and slice values are exposed as opaque `common.LogTopic`
  values because the original value cannot be reconstructed from the log topic.

`abi.MakeTopics` conventions:

- Use `common.LogTopic` when the caller already has a final 64-byte topic.
- `common.Hash` is treated as an ABI `bytes32` value and is left-aligned like
  other fixed bytes.
- `string` and `[]byte` inputs are treated as indexed dynamic preimages and are
  hashed.
- Tuple, array, and slice filter rules require precomputed `common.LogTopic`
  values.

## Generated Go Bindings

Generated bindings use the VM64 ABI model for call data, return data, and event
handling.

Generated event structs:

- Static indexed arguments use their decoded Go type.
- Indexed `string`, `bytes`, tuple, array, and slice values use
  `common.LogTopic` because only the topic value is available in the log.

Generated filter/watch parameters:

- Indexed `string` and dynamic `bytes` remain preimage types so `abi.MakeTopics`
  can hash them.
- Indexed tuple, array, and slice filters use `common.LogTopic` and require the
  caller to provide the precomputed topic.

Bindings that contain ABI `function` values in constructors, methods, outputs,
events, or errors are rejected.

## Embedded web3.js

The embedded web3.js ABI coder follows the same VM64 word model.

It supports:

- 64-byte ABI words for inputs and outputs.
- 64-byte QRL address ABI words.
- `uintN` and `intN` bounds up to 512 bits.
- `bytes1` through `bytes64`.
- Dynamic arrays and nested dynamic values using 64-byte offsets.
- 64-byte event topics.

It rejects:

- short static output words,
- malformed dynamic offsets,
- offsets into the head region,
- unaligned offsets,
- truncated dynamic tails,
- non-zero dynamic padding,
- invalid topic hex.

## RPC, GraphQL, And API Surfaces

RPC and API surfaces expose VM64-width values where the underlying runtime value
is VM64-width.

- Raw JSON-RPC log topic filters require full 64-byte topic hex.
- Higher-level ABI event paths use `Event.Topic()` and `common.LogTopic` to
  construct full topics.
- GraphQL log topics use the `Bytes64` scalar.
- Receipt log topics and log data are exposed at VM64 widths.
- Storage values exposed through QRL APIs use the full storage value width.
- Q-address RPC argument formatting uses full 64-byte QRL addresses.

## Compatibility Notes

This is a fork-level wire-format alignment.

Out-of-tree consumers should check for:

- code that stores or compares old 32-byte event topics,
- code that right-aligns `common.Hash` as if it were already a full log topic,
- generated bindings that expect indexed dynamic/composite fields to be their
  preimage type instead of `common.LogTopic`,
- JSON-RPC filter code that sends 32-byte topics directly,
- ABI code that assumes 32-byte offsets, lengths, or fixed bytes padding.

After this model lands:

- regenerate out-of-tree abigen bindings,
- regenerate Qrysm deposit-contract bindings,
- update Hyperion compiler/codegen assumptions for ABI calldata, return data,
  event logs, and linked bytecode to use 64-byte VM64 ABI words and topics.

