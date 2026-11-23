#!/usr/bin/env python3
"""Rebuild the pinned migration artifact with a Hyperion compiler (not solc)."""
import argparse
import hashlib
import json
from pathlib import Path
import subprocess


def widen_mapping_hashes(runtime):
    """Adapt this contract's compiler-generated slot hashes to KECCAK512.

    The source uses SHA-256 explicitly; every KECCAK256 instruction emitted
    by the pinned compiler is a mapping-slot calculation. Keep PUSH data,
    jump offsets, and the compiler's trailing CBOR metadata unchanged.
    """
    code = bytearray.fromhex(runtime)
    # This adaptation is deliberately limited to the reviewed pinned runtime.
    # Compiler upgrades must review both slot hashing and uint256 cleanup masks.
    if hashlib.sha256(code).hexdigest() != "5f39ec4e020fc3613128d2128d68ded8c7e5cf23dbcbb8125ac2750529314b10":
        raise ValueError("unreviewed compiler runtime; review the 512-bit storage adaptation")
    # Mapping getters (including nested getters), nested weight lookup,
    # and usedOTS read/write mask slots to 256 bits in this compiler.
    # Replace their (1 << 256) - 1 masks with (1 << 512) - 1, preserving
    # instruction lengths and all jump destinations. Other uint256 masks
    # belong to the explicit legacy SHA-256/ABI operations and stay intact.
    for pc in (392, 448, 489, 566, 618, 674, 2680, 3574, 3848):
        if code[pc - 4:pc + 5] != bytes.fromhex("600160016101001b03"):
            raise ValueError("unrecognized mapping mask")
        code[pc + 1] = 0x02
    metadata_size = int.from_bytes(code[-2:], "big")
    end = len(code) - metadata_size - 2
    if end < 1 or code[end - 1] != 0xFE or code[end] != 0xA1:
        raise ValueError("unrecognized compiler metadata; review mapping hash adaptation")
    pc = 0
    while pc < end:
        opcode = code[pc]
        if opcode == 0x20:
            code[pc] = 0x21
        pc += 1 + (opcode - 0x5F if 0x60 <= opcode <= 0x9F else 0)
    if pc != end:
        raise ValueError("truncated PUSH in contract runtime")
    return code.hex()


parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--hypc", required=True, help="path to Hyperion hypc")
args = parser.parse_args()
directory = Path(__file__).resolve().parent
source = (directory / "LegacyQRLMigration.hyp").read_text()
request = {
    "language": "Hyperion",
    "sources": {"LegacyQRLMigration.hyp": {"content": source}},
    "settings": {
        "optimizer": {"enabled": True, "runs": 200},
        "qrvmVersion": "zond",
        "metadata": {"bytecodeHash": "none"},
        "outputSelection": {"*": {"*": ["abi", "storageLayout", "qrvm.deployedBytecode.object"]}},
    },
}
result = subprocess.run([args.hypc, "--standard-json"], input=json.dumps(request), text=True, capture_output=True, check=True)
output = json.loads(result.stdout)
errors = [error for error in output.get("errors", []) if error["severity"] == "error"]
if errors:
    raise SystemExit("\n".join(error["formattedMessage"] for error in errors))
contract = output["contracts"]["LegacyQRLMigration.hyp"]["LegacyQRLMigration"]
expected = ["snapshotHeight", "snapshotBlockHash", "snapshotCommitment", "targetChainId", "totalUnclaimed", "remaining", "accountKind", "usedOTS", "treeHeight", "threshold", "weight"]
actual = contract["storageLayout"]["storage"]
word = "t_uint512"
mapping = "t_mapping(t_uint512,t_uint512)"
nested = "t_mapping(t_uint512,t_mapping(t_uint512,t_uint512))"
expected_types = [word] * 5 + [mapping, mapping, nested, mapping, mapping, nested]
types = contract["storageLayout"]["types"]
if (
    len(actual) != len(expected)
    or any(
        entry["label"] != name
        or int(entry["slot"]) != slot
        or entry["offset"] != 0
        or entry["type"] != expected_types[slot]
        for slot, (name, entry) in enumerate(zip(expected, actual))
    )
    or types[word] != {"encoding": "inplace", "label": "uint512", "numberOfBytes": "64"}
    or any(
        types[name]["encoding"] != "mapping"
        or types[name]["key"] != word
        or types[name]["value"] != value
        or types[name]["numberOfBytes"] != "64"
        for name, value in [(mapping, word), (nested, mapping)]
    )
):
    raise SystemExit("contract storage layout changed; update and test the genesis builder first")
artifact = {
    "compiler": subprocess.check_output([args.hypc, "--version"], text=True).strip(),
    "source_sha256": hashlib.sha256(source.encode()).hexdigest(),
    "settings": request["settings"],
    "abi": contract["abi"],
    "runtime": widen_mapping_hashes(contract["qrvm"]["deployedBytecode"]["object"]),
    "storage_hash": "keccak512",
    "storageLayout": contract["storageLayout"],
}
(directory / "LegacyQRLMigration.json").write_text(json.dumps(artifact, indent=2) + "\n")
print(f"Wrote LegacyQRLMigration.json ({len(artifact['runtime']) // 2} runtime bytes)")
