#!/usr/bin/env bash

set -Eeuo pipefail

if [ "${0##*/}" = "go" ]; then
	: "${VM64_E2E_FAKE_GO_LOG:?fake Go log is required}"
	printf '%s\n' "$*" >> "$VM64_E2E_FAKE_GO_LOG"
	exit 0
fi

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
SCRIPT_PATH="$SCRIPT_DIR/$(basename -- "${BASH_SOURCE[0]}")"
STATE_TOOL="$SCRIPT_DIR/lifecycle_state.py"
SOURCE_SHA=1111111111111111111111111111111111111111
ENCLAVE_UUID=22222222222222222222222222222222
INITIAL_TREE_ID=3333333333333333333333333333333333333333333333333333333333333333
RESUME_TREE_ID=4444444444444444444444444444444444444444444444444444444444444444

TEST_ROOT=$(mktemp -d "${TMPDIR:-/tmp}/go-qrl-run-live-stages.XXXXXX")
cleanup() {
	rm -rf -- "$TEST_ROOT"
}
trap cleanup EXIT HUP INT TERM

STATE_FILE="$TEST_ROOT/checkpoint.json"
DUMP_DIR="$TEST_ROOT/dump"
FAKE_BIN="$TEST_ROOT/bin"
GO_LOG="$TEST_ROOT/go.log"
mkdir -p "$DUMP_DIR" "$FAKE_BIN"
ln -s "$SCRIPT_PATH" "$FAKE_BIN/go"

"$STATE_TOOL" init \
	--file "$STATE_FILE" \
	--source-sha "$SOURCE_SHA" \
	--enclave-name compatibility-test \
	--enclave-uuid "$ENCLAVE_UUID" \
	--dump-dir "$DUMP_DIR" \
	--tree-id "$INITIAL_TREE_ID"

for stage in fixture host-preflight network-start el1 el2 deposit; do
	"$STATE_TOOL" begin --file "$STATE_FILE" --stage "$stage"
	"$STATE_TOOL" finish --file "$STATE_FILE" --stage "$stage" --exit-code 0
done

# Model a preserved failure so the driver must resume at system-base rather
# than replaying any completed network-independent or live transaction stage.
"$STATE_TOOL" begin --file "$STATE_FILE" --stage system-base
"$STATE_TOOL" finish --file "$STATE_FILE" --stage system-base --exit-code 1
"$STATE_TOOL" mark-resumed --file "$STATE_FILE" --tree-id "$RESUME_TREE_ID"

LOCK_FILE="${STATE_FILE}.lock"
LOCK_TOKEN=run-live-stages-test-token
printf '%s\t%s\t%s\n' "$(hostname)" "$$" "$LOCK_TOKEN" > "$LOCK_FILE"

PATH="$FAKE_BIN:$PATH" \
	VM64_E2E_FAKE_GO_LOG="$GO_LOG" \
	VM64_E2E_STATE_LOCK_FILE="$LOCK_FILE" \
	VM64_E2E_STATE_LOCK_TOKEN="$LOCK_TOKEN" \
	"$SCRIPT_DIR/run_live_stages.sh" \
	-e "$ENCLAVE_UUID" \
	-c "$SOURCE_SHA" \
	-g example.invalid/qrl-genesis:test \
	-s "$STATE_FILE" \
	-o "$TEST_ROOT/results"

calls=()
while IFS= read -r call; do
	calls+=("$call")
done < "$GO_LOG"
if [ "${#calls[@]}" -ne 5 ]; then
	printf 'fake Go calls = %s, want five system/fresh-sync resume calls\n' "${#calls[@]}" >&2
	printf '  %s\n' "${calls[@]}" >&2
	exit 1
fi

expect_call() {
	local fragment=$1
	local call
	for call in "${calls[@]}"; do
		if [[ "$call" == *"$fragment"* ]]; then
			return 0
		fi
	done
	printf 'missing fake Go call containing: %s\n' "$fragment" >&2
	exit 1
}

expect_call "run ./cmd/systemcheck -enclave $ENCLAVE_UUID -phase base -checkpoint $STATE_FILE"
expect_call "run ./cmd/systemcheck -enclave $ENCLAVE_UUID -phase signer-restart -checkpoint $STATE_FILE -require-zero-duty-history"
expect_call "run ./cmd/systemcheck -enclave $ENCLAVE_UUID -phase participant-restart -checkpoint $STATE_FILE -require-zero-duty-history"
expect_call "run ./cmd/freshsync -enclave $ENCLAVE_UUID -checkpoint $STATE_FILE -syncmode snap"
expect_call "run ./cmd/freshsync -enclave $ENCLAVE_UUID -checkpoint $STATE_FILE -syncmode full"

for call in "${calls[@]}"; do
	if [[ "$call" == *"run ./cmd/freshsync"* && "$call" == *"-cleanup-on-failure"* ]]; then
		echo "canonical fresh-sync retry unexpectedly deletes its resumable service pair: $call" >&2
		exit 1
	fi
done

if [[ "${calls[0]}" == *"-require-zero-duty-history"* ]]; then
	echo 'resumed system-base unexpectedly required zero process-lifetime duty history' >&2
	exit 1
fi

printf 'run_live_stages checkpoint propagation and resume-point tests passed\n'
