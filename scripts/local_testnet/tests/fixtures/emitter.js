// Precompiled fixture for event_roundtrip.js.
//
// Hyperion source (compile with hypc from github.com/cyyber/hyperion,
// built at commit f2e6ae7a59e8dafc23a2f34164fdd26180cec2dd):
//
//     // SPDX-License-Identifier: LGPL-3.0
//     pragma hyperion >=0.1.0;
//
//     contract EventEmitter {
//         event Deployed(uint256 value);
//
//         constructor() {
//             emit Deployed(1337);
//         }
//     }
//
// Regenerate with: hypc --bin --abi EventEmitter.hyp
var EMITTER = {
  signature: "Deployed(uint256)",
  value: 1337,
  abi: [{"inputs":[],"stateMutability":"nonpayable","type":"constructor"},{"anonymous":false,"inputs":[{"indexed":false,"internalType":"uint256","name":"value","type":"uint256"}],"name":"Deployed","type":"event"}],
  bin: "0x61010060805234a015600f575fa0fd5b509fb94ae47ec9f4248692e2ecf9740b67ab493f3dcc8452bedc7d9cd911c28d1ca50000000000000000000000000000000000000000000000000000000000000000610539608051605fb1b060d0565b608051a0b103b0c160e7565b5fa1b050b1b050565b5f7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffa216b050b1b050565b5fa1b050b1b050565b5f60bc60b860b4a4606b565b609f565b6074565bb050b1b050565b60caa160a8565ba2525050565b5f6040a201b05060e15fa301a460c3565bb2b15050565b6063a06100f35f395ff3fe6101006080525fa0fdfea2646970667358221220c4656c9f7b30275bbd5d53e34095e42408ccc42edf082781f00e9608fb5094b164687970637826302e322e302d646576656c6f702e323032362e372e382b636f6d6d69742e66326536616537610057"
};
