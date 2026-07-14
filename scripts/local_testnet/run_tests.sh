#!/usr/bin/env bash

# Runs the JavaScript E2E suites in ./tests against a running local testnet
# (see start_local_testnet.sh) through the gqrl console.

set -Eeuo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
ROOT_DIR="$SCRIPT_DIR/../.."
ENCLAVE_NAME=local-testnet
EL_SERVICE=el-1-gqrl-qrysm
RPC_URL=""
GRAPHQL_URL=""
GRAPHQL_URL_EXPLICIT=false
WS_URL=""
WS_URL_EXPLICIT=false
WAIT_TIMEOUT=600

# Prefunded dev account (see the prefunded_accounts comment in
# network_params.yaml); used to sign the deployment transaction for
# tests/event_roundtrip.js.
DEPLOYER_SEED=010000f29f58aff0b00de2844f7e20bd9eeaacc379150043beeb328335817512b29fbb7184da84a092f842b2a06d72a24a5d28

SUITES=(web3_sanity api_surfaces logs_topics event_roundtrip abi_vm64)

# Get options
while getopts "e:s:r:g:w:t:h" flag; do
  case "${flag}" in
    e) ENCLAVE_NAME=${OPTARG};;
    s) EL_SERVICE=${OPTARG};;
    r) RPC_URL=${OPTARG};;
    g) GRAPHQL_URL=${OPTARG}; GRAPHQL_URL_EXPLICIT=true;;
    w) WS_URL=${OPTARG}; WS_URL_EXPLICIT=true;;
    t) WAIT_TIMEOUT=${OPTARG};;
    h)
        echo "Run the local testnet E2E test suites."
        echo
        echo "usage: $0 <Options>"
        echo
        echo "Options:"
        echo "   -e: enclave name                                default: $ENCLAVE_NAME"
        echo "   -s: execution layer kurtosis service name       default: $EL_SERVICE"
        echo "   -r: HTTP RPC endpoint; skips kurtosis service resolution when set"
        echo "   -g: GraphQL endpoint; defaults to <HTTP RPC endpoint>/graphql when available"
        echo "   -w: WS RPC endpoint; skips subscription checks when omitted/unavailable"
        echo "   -t: seconds to wait for block production        default: $WAIT_TIMEOUT"
        echo "   -h: this help"
        exit
        ;;
    *)
        echo "Unknown option. Run $0 -h for help." >&2
        exit 1
        ;;
  esac
done

GQRL="$ROOT_DIR/build/bin/gqrl"
if [ ! -x "$GQRL" ]; then
    echo "Building gqrl."
    (cd "$ROOT_DIR" && go run build/ci.go install ./cmd/gqrl)
fi
CLEF="$ROOT_DIR/build/bin/clef"

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
elif [ "$WS_URL_EXPLICIT" = true ]; then
    echo "WS endpoint was explicitly set but empty."
    exit 1
else
    echo "WS endpoint unavailable; skipping subscription checks."
fi

echo "Waiting for the chain to produce blocks (timeout ${WAIT_TIMEOUT}s)."
START_TIME=$(date +%s)
while true; do
    BLOCK_NUMBER=$("$GQRL" attach --exec "qrl.blockNumber" "$RPC_URL" 2>/dev/null || echo "")
    if [ -n "$BLOCK_NUMBER" ] && [ "$BLOCK_NUMBER" -gt 0 ] 2>/dev/null; then
        echo "Chain is at block $BLOCK_NUMBER."
        break
    fi
    if [ $(( $(date +%s) - START_TIME )) -ge "$WAIT_TIMEOUT" ]; then
        echo "Timed out waiting for block production (last reading: '${BLOCK_NUMBER:-unreachable}')."
        exit 1
    fi
    sleep 5
done

if [ "$GRAPHQL_URL_EXPLICIT" = false ]; then
    GRAPHQL_PROBE=$(curl -sS -H "Content-Type: application/json" \
        --data '{"query":"{chainID}","variables":null}' "$GRAPHQL_URL" 2>/dev/null || true)
    if ! echo "$GRAPHQL_PROBE" | grep -q '"chainID"'; then
        echo "GraphQL endpoint $GRAPHQL_URL is unavailable; skipping GraphQL checks."
        GRAPHQL_URL=""
    fi
fi
if [ -n "$GRAPHQL_URL" ]; then
    echo "Using GraphQL endpoint $GRAPHQL_URL."
fi

# Sign the contract deployment for event_roundtrip.js from the prefunded dev
# account. The console has no account management, so the suite feeds this
# pre-signed transaction to qrl.sendRawTransaction.
EMITTER_BIN=$(sed -n 's/^ *bin: "\(0x[0-9a-fA-F]*\)".*/\1/p' "$SCRIPT_DIR/tests/fixtures/emitter.js")
if [ -z "$EMITTER_BIN" ]; then
    echo "Could not extract deployment bytecode from tests/fixtures/emitter.js."
    exit 1
fi
echo "Signing the event_roundtrip deployment transaction."
(cd "$ROOT_DIR" && go run ./scripts/local_testnet/txsigner \
    -rpc "$RPC_URL" -seed "$DEPLOYER_SEED" -data "$EMITTER_BIN" -format js) \
    > "$SCRIPT_DIR/tests/.params.js"

FAILED_SUITES=()
for SUITE in "${SUITES[@]}"; do
    echo
    echo "=== $SUITE ==="
    OUTPUT=$("$GQRL" attach --jspath "$SCRIPT_DIR/tests" --exec "loadScript('$SUITE.js')" "$RPC_URL" 2>&1) || true
    echo "$OUTPUT"
    if ! echo "$OUTPUT" | grep -q "^SUITE $SUITE: PASSED"; then
        FAILED_SUITES+=("$SUITE")
    fi
done

echo
echo "=== go_abi ==="
GOABI_ARGS=(-rpc "$RPC_URL" -seed "$DEPLOYER_SEED" -bin "$EMITTER_BIN")
if [ -n "$GRAPHQL_URL" ]; then
    GOABI_ARGS+=(-graphql "$GRAPHQL_URL")
fi
if [ -n "$WS_URL" ]; then
    GOABI_ARGS+=(-ws "$WS_URL")
fi
OUTPUT=$(cd "$ROOT_DIR" && go run ./scripts/local_testnet/goabi "${GOABI_ARGS[@]}" 2>&1) || true
echo "$OUTPUT"
if ! echo "$OUTPUT" | grep -q "^SUITE go_abi: PASSED"; then
    FAILED_SUITES+=("go_abi")
fi

echo
echo "=== clef_api ==="
OUTPUT=$(
    set -Eeuo pipefail
    if [ ! -x "$CLEF" ]; then
        echo "Building clef."
        (cd "$ROOT_DIR" && go run build/ci.go install ./cmd/clef)
    fi
    CLEF_DIR=$(mktemp -d)
    CLEF_PORT=18550
    CLEF_LOG="$CLEF_DIR/clef.log"
    CLEF_MASTER_PASSWORD=localtestnetmaster
    CLEF_ACCOUNT_PASSWORD=localtestnetaccount
    cleanup() {
        if [ -n "${CLEF_PID:-}" ]; then
            kill "$CLEF_PID" >/dev/null 2>&1 || true
            wait "$CLEF_PID" >/dev/null 2>&1 || true
        fi
        rm -rf "$CLEF_DIR"
    }
    trap cleanup EXIT

    printf '%s\n%s\n' "$CLEF_MASTER_PASSWORD" "$CLEF_MASTER_PASSWORD" |
        "$CLEF" --suppress-bootwarn --lightkdf --configdir "$CLEF_DIR/config" \
            --keystore "$CLEF_DIR/keystore" init >/dev/null
    printf '%s\n' "$DEPLOYER_SEED" > "$CLEF_DIR/seed.hex"
    printf '%s\n' "$CLEF_ACCOUNT_PASSWORD" > "$CLEF_DIR/account-password.txt"
    "$CLEF" --suppress-bootwarn --lightkdf --configdir "$CLEF_DIR/config" \
        --keystore "$CLEF_DIR/keystore" importraw --password "$CLEF_DIR/account-password.txt" \
        "$CLEF_DIR/seed.hex" > "$CLEF_DIR/import.out"
    CLEF_ACCOUNT=$(sed -n 's/^  Address //p' "$CLEF_DIR/import.out")
    if [ -z "$CLEF_ACCOUNT" ]; then
        echo "could not parse imported clef account"
        cat "$CLEF_DIR/import.out"
        exit 1
    fi
    cat > "$CLEF_DIR/rules.js" <<'EOF'
function ApproveListing(req) { return 'Approve'; }
function ApproveSignData(req) { return 'Approve'; }
function ApproveTx(req) { return 'Approve'; }
EOF
    if command -v sha256sum >/dev/null 2>&1; then
        RULE_HASH=$(sha256sum "$CLEF_DIR/rules.js" | awk '{print $1}')
    else
        RULE_HASH=$(shasum -a 256 "$CLEF_DIR/rules.js" | awk '{print $1}')
    fi
    printf '%s\n' "$CLEF_MASTER_PASSWORD" |
        "$CLEF" --suppress-bootwarn --lightkdf --configdir "$CLEF_DIR/config" \
            --keystore "$CLEF_DIR/keystore" attest "$RULE_HASH" >/dev/null
    # setpw prompts for account password, confirmation, then master password.
    printf '%s\n%s\n%s\n' "$CLEF_ACCOUNT_PASSWORD" "$CLEF_ACCOUNT_PASSWORD" "$CLEF_MASTER_PASSWORD" |
        "$CLEF" --suppress-bootwarn --lightkdf --configdir "$CLEF_DIR/config" \
            --keystore "$CLEF_DIR/keystore" setpw "$CLEF_ACCOUNT" >/dev/null

    printf '%s\n' "$CLEF_MASTER_PASSWORD" |
        "$CLEF" --suppress-bootwarn --lightkdf --advanced --configdir "$CLEF_DIR/config" \
        --keystore "$CLEF_DIR/keystore" --chainid 1337 --rules "$CLEF_DIR/rules.js" \
        --http --http.addr 127.0.0.1 --http.port "$CLEF_PORT" --http.vhosts "*" \
        --ipcdisable --auditlog "" >"$CLEF_LOG" 2>&1 &
    CLEF_PID=$!

    RESPONSE=""
    for _ in $(seq 1 30); do
        RESPONSE=$(curl -sS -H "Content-Type: application/json" \
            --data '{"jsonrpc":"2.0","method":"account_version","params":[],"id":1}' \
            "http://127.0.0.1:$CLEF_PORT/" 2>/dev/null || true)
        if echo "$RESPONSE" | grep -q '"result":"[^"]\+"'; then
            break
        fi
        if ! kill -0 "$CLEF_PID" >/dev/null 2>&1; then
            echo "clef exited before account_version responded"
            cat "$CLEF_LOG"
            exit 1
        fi
        sleep 1
    done
    if ! echo "$RESPONSE" | grep -q '"result":"[^"]\+"'; then
        echo "account_version did not respond successfully; last response: ${RESPONSE:-<empty>}"
        cat "$CLEF_LOG"
        exit 1
    fi

    LIST_RESPONSE=$(curl -sS -H "Content-Type: application/json" \
        --data '{"jsonrpc":"2.0","method":"account_list","params":[],"id":2}' \
        "http://127.0.0.1:$CLEF_PORT/")
    if ! echo "$LIST_RESPONSE" | grep -q "$CLEF_ACCOUNT"; then
        echo "account_list did not include imported account: $LIST_RESPONSE"
        exit 1
    fi
    SIGN_DATA_RESPONSE=$(curl -sS -H "Content-Type: application/json" \
        --data "{\"jsonrpc\":\"2.0\",\"method\":\"account_signData\",\"params\":[\"text/plain\",\"$CLEF_ACCOUNT\",\"0x68656c6c6f\"],\"id\":3}" \
        "http://127.0.0.1:$CLEF_PORT/")
    if ! echo "$SIGN_DATA_RESPONSE" | grep -Eq '"result":"0x[0-9a-fA-F]+"'; then
        echo "account_signData did not return a signature: $SIGN_DATA_RESPONSE"
        exit 1
    fi
    TYPED_DATA_PAYLOAD=$(cat <<EOF
{
  "jsonrpc": "2.0",
  "method": "account_signTypedData",
  "params": [
    "$CLEF_ACCOUNT",
    {
      "types": {
        "EIP712Domain": [
          {"name": "name", "type": "string"},
          {"name": "version", "type": "string"},
          {"name": "chainId", "type": "uint256"},
          {"name": "verifyingContract", "type": "address"}
        ],
        "Message": [
          {"name": "sender", "type": "address"},
          {"name": "contents", "type": "string"},
          {"name": "value", "type": "uint256"}
        ]
      },
      "primaryType": "Message",
      "domain": {
        "name": "Local Testnet",
        "version": "1",
        "chainId": "1337",
        "verifyingContract": "$CLEF_ACCOUNT"
      },
      "message": {
        "sender": "$CLEF_ACCOUNT",
        "contents": "hello",
        "value": "1"
      }
    }
  ],
  "id": 4
}
EOF
)
    SIGN_TYPED_RESPONSE=$(curl -sS -H "Content-Type: application/json" \
        --data "$TYPED_DATA_PAYLOAD" "http://127.0.0.1:$CLEF_PORT/")
    if ! echo "$SIGN_TYPED_RESPONSE" | grep -Eq '"result":"0x[0-9a-fA-F]+"'; then
        echo "account_signTypedData did not return a signature: $SIGN_TYPED_RESPONSE"
        exit 1
    fi
    SIGN_TX_RESPONSE=$(curl -sS -H "Content-Type: application/json" \
        --data "{\"jsonrpc\":\"2.0\",\"method\":\"account_signTransaction\",\"params\":[{\"from\":\"$CLEF_ACCOUNT\",\"to\":\"$CLEF_ACCOUNT\",\"gas\":\"0x5208\",\"maxFeePerGas\":\"0x3b9aca00\",\"maxPriorityFeePerGas\":\"0x0\",\"value\":\"0x0\",\"nonce\":\"0x0\",\"chainId\":\"0x539\"}],\"id\":5}" \
        "http://127.0.0.1:$CLEF_PORT/")
    if ! echo "$SIGN_TX_RESPONSE" | grep -Eq '"raw":"0x[0-9a-fA-F]+"'; then
        echo "account_signTransaction did not return a signed tx: $SIGN_TX_RESPONSE"
        exit 1
    fi

    echo "PASS: account_version returned $RESPONSE"
    echo "PASS: account_list returned imported account $CLEF_ACCOUNT"
    echo "PASS: account_signData returned a signature"
    echo "PASS: account_signTypedData returned a signature"
    echo "PASS: account_signTransaction returned a signed transaction"
    echo "SUITE clef_api: PASSED"
) || true
echo "$OUTPUT"
if ! echo "$OUTPUT" | grep -q "^SUITE clef_api: PASSED"; then
    FAILED_SUITES+=("clef_api")
fi

echo
if [ ${#FAILED_SUITES[@]} -ne 0 ]; then
    echo "Failed suites: ${FAILED_SUITES[*]}."
    exit 1
fi
TOTAL_SUITES=$(( ${#SUITES[@]} + 2 ))
echo "All $TOTAL_SUITES suites passed."
