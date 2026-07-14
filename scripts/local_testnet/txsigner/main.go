// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

// txsigner builds and signs a transaction from a raw ML-DSA-87 wallet seed
// against a running node. The gqrl console has no account management, so the
// local testnet test runner (scripts/local_testnet/run_tests.sh) uses this
// helper to pre-sign transactions from the prefunded dev accounts and feeds
// the result to qrl.sendRawTransaction inside the JavaScript suites.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/hexutil"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto/pqcrypto/wallet"
	"github.com/theQRL/go-qrl/qrlclient"
)

func main() {
	var (
		rpcURL = flag.String("rpc", "", "HTTP RPC endpoint of the node")
		seed   = flag.String("seed", "", "hex encoded ML-DSA-87 wallet seed")
		data   = flag.String("data", "", "hex encoded calldata (deployment bytecode when -to is empty)")
		to     = flag.String("to", "", "recipient address; empty for contract creation")
		value  = flag.String("value", "0", "value to transfer, in planck")
		format = flag.String("format", "json", `output format: "json" or "js" (a loadScript-able PARAMS file)`)
	)
	flag.Parse()
	if *rpcURL == "" || *seed == "" {
		fmt.Fprintln(os.Stderr, "usage: txsigner -rpc <url> -seed <hexseed> [-data <hex>] [-to <address>] [-value <planck>] [-format json|js]")
		os.Exit(2)
	}

	w, err := wallet.RestoreFromSeedHex(strings.TrimPrefix(*seed, "0x"))
	if err != nil {
		fatalf("invalid seed: %v", err)
	}
	from := common.Address(w.GetAddress())

	var toAddr *common.Address
	if *to != "" {
		addr, err := common.NewAddressFromString(*to)
		if err != nil {
			fatalf("invalid -to address: %v", err)
		}
		toAddr = &addr
	}
	payload, err := hexutil.Decode(*data)
	if *data != "" && err != nil {
		fatalf("invalid -data: %v", err)
	}
	amount, ok := new(big.Int).SetString(*value, 10)
	if !ok {
		fatalf("invalid -value: %q", *value)
	}

	ctx := context.Background()
	client, err := qrlclient.Dial(*rpcURL)
	if err != nil {
		fatalf("dial %s: %v", *rpcURL, err)
	}
	defer client.Close()

	chainID, err := client.ChainID(ctx)
	if err != nil {
		fatalf("chain id: %v", err)
	}
	balance, err := client.PendingBalanceAt(ctx, from)
	if err != nil {
		fatalf("balance of %s: %v", from.Hex(), err)
	}
	if balance.Sign() == 0 {
		fatalf("account %s has no funds; is it in the genesis prefunded accounts (network_params.yaml)?", from.Hex())
	}
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		fatalf("nonce of %s: %v", from.Hex(), err)
	}
	gasFeeCap, err := client.SuggestGasPrice(ctx)
	if err != nil {
		fatalf("gas price: %v", err)
	}
	gasTipCap, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		fatalf("gas tip: %v", err)
	}
	// Leave plenty of headroom for base fee fluctuations between signing and
	// inclusion; the account is a throwaway dev account.
	gasFeeCap = new(big.Int).Mul(gasFeeCap, big.NewInt(4))
	if gasFeeCap.Cmp(gasTipCap) < 0 {
		gasFeeCap = gasTipCap
	}
	gas, err := client.EstimateGas(ctx, qrl.CallMsg{
		From:  from,
		To:    toAddr,
		Value: amount,
		Data:  payload,
	})
	if err != nil {
		fatalf("estimate gas: %v", err)
	}
	gas += gas / 5 // 20% margin

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		GasTipCap: gasTipCap,
		GasFeeCap: gasFeeCap,
		Gas:       gas,
		To:        toAddr,
		Value:     amount,
		Data:      payload,
	})
	signed, err := types.SignTx(tx, types.LatestSignerForChainID(chainID), w)
	if err != nil {
		fatalf("sign: %v", err)
	}
	raw, err := signed.MarshalBinary()
	if err != nil {
		fatalf("encode: %v", err)
	}

	out, err := json.Marshal(map[string]any{
		"address":        from.Hex(),
		"balance":        balance.String(),
		"chainId":        chainID.String(),
		"nonce":          nonce,
		"txHash":         signed.Hash().Hex(),
		"rawTransaction": hexutil.Encode(raw),
	})
	if err != nil {
		fatalf("marshal output: %v", err)
	}
	switch *format {
	case "json":
		fmt.Println(string(out))
	case "js":
		fmt.Printf("var PARAMS = %s;\n", out)
	default:
		fatalf("unknown -format %q", *format)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "txsigner: "+format+"\n", args...)
	os.Exit(1)
}
