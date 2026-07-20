#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
ENCLAVE_NAME=${1:-local-testnet}
LOGS_SUBDIR=${2:-$SCRIPT_DIR/logs/$ENCLAVE_NAME}
LOGS_PARENT=$(dirname "$LOGS_SUBDIR")
INSPECT_FILE="$LOGS_PARENT/$ENCLAVE_NAME-inspect.txt"

mkdir -p "$LOGS_PARENT"
INSPECT_STATUS=0
if ! kurtosis enclave inspect "$ENCLAVE_NAME" >"$INSPECT_FILE" 2>&1; then
    echo "Could not inspect enclave $ENCLAVE_NAME; preserving the diagnostic and attempting dump/removal." >&2
    INSPECT_STATUS=1
fi

DUMP_STATUS=0
if [ -e "$LOGS_SUBDIR" ]; then
    LOGS_SUBDIR="$LOGS_SUBDIR-$(date +%Y%m%d%H%M%S)"
fi
if kurtosis enclave dump "$ENCLAVE_NAME" "$LOGS_SUBDIR"; then
    echo "Local testnet logs stored in $LOGS_SUBDIR."
else
    echo "Failed to dump enclave $ENCLAVE_NAME; preserving it for recovery and inspection." >&2
    DUMP_STATUS=1
fi

REMOVE_STATUS=0
if [ "$DUMP_STATUS" -eq 0 ]; then
    if kurtosis enclave rm -f "$ENCLAVE_NAME"; then
        echo "Local testnet stopped; the shared Kurtosis engine was left running."
    else
        echo "Failed to remove enclave $ENCLAVE_NAME." >&2
        REMOVE_STATUS=1
    fi
else
    echo "Local testnet remains available as enclave $ENCLAVE_NAME." >&2
fi

if [ "$INSPECT_STATUS" -ne 0 ] || [ "$DUMP_STATUS" -ne 0 ] || [ "$REMOVE_STATUS" -ne 0 ]; then
    exit 1
fi
