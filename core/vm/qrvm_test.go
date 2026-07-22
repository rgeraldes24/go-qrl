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

package vm

import (
	"math/big"
	"testing"

	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/common/uint512"
	"github.com/theQRL/go-qrl/core/rawdb"
	"github.com/theQRL/go-qrl/core/state"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/params"
)

var codeStoreOutOfGasInitCode = []byte{
	byte(PUSH1), 1,
	byte(PUSH1), 0,
	byte(RETURN),
}

func newCodeStoreOutOfGasTestVM(t *testing.T, creator common.Address) (*QRVM, *state.StateDB) {
	t.Helper()

	statedb, err := state.New(types.EmptyRootHash, state.NewDatabase(rawdb.NewMemoryDatabase()), nil)
	if err != nil {
		t.Fatal(err)
	}
	statedb.CreateAccount(creator)
	statedb.AddBalance(creator, big.NewInt(10))

	blockContext := BlockContext{
		CanTransfer: func(db StateDB, address common.Address, amount *big.Int) bool {
			return db.GetBalance(address).Cmp(amount) >= 0
		},
		Transfer: func(db StateDB, sender, recipient common.Address, amount *big.Int) {
			db.SubBalance(sender, amount)
			db.AddBalance(recipient, amount)
		},
		BlockNumber: big.NewInt(0),
	}
	return NewQRVM(blockContext, TxContext{}, statedb, params.AllBeaconProtocolChanges, Config{}), statedb
}

func assertFailedCreationState(t *testing.T, statedb *state.StateDB, creator, contract common.Address) {
	t.Helper()

	if nonce := statedb.GetNonce(creator); nonce != 1 {
		t.Fatalf("creator nonce mismatch: have %d, want 1", nonce)
	}
	if balance := statedb.GetBalance(creator); balance.Cmp(big.NewInt(10)) != 0 {
		t.Fatalf("creator balance mismatch: have %v, want 10", balance)
	}
	if statedb.Exist(contract) {
		t.Fatal("failed creation left the contract account in state")
	}
}

func TestCreateCodeStoreOutOfGas(t *testing.T) {
	creator := common.BytesToAddress([]byte("creator"))
	qrvm, statedb := newCodeStoreOutOfGasTestVM(t, creator)

	_, address, leftOverGas, err := qrvm.Create(AccountRef(creator), codeStoreOutOfGasInitCode, 199, big.NewInt(7))
	if err != ErrCodeStoreOutOfGas {
		t.Fatalf("error mismatch: have %v, want %v", err, ErrCodeStoreOutOfGas)
	}
	if leftOverGas != 0 {
		t.Fatalf("leftover gas mismatch: have %d, want 0", leftOverGas)
	}
	assertFailedCreationState(t, statedb, creator, address)
}

func TestCreateOpcodesCodeStoreOutOfGas(t *testing.T) {
	creator := common.BytesToAddress([]byte("creator"))
	salt := new(uint512.Int).SetUint64(1)
	const initialGas = uint64(199)

	tests := []struct {
		name            string
		execute         executionFunc
		salt            *uint512.Int
		contractAddress common.Address
	}{
		{"CREATE", opCreate, nil, crypto.CreateAddress(creator, 0)},
		{"CREATE2", opCreate2, salt, crypto.CreateAddress2(creator, salt.Bytes64(), crypto.Keccak256(codeStoreOutOfGasInitCode))},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			qrvm, statedb := newCodeStoreOutOfGasTestVM(t, creator)
			stack := newstack()
			defer returnStack(stack)
			if test.salt != nil {
				stack.push(test.salt)
			}
			stack.push(new(uint512.Int).SetUint64(uint64(len(codeStoreOutOfGasInitCode))))
			stack.push(new(uint512.Int))
			stack.push(new(uint512.Int).SetUint64(7))
			memory := NewMemory()
			memory.Resize(uint64(len(codeStoreOutOfGasInitCode)))
			memory.Set(0, uint64(len(codeStoreOutOfGasInitCode)), codeStoreOutOfGasInitCode)
			contract := NewContract(AccountRef(common.Address{}), AccountRef(creator), new(big.Int), initialGas)
			pc := uint64(0)

			if _, err := test.execute(&pc, qrvm.interpreter, &ScopeContext{Memory: memory, Stack: stack, Contract: contract}); err != nil {
				t.Fatal(err)
			}
			if result := stack.pop(); !result.IsZero() {
				t.Fatalf("result mismatch: have %v, want 0", &result)
			}
			if contract.Gas != initialGas/64 {
				t.Fatalf("parent gas mismatch: have %d, want %d", contract.Gas, initialGas/64)
			}
			assertFailedCreationState(t, statedb, creator, test.contractAddress)
		})
	}
}
