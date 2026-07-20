# The published generator image is retained as an immutable Python/filesystem
# base. Both its generator sources and its Qrysm binaries predate VM64, so
# rebuild the final image from exact commits for both repositories.
ARG GENESIS_GO_BUILDER_IMAGE=scratch
ARG GENESIS_BASE_IMAGE=scratch

FROM ${GENESIS_GO_BUILDER_IMAGE} AS qrysm-builder

ARG QRYSM_GIT_REPO
ARG QRYSM_GIT_COMMIT
ARG GENERATOR_GIT_REPO
ARG GENERATOR_GIT_COMMIT

COPY qrysm-deposit-runtime/main.go /tmp/qrysm-deposit-runtime/main.go

RUN test -n "${QRYSM_GIT_REPO}" && test -n "${QRYSM_GIT_COMMIT}" && \
    test -n "${GENERATOR_GIT_REPO}" && test -n "${GENERATOR_GIT_COMMIT}" && \
    printf '%s\n' "${QRYSM_GIT_COMMIT}" | grep -Eq '^[0-9a-f]{40}$' && \
    printf '%s\n' "${GENERATOR_GIT_COMMIT}" | grep -Eq '^[0-9a-f]{40}$' && \
    git init /src/qrysm && \
    git -C /src/qrysm remote add origin "${QRYSM_GIT_REPO}" && \
    git -C /src/qrysm fetch --depth 1 origin "${QRYSM_GIT_COMMIT}" && \
    git -C /src/qrysm checkout --detach FETCH_HEAD && \
    test "$(git -C /src/qrysm rev-parse HEAD)" = "${QRYSM_GIT_COMMIT}" && \
    git init /src/generator && \
    git -C /src/generator remote add origin "${GENERATOR_GIT_REPO}" && \
    git -C /src/generator fetch --depth 1 origin "${GENERATOR_GIT_COMMIT}" && \
    git -C /src/generator checkout --detach FETCH_HEAD && \
    test "$(git -C /src/generator rev-parse HEAD)" = "${GENERATOR_GIT_COMMIT}" && \
    cd /src/qrysm && \
    GOTOOLCHAIN=local go install -mod=readonly \
        ./cmd/qrysmctl \
        ./cmd/staking-deposit-cli/deposit && \
    GOTOOLCHAIN=local go run -mod=readonly -tags vm64_qrysm_runtime \
        /tmp/qrysm-deposit-runtime/main.go > /tmp/deposit-runtime.hex

FROM ${GENESIS_BASE_IMAGE}

ARG QRYSM_GIT_REPO
ARG QRYSM_GIT_COMMIT
ARG GENERATOR_GIT_REPO
ARG GENERATOR_GIT_COMMIT
ARG GENESIS_BASE_IMAGE

USER root

# The immutable base already contains the Python environment. Reuse it only
# when the pinned source commit has the identical dependency manifest, while
# replacing every generator-controlled source/configuration file.
COPY --from=qrysm-builder /src/generator/apps/el-gen/requirements.txt /tmp/vm64-requirements.txt
RUN cmp /apps/el-gen/requirements.txt /tmp/vm64-requirements.txt && \
    mv /apps/el-gen/.venv /tmp/el-gen-venv && \
    rm -rf /apps /defaults /config /work/entrypoint.sh && \
    mkdir -p /apps/el-gen && \
    mv /tmp/el-gen-venv /apps/el-gen/.venv && \
    rm /tmp/vm64-requirements.txt

COPY --from=qrysm-builder /src/generator/apps /apps
COPY --from=qrysm-builder /src/generator/defaults /defaults
COPY --from=qrysm-builder /src/generator/config-example /config
COPY --from=qrysm-builder /src/generator/entrypoint.sh /work/entrypoint.sh
COPY --from=qrysm-builder /go/bin/qrysmctl /usr/local/bin/qrysmctl
COPY --from=qrysm-builder /go/bin/deposit /usr/local/bin/deposit
COPY --from=qrysm-builder /tmp/deposit-runtime.hex /apps/el-gen/deposit-runtime.hex
COPY vm64_genesis_gqrl.py /apps/el-gen/vm64_genesis_gqrl.py

RUN mv /apps/el-gen/genesis_gqrl.py /apps/el-gen/genesis_gqrl_upstream.py && \
    mv /apps/el-gen/vm64_genesis_gqrl.py /apps/el-gen/genesis_gqrl.py && \
    /apps/el-gen/.venv/bin/pip check && \
    grep -q 'to_bytes(length=64' /apps/el-gen/genesis_gqrl_upstream.py && \
    grep -q '"baseFeePerGas": "0x3b9aca00"' /apps/el-gen/genesis_gqrl_upstream.py && \
    python3 /apps/el-gen/genesis_gqrl.py --self-test && \
    python3 /apps/el-gen/genesis_gqrl.py --manifest > /apps/el-gen/vm64-deposit-manifest.json && \
    qrysmctl --help >/dev/null && \
    deposit --help >/dev/null

LABEL org.opencontainers.image.source="${GENERATOR_GIT_REPO}" \
      org.opencontainers.image.revision="${GENERATOR_GIT_COMMIT}" \
      org.opencontainers.image.base.name="${GENESIS_BASE_IMAGE}" \
      io.theqrl.vm64.generator.source="${GENERATOR_GIT_REPO}" \
      io.theqrl.vm64.generator.revision="${GENERATOR_GIT_COMMIT}" \
      io.theqrl.vm64.qrysm.source="${QRYSM_GIT_REPO}" \
      io.theqrl.vm64.qrysm.revision="${QRYSM_GIT_COMMIT}" \
      io.theqrl.vm64.deposit-storage-layout="vm64-packed-bytes32-pairs-v1" \
      io.theqrl.vm64.genesis-generator="true"
