# QRL Typed Structured Data v1

## Status

This document defines the version 1 wire format implemented by `go-qrl`.
It is a native QRL format. It is not an EIP-712 compatibility mode.

## Constants

| Name | Value |
| --- | --- |
| Version | `1` |
| Digest prefix | ASCII `QRL-TYPED-DATA-V1` |
| Digest prefix hex | `0x51524c2d54595045442d444154412d5631` |
| Hash | Keccak-256, 32 bytes |
| Encoded member word | 64 bytes |
| Signature algorithm | ML-DSA-87 |

All text used for hashing is UTF-8. Concatenations below contain no implicit
length, separator, or terminator bytes beyond those shown explicitly.

## JSON Object

A request has four required properties and no unknown top-level properties:

```json
{
  "types": {
    "QRLTypedDataDomain": [
      {"name":"name","type":"string"},
      {"name":"version","type":"string"},
      {"name":"chainId","type":"uint256"},
      {"name":"verifyingContract","type":"address"},
      {"name":"salt","type":"bytes32"}
    ],
    "Transfer": [
      {"name":"from","type":"address"},
      {"name":"to","type":"address"},
      {"name":"amount","type":"uint512"},
      {"name":"nonce","type":"uint64"},
      {"name":"deadline","type":"uint64"}
    ]
  },
  "primaryType": "Transfer",
  "domain": {
    "name": "QRL Wallet",
    "version": "1",
    "chainId": "1337",
    "verifyingContract": "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
    "salt": "0x0000000000000000000000000000000000000000000000000000000000000000"
  },
  "message": {
    "from": "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
    "to": "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
    "amount": "1",
    "nonce": "0",
    "deadline": "2000000000"
  }
}
```

The `QRLTypedDataDomain` declaration is mandatory and its five fields, types,
and order are exact. Domain values are all required. `name` and `version` must
be non-empty, `chainId` must fit `uint256`, `verifyingContract` must be a full
Q-address, and `salt` must contain exactly 32 bytes.

The primary message and every nested struct must contain exactly its declared
fields. Missing and extra fields are invalid. Names are case-sensitive ASCII
identifiers matching `[A-Za-z_][A-Za-z0-9_]*`. Duplicate fields, undefined
types, primitive-name collisions, recursive type graphs, and duplicate JSON
object keys at any depth are invalid in v1.

JSON addresses are full `Q`-prefixed strings. Byte values are even-length,
`0x`-prefixed hexadecimal strings. Booleans use JSON `true` or `false`.
Integers may be lossless JSON integer tokens or decimal/`0x` hexadecimal
strings; clients should use strings for values outside their language's safe
integer range. Strings are hashed as their exact UTF-8 bytes without Unicode
normalization. Arrays use JSON arrays and structs use JSON objects.

## Type Grammar

Version 1 supports:

- `address`
- `bool`
- explicit `uintN` and `intN`, where `N` is 8 through 512 in multiples of 8
- `bytesN`, where `N` is 1 through 64
- `bytes`
- `string`
- named structs
- static arrays `T[N]` and dynamic arrays `T[]`, including nested arrays

Widths and array lengths use canonical decimal notation without leading zeroes.
Bare `uint` and `int`, fixed-point types, optional values, and ABI `function`
values are not supported. A static or supplied dynamic array may contain at
most 1024 elements. Implementations reject more than 256 types, more than 256
fields per type, or nesting deeper than 32 levels.

## Canonical Type Description

The canonical description of a struct is:

```text
TypeName(fieldType fieldName,fieldType fieldName,...)
```

The primary type is emitted first. Every transitively referenced struct is
then emitted once in ascending bytewise name order. Field declaration order is
preserved. No whitespace is inserted except the single space between a field's
type and name.

For example:

```text
Mail(Person from,Person to,string contents)Person(string name,address wallet)
```

The type hash is:

```text
typeHash(T) = keccak256(UTF8(canonicalTypeDescription(T)))
```

## VM64 Member Encoding

Every encoded struct contains one 64-byte word for its type hash followed by
one 64-byte word for each declared member:

```text
encodeData(value) = bytes32Word(typeHash(type(value))) ||
                    encodeMember(value.member1) ||
                    ... ||
                    encodeMember(value.memberN)

hashStruct(value) = keccak256(encodeData(value))
```

`bytes32Word(hash)` places the 32-byte hash in bytes 0 through 31 and fills
bytes 32 through 63 with zeroes. This is left alignment, matching a VM64
`bytes32`; a Keccak hash remains 32 bytes.

Primitive members are encoded as follows:

- `address`: the complete 64 raw address bytes.
- `bool`: 63 zero bytes followed by `0x00` or `0x01`.
- `uintN`: big-endian, right-aligned, and zero-extended to 64 bytes.
- `intN`: range-checked at its declared width, then two's-complement
  sign-extended to 64 bytes.
- `bytesN`: raw bytes left-aligned, followed by zeroes to 64 bytes.
- `string`: `bytes32Word(keccak256(UTF8(value)))`.
- dynamic `bytes`: `bytes32Word(keccak256(rawBytes))`.
- struct: `bytes32Word(hashStruct(value))`.
- array: encode each element according to these rules, concatenate the
  resulting 64-byte words, then encode
  `bytes32Word(keccak256(concatenatedElements))`.

Declared widths are authoritative. For example, a `uint256` rejects a value
with bit 256 set even though the enclosing member word is 512 bits.

## Final Digest

Let `domainHash` be the hash of the `QRLTypedDataDomain` value and
`messageHash` be the hash of the primary message. The signed digest is exactly:

```text
keccak256(
    UTF8("QRL-TYPED-DATA-V1") ||
    domainHash ||
    messageHash
)
```

Changing the chain ID, verifying contract, salt, primary type, any member,
nonce, or deadline changes the digest. Nonce uniqueness and deadline policy
are application responsibilities; v1 makes those values signable but does not
maintain replay state.

## ML-DSA Signature Envelope

The 32-byte digest is signed by the normal QRL ML-DSA-87 wallet operation.
Typed-data v1 does not replace the wallet's FIPS 204 context. For the current
wallet format that context is:

```text
UTF8("ZOND") || 0x01 || descriptor
```

The typed-data digest prefix supplies application-level separation from QRL
transactions and plain signed messages. ML-DSA cannot recover a public key, so
`account_signTypedData` returns a verification envelope:

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

For ML-DSA-87 v1, the public key is exactly 2592 bytes, the descriptor is
exactly 3 bytes, and the signature is exactly 4627 bytes. All three are
lower-level byte strings serialized as `0x`-prefixed hexadecimal JSON.

A verifier must:

1. Require the exact version, algorithm, and byte lengths.
2. Parse the descriptor and derive the Q-address from the public key and
   descriptor using the standard QRL address derivation.
3. Require that derived address to equal the envelope address.
4. Recompute the typed-data digest and require it to equal `digest`.
5. Verify the ML-DSA-87 signature with the descriptor-bound signing context.

The generic `account_signData` path must not accept this typed-data MIME type,
because its byte-string response omits the public key and descriptor required
for independent verification.

## Golden Vector

The normative machine-readable vector is
`signer/core/testdata/qrl_typed_data_v1.json`. Its expected values are:

```text
typeHash    = 0xf83b6906de1dd35c2089de5694091f4d5f514070a32365057a5767edf0d61955
domainHash  = 0xc917c713560432d820854ef9fa03bedb230f7ff66fc9aa83df31c81d96b35484
messageHash = 0xe4bf84b96e16b2bbfe2844bd9b847d7bfa4f4b88093d67f24294f621a426d574
digest      = 0x37ca63f91ec5cb22459c21a002724067bb6b7c806fe82b52f4237b478a209a69
```

Signatures are hedged and therefore are not deterministic golden values.
Every produced signature must nevertheless verify against the vector digest.

## Scope Boundaries

This specification defines hashing, off-chain signing, and verification in
`go-qrl`. A matching Hyperion coder, SDK coders, and practical on-chain
verification still require coordinated follow-up work. In particular, an
ML-DSA verification precompile address, activation rule, and gas schedule are
consensus decisions and are intentionally not assigned by this document.
