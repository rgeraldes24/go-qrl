#!/usr/bin/env bash

# Resolve a run-unique enclave name to a validated full Kurtosis UUID. The
# bounded polling handles the ambiguous case where the CLI is interrupted after
# the engine accepted an enclave-add request but before the add response returns.
set -Eeuo pipefail

if (( $# < 1 || $# > 2 )); then
	echo "Usage: $0 enclave-name [wait-seconds]" >&2
	exit 2
fi

enclave_name=$1
wait_seconds=${2:-60}
poll_seconds=${VM64_E2E_ENCLAVE_RECOVERY_POLL_SECONDS:-2}
attempt_seconds=${VM64_E2E_ENCLAVE_RECOVERY_ATTEMPT_SECONDS:-10}
kurtosis_bin=${KURTOSIS_BIN:-kurtosis}

if [[ ! "$enclave_name" =~ ^[A-Za-z0-9][-A-Za-z0-9]{0,59}$ ]]; then
	echo "Enclave name must match ^[A-Za-z0-9][-A-Za-z0-9]{0,59}$." >&2
	exit 2
fi
for value_name in wait_seconds poll_seconds attempt_seconds; do
	value=${!value_name}
	if [[ ! "$value" =~ ^[1-9][0-9]*$ ]]; then
		echo "$value_name must be a positive integer." >&2
		exit 2
	fi
done
if ! command -v "$kurtosis_bin" >/dev/null 2>&1; then
	echo "Kurtosis command is unavailable: $kurtosis_bin" >&2
	exit 127
fi

if command -v timeout >/dev/null 2>&1; then
	timeout_bin=timeout
elif command -v gtimeout >/dev/null 2>&1; then
	timeout_bin=gtimeout
else
	echo "Enclave UUID recovery requires GNU timeout (or gtimeout)." >&2
	exit 127
fi

recovery_deadline_epoch=$(( $(date +%s) + wait_seconds ))
recovery_attempt=0
while :; do
	recovery_attempt=$((recovery_attempt + 1))
	inspect_status=0
	inspect_output=$("$timeout_bin" --verbose --signal=INT --kill-after=5s \
		"${attempt_seconds}s" "$kurtosis_bin" enclave inspect \
		--full-uuids "$enclave_name" 2>&1) || inspect_status=$?
	printf 'enclave UUID recovery attempt %d status=%d at %s:\n%s\n' \
		"$recovery_attempt" "$inspect_status" \
		"$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$inspect_output" >&2
	if (( inspect_status == 0 )); then
		enclave_uuid=$(printf '%s\n' "$inspect_output" |
			awk '$1 == "UUID:" { print $2; exit }')
		if [[ ! "$enclave_uuid" =~ ^[0-9a-f]{32}$ ]]; then
			echo "Enclave inspection returned an invalid full UUID: $enclave_uuid" >&2
			exit 1
		fi
		resolved_name=$(printf '%s\n' "$inspect_output" |
			awk '$1 == "Name:" { print $2; exit }')
		if [ "$resolved_name" != "$enclave_name" ]; then
			echo "Enclave inspection returned name $resolved_name, want $enclave_name." >&2
			exit 1
		fi
		printf '%s\n' "$enclave_uuid"
		exit 0
	fi
	if (( $(date +%s) >= recovery_deadline_epoch )); then
		echo "Could not resolve enclave $enclave_name to a full UUID within ${wait_seconds}s." >&2
		exit 1
	fi
	sleep "$poll_seconds"
done
