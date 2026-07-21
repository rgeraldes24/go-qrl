#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
RUNNER=(go -C "$SCRIPT_DIR" run ./cmd/vm64e2e)

if [ "$(uname -s)" = Darwin ] && [ "${VM64_E2E_CAFFEINATED:-0}" != 1 ]; then
	export VM64_E2E_CAFFEINATED=1
	exec caffeinate -dimsu "${RUNNER[@]}" "$@"
fi

exec "${RUNNER[@]}" "$@"
