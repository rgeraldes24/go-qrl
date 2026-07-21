#!/usr/bin/env bash

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ROOT_DIR=$SCRIPT_DIR/../..
test_directory=$(mktemp -d "${TMPDIR:-/tmp}/prepare-local-testnet-test.XXXXXX")
cleanup() {
	rm -rf -- "$test_directory"
}
trap cleanup EXIT

fake_bin=$test_directory/bin
mkdir -p "$fake_bin"
export FAKE_KURTOSIS_LOG=$test_directory/kurtosis.log

cat >"$fake_bin/kurtosis" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
printf '%s\n' "$*" >> "$FAKE_KURTOSIS_LOG"
if [ "$#" -eq 1 ] && [ "$1" = version ]; then
	printf 'CLI Version: 1.20.0\nEngine Version: 1.20.0\n'
	exit 0
fi
echo "unexpected preparation-time Kurtosis operation: $*" >&2
exit 97
EOF

cat >"$fake_bin/docker" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
if [ "$#" -eq 1 ] && [ "$1" = --version ]; then
	echo 'Docker version 29.0.0, build fake'
	exit 0
fi
if [ "${1:-}" = image ] && [ "${2:-}" = inspect ] && [ "${3:-}" = --format ]; then
	echo 'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'
	exit 0
fi
echo "unexpected preparation-time Docker operation: $*" >&2
exit 98
EOF

cat >"$fake_bin/yq" <<'EOF'
#!/usr/bin/env bash
set -Eeuo pipefail
if [ "${1:-}" = --version ]; then
	echo 'yq (https://github.com/mikefarah/yq/) version v4.47.2'
	exit 0
fi
if [ "${1:-}" = eval ]; then
	shift
	if [ "${1:-}" = -r ]; then
		shift
	fi
	expression=${1:-}
	params_file=${2:-}
	case "$expression" in
	'.participants | length')
		grep -Ec '^[[:space:]]*-[[:space:]]+[A-Za-z_]+:' "$params_file"
		exit 0
		;;
	'.participants[].el_image | select(tag == "!!str")')
		sed -n 's/^[[:space:]-]*el_image:[[:space:]]*//p' "$params_file"
		exit 0
		;;
	'.participants[].remote_signer_image | select(tag == "!!str")')
		sed -n 's/^[[:space:]-]*remote_signer_image:[[:space:]]*//p' "$params_file"
		exit 0
		;;
	esac
	# Other eval calls only edit the temporary effective-params copy. The
	# boundary test need not reproduce those deterministic YAML edits.
	exit 0
fi
if [ "${1:-}" = -n ]; then
	cat <<JSON
{
  "schema": 1,
  "source_sha": "$SOURCE_SHA",
  "worktree_dirty": $WORKTREE_DIRTY,
  "qrl_package": {"repository": "$QRL_PACKAGE_REPO", "revision": "$QRL_PKG_VERSION"},
  "qrysm": {"repository": "$QRYSM_GIT_REPO", "revision": "$QRYSM_GIT_COMMIT"},
  "generator": {"repository": "$GENERATOR_GIT_REPO", "revision": "$GENERATOR_GIT_COMMIT"},
  "effective_params": {"path": "$EFFECTIVE_PARAMS_PATH", "sha256": "$EFFECTIVE_PARAMS_SHA256"},
  "images": {
    "execution": {"name": "$EL_IMAGE", "id": "$EL_IMAGE_ID"},
    "alltools": {"name": "$ALLTOOLS_IMAGE", "id": "$ALLTOOLS_IMAGE_ID"},
    "consensus": {"name": "$CL_IMAGE", "id": "$CL_IMAGE_ID"},
    "validator": {"name": "$VC_IMAGE", "id": "$VC_IMAGE_ID"},
    "genesis": {"name": "$GENESIS_IMAGE", "id": "$GENESIS_IMAGE_ID"}
  },
  "build": {"execution": $BUILD_IMAGE, "qrysm": $BUILD_QRYSM_IMAGES, "genesis": $BUILD_GENESIS_IMAGE},
  "versions": {"docker": "$DOCKER_VERSION", "kurtosis": "fake", "yq": "$YQ_VERSION_OUTPUT", "go": "$GO_VERSION_OUTPUT"}
}
JSON
	exit 0
fi
echo "unexpected preparation-time yq operation: $*" >&2
exit 99
EOF
chmod +x "$fake_bin/docker" "$fake_bin/kurtosis" "$fake_bin/yq"

source_sha=$(git -C "$ROOT_DIR" rev-parse HEAD)

run_prepare() {
	local params_file=$1
	local effective=$2
	local preparation=$3
	PATH="$fake_bin:$PATH" \
		SOURCE_SHA=$source_sha \
		BUILD_QRYSM_IMAGES=false \
		BUILD_GENESIS_IMAGE=false \
		CL_IMAGE=example.invalid/consensus:test \
		VC_IMAGE=example.invalid/validator:test \
		GENESIS_IMAGE=example.invalid/genesis:test \
		EFFECTIVE_PARAMS_OUTPUT=$effective \
		PREPARATION_OUTPUT=$preparation \
		"$SCRIPT_DIR/prepare_local_testnet.sh" -e ignored-by-preparation -b false -n "$params_file"
}

unset EL_IMAGE ALLTOOLS_IMAGE
consistent_params=$test_directory/consistent.yaml
cat >"$consistent_params" <<'EOF'
participants:
  - el_image: example.invalid/execution:yaml
    remote_signer_image: example.invalid/alltools:yaml
  - el_image: example.invalid/execution:yaml
    remote_signer_image: example.invalid/alltools:yaml
EOF
effective=$test_directory/network_params.effective.yaml
preparation=$test_directory/preparation.json
run_prepare "$consistent_params" "$effective" "$preparation"

test -s "$effective"
test -s "$preparation"
grep -Fq '"schema": 1' "$preparation"
grep -Fq '"execution": {"name": "example.invalid/execution:yaml"' "$preparation"
grep -Fq '"alltools": {"name": "example.invalid/alltools:yaml"' "$preparation"

override_params=$test_directory/override.yaml
cat >"$override_params" <<'EOF'
participants:
  - name: one
  - name: two
EOF
override_preparation=$test_directory/override-preparation.json
EL_IMAGE=example.invalid/execution:override \
	ALLTOOLS_IMAGE=example.invalid/alltools:override \
	run_prepare "$override_params" "$test_directory/override-effective.yaml" "$override_preparation"
grep -Fq '"execution": {"name": "example.invalid/execution:override"' "$override_preparation"
grep -Fq '"alltools": {"name": "example.invalid/alltools:override"' "$override_preparation"

mixed_params=$test_directory/mixed.yaml
cat >"$mixed_params" <<'EOF'
participants:
  - el_image: example.invalid/execution:one
    remote_signer_image: example.invalid/alltools:same
  - el_image: example.invalid/execution:two
    remote_signer_image: example.invalid/alltools:same
EOF
if run_prepare "$mixed_params" "$test_directory/mixed-effective.yaml" "$test_directory/mixed-preparation.json" >"$test_directory/mixed.stdout" 2>"$test_directory/mixed.stderr"; then
	echo "Preparation accepted mixed participant execution images." >&2
	exit 1
fi
grep -Fq 'mixed el_image values' "$test_directory/mixed.stderr"

mixed_signer_params=$test_directory/mixed-signer.yaml
cat >"$mixed_signer_params" <<'EOF'
participants:
  - el_image: example.invalid/execution:same
    remote_signer_image: example.invalid/alltools:one
  - el_image: example.invalid/execution:same
    remote_signer_image: example.invalid/alltools:two
EOF
if run_prepare "$mixed_signer_params" "$test_directory/mixed-signer-effective.yaml" "$test_directory/mixed-signer-preparation.json" >"$test_directory/mixed-signer.stdout" 2>"$test_directory/mixed-signer.stderr"; then
	echo "Preparation accepted mixed participant remote-signer images." >&2
	exit 1
fi
grep -Fq 'mixed remote_signer_image values' "$test_directory/mixed-signer.stderr"

missing_params=$test_directory/missing.yaml
cat >"$missing_params" <<'EOF'
participants:
  - el_image: example.invalid/execution:same
    remote_signer_image: example.invalid/alltools:same
  - el_image: example.invalid/execution:same
EOF
if run_prepare "$missing_params" "$test_directory/missing-effective.yaml" "$test_directory/missing-preparation.json" >"$test_directory/missing.stdout" 2>"$test_directory/missing.stderr"; then
	echo "Preparation accepted a missing participant remote-signer image." >&2
	exit 1
fi
grep -Fq 'must define a non-empty string remote_signer_image for every participant' "$test_directory/missing.stderr"

missing_execution_params=$test_directory/missing-execution.yaml
cat >"$missing_execution_params" <<'EOF'
participants:
  - el_image: example.invalid/execution:same
    remote_signer_image: example.invalid/alltools:same
  - remote_signer_image: example.invalid/alltools:same
EOF
if run_prepare "$missing_execution_params" "$test_directory/missing-execution-effective.yaml" "$test_directory/missing-execution-preparation.json" >"$test_directory/missing-execution.stdout" 2>"$test_directory/missing-execution.stderr"; then
	echo "Preparation accepted a missing participant execution image." >&2
	exit 1
fi
grep -Fq 'must define a non-empty string el_image for every participant' "$test_directory/missing-execution.stderr"

if [ "$(wc -l < "$FAKE_KURTOSIS_LOG" | tr -d ' ')" != 2 ] || grep -Fvxq version "$FAKE_KURTOSIS_LOG"; then
	echo "Preparation used Kurtosis for more than its read-only version check:" >&2
	cat "$FAKE_KURTOSIS_LOG" >&2
	exit 1
fi
if grep -Eq 'kurtosis[[:space:]]+(enclave|service|run)' "$SCRIPT_DIR/prepare_local_testnet.sh"; then
	echo "Preparation contains a forbidden enclave, service, or package-run operation." >&2
	exit 1
fi
if ! grep -Fq '"$SCRIPT_DIR/prepare_local_testnet.sh"' "$SCRIPT_DIR/start_local_testnet.sh"; then
	echo "Network startup no longer delegates to the reusable preparation entrypoint." >&2
	exit 1
fi

echo "prepare_local_testnet boundary test: PASS"
