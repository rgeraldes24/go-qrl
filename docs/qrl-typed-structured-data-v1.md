# QRL Typed Structured Data v1

## Status

This document defines the native QRL typed-data format implemented by
`go-qrl`. It replaces the incompatible EIP-712 signing path.

## Constants

| Name | Value |
| --- | --- |
| Version | `1` |
| Digest prefix | UTF-8 `QRL-TYPED-DATA-V1` |
| Hash | Legacy Keccak-256, not NIST SHA3-256 |
| Encoded member word | 64 bytes |
| Signature algorithm | ML-DSA-87 |

JSON is only a transport representation. Implementations parse JSON into typed
values and hash the encoding below. JSON bytes and object-property order are
never hashed.

## Request

A request contains `types`, `primaryType`, `domain`, and `message`.
The mandatory domain declaration is exactly:

```text
QRLTypedDataDomain(
    string name,
    string version,
    uint256 chainId,
    address verifyingContract,
    bytes32 salt
)
```

The declaration uses the compact canonical form:

```text
QRLTypedDataDomain(string name,string version,uint256 chainId,address verifyingContract,bytes32 salt)
```

All five domain values are required. `name` and `version` are non-empty,
`chainId` fits `uint256`, `verifyingContract` is a full Q-address, and
`salt` is exactly 32 bytes. `QRLTypedDataDomain` cannot be the primary type.

Every message and nested struct contains exactly its declared fields. Missing
and extra fields are invalid. Type and field names use
`[A-Za-z][A-Za-z0-9_]*`. Undefined reference types and primitive-name
collisions are invalid.

JSON addresses use full `Q`-prefixed strings. Byte values use
`0x`-prefixed hexadecimal strings. Integers may be decimal JSON integer
tokens or decimal/`0x` strings. Clients should use strings outside their
language's safe integer range.

## Types

Version 1 supports:

- `address`
- `bool`
- explicit `uintN` and `intN`, where `N` is 8 through 512 in multiples of 8
- `bytesN`, where `N` is 1 through 64
- dynamic `bytes`
- `string`
- named structs
- dynamic arrays `T[]`

Bare `uint` and `int`, fixed-point types, optional values, and ABI
`function` values are unsupported.

## Type Description

A struct is described as:

```text
TypeName(fieldType fieldName,fieldType fieldName,...)
```

The primary type comes first. Transitively referenced structs follow once in
ascending bytewise name order. Field order is preserved.

```text
typeHash(T) = keccak256(UTF8(canonicalTypeDescription(T)))
```

## VM64 Encoding

Each struct starts with its type hash in one 64-byte word, followed by one
64-byte word per member:

```text
encodeData(value) = bytes32Word(typeHash(type(value))) ||
                    encodeMember(value.member1) ||
                    ... ||
                    encodeMember(value.memberN)

hashStruct(value) = keccak256(encodeData(value))
```

`bytes32Word` places a 32-byte hash in bytes 0 through 31 and zero-fills bytes
32 through 63. Hashes therefore use the same left alignment as VM64
`bytes32`.

Members encode as follows:

- `address`: all 64 raw address bytes.
- `bool`: right-aligned `0` or `1`.
- `uintN`: range-checked, big-endian, right-aligned, and zero-extended.
- `intN`: range-checked and sign-extended in two's-complement form.
- `bytesN`: left-aligned and zero-padded.
- `string`: `bytes32Word(keccak256(UTF8(value)))`.
- dynamic `bytes`: `bytes32Word(keccak256(rawBytes))`.
- struct: `bytes32Word(hashStruct(value))`.
- array: hash the concatenation of each encoded element and place the hash in a
  `bytes32Word`.

Declared widths remain authoritative. A `uint256` cannot use the high half of
the surrounding 512-bit word.

## Final Digest

```text
keccak256(
    UTF8("QRL-TYPED-DATA-V1") ||
    hashStruct(domain) ||
    hashStruct(message)
)
```

Nonce use, deadlines, and application replay policy remain application
responsibilities.

## Signing Result

The 32-byte digest is signed with the normal QRL ML-DSA-87 wallet operation and
its existing context:

```text
UTF8("ZOND") || 0x01 || descriptor
```

`account_signTypedData` returns:

```json
{
  "version": "1",
  "algorithm": "ML-DSA-87",
  "address": "Q...",
  "digest": "0x...",
  "publicKey": "0x...",
  "descriptor": "0x...",
  "signature": "0x..."
}
```

A verifier derives the Q-address from the public key and descriptor, recomputes
the digest from the supplied typed-data request, and verifies the signature.
The generic `account_signData` method rejects `data/typed` because its raw
byte response omits the public key and descriptor required for verification.

Clef also requires the domain `chainId` to equal its configured chain ID. This
is signing policy, not part of the hash.

## Conformance Vector

`signer/core/testdata/qrl_typed_data_v1.json` contains the normative request,
expected type/domain/message hashes, final digest, and a deterministic
test-only ML-DSA envelope. Production signatures remain hedged.

## Follow-Ups

External-signer convenience integration, additional malformed-input policies,
SDK and Hyperion implementations, and on-chain verification are separate work.
