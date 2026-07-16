#!/usr/bin/env bash

set -Eeuo pipefail

QRYSM_REPO=${1:-}
GENESIS_GENERATOR_REPO=${2:-}

BEACON_IMAGE=local/qrysm-beacon:vm64
VALIDATOR_IMAGE=local/qrysm-validator:vm64
GENESIS_IMAGE=local/qrl-genesis-generator:vm64

if [ -z "$QRYSM_REPO" ] || [ -z "$GENESIS_GENERATOR_REPO" ]; then
    echo "usage: $0 <cyyber/qrysm checkout> <qrl-genesis-generator PR 9 checkout>" >&2
    exit 1
fi

for command in bazel docker git; do
    if ! command -v "$command" &> /dev/null; then
        echo "$command is not installed or not on PATH." >&2
        exit 1
    fi
done

if [ ! -d "$QRYSM_REPO/.git" ] && ! git -C "$QRYSM_REPO" rev-parse --git-dir &> /dev/null; then
    echo "Not a git checkout: $QRYSM_REPO" >&2
    exit 1
fi

if [ ! -f "$GENESIS_GENERATOR_REPO/Dockerfile.local" ]; then
    echo "Dockerfile.local not found in $GENESIS_GENERATOR_REPO" >&2
    exit 1
fi

QRYSM_REPO=$(cd "$QRYSM_REPO" && pwd)
GENESIS_GENERATOR_REPO=$(cd "$GENESIS_GENERATOR_REPO" && pwd)
QRYSM_SHA=$(git -C "$QRYSM_REPO" rev-parse HEAD)
GENESIS_SHA=$(git -C "$GENESIS_GENERATOR_REPO" rev-parse HEAD)

echo "Building Qrysm images from $QRYSM_SHA."
PLATFORM_ARGS=()
if [ "$(uname -m)" = "arm64" ]; then
    PLATFORM_ARGS=(--platforms=@io_bazel_rules_go//go/toolchain:linux_arm64_cgo)
fi

(
    cd "$QRYSM_REPO"
    bazel build //cmd/beacon-chain:oci_image_tarball "${PLATFORM_ARGS[@]}" --config=release
    docker load -i bazel-bin/cmd/beacon-chain/oci_image_tarball/tarball.tar
    docker tag qrledger/qrysm:latest "$BEACON_IMAGE"

    bazel build //cmd/validator:oci_image_tarball "${PLATFORM_ARGS[@]}" --config=release
    docker load -i bazel-bin/cmd/validator/oci_image_tarball/tarball.tar
    docker tag qrledger/qrysm:latest "$VALIDATOR_IMAGE"
)

echo "Building genesis generator image from $GENESIS_SHA with Qrysm $QRYSM_SHA."
docker buildx build --load \
    --build-context "qrysm=$QRYSM_REPO" \
    -f "$GENESIS_GENERATOR_REPO/Dockerfile.local" \
    -t "$GENESIS_IMAGE" \
    "$GENESIS_GENERATOR_REPO"

echo "Built $BEACON_IMAGE, $VALIDATOR_IMAGE, and $GENESIS_IMAGE."
