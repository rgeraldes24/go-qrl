# QRVM tool

The QRVM tool provides a few useful subcommands to facilitate testing at the QRVM
layer.

* transition tool    (`t8n`) : a stateless state transition utility
* transaction tool   (`t9n`) : a transaction validation utility
* block builder tool (`b11r`): a block assembler utility

## State transition tool (`t8n`)


The `qrvm t8n` tool is a stateless state transition utility. It is a utility
which can

1. Take a prestate, including
  - Accounts,
  - Block context information,
  - Previous blockshashes (*optional)
2. Apply a set of transactions,
3. Apply a mining-reward (*optional),
4. And generate a post-state, including
  - State root, transaction root, receipt root,
  - Information about rejected transactions,
  - Optionally: a full or partial post-state dump

### Specification

The idea is to specify the behaviour of this binary very _strict_, so that other
node implementors can build replicas based on their own state-machines, and the
state generators can swap between a \`geth\`-based implementation and a \`parityvm\`-based
implementation.

#### Command line params

Command line params that need to be supported are

```
    --input.alloc value            (default: "alloc.json")
    --input.env value              (default: "env.json")
    --input.txs value              (default: "txs.json")
    --output.alloc value           (default: "alloc.json")
    --output.basedir value        
    --output.body value           
    --output.result value          (default: "result.json")
    --state.chainid value          (default: 1)
    --state.reward value           (default: 0)
    --state.fork value             (default: "Zond")
    --trace.memory                 (default: false)
    --trace.nomemory               (default: true)
    --trace.noreturndata           (default: true)
    --trace.nostack                (default: false)
    --trace.returndata             (default: false)
```
#### Objects

The transition tool uses JSON objects to read and write data related to the transition operation. The
following object definitions are required.

##### `alloc`

The `alloc` object defines the prestate that transition will begin with.

```go
// Map of address to account definition.
type Alloc map[common.Address]Account
// Genesis account. Each field is optional.
type Account struct {
    Code    []byte                                `json:"code"`
    Storage map[common.Hash]common.StorageValue64 `json:"storage"`
    Balance *big.Int                              `json:"balance"`
    Nonce   uint64                                `json:"nonce"`
    Seed    []byte                                `json:"seed"`
}
```

##### `env`

The `env` object defines the environmental context in which the transition will
take place.

```go
type Env struct {
    // required
    CurrentCoinbase  common.Address      `json:"currentCoinbase"`
    CurrentGasLimit  uint64              `json:"currentGasLimit"`
    CurrentNumber    uint64              `json:"currentNumber"`
    CurrentTimestamp uint64              `json:"currentTimestamp"`
    Withdrawals      []*Withdrawal       `json:"withdrawals"`
    // optional
    CurrentRandom     *big.Int           `json:"currentRandom"`
    CurrentBaseFee    *big.Int           `json:"currentBaseFee"`
    ParentGasUsed     uint64             `json:"parentGasUsed"`
    ParentGasLimit    uint64             `json:"parentGasLimit"`
    ParentTimestamp   uint64             `json:"parentTimestamp"`
    BlockHashes       map[uint64]common.Hash `json:"blockHashes"`
}
type Withdrawal struct {
    Index          uint64         `json:"index"`
    ValidatorIndex uint64         `json:"validatorIndex"`
    Recipient      common.Address `json:"recipient"`
    Amount         *big.Int       `json:"amount"`
}
```

##### `txs`

The `txs` object is an array of any of the transaction types: `DynamicFeeTx`.

```go
type AccessList []AccessTuple
type AccessTuple struct {
	Address     common.Address `json:"address"        gencodec:"required"`
	StorageKeys []common.Hash  `json:"storageKeys"    gencodec:"required"`
}
type DynamicFeeTx struct {
	ChainID    *big.Int        `json:"chainId"`
	Nonce      uint64          `json:"nonce"`
	GasTipCap  *big.Int        `json:"maxPriorityFeePerGas"`
	GasFeeCap  *big.Int        `json:"maxFeePerGas"`
	Gas        uint64          `json:"gas"`
	To         *common.Address `json:"to"`
	Value      *big.Int        `json:"value"`
	Data       []byte          `json:"data"`
	AccessList AccessList      `json:"accessList"`
	PublicKey  *big.Int        `json:"publicKey"`
	Signature  *big.Int        `json:"signature"`
  Seed       *common.Hash    `json:"seed"`
}
```

##### `result`

The `result` object is output after a transition is executed. It includes
information about the post-transition environment.

```go
type ExecutionResult struct {
    StateRoot   common.Hash    `json:"stateRoot"`
    TxRoot      common.Hash    `json:"txRoot"`
    ReceiptRoot common.Hash    `json:"receiptsRoot"`
    LogsHash    common.Hash    `json:"logsHash"`
    Bloom       types.Bloom    `json:"logsBloom"`
    Receipts    types.Receipts `json:"receipts"`
    Rejected    []*rejectedTx  `json:"rejected,omitempty"`
    GasUsed     uint64         `json:"gasUsed"`
    BaseFee     *big.Int       `json:"currentBaseFee,omitempty"`
}
```

#### Error codes and output

All logging should happen against the `stderr`.
There are a few (not many) errors that can occur, those are defined below.

##### QRVM-based errors (`2` to `9`)

- Other QRVM error. Exit code `2`
- Failed configuration: when a non-supported or invalid fork was specified. Exit code `3`.
- Block history is not supplied, but needed for a `BLOCKHASH` operation. If `BLOCKHASH`
  is invoked targeting a block which history has not been provided for, the program will
  exit with code `4`.

##### IO errors (`10`-`20`)

- Invalid input json: the supplied data could not be marshalled.
  The program will exit with code `10`
- IO problems: failure to load or save files, the program will exit with code `11`

```
# This should exit with 3
./qrvm t8n --input.alloc=./testdata/1/alloc.json --input.txs=./testdata/1/txs.json --input.env=./testdata/1/env.json --state.fork=Zond 2>/dev/null
exitcode:3 OK
```
#### Forks
### Basic usage

The chain configuration to be used for a transition is specified via the
`--state.fork` CLI flag. A list of possible values and configurations can be
found in [`tests/init.go`](tests/init.go).

#### Examples
##### Basic usage

Invoking it with the provided example files
```
./qrvm t8n --input.alloc=./testdata/1/alloc.json --input.txs=./testdata/1/txs.json --input.env=./testdata/1/env.json --state.fork=Zond
```
Two resulting files:

`alloc.json`:
```json
{
  "Q000000000000000000000000000000000000000000000000000000000000000000000000000000000000000020687fa825ab4ad40a89c303f22f65fef9778555": {
    "balance": "0xebd44d22b000",
    "nonce": "0x1"
  },
  "Q00000000000000000000000000000000be6c1fd78f40b86a24dc2d7d633e2912d71e5d166f8be2c850d5727f0adcc170c7741b784295eae0c4f28291d0928dc7": {
    "balance": "0x5ffd4878be161d74",
    "nonce": "0xac"
  }
}
```
`result.json`:
```json
{
  "stateRoot": "0x9ea46a9c1f83e9309b94788db918c497b966bcd64a7bc1e1353411b90f21da90",
  "txRoot": "0xb59ce316b58ffc4c817b5765f6b3bc830c950c1bdce9dcd2898eb9bbc1c8de23",
  "receiptsRoot": "0xf78dfb743fbd92ade140711c8bbc542b5e307f0ab7984eff35d751969fe57efa",
  "logsHash": "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
  "logsBloom": "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
  "receipts": [
    {
      "type": "0x2",
      "root": "0x",
      "status": "0x1",
      "cumulativeGasUsed": "0x5208",
      "logsBloom": "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
      "logs": null,
      "transactionHash": "0x6973b7b3c04e2bd83821853ea3022a57604d903dd644f4a6289555a8c886c21d",
      "contractAddress": "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
      "gasUsed": "0x5208",
      "effectiveGasPrice": null,
      "blockHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
      "transactionIndex": "0x0"
    }
  ],
  "rejected": [
    {
      "index": 1,
      "error": "nonce too low: address Q000000000000000000000000000000000000000000000000000000000000000000000000000000000000000020687Fa825ab4AD40A89C303F22F65FEf9778555, tx: 0 state: 1"
    }
  ],
  "gasUsed": "0x5208",
  "currentBaseFee": "0x3b9aca00",
  "withdrawalsRoot": "0x56e81f171bcc55a6ff8345e692c0f86e5b48e01b996cadc001622fb5e363b421"
}
```

We can make them spit out the data to e.g. `stdout` like this:
```
./qrvm t8n --input.alloc=./testdata/1/alloc.json --input.txs=./testdata/1/txs.json --input.env=./testdata/1/env.json --output.result=stdout --output.alloc=stdout --state.fork=Zond
```
Output:
```json
{
  "alloc": {
    "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000008a8eafb1cf62bfbeb1741769dae1a9dd47996192": {
      "balance": "0xfeed1a9d",
      "nonce": "0x1"
    },
    "Q00000000000000000000000000000000be6c1fd78f40b86a24dc2d7d633e2912d71e5d166f8be2c850d5727f0adcc170c7741b784295eae0c4f28291d0928dc7": {
      "balance": "0x5ffd4878be161d74",
      "nonce": "0xac"
    },
    "Q0000000000000000000000000000000000000000000000000000000000000000000000000000000000000000c94f5374fce5edbc8e2a8697c15331677e6ebf0b": {
      "balance": "0xa410"
    }
  },
  "result": {
    "stateRoot": "0x84208a19bc2b46ada7445180c1db162be5b39b9abc8c0a54b05d32943eae4e13",
    "txRoot": "0xc4761fd7b87ff2364c7c60b6c5c8d02e522e815328aaea3f20e3b7b7ef52c42d",
    "receiptsRoot": "0x056b23fbba480696b65fe5a59b8f2148a1299103c4f57df839233af2cf4ca2d2",
    "logsHash": "0x1dcc4de8dec75d7aab85b567b6ccd41ad312451b948a7413f0a142fd40d49347",
    "logsBloom": "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
    "receipts": [
      {
        "root": "0x",
        "status": "0x1",
        "cumulativeGasUsed": "0x5208",
        "logsBloom": "0x00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
        "logs": null,
        "transactionHash": "0x0557bacce3375c98d806609b8d5043072f0b6a8bae45ae5a67a00d3a1a18d673",
        "contractAddress": "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000",
        "gasUsed": "0x5208",
        "blockHash": "0x0000000000000000000000000000000000000000000000000000000000000000",
        "transactionIndex": "0x0"
      }
    ],
    "rejected": [
      {
        "index": 1,
        "error": "nonce too low: address Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000008A8eAFb1cf62BfBeb1741769DAE1a9dd47996192, tx: 0 state: 1"
      }
    ],
    "gasUsed": "0x5208"
  }
}
```

#### Future QIPS

It is also possible to experiment with future qips that are not yet defined in a hard fork.
Example, putting QIP-1344 into Zond: 
```
./qrvm t8n --state.fork=Zond+1344 --input.pre=./testdata/1/pre.json --input.txs=./testdata/1/txs.json --input.env=/testdata/1/env.json
```

#### Block history

The `BLOCKHASH` opcode requires blockhashes to be provided by the caller, inside the `env`.
If a required blockhash is not provided, the exit code should be `4`:
Example where blockhashes are provided: 
```
./qrvm t8n --input.alloc=./testdata/3/alloc.json --input.txs=./testdata/3/txs.json --input.env=./testdata/3/env.json  --trace --state.fork=Zond

```

```
cat trace-0-0x72fadbef39cd251a437eea619cfeda752271a5faaaa2147df012e112159ffb81.jsonl | grep BLOCKHASH -C2
```
```
{"pc":0,"op":96,"gas":"0x5f58ef8","gasCost":"0x3","memSize":0,"stack":[],"depth":1,"refund":0,"opName":"PUSH1"}
{"pc":2,"op":64,"gas":"0x5f58ef5","gasCost":"0x14","memSize":0,"stack":["0x1"],"depth":1,"refund":0,"opName":"BLOCKHASH"}
{"pc":3,"op":0,"gas":"0x5f58ee1","gasCost":"0x0","memSize":0,"stack":["0xdac58aa524e50956d0c0bae7f3f8bb9d35381365d07804dd5b48a5a297c06af4"],"depth":1,"refund":0,"opName":"STOP"}
{"output":"","gasUsed":"0x17"}
```

In this example, the caller has not provided the required blockhash:
```
./qrvm t8n --input.alloc=./testdata/4/alloc.json --input.txs=./testdata/4/txs.json --input.env=./testdata/4/env.json --trace --state.fork=Zond
ERROR(4): getHash(3) invoked, blockhash for that block not provided
```
Error code: 4

#### Chaining

Another thing that can be done, is to chain invocations:
```
./qrvm t8n --input.alloc=./testdata/1/alloc.json --input.txs=./testdata/1/txs.json --input.env=./testdata/1/env.json --state.fork=Zond --output.alloc=stdout | ./qrvm t8n --input.alloc=stdin --input.env=./testdata/1/env.json --input.txs=./testdata/1/txs.json --state.fork=Zond

```
What happened here, is that we first applied two identical transactions, so the second one was rejected. 
Then, taking the poststate alloc as the input for the next state, we tried again to include
the same two transactions: this time, both failed due to too low nonce.

In order to meaningfully chain invocations, one would need to provide meaningful new `env`, otherwise the
actual blocknumber (exposed to the QRVM) would not increase.

#### Transactions in RLP form

It is possible to provide already-signed transactions as input to, using an `input.txs` which ends with the `rlp` suffix.
The input format for RLP-form transactions is _identical_ to the _output_ format for block bodies. Therefore, it's fully possible
to use the qrvm to go from `json` input to `rlp` input.

The following command takes **json** the transactions in `./testdata/13/txs.json` and signs them. After execution, they are output to `signed_txs.rlp`.:
```
./qrvm t8n --state.fork=Zond --input.alloc=./testdata/13/alloc.json --input.txs=./testdata/13/txs.json --input.env=./testdata/13/env.json --output.result=alloc_jsontx.json --output.body=signed_txs.rlp
INFO [08-29|20:19:29.728] Trie dumping started                     root=7fe86c..0f502d
INFO [08-29|20:19:29.728] Trie dumping complete                    accounts=3 elapsed="149.584µs"
INFO [08-29|20:19:29.729] Wrote file                               file=alloc.json
INFO [08-29|20:19:29.730] Wrote file                               file=alloc_jsontx.json
INFO [08-29|20:19:29.730] Wrote file                               file=signed_txs.rlp
```

The `output.body` is the rlp-list of transactions, encoded in hex and placed in a string a'la `json` encoding rules:
```
cat signed_txs.rlp
"0xf93926b91c9002f91c8c010180820fa08284d0b840111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111118080c08301000080b91213...<snip>..."
```

The output above is abbreviated; the transaction recipient is encoded as `b840` followed by a 64-byte address.

We can use `rlpdump` to check what the contents are:
```
rlpdump -hex $(cat signed_txs.rlp | jq -r )
[
  02f91c8c010180820fa08284d0b840111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111118080c08301000080b91213...<snip>,
  02f91c8c010280820fa08284d0b840111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111111118080c08301000080b91213...<snip>,
]
```
The dump above is abbreviated; `b840` is the 64-byte recipient string marker.

Now, we can now use those (or any other already signed transactions), as input, like so: 
```
./qrvm t8n --state.fork=Zond --input.alloc=./testdata/13/alloc.json --input.txs=./signed_txs.rlp --input.env=./testdata/13/env.json --output.result=alloc_rlptx.json
INFO [08-29|20:20:43.691] Trie dumping started                     root=7fe86c..0f502d
INFO [08-29|20:20:43.691] Trie dumping complete                    accounts=3 elapsed="142.292µs"
INFO [08-29|20:20:43.691] Wrote file                               file=alloc.json
INFO [08-29|20:20:43.691] Wrote file                               file=alloc_rlptx.json
```
You might have noticed that the results from these two invocations were stored in two separate files. 
And we can now finally check that they match.
```
cat alloc_jsontx.json | jq .stateRoot && cat alloc_rlptx.json | jq .stateRoot
"0x7fe86c15fc609c9e60ce82000d90d9e2bd57cc541abe691c3ebd888b4a0f502d"
"0x7fe86c15fc609c9e60ce82000d90d9e2bd57cc541abe691c3ebd888b4a0f502d"
```

## Transaction tool

The transaction tool is used to perform static validity checks on transactions such as:
* intrinsic gas calculation
* max values on integers
* fee semantics, such as `maxFeePerGas < maxPriorityFeePerGas`
* newer tx types on old forks

### Examples

```
./qrvm t9n --state.fork Zond --input.txs testdata/15/signed_txs.rlp
[
  {
    "address": "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000002014ae9f42335b44f94ee97d6248c0f55f0ee16e",
    "hash": "0x815bdb20d68fb0844a6efc5fa63ceecfdf1dbf920ab4f088bdcefbe33e402e52",
    "intrinsicGas": "0x5208"
  },
  {
    "address": "Q00000000000000000000000000000000000000000000000000000000000000000000000000000000000000002014ae9f42335b44f94ee97d6248c0f55f0ee16e",
    "hash": "0x4e91af0609b40a35c409645a201f7c07f5bba8c51f853356ba92f459ce8821e7",
    "intrinsicGas": "0x5208"
  }
]
```
## Block builder tool (b11r)

The `qrvm b11r` tool is used to assemble and seal full block rlps.

### Specification

#### Command line params

Command line params that need to be supported are:

```
    --input.header value        `stdin` or file name of where to find the block header to use. (default: "header.json")
    --input.txs value           `stdin` or file name of where to find the transactions list in RLP form. (default: "txs.rlp")
    --output.basedir value      Specifies where output files are placed. Will be created if it does not exist.
    --output.block value        Determines where to put the alloc of the post-state. (default: "block.json")
                                <file> - into the file <file>
                                `stdout` - into the stdout output
                                `stderr` - into the stderr output
    --verbosity value           Sets the verbosity level. (default: 3)
```

#### Objects

##### `header`

The `header` object is a consensus header.

```go=
type Header struct {
        ParentHash  common.Hash       `json:"parentHash"`
        Coinbase    *common.Address   `json:"miner"`
        Root        common.Hash       `json:"stateRoot"         gencodec:"required"`
        TxHash      *common.Hash      `json:"transactionsRoot"`
        ReceiptHash *common.Hash      `json:"receiptsRoot"`
        Bloom       types.Bloom       `json:"logsBloom"`
        Number      *big.Int          `json:"number"            gencodec:"required"`
        GasLimit    uint64            `json:"gasLimit"          gencodec:"required"`
        GasUsed     uint64            `json:"gasUsed"`
        Time        uint64            `json:"timestamp"         gencodec:"required"`
        Extra       []byte            `json:"extraData"`
        Random   common.Hash          `json:"prevRandao"`
        Nonce       *types.BlockNonce `json:"nonce"`
        BaseFee     *big.Int          `json:"baseFeePerGas"`
}
```
#### `txs`

The `txs` object is a list of RLP-encoded transactions in hex representation.

```go=
type Txs []string
```

#### `output`

The `output` object contains two values, the block RLP and the block hash.

```go=
type BlockInfo struct {
    Rlp  []byte      `json:"rlp"`
    Hash common.Hash `json:"hash"`
}
```

## A Note on Encoding

The encoding of values for `qrvm` utility attempts to be relatively flexible. It
generally supports hex-encoded or decimal-encoded numeric values, and
hex-encoded byte values such as `common.Address`, `common.Hash`, and
`common.StorageValue64`. VM64-specific widths are enforced where relevant:
addresses are 64 bytes, storage keys remain 32-byte hashes, and storage values
are 64 bytes.

## Testing

There are many test cases in the [`cmd/qrvm/testdata`](./testdata) directory.
These fixtures are used to power the `t8n` tests in
[`t8n_test.go`](./t8n_test.go). The best way to verify correctness of new `qrvm`
implementations is to execute these and verify the output and error codes match
the expected values.
