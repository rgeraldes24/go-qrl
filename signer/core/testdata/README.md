### QRL Typed Structured Data v1 tests

The JSON fixtures in this directory exercise the native QRL typed-data format.
The root fixtures cover valid arrays and nested custom types, plus validation
failures retained from the previous typed-data suite. Files prefixed with
`expfail_` must be rejected for the condition represented by their filename.
`qrl_typed_data_v1.json` contains the normative QRL v1 golden hashes.

The `fuzzing` directory contains regression inputs derived from payloads that
previously caused parser crashes or hangs. Their contents have been updated for
the QRL v1 domain and VM64 type rules.
