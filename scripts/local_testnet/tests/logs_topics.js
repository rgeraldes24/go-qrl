// VM64 log topic input suite, run through `gqrl attach --exec` by
// run_tests.sh. Raw qrl.getLogs topic filters are intentionally not ABI-aware:
// callers must pass full 64-byte topic hex. Higher-level contract event
// filters handle ABI signature-topic formatting.
//
// The outgoing request is captured by temporarily wrapping the console's
// provider, the same trick web3ext_test.go uses with a fake provider.

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

var realProvider = web3.currentProvider;
var captured = null;
var spy = {
    send: function (payload) {
        if (payload.method === "qrl_getLogs") {
            captured = payload.params[0];
        }
        return realProvider.send(payload);
    },
    sendAsync: function (payload, callback) {
        if (payload.method === "qrl_getLogs") {
            captured = payload.params[0];
        }
        return realProvider.sendAsync(payload, callback);
    },
    isConnected: function () {
        return true;
    }
};

var hash = web3.sha3("Deployed(uint256)").slice(2); // 64 hex chars, a Keccak hash
var wide = new Array(65).join("ab");                 // 128 hex chars
var fullTopic = "0x" + wide;
var signatureTopic = "0x" + hash + zeros(64);        // hash||zeros

try {
    web3.setProvider(spy);

    check("qrl.getLogs accepts full 64-byte topics and wildcards", function () {
        captured = null;
        var logs = qrl.getLogs({
            fromBlock: "0x0",
            toBlock: "latest",
            topics: [fullTopic, null, [signatureTopic, fullTopic]]
        });
        if (!(logs instanceof Array)) {
            throw new Error("qrl.getLogs did not return an array");
        }
        if (captured === null) {
            throw new Error("provider spy captured no qrl_getLogs request");
        }
        return true;
    });

    check("full topics pass through verbatim", function () {
        if (captured.topics[0] !== fullTopic) {
            throw new Error("have " + captured.topics[0] + " want " + fullTopic);
        }
        return true;
    });

    check("null topics pass through as wildcards", function () {
        return captured.topics[1] === null;
    });

    check("OR topic lists preserve each full topic", function () {
        var or = captured.topics[2];
        if (!(or instanceof Array) || or.length !== 2) {
            throw new Error("unexpected OR topics: " + JSON.stringify(or));
        }
        if (or[0] !== signatureTopic || or[1] !== fullTopic) {
            throw new Error("unexpected OR topics: " + JSON.stringify(or));
        }
        return true;
    });

    check("raw 32-byte hash topics are rejected unless expanded to VM64", function () {
        captured = null;
        try {
            qrl.getLogs({fromBlock: "0x0", toBlock: "latest", topics: ["0x" + hash]});
        } catch (e) {
            if (captured.topics[0] !== "0x" + hash) {
                throw new Error("provider did not pass raw hash through: " + captured.topics[0]);
            }
            return true;
        }
        throw new Error("expected an error");
    });

    check("short hex topics are rejected", function () {
        captured = null;
        try {
            qrl.getLogs({fromBlock: "0x0", toBlock: "latest", topics: ["0xbb"]});
        } catch (e) {
            if (captured.topics[0] !== "0xbb") {
                throw new Error("provider did not pass short topic through: " + captured.topics[0]);
            }
            return true;
        }
        throw new Error("expected an error");
    });

    check("over-wide topics are rejected", function () {
        try {
            qrl.getLogs({fromBlock: "0x0", toBlock: "latest", topics: ["0x" + zeros(130)]});
        } catch (e) {
            return true;
        }
        throw new Error("expected an error");
    });

    check("plain string topics are converted to bytes and rejected as short", function () {
        captured = null;
        try {
            qrl.getLogs({fromBlock: "0x0", toBlock: "latest", topics: ["hello"]});
        } catch (e) {
            if (captured.topics[0] !== "0x68656c6c6f") {
                throw new Error("unexpected converted string topic: " + captured.topics[0]);
            }
            return true;
        }
        throw new Error("expected an error");
    });

    check("odd-nibble topic hex is rejected", function () {
        try {
            qrl.getLogs({fromBlock: "0x0", toBlock: "latest", topics: ["0xb"]});
        } catch (e) {
            return true;
        }
        throw new Error("expected an error");
    });

    check("invalid topic hex is rejected", function () {
        try {
            qrl.getLogs({fromBlock: "0x0", toBlock: "latest", topics: ["0xzz"]});
        } catch (e) {
            return true;
        }
        throw new Error("expected an error");
    });
} finally {
    web3.setProvider(realProvider);
}

console.log("SUITE logs_topics: " + (_fail === 0 ? "PASSED" : "FAILED") +
    " (" + _pass + "/" + (_pass + _fail) + " checks)");
