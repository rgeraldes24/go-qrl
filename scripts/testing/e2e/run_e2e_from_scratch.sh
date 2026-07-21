#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../../.." && pwd)
SCRIPT_PATH="$SCRIPT_DIR/$(basename -- "${BASH_SOURCE[0]}")"
NETWORK_DIR="$REPO_ROOT/scripts/local_testnet"
# shellcheck source=lifecycle_lib.sh
source "$SCRIPT_DIR/lifecycle_lib.sh"

KURTOSIS_BIN=${VM64_E2E_KURTOSIS_BIN:-kurtosis}
MAKE_BIN=${VM64_E2E_MAKE_BIN:-make}
NETWORK_START_SCRIPT=${VM64_E2E_NETWORK_START_SCRIPT:-$NETWORK_DIR/start_local_testnet.sh}
NETWORK_STOP_SCRIPT=${VM64_E2E_NETWORK_STOP_SCRIPT:-$NETWORK_DIR/stop_local_testnet.sh}
LIVE_STAGES_SCRIPT=${VM64_E2E_LIVE_STAGES_SCRIPT:-$SCRIPT_DIR/run_live_stages.sh}

ORIGINAL_ARGS=("$@")
ENCLAVE_NAME=vm64-e2e
DUMP_DIR=
LIFECYCLE_STATE_FILE=
RESUME_FILE=
PRESERVE_ON_FAILURE=${VM64_E2E_PRESERVE_ON_FAILURE:-1}
ENCLAVE_EXPLICIT=0
DUMP_EXPLICIT=0

usage() {
	echo "Usage: $0 [-e enclave] [-d dump-directory] [-p|-D] [-s state-file] [-r state-file]"
	echo "  -p  preserve the exact enclave and checkpoint on failure (default)"
	echo "  -D  dump and destroy the owned enclave on failure"
	echo "  -r  resume a preserved lifecycle from its failed stage"
}

while getopts "e:d:pDs:r:h" option; do
	case "$option" in
	e)
		ENCLAVE_NAME=$OPTARG
		ENCLAVE_EXPLICIT=1
		;;
	d)
		DUMP_DIR=$OPTARG
		DUMP_EXPLICIT=1
		;;
	p) PRESERVE_ON_FAILURE=1 ;;
	D) PRESERVE_ON_FAILURE=0 ;;
	s) LIFECYCLE_STATE_FILE=$OPTARG ;;
	r) RESUME_FILE=$OPTARG ;;
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
if [ -n "$RESUME_FILE" ] && { [ "$ENCLAVE_EXPLICIT" -eq 1 ] || [ "$DUMP_EXPLICIT" -eq 1 ] || [ -n "$LIFECYCLE_STATE_FILE" ]; }; then
	echo "-r loads the enclave, dump directory, and state path; do not combine it with -e, -d, or -s." >&2
	exit 2
fi
if [ "$PRESERVE_ON_FAILURE" != "0" ] && [ "$PRESERVE_ON_FAILURE" != "1" ]; then
	echo "VM64_E2E_PRESERVE_ON_FAILURE must be 0 or 1." >&2
	exit 2
fi

if [ "$(uname -s)" = "Darwin" ] && [ "${VM64_E2E_CAFFEINATED:-0}" != "1" ]; then
	export VM64_E2E_CAFFEINATED=1
	exec caffeinate -dimsu "$SCRIPT_PATH" "${ORIGINAL_ARGS[@]}"
fi

cd "$REPO_ROOT"
SOURCE_SHA=$(git rev-parse HEAD)
if [[ ! "$SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]]; then
	echo "Could not resolve an exact source commit." >&2
	exit 1
fi
TREE_ID=$("$LIFECYCLE_STATE_TOOL" tree-id --repo "$REPO_ROOT")
WORKTREE_STATUS=$(git status --porcelain --untracked-files=all)

enclave_uuid() {
	local identifier=$1
	local output uuid
	if ! output=$("$KURTOSIS_BIN" enclave inspect --full-uuids "$identifier" 2>&1); then
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
state_lock=
state_lock_token=
resume_available=1

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
	"$NETWORK_STOP_SCRIPT" "$owned_uuid" "$DUMP_DIR"
}

acquire_state_lock() {
	local allow_stale=$1
	if ! acquire_lifecycle_state_lock "$LIFECYCLE_STATE_FILE" "$allow_stale"; then
		state_lock=
		state_lock_token=
		return 1
	fi
	state_lock=$LIFECYCLE_STATE_LOCK_FILE
	state_lock_token=$LIFECYCLE_STATE_LOCK_TOKEN
}

release_state_lock() {
	if [ -z "$state_lock" ]; then
		return 0
	fi
	LIFECYCLE_STATE_LOCK_FILE=$state_lock
	LIFECYCLE_STATE_LOCK_TOKEN=$state_lock_token
	if ! release_lifecycle_state_lock; then
		return 1
	fi
	state_lock=
	state_lock_token=
}

cleanup() {
	local status=$?
	trap - EXIT INT TERM
	if [ -n "$owned_uuid" ] && [ "$cleanup_attempted" -eq 0 ]; then
		if [ "$status" -ne 0 ] && [ "$PRESERVE_ON_FAILURE" -eq 1 ]; then
			echo "VM64 E2E failed; preserving enclave $ENCLAVE_NAME (UUID $owned_uuid)." >&2
			if [ "$resume_available" -eq 1 ] && [ -s "$LIFECYCLE_STATE_FILE" ]; then
				echo "Resume from the failed stage with: $SCRIPT_PATH -r $LIFECYCLE_STATE_FILE" >&2
			fi
		else
			cleanup_attempted=1
			if stop_owned_enclave; then
				if [ "$status" -ne 0 ] && [ -s "$LIFECYCLE_STATE_FILE" ]; then
					if ! "$LIFECYCLE_STATE_TOOL" mark-cleaned --file "$LIFECYCLE_STATE_FILE"; then
						echo "The enclave was removed, but its lifecycle state could not be marked cleaned." >&2
						status=1
					fi
				fi
				owned_uuid=
			else
				echo "Cleanup for enclave $ENCLAVE_NAME (UUID $owned_uuid) did not complete cleanly; inspect that UUID and the dump diagnostics to determine its state." >&2
				if [ "$status" -eq 0 ]; then
					status=1
				fi
			fi
		fi
	fi
	if ! release_state_lock && [ "$status" -eq 0 ]; then
		status=1
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

if [ -n "$RESUME_FILE" ]; then
	LIFECYCLE_STATE_FILE=$RESUME_FILE
	PRESERVE_ON_FAILURE=1
	if ! acquire_state_lock 1; then
		exit 1
	fi
	"$LIFECYCLE_STATE_TOOL" validate --file "$LIFECYCLE_STATE_FILE" --source-sha "$SOURCE_SHA"
	if [ -n "$WORKTREE_STATUS" ] && [ "${VM64_E2E_DIAGNOSTIC_RESUME:-0}" != "1" ]; then
		echo "The checkout changed after provisioning. Set VM64_E2E_DIAGNOSTIC_RESUME=1 to record the changed tree and continue as diagnostic evidence." >&2
		exit 1
	fi
	ENCLAVE_NAME=$("$LIFECYCLE_STATE_TOOL" get --file "$LIFECYCLE_STATE_FILE" --field enclave_name)
	owned_uuid=$("$LIFECYCLE_STATE_TOOL" get --file "$LIFECYCLE_STATE_FILE" --field enclave_uuid)
	DUMP_DIR=$("$LIFECYCLE_STATE_TOOL" get --file "$LIFECYCLE_STATE_FILE" --field dump_dir)
	failed_stage=$("$LIFECYCLE_STATE_TOOL" get --file "$LIFECYCLE_STATE_FILE" --field current_stage)
	case "$failed_stage" in
	network-start)
		resume_available=0
		echo "A partially provisioned network-start stage cannot be replayed safely in place; preserve this enclave for diagnosis and start a fresh lifecycle." >&2
		exit 1
		;;
	el1 | el2 | deposit)
		resume_available=0
		echo "The legacy $failed_stage stage has no durable raw-transaction outbox and cannot be replayed safely; preserve this enclave for diagnosis. Use vm64e2e run/resume for interruption-safe certification." >&2
		exit 1
		;;
	esac
	current_uuid=$(enclave_uuid "$owned_uuid" || true)
	if [ "$current_uuid" != "$owned_uuid" ]; then
		resume_available=0
		echo "Preserved enclave UUID $owned_uuid is unavailable or changed; refusing resume." >&2
		exit 1
	fi
	"$LIFECYCLE_STATE_TOOL" mark-resumed --file "$LIFECYCLE_STATE_FILE" --tree-id "$TREE_ID"
else
	if [ -n "$WORKTREE_STATUS" ]; then
		echo "A fresh certified lifecycle requires a clean tracked and untracked worktree." >&2
		exit 1
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
	if [ -z "$LIFECYCLE_STATE_FILE" ]; then
		LIFECYCLE_STATE_FILE="${DUMP_DIR}.state.json"
	fi
	if ! mkdir -p -- "$(dirname -- "$LIFECYCLE_STATE_FILE")"; then
		echo "Could not create the lifecycle state directory for $LIFECYCLE_STATE_FILE." >&2
		exit 1
	fi
	if ! acquire_state_lock 0; then
		exit 1
	fi
	if [ -e "$LIFECYCLE_STATE_FILE" ]; then
		terminal_status=$("$LIFECYCLE_STATE_TOOL" get --file "$LIFECYCLE_STATE_FILE" --field status || true)
		case "$terminal_status" in
		complete_clean|complete_after_resume|cleaned_after_failure)
			archive_base="${LIFECYCLE_STATE_FILE}.${terminal_status}.$(date +%Y%m%d%H%M%S)"
			archived_state=$archive_base
			archive_suffix=0
			while [ -e "$archived_state" ]; do
				archive_suffix=$((archive_suffix + 1))
				archived_state="${archive_base}.${archive_suffix}"
			done
			mv "$LIFECYCLE_STATE_FILE" "$archived_state"
			echo "Archived terminal lifecycle state as $archived_state."
			;;
		*)
			echo "Refusing to replace non-terminal or invalid lifecycle state $LIFECYCLE_STATE_FILE." >&2
			exit 1
			;;
		esac
	fi
	if ! add_output=$("$KURTOSIS_BIN" enclave add -n "$ENCLAVE_NAME" 2>&1); then
		echo "Could not atomically reserve fresh enclave $ENCLAVE_NAME; no existing enclave was modified." >&2
		echo "$add_output" >&2
		exit 1
	fi
	if ! owned_uuid=$(enclave_uuid "$ENCLAVE_NAME"); then
		echo "Created enclave $ENCLAVE_NAME but could not capture its UUID; it was preserved for diagnosis." >&2
		exit 1
	fi
	"$LIFECYCLE_STATE_TOOL" init --file "$LIFECYCLE_STATE_FILE" \
		--source-sha "$SOURCE_SHA" --enclave-name "$ENCLAVE_NAME" \
		--enclave-uuid "$owned_uuid" --dump-dir "$DUMP_DIR" --tree-id "$TREE_ID"
	recorded_uuid=$("$LIFECYCLE_STATE_TOOL" get --file "$LIFECYCLE_STATE_FILE" --field enclave_uuid)
	if [ "$recorded_uuid" != "$owned_uuid" ]; then
		echo "Lifecycle state recorded UUID $recorded_uuid, expected owned UUID $owned_uuid." >&2
		exit 1
	fi
fi

# Keep the large compiler preflight outside the live five-second-slot window.
run_checkpointed_stage fixture "$MAKE_BIN" vm64-fixture-check
# Populate the host build cache before validators begin five-second duties.
run_checkpointed_stage host-preflight "$MAKE_BIN" local-testnet-host-preflight
# -k prevents the startup helper from deleting the atomically reserved enclave.
run_checkpointed_stage network-start "$NETWORK_START_SCRIPT" -k -e "$owned_uuid"

# shellcheck source=../../local_testnet/images.lock.env
source "$NETWORK_DIR/images.lock.env"
GENESIS_IMAGE=${LOCAL_TESTNET_GENESIS_IMAGE:-theqrl-dev/qrl-genesis-generator:${PINNED_GENERATOR_GIT_COMMIT:0:12}-${PINNED_QRYSM_GIT_COMMIT:0:12}}
"$LIVE_STAGES_SCRIPT" -e "$owned_uuid" -c "$SOURCE_SHA" \
	-g "$GENESIS_IMAGE" -s "$LIFECYCLE_STATE_FILE"

cleanup_attempted=1
if ! run_checkpointed_stage cleanup stop_owned_enclave; then
	echo "Cleanup for enclave $ENCLAVE_NAME (UUID $owned_uuid) reported a failure; inspect that UUID and the dump diagnostics to determine its state." >&2
	exit 1
fi
owned_uuid=
status=$("$LIFECYCLE_STATE_TOOL" get --file "$LIFECYCLE_STATE_FILE" --field status)
case "$status" in
complete_clean|complete_after_resume) ;;
*)
	echo "Lifecycle cleanup returned without a terminal success state: $status" >&2
	exit 1
	;;
esac
if ! release_state_lock; then
	exit 1
fi
trap - EXIT INT TERM

echo "VM64 end-to-end lifecycle passed with status $status and enclave cleanup completed."
