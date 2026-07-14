// VM64 ABI suite, run through `gqrl attach --exec` by run_tests.sh. It
// exercises the embedded web3 ABI coder without relying on account management:
// getData builds calldata locally, and unpackOutput decodes synthetic return
// data using the same contract ABI path used by qrl.call results.

var _pass = 0;
var _fail = 0;

function check(desc, fn) {
    try {
        if (fn() === false) {
            throw new Error("assertion returned false");
        }
        _pass++;
        console.log("PASS: " + desc);
    } catch (e) {
        _fail++;
        console.log("FAIL: " + desc + " -- " + e);
    }
}

function zeros(n) {
    return new Array(n + 1).join("0");
}

function word(hex) {
    hex = hex.replace(/^0x/, "").toLowerCase();
    return zeros(128 - hex.length) + hex;
}

function fixedBytes(hex) {
    hex = hex.replace(/^0x/, "").toLowerCase();
    return hex + zeros(128 - hex.length);
}

function selector(signature) {
    return web3.sha3(signature).slice(2, 10);
}

function assertEqual(name, have, want) {
    if (have !== want) {
        throw new Error(name + ": have " + have + " want " + want);
    }
}

var abi = [{
    name: "store",
    type: "function",
    constant: false,
    inputs: [
        {name: "amount", type: "uint512"},
        {name: "tag", type: "bytes4"},
        {name: "recipient", type: "address"},
        {name: "payload", type: "bytes"}
    ],
    outputs: []
}, {
    name: "read",
    type: "function",
    constant: true,
    inputs: [],
    outputs: [
        {name: "amount", type: "uint512"},
        {name: "tag", type: "bytes4"},
        {name: "recipient", type: "address"},
        {name: "payload", type: "bytes"}
    ]
}, {
    name: "acceptBytes64",
    type: "function",
    constant: false,
    inputs: [{name: "value", type: "bytes64"}],
    outputs: []
}];

var miner = qrl.getBlock("latest").miner;
var minerHex = miner.slice(1).toLowerCase();
var contract = qrl.contract(abi).at(miner);

check("ABI calldata uses 64-byte VM words", function () {
    var data = contract.store.getData(web3.toBigNumber(1337), "0x01020304", miner, "0xabcd");
    var expected = "0x" +
        selector("store(uint512,bytes4,address,bytes)") +
        word("539") +
        fixedBytes("01020304") +
        minerHex +
        word("100") +
        word("2") +
        fixedBytes("abcd");

    assertEqual("calldata", data.toLowerCase(), expected);
    return true;
});

check("ABI bytesN values are left-aligned", function () {
    var value = "0x" + new Array(65).join("ab");
    var data = contract.acceptBytes64.getData(value);
    var expected = "0x" + selector("acceptBytes64(bytes64)") + value.slice(2);
    assertEqual("bytes64 calldata", data.toLowerCase(), expected);
    return true;
});

check("ABI output decoding consumes 64-byte dynamic offsets", function () {
    var encoded = "0x" +
        word("539") +
        fixedBytes("01020304") +
        minerHex +
        word("100") +
        word("2") +
        fixedBytes("abcd");
    var decoded = contract.read.unpackOutput(encoded);

    assertEqual("decoded amount", decoded[0].toString(10), "1337");
    assertEqual("decoded bytes4", decoded[1].toLowerCase(), "0x01020304");
    assertEqual("decoded address bytes", "Q" + decoded[2].slice(1).toLowerCase(), "Q" + minerHex);
    assertEqual("decoded payload", decoded[3].toLowerCase(), "0xabcd");
    return true;
});

check("ABI rejects malformed trailing partial words", function () {
    var encoded = "0x" + word("1") + "00";
    try {
        contract.read.unpackOutput(encoded);
    } catch (e) {
        return true;
    }
    throw new Error("expected unpackOutput to reject trailing partial word");
});

console.log("SUITE abi_vm64: " + (_fail === 0 ? "PASSED" : "FAILED") +
    " (" + _pass + "/" + (_pass + _fail) + " checks)");
