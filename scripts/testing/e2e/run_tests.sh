#!/usr/bin/env bash

# Runs the JavaScript E2E suites in ./testdata/console against a running local testnet
# (see scripts/local_testnet/start_local_testnet.sh) through the gqrl console.

set -Eeuo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
ROOT_DIR="$SCRIPT_DIR/../../.."
ENCLAVE_NAME=local-testnet
EL_SERVICE=el-1-gqrl-qrysm
RPC_URL=""
GRAPHQL_URL=""
GRAPHQL_URL_EXPLICIT=false
WS_URL=""
WS_URL_EXPLICIT=false
WAIT_TIMEOUT=600
STRICT=false
RUN_STANDALONE_CLEF=true
RESULTS_DIR=""
EXPECTED_GIT_COMMIT=${EXPECTED_GIT_COMMIT:-}

# Prefunded dev account (see the prefunded_accounts comment in
# scripts/local_testnet/network_params.yaml); used to sign the deployment transaction for
# testdata/console/event_roundtrip.js.
DEPLOYER_SEED=010000f29f58aff0b00de2844f7e20bd9eeaacc379150043beeb328335817512b29fbb7184da84a092f842b2a06d72a24a5d28

SUITES=(web3_sanity api_surfaces logs_topics event_roundtrip abi_vm64)

# Get options
while getopts "e:s:r:g:w:t:o:cCh" flag; do
  case "${flag}" in
    e) ENCLAVE_NAME=${OPTARG};;
    s) EL_SERVICE=${OPTARG};;
    r) RPC_URL=${OPTARG};;
    g) GRAPHQL_URL=${OPTARG}; GRAPHQL_URL_EXPLICIT=true;;
    w) WS_URL=${OPTARG}; WS_URL_EXPLICIT=true;;
    t) WAIT_TIMEOUT=${OPTARG};;
    o) RESULTS_DIR=${OPTARG};;
    c) STRICT=true;;
    C) RUN_STANDALONE_CLEF=false;;
    h)
        echo "Run the local testnet E2E test suites."
        echo
        echo "usage: $0 <Options>"
        echo
        echo "Options:"
        echo "   -e: enclave name                                default: $ENCLAVE_NAME"
        echo "   -s: execution layer kurtosis service name       default: $EL_SERVICE"
        echo "   -r: HTTP RPC endpoint; skips kurtosis service resolution when set"
        echo "   -g: GraphQL endpoint; defaults to <HTTP RPC endpoint>/graphql"
        echo "   -w: WS RPC endpoint; resolved from Kurtosis when possible"
        echo "   -t: seconds to wait for block production        default: $WAIT_TIMEOUT"
        echo "   -o: directory for per-suite logs and summary"
        echo "   -c: strict CI mode; rebuild clients and require GraphQL and WS"
        echo "   -C: skip the standalone Clef crypto suite (for a secondary-node run)"
        echo "   -h: this help"
        exit
        ;;
    *)
        echo "Unknown option. Run $0 -h for help." >&2
        exit 1
        ;;
  esac
done

if [ "$STRICT" = true ] && [ -z "$EXPECTED_GIT_COMMIT" ]; then
    EXPECTED_GIT_COMMIT=$(git -C "$ROOT_DIR" rev-parse HEAD)
    echo "Strict mode derived expected revision $EXPECTED_GIT_COMMIT from the checkout."
fi
if [ -n "$EXPECTED_GIT_COMMIT" ] && [[ ! "$EXPECTED_GIT_COMMIT" =~ ^[0-9a-f]{40}$ ]]; then
    echo "EXPECTED_GIT_COMMIT must be an exact lowercase 40-character commit, got: $EXPECTED_GIT_COMMIT" >&2
    exit 1
fi

GQRL="$ROOT_DIR/build/bin/gqrl"
CLEF="$ROOT_DIR/build/bin/clef"
if [ "$STRICT" = true ]; then
    BUILD_TARGETS=(./cmd/gqrl)
    if [ "$RUN_STANDALONE_CLEF" = true ]; then
        BUILD_TARGETS+=(./cmd/clef)
        echo "Building gqrl and clef from the checked-out source."
    else
        echo "Building gqrl from the checked-out source; standalone Clef verification is disabled."
    fi
    (cd "$ROOT_DIR" && go run build/ci.go install "${BUILD_TARGETS[@]}")
elif [ ! -x "$GQRL" ]; then
    echo "Building gqrl."
    (cd "$ROOT_DIR" && go run build/ci.go install ./cmd/gqrl)
fi

if [ -n "$EXPECTED_GIT_COMMIT" ]; then
    GQRL_VERSION=$("$GQRL" version)
    if ! grep -Fq "Git Commit: $EXPECTED_GIT_COMMIT" <<<"$GQRL_VERSION"; then
        echo "gqrl was not built from expected commit $EXPECTED_GIT_COMMIT:" >&2
        echo "$GQRL_VERSION" >&2
        exit 1
    fi
fi

if [ -z "$RESULTS_DIR" ]; then
    RESULTS_DIR="$SCRIPT_DIR/logs/test-results"
fi
mkdir -p "$RESULTS_DIR"
SUMMARY_FILE="$RESULTS_DIR/summary.tsv"
printf 'suite\texit_code\tmarker\n' > "$SUMMARY_FILE"
PARAMS_FILE="$SCRIPT_DIR/testdata/console/.params.js"
cleanup() {
    rm -f "$PARAMS_FILE"
}
trap cleanup EXIT

FAILED_SUITES=()
run_suite() {
    local suite=$1
    shift
    local output_file="$RESULTS_DIR/$suite.log"
    local status
    local marker=missing

    echo
    echo "=== $suite ==="
    if "$@" >"$output_file" 2>&1; then
        status=0
    else
        status=$?
    fi
    cat "$output_file"
    if grep -q "^SUITE $suite: PASSED" "$output_file"; then
        marker=passed
    fi
    printf '%s\t%s\t%s\n' "$suite" "$status" "$marker" >> "$SUMMARY_FILE"
    if [ "$status" -ne 0 ] || [ "$marker" != passed ]; then
        FAILED_SUITES+=("$suite")
    fi
}

if [ -z "$RPC_URL" ]; then
    if ! command -v kurtosis &> /dev/null; then
        echo "kurtosis command not found. Please install kurtosis and try again."
        exit 1
    fi
    RPC_URL=$(kurtosis port print "$ENCLAVE_NAME" "$EL_SERVICE" rpc)
    if [ -z "$WS_URL" ]; then
        WS_URL=$(kurtosis port print "$ENCLAVE_NAME" "$EL_SERVICE" ws 2>/dev/null || true)
    fi
fi
case "$RPC_URL" in
    http://*|https://*) ;;
    *) RPC_URL="http://$RPC_URL";;
esac
if [ -z "$GRAPHQL_URL" ]; then
    GRAPHQL_URL="${RPC_URL%/}/graphql"
else
    case "$GRAPHQL_URL" in
        http://*|https://*) ;;
        *) GRAPHQL_URL="http://$GRAPHQL_URL";;
    esac
fi
if [ -n "$WS_URL" ]; then
    case "$WS_URL" in
        ws://*|wss://*) ;;
        *) WS_URL="ws://$WS_URL";;
    esac
fi

echo "Using RPC endpoint $RPC_URL."
if [ -n "$WS_URL" ]; then
    echo "Using WS endpoint $WS_URL."
elif [ "$WS_URL_EXPLICIT" = true ] || [ "$STRICT" = true ]; then
    echo "WS endpoint is required but unavailable."
    exit 1
else
    echo "WS endpoint unavailable; skipping subscription checks."
fi

echo "Waiting for the chain to produce blocks (timeout ${WAIT_TIMEOUT}s)."
START_TIME=$(date +%s)
while true; do
    BLOCK_RESPONSE=$(curl --fail --silent --show-error \
        --connect-timeout 5 --max-time 15 \
        -H "Content-Type: application/json" \
        --data '{"jsonrpc":"2.0","method":"qrl_blockNumber","params":[],"id":1}' \
        "$RPC_URL" 2>/dev/null || true)
    BLOCK_HEX=$(sed -n 's/.*"result":"\(0x[0-9a-fA-F]*\)".*/\1/p' <<<"$BLOCK_RESPONSE")
    BLOCK_NUMBER=""
    if [[ "$BLOCK_HEX" =~ ^0x[0-9a-fA-F]+$ ]]; then
        BLOCK_NUMBER=$((BLOCK_HEX))
    fi
    if [ -n "$BLOCK_NUMBER" ] && [ "$BLOCK_NUMBER" -gt 0 ]; then
        echo "Chain is at block $BLOCK_NUMBER."
        break
    fi
    if [ $(( $(date +%s) - START_TIME )) -ge "$WAIT_TIMEOUT" ]; then
        echo "Timed out waiting for block production (last reading: '${BLOCK_NUMBER:-unreachable}')."
        exit 1
    fi
    sleep 5
done

GRAPHQL_PROBE=""
for _ in $(seq 1 10); do
    GRAPHQL_PROBE=$(curl --fail --silent --show-error \
        --connect-timeout 5 --max-time 15 \
        -H "Content-Type: application/json" \
        --data '{"query":"{chainID}","variables":null}' "$GRAPHQL_URL" 2>/dev/null || true)
    if grep -q '"chainID"' <<<"$GRAPHQL_PROBE"; then
        break
    fi
    sleep 2
done
if ! grep -q '"chainID"' <<<"$GRAPHQL_PROBE"; then
    if [ "$GRAPHQL_URL_EXPLICIT" = true ] || [ "$STRICT" = true ]; then
        echo "GraphQL endpoint $GRAPHQL_URL is required but unavailable."
        exit 1
    fi
    echo "GraphQL endpoint $GRAPHQL_URL is unavailable; skipping GraphQL checks."
    GRAPHQL_URL=""
else
    echo "Using GraphQL endpoint $GRAPHQL_URL."
fi

if [ -n "$EXPECTED_GIT_COMMIT" ]; then
    CLIENT_VERSION=$("$GQRL" attach --exec "web3.version.node" "$RPC_URL")
    EXPECTED_SHORT_COMMIT=${EXPECTED_GIT_COMMIT:0:8}
    if [[ "$CLIENT_VERSION" != *"$EXPECTED_SHORT_COMMIT"* ]]; then
        echo "RPC node does not report expected commit $EXPECTED_SHORT_COMMIT: $CLIENT_VERSION" >&2
        exit 1
    fi
    echo "RPC node reports expected commit $EXPECTED_SHORT_COMMIT."
fi

# Sign the contract deployment for event_roundtrip.js from the prefunded dev
# account. The console has no account management, so the suite feeds this
# pre-signed transaction to qrl.sendRawTransaction.
EMITTER_BIN=$(sed -n 's/^ *bin: "\(0x[0-9a-fA-F]*\)".*/\1/p' "$SCRIPT_DIR/testdata/contracts/emitter.js")
if [ -z "$EMITTER_BIN" ]; then
    echo "Could not extract deployment bytecode from testdata/contracts/emitter.js."
    exit 1
fi
echo "Signing the event_roundtrip deployment transaction."
go -C "$SCRIPT_DIR" run ./txsigner \
    -rpc "$RPC_URL" -seed "$DEPLOYER_SEED" -data "$EMITTER_BIN" -format js \
    > "$PARAMS_FILE"

for SUITE in "${SUITES[@]}"; do
    run_suite "$SUITE" "$GQRL" attach --jspath "$SCRIPT_DIR/testdata" \
        --exec "loadScript('console/$SUITE.js')" "$RPC_URL"
done

GOABI_ARGS=(-rpc "$RPC_URL" -seed "$DEPLOYER_SEED" -bin "$EMITTER_BIN")
if [ -n "$GRAPHQL_URL" ]; then
    GOABI_ARGS+=(-graphql "$GRAPHQL_URL")
fi
if [ -n "$WS_URL" ]; then
    GOABI_ARGS+=(-ws "$WS_URL")
fi
run_suite go_abi go -C "$SCRIPT_DIR" run ./cmd/goabi "${GOABI_ARGS[@]}"

run_clef_api() (
    set -Eeuo pipefail
    if [ ! -x "$CLEF" ]; then
        echo "Building clef."
        (cd "$ROOT_DIR" && go run build/ci.go install ./cmd/clef)
    fi
    CLEF_PORT=${CLEF_PORT:-18550}
    DEPLOYER_SEED="$DEPLOYER_SEED" go -C "$SCRIPT_DIR" run ./clefverify run \
        -clef "$CLEF" \
        -seed-env DEPLOYER_SEED \
        -artifacts "$RESULTS_DIR/clef_api" \
        -port "$CLEF_PORT"
)
CLEF_SUITE_COUNT=0
if [ "$RUN_STANDALONE_CLEF" = true ]; then
    run_suite clef_api run_clef_api
    CLEF_SUITE_COUNT=1
else
    echo
    echo "Skipping the standalone Clef crypto suite by request; endpoint-dependent node suites remain enabled."
fi

echo
if [ ${#FAILED_SUITES[@]} -ne 0 ]; then
    echo "Failed suites: ${FAILED_SUITES[*]}."
    echo "Per-suite results: $SUMMARY_FILE"
    exit 1
fi
TOTAL_SUITES=$(( ${#SUITES[@]} + 1 + CLEF_SUITE_COUNT ))
echo "All $TOTAL_SUITES suites passed."
echo "Per-suite results: $SUMMARY_FILE"
