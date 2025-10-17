# Accepted QRL schemes

## Consensus/Tx

The consensus' cryptographic guarantees must be >= the strongest guarantees we offer at the transaction layer. If consensus is weaker, an adversary who can forge consensus signatures ca rewrite the history regardless of how strong your transaction signatures are.

Category parity(Cat-5)
Consensus scheme: ML-DSA-87
Accepted Tx schemes: ML-DSA-87 or SPHINCS+-256s

Best size/perf while matching cat-5: keeps block header/signature sizes reasonable (~4-5 KB per signature) while meeting Cat-5 parity with your strongest tx scheme. (SPHINCS+ Cat-5 signatures are ~30KB); greate for conservatism, but heavy for every consensus message).

## Descriptor 

We do not carry `descriptor` in the tx; it's derived from the type (and bound via Sender in the signature hash).

## Gas Model 

Charge like GTXDATANONZERO.
Two levers cover DoS risk: bandwidth and CPU. The simplest, robust choice is to charge auth bytes like calldata (EIP-2028), and-optionally-add a small per-type verify overhead constant if you want to reflect CPU.

Intrinsic gas formula: 

```
IntrinsicGas(tx) = 
    TX_BASE_GAS +                                // 21,000
    CALLDATA_GAS(data) +                         // 4 per zero byte, 16 per non-zero
    AUTH_GAS(tx.Type, len(pubkey) + len(sig)) +  // see below
    ACCESS_LIST_GAS(accesslist)
```

Auth gas (bytes-dominant):

```
AUTH_GAS(t, auth_bytes) = AUTH_BYTE_COST * auth_bytes + AUTH_VERIFY_GAS[t] 
```

- `AUTH_BYTE_COST` = 16 (match GTXDATANONZERO so random auth bytes pay like random calldata)

- `AUTH_VERIFY_GAS`: Optional - start with `AUTH_VERIFY_GAS[ML-DSA-87] = 0`, `AUTH_VERIFY_GAS[SPHINCS+256s] = 25,000`, then tune from node benchmarks. Keep it small relative to byte-cost so bandwidth remains the main driver.


### Concrete numbers

With the default constants from go-qrllib: 

- ML-DSA-87: `pk=2592`, `sig=4627` > `authBytes=7219` > **115,504** gas (plus base 21k and calldata/accesslist).
- SPHINCS+-256s: `pk=64`, `sig=29,792` > `authBytes=29,856` > **477,696** gas (plus base 21k and calldata/accesslist)

Capacity intuition (30M gas block):

- ~219 ML-DSA-87 simple transfers/block
- ~60 SPHINCS+256s simple transfers/block

## JSON-RPC & wallet surface

`qrl_estimateGas`: include the **auth-bytes** component in intrinsic gas.