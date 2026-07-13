## Changelog for external API

The API uses [semantic versioning](https://semver.org/).

TL;DR: Given a version number MAJOR.MINOR.PATCH, increment the:

* MAJOR version when you make incompatible API changes,
* MINOR version when you add functionality in a backwards-compatible manner, and
* PATCH version when you make backwards-compatible bug fixes.

Additional labels for pre-release and build metadata are available as extensions to the MAJOR.MINOR.PATCH format.

## 7.0.0

- Replaced EIP-712 hashing in `account_signTypedData` with QRL Typed Structured
  Data v1, using 64-byte VM words and full Q-addresses.
- Changed `account_signTypedData` to return a verification envelope containing
  the ML-DSA-87 public key, wallet descriptor, digest, and signature.
- Reserved `application/vnd.qrl.typed-data+json` for the dedicated typed-data
  API. `account_signData` rejects typed data because its raw byte response
  cannot carry the metadata required for ML-DSA verification.
