# Simple Local Testnet

These scripts allow for running a small local testnet with a default of 2 gqrl execution clients, 2 beacon nodes, 2 validator clients, and a Clef remote signer using Kurtosis.
This setup can be useful for testing and development.

The execution client images are built locally from this repository. The beacon node, validator, and genesis generator use the published Qrysm images configured in `network_params.yaml` by default.

## Installation

1. Install [Docker](https://docs.docker.com/get-docker/). Verify that Docker has been successfully installed by running `sudo docker run hello-world`.

1. Install [Kurtosis](https://docs.kurtosis.com/install/). Verify that Kurtosis has been successfully installed by running `kurtosis version` which should display the version.

1. Install [yq](https://github.com/mikefarah/yq). If you are on Ubuntu, you can install `yq` by running `snap install yq`.

## Starting the testnet

To start a testnet, from the go-qrl root repository:
```bash
cd ./scripts/local_testnet
./start_local_testnet.sh
```

This first builds the `theqrl-dev/go-qrl:latest` and `theqrl-dev/go-qrl-alltools:latest` Docker images from the repository root, then starts the network. You will see a list of services running and "Started!" at the end.
Clef is initialized with an automatically approved development account funded in `network_params.yaml`, so console transactions can be submitted without interactive signer prompts.
You can also select your own go-qrl docker image to use by specifying it in `network_params.yaml` under the `el_image` key.
Full configuration reference for kurtosis is specified [here](https://github.com/theQRL/qrl-package?tab=readme-ov-file#configuration).

The network is orchestrated by a public fork based on [cyyber/qrl-package PR #13](https://github.com/cyyber/qrl-package/pull/13) and pinned to an exact commit via `QRL_PKG_VERSION` in `start_local_testnet.sh`. The pinned package adds the VM64 migrations and non-interactive Clef setup required by this network. Bump the pin deliberately when the package advances.

To test local Qrysm or genesis-generator changes, build development images from checkouts of `cyyber/qrysm` and [qrl-genesis-generator PR #9](https://github.com/cyyber/qrl-genesis-generator/pull/9):

```bash
./scripts/local_testnet/build_consensus_images.sh \
    /path/to/cyyber/qrysm \
    /path/to/qrl-genesis-generator-pr9
```

The script builds `local/qrysm-beacon:vm64`, `local/qrysm-validator:vm64`, and `local/qrl-genesis-generator:vm64`. Point the corresponding image fields in `network_params.yaml` at these local tags before starting the network.

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

Some testnet parameters can be varied by modifying the `network_params.yaml` file. Kurtosis also comes with a web UI which can be open with `kurtosis web`.

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

This dumps all service logs to `./logs` before destroying the enclave. You will see "Local testnet stopped." at the end.

## CLI options

The script comes with some CLI options, which can be viewed with `./start_local_testnet.sh -h`. One of the CLI options is to avoid rebuilding go-qrl each time the testnet starts, which can be configured with the command:

```bash
./start_local_testnet.sh -b false
```
