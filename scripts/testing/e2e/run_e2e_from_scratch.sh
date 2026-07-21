#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../../.." && pwd)
SCRIPT_PATH="$SCRIPT_DIR/$(basename -- "${BASH_SOURCE[0]}")"
NETWORK_DIR="$REPO_ROOT/scripts/local_testnet"

ENCLAVE_NAME=vm64-e2e
DUMP_DIR=

usage() {
	echo "Usage: $0 [-e enclave] [-d dump-directory]"
}

while getopts "e:d:h" option; do
	case "$option" in
	e) ENCLAVE_NAME=$OPTARG ;;
	d) DUMP_DIR=$OPTARG ;;
	h)
		usage
		exit 0
		;;
	*)
		usage >&2
		exit 2
		;;
	esac
done
shift $((OPTIND - 1))
if [ "$#" -ne 0 ]; then
	usage >&2
	exit 2
fi
if [ -z "$ENCLAVE_NAME" ]; then
	echo "Enclave name must not be empty." >&2
	exit 2
fi
if [[ ! "$ENCLAVE_NAME" =~ ^[A-Za-z0-9][-A-Za-z0-9]{0,59}$ ]]; then
	echo "Enclave name must match ^[A-Za-z0-9][-A-Za-z0-9]{0,59}$." >&2
	exit 2
fi
if [ -z "$DUMP_DIR" ]; then
	DUMP_DIR="/tmp/${ENCLAVE_NAME}-dump"
fi

if [ "$(uname -s)" = "Darwin" ] && [ "${VM64_E2E_CAFFEINATED:-0}" != "1" ]; then
	export VM64_E2E_CAFFEINATED=1
	exec caffeinate -dimsu "$SCRIPT_PATH" -e "$ENCLAVE_NAME" -d "$DUMP_DIR"
fi

cd "$REPO_ROOT"

enclave_uuid() {
	local identifier=$1
	local output uuid
	if ! output=$(kurtosis enclave inspect --full-uuids "$identifier" 2>&1); then
		return 1
	fi
	uuid=$(printf '%s\n' "$output" | awk '$1 == "UUID:" { print $2; exit }')
	if [ -z "$uuid" ]; then
		return 1
	fi
	printf '%s\n' "$uuid"
}

owned_uuid=
cleanup_attempted=0
stop_owned_enclave() {
	local current_uuid
	if ! current_uuid=$(enclave_uuid "$owned_uuid"); then
		echo "Could not inspect reserved enclave $ENCLAVE_NAME by UUID $owned_uuid." >&2
		return 1
	fi
	if [ "$current_uuid" != "$owned_uuid" ]; then
		echo "Reserved enclave lookup returned UUID $current_uuid, expected $owned_uuid; refusing cleanup." >&2
		return 1
	fi
	"$NETWORK_DIR/stop_local_testnet.sh" "$owned_uuid" "$DUMP_DIR"
}

cleanup() {
	local status=$?
	trap - EXIT INT TERM
	if [ -n "$owned_uuid" ] && [ "$cleanup_attempted" -eq 0 ]; then
		cleanup_attempted=1
		if ! stop_owned_enclave; then
			echo "Cleanup for enclave $ENCLAVE_NAME (UUID $owned_uuid) did not complete cleanly; inspect that UUID and the dump diagnostics to determine its state." >&2
			if [ "$status" -eq 0 ]; then
				status=1
			fi
		fi
	fi
	exit "$status"
}
signal_exit() {
	local status=$1
	trap - INT TERM
	exit "$status"
}
trap cleanup EXIT
trap 'signal_exit 130' INT
trap 'signal_exit 143' TERM

if ! add_output=$(kurtosis enclave add -n "$ENCLAVE_NAME" 2>&1); then
	echo "Could not atomically reserve fresh enclave $ENCLAVE_NAME; no existing enclave was modified." >&2
	echo "$add_output" >&2
	exit 1
fi
if ! owned_uuid=$(enclave_uuid "$ENCLAVE_NAME"); then
	echo "Created enclave $ENCLAVE_NAME but could not capture its UUID; it was preserved for diagnosis." >&2
	exit 1
fi

# Keep the large compiler preflight outside the live five-second-slot window.
make vm64-fixture-check
# Populate the host build cache before validators begin five-second duties.
make local-testnet-host-preflight

# -k prevents the startup helper from deleting the atomically reserved enclave.
"$NETWORK_DIR/start_local_testnet.sh" -k -e "$owned_uuid"
make local-testnet-e2e LOCAL_TESTNET_ENCLAVE="$owned_uuid"

cleanup_attempted=1
if ! stop_owned_enclave; then
	echo "Cleanup for enclave $ENCLAVE_NAME (UUID $owned_uuid) reported a failure; inspect that UUID and the dump diagnostics to determine its state." >&2
	exit 1
fi
owned_uuid=
trap - EXIT INT TERM

echo "VM64 end-to-end lifecycle passed and enclave cleanup completed."
