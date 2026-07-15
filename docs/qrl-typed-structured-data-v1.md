# QRL Typed Structured Data v1

## Status

This document defines the native QRL typed-data format implemented by
`go-qrl`. It replaces the incompatible EIP-712 signing path.

## Constants

| Name | Value |
| --- | --- |
| Version | `1` |
| Digest prefix | `0x1901` |
| Hash | Legacy Keccak-256, not NIST SHA3-256 |
| Encoded member word | 64 bytes |
| Signature algorithm | ML-DSA-87 |

JSON is only a transport representation. Implementations parse JSON into typed
values and hash the encoding below. JSON bytes and object-property order are
never hashed.

## Request

A request contains `types`, `primaryType`, `domain`, and `message`.
The domain declaration contains one or more of these fields:

```text
QRLTypedDataDomain(
    string name,
    string version,
    uint256 chainId,
    address verifyingContract,
    bytes32 salt
)
```

Applications include the fields relevant to their signing domain. The field
declaration and order are part of the domain type hash. For example, the
complete declaration uses the compact form:

```text
QRLTypedDataDomain(string name,string version,uint256 chainId,address verifyingContract,bytes32 salt)
```

When present, `chainId` fits `uint256`, `verifyingContract` is a full Q-address,
and `salt` is exactly 32 bytes. Signatures intended for on-chain verification
should include `chainId` and `verifyingContract`.

Every message and nested struct contains exactly its declared fields. Missing
and extra fields are invalid. Undefined reference types are invalid.

JSON addresses use full `Q`-prefixed strings. Byte values use
`0x`-prefixed hexadecimal strings. Integers may be decimal JSON integer
tokens or decimal/`0x` strings. Values outside the JSON implementation's
safe integer range must be encoded as strings.

## Types

Version 1 supports:

- `address`
- `bool`
- `uint` and `int`, interpreted as 512-bit integers
- explicit `uintN` and `intN`, where `N` is 8 through 512 in multiples of 8
- `bytesN`, where `N` is 1 through 64
- dynamic `bytes`
- `string`
- named structs
- dynamic arrays `T[]`

Fixed-point types, optional values, and ABI `function` values are unsupported.

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
    0x19 ||
    0x01 ||
    hashStruct(domain) ||
    hashStruct(message)
)
```

Nonce use, deadlines, and application replay policy remain application
responsibilities.

## Signing

The 32-byte digest is signed with the normal QRL ML-DSA-87 wallet operation and
its existing context:

```text
UTF8("ZOND") || 0x01 || descriptor
```

`account_signTypedData` returns the raw ML-DSA-87 signature, preserving the
existing Clef API shape. Verification also requires the signing public key and
wallet descriptor, which are obtained through the wallet or application flow.

## Follow-Ups

SDK and Hyperion implementations and on-chain verification are separate work.
