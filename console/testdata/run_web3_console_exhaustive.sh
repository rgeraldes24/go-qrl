#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENCLAVE="qrl-console-web3"
QRL_PACKAGE_DIR="/private/tmp/qrl-package-pr13"
ARGS_FILE="$ROOT_DIR/console/testdata/network_params.yaml"
GO_QRL_IMAGE="local/go-qrl:console-web3-live"
GO_QRL_ALLTOOLS_IMAGE="local/go-qrl:console-web3-live-alltools"

usage() {
  cat >&2 <<'USAGE'
Usage:
  console/testdata/run_web3_console_exhaustive.sh <endpoint>
  console/testdata/run_web3_console_exhaustive.sh --kurtosis [--build] [--rm]
  console/testdata/run_web3_console_exhaustive.sh --rm
USAGE
}

remove_enclave() {
  kurtosis enclave rm -f "$ENCLAVE"
}

run_kurtosis() {
  if [[ "${BUILD_IMAGES:-0}" == 1 ]]; then
    docker build -t "$GO_QRL_IMAGE" -f "$ROOT_DIR/Dockerfile" "$ROOT_DIR" >&2
    docker build -t "$GO_QRL_ALLTOOLS_IMAGE" -f "$ROOT_DIR/Dockerfile.alltools" "$ROOT_DIR" >&2
  fi
  kurtosis enclave rm -f "$ENCLAVE" >/dev/null 2>&1 || true
  kurtosis run --enclave "$ENCLAVE" "$QRL_PACKAGE_DIR" --args-file "$ARGS_FILE" >&2
  kurtosis enclave inspect "$ENCLAVE" | awk '/rpc: 8545\/tcp ->/ { print "http://" $NF; exit }'
}

run_console_test() {
  local endpoint="$1"
  local debug_side_effects="$2"
  local output

  cd "$ROOT_DIR"
  output="$(
    go run ./cmd/gqrl attach \
      --jspath "$ROOT_DIR" \
      --exec "var WEB3_EXHAUSTIVE_TEST_RPC_ENDPOINT_MUTATION = false; var WEB3_EXHAUSTIVE_TEST_DEBUG_SIDE_EFFECTS = $debug_side_effects; var WEB3_EXHAUSTIVE_TEST_DESTRUCTIVE_DEBUG = false; loadScript(\"console/testdata/web3_console_exhaustive.js\")" \
      "$endpoint" 2>&1
  )" || {
    printf '%s\n' "$output"
    return 1
  }

  printf '%s\n' "$output"
  if grep -Eq '^(FAIL |GoError:)' <<<"$output"; then
    return 1
  fi
  grep -q 'COVERAGE_SUMMARY' <<<"$output"
}

case "${1:-}" in
  -h|--help)
    usage
    exit 0
    ;;
  --rm)
    if (($# != 1)); then
      usage
      exit 2
    fi
    remove_enclave
    exit 0
    ;;
  --kurtosis)
    shift
    BUILD_IMAGES=0
    while (($# > 0)); do
      case "$1" in
        --build) BUILD_IMAGES=1 ;;
        --rm) trap remove_enclave EXIT ;;
        *) usage; exit 2 ;;
      esac
      shift
    done
    endpoint="$(run_kurtosis)"
    debug_side_effects=true
    ;;
  "")
    usage
    exit 2
    ;;
  *)
    if (($# != 1)); then
      usage
      exit 2
    fi
    endpoint="$1"
    debug_side_effects=false
    ;;
esac

echo "Running exhaustive web3.js console test against $endpoint"
run_console_test "$endpoint" "$debug_side_effects"
