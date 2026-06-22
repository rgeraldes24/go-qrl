#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ENCLAVE="qrl-console-web3"
QRL_PACKAGE_DIR="/private/tmp/qrl-package-pr13"
ARGS_FILE="$ROOT_DIR/testdata/kurtosis_network_params.yaml"
GO_QRL_IMAGE="local/go-qrl:console-web3-live"
GO_QRL_ALLTOOLS_IMAGE="local/go-qrl:console-web3-live-alltools"

usage() {
  cat >&2 <<'USAGE'
Usage:
  testdata/run_kurtosis_network.sh start [--build]
  testdata/run_kurtosis_network.sh endpoint
  testdata/run_kurtosis_network.sh inspect
  testdata/run_kurtosis_network.sh stop
USAGE
}

endpoint() {
  kurtosis enclave inspect "$ENCLAVE" | awk '/rpc: 8545\/tcp ->/ { print "http://" $NF; exit }'
}

start() {
  local build=0

  while (($# > 0)); do
    case "$1" in
      --build) build=1 ;;
      *) usage; exit 2 ;;
    esac
    shift
  done

  if [[ "$build" == 1 ]]; then
    docker build -t "$GO_QRL_IMAGE" -f "$ROOT_DIR/Dockerfile" "$ROOT_DIR" >&2
    docker build -t "$GO_QRL_ALLTOOLS_IMAGE" -f "$ROOT_DIR/Dockerfile.alltools" "$ROOT_DIR" >&2
  fi

  kurtosis enclave rm -f "$ENCLAVE" >/dev/null 2>&1 || true
  kurtosis run --enclave "$ENCLAVE" "$QRL_PACKAGE_DIR" --args-file "$ARGS_FILE" >&2
  endpoint
}

case "${1:-}" in
  -h|--help)
    usage
    ;;
  start)
    shift
    start "$@"
    ;;
  endpoint)
    if (($# != 1)); then
      usage
      exit 2
    fi
    endpoint
    ;;
  inspect)
    if (($# != 1)); then
      usage
      exit 2
    fi
    kurtosis enclave inspect "$ENCLAVE"
    ;;
  stop)
    if (($# != 1)); then
      usage
      exit 2
    fi
    kurtosis enclave rm -f "$ENCLAVE"
    ;;
  *)
    usage
    exit 2
    ;;
esac
