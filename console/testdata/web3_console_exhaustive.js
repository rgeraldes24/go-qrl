(function () {
  /*
   * Exhaustive live console coverage for the embedded web3.js bundle.
   *
   * Run through console/testdata/run_web3_console_exhaustive.sh. The runner
   * attaches to a live go-qrl endpoint and loads this file through loadScript.
   */
  var results = [];
  var covered = {};
  var failures = [];
  var builtins = {
    __defineGetter__: true,
    __defineSetter__: true,
    __lookupGetter__: true,
    __lookupSetter__: true,
    __proto__: true,
    constructor: true,
    hasOwnProperty: true,
    isPrototypeOf: true,
    propertyIsEnumerable: true,
    toLocaleString: true,
    toString: true,
    valueOf: true
  };

  function stringify(value) {
    if (value === null) return "null";
    if (typeof value === "undefined") return "undefined";
    if (value && typeof value.toString === "function" && value.constructor && value.constructor.name === "BigNumber") {
      return value.toString(10);
    }
    try {
      return JSON.stringify(value);
    } catch (err) {
      return String(value);
    }
  }

  function isHex(value) {
    return typeof value === "string" && value.slice(0, 2) === "0x";
  }

  function isHexBytes(value, bytes) {
    return isHex(value) && value.length === 2 + bytes * 2;
  }

  function isHexArray(value) {
    if (!(value instanceof Array)) return false;
    for (var i = 0; i < value.length; i++) {
      if (!isHex(value[i])) return false;
    }
    return true;
  }

  function toNumber(value) {
    if (value && typeof value.toNumber === "function") return value.toNumber();
    if (typeof value === "string" && value.slice(0, 2) === "0x") return parseInt(value.slice(2), 16);
    return Number(value);
  }

  function zeros(count) {
    var out = "";
    for (var i = 0; i < count; i++) out += "0";
    return out;
  }

  function hexByte(value) {
    var out = Number(value).toString(16);
    if (out.length === 1) out = "0" + out;
    expect(out.length === 2, "value does not fit in one byte: " + value);
    return out;
  }

  function strip0x(value) {
    expect(isHex(value), "expected hex string: " + stringify(value));
    return value.slice(2).toLowerCase();
  }

  function sameHex(a, b) {
    return typeof a === "string" && typeof b === "string" && strip0x(a) === strip0x(b);
  }

  function sameAddress(a, b) {
    return typeof a === "string" && typeof b === "string" && a.toLowerCase() === b.toLowerCase();
  }

  function rightAlignedTopic(value) {
    var body = strip0x(value);
    expect(body.length <= 128, "topic too wide: " + value);
    return "0x" + zeros(128 - body.length) + body;
  }

  function addressTopic(address) {
    expect(typeof address === "string" && address.length === 129 && address.charAt(0) === "Q", "bad QRL address: " + address);
    return "0x" + address.slice(1).toLowerCase();
  }

  function push1(value) {
    return "60" + hexByte(value);
  }

  function push64(value) {
    var body = strip0x(value);
    expect(body.length === 128, "PUSH64 value is not 64 bytes: " + value);
    return "9f" + body;
  }

  function expect(condition, message) {
    if (!condition) throw new Error(message);
  }

  function assertRawTransferLog(log, txHash) {
    expect(log, "missing emitted event log");
    expect(sameAddress(log.address, eventContractAddress), "event address mismatch: " + stringify(log.address));
    if (txHash) expect(sameHex(log.transactionHash, txHash), "event tx hash mismatch: " + stringify(log.transactionHash));
    expect(log.topics instanceof Array, "event topics missing");
    expect(log.topics.length === eventTopics.length, "event topic count mismatch: " + stringify(log.topics));
    for (var i = 0; i < eventTopics.length; i++) {
      expect(sameHex(log.topics[i], eventTopics[i]), "event topic " + i + " mismatch: " + stringify(log.topics[i]));
      expect(isHexBytes(log.topics[i], 64), "event topic " + i + " not 64 bytes");
    }
    expect(log.data === "0x" || log.data === "" || typeof log.data === "undefined", "unexpected event data: " + stringify(log.data));
  }

  function findRawTransferLog(logs, txHash) {
    expect(logs instanceof Array, "logs should be an array");
    for (var i = 0; i < logs.length; i++) {
      try {
        assertRawTransferLog(logs[i], txHash);
        return logs[i];
      } catch (err) {}
    }
    return null;
  }

  function assertDecodedTransfer(log, txHash) {
    expect(log, "missing decoded event log");
    expect(log.event === "Transfer", "decoded event name mismatch: " + stringify(log.event));
    expect(sameAddress(log.address, eventContractAddress), "decoded event address mismatch: " + stringify(log.address));
    if (txHash) expect(sameHex(log.transactionHash, txHash), "decoded event tx hash mismatch: " + stringify(log.transactionHash));
    expect(log.args && sameAddress(log.args.from, account), "decoded from mismatch: " + stringify(log.args));
    expect(log.args.amount && log.args.amount.toString(10) === "1", "decoded amount mismatch: " + stringify(log.args));
  }

  function findDecodedTransfer(logs, txHash) {
    expect(logs instanceof Array, "decoded logs should be an array");
    for (var i = 0; i < logs.length; i++) {
      try {
        assertDecodedTransfer(logs[i], txHash);
        return logs[i];
      } catch (err) {}
    }
    return null;
  }

  function eventEmitterInitCode(signatureTopic, fromTopic, amountTopic) {
    var runtime = push64(amountTopic) + push64(fromTopic) + push64(signatureTopic) + push1(0) + push1(0) + "c3" + "00";
    var runtimeLen = runtime.length / 2;
    var runtimeOffset = 12;
    var init = push1(runtimeLen) + push1(runtimeOffset) + push1(0) + "39" + push1(runtimeLen) + push1(0) + "f3";
    expect(init.length / 2 === runtimeOffset, "bad init offset");
    return "0x" + init + runtime;
  }

  function emitTransferFromEventContract() {
    var hash = web3.qrl.sendTransaction({from: account, to: eventContractAddress, data: "0x", gas: "0x4c4b40"});
    expect(isHexBytes(hash, 32), "bad emitted event tx hash");
    var receipt = waitReceipt(hash, receiptWaitSeconds);
    expect(receipt, "emitted event tx was not mined within " + receiptWaitSeconds + "s");
    expect(toNumber(receipt.status) === 1, "emitted event tx failed");
    expect(findRawTransferLog(receipt.logs, hash), "receipt did not include emitted event log");
    return {hash: hash, receipt: receipt};
  }

  function mark(kind, path, detail) {
    covered[path] = true;
    results.push({kind: kind, path: path, detail: detail || ""});
    console.log(kind + " " + path + (detail ? " :: " + detail : ""));
  }

  function pass(path, detail) {
    mark("PASS", path, detail);
  }

  function skip(path, reason) {
    mark("SKIP", path, reason);
  }

  function check(path, fn) {
    try {
      pass(path, fn());
    } catch (err) {
      var message = err && err.message ? err.message : String(err);
      failures.push(path + ": " + message);
      mark("FAIL", path, message);
    }
  }

  function checkCallback(path, fn, validate) {
    check(path, function () {
      return stringify(callbackResult(fn, validate));
    });
  }

  function callbackResult(fn, validate) {
    var called = false;
    var callbackErr = null;
    var callbackValue = null;
    fn(function (err, value) {
      called = true;
      callbackErr = err;
      callbackValue = value;
    });
    expect(called, "callback was not called");
    expect(!callbackErr, "callback error: " + stringify(callbackErr));
    if (validate) validate(callbackValue);
    return callbackValue;
  }

  function expectedError(path, fn, needles) {
    try {
      var value = fn();
      failures.push(path + ": expected error, got " + stringify(value));
      mark("FAIL", path, "expected error, got " + stringify(value));
    } catch (err) {
      var message = err && err.message ? err.message : String(err);
      var ok = !needles || needles.length === 0;
      for (var i = 0; needles && i < needles.length; i++) {
        if (message.indexOf(needles[i]) !== -1) ok = true;
      }
      if (!ok) {
        failures.push(path + ": unexpected error: " + message);
        mark("FAIL", path, "unexpected error: " + message);
      } else {
        mark("PASS_EXPECTED_ERROR", path, message);
      }
    }
  }

  function checkOrExpectedError(path, fn, validate, needles) {
    try {
      var value = fn();
      if (validate) validate(value);
      pass(path, stringify(value));
    } catch (err) {
      var message = err && err.message ? err.message : String(err);
      var ok = false;
      for (var i = 0; needles && i < needles.length; i++) {
        if (message.indexOf(needles[i]) !== -1) ok = true;
      }
      if (!ok) {
        failures.push(path + ": unexpected error: " + message);
        mark("FAIL", path, message);
      } else {
        mark("PASS_EXPECTED_ERROR", path, message);
      }
    }
  }

  function maybeSandboxed(path, fn) {
    try {
      var value = fn();
      pass(path, "succeeded: " + stringify(value));
    } catch (err) {
      var message = err && err.message ? err.message : String(err);
      if (message.indexOf("operation not permitted") !== -1 || message.indexOf("bind") !== -1 || message.indexOf("listen") !== -1) {
        mark("PASS_EXPECTED_SANDBOX_ERROR", path, message);
      } else {
        failures.push(path + ": unexpected error: " + message);
        mark("FAIL", path, message);
      }
    }
  }

  function allKeys(obj) {
    var out = {};
    for (var k in obj) out[k] = true;
    var p = obj;
    while (p) {
      Object.getOwnPropertyNames(p).forEach(function (k) { out[k] = true; });
      p = Object.getPrototypeOf(p);
    }
    return Object.keys(out).sort();
  }

  function assertCovered(moduleName, obj) {
    var keys = allKeys(obj);
    var missing = [];
    for (var i = 0; i < keys.length; i++) {
      if (builtins[keys[i]]) continue;
      var path = moduleName + "." + keys[i];
      if (!covered[path]) missing.push(path);
    }
    if (missing.length > 0) {
      throw new Error("uncovered " + moduleName + " keys: " + missing.join(", "));
    }
  }

  function rpcCall(method, params) {
    return web3._requestManager.send({method: method, params: params || []});
  }

  function sleepBlocks(count) {
    expect(admin.sleepBlocks(count, 20) === true, "sleepBlocks failed");
  }

  function waitReceipt(hash, blocks) {
    for (var i = 0; i < blocks; i++) {
      var receipt = web3.qrl.getTransactionReceipt(hash);
      if (receipt) return receipt;
      admin.sleep(1);
    }
    return web3.qrl.getTransactionReceipt(hash);
  }

  var account = null;
  var block0 = null;
  var block1 = null;
  var signed = null;
  var rawTxHash = null;
  var rawReceipt = null;
  var sentTxHash = null;
  var sentReceipt = null;
  var sentBlock = null;
  var contractTxHash = null;
  var contractReceipt = null;
  var eventFactory = null;
  var eventContract = null;
  var eventContractAddress = null;
  var eventTopics = null;
  var eventTxHash = null;
  var eventReceipt = null;
  var networkId = null;
  var chainId = null;
  var hasDev = typeof web3.dev !== "undefined";
  var receiptWaitSeconds = typeof WEB3_EXHAUSTIVE_RECEIPT_WAIT_SECONDS === "number" ? WEB3_EXHAUSTIVE_RECEIPT_WAIT_SECONDS : 90;
  var testRpcEndpointMutation = WEB3_EXHAUSTIVE_TEST_RPC_ENDPOINT_MUTATION === true;
  var testDebugSideEffects = WEB3_EXHAUSTIVE_TEST_DEBUG_SIDE_EFFECTS === true;
  var testDestructiveDebug = WEB3_EXHAUSTIVE_TEST_DESTRUCTIVE_DEBUG === true;
  var topic64 = "0x" + zeros(127) + "1";
  var tmpPrefix = "/tmp/go-qrl-web3-exhaustive-" + Date.now() + "-";

  function triggerBlock() {
    if (hasDev) {
      return dev.addWithdrawal({index: "0x2", validatorIndex: "0x1", address: account, amount: "0x1"});
    }
    var hash = web3.qrl.sendTransaction({from: account, to: account, value: "0x4"});
    var receipt = waitReceipt(hash, receiptWaitSeconds);
    expect(receipt, "filter trigger tx was not mined within " + receiptWaitSeconds + "s");
    return hash;
  }

  check("web3._requestManager", function () {
    expect(web3._requestManager && typeof web3._requestManager.send === "function", "missing send");
    return "send function present";
  });
  check("web3.currentProvider", function () {
    expect(web3.currentProvider && typeof web3.currentProvider.isConnected === "function", "missing provider");
    expect(web3.currentProvider.isConnected() === true, "provider disconnected");
    return "connected";
  });
  check("web3._extend", function () {
    expect(typeof web3._extend === "function", "missing extend");
    expect(web3._extend.Method && web3._extend.Property && web3._extend.formatters, "extend helpers missing");
    return "Method/Property/formatters present";
  });
  check("web3.settings", function () {
    expect(web3.settings && typeof web3.settings === "object", "settings missing");
    return "object";
  });
  skip("qrl._requestManager", "internal request manager; covered via web3._requestManager");
  skip("net._requestManager", "internal request manager; covered via web3._requestManager");
  check("web3.providers", function () {
    expect(typeof web3.providers.HttpProvider === "function", "HttpProvider missing");
    expect(typeof web3.providers.IpcProvider === "function", "IpcProvider missing");
    var httpProvider = new web3.providers.HttpProvider("http://127.0.0.1:0", 1, "user", "pass");
    expect(httpProvider.host === "http://127.0.0.1:0", "HttpProvider host mismatch");
    expect(httpProvider.timeout === 1, "HttpProvider timeout mismatch");
    expect(typeof httpProvider.prepareRequest === "function", "HttpProvider.prepareRequest missing");
    expect(typeof httpProvider.send === "function", "HttpProvider.send missing");
    expect(typeof httpProvider.sendAsync === "function", "HttpProvider.sendAsync missing");
    expect(typeof httpProvider.isConnected === "function", "HttpProvider.isConnected missing");
    expect(typeof web3.providers.IpcProvider.prototype.send === "function", "IpcProvider.send missing");
    expect(typeof web3.providers.IpcProvider.prototype.sendAsync === "function", "IpcProvider.sendAsync missing");
    expect(typeof web3.providers.IpcProvider.prototype.isConnected === "function", "IpcProvider.isConnected missing");
    covered["providers.HttpProvider"] = true;
    covered["providers.IpcProvider"] = true;
    return "constructors/prototypes present";
  });
  check("web3.setProvider", function () {
    var provider = web3.currentProvider;
    web3.setProvider(provider);
    expect(web3.currentProvider === provider, "provider mismatch after setProvider");
    return "same provider";
  });
  check("web3.reset", function () {
    web3.reset(true);
    expect(web3.settings && typeof web3.settings === "object", "settings missing after reset");
    return "keep sync polls";
  });

  check("web3.admin", function () { expect(admin === web3.admin, "admin alias mismatch"); return "alias"; });
  check("web3.debug", function () { expect(debug === web3.debug, "debug alias mismatch"); return "alias"; });
  if (hasDev) {
    check("web3.dev", function () { expect(dev === web3.dev, "dev alias mismatch"); return "alias"; });
  } else {
    skip("web3.dev", "dev module not exposed by this endpoint");
  }
  check("web3.miner", function () {
    expect(typeof web3.miner === "undefined", "deprecated miner namespace should not be exported");
    expect(typeof miner === "undefined", "deprecated miner alias should not be exported");
    return "absent";
  });
  check("web3.personal", function () {
    expect(typeof web3.personal === "undefined", "deprecated personal namespace should not be exported");
    expect(typeof personal === "undefined", "deprecated personal alias should not be exported");
    return "absent";
  });
  check("web3.shh", function () {
    expect(typeof web3.shh === "undefined", "deprecated shh namespace should not be exported");
    expect(typeof shh === "undefined", "deprecated shh alias should not be exported");
    return "absent";
  });
  check("qrl.protocolVersion", function () {
    expect(typeof web3.qrl.protocolVersion === "undefined", "qrl_protocolVersion should not be exported");
    return "absent";
  });
  check("qrl.getProtocolVersion", function () {
    expect(typeof web3.qrl.getProtocolVersion === "undefined", "qrl.getProtocolVersion should not be exported");
    return "absent";
  });
  check("qrl.resend", function () {
    expect(typeof web3.qrl.resend === "undefined", "qrl.resend should not be exported");
    return "absent";
  });
  check("qrl.submitTransaction", function () {
    expect(typeof web3.qrl.submitTransaction === "undefined", "qrl.submitTransaction should not be exported");
    return "absent";
  });
  check("qrl.compile.hyperion", function () {
    expect(typeof web3.qrl.compile === "undefined" || typeof web3.qrl.compile.hyperion === "undefined", "qrl.compile.hyperion should not be exported");
    return "absent";
  });
  check("web3.net", function () { expect(net === web3.net, "net alias mismatch"); return "alias"; });
  check("web3.qrl", function () { expect(qrl === web3.qrl, "qrl alias mismatch"); return "alias"; });
  check("web3.rpc", function () { expect(rpc === web3.rpc, "rpc alias mismatch"); return "alias"; });
  check("web3.txpool", function () { expect(txpool === web3.txpool, "txpool alias mismatch"); return "alias"; });
  check("web3.db", function () {
    expect(typeof web3.db === "undefined", "web3.db should not be exported");
    return "absent";
  });
  check("web3.version", function () {
    expect(web3.version.api, "api version missing");
    networkId = String(web3.version.network);
    expect(networkId.length > 0 && !isNaN(Number(networkId)), "network mismatch: " + web3.version.network);
    expect(web3.version.node.indexOf("Gqrl/") === 0, "node mismatch: " + web3.version.node);
    covered["version.api"] = true;
    covered["version.network"] = true;
    covered["version.node"] = true;
    return networkId;
  });
  checkCallback("version.getNetwork", function (cb) { web3.version.getNetwork(cb); }, function (value) {
    expect(String(value) === networkId, "network mismatch: " + stringify(value));
  });
  checkCallback("version.getNode", function (cb) { web3.version.getNode(cb); }, function (value) {
    expect(String(value).indexOf("Gqrl/") === 0, "node mismatch: " + stringify(value));
  });

  check("web3.isConnected", function () { expect(web3.isConnected() === true, "not connected"); return "true"; });
  check("web3.createBatch", function () {
    var batch = web3.createBatch();
    expect(batch && typeof batch.add === "function" && typeof batch.execute === "function", "bad batch");
    var called = false;
    var batchErr = null;
    var batchValue = null;
    batch.add(web3.version.getNetwork.request(function (err, value) {
      called = true;
      batchErr = err;
      batchValue = value;
    }));
    batch.execute();
    expect(called, "batch callback was not called");
    expect(!batchErr, "batch callback error: " + stringify(batchErr));
    expect(String(batchValue) === networkId, "bad batch network: " + stringify(batchValue));
    return "executed";
  });
  check("web3.BigNumber", function () {
    var n = new web3.BigNumber("123");
    expect(n.plus(1).toString(10) === "124", "bad BigNumber");
    return n.toString(10);
  });
  check("web3.toHex", function () { expect(web3.toHex(255) === "0xff", "bad toHex"); return "0xff"; });
  check("web3.toAscii", function () { expect(web3.toAscii("0x6869") === "hi", "bad toAscii"); return "hi"; });
  check("web3.toUtf8", function () { expect(web3.toUtf8("0x6869") === "hi", "bad toUtf8"); return "hi"; });
  check("web3.fromAscii", function () { expect(web3.fromAscii("hi").slice(0, 6) === "0x6869", "bad fromAscii"); return web3.fromAscii("hi").slice(0, 6); });
  check("web3.fromUtf8", function () { expect(web3.fromUtf8("hi") === "0x6869", "bad fromUtf8"); return "0x6869"; });
  check("web3.toDecimal", function () { expect(web3.toDecimal("0x10") === 16, "bad toDecimal"); return "16"; });
  check("web3.fromDecimal", function () { expect(web3.fromDecimal(16) === "0x10", "bad fromDecimal"); return "0x10"; });
  check("web3.toBigNumber", function () { expect(web3.toBigNumber("0x10").toString(10) === "16", "bad toBigNumber"); return "16"; });
  check("web3.toPlanck", function () { expect(web3.toPlanck(1, "quanta").toString(10) === "1000000000000000000", "bad toPlanck"); return "1 quanta"; });
  check("web3.fromPlanck", function () { expect(web3.fromPlanck("1000000000000000000", "quanta").toString(10) === "1", "bad fromPlanck"); return "1"; });
  check("web3.sha3", function () { expect(isHexBytes(web3.sha3("qrl"), 32), "bad sha3"); return web3.sha3("qrl").slice(0, 18); });
  check("web3.padLeft", function () { expect(web3.padLeft("1", 4) === "0001", "bad padLeft"); return "0001"; });
  check("web3.padRight", function () { expect(web3.padRight("1", 4) === "1000", "bad padRight"); return "1000"; });

  check("qrl.accounts", function () {
    account = web3.qrl.accounts[0];
    expect(typeof account === "string" && account.length === 129 && account.charAt(0) === "Q", "bad account " + account);
    return account;
  });
  checkCallback("qrl.getAccounts", function (cb) { web3.qrl.getAccounts(cb); }, function (value) {
    expect(value instanceof Array && value[0] === account, "accounts mismatch");
  });
  check("web3.isAddress", function () { expect(web3.isAddress(account), "isAddress rejected account"); return "true"; });
  check("web3.isChecksumAddress", function () { expect(web3.isChecksumAddress(account), "checksum rejected account"); return "true"; });
  check("web3.toChecksumAddress", function () { expect(web3.toChecksumAddress(account) === account, "checksum mismatch"); return "ok"; });
  check("qrl.blockNumber", function () {
    expect(web3.qrl.blockNumber >= 0, "block number negative");
    return String(web3.qrl.blockNumber);
  });
  checkCallback("qrl.getBlockNumber", function (cb) { web3.qrl.getBlockNumber(cb); }, function (value) {
    expect(toNumber(value) >= 0, "bad block number");
  });
  check("qrl.chainId", function () {
    chainId = toNumber(web3.qrl.chainId());
    expect(chainId > 0, "bad chain id");
    return String(chainId);
  });
  check("qrl.syncing", function () {
    expect(web3.qrl.syncing === false || web3.qrl.syncing.currentBlock !== undefined, "bad syncing value");
    var watcher = web3.qrl.isSyncing(function () {});
    expect(watcher && typeof watcher.stopWatching === "function", "bad isSyncing watcher");
    watcher.stopWatching();
    covered["qrl.isSyncing"] = true;
    return stringify(web3.qrl.syncing);
  });
  checkCallback("qrl.getSyncing", function (cb) { web3.qrl.getSyncing(cb); }, function (value) {
    expect(value === false || (value && value.currentBlock !== undefined), "bad syncing value");
  });
  check("qrl.gasPrice", function () {
    expect(web3.qrl.gasPrice.gte(0), "bad gasPrice");
    return web3.qrl.gasPrice.toString(10);
  });
  checkCallback("qrl.getGasPrice", function (cb) { web3.qrl.getGasPrice(cb); }, function (value) {
    expect(value && value.gte && value.gte(0), "bad gasPrice");
  });
  check("qrl.maxPriorityFeePerGas", function () {
    expect(web3.qrl.maxPriorityFeePerGas.gt(0), "bad max priority fee");
    return web3.qrl.maxPriorityFeePerGas.toString(10);
  });
  checkCallback("qrl.getMaxPriorityFeePerGas", function (cb) { web3.qrl.getMaxPriorityFeePerGas(cb); }, function (value) {
    expect(value && value.gt && value.gt(0), "bad max priority fee");
  });
  check("qrl.defaultBlock", function () {
    var old = web3.qrl.defaultBlock;
    web3.qrl.defaultBlock = "latest";
    expect(web3.qrl.defaultBlock === "latest", "defaultBlock set failed");
    web3.qrl.defaultBlock = old;
    return "set/get";
  });
  check("qrl.defaultAccount", function () {
    var old = web3.qrl.defaultAccount;
    web3.qrl.defaultAccount = account;
    expect(web3.qrl.defaultAccount === account, "defaultAccount set failed");
    web3.qrl.defaultAccount = old;
    return "set/get";
  });

  check("net.listening", function () {
    expect(web3.net.listening === true || web3.net.listening === false, "bad listening value");
    return stringify(web3.net.listening);
  });
  checkCallback("net.getListening", function (cb) { web3.net.getListening(cb); }, function (value) {
    expect(value === true || value === false, "bad listening value");
  });
  check("net.peerCount", function () {
    expect(web3.net.peerCount >= 0, "peerCount should be non-negative");
    return String(web3.net.peerCount);
  });
  checkCallback("net.getPeerCount", function (cb) { web3.net.getPeerCount(cb); }, function (value) {
    expect(toNumber(value) >= 0, "bad peer count");
  });
  check("net.version", function () {
    expect(String(web3.net.version) === networkId, "net.version mismatch");
    return String(web3.net.version);
  });
  checkCallback("net.getVersion", function (cb) { web3.net.getVersion(cb); }, function (value) {
    expect(String(value) === networkId, "net version mismatch");
  });

  check("rpc.modules", function () {
    expect(web3.rpc.modules.qrl, "qrl module missing");
    return Object.keys(web3.rpc.modules).sort().join(",");
  });
  checkCallback("rpc.getModules", function (cb) { web3.rpc.getModules(cb); }, function (value) {
    expect(value && value.qrl, "qrl module missing");
  });

  check("admin.nodeInfo", function () {
    expect(admin.nodeInfo.name.indexOf("Gqrl/") === 0, "nodeInfo mismatch");
    return admin.nodeInfo.name;
  });
  checkCallback("admin.getNodeInfo", function (cb) { admin.getNodeInfo(cb); }, function (value) {
    expect(value && value.name && value.name.indexOf("Gqrl/") === 0, "nodeInfo mismatch");
  });
  check("admin.peers", function () {
    expect(admin.peers instanceof Array, "peers should be an array");
    return String(admin.peers.length);
  });
  checkCallback("admin.getPeers", function (cb) { admin.getPeers(cb); }, function (value) {
    expect(value instanceof Array, "peers should be an array");
  });
  check("admin.datadir", function () {
    expect(typeof admin.datadir === "string" && admin.datadir.length > 0, "unexpected datadir " + admin.datadir);
    tmpPrefix = admin.datadir + "/web3-console-exhaustive-" + Date.now() + "-";
    return admin.datadir;
  });
  checkCallback("admin.getDatadir", function (cb) { admin.getDatadir(cb); }, function (value) {
    expect(typeof value === "string" && value.length > 0, "unexpected datadir " + value);
  });
  expectedError("admin.addPeer", function () { return admin.addPeer("not-a-node"); }, ["invalid", "missing", "too short"]);
  expectedError("admin.removePeer", function () { return admin.removePeer("not-a-node"); }, ["invalid", "missing", "too short"]);
  expectedError("admin.addTrustedPeer", function () { return admin.addTrustedPeer("not-a-node"); }, ["invalid", "missing", "too short"]);
  expectedError("admin.removeTrustedPeer", function () { return admin.removeTrustedPeer("not-a-node"); }, ["invalid", "missing", "too short"]);
  check("admin.sleep", function () { expect(admin.sleep(0) === true, "sleep failed"); return "true"; });
  check("admin.sleepBlocks", function () { expect(admin.sleepBlocks(0, 1) === true, "sleepBlocks(0) failed"); return "true"; });
  if (testRpcEndpointMutation) {
    maybeSandboxed("admin.startHTTP", function () { return admin.startHTTP("127.0.0.1", 0, "*", "qrl,net,web3"); });
    check("admin.stopHTTP", function () { return stringify(admin.stopHTTP()); });
    maybeSandboxed("admin.startWS", function () { return admin.startWS("127.0.0.1", 0, "*", "qrl,net,web3"); });
    check("admin.stopWS", function () { return stringify(admin.stopWS()); });
  } else {
    skip("admin.startHTTP", "RPC endpoint mutation disabled by default; set TEST_RPC_ENDPOINT_MUTATION=1 on disposable networks");
    skip("admin.stopHTTP", "RPC endpoint mutation disabled by default; set TEST_RPC_ENDPOINT_MUTATION=1 on disposable networks");
    skip("admin.startWS", "RPC endpoint mutation disabled by default; set TEST_RPC_ENDPOINT_MUTATION=1 on disposable networks");
    skip("admin.stopWS", "RPC endpoint mutation disabled by default; set TEST_RPC_ENDPOINT_MUTATION=1 on disposable networks");
  }
  check("admin.exportChain", function () {
    var file = tmpPrefix + "export.rlp";
    var ok = admin.exportChain(file, 0, 0);
    expect(ok === true || ok === null || typeof ok === "undefined", "unexpected export result " + stringify(ok));
    return file;
  });
  expectedError("admin.importChain", function () { return admin.importChain(tmpPrefix + "missing.rlp"); }, ["no such file", "open"]);
  check("admin.clearHistory", function () { admin.clearHistory(); return "cleared"; });

  check("txpool.status", function () {
    expect(typeof txpool.status.pending === "number", "bad status");
    return stringify(txpool.status);
  });
  checkCallback("txpool.getStatus", function (cb) { txpool.getStatus(cb); }, function (value) {
    expect(value && typeof value.pending === "number", "bad status");
  });
  check("txpool.content", function () {
    expect(txpool.content.pending !== undefined, "bad content");
    return "ok";
  });
  checkCallback("txpool.getContent", function (cb) { txpool.getContent(cb); }, function (value) {
    expect(value && value.pending !== undefined, "bad content");
  });
  check("txpool.inspect", function () {
    expect(txpool.inspect.pending !== undefined, "bad inspect");
    return "ok";
  });
  checkCallback("txpool.getInspect", function (cb) { txpool.getInspect(cb); }, function (value) {
    expect(value && value.pending !== undefined, "bad inspect");
  });
  check("txpool.contentFrom", function () {
    var content = txpool.contentFrom(account);
    expect(content.pending !== undefined, "bad contentFrom");
    return "ok";
  });

  check("qrl.getBalance", function () { expect(web3.qrl.getBalance(account).gt(0), "balance missing"); return web3.qrl.getBalance(account).toString(10); });
  check("qrl.getCode", function () { expect(web3.qrl.getCode(account) === "0x", "code mismatch"); return "0x"; });
  check("qrl.getStorageAt", function () { expect(isHexBytes(web3.qrl.getStorageAt(account, "0x0", "latest"), 64), "storage not 64-byte hex"); return "64-byte"; });
  check("qrl.getTransactionCount", function () { expect(web3.qrl.getTransactionCount(account) >= 0, "bad nonce"); return String(web3.qrl.getTransactionCount(account)); });
  check("qrl.call", function () { expect(web3.qrl.call({from: account, to: account, data: "0x"}, "latest") === "0x", "call mismatch"); return "0x"; });
  check("qrl.estimateGas", function () { expect(web3.qrl.estimateGas({from: account, to: account, value: "0x1"}, "latest") >= 21000, "gas too low"); return "ok"; });
  check("qrl.createAccessList", function () {
    var res = web3.qrl.createAccessList({from: account, to: account, value: "0x0"}, "latest");
    expect(res && res.accessList !== undefined, "bad access list");
    return "accessList=" + res.accessList.length;
  });
  check("qrl.feeHistory", function () {
    var res = web3.qrl.feeHistory(1, "latest", []);
    expect(res && res.baseFeePerGas.length >= 1, "bad fee history");
    return "baseFeePerGas=" + res.baseFeePerGas.length;
  });
  check("qrl.getProof", function () {
    var res = web3.qrl.getProof(account, [], "latest");
    expect(res.address === account, "proof address mismatch");
    return "storageProof=" + res.storageProof.length;
  });
  check("qrl.getLogs", function () {
    var logs = web3.qrl.getLogs({fromBlock: "0x0", toBlock: "latest", topics: [topic64]});
    expect(logs instanceof Array, "logs not array");
    return "logs=" + logs.length;
  });
  check("qrl.pendingTransactions", function () {
    expect(web3.qrl.pendingTransactions instanceof Array, "pendingTransactions not array");
    return "pending=" + web3.qrl.pendingTransactions.length;
  });
  checkCallback("qrl.getPendingTransactions", function (cb) { web3.qrl.getPendingTransactions(cb); }, function (value) {
    expect(value instanceof Array, "pendingTransactions not array");
  });

  check("qrl.sign", function () {
    var sig = web3.qrl.sign(account, "0x1234");
    expect(isHex(sig) && sig.length > 2, "bad signature");
    return "sigBytes=" + ((sig.length - 2) / 2);
  });
  check("qrl.fillTransaction", function () {
    var filled = web3.qrl.fillTransaction({from: account, to: account, value: "0x1"});
    expect(filled && isHex(filled.raw), "bad filled tx");
    return "rawBytes=" + ((filled.raw.length - 2) / 2);
  });
  check("qrl.signTransaction", function () {
    var nonce = web3.qrl.getTransactionCount(account);
    signed = web3.qrl.signTransaction({
      from: account,
      to: account,
      value: "0x2",
      nonce: web3.fromDecimal(nonce),
      gas: "0x5208",
      maxPriorityFeePerGas: "0x3b9aca00",
      maxFeePerGas: "0x3b9aca00"
    });
    expect(signed && isHex(signed.raw), "bad signed tx");
    return "nonce=" + nonce;
  });
  check("qrl.sendRawTransaction", function () {
    rawTxHash = web3.qrl.sendRawTransaction(signed.raw);
    expect(isHexBytes(rawTxHash, 32), "bad raw tx hash");
    rawReceipt = waitReceipt(rawTxHash, receiptWaitSeconds);
    expect(rawReceipt, "raw tx was not mined within " + receiptWaitSeconds + "s");
    expect(toNumber(rawReceipt.status) === 1, "raw tx failed");
    return rawTxHash + " mined";
  });
  check("qrl.sendTransaction", function () {
    sentTxHash = web3.qrl.sendTransaction({from: account, to: account, value: "0x3"});
    expect(isHexBytes(sentTxHash, 32), "bad sent tx hash");
    sentReceipt = waitReceipt(sentTxHash, receiptWaitSeconds);
    var tx = web3.qrl.getTransaction(sentTxHash);
    expect(tx && tx.hash === sentTxHash, "sent tx was not retrievable after submission");
    expect(sentReceipt, "send tx was not mined within " + receiptWaitSeconds + "s");
    expect(toNumber(sentReceipt.status) === 1, "send tx failed");
    sentBlock = web3.qrl.getBlock(sentReceipt.blockNumber, true);
    expect(sentBlock.transactions.length > 0, "sent block has no transactions");
    return sentTxHash + " mined";
  });
  check("qrl.getTransaction", function () {
    var tx = web3.qrl.getTransaction(sentTxHash);
    expect(tx.hash === sentTxHash, "tx hash mismatch");
    return tx.hash;
  });
  check("qrl.getTransactionReceipt", function () {
    var receipt = web3.qrl.getTransactionReceipt(sentTxHash);
    expect(receipt && receipt.transactionHash === sentTxHash, "receipt hash mismatch");
    return "status=" + receipt.status;
  });
  check("qrl.getRawTransaction", function () { expect(isHex(web3.qrl.getRawTransaction(sentTxHash)), "raw tx missing"); return "ok"; });
  check("qrl.getBlock", function () {
    expect(web3.qrl.getBlock(sentBlock.number, true).hash === sentBlock.hash, "getBlock number mismatch");
    expect(web3.qrl.getBlock(sentBlock.hash, true).number === sentBlock.number, "getBlock hash mismatch");
    return "block=" + sentBlock.number;
  });
  check("qrl.getBlockByNumber", function () { expect(web3.qrl.getBlockByNumber(sentBlock.number, true).hash === sentBlock.hash, "mismatch"); return "ok"; });
  check("qrl.getBlockByHash", function () { expect(toNumber(web3.qrl.getBlockByHash(sentBlock.hash, true).number) === toNumber(sentBlock.number), "mismatch"); return "ok"; });
  check("qrl.getHeaderByNumber", function () { expect(web3.qrl.getHeaderByNumber(sentBlock.number).hash === sentBlock.hash, "mismatch"); return "ok"; });
  check("qrl.getHeaderByHash", function () { expect(toNumber(web3.qrl.getHeaderByHash(sentBlock.hash).number) === toNumber(sentBlock.number), "mismatch"); return "ok"; });
  check("qrl.getBlockTransactionCount", function () {
    expect(web3.qrl.getBlockTransactionCount(sentBlock.number) === sentBlock.transactions.length, "count by number mismatch");
    expect(web3.qrl.getBlockTransactionCount(sentBlock.hash) === sentBlock.transactions.length, "count by hash mismatch");
    return "ok";
  });
  check("qrl.getTransactionFromBlock", function () {
    var byNumber = web3.qrl.getTransactionFromBlock(sentBlock.number, 0);
    var byHash = web3.qrl.getTransactionFromBlock(sentBlock.hash, 0);
    expect(byNumber.hash === sentBlock.transactions[0].hash, "tx by number mismatch");
    expect(byHash.hash === sentBlock.transactions[0].hash, "tx by hash mismatch");
    return "ok";
  });
  check("qrl.getRawTransactionFromBlock", function () {
    var byNumber = web3.qrl.getRawTransactionFromBlock(sentBlock.number, 0);
    var byHash = web3.qrl.getRawTransactionFromBlock(sentBlock.hash, 0);
    expect(isHex(byNumber), "raw by number missing");
    expect(isHex(byHash), "raw by hash missing");
    return "ok";
  });
  check("qrl.getBlockReceipts", function () {
    var receipts = web3.qrl.getBlockReceipts(sentBlock.hash);
    expect(receipts instanceof Array, "receipts missing");
    expect(receipts.length > 0, "block receipts missing mined transaction");
    return "receipts=" + receipts.length;
  });

  check("qrl.contract", function () {
    var abi = [
      {type: "function", name: "set", constant: false, payable: false, inputs: [{name: "u", type: "uint512"}, {name: "b", type: "bool"}, {name: "a", type: "address"}, {name: "s", type: "string"}], outputs: []},
      {type: "event", name: "Transfer", anonymous: false, inputs: [{name: "from", type: "address", indexed: true}, {name: "amount", type: "uint512", indexed: true}]}
    ];
    var factory = web3.qrl.contract(abi);
    eventFactory = factory;
    expect(factory && typeof factory.at === "function" && typeof factory.getData === "function", "bad contract factory");
    var callbackContract = null;
    var c = factory.at(account, function (err, value) {
      expect(!err, "contract at callback error: " + stringify(err));
      callbackContract = value;
    });
    expect(callbackContract && callbackContract.address === account, "contract callback failed");
    var data = c.set.getData("1", true, account, "hi");
    expect(data.length === 778, "ABI data length mismatch " + data.length);
    var req = c.set.request("1", true, account, "hi");
    expect(req.method === "qrl_sendTransaction", "bad request method");
    var callResult = c.set.call("1", true, account, "hi", {from: account});
    expect(callResult instanceof Array && callResult.length === 0, "bad contract call result");
    expect(c.set.estimateGas("1", true, account, "hi", {from: account}) >= 21000, "bad contract estimateGas");
    contractTxHash = c.set.sendTransaction("1", true, account, "hi", {from: account});
    expect(isHexBytes(contractTxHash, 32), "bad contract tx hash");
    contractReceipt = waitReceipt(contractTxHash, receiptWaitSeconds);
    expect(contractReceipt && toNumber(contractReceipt.status) === 1, "contract wrapper tx was not mined");

    var signatureTopic = rightAlignedTopic(web3.sha3("Transfer(address,uint512)"));
    var fromTopic = addressTopic(account);
    var amountTopic = topic64;
    eventTopics = [signatureTopic, fromTopic, amountTopic];
    var initCode = eventEmitterInitCode(signatureTopic, fromTopic, amountTopic);
    var deployHash = web3.qrl.sendTransaction({from: account, data: initCode, gas: "0x4c4b40"});
    expect(isHexBytes(deployHash, 32), "bad event contract deploy hash");
    var deployReceipt = waitReceipt(deployHash, receiptWaitSeconds);
    expect(deployReceipt && toNumber(deployReceipt.status) === 1, "event contract deploy failed");
    expect(typeof deployReceipt.contractAddress === "string" && web3.isAddress(deployReceipt.contractAddress), "missing event contract address");
    eventContractAddress = deployReceipt.contractAddress;
    eventContract = factory.at(eventContractAddress);
    expect(web3.qrl.getCode(eventContractAddress).length > 2, "event contract code missing");

    var emitted = emitTransferFromEventContract();
    eventTxHash = emitted.hash;
    eventReceipt = emitted.receipt;

    var ev = eventContract.Transfer({from: account, amount: "1"}, {fromBlock: eventReceipt.blockNumber, toBlock: eventReceipt.blockNumber});
    expect(ev.options.topics.length === 3, "event topics missing");
    expect(isHexBytes(ev.options.topics[0], 64), "event signature topic not 64 bytes");
    for (var i = 0; i < eventTopics.length; i++) {
      expect(sameHex(ev.options.topics[i], eventTopics[i]), "ABI event topic " + i + " mismatch");
    }
    callbackResult(function (cb) { ev.get(cb); }, function (value) {
      expect(value instanceof Array, "event get failed");
      expect(findDecodedTransfer(value, eventTxHash), "ABI event filter did not return emitted log");
    });
    expect(ev.stopWatching() === true, "event stop failed");
    var all = eventContract.allEvents({fromBlock: eventReceipt.blockNumber, toBlock: eventReceipt.blockNumber});
    callbackResult(function (cb) { all.get(cb); }, function (value) {
      expect(value instanceof Array, "allEvents get failed");
      expect(findDecodedTransfer(value, eventTxHash), "allEvents did not return emitted log");
    });
    expect(all.stopWatching() === true, "allEvents stop failed");
    covered["qrl.filter"] = true;
    return "abi/function/event ok at " + eventContractAddress;
  });
  check("qrl.getCode.contract", function () {
    var code = web3.qrl.getCode(eventContractAddress);
    expect(isHex(code) && code.length > 2, "contract code missing");
    return "bytes=" + ((code.length - 2) / 2);
  });
  check("qrl.getTransaction.emittedEvent", function () {
    var tx = web3.qrl.getTransaction(eventTxHash);
    expect(tx && sameHex(tx.hash, eventTxHash), "event tx missing");
    expect(sameAddress(tx.to, eventContractAddress), "event tx target mismatch: " + stringify(tx.to));
    return tx.hash;
  });
  check("qrl.getTransactionReceipt.emittedEvent", function () {
    var receipt = web3.qrl.getTransactionReceipt(eventTxHash);
    expect(receipt && sameHex(receipt.transactionHash, eventTxHash), "event receipt missing");
    expect(toNumber(receipt.status) === 1, "event receipt failed");
    expect(findRawTransferLog(receipt.logs, eventTxHash), "event receipt did not include emitted log");
    return "logs=" + receipt.logs.length;
  });
  check("qrl.getLogs.emittedEvent", function () {
    var base = {fromBlock: eventReceipt.blockNumber, toBlock: eventReceipt.blockNumber, address: eventContractAddress};
    var rawSignatureTopic = web3.sha3("Transfer(address,uint512)");
    var exact = web3.qrl.getLogs({fromBlock: base.fromBlock, toBlock: base.toBlock, address: base.address, topics: eventTopics});
    expect(findRawTransferLog(exact, eventTxHash), "exact topic getLogs missed emitted log");
    var rawSignature = web3.qrl.getLogs({fromBlock: base.fromBlock, toBlock: base.toBlock, address: base.address, topics: [rawSignatureTopic, null, eventTopics[2]]});
    expect(findRawTransferLog(rawSignature, eventTxHash), "raw signature topic getLogs missed emitted log");
    var wildcard = web3.qrl.getLogs({fromBlock: base.fromBlock, toBlock: base.toBlock, address: base.address, topics: [eventTopics[0], null, eventTopics[2]]});
    expect(findRawTransferLog(wildcard, eventTxHash), "wildcard topic getLogs missed emitted log");
    var orTopics = web3.qrl.getLogs({fromBlock: base.fromBlock, toBlock: base.toBlock, address: base.address, topics: [eventTopics[0], ["0x" + zeros(127) + "2", eventTopics[1]], eventTopics[2]]});
    expect(findRawTransferLog(orTopics, eventTxHash), "OR topic getLogs missed emitted log");
    var wrong = web3.qrl.getLogs({fromBlock: base.fromBlock, toBlock: base.toBlock, address: base.address, topics: [eventTopics[0], "0x" + zeros(127) + "2", eventTopics[2]]});
    expect(!findRawTransferLog(wrong, eventTxHash), "wrong topic getLogs matched emitted log");
    return "exact=" + exact.length + " wildcard=" + wildcard.length + " or=" + orTopics.length;
  });
  check("qrl.getBlockReceipts.emittedEvent", function () {
    var receipts = web3.qrl.getBlockReceipts(eventReceipt.blockHash);
    expect(receipts instanceof Array, "event block receipts missing");
    var foundReceipt = null;
    for (var i = 0; i < receipts.length; i++) {
      if (sameHex(receipts[i].transactionHash, eventTxHash)) foundReceipt = receipts[i];
    }
    expect(foundReceipt, "event receipt missing from block receipts");
    expect(findRawTransferLog(foundReceipt.logs, eventTxHash), "block receipts did not include emitted log");
    return "receipts=" + receipts.length;
  });
  check("qrl.filter", function () {
    var rawSignatureTopic = web3.sha3("Transfer(address,uint512)");
    var objectFilter = web3.qrl.filter({fromBlock: eventReceipt.blockNumber, toBlock: eventReceipt.blockNumber, address: eventContractAddress, topics: eventTopics});
    callbackResult(function (cb) { objectFilter.get(cb); }, function (value) {
      expect(value instanceof Array, "object filter get failed");
      expect(findRawTransferLog(value, eventTxHash), "object filter get missed emitted log");
    });
    expect(objectFilter.stopWatching() === true, "object filter stop failed");
    var rawSignatureFilter = web3.qrl.filter({fromBlock: eventReceipt.blockNumber, toBlock: eventReceipt.blockNumber, address: eventContractAddress, topics: [rawSignatureTopic, null, eventTopics[2]]});
    callbackResult(function (cb) { rawSignatureFilter.get(cb); }, function (value) {
      expect(value instanceof Array, "raw signature filter get failed");
      expect(findRawTransferLog(value, eventTxHash), "raw signature filter missed emitted log");
    });
    expect(rawSignatureFilter.stopWatching() === true, "raw signature filter stop failed");
    var logFilterId = rpcCall("qrl_newFilter", [{fromBlock: "latest", toBlock: "latest", address: eventContractAddress, topics: eventTopics}]);
    var liveEmitted = emitTransferFromEventContract();
    var logChanges = [];
    var liveLog = null;
    for (var j = 0; j < 10 && !liveLog; j++) {
      admin.sleep(1);
      logChanges = rpcCall("qrl_getFilterChanges", [logFilterId]);
      liveLog = findRawTransferLog(logChanges, liveEmitted.hash);
    }
    expect(liveLog, "raw log filter changes missing emitted log");
    expect(rpcCall("qrl_uninstallFilter", [logFilterId]) === true, "log filter uninstall failed");
    var id = rpcCall("qrl_newBlockFilter", []);
    triggerBlock();
    var changes = [];
    for (var i = 0; i < 5 && changes.length === 0; i++) {
      admin.sleep(1);
      changes = rpcCall("qrl_getFilterChanges", [id]);
    }
    expect(changes.length > 0, "block filter changes missing");
    expect(rpcCall("qrl_uninstallFilter", [id]) === true, "uninstall failed");
    expectedError("qrl.filter.latest.get", function () {
      var latest = web3.qrl.filter("latest");
      try { return callbackResult(function (cb) { latest.get(cb); }); } finally { try { latest.stopWatching(); } catch (e) {} }
    }, ["callback error", "filter not found", "not found"]);
    return "object+raw log+raw latest";
  });
  if (hasDev) {
    check("dev.setFeeRecipient", function () { dev.setFeeRecipient(account); return "ok"; });
    check("dev.addWithdrawal", function () {
      var res = dev.addWithdrawal({index: "0x3", validatorIndex: "0x1", address: account, amount: "0x1"});
      return stringify(res);
    });
  }

  check("debug.getRawHeader", function () { expect(isHex(debug.getRawHeader(sentBlock.hash)), "missing raw header"); return "ok"; });
  check("debug.getRawBlock", function () { expect(isHex(debug.getRawBlock(sentBlock.hash)), "missing raw block"); return "ok"; });
  check("debug.getRawReceipts", function () {
    var raw = debug.getRawReceipts(sentBlock.hash);
    expect(raw === null || typeof raw === "undefined" || isHexArray(raw), "bad raw receipts: " + stringify(raw));
    expect(raw && raw.length > 0, "raw receipts missing mined transaction");
    return stringify(raw);
  });
  check("debug.getRawReceipts.emittedEvent", function () {
    var raw = debug.getRawReceipts(eventReceipt.blockHash);
    expect(isHexArray(raw), "bad event raw receipts: " + stringify(raw));
    expect(raw.length > 0, "event raw receipts missing");
    return "rawReceipts=" + raw.length;
  });
  check("debug.getRawTransaction", function () { expect(isHex(debug.getRawTransaction(sentTxHash)), "missing debug raw tx"); return "ok"; });
  check("debug.printBlock", function () { debug.printBlock(0); return "printed"; });
  check("debug.dumpBlock", function () { var dump = debug.dumpBlock(sentBlock.number); expect(dump && dump.root, "bad dump"); return "ok"; });
  check("debug.memStats", function () { expect(debug.memStats().Alloc !== undefined, "bad memstats"); return "ok"; });
  check("debug.gcStats", function () { expect(debug.gcStats().NumGC !== undefined || debug.gcStats().Pause !== undefined, "bad gcstats"); return "ok"; });
  check("debug.freeOSMemory", function () { return stringify(debug.freeOSMemory()); });
  check("debug.verbosity", function () { return stringify(debug.verbosity(3)); });
  check("debug.vmodule", function () { return stringify(debug.vmodule("")); });
  check("debug.setGCPercent", function () { var old = debug.setGCPercent(100); expect(typeof old === "number", "bad old GC percent"); return String(old); });
  checkOrExpectedError("debug.getTrieFlushInterval", function () { return debug.getTrieFlushInterval(); }, function (value) {
    expect(typeof value === "string" && value.length > 0, "bad trie flush interval");
  }, ["path-based scheme", "undefined"]);
  checkOrExpectedError("debug.setTrieFlushInterval", function () { return debug.setTrieFlushInterval("1m"); }, function (value) {
    expect(value === null || typeof value === "undefined", "bad set trie flush result");
  }, ["path-based scheme", "undefined"]);
  check("debug.getBadBlocks", function () { expect(debug.getBadBlocks() instanceof Array, "bad blocks not array"); return "ok"; });
  expectedError("debug.preimage", function () { return debug.preimage("0x" + zeros(64)); }, ["unknown preimage"]);
  check("debug.dbAncients", function () { expect(debug.dbAncients() >= 0, "bad ancients"); return String(debug.dbAncients()); });
  expectedError("debug.dbAncient", function () { return debug.dbAncient("headers", 0); }, ["not found", "out of bounds", "ancient", "invalid"]);
  expectedError("debug.dbGet", function () { return debug.dbGet("0x00"); }, ["not found", "missing", "invalid"]);
  expectedError("debug.chaindbProperty", function () { return debug.chaindbProperty("leveldb.stats"); }, ["Invalid number of input parameters"]);
  if (testDestructiveDebug) {
    check("debug.chaindbCompact", function () { debug.chaindbCompact(); return "compacted"; });
  } else {
    skip("debug.chaindbCompact", "database compaction side effect; set TEST_DESTRUCTIVE_DEBUG=1 on disposable nodes");
  }
  skip("debug.freezeClient", "intentional client freeze/hang API");
  if (testDebugSideEffects) {
    check("debug.stacks", function () { debug.stacks("web3-console-no-such-stack-filter"); return "printed"; });
    check("debug.cpuProfile", function () { return stringify(debug.cpuProfile(tmpPrefix + "cpu.prof", 0)); });
    check("debug.startCPUProfile", function () { return stringify(debug.startCPUProfile(tmpPrefix + "start-cpu.prof")); });
    check("debug.stopCPUProfile", function () { return stringify(debug.stopCPUProfile()); });
    check("debug.goTrace", function () { return stringify(debug.goTrace(tmpPrefix + "go.trace", 0)); });
    check("debug.startGoTrace", function () { return stringify(debug.startGoTrace(tmpPrefix + "start-go.trace")); });
    check("debug.stopGoTrace", function () { return stringify(debug.stopGoTrace()); });
    check("debug.blockProfile", function () { return stringify(debug.blockProfile(tmpPrefix + "block.prof", 0)); });
    check("debug.setBlockProfileRate", function () { debug.setBlockProfileRate(1); debug.setBlockProfileRate(0); return "set/reset"; });
    check("debug.writeBlockProfile", function () { return stringify(debug.writeBlockProfile(tmpPrefix + "write-block.prof")); });
    check("debug.mutexProfile", function () { return stringify(debug.mutexProfile(tmpPrefix + "mutex.prof", 0)); });
    check("debug.setMutexProfileFraction", function () { debug.setMutexProfileFraction(1); debug.setMutexProfileFraction(0); return "set/reset"; });
    check("debug.writeMutexProfile", function () { return stringify(debug.writeMutexProfile(tmpPrefix + "write-mutex.prof")); });
    check("debug.writeMemProfile", function () { return stringify(debug.writeMemProfile(tmpPrefix + "mem.prof")); });
  } else {
    skip("debug.stacks", "large runtime stack dump; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.cpuProfile", "profiling file output side effect; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.startCPUProfile", "profiling state mutation; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.stopCPUProfile", "paired with skipped startCPUProfile");
    skip("debug.goTrace", "trace file output side effect; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.startGoTrace", "trace state mutation; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.stopGoTrace", "paired with skipped startGoTrace");
    skip("debug.blockProfile", "profile file output side effect; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.setBlockProfileRate", "global profiler setting mutation; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.writeBlockProfile", "profile file output side effect; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.mutexProfile", "profile file output side effect; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.setMutexProfileFraction", "global profiler setting mutation; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.writeMutexProfile", "profile file output side effect; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
    skip("debug.writeMemProfile", "profile file output side effect; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
  }

  check("debug.traceTransaction", function () { var trace = debug.traceTransaction(sentTxHash, {tracer: "callTracer"}); expect(trace && (trace.from || trace.gas), "bad trace tx"); return "ok"; });
  check("debug.traceTransaction.emittedEvent", function () {
    var trace = debug.traceTransaction(eventTxHash, {tracer: "callTracer"});
    expect(trace && (trace.from || trace.gas), "bad event trace tx");
    if (trace.to) expect(sameAddress(trace.to, eventContractAddress), "event trace target mismatch: " + stringify(trace.to));
    return "ok";
  });
  check("debug.traceCall", function () { var trace = debug.traceCall({from: account, to: account, data: "0x"}, "latest", {tracer: "callTracer"}); expect(trace && (trace.from || trace.gas), "bad trace call"); return "ok"; });
  check("debug.traceBlockByNumber", function () { expect(debug.traceBlockByNumber(sentBlock.number, {}) instanceof Array, "bad trace block number"); return "ok"; });
  check("debug.traceBlockByHash", function () { expect(debug.traceBlockByHash(sentBlock.hash, {}) instanceof Array, "bad trace block hash"); return "ok"; });
  check("debug.traceBlock", function () { expect(debug.traceBlock(debug.getRawBlock(sentBlock.hash), {}) instanceof Array, "bad trace block raw"); return "ok"; });
  expectedError("debug.traceBadBlock", function () { return debug.traceBadBlock(sentBlock.hash, {}); }, ["not found", "bad block"]);
  check("debug.intermediateRoots", function () {
    var roots = debug.intermediateRoots(sentBlock.hash, {});
    expect(roots === null || typeof roots === "undefined" || roots instanceof Array, "bad intermediate roots");
    return stringify(roots);
  });
  if (testDebugSideEffects) {
    check("debug.traceBlockFromFile", function () {
      var file = tmpPrefix + "single-block.rlp";
      expect(admin.exportChain(file, sentBlock.number, sentBlock.number) === true, "single block export failed");
      expect(debug.traceBlockFromFile(file, {}) instanceof Array, "bad trace block from file");
      return "ok";
    });
  } else {
    skip("debug.traceBlockFromFile", "requires exported RLP block file; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
  }
  if (testDebugSideEffects) {
    check("debug.standardTraceBlockToFile", function () {
      var files = debug.standardTraceBlockToFile(sentBlock.hash, {});
      expect(files instanceof Array && files.length > 0, "missing standard trace output files");
      return files.length + " files";
    });
  } else {
    skip("debug.standardTraceBlockToFile", "writes standard trace files; set TEST_DEBUG_SIDE_EFFECTS=1 on disposable nodes");
  }
  skip("debug.standardTraceBadBlockToFile", "writes standard trace files and needs bad block");
  check("debug.accountRange", function () {
    var res = debug.accountRange("latest", "0x", 10, false, false, false);
    expect(res && res.root, "bad account range");
    return "accounts=" + Object.keys(res.accounts || {}).length;
  });
  check("debug.storageRangeAt", function () {
    var res = debug.storageRangeAt(sentBlock.hash, toNumber(sentReceipt.transactionIndex), account, "0x", 10);
    expect(res && res.storage !== undefined, "bad storage range");
    return "storage=" + Object.keys(res.storage || {}).length;
  });
  checkOrExpectedError("debug.getModifiedAccountsByNumber", function () {
    return debug.getModifiedAccountsByNumber(0, sentBlock.number);
  }, function (res) {
    expect(res === null || typeof res === "undefined" || res instanceof Array, "bad modified accounts by number");
  }, ["no preimage found"]);
  checkOrExpectedError("debug.getModifiedAccountsByHash", function () {
    var genesis = web3.qrl.getBlock(0, false).hash;
    return debug.getModifiedAccountsByHash(genesis, sentBlock.hash);
  }, function (res) {
    expect(res === null || typeof res === "undefined" || res instanceof Array, "bad modified accounts by hash");
  }, ["no preimage found"]);
  checkOrExpectedError("debug.getAccessibleState", function () { return debug.getAccessibleState(0, sentBlock.number); }, function (value) {
    expect(typeof value === "number" || typeof value === "string", "bad accessible state result");
  }, ["path-based scheme", "state history"]);

  if (testDestructiveDebug) {
    check("debug.setHead", function () {
      var head = web3.qrl.blockNumber;
      debug.setHead(web3.fromDecimal(head));
      expect(toNumber(web3.qrl.blockNumber) <= toNumber(head), "head did not stay at or below requested block");
      return "head=" + head;
    });
  } else {
    skip("debug.setHead", "destructive chain rewind; set TEST_DESTRUCTIVE_DEBUG=1 on disposable nodes");
  }

  assertCovered("web3", web3);
  assertCovered("qrl", web3.qrl);
  assertCovered("net", web3.net);
  assertCovered("version", web3.version);
  assertCovered("providers", web3.providers);
  assertCovered("admin", admin);
  assertCovered("debug", debug);
  assertCovered("txpool", txpool);
  if (hasDev) assertCovered("dev", dev);
  assertCovered("rpc", web3.rpc);

  var counts = {};
  for (var r = 0; r < results.length; r++) counts[results[r].kind] = (counts[results[r].kind] || 0) + 1;
  console.log("COVERAGE_SUMMARY " + stringify(counts));
  if (failures.length > 0) {
    throw new Error("web3 exhaustive console failures: " + failures.join("; "));
  }
})();
