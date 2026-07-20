# Simple Local Testnet

These scripts allow for running a small local testnet with a default of 2 gqrl execution clients, 2 beacon nodes, 2 validator clients, and a Clef remote signer using Kurtosis.
This setup can be useful for testing and development.

## Installation

1. Install [Docker](https://docs.docker.com/get-docker/). Verify that Docker has been successfully installed by running `sudo docker run hello-world`.

1. Install Kurtosis 1.20.0 from the [Kurtosis releases](https://github.com/kurtosis-tech/kurtosis-cli-release-artifacts/releases). Verify it with `kurtosis version`.

1. Install [yq v4](https://github.com/mikefarah/yq), Git, Python 3, Node.js, and the Go version declared in `go.mod`. Docker BuildKit and the buildx plugin are required when building the source-pinned Qrysm and genesis-generator images.

Reproducing every CI-only static gate also requires actionlint and ShellCheck.
The source-built execution, alltools, Qrysm, generator, and Hyperion toolchains
need several gigabytes of free Docker disk space.

## Starting the testnet

To start a testnet, from the go-qrl root repository:
```bash
cd ./scripts/local_testnet
./start_local_testnet.sh
```

When images are built, they are tagged with the checked-out Git commit and the
same commit is stored in each image's `commit` label. The script writes CI and
image overrides to a temporary copy of `network_params.yaml`; it never mutates
the checked-in file. The beacon-chain and validator binaries are built locally
from the exact Qrysm commit in `images.lock.env`, statically linked, and copied
over the binaries in digest-pinned runtime bases. The script verifies each
final image's source revision, role, and reported version before passing its
source-derived local tag to Kurtosis, so the legacy binaries in the base images
cannot be used as a fallback. The VM64 genesis generator is likewise built
locally from pinned generator and Qrysm commits on a digest-pinned base image;
both source revisions are stored in image labels and verified before use. Set
the corresponding image or source environment variable only when deliberately
testing a different input.

The pinned upstream generator still contains the pre-VM64 deposit allocation:
one `bytes32` zero-hash per 64-byte storage word in slots `0x22..0x40`.
`vm64_genesis_gqrl.py` fails unless it finds exactly that one stale allocation,
then installs constructor-equivalent Hyperion storage in the 16 packed slots
`0x11..0x20` (`odd zero-hash || even zero-hash`) and replaces the runtime with
bytecode extracted from the pinned Qrysm source. The image embeds a manifest
containing the runtime and canonical storage hashes. Its self-test requires the
standard empty deposit root `0xd70a...e5e` and rejects the uninitialized VM64
regression root `0x691a...c5c`.

You will see a list of services running and "Started!" at the end. To select
an existing go-qrl Docker image, start with `-b false` and either set
`EL_IMAGE` or set `el_image` in `network_params.yaml` without an image
override. Full configuration reference for Kurtosis is specified
[here](https://github.com/theQRL/qrl-package?tab=readme-ov-file#configuration).

To view all running services:

```bash
kurtosis enclave inspect local-testnet
```

To view the logs:

```bash
kurtosis service logs local-testnet $SERVICE_NAME
```

where `$SERVICE_NAME` is obtained by inspecting the running services above. For example, to view the logs of the first gqrl node, beacon node and validator client:

```bash
kurtosis service logs local-testnet -f el-1-gqrl-qrysm
kurtosis service logs local-testnet -f cl-1-qrysm-gqrl
kurtosis service logs local-testnet -f vc-1-gqrl-qrysm
```

If you would like to save the logs, use the command:

```bash
kurtosis dump $OUTPUT_DIRECTORY
```

This will create a folder named `$OUTPUT_DIRECTORY` in the present working directory that contains all logs and other information. If you want the logs for a particular service and saved to a file named `logs.txt`:

```bash
kurtosis service logs local-testnet $SERVICE_NAME -a > logs.txt
```
where `$SERVICE_NAME` can be viewed by running `kurtosis enclave inspect local-testnet`.

Some testnet parameters can be varied by modifying `network_params.yaml`.
Startup normally builds the checkout and injects `EL_IMAGE`; setting that
variable while builds are enabled chooses the tag for the newly built image.
To select an already-existing execution image, pass `-b false` and set
`EL_IMAGE`. The YAML `el_image` value is used only with `-b false` and no image
override. Kurtosis also provides a web UI through `kurtosis web`.

## Attaching a console

To attach a gqrl console to the first execution node:

```bash
./build/bin/gqrl attach "http://$(kurtosis port print local-testnet el-1-gqrl-qrysm rpc)"
```

The first account is managed by Clef and can submit transactions without an interactive approval prompt:

```js
qrl.accounts
qrl.sendTransaction({from: qrl.accounts[0], to: qrl.accounts[0], value: 1})
```

## Stopping the testnet

To stop the testnet, from the go-qrl root repository:

```bash
cd ./scripts/local_testnet
./stop_local_testnet.sh
```

This dumps all service logs to `./logs` before destroying the enclave. It
removes only the requested enclave and leaves the shared Kurtosis engine
running. If the dump fails, it preserves the enclave and exits unsuccessfully
so diagnostic state is not destroyed. An alternative dump directory can be
passed as the second argument:

```bash
./stop_local_testnet.sh local-testnet /tmp/local-testnet-dump
```

## Running the VM64 end-to-end suites

With the testnet running, execute:

```bash
./run_tests.sh
```

Strict mode rebuilds the host clients, requires HTTP, GraphQL, and WebSocket
coverage, and preserves each suite's output and command status:

```bash
./run_tests.sh -c -o ./logs/test-results
```

The complete `make local-testnet-e2e` target and the GitHub workflow run those
endpoint-dependent suites independently against both execution nodes. The EL1
run also performs the standalone Clef cryptographic/API suite; the EL2 run uses
`-C` to skip only that node-independent repetition. Topology Clef signing from
both execution nodes is still exercised by the subsequent `systemcheck` gate.

From the repository root, `make vm64-fixture-check` verifies the pinned
Hyperion compiler output before a network is started. With the network running,
`make local-testnet-e2e` runs the strict VM64 suites
against both execution nodes, followed by a real 64-byte validator deposit
through both EL and CL nodes, then the required two-node, topology-Clef,
non-empty access-list, finality, restart, and genuinely empty-datadir snap/full
synchronization checks. See
[`depositcheck`](depositcheck/README.md) for the deposit lifecycle assertions and
[`freshsync`](freshsync/README.md) for the temporary third EL/CL topology and
its cleanup behavior. The canonical target requires zero validator-duty failure
history, so a failure during an earlier endpoint or deposit phase cannot be
hidden by a later baseline. A standalone `systemcheck` invocation instead
baselines process-cumulative Qrysm counters and fails on every subsequent
increase; pass `-require-zero-duty-history` to opt into the canonical policy.
The automatic-withdrawal gate proves the exact recipient balance delta on both
execution nodes while the withdrawal block is fresh. After consensus finality
advances, it re-reads that block from both nodes and revalidates its hash,
withdrawal root, exact body, full-width recipient, and amount. This finalized
check deliberately does not require archive state, so the topology continues to
exercise ordinary pruning full nodes.

The deposit gate is one-shot. The safest complete local run uses a fresh
enclave and one keep-awake lifecycle that performs the compiler preflight,
starts the network, runs every live gate, writes a dump, and removes only that
enclave:

```bash
make local-testnet-e2e-from-scratch-awake \
  LOCAL_TESTNET_ENCLAVE=vm64-e2e \
  LOCAL_TESTNET_DUMP_DIR=/tmp/vm64-e2e-dump
```

The from-scratch runner refuses to replace an existing enclave, wraps the whole
lifecycle with macOS `caffeinate`, and performs the same steps directly on other
operating systems. Before validators start, it also runs
`local-testnet-host-preflight` so strict client rebuilds and all Go helpers use a
warm host cache rather than competing with five-second duties. Use that target
before manually provisioning a network too. `local-testnet-e2e-awake` remains
available when a network is already running, but strict zero-history evidence
begins when its validator processes start, so protect provisioning from sleep
too. Keep laptops on AC power when possible: suspending a five-second-slot
network makes already expired validator duties replay during wake-up and
invalidates timing-sensitive finality evidence. Caffeinate cannot prevent
lid-close or critical-battery sleep.

Keep the Hyperion fixture preflight outside the live-network window: its
compiler image is intentionally large and must not compete with the
timing-sensitive clients for Docker disk or CPU.

If startup used a `GENESIS_IMAGE` override, pass the same image reference (tag
or image ID) as `LOCAL_TESTNET_GENESIS_IMAGE`; the deposit checker resolves it
to an immutable image ID before validation. The Make target covers the runtime
and generated Hyperion fixture. The GitHub workflow additionally runs actionlint, pinned
ShellCheck, JavaScript syntax checks, generated-artifact drift checks, all Go
tests and vet, race-sensitive packages, and bounded native fuzzing.

The `VM64 end-to-end` GitHub Actions workflow runs this mode in an isolated,
run-specific enclave. It pulls digest-pinned base/toolchain images, builds
source-revision-tagged final images, and uploads the effective network
parameters, deposit manifest, per-suite results, image metadata, and Kurtosis
dump even when a suite fails. Digest updates are explicit reviewable changes
to `images.lock.env`; a same-source workflow rerun never follows a moved tag.

## CLI options

The script comes with some CLI options, which can be viewed with `./start_local_testnet.sh -h`. One of the CLI options is to avoid rebuilding go-qrl each time the testnet starts, which can be configured with the command:

```bash
./start_local_testnet.sh -b false
```
