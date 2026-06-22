#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
NETWORK_SCRIPT="$ROOT_DIR/testdata/run_kurtosis_network.sh"

usage() {
  cat >&2 <<'USAGE'
Usage:
  console/testdata/run_web3_console_exhaustive.sh <endpoint>
  console/testdata/run_web3_console_exhaustive.sh --kurtosis [--build] [--rm]
  console/testdata/run_web3_console_exhaustive.sh --rm
USAGE
}

remove_network() {
  "$NETWORK_SCRIPT" stop
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
    remove_network
    exit 0
    ;;
  --kurtosis)
    shift
    network_args=()
    while (($# > 0)); do
      case "$1" in
        --build) network_args+=(--build) ;;
        --rm) trap remove_network EXIT ;;
        *) usage; exit 2 ;;
      esac
      shift
    done
    endpoint="$("$NETWORK_SCRIPT" start "${network_args[@]}")"
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
