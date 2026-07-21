#!/usr/bin/env bash

# Shared checkpoint helpers for the local VM64 lifecycle. The caller owns the
# state-file lock and must set LIFECYCLE_STATE_FILE before using these helpers.

LIFECYCLE_LIB_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
LIFECYCLE_STATE_TOOL="$LIFECYCLE_LIB_DIR/lifecycle_state.py"
LIFECYCLE_STATE_LOCK_FILE=
LIFECYCLE_STATE_LOCK_TOKEN=

_lifecycle_read_lock_owner() {
	local lock_file=$1
	local line extra
	if [ ! -f "$lock_file" ] || [ -L "$lock_file" ]; then
		return 1
	fi
	if ! awk -F '\t' 'NR != 1 || NF != 3 { invalid = 1 } END { exit invalid || NR != 1 }' "$lock_file"; then
		return 1
	fi
	if ! IFS= read -r line < "$lock_file"; then
		return 1
	fi
	IFS=$'\t' read -r LIFECYCLE_LOCK_OWNER_HOST LIFECYCLE_LOCK_OWNER_PID LIFECYCLE_LOCK_OWNER_TOKEN extra <<< "$line"
	if [ -n "${extra:-}" ] || [ -z "${LIFECYCLE_LOCK_OWNER_HOST:-}" ] || \
		[[ ! "${LIFECYCLE_LOCK_OWNER_PID:-}" =~ ^[1-9][0-9]*$ ]] || [ -z "${LIFECYCLE_LOCK_OWNER_TOKEN:-}" ]; then
		return 1
	fi
}

_lifecycle_link_exact() {
	python3 -c 'import os, sys; os.link(sys.argv[1], sys.argv[2], follow_symlinks=False)' "$1" "$2"
}

_lifecycle_publish_lock() {
	local lock_file=$1
	local owner_host=$2
	local owner_pid=$3
	local owner_token=$4
	local candidate
	if ! candidate=$(mktemp "${lock_file}.candidate.XXXXXX"); then
		return 1
	fi
	chmod 600 "$candidate"
	if ! printf '%s\t%s\t%s\n' "$owner_host" "$owner_pid" "$owner_token" > "$candidate"; then
		rm -f -- "$candidate"
		return 1
	fi
	if _lifecycle_link_exact "$candidate" "$lock_file" 2>/dev/null; then
		rm -f -- "$candidate"
		return 0
	fi
	rm -f -- "$candidate"
	if [ -e "$lock_file" ]; then
		return 10
	fi
	return 1
}

_lifecycle_reconcile_lock_recovery() {
	local lock_file=$1
	local recovery_file=$2
	local allow_stale=$3
	local local_host=$4
	if [ ! -e "$recovery_file" ]; then
		return 0
	fi
	if [ "$allow_stale" -ne 1 ]; then
		echo "Lifecycle lock recovery is already in progress: $lock_file" >&2
		return 1
	fi
	if ! _lifecycle_read_lock_owner "$recovery_file"; then
		echo "Lifecycle lock recovery owner is unverifiable: $recovery_file" >&2
		return 1
	fi
	if [ "$LIFECYCLE_LOCK_OWNER_HOST" != "$local_host" ] || kill -0 "$LIFECYCLE_LOCK_OWNER_PID" 2>/dev/null; then
		echo "Lifecycle lock recovery is held by a live, remote, or unverifiable owner: $lock_file" >&2
		return 1
	fi
	if [ ! -e "$lock_file" ]; then
		if ! _lifecycle_link_exact "$recovery_file" "$lock_file" 2>/dev/null && [ ! -e "$lock_file" ]; then
			echo "Could not restore interrupted lifecycle lock recovery: $lock_file" >&2
			return 1
		fi
	fi
	if [ "$lock_file" -ef "$recovery_file" ]; then
		if ! unlink "$recovery_file"; then
			echo "Could not retire restored lifecycle lock recovery: $recovery_file" >&2
			return 1
		fi
		return 0
	fi
	# A different complete lock was published before the prior process stopped.
	# Retire only the stale claim; the current lock is validated below.
	if ! unlink "$recovery_file"; then
		echo "Could not retire completed lifecycle lock recovery: $recovery_file" >&2
		return 1
	fi
}

acquire_lifecycle_state_lock() {
	local state_file=$1
	local allow_stale=$2
	local lock_file="${state_file}.lock"
	local recovery_file="${lock_file}.recovery"
	local local_host owner_host owner_pid owner_token token publish_status
	if [ "$allow_stale" != 0 ] && [ "$allow_stale" != 1 ]; then
		echo "Lifecycle lock stale-owner policy must be 0 or 1." >&2
		return 1
	fi
	if ! local_host=$(hostname); then
		echo "Could not identify the local host for lifecycle lock ownership." >&2
		return 1
	fi
	if ! _lifecycle_reconcile_lock_recovery "$lock_file" "$recovery_file" "$allow_stale" "$local_host"; then
		return 1
	fi
	token="$local_host:$$:${RANDOM}:$(date +%s)"
	if _lifecycle_publish_lock "$lock_file" "$local_host" "$$" "$token"; then
		LIFECYCLE_STATE_LOCK_FILE=$lock_file
		LIFECYCLE_STATE_LOCK_TOKEN=$token
		export VM64_E2E_STATE_LOCK_FILE=$lock_file
		export VM64_E2E_STATE_LOCK_TOKEN=$token
		return 0
	else
		publish_status=$?
	fi
	if [ "$publish_status" -ne 10 ]; then
		echo "Could not atomically publish lifecycle lock owner: $lock_file" >&2
		return 1
	fi
	if [ "$allow_stale" -ne 1 ]; then
		echo "Lifecycle state is already locked: $state_file" >&2
		return 1
	fi
	if ! _lifecycle_read_lock_owner "$lock_file"; then
		echo "Lifecycle state lock owner is unverifiable: $lock_file" >&2
		return 1
	fi
	owner_host=$LIFECYCLE_LOCK_OWNER_HOST
	owner_pid=$LIFECYCLE_LOCK_OWNER_PID
	owner_token=$LIFECYCLE_LOCK_OWNER_TOKEN
	if [ "$owner_host" != "$local_host" ] || kill -0 "$owner_pid" 2>/dev/null; then
		echo "Lifecycle state is held by a live, remote, or unverifiable owner: $state_file" >&2
		return 1
	fi
	echo "Recovering stale lifecycle lock left by PID $owner_pid on $owner_host."
	if ! _lifecycle_link_exact "$lock_file" "$recovery_file" 2>/dev/null; then
		echo "Lifecycle lock recovery is already in progress: $lock_file" >&2
		return 1
	fi
	if [ ! "$lock_file" -ef "$recovery_file" ] || ! _lifecycle_read_lock_owner "$recovery_file" || \
		[ "$LIFECYCLE_LOCK_OWNER_HOST" != "$owner_host" ] || [ "$LIFECYCLE_LOCK_OWNER_PID" != "$owner_pid" ] || \
		[ "$LIFECYCLE_LOCK_OWNER_TOKEN" != "$owner_token" ]; then
		rm -f -- "$recovery_file"
		echo "Lifecycle lock changed while stale recovery was being claimed: $lock_file" >&2
		return 1
	fi
	if ! unlink "$lock_file"; then
		rm -f -- "$recovery_file"
		echo "Could not remove stale lifecycle lock: $lock_file" >&2
		return 1
	fi
	if ! _lifecycle_publish_lock "$lock_file" "$local_host" "$$" "$token"; then
		if [ ! -e "$lock_file" ]; then
			_lifecycle_link_exact "$recovery_file" "$lock_file" 2>/dev/null || true
		fi
		rm -f -- "$recovery_file"
		echo "Could not publish replacement lifecycle lock: $lock_file" >&2
		return 1
	fi
	if ! unlink "$recovery_file"; then
		echo "Could not retire lifecycle lock recovery claim: $recovery_file" >&2
		return 1
	fi
	LIFECYCLE_STATE_LOCK_FILE=$lock_file
	LIFECYCLE_STATE_LOCK_TOKEN=$token
	export VM64_E2E_STATE_LOCK_FILE=$lock_file
	export VM64_E2E_STATE_LOCK_TOKEN=$token
}

release_lifecycle_state_lock() {
	local lock_file=${LIFECYCLE_STATE_LOCK_FILE:-}
	local owner_host owner_pid owner_token
	if [ -z "$lock_file" ]; then
		return 0
	fi
	if ! _lifecycle_read_lock_owner "$lock_file"; then
		echo "Could not read lifecycle lock ownership from $lock_file." >&2
		return 1
	fi
	owner_host=$LIFECYCLE_LOCK_OWNER_HOST
	owner_pid=$LIFECYCLE_LOCK_OWNER_PID
	owner_token=$LIFECYCLE_LOCK_OWNER_TOKEN
	if [ "$owner_token" != "$LIFECYCLE_STATE_LOCK_TOKEN" ] || [ "$owner_pid" != "$$" ] || [ "$owner_host" != "$(hostname)" ]; then
		echo "Refusing to release lifecycle lock with changed ownership: $lock_file" >&2
		return 1
	fi
	if ! unlink "$lock_file"; then
		echo "Could not remove lifecycle lock file $lock_file." >&2
		return 1
	fi
	LIFECYCLE_STATE_LOCK_FILE=
	LIFECYCLE_STATE_LOCK_TOKEN=
	unset VM64_E2E_STATE_LOCK_FILE VM64_E2E_STATE_LOCK_TOKEN
}

run_checkpointed_stage() {
	local stage=$1
	shift
	local state_status status
	if "$LIFECYCLE_STATE_TOOL" is-complete --file "$LIFECYCLE_STATE_FILE" --stage "$stage"; then
		echo "Skipping completed VM64 E2E stage: $stage"
		return 0
	else
		state_status=$?
		if [ "$state_status" -ne 10 ]; then
			echo "Could not read completion state for VM64 E2E stage $stage." >&2
			return "$state_status"
		fi
	fi
	echo
	echo "=== VM64 E2E stage: $stage ==="
	if ! "$LIFECYCLE_STATE_TOOL" begin --file "$LIFECYCLE_STATE_FILE" --stage "$stage"; then
		echo "Could not checkpoint the start of VM64 E2E stage $stage; the stage was not run." >&2
		return 1
	fi
	if "$@"; then
		status=0
	else
		status=$?
	fi
	if ! "$LIFECYCLE_STATE_TOOL" finish --file "$LIFECYCLE_STATE_FILE" --stage "$stage" --exit-code "$status"; then
		echo "Could not checkpoint the result of VM64 E2E stage $stage." >&2
		return 1
	fi
	return "$status"
}
