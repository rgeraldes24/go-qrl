#!/usr/bin/env bash

# Bound a command by the shared CI pre-cleanup deadline. Individual workflow
# step timeouts still apply; this guard prevents their cumulative runtime from
# consuming the time reserved for enclave diagnostics and cleanup.
set -Eeuo pipefail

if (( $# == 0 )); then
	echo "Usage: $0 command [argument ...]" >&2
	exit 2
fi

deadline=${VM64_E2E_PRE_CLEANUP_DEADLINE_EPOCH:-}
if [[ ! "$deadline" =~ ^[1-9][0-9]*$ ]]; then
	echo "VM64_E2E_PRE_CLEANUP_DEADLINE_EPOCH must be a positive Unix timestamp." >&2
	exit 2
fi

now=$(date +%s)
remaining=$((deadline - now))
if (( remaining <= 0 )); then
	echo "VM64 E2E pre-cleanup deadline has been reached." >&2
	exit 124
fi

if command -v timeout >/dev/null 2>&1; then
	timeout_bin=timeout
elif command -v gtimeout >/dev/null 2>&1; then
	timeout_bin=gtimeout
else
	echo "VM64 E2E deadline guard requires GNU timeout (or gtimeout)." >&2
	exit 127
fi

# systemcheck can sequentially recover three stopped services with a bounded
# two-minute context each after receiving INT. Leave enough grace for those
# recovery defers before forcing termination.
exec "$timeout_bin" --verbose --signal=INT --kill-after=7m "${remaining}s" "$@"
