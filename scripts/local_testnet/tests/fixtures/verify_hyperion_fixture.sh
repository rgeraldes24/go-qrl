#!/usr/bin/env bash
# Copyright 2026 The go-qrl Authors
# This file is part of the go-qrl library.
#
# The go-qrl library is free software: you can redistribute it and/or modify
# it under the terms of the GNU Lesser General Public License as published by
# the Free Software Foundation, either version 3 of the License, or
# (at your option) any later version.
#
# The go-qrl library is distributed in the hope that it will be useful,
# but WITHOUT ANY WARRANTY; without even the implied warranty of
# MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
# GNU Lesser General Public License for more details.
#
# You should have received a copy of the GNU Lesser General Public License
# along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

# Rebuild the VM64 contract fixture with a content-addressed Hyperion toolchain
# and fail if any checked-in compiler or derived artifact differs.
set -Eeuo pipefail

readonly HYPERION_COMMIT="f2e6ae7a59e8dafc23a2f34164fdd26180cec2dd"
readonly HYPERION_SOURCE_URL="https://codeload.github.com/cyyber/hyperion/tar.gz/${HYPERION_COMMIT}"
readonly HYPERION_SOURCE_SHA256="d743cf3d6eb5482a425d9bf75bc9aa9778256b4ad3ac84e90373d3f7bbdb76a4"
# This is the Ubuntu 22.04 build image pinned by Hyperion's own CI at the
# compiler commit above. Its dependencies include the QRVM backend toolchain.
readonly HYPERION_BUILD_IMAGE="solbuildpackpusher/solidity-buildpack-deps@sha256:4df420b7ccd96f540a4300a4fae0fcac2f4d3f23ffff9e3777c1f2d7c37ef901"

SCRIPT_DIR=$(CDPATH='' cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
REPO_ROOT=$(CDPATH='' cd -- "${SCRIPT_DIR}/../../../.." && pwd -P)
readonly SCRIPT_DIR REPO_ROOT
readonly FIXTURE_SOURCE="scripts/local_testnet/tests/fixtures/EventEmitter.hyp"

if (( $# != 0 )); then
	printf 'usage: %s\n' "$0" >&2
	exit 2
fi

for tool in cmp diff go mktemp tar; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		printf 'fixture verification requires %s\n' "$tool" >&2
		exit 1
	fi
done

tmp_dir=$(mktemp -d "${TMPDIR:-/tmp}/go-qrl-hyperion-fixture.XXXXXX")
cleanup() {
	chmod -R u+w "$tmp_dir" 2>/dev/null || true
	rm -rf "$tmp_dir"
}
trap cleanup EXIT

compiled_dir="${tmp_dir}/compiled"
derived_dir="${tmp_dir}/derived"
mkdir -p "$compiled_dir" "$derived_dir"

sha256_file() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		printf 'fixture verification requires sha256sum or shasum\n' >&2
		return 1
	fi
}

verify_compiler_version() {
	local compiler=$1
	local version
	version=$("$compiler" --version)
	if [[ "$version" != *"commit.${HYPERION_COMMIT:0:8}"* ]]; then
		printf 'wrong Hyperion compiler revision; expected commit %s, got:\n%s\n' \
			"$HYPERION_COMMIT" "$version" >&2
		return 1
	fi
	printf '%s\n' "$version"
}

compile_with_local_hypc() {
	local compiler=$1
	if [[ "$compiler" != */* ]]; then
		compiler=$(command -v "$compiler")
	fi
	if [[ ! -x "$compiler" ]]; then
		printf 'HYPERION_FIXTURE_HYPC is not executable: %s\n' "$compiler" >&2
		return 1
	fi
	verify_compiler_version "$compiler"
	(
		cd "$REPO_ROOT"
		"$compiler" \
			--bin \
			--abi \
			--storage-layout \
			--no-cbor-metadata \
			-o "$compiled_dir" \
			--overwrite \
			"$FIXTURE_SOURCE"
	)
}

compile_with_pinned_container() {
	for tool in curl docker; do
		if ! command -v "$tool" >/dev/null 2>&1; then
			printf 'fixture compiler rebuild requires %s\n' "$tool" >&2
			return 1
		fi
	done

	local archive="${tmp_dir}/hyperion.tar.gz"
	local source_dir="${tmp_dir}/hyperion"
	local actual_sha
	curl --fail --location --retry 3 --proto '=https' --tlsv1.2 \
		--output "$archive" "$HYPERION_SOURCE_URL"
	actual_sha=$(sha256_file "$archive")
	if [[ "$actual_sha" != "$HYPERION_SOURCE_SHA256" ]]; then
		printf 'Hyperion source checksum = %s, want %s\n' \
			"$actual_sha" "$HYPERION_SOURCE_SHA256" >&2
		return 1
	fi
	mkdir -p "$source_dir"
	tar --extract --gzip --file "$archive" --strip-components=1 --directory "$source_dir"

	local build_jobs=${HYPERION_BUILD_JOBS:-2}
	if [[ ! "$build_jobs" =~ ^[1-9][0-9]*$ ]]; then
		printf 'HYPERION_BUILD_JOBS must be a positive integer, got %q\n' "$build_jobs" >&2
		return 1
	fi

	docker run --rm --platform linux/amd64 \
		--user "$(id -u):$(id -g)" \
		--env XDG_CACHE_HOME=/tmp/go-qrl-hyperion-cache \
		--env HYPERION_COMMIT="$HYPERION_COMMIT" \
		--env HYPERION_BUILD_JOBS="$build_jobs" \
		--env FIXTURE_SOURCE="$FIXTURE_SOURCE" \
		--mount "type=bind,src=${source_dir},dst=/hyperion" \
		--mount "type=bind,src=${REPO_ROOT},dst=/workspace,readonly" \
		--mount "type=bind,src=${compiled_dir},dst=/artifacts" \
		--workdir /workspace \
		"$HYPERION_BUILD_IMAGE" \
		bash -Eeuo pipefail -c '
			mkdir -p "$XDG_CACHE_HOME" /hyperion/build
			printf "%s" "$HYPERION_COMMIT" > /hyperion/commit_hash.txt
			printf "fixture" > /hyperion/prerelease.txt
			cmake \
				-S /hyperion \
				-B /hyperion/build \
				-G "Unix Makefiles" \
				-DCMAKE_BUILD_TYPE=Release
			cmake --build /hyperion/build \
				--target hypc \
				--parallel "$HYPERION_BUILD_JOBS"
			version=$(/hyperion/build/hypc/hypc --version)
			if [[ "$version" != *"commit.${HYPERION_COMMIT:0:8}"* ]]; then
				printf "built the wrong Hyperion revision:\n%s\n" "$version" >&2
				exit 1
			fi
			printf "%s\n" "$version"
			/hyperion/build/hypc/hypc \
				--bin \
				--abi \
				--storage-layout \
				--no-cbor-metadata \
				-o /artifacts \
				--overwrite \
				"$FIXTURE_SOURCE"
		'
}

compare_artifact() {
	local expected=$1
	local actual=$2
	local show_diff=${3:-false}
	if cmp --silent "$expected" "$actual"; then
		printf 'verified %s\n' "${expected#"$REPO_ROOT"/}"
		return 0
	fi
	printf 'fixture drift: %s\n' "${expected#"$REPO_ROOT"/}" >&2
	printf '  checked-in sha256: %s\n' "$(sha256_file "$expected")" >&2
	printf '  generated  sha256: %s\n' "$(sha256_file "$actual")" >&2
	if [[ "$show_diff" == true ]]; then
		diff --unified "$expected" "$actual" >&2 || true
	fi
	return 1
}

if [[ -n ${HYPERION_FIXTURE_HYPC:-} ]]; then
	compile_with_local_hypc "$HYPERION_FIXTURE_HYPC"
else
	compile_with_pinned_container
fi

status=0
compare_artifact "${SCRIPT_DIR}/EventEmitter.abi" "${compiled_dir}/EventEmitter.abi" true || status=1
compare_artifact "${SCRIPT_DIR}/EventEmitter.bin" "${compiled_dir}/EventEmitter.bin" || status=1
compare_artifact "${SCRIPT_DIR}/EventEmitter_storage.json" "${compiled_dir}/EventEmitter_storage.json" true || status=1

# Rebuild the two derived representations from the freshly compiled artifacts,
# never from the checked-in ABI/bin pair. This catches stale generated files even
# when they are mutually consistent with each other.
cp "${SCRIPT_DIR}/generate_emitter_js.go" "${derived_dir}/"
cp "${compiled_dir}/EventEmitter.abi" "${derived_dir}/"
cp "${compiled_dir}/EventEmitter.bin" "${derived_dir}/"
(
	cd "$derived_dir"
	go run generate_emitter_js.go
)
(
	cd "$REPO_ROOT"
	go run ./cmd/abigen \
		--abi "${compiled_dir}/EventEmitter.abi" \
		--bin "${compiled_dir}/EventEmitter.bin" \
		--pkg main \
		--type EventEmitter \
		--out "${derived_dir}/emitter_binding.go"
)

compare_artifact "${SCRIPT_DIR}/emitter.js" "${derived_dir}/emitter.js" true || status=1
compare_artifact "${REPO_ROOT}/scripts/local_testnet/goabi/emitter_binding.go" \
	"${derived_dir}/emitter_binding.go" true || status=1

if (( status != 0 )); then
	printf 'Hyperion fixture verification failed; regenerate all artifacts with the pinned compiler.\n' >&2
	exit "$status"
fi

(
	cd "$REPO_ROOT"
	go test -count=1 ./scripts/local_testnet/goabi
)
printf 'Hyperion fixture and generated artifacts match compiler %s.\n' "$HYPERION_COMMIT"
