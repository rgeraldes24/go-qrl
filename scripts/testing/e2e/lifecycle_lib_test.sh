#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=lifecycle_lib.sh
source "$SCRIPT_DIR/lifecycle_lib.sh"

TEST_DIR=$(mktemp -d "${TMPDIR:-/tmp}/vm64-lifecycle-lib.XXXXXX")
cleanup() {
	rm -rf -- "$TEST_DIR"
}
trap cleanup EXIT

LIFECYCLE_STATE_FILE="$TEST_DIR/state.json"
MARKERS="$TEST_DIR/markers.txt"
SHA=aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
UUID=bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb
TREE_ID=dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd

"$LIFECYCLE_STATE_TOOL" init --file "$LIFECYCLE_STATE_FILE" \
	--source-sha "$SHA" --enclave-name vm64-test --enclave-uuid "$UUID" \
	--dump-dir "$TEST_DIR/dump" --tree-id "$TREE_ID"

run_checkpointed_stage fixture /bin/sh -c 'printf "fixture\n" >> "$1"' _ "$MARKERS"
run_checkpointed_stage fixture /bin/sh -c 'printf "duplicate\n" >> "$1"' _ "$MARKERS"
if run_checkpointed_stage host-preflight /bin/sh -c 'exit 23'; then
	echo "Failing stage unexpectedly passed." >&2
	exit 1
else
	status=$?
fi
if [ "$status" -ne 23 ]; then
	echo "Failing stage returned $status, want 23." >&2
	exit 1
fi

"$LIFECYCLE_STATE_TOOL" mark-resumed --file "$LIFECYCLE_STATE_FILE" --tree-id "$TREE_ID"
run_checkpointed_stage host-preflight /bin/sh -c 'printf "host\n" >> "$1"' _ "$MARKERS"

if [ "$(grep -c '^fixture$' "$MARKERS")" -ne 1 ] || grep -q '^duplicate$' "$MARKERS"; then
	echo "Completed stage was replayed." >&2
	exit 1
fi
if [ "$(grep -c '^host$' "$MARKERS")" -ne 1 ]; then
	echo "Failed stage did not resume exactly once." >&2
	exit 1
fi
if [ "$("$LIFECYCLE_STATE_TOOL" attempt-count --file "$LIFECYCLE_STATE_FILE" --stage host-preflight)" -ne 2 ]; then
	echo "Failed and resumed attempts were not both retained." >&2
	exit 1
fi

LOCK_STATE="$TEST_DIR/lock-checkpoint.json"
LOCK_FILE="${LOCK_STATE}.lock"
RECOVERY_FILE="${LOCK_FILE}.recovery"
DEAD_PID=1073741824
LOCAL_HOST=$(hostname)

release_test_lock() {
	if ! release_lifecycle_state_lock; then
		echo "Could not release test lifecycle lock." >&2
		exit 1
	fi
}

# An interruption while the complete owner candidate is still unpublished must
# not create a lock that blocks the next lifecycle process.
CANDIDATE=$(mktemp "${LOCK_FILE}.candidate.XXXXXX")
printf '%s\t%s\t%s\n' "$LOCAL_HOST" "$DEAD_PID" unpublished > "$CANDIDATE"
acquire_lifecycle_state_lock "$LOCK_STATE" 0
release_test_lock
if [ ! -f "$CANDIDATE" ]; then
	echo "Unpublished lock candidate was unexpectedly consumed." >&2
	exit 1
fi
rm -f -- "$CANDIDATE"

# A complete owner published before a hard stop is stale and recoverable.
printf '%s\t%s\t%s\n' "$LOCAL_HOST" "$DEAD_PID" stale-published > "$LOCK_FILE"
acquire_lifecycle_state_lock "$LOCK_STATE" 1
release_test_lock

# Recover both stale-replacement interruption boundaries: immediately after
# the recovery claim and after the stale primary link has been removed.
for recovery_point in claimed removed; do
	printf '%s\t%s\t%s\n' "$LOCAL_HOST" "$DEAD_PID" "stale-$recovery_point" > "$LOCK_FILE"
	_lifecycle_link_exact "$LOCK_FILE" "$RECOVERY_FILE"
	if [ "$recovery_point" = removed ]; then
		unlink "$LOCK_FILE"
	fi
	acquire_lifecycle_state_lock "$LOCK_STATE" 1
	if [ -e "$RECOVERY_FILE" ]; then
		echo "Interrupted $recovery_point recovery claim was not retired." >&2
		exit 1
	fi
	release_test_lock
done

# If replacement publication completed before claim cleanup, retire only the
# old claim and leave the different live owner untouched.
printf '%s\t%s\t%s\n' "$LOCAL_HOST" "$DEAD_PID" stale-claim > "$RECOVERY_FILE"
printf '%s\t%s\t%s\n' "$LOCAL_HOST" "$$" live-replacement > "$LOCK_FILE"
if acquire_lifecycle_state_lock "$LOCK_STATE" 1; then
	echo "Live replacement lock was stolen while retiring a stale claim." >&2
	exit 1
fi
if [ "$(awk -F '\t' 'NR == 1 { print $3 }' "$LOCK_FILE")" != live-replacement ] || [ -e "$RECOVERY_FILE" ]; then
	echo "Completed recovery changed the live replacement or retained its stale claim." >&2
	exit 1
fi
rm -f -- "$LOCK_FILE"

# A malformed externally-created lock is not evidence that its owner is dead;
# fail closed and retain the bytes for diagnosis.
printf '{\n' > "$LOCK_FILE"
if acquire_lifecycle_state_lock "$LOCK_STATE" 1; then
	echo "Malformed lifecycle lock was accepted." >&2
	exit 1
fi
if [ "$(cat "$LOCK_FILE")" != "{" ]; then
	echo "Malformed fail-closed lifecycle lock was replaced." >&2
	exit 1
fi
rm -f -- "$LOCK_FILE"

echo "VM64 lifecycle shell checkpoint test passed."
