// Copyright 2026 The go-ethereum Authors
// This file is part of the go-ethereum library.

package vm

import (
	"github.com/holiman/uint256"
	"github.com/theQRL/go-qrl/common"
)

const addressWords = common.AddressLength / common.HashLength

func pushAddress(stack *Stack, addr common.Address) {
	hi := new(uint256.Int).SetBytes(addr[:common.HashLength])
	lo := new(uint256.Int).SetBytes(addr[common.HashLength:])
	stack.push(hi)
	stack.push(lo)
}

func pushZeroAddress(stack *Stack) {
	stack.push(new(uint256.Int))
	stack.push(new(uint256.Int))
}

func popAddress(stack *Stack) common.Address {
	lo := stack.pop()
	hi := stack.pop()
	return addressFromWords(&hi, &lo)
}

func peekAddress(stack *Stack) common.Address {
	return addressFromStackBack(stack, 0)
}

func addressFromStackBack(stack *Stack, loBack int) common.Address {
	lo := stack.Back(loBack)
	hi := stack.Back(loBack + 1)
	return addressFromWords(hi, lo)
}

func addressFromWords(hi, lo *uint256.Int) common.Address {
	return AddressFromWords(hi, lo)
}

// AddressFromWords reassembles a 64-byte QRL address from high and low
// 32-byte VM words.
func AddressFromWords(hi, lo *uint256.Int) common.Address {
	var addr common.Address
	hiBytes := hi.Bytes32()
	loBytes := lo.Bytes32()
	copy(addr[:common.HashLength], hiBytes[:])
	copy(addr[common.HashLength:], loBytes[:])
	return addr
}
