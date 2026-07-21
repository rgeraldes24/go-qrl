#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
RECOVER="$SCRIPT_DIR/recover_enclave_uuid.sh"
TEST_UUID=0123456789abcdef0123456789abcdef
TEST_ENCLAVE=vm64-recovery-test

test_dir=$(mktemp -d "${TMPDIR:-/tmp}/vm64-enclave-recovery-test.XXXXXX")
cleanup() {
	rm -rf "$test_dir"
}
trap cleanup EXIT

fake_kurtosis="$test_dir/kurtosis"
cat > "$fake_kurtosis" <<'FAKE_KURTOSIS'
#!/usr/bin/env bash
set -Eeuo pipefail

if [[ "$*" != "enclave inspect --full-uuids $FAKE_ENCLAVE" ]]; then
	echo "unexpected fake Kurtosis arguments: $*" >&2
	exit 2
fi

case "$FAKE_MODE" in
valid)
	printf 'Name: %s\nUUID: %s\n' "$FAKE_ENCLAVE" "$FAKE_UUID"
	;;
delayed)
	fake_count=0
	if [ -s "$FAKE_STATE" ]; then
		fake_count=$(<"$FAKE_STATE")
	fi
	fake_count=$((fake_count + 1))
	printf '%s\n' "$fake_count" > "$FAKE_STATE"
	if (( fake_count < 2 )); then
		echo "enclave not found yet" >&2
		exit 1
	fi
	printf 'Name: %s\nUUID: %s\n' "$FAKE_ENCLAVE" "$FAKE_UUID"
	;;
malformed)
	printf 'Name: %s\nUUID: 0123\n' "$FAKE_ENCLAVE"
	;;
mismatched)
	printf 'Name: different-enclave\nUUID: %s\n' "$FAKE_UUID"
	;;
missing)
	echo "enclave not found" >&2
	exit 1
	;;
*)
	echo "unknown FAKE_MODE=$FAKE_MODE" >&2
	exit 2
	;;
esac
FAKE_KURTOSIS
chmod 0755 "$fake_kurtosis"

run_recovery() {
	FAKE_MODE=$1
	shift
	env \
		KURTOSIS_BIN="$fake_kurtosis" \
		FAKE_MODE="$FAKE_MODE" \
		FAKE_ENCLAVE="$TEST_ENCLAVE" \
		FAKE_UUID="$TEST_UUID" \
		FAKE_STATE="$test_dir/state" \
		VM64_E2E_ENCLAVE_RECOVERY_POLL_SECONDS=1 \
		VM64_E2E_ENCLAVE_RECOVERY_ATTEMPT_SECONDS=1 \
		"$RECOVER" "$TEST_ENCLAVE" "$@"
}

valid_uuid=$(run_recovery valid 2 2> "$test_dir/valid.log")
if [ "$valid_uuid" != "$TEST_UUID" ]; then
	echo "valid recovery UUID = $valid_uuid, want $TEST_UUID" >&2
	exit 1
fi

delayed_uuid=$(run_recovery delayed 4 2> "$test_dir/delayed.log")
if [ "$delayed_uuid" != "$TEST_UUID" ]; then
	echo "delayed recovery UUID = $delayed_uuid, want $TEST_UUID" >&2
	exit 1
fi
if [ "$(<"$test_dir/state")" != "2" ]; then
	echo "delayed recovery did not poll exactly twice" >&2
	exit 1
fi

recovery_test_status=0
run_recovery malformed 2 > "$test_dir/malformed.out" \
	2> "$test_dir/malformed.log" || recovery_test_status=$?
if [ "$recovery_test_status" -ne 1 ]; then
	echo "malformed UUID status = $recovery_test_status, want 1" >&2
	exit 1
fi

recovery_test_status=0
run_recovery mismatched 2 > "$test_dir/mismatched.out" \
	2> "$test_dir/mismatched.log" || recovery_test_status=$?
if [ "$recovery_test_status" -ne 1 ]; then
	echo "mismatched enclave status = $recovery_test_status, want 1" >&2
	exit 1
fi

recovery_test_status=0
run_recovery missing 1 > "$test_dir/missing.out" \
	2> "$test_dir/missing.log" || recovery_test_status=$?
if [ "$recovery_test_status" -ne 1 ]; then
	echo "missing enclave status = $recovery_test_status, want 1" >&2
	exit 1
fi

echo "enclave UUID recovery tests: PASS"
