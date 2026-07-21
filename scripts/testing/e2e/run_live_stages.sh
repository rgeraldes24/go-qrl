#!/usr/bin/env bash

# Runs the ordered live VM64 gates against an existing network. A lifecycle
# state file makes every completed stage durable so a preserved local enclave
# can continue at the failed stage.

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_ROOT=$(cd -- "$SCRIPT_DIR/../../.." && pwd)
# shellcheck source=lifecycle_lib.sh
source "$SCRIPT_DIR/lifecycle_lib.sh"

ENCLAVE=
SOURCE_SHA=
GENESIS_IMAGE=
LIFECYCLE_STATE_FILE=
RESULTS_DIR="$SCRIPT_DIR/logs/test-results"

usage() {
	echo "Usage: $0 -e enclave -c source-sha -g genesis-image -s state-file [-o results-directory]"
}

while getopts "e:c:g:s:o:h" option; do
	case "$option" in
	e) ENCLAVE=$OPTARG ;;
	c) SOURCE_SHA=$OPTARG ;;
	g) GENESIS_IMAGE=$OPTARG ;;
	s) LIFECYCLE_STATE_FILE=$OPTARG ;;
	o) RESULTS_DIR=$OPTARG ;;
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
if [ "$#" -ne 0 ] || [ -z "$ENCLAVE" ] || [ -z "$SOURCE_SHA" ] || [ -z "$GENESIS_IMAGE" ] || [ -z "$LIFECYCLE_STATE_FILE" ]; then
	usage >&2
	exit 2
fi
if [[ ! "$SOURCE_SHA" =~ ^[0-9a-f]{40}$ ]]; then
	echo "Source SHA must be an exact lowercase 40-character commit." >&2
	exit 2
fi

cd "$REPO_ROOT"
"$LIFECYCLE_STATE_TOOL" validate --file "$LIFECYCLE_STATE_FILE" --source-sha "$SOURCE_SHA"
RECORDED_UUID=$("$LIFECYCLE_STATE_TOOL" get --file "$LIFECYCLE_STATE_FILE" --field enclave_uuid)
if [ "$ENCLAVE" != "$RECORDED_UUID" ]; then
	echo "Live-stage enclave $ENCLAVE does not match checkpoint UUID $RECORDED_UUID." >&2
	exit 1
fi
EXPECTED_LOCK_FILE="${LIFECYCLE_STATE_FILE}.lock"
if [ "${VM64_E2E_STATE_LOCK_FILE:-}" != "$EXPECTED_LOCK_FILE" ] || [ ! -f "$EXPECTED_LOCK_FILE" ]; then
	echo "Live stages require the owning lifecycle process and its state lock." >&2
	exit 1
fi
if ! _lifecycle_read_lock_owner "$EXPECTED_LOCK_FILE"; then
	echo "Could not read lifecycle lock ownership before live stages." >&2
	exit 1
fi
LOCK_HOST=$LIFECYCLE_LOCK_OWNER_HOST
LOCK_PID=$LIFECYCLE_LOCK_OWNER_PID
LOCK_TOKEN=$LIFECYCLE_LOCK_OWNER_TOKEN
if [ -z "${VM64_E2E_STATE_LOCK_TOKEN:-}" ] || [ "$LOCK_TOKEN" != "$VM64_E2E_STATE_LOCK_TOKEN" ] || \
	[ "$LOCK_HOST" != "$(hostname)" ] || [ "$LOCK_PID" != "$PPID" ]; then
	echo "Lifecycle lock ownership changed before live stages." >&2
	exit 1
fi

run_system_phase() {
	local stage=$1
	local phase=$2
	local attempt_count
	attempt_count=$("$LIFECYCLE_STATE_TOOL" attempt-count --file "$LIFECYCLE_STATE_FILE" --stage "$stage")
	if [ "$attempt_count" -eq 0 ]; then
		run_checkpointed_stage "$stage" go -C scripts/testing/e2e run ./cmd/systemcheck \
			-enclave "$ENCLAVE" -phase "$phase" -checkpoint "$LIFECYCLE_STATE_FILE" \
			-require-zero-duty-history
		return
	fi
	echo "Retrying $stage as diagnostic evidence; pre-existing process-cumulative duty failures are baselined, while every new failure remains fatal."
	run_checkpointed_stage "$stage" go -C scripts/testing/e2e run ./cmd/systemcheck \
		-enclave "$ENCLAVE" -phase "$phase" -checkpoint "$LIFECYCLE_STATE_FILE"
}

run_checkpointed_stage el1 env EXPECTED_GIT_COMMIT="$SOURCE_SHA" \
	"$SCRIPT_DIR/run_tests.sh" -c -e "$ENCLAVE" -s el-1-gqrl-qrysm -o "$RESULTS_DIR/el-1"
run_checkpointed_stage el2 env EXPECTED_GIT_COMMIT="$SOURCE_SHA" \
	"$SCRIPT_DIR/run_tests.sh" -c -C -e "$ENCLAVE" -s el-2-gqrl-qrysm -o "$RESULTS_DIR/el-2"
run_checkpointed_stage deposit go -C scripts/testing/e2e run ./cmd/depositcheck \
	-enclave "$ENCLAVE" -generator-image "$GENESIS_IMAGE"
run_system_phase system-base base
run_system_phase system-signer signer-restart
run_system_phase system-participant participant-restart
run_checkpointed_stage fresh-snap go -C scripts/testing/e2e run ./cmd/freshsync \
	-enclave "$ENCLAVE" -checkpoint "$LIFECYCLE_STATE_FILE" -syncmode snap \
	-fresh-el-service fresh-sync-el-snap -fresh-cl-service fresh-sync-cl-snap
run_checkpointed_stage fresh-full go -C scripts/testing/e2e run ./cmd/freshsync \
	-enclave "$ENCLAVE" -checkpoint "$LIFECYCLE_STATE_FILE" -syncmode full \
	-fresh-el-service fresh-sync-el-full -fresh-cl-service fresh-sync-cl-full
