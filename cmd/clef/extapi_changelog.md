## Changelog for external API

The API uses [semantic versioning](https://semver.org/).

TL;DR: Given a version number MAJOR.MINOR.PATCH, increment the:

* MAJOR version when you make incompatible API changes,
* MINOR version when you add functionality in a backwards-compatible manner, and
* PATCH version when you make backwards-compatible bug fixes.

Additional labels for pre-release and build metadata are available as extensions to the MAJOR.MINOR.PATCH format.

## 1.0.0

- Replaced EIP-712 hashing in `account_signTypedData` with QRL Typed Structured
  Data v1, using 64-byte VM words and full Q-addresses.
- Changed `account_signTypedData` to return a verification envelope containing
  the ML-DSA-87 public key, wallet descriptor, digest, and signature.
- Kept `data/typed` as the typed-data MIME identifier. `account_signData`
  rejects typed data because its raw byte response cannot carry the metadata
  required for ML-DSA verification; callers must use `account_signTypedData`.
