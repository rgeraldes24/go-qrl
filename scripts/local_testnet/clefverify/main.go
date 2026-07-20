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
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

// clefverify validates the exact responses produced by the standalone Clef
// local-testnet scenario. Keeping the verifier in Go lets the E2E test use the
// same VM64 address, transaction and ML-DSA implementations as the node.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	var paths scenarioPaths
	flag.StringVar(&paths.seed, "seed", "", "hex-encoded ML-DSA-87 extended seed imported into Clef")
	flag.StringVar(&paths.account, "account", "", "Q-prefixed account returned by Clef importraw")
	flag.StringVar(&paths.versionResponse, "version-response", "", "account_version JSON-RPC response")
	flag.StringVar(&paths.listResponse, "list-response", "", "account_list JSON-RPC response")
	flag.StringVar(&paths.dataRequest, "data-request", "", "account_signData JSON-RPC request")
	flag.StringVar(&paths.dataResponse, "data-response", "", "account_signData JSON-RPC response")
	flag.StringVar(&paths.typedRequest, "typed-request", "", "account_signTypedData JSON-RPC request")
	flag.StringVar(&paths.typedResponse, "typed-response", "", "account_signTypedData JSON-RPC response")
	flag.StringVar(&paths.txRequest, "tx-request", "", "account_signTransaction JSON-RPC request")
	flag.StringVar(&paths.txResponse, "tx-response", "", "account_signTransaction JSON-RPC response")
	flag.Parse()

	if err := verifyScenarioFiles(paths); err != nil {
		fmt.Fprintf(os.Stderr, "clefverify: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("PASS: account_version and account_list returned the exact imported VM64 account")
	fmt.Println("PASS: account_signData returned an exact-width, cryptographically valid ML-DSA-87 signature")
	fmt.Println("PASS: account_signTypedData signed the expected QRL typed-data digest with ML-DSA-87")
	fmt.Println("PASS: account_signTransaction returned a consistent signed body with the recovered 64-byte sender and recipient")
	fmt.Println("SUITE clef_api: PASSED")
}
