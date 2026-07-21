#!/usr/bin/env bash

# Build and attest local-testnet inputs without touching an enclave.
# Requires `docker`, `kurtosis`, and `yq`.

set -Eeuo pipefail

SCRIPT_DIR="$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )"
ROOT_DIR="$SCRIPT_DIR/../.."
# shellcheck source=images.lock.env
source "$SCRIPT_DIR/images.lock.env"
NETWORK_PARAMS_FILE=$SCRIPT_DIR/network_params.yaml
QRL_PACKAGE_REPO=${QRL_PACKAGE_REPO:-$PINNED_QRL_PACKAGE_REPO}
QRL_PKG_VERSION=${QRL_PKG_VERSION:-$PINNED_QRL_PACKAGE_COMMIT}
SOURCE_SHA=${SOURCE_SHA:-}
EL_IMAGE=${EL_IMAGE:-}
ALLTOOLS_IMAGE=${ALLTOOLS_IMAGE:-}
GENESIS_BASE_IMAGE=${GENESIS_BASE_IMAGE:-$PINNED_GENESIS_BASE_IMAGE}
GENESIS_GO_BUILDER_IMAGE=${GENESIS_GO_BUILDER_IMAGE:-$PINNED_GENESIS_GO_BUILDER_IMAGE}
QRYSM_GIT_REPO=${QRYSM_GIT_REPO:-$PINNED_QRYSM_GIT_REPO}
QRYSM_GIT_COMMIT=${QRYSM_GIT_COMMIT:-$PINNED_QRYSM_GIT_COMMIT}
QRYSM_GO_BUILDER_IMAGE=${QRYSM_GO_BUILDER_IMAGE:-$PINNED_QRYSM_GO_BUILDER_IMAGE}
CL_BASE_IMAGE=${CL_BASE_IMAGE:-$PINNED_CL_BASE_IMAGE}
VC_BASE_IMAGE=${VC_BASE_IMAGE:-$PINNED_VC_BASE_IMAGE}
CL_IMAGE=${CL_IMAGE:-theqrl-dev/qrysm-beacon:${QRYSM_GIT_COMMIT:0:12}}
VC_IMAGE=${VC_IMAGE:-theqrl-dev/qrysm-validator:${QRYSM_GIT_COMMIT:0:12}}
GENERATOR_GIT_REPO=${GENERATOR_GIT_REPO:-$PINNED_GENERATOR_GIT_REPO}
GENERATOR_GIT_COMMIT=${GENERATOR_GIT_COMMIT:-$PINNED_GENERATOR_GIT_COMMIT}
GENESIS_IMAGE=${GENESIS_IMAGE:-theqrl-dev/qrl-genesis-generator:${GENERATOR_GIT_COMMIT:0:12}-${QRYSM_GIT_COMMIT:0:12}}
GO_BUILDER_IMAGE=${GO_BUILDER_IMAGE:-$PINNED_GO_BUILDER_IMAGE}
ALPINE_RUNTIME_IMAGE=${ALPINE_RUNTIME_IMAGE:-$PINNED_ALPINE_RUNTIME_IMAGE}
EFFECTIVE_PARAMS_OUTPUT=${EFFECTIVE_PARAMS_OUTPUT:-}
PREPARATION_OUTPUT=${PREPARATION_OUTPUT:-}
BUILD_QRYSM_IMAGES=${BUILD_QRYSM_IMAGES:-true}
BUILD_GENESIS_IMAGE=${BUILD_GENESIS_IMAGE:-true}

BUILD_IMAGE=true
CI=false

# Get options
while getopts "e:b:n:hc" flag; do
  case "${flag}" in
    # Preparation is enclave-independent. Keep accepting -e so existing
    # callers can share their startup argument list without granting this
    # script any enclave lifecycle responsibility.
    e) :;;
    b) BUILD_IMAGE=${OPTARG};;
    n) NETWORK_PARAMS_FILE=${OPTARG};;
    c) CI=true;;
    h)
        echo "Prepare and attest local-testnet images and package inputs without touching an enclave."
        echo
        echo "usage: $0 <Options>"
        echo
        echo "Options:"
        echo "   -e: accepted for startup compatibility; ignored during preparation"
        echo "   -b: build go-qrl images; false selects one consistent EL and remote-signer image from params unless overridden"
        echo "   -n: kurtosis network params file path           default: $NETWORK_PARAMS_FILE"
        echo "   -c: CI mode, omit additional services and enforce immutable inputs"
        echo "   -h: this help"
        exit
        ;;
    *)
        echo "Unknown option. Run $0 -h for help." >&2
        exit 1
        ;;
  esac
done

if ! command -v docker &> /dev/null; then
    echo "Docker is not installed. Please install Docker and try again."
    exit 1
fi

if ! command -v kurtosis &> /dev/null; then
    echo "kurtosis command not found. Please install kurtosis and try again."
    exit 1
fi

if ! command -v yq &> /dev/null; then
    echo "yq not found. Please install yq and try again."
    exit 1
fi
if ! yq --version | grep -Eq 'version v?4\.'; then
    echo "Mike Farah yq version 4 is required." >&2
    exit 1
fi
if [ -z "$EFFECTIVE_PARAMS_OUTPUT" ] || [ -z "$PREPARATION_OUTPUT" ]; then
    echo "EFFECTIVE_PARAMS_OUTPUT and PREPARATION_OUTPUT are required." >&2
    exit 1
fi

if [ ! -f "$NETWORK_PARAMS_FILE" ]; then
    echo "Network params file not found: $NETWORK_PARAMS_FILE" >&2
    exit 1
fi

resolve_consistent_participant_image() {
    local field=$1
    local override_name=$2
    local participant_count
    local image_values
    local image
    local resolved=
    local resolved_count=0

    if ! participant_count=$(yq eval '.participants | length' "$NETWORK_PARAMS_FILE"); then
        echo "Could not count participants while resolving $field from $NETWORK_PARAMS_FILE." >&2
        return 1
    fi
    if [[ ! "$participant_count" =~ ^[1-9][0-9]*$ ]]; then
        echo "Network params must contain at least one participant to resolve $field when BUILD_IMAGE=false and $override_name is unset." >&2
        return 1
    fi
    if ! image_values=$(yq eval -r ".participants[].${field} | select(tag == \"!!str\")" "$NETWORK_PARAMS_FILE"); then
        echo "Could not read $field from $NETWORK_PARAMS_FILE." >&2
        return 1
    fi
    while IFS= read -r image; do
        resolved_count=$((resolved_count + 1))
        if [ -z "$image" ]; then
            echo "Network params contain an empty $field; every participant must provide one when BUILD_IMAGE=false and $override_name is unset." >&2
            return 1
        fi
        if [ -z "$resolved" ]; then
            resolved=$image
        elif [ "$resolved" != "$image" ]; then
            echo "Network params contain mixed $field values; set one consistent value or override $override_name explicitly." >&2
            return 1
        fi
    done <<< "$image_values"
    if [ "$resolved_count" -ne "$participant_count" ] || [ -z "$resolved" ]; then
        echo "Network params must define a non-empty string $field for every participant when BUILD_IMAGE=false and $override_name is unset." >&2
        return 1
    fi
    printf '%s\n' "$resolved"
}

if [ "$BUILD_IMAGE" = false ]; then
    if [ -z "$EL_IMAGE" ]; then
        EL_IMAGE=$(resolve_consistent_participant_image el_image EL_IMAGE)
    fi
    if [ -z "$ALLTOOLS_IMAGE" ]; then
        ALLTOOLS_IMAGE=$(resolve_consistent_participant_image remote_signer_image ALLTOOLS_IMAGE)
    fi
fi

if [ "$CI" = true ]; then
    if [[ ! "$QRL_PKG_VERSION" =~ ^[0-9a-f]{40}$ ]]; then
        echo "CI mode requires QRL_PKG_VERSION to be an exact 40-character commit." >&2
        exit 1
    fi
    for commit_var in QRYSM_GIT_COMMIT GENERATOR_GIT_COMMIT; do
        commit=${!commit_var}
        if [[ ! "$commit" =~ ^[0-9a-f]{40}$ ]]; then
            echo "CI mode requires $commit_var to be an exact 40-character commit." >&2
            exit 1
        fi
    done
    for build_var in BUILD_IMAGE BUILD_QRYSM_IMAGES BUILD_GENESIS_IMAGE; do
        if [ "${!build_var}" != true ]; then
            echo "CI mode requires $build_var=true." >&2
            exit 1
        fi
    done
    EXPECTED_CL_IMAGE="theqrl-dev/qrysm-beacon:${QRYSM_GIT_COMMIT:0:12}"
    EXPECTED_VC_IMAGE="theqrl-dev/qrysm-validator:${QRYSM_GIT_COMMIT:0:12}"
    if [ "$CL_IMAGE" != "$EXPECTED_CL_IMAGE" ] || [ "$VC_IMAGE" != "$EXPECTED_VC_IMAGE" ]; then
        echo "CI mode requires exact source-derived Qrysm image tags: $EXPECTED_CL_IMAGE and $EXPECTED_VC_IMAGE." >&2
        exit 1
    fi
    for image_var in CL_BASE_IMAGE VC_BASE_IMAGE QRYSM_GO_BUILDER_IMAGE GENESIS_BASE_IMAGE GENESIS_GO_BUILDER_IMAGE GO_BUILDER_IMAGE ALPINE_RUNTIME_IMAGE; do
        image=${!image_var}
        if [[ "$image" != *@sha256:* ]]; then
            echo "CI mode requires $image_var to be digest-pinned, got: $image" >&2
            exit 1
        fi
    done
fi

if [ "$BUILD_QRYSM_IMAGES" = true ]; then
    echo "Building VM64 Qrysm beacon-chain and validator from pinned source revision."
    docker build \
        --target beacon \
        --build-arg "QRYSM_GO_BUILDER_IMAGE=$QRYSM_GO_BUILDER_IMAGE" \
        --build-arg "QRYSM_CL_BASE_IMAGE=$CL_BASE_IMAGE" \
        --build-arg "QRYSM_VC_BASE_IMAGE=$VC_BASE_IMAGE" \
        --build-arg "QRYSM_GIT_REPO=$QRYSM_GIT_REPO" \
        --build-arg "QRYSM_GIT_COMMIT=$QRYSM_GIT_COMMIT" \
        -f "$SCRIPT_DIR/Dockerfile.qrysm-consensus" \
        -t "$CL_IMAGE" "$SCRIPT_DIR"
    docker build \
        --target validator \
        --build-arg "QRYSM_GO_BUILDER_IMAGE=$QRYSM_GO_BUILDER_IMAGE" \
        --build-arg "QRYSM_CL_BASE_IMAGE=$CL_BASE_IMAGE" \
        --build-arg "QRYSM_VC_BASE_IMAGE=$VC_BASE_IMAGE" \
        --build-arg "QRYSM_GIT_REPO=$QRYSM_GIT_REPO" \
        --build-arg "QRYSM_GIT_COMMIT=$QRYSM_GIT_COMMIT" \
        -f "$SCRIPT_DIR/Dockerfile.qrysm-consensus" \
        -t "$VC_IMAGE" "$SCRIPT_DIR"

    for image_and_role in "$CL_IMAGE:beacon-chain" "$VC_IMAGE:validator"; do
        image=${image_and_role%:*}
        expected_role=${image_and_role##*:}
        image_commit=$(docker image inspect \
            --format '{{index .Config.Labels "io.theqrl.vm64.qrysm.revision"}}' "$image")
        image_role=$(docker image inspect \
            --format '{{index .Config.Labels "io.theqrl.vm64.qrysm.role"}}' "$image")
        if [ "$image_commit" != "$QRYSM_GIT_COMMIT" ]; then
            echo "Qrysm image $image has revision $image_commit, expected $QRYSM_GIT_COMMIT." >&2
            exit 1
        fi
        if [ "$image_role" != "$expected_role" ]; then
            echo "Qrysm image $image has role $image_role, expected $expected_role." >&2
            exit 1
        fi
        image_version=$(docker run --rm "$image" --version)
        if [[ "$image_version" != *"$QRYSM_GIT_COMMIT"* ]]; then
            echo "Qrysm image $image version does not contain $QRYSM_GIT_COMMIT: $image_version" >&2
            exit 1
        fi
    done
else
    echo "Not rebuilding VM64 Qrysm beacon-chain and validator images."
fi

if [ "$BUILD_GENESIS_IMAGE" = true ]; then
    echo "Building VM64 genesis-generator from pinned generator and Qrysm revisions."
    docker build \
        --build-arg "GENESIS_BASE_IMAGE=$GENESIS_BASE_IMAGE" \
        --build-arg "GENESIS_GO_BUILDER_IMAGE=$GENESIS_GO_BUILDER_IMAGE" \
        --build-arg "QRYSM_GIT_REPO=$QRYSM_GIT_REPO" \
        --build-arg "QRYSM_GIT_COMMIT=$QRYSM_GIT_COMMIT" \
        --build-arg "GENERATOR_GIT_REPO=$GENERATOR_GIT_REPO" \
        --build-arg "GENERATOR_GIT_COMMIT=$GENERATOR_GIT_COMMIT" \
        -f "$SCRIPT_DIR/Dockerfile.genesis-generator" \
        -t "$GENESIS_IMAGE" "$SCRIPT_DIR"
    IMAGE_QRYSM_COMMIT=$(docker image inspect \
        --format '{{index .Config.Labels "io.theqrl.vm64.qrysm.revision"}}' "$GENESIS_IMAGE")
    IMAGE_GENERATOR_COMMIT=$(docker image inspect \
        --format '{{index .Config.Labels "io.theqrl.vm64.generator.revision"}}' "$GENESIS_IMAGE")
    IMAGE_DEPOSIT_STORAGE_LAYOUT=$(docker image inspect \
        --format '{{index .Config.Labels "io.theqrl.vm64.deposit-storage-layout"}}' "$GENESIS_IMAGE")
    if [ "$IMAGE_QRYSM_COMMIT" != "$QRYSM_GIT_COMMIT" ]; then
        echo "Generator image has Qrysm revision $IMAGE_QRYSM_COMMIT, expected $QRYSM_GIT_COMMIT." >&2
        exit 1
    fi
    if [ "$IMAGE_GENERATOR_COMMIT" != "$GENERATOR_GIT_COMMIT" ]; then
        echo "Generator image has source revision $IMAGE_GENERATOR_COMMIT, expected $GENERATOR_GIT_COMMIT." >&2
        exit 1
    fi
    if [ "$IMAGE_DEPOSIT_STORAGE_LAYOUT" != "vm64-packed-bytes32-pairs-v1" ]; then
        echo "Generator image has deposit storage layout $IMAGE_DEPOSIT_STORAGE_LAYOUT, expected vm64-packed-bytes32-pairs-v1." >&2
        exit 1
    fi
    docker run --rm --pull=never --network=none \
        --entrypoint python3 "$GENESIS_IMAGE" \
        /apps/el-gen/genesis_gqrl.py --self-test
    docker run --rm --pull=never --network=none \
        --entrypoint cat "$GENESIS_IMAGE" \
        /apps/el-gen/vm64-deposit-manifest.json | \
        yq eval -p=json -e \
            '.schema == 1 and .storage_layout == "vm64-packed-bytes32-pairs-v1" and .empty_deposit_root == "0xd70a234731285c6804c2a4f56711ddb8c82c99740f207854891028af34e27e5e"' \
            >/dev/null
else
    echo "Not rebuilding VM64 genesis-generator image."
fi

RUNTIME_PARAMS_FILE=$(mktemp "${TMPDIR:-/tmp}/go-qrl-network-params.XXXXXX.yaml")
cleanup() {
    rm -f "$RUNTIME_PARAMS_FILE"
}
trap cleanup EXIT
cp "$NETWORK_PARAMS_FILE" "$RUNTIME_PARAMS_FILE"

if [ "$CI" = true ]; then
    yq eval '.additional_services = []' -i "$RUNTIME_PARAMS_FILE"
    echo "Running without additional services (CI mode)."
fi

CHECKED_OUT_SHA=$(git -C "$ROOT_DIR" rev-parse HEAD)
if [ -z "$SOURCE_SHA" ]; then
    SOURCE_SHA=$CHECKED_OUT_SHA
elif [ "$SOURCE_SHA" != "$CHECKED_OUT_SHA" ]; then
    echo "SOURCE_SHA $SOURCE_SHA does not match checked-out commit $CHECKED_OUT_SHA." >&2
    exit 1
fi
WORKTREE_DIRTY=false
if [ -n "$(git -C "$ROOT_DIR" status --porcelain --untracked-files=all)" ]; then
    WORKTREE_DIRTY=true
fi

if [ "$BUILD_IMAGE" = true ]; then
    if [ "$CI" = true ] && [ "$WORKTREE_DIRTY" = true ]; then
        echo "CI image builds require a clean worktree, including no untracked Docker-context files." >&2
        exit 1
    fi
    EL_IMAGE=${EL_IMAGE:-theqrl-dev/go-qrl:${SOURCE_SHA:0:12}}
    ALLTOOLS_IMAGE=${ALLTOOLS_IMAGE:-theqrl-dev/go-qrl-alltools:${SOURCE_SHA:0:12}}
    echo "Building go-qrl Docker images."
    # gqrl (execution client)
    docker build --build-arg "COMMIT=$SOURCE_SHA" \
        --build-arg "GO_BUILDER_IMAGE=$GO_BUILDER_IMAGE" \
        --build-arg "ALPINE_RUNTIME_IMAGE=$ALPINE_RUNTIME_IMAGE" \
        -f "$ROOT_DIR/Dockerfile" \
        -t "$EL_IMAGE" "$ROOT_DIR"
    # alltools (clef et al., used when testing the clef remote signer)
    docker build --build-arg "COMMIT=$SOURCE_SHA" \
        --build-arg "GO_BUILDER_IMAGE=$GO_BUILDER_IMAGE" \
        --build-arg "ALPINE_RUNTIME_IMAGE=$ALPINE_RUNTIME_IMAGE" \
        -f "$ROOT_DIR/Dockerfile.alltools" \
        -t "$ALLTOOLS_IMAGE" "$ROOT_DIR"
    for image in "$EL_IMAGE" "$ALLTOOLS_IMAGE"; do
        IMAGE_COMMIT=$(docker image inspect --format '{{index .Config.Labels "commit"}}' "$image")
        if [ "$IMAGE_COMMIT" != "$SOURCE_SHA" ]; then
            echo "Image $image has commit label $IMAGE_COMMIT, expected $SOURCE_SHA." >&2
            exit 1
        fi
    done
else
    echo "Not rebuilding go-qrl Docker images."
fi

if [ -n "$EL_IMAGE" ]; then
    EL_IMAGE="$EL_IMAGE" yq eval '.participants[].el_image = strenv(EL_IMAGE)' -i "$RUNTIME_PARAMS_FILE"
fi
if [ -n "$ALLTOOLS_IMAGE" ]; then
    ALLTOOLS_IMAGE="$ALLTOOLS_IMAGE" yq eval \
        '.participants[].remote_signer_image = strenv(ALLTOOLS_IMAGE)' -i "$RUNTIME_PARAMS_FILE"
fi
if [ -n "$CL_IMAGE" ]; then
    CL_IMAGE="$CL_IMAGE" yq eval '.participants[].cl_image = strenv(CL_IMAGE)' -i "$RUNTIME_PARAMS_FILE"
fi
if [ -n "$VC_IMAGE" ]; then
    VC_IMAGE="$VC_IMAGE" yq eval '.participants[].vc_image = strenv(VC_IMAGE)' -i "$RUNTIME_PARAMS_FILE"
fi
if [ -n "$GENESIS_IMAGE" ]; then
    GENESIS_IMAGE="$GENESIS_IMAGE" yq eval \
        '.qrl_genesis_generator_params.image = strenv(GENESIS_IMAGE)' -i "$RUNTIME_PARAMS_FILE"
fi

mkdir -p "$(dirname "$EFFECTIVE_PARAMS_OUTPUT")"
cp "$RUNTIME_PARAMS_FILE" "$EFFECTIVE_PARAMS_OUTPUT"

for required_image in "$EL_IMAGE" "$ALLTOOLS_IMAGE" "$CL_IMAGE" "$VC_IMAGE" "$GENESIS_IMAGE"; do
    if [ -z "$required_image" ]; then
        echo "Preparation metadata requires explicit resolved images when a build is disabled." >&2
        exit 1
    fi
done
mkdir -p "$(dirname "$PREPARATION_OUTPUT")"
EFFECTIVE_PARAMS_PATH=$EFFECTIVE_PARAMS_OUTPUT
EFFECTIVE_PARAMS_SHA256=$(openssl dgst -sha256 "$EFFECTIVE_PARAMS_PATH" | awk '{print $NF}')
EL_IMAGE_ID=$(docker image inspect --format '{{.Id}}' "$EL_IMAGE")
ALLTOOLS_IMAGE_ID=$(docker image inspect --format '{{.Id}}' "$ALLTOOLS_IMAGE")
CL_IMAGE_ID=$(docker image inspect --format '{{.Id}}' "$CL_IMAGE")
VC_IMAGE_ID=$(docker image inspect --format '{{.Id}}' "$VC_IMAGE")
GENESIS_IMAGE_ID=$(docker image inspect --format '{{.Id}}' "$GENESIS_IMAGE")
DOCKER_VERSION=$(docker --version)
KURTOSIS_VERSION_OUTPUT=$(kurtosis version)
YQ_VERSION_OUTPUT=$(yq --version)
GO_VERSION_OUTPUT=$(go version)
export SOURCE_SHA WORKTREE_DIRTY QRL_PACKAGE_REPO QRL_PKG_VERSION QRYSM_GIT_REPO QRYSM_GIT_COMMIT
export GENERATOR_GIT_REPO GENERATOR_GIT_COMMIT EFFECTIVE_PARAMS_PATH EFFECTIVE_PARAMS_SHA256
export EL_IMAGE EL_IMAGE_ID ALLTOOLS_IMAGE ALLTOOLS_IMAGE_ID CL_IMAGE CL_IMAGE_ID
export VC_IMAGE VC_IMAGE_ID GENESIS_IMAGE GENESIS_IMAGE_ID BUILD_IMAGE BUILD_QRYSM_IMAGES BUILD_GENESIS_IMAGE
export DOCKER_VERSION KURTOSIS_VERSION_OUTPUT YQ_VERSION_OUTPUT GO_VERSION_OUTPUT
yq -n -o=json '{
      "schema": 1,
      "source_sha": strenv(SOURCE_SHA),
      "worktree_dirty": (strenv(WORKTREE_DIRTY) == "true"),
      "qrl_package": {"repository": strenv(QRL_PACKAGE_REPO), "revision": strenv(QRL_PKG_VERSION)},
      "qrysm": {"repository": strenv(QRYSM_GIT_REPO), "revision": strenv(QRYSM_GIT_COMMIT)},
      "generator": {"repository": strenv(GENERATOR_GIT_REPO), "revision": strenv(GENERATOR_GIT_COMMIT)},
      "effective_params": {"path": strenv(EFFECTIVE_PARAMS_PATH), "sha256": strenv(EFFECTIVE_PARAMS_SHA256)},
      "images": {
        "execution": {"name": strenv(EL_IMAGE), "id": strenv(EL_IMAGE_ID)},
        "alltools": {"name": strenv(ALLTOOLS_IMAGE), "id": strenv(ALLTOOLS_IMAGE_ID)},
        "consensus": {"name": strenv(CL_IMAGE), "id": strenv(CL_IMAGE_ID)},
        "validator": {"name": strenv(VC_IMAGE), "id": strenv(VC_IMAGE_ID)},
        "genesis": {"name": strenv(GENESIS_IMAGE), "id": strenv(GENESIS_IMAGE_ID)}
      },
      "build": {
        "execution": (strenv(BUILD_IMAGE) == "true"),
        "qrysm": (strenv(BUILD_QRYSM_IMAGES) == "true"),
        "genesis": (strenv(BUILD_GENESIS_IMAGE) == "true")
      },
      "versions": {
        "docker": strenv(DOCKER_VERSION),
        "kurtosis": strenv(KURTOSIS_VERSION_OUTPUT),
        "yq": strenv(YQ_VERSION_OUTPUT),
        "go": strenv(GO_VERSION_OUTPUT)
      }
}' > "$PREPARATION_OUTPUT"
echo "Wrote local-testnet preparation metadata to $PREPARATION_OUTPUT."
echo "Local-testnet preparation completed without creating, inspecting, or removing an enclave."
