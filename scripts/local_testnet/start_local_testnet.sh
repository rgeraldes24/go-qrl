#!/usr/bin/env bash

# Requires `docker`, `kurtosis`, `yq`

set -Eeuo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
ENCLAVE_NAME=local-testnet
NETWORK_PARAMS_FILE=$SCRIPT_DIR/network_params.yaml
# Pinned head of cyyber/qrl-package PR #13, which carries the 64-byte address
# migration and passes the configured genesis-generator image to every setup
# phase. Kurtosis must run this revision from a local checkout because the PR
# commit is not reachable from the canonical package repository.
QRL_PKG_VERSION=261beca5fada67ec5ccad668025e3e07efb3f1e4
QRL_PKG_PATH=${QRL_PKG_PATH:-}

# This repo is the execution client: the whole point of the local testnet is
# to run locally built go-qrl images, so images are built by default.
BUILD_IMAGE=true
CI=false
KEEP_ENCLAVE=false

# Get options
while getopts "e:b:n:p:hck" flag; do
  case "${flag}" in
    e) ENCLAVE_NAME=${OPTARG};;
    b) BUILD_IMAGE=${OPTARG};;
    n) NETWORK_PARAMS_FILE=${OPTARG};;
    p) QRL_PKG_PATH=${OPTARG};;
    c) CI=true;;
    k) KEEP_ENCLAVE=true;;
    h)
        echo "Start a local testnet with kurtosis."
        echo
        echo "usage: $0 <Options>"
        echo
        echo "Options:"
        echo "   -e: enclave name                                default: $ENCLAVE_NAME"
        echo "   -b: whether to build go-qrl docker images       default: $BUILD_IMAGE"
        echo "   -n: kurtosis network params file path           default: $NETWORK_PARAMS_FILE"
        echo "   -p: qrl-package PR 13 checkout path             default: QRL_PKG_PATH"
        echo "   -c: CI mode, run without other additional services like Grafana and explorer"
        echo "   -k: keeping enclave to allow starting the testnet without destroying the existing one"
        echo "   -h: this help"
        exit
        ;;
    *)
        echo "Unknown option. Run $0 -h for help." >&2
        exit 1
        ;;
  esac
done

if ! command -v docker &> /dev/null; then
    echo "Docker is not installed. Please install Docker and try again."
    exit 1
fi

if ! command -v kurtosis &> /dev/null; then
    echo "kurtosis command not found. Please install kurtosis and try again."
    exit 1
fi

if ! command -v yq &> /dev/null; then
    echo "yq not found. Please install yq and try again."
    exit 1
fi

if [ -z "$QRL_PKG_PATH" ]; then
    echo "A local checkout of cyyber/qrl-package PR #13 is required." >&2
    echo "Pass it with -p or set QRL_PKG_PATH." >&2
    exit 1
fi

if ! QRL_PKG_HEAD=$(git -C "$QRL_PKG_PATH" rev-parse HEAD 2>/dev/null); then
    echo "Not a qrl-package git checkout: $QRL_PKG_PATH" >&2
    exit 1
fi

if [ "$QRL_PKG_HEAD" != "$QRL_PKG_VERSION" ]; then
    echo "qrl-package checkout is at $QRL_PKG_HEAD; expected PR #13 commit $QRL_PKG_VERSION." >&2
    exit 1
fi

QRL_PKG_PATH=$(cd "$QRL_PKG_PATH" && pwd)

for image in \
    local/qrysm-beacon:vm64 \
    local/qrysm-validator:vm64 \
    local/qrl-genesis-generator:vm64; do
    if ! docker image inspect "$image" &> /dev/null; then
        echo "Required local image is missing: $image" >&2
        echo "Build the consensus images with scripts/local_testnet/build_consensus_images.sh." >&2
        exit 1
    fi
done

if [ "$CI" = true ]; then
  yq eval '.additional_services = []' -i "$NETWORK_PARAMS_FILE"
  echo "Running without additional services (CI mode)."
fi

if [ "$BUILD_IMAGE" = true ]; then
    echo "Building go-qrl Docker images."
    ROOT_DIR="$SCRIPT_DIR/../.."
    # gqrl (execution client)
    docker build -f "$ROOT_DIR/Dockerfile" -t theqrl-dev/go-qrl:latest "$ROOT_DIR"
    # alltools (clef et al., used when testing the clef remote signer)
    docker build -f "$ROOT_DIR/Dockerfile.alltools" -t theqrl-dev/go-qrl-alltools:latest "$ROOT_DIR"
else
    echo "Not rebuilding go-qrl Docker images."
fi

if [ "$KEEP_ENCLAVE" = false ]; then
  # Stop local testnet
  kurtosis enclave rm -f "$ENCLAVE_NAME" 2>/dev/null || true
fi

kurtosis run --enclave "$ENCLAVE_NAME" "$QRL_PKG_PATH" --args-file "$NETWORK_PARAMS_FILE"

echo "Started!"
