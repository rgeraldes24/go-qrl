#!/usr/bin/env bash

# Requires `docker`, `kurtosis`, `yq`

set -Eeuo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
ENCLAVE_NAME=local-testnet
NETWORK_PARAMS_FILE=$SCRIPT_DIR/network_params.yaml
# Pinned cyyber/qrl-package PR #13 revision with a matching Kurtosis package
# name, hosted on the rgeraldes24 fork for remote execution.
QRL_PKG_VERSION=3892c3d2596403c080424d9e8fc99ff172483fe0

BUILD_IMAGE=true
CI=false
KEEP_ENCLAVE=false

# Get options
while getopts "e:b:n:hck" flag; do
  case "${flag}" in
    e) ENCLAVE_NAME=${OPTARG};;
    b) BUILD_IMAGE=${OPTARG};;
    n) NETWORK_PARAMS_FILE=${OPTARG};;
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

kurtosis run --enclave "$ENCLAVE_NAME" "github.com/rgeraldes24/qrl-package@$QRL_PKG_VERSION" --args-file "$NETWORK_PARAMS_FILE"

echo "Started!"
