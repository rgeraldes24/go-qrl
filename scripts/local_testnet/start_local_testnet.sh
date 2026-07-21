#!/usr/bin/env bash

# Prepare all deterministic inputs, then start a network. This entrypoint never
# runs the E2E suites; tests against an existing network remain a separate
# command.

set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
ENCLAVE_NAME=local-testnet
NETWORK_PARAMS_FILE=$SCRIPT_DIR/network_params.yaml
BUILD_IMAGE=true
CI=false
KEEP_ENCLAVE=false

while getopts "e:b:n:hck" flag; do
	case "${flag}" in
		e) ENCLAVE_NAME=${OPTARG} ;;
		b) BUILD_IMAGE=${OPTARG} ;;
		n) NETWORK_PARAMS_FILE=${OPTARG} ;;
		c) CI=true ;;
		k) KEEP_ENCLAVE=true ;;
		h)
			echo "Start a local testnet with Kurtosis without running tests."
			echo
			echo "usage: $0 <Options>"
			echo
			echo "Options:"
			echo "   -e: enclave name                                default: $ENCLAVE_NAME"
			echo "   -b: whether to build go-qrl Docker images       default: $BUILD_IMAGE"
			echo "   -n: Kurtosis network params file path           default: $NETWORK_PARAMS_FILE"
			echo "   -c: CI mode, omit extra services and enforce immutable inputs"
			echo "   -k: retain an existing enclave instead of replacing it first"
			echo "   -h: this help"
			exit 0
			;;
		*)
			echo "Unknown option. Run $0 -h for help." >&2
			exit 1
			;;
	esac
done

temporary_directory=$(mktemp -d "${TMPDIR:-/tmp}/go-qrl-local-testnet.XXXXXX")
cleanup() {
	rm -rf "$temporary_directory"
}
trap cleanup EXIT

effective_params=${EFFECTIVE_PARAMS_OUTPUT:-}
preparation_output=${PREPARATION_OUTPUT:-}
if [ -z "$effective_params" ]; then
	if [ -n "$preparation_output" ]; then
		effective_params="$(dirname "$preparation_output")/network_params.effective.yaml"
	else
		effective_params="$temporary_directory/network_params.effective.yaml"
	fi
fi
if [ -z "$preparation_output" ]; then
	preparation_output="$temporary_directory/preparation.json"
fi

preparation_args=(-b "$BUILD_IMAGE" -n "$NETWORK_PARAMS_FILE")
if [ "$CI" = true ]; then
	preparation_args+=(-c)
fi
EFFECTIVE_PARAMS_OUTPUT=$effective_params \
	PREPARATION_OUTPUT=$preparation_output \
	"$SCRIPT_DIR/prepare_local_testnet.sh" "${preparation_args[@]}"

read_preparation() {
	yq eval -e -r "$1" "$preparation_output"
}

qrl_package_repo=$(read_preparation '.qrl_package.repository')
qrl_package_revision=$(read_preparation '.qrl_package.revision')
el_image=$(read_preparation '.images.execution.name')
alltools_image=$(read_preparation '.images.alltools.name')
cl_image=$(read_preparation '.images.consensus.name')
vc_image=$(read_preparation '.images.validator.name')
genesis_image=$(read_preparation '.images.genesis.name')

if [ "$KEEP_ENCLAVE" = false ]; then
	# Replace only the exact requested enclave. Ignore a genuinely absent enclave,
	# but fail closed if Kurtosis cannot remove one that exists; starting into
	# stale state would invalidate every subsequent result.
	if kurtosis enclave inspect "$ENCLAVE_NAME" >/dev/null 2>&1; then
		echo "Removing existing enclave $ENCLAVE_NAME."
		kurtosis enclave rm -f "$ENCLAVE_NAME"
		if kurtosis enclave inspect "$ENCLAVE_NAME" >/dev/null 2>&1; then
			echo "Enclave $ENCLAVE_NAME still exists after removal." >&2
			exit 1
		fi
	fi
fi

qrl_package_ref="$qrl_package_repo@$qrl_package_revision"
echo "Starting $qrl_package_ref in enclave $ENCLAVE_NAME."
kurtosis run --enclave "$ENCLAVE_NAME" "$qrl_package_ref" --args-file "$effective_params"

if [ "$CI" = true ]; then
	assert_service_image() {
		local service_name=$1
		local expected_image=$2
		local inspection
		local actual_image
		if ! inspection=$(kurtosis service inspect -o json "$ENCLAVE_NAME" "$service_name"); then
			echo "Could not inspect required service $service_name in enclave $ENCLAVE_NAME." >&2
			exit 1
		fi
		actual_image=$(yq eval -p=json -r '.image' - <<<"$inspection")
		if [ "$actual_image" != "$expected_image" ]; then
			echo "Service $service_name runs $actual_image, expected $expected_image." >&2
			exit 1
		fi
		echo "Verified deployed image: $service_name -> $actual_image"
	}

	# Verify the images actually wired into every long-running participant and
	# signer/key-generation helper after package execution.
	for service_spec in \
		"el-1-gqrl-qrysm|$el_image" \
		"el-2-gqrl-qrysm|$el_image" \
		"cl-1-qrysm-gqrl|$cl_image" \
		"cl-2-qrysm-gqrl|$cl_image" \
		"vc-1-gqrl-qrysm|$vc_image" \
		"vc-2-gqrl-qrysm|$vc_image" \
		"signer-clef|$alltools_image" \
		"clef-keystore-generation-el-clef-keystore|$alltools_image" \
		"validator-key-generation-cl-validator-keystore|$genesis_image"; do
		service_name=${service_spec%%|*}
		expected_image=${service_spec#*|}
		assert_service_image "$service_name" "$expected_image"
	done
fi

echo "Started!"
