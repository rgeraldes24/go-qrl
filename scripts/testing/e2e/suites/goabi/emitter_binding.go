// Code generated - DO NOT EDIT.
// This file is a generated binding and any manual changes will be lost.

package goabi

import (
	"errors"
	"math/big"
	"strings"

	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/accounts/abi"
	"github.com/theQRL/go-qrl/accounts/abi/bind"
	"github.com/theQRL/go-qrl/common"
	"github.com/theQRL/go-qrl/core/types"
	"github.com/theQRL/go-qrl/event"
)

// Reference imports to suppress errors if they are not otherwise used.
var (
	_ = errors.New
	_ = big.NewInt
	_ = strings.NewReader
	_ = qrl.NotFound
	_ = bind.Bind
	_ = common.Big1
	_ = types.BloomLookup
	_ = event.NewSubscription
	_ = abi.ConvertType
)

// EventEmitterRecord is an auto generated low-level Go binding around an user-defined struct.
type EventEmitterRecord struct {
	Amount    *big.Int
	Recipient common.Address
	Tag       [64]byte
}

// EventEmitterMetaData contains all meta data concerning the EventEmitter contract.
var EventEmitterMetaData = &bind.MetaData{
	ABI: "[{\"inputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"constructor\"},{\"anonymous\":true,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"uint512\",\"name\":\"amount\",\"type\":\"uint512\"},{\"indexed\":true,\"internalType\":\"bytes64\",\"name\":\"tag\",\"type\":\"bytes64\"},{\"indexed\":true,\"internalType\":\"bool\",\"name\":\"enabled\",\"type\":\"bool\"}],\"name\":\"AnonymousStored\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":false,\"internalType\":\"uint256\",\"name\":\"value\",\"type\":\"uint256\"}],\"name\":\"Deployed\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"bytes\",\"name\":\"payload\",\"type\":\"bytes\"},{\"indexed\":true,\"internalType\":\"string\",\"name\":\"note\",\"type\":\"string\"},{\"indexed\":false,\"internalType\":\"uint512\",\"name\":\"amount\",\"type\":\"uint512\"}],\"name\":\"Dynamic\",\"type\":\"event\"},{\"anonymous\":false,\"inputs\":[{\"indexed\":true,\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"indexed\":true,\"internalType\":\"uint512\",\"name\":\"amount\",\"type\":\"uint512\"},{\"indexed\":true,\"internalType\":\"int512\",\"name\":\"delta\",\"type\":\"int512\"},{\"indexed\":false,\"internalType\":\"bytes64\",\"name\":\"tag\",\"type\":\"bytes64\"},{\"indexed\":false,\"internalType\":\"bytes\",\"name\":\"payload\",\"type\":\"bytes\"},{\"indexed\":false,\"internalType\":\"string\",\"name\":\"note\",\"type\":\"string\"},{\"indexed\":false,\"internalType\":\"bool\",\"name\":\"enabled\",\"type\":\"bool\"}],\"name\":\"Stored\",\"type\":\"event\"},{\"inputs\":[],\"name\":\"clear\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint512\",\"name\":\"amount\",\"type\":\"uint512\"},{\"internalType\":\"int512\",\"name\":\"delta\",\"type\":\"int512\"},{\"internalType\":\"bytes64\",\"name\":\"tag\",\"type\":\"bytes64\"},{\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"internalType\":\"bytes\",\"name\":\"payload\",\"type\":\"bytes\"},{\"internalType\":\"string\",\"name\":\"note\",\"type\":\"string\"},{\"internalType\":\"bool\",\"name\":\"enabled\",\"type\":\"bool\"}],\"name\":\"echo\",\"outputs\":[{\"internalType\":\"uint512\",\"name\":\"\",\"type\":\"uint512\"},{\"internalType\":\"int512\",\"name\":\"\",\"type\":\"int512\"},{\"internalType\":\"bytes64\",\"name\":\"\",\"type\":\"bytes64\"},{\"internalType\":\"address\",\"name\":\"\",\"type\":\"address\"},{\"internalType\":\"bytes\",\"name\":\"\",\"type\":\"bytes\"},{\"internalType\":\"string\",\"name\":\"\",\"type\":\"string\"},{\"internalType\":\"bool\",\"name\":\"\",\"type\":\"bool\"}],\"stateMutability\":\"pure\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint512[]\",\"name\":\"values\",\"type\":\"uint512[]\"},{\"internalType\":\"bytes64[2]\",\"name\":\"tags\",\"type\":\"bytes64[2]\"}],\"name\":\"echoArrays\",\"outputs\":[{\"internalType\":\"uint512[]\",\"name\":\"\",\"type\":\"uint512[]\"},{\"internalType\":\"bytes64[2]\",\"name\":\"\",\"type\":\"bytes64[2]\"}],\"stateMutability\":\"pure\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"bytes1\",\"name\":\"value1\",\"type\":\"bytes1\"},{\"internalType\":\"bytes32\",\"name\":\"value32\",\"type\":\"bytes32\"},{\"internalType\":\"bytes33\",\"name\":\"value33\",\"type\":\"bytes33\"},{\"internalType\":\"bytes64\",\"name\":\"value64\",\"type\":\"bytes64\"}],\"name\":\"echoFixed\",\"outputs\":[{\"internalType\":\"bytes1\",\"name\":\"\",\"type\":\"bytes1\"},{\"internalType\":\"bytes32\",\"name\":\"\",\"type\":\"bytes32\"},{\"internalType\":\"bytes33\",\"name\":\"\",\"type\":\"bytes33\"},{\"internalType\":\"bytes64\",\"name\":\"\",\"type\":\"bytes64\"}],\"stateMutability\":\"pure\",\"type\":\"function\"},{\"inputs\":[{\"components\":[{\"internalType\":\"uint512\",\"name\":\"amount\",\"type\":\"uint512\"},{\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"internalType\":\"bytes64\",\"name\":\"tag\",\"type\":\"bytes64\"}],\"internalType\":\"structEventEmitter.Record\",\"name\":\"record\",\"type\":\"tuple\"}],\"name\":\"echoRecord\",\"outputs\":[{\"components\":[{\"internalType\":\"uint512\",\"name\":\"amount\",\"type\":\"uint512\"},{\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"internalType\":\"bytes64\",\"name\":\"tag\",\"type\":\"bytes64\"}],\"internalType\":\"structEventEmitter.Record\",\"name\":\"\",\"type\":\"tuple\"}],\"stateMutability\":\"pure\",\"type\":\"function\"},{\"inputs\":[],\"name\":\"read\",\"outputs\":[{\"internalType\":\"uint512\",\"name\":\"amount\",\"type\":\"uint512\"},{\"internalType\":\"int512\",\"name\":\"delta\",\"type\":\"int512\"},{\"internalType\":\"bytes64\",\"name\":\"tag\",\"type\":\"bytes64\"},{\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"internalType\":\"bytes\",\"name\":\"payload\",\"type\":\"bytes\"},{\"internalType\":\"string\",\"name\":\"note\",\"type\":\"string\"},{\"internalType\":\"bool\",\"name\":\"enabled\",\"type\":\"bool\"}],\"stateMutability\":\"view\",\"type\":\"function\"},{\"inputs\":[{\"internalType\":\"uint512\",\"name\":\"amount\",\"type\":\"uint512\"},{\"internalType\":\"int512\",\"name\":\"delta\",\"type\":\"int512\"},{\"internalType\":\"bytes64\",\"name\":\"tag\",\"type\":\"bytes64\"},{\"internalType\":\"address\",\"name\":\"recipient\",\"type\":\"address\"},{\"internalType\":\"bytes\",\"name\":\"payload\",\"type\":\"bytes\"},{\"internalType\":\"string\",\"name\":\"note\",\"type\":\"string\"},{\"internalType\":\"bool\",\"name\":\"enabled\",\"type\":\"bool\"}],\"name\":\"store\",\"outputs\":[],\"stateMutability\":\"nonpayable\",\"type\":\"function\"}]",
	Bin: "0x61010060805234a01562000011575fa0fd5b509fb94ae47ec9f4248692e2ecf9740b67ab493f3dcc8452bedc7d9cd911c28d1ca5000000000000000000000000000000000000000000000000000000000000000061053960805162000065b1b0620000e8565b608051a0b103b0c162000103565b5fa1b050b1b050565b5f7fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffa216b050b1b050565b5fa1b050b1b050565b5f620000d0620000ca620000c4a462000073565b620000a7565b6200007c565bb050b1b050565b620000e2a1620000b0565ba2525050565b5f6040a201b050620000fd5fa301a4620000d7565bb2b15050565b6117e5a0620001115f395ff3fe61010060805234a015610010575fa0fd5b506004361061007d575f356101e01ca063563d38a11161005b57a063563d38a1146100dd57a06357de26a41461011057a06364f3072f1461013457a063eae49aa9146101645761007d565ba0633d0e10891461008157a0634b79d0e31461009d57a06352efea6e146100d3575b5fa0fd5b61009b6004a03603a101b0610096b1b0610a0c565b610195565b005b6100b76004a03603a101b06100b2b1b0610a0c565b6102fc565b6080516100cab7b6b5b4b3b2b1b0610c29565b608051a0b103b0f35b6100db610406565b005b6100f76004a03603a101b06100f2b1b0610e07565b610445565b608051610107b4b3b2b1b0610e99565b608051a0b103b0f35b610118610460565b60805161012bb7b6b5b4b3b2b1b0610c29565b608051a0b103b0f35b61014e6004a03603a101b0610149b1b0610eff565b610639565b60805161015bb1b0610f97565b608051a0b103b0f35b61017e6004a03603a101b0610179b1b0611026565b610659565b60805161018cb2b1b06111c1565b608051a0b103b0f35ba85fa1b05550a76001a1b05550a66002a1b05550a56003a1b05550a4a46004b1a26101c1b2b1b0611365565b50a2a26005b1a26101d3b2b1b0611494565b50a060065f6101000aa154a160ff021916b0a315150217b05550a7a9a79f0971a927eb69632cd5aced366c9dd3ee5626b6c0a27cb781139eeffab9e5372f0000000000000000000000000000000000000000000000000000000000000000aaa9a9a9a9a9608051610249b6b5b4b3b2b1b06115c7565b608051a0b103b0c4a2a2608051610261b2b1b061164b565b608051a0b103b0206101001ba5a560805161027db2b1b0611691565b608051a0b103b0206101001b9f4ef7447df163d4aaeab9c66fa93651de5eebb002dcf9b60da1ebaa28ae95e8250000000000000000000000000000000000000000000000000000000000000000ab6080516102d8b1b06116a9565b608051a0b103b0c3a01515a7aaa8608051608051a0b103b0c4505050505050505050565b5fa05fa060c0a05fafafafafafafafafafa4a4a0a0603f016040a0b10402604001608051b0a101608052a0b3b2b1b0a17fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff16a152604001a3a3a0a2a4375fa1a40152603f19603fa20116b050a0a301b250505050505050b350b0b1b2b350a2a2a0a0603f016040a0b10402604001608051b0a101608052a0b3b2b1b0a17fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff16a152604001a3a3a0a2a4375fa1a40152603f19603fa20116b050a0a301b250505050505050b150b0b150b650b650b650b650b650b650b650b950b950b950b950b950b950b9b2505050565b5fa05560015fb05560025fb05560035fb05560045f610425b1b0610723565b60055f610432b1b0610782565b60065f6101000aa154b060ff021916b055565b5fa05fa0a7a7a7a7b350b350b350b350b450b450b450b4b050565b5fa05fa060c0a05fa0546001546002546003546004600560065fb054b06101000ab00460ff16a2a054610492b0611270565ba0603f016040a0b10402604001608051b0a101608052a0b2b1b0a17fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff16a152604001a2a0546104e0b0611270565ba01561054d57a0603f1061050257610100a0a3540402a352b1604001b161054d565ba201b1b07fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff165f5260405f20b05ba154a152b0600101b0604001a0a31161053057a2b003603f16a201b15b5050505050b250a1a054610560b0611270565ba0603f016040a0b10402604001608051b0a101608052a0b2b1b0a17fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff16a152604001a2a0546105aeb0611270565ba01561061b57a0603f106105d057610100a0a3540402a352b1604001b161061b565ba201b1b07fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff165f5260405f20b05ba154a152b0600101b0604001a0a3116105fe57a2b003603f16a201b15b5050505050b150b650b650b650b650b650b650b650b0b1b2b3b4b5b6565b6106416107e1565ba1a03603a101b0610652b1b06117ba565bb050b1b050565b60c0610663610802565ba4a4a4a2a2a0a0604002604001608051b0a101608052a0b3b2b1b0a17fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff16a152604001a3a35fb25ba1a410156106c957a235a152604001b1604001b1b2600101b26106ab565bb250505050505050b150b0b150a06002a0604002608051b0a101608052a0b2b1b0a260025fb25ba1a4101561070e57a235a152604001b1604001b1b2600101b26106f0565bb2505050505050b050b150b150b350b3b15050565b50a05461072fb0611270565b5fa255a0603f10610740575061077f565b603f016040b004b07fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff165f5260405f20b0a101b061077eb1b0610824565b5b50565b50a05461078eb0611270565b5fa255a0603f1061079f57506107de565b603f016040b004b07fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff165f5260405f20b0a101b06107ddb1b0610824565b5b50565b608051a060c001608052a05fa1526040015fa1526040015fa01916a15250b0565b608051a0608001608052a06002b06040a202a036a337a0a201b15050b05050b0565b5ba0a21115610838575fa155600101610825565b50b0565b5f608051b050b0565b5fa0fd5b5fa0fd5b5fa1b050b1b050565b61085fa161084d565ba114610869575fa0fd5b50565b5fa135b05061087aa1610856565bb2b15050565b5fa1b050b1b050565b610892a1610880565ba11461089c575fa0fd5b50565b5fa135b0506108ada1610889565bb2b15050565b5fa1b050b1b050565b6108c5a16108b3565ba1146108cf575fa0fd5b50565b5fa135b0506108e0a16108bc565bb2b15050565b5f6108f0a261084d565bb050b1b050565b610900a16108e6565ba11461090a575fa0fd5b50565b5fa135b05061091ba16108f7565bb2b15050565b5fa0fd5b5fa0fd5b5fa0fd5b5fa0a3603fa4011261094257610941610921565b5ba235b05067ffffffffffffffffa1111561095f5761095e610925565b5b6040a301b150a36001a202a301111561097b5761097a610929565b5bb250b2b050565b5fa0a3603fa4011261099757610996610921565b5ba235b05067ffffffffffffffffa111156109b4576109b3610925565b5b6040a301b150a36001a202a30111156109d0576109cf610929565b5bb250b2b050565b5fa11515b050b1b050565b6109eba16109d7565ba1146109f5575fa0fd5b50565b5fa135b050610a06a16109e2565bb2b15050565b5fa05fa05fa05fa05f6101c0aaac031215610a2a57610a29610845565b5b5f610a37aca2ad0161086c565bb950506040610a48aca2ad0161089f565bb850506080610a59aca2ad016108d2565bb7505060c0610a6aaca2ad0161090d565bb65050610100aa013567ffffffffffffffffa11115610a8c57610a8b610849565b5b610a98aca2ad0161092d565bb550b55050610140aa013567ffffffffffffffffa11115610abc57610abb610849565b5b610ac8aca2ad01610982565bb350b35050610180610adcaca2ad016109f8565bb15050b2b5b850b2b5b850b2b5b8565b610af5a161084d565ba2525050565b610b04a1610880565ba2525050565b610b13a16108b3565ba2525050565b610b22a16108e6565ba2525050565b5fa151b050b1b050565b5fa2a2526040a201b050b2b15050565b5f5ba3a11015610b5f57a0a20151a1a401526040a101b050610b44565ba35ba1a11015610b79575fa1a501536001a101b050610b61565b5050505050565b5f603f19603fa30116b050b1b050565b5f610b9aa2610b28565b610ba4a1a5610b32565bb350610bb4a1a56040a601610b42565b610bbda1610b80565ba401b15050b2b15050565b5fa151b050b1b050565b5fa2a2526040a201b050b2b15050565b5f610beca2610bc8565b610bf6a1a5610bd2565bb350610c06a1a56040a601610b42565b610c0fa1610b80565ba401b15050b2b15050565b610c23a16109d7565ba2525050565b5f6101c0a201b050610c3d5fa301aa610aec565b610c4a6040a301a9610afb565b610c576080a301a8610b0a565b610c6460c0a301a7610b19565ba1a103610100a30152610c77a1a6610b90565bb050a1a103610140a30152610c8ca1a5610be2565bb050610c9c610180a301a4610c1a565bb8b75050505050505050565b5f9fff000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000a216b050b1b050565b610cfca1610ca8565ba114610d06575fa0fd5b50565b5fa135b050610d17a1610cf3565bb2b15050565b5f9fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff0000000000000000000000000000000000000000000000000000000000000000a216b050b1b050565b610d71a1610d1d565ba114610d7b575fa0fd5b50565b5fa135b050610d8ca1610d68565bb2b15050565b5f9fffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff00000000000000000000000000000000000000000000000000000000000000a216b050b1b050565b610de6a1610d92565ba114610df0575fa0fd5b50565b5fa135b050610e01a1610ddd565bb2b15050565b5fa05fa0610100a5a7031215610e2057610e1f610845565b5b5f610e2da7a2a801610d09565bb450506040610e3ea7a2a801610d7e565bb350506080610e4fa7a2a801610df3565bb2505060c0610e60a7a2a8016108d2565bb15050b2b5b1b450b250565b610e75a1610ca8565ba2525050565b610e84a1610d1d565ba2525050565b610e93a1610d92565ba2525050565b5f610100a201b050610ead5fa301a7610e6c565b610eba6040a301a6610e7b565b610ec76080a301a5610e8a565b610ed460c0a301a4610b0a565bb5b45050505050565b5fa0fd5b5f60c0a2a4031215610ef657610ef5610edd565b5ba1b050b2b15050565b5f60c0a2a4031215610f1457610f13610845565b5b5f610f21a4a2a501610ee1565bb15050b2b15050565b610f33a161084d565ba2525050565b610f42a16108e6565ba2525050565b610f51a16108b3565ba2525050565b60c0a2015fa20151610f6b5fa501a2610f2a565b506040a20151610f7e6040a501a2610f39565b506080a20151610f916080a501a2610f48565b50505050565b5f60c0a201b050610faa5fa301a4610f57565bb2b15050565b5fa0a3603fa40112610fc557610fc4610921565b5ba235b05067ffffffffffffffffa11115610fe257610fe1610925565b5b6040a301b150a36040a202a3011115610ffe57610ffd610929565b5bb250b2b050565b5fa1b050a26040600202a20111156110205761101f610929565b5bb2b15050565b5fa05f60c0a4a603121561103d5761103c610845565b5b5fa4013567ffffffffffffffffa1111561105a57611059610849565b5b611066a6a2a701610fb0565bb350b350506040611079a6a2a701611005565bb15050b250b250b2565b5fa151b050b1b050565b5fa2a2526040a201b050b2b15050565b5fa1b0506040a201b050b1b050565b5f6110b7a3a3610f2a565b6040a301b050b2b15050565b5f6040a201b050b1b050565b5f6110d9a2611083565b6110e3a1a561108d565bb3506110eea361109d565ba05f5ba3a1101561111e57a151611105a8a26110ac565bb750611110a36110c3565bb250506001a101b0506110f1565b50a5b350505050b2b15050565b5f6002b050b1b050565b5fa1b050b2b15050565b5fa1b050b1b050565b5f611153a3a3610f48565b6040a301b050b2b15050565b5f6040a201b050b1b050565b611174a161112b565b61117ea1a4611135565bb250611189a261113f565ba05f5ba3a110156111b957a1516111a0a7a2611148565bb6506111aba361115f565bb250506001a101b05061118c565b505050505050565b5f60c0a201b050a1a1035fa301526111d9a1a56110cf565bb0506111e86040a301a461116b565bb3b2505050565b5fa2b050b2b15050565b5fa16101001bb050b1b050565b61122f7f4e487b71000000000000000000000000000000000000000000000000000000006111f9565b5f52604160045260445ffd5b6112647f4e487b71000000000000000000000000000000000000000000000000000000006111f9565b5f52602260045260445ffd5b5f6002a204b0506001a216a061128757607fa216b1505b6040a210a10361129a5761129961123b565b5b50b1b050565b5fa1b050a15f5260405f20b050b1b050565b5f6040603fa30104b050b1b050565b5ba1a110156112d8575fa1556001a101b0506112c2565b5050565b603fa2111561131d576112eea16112a0565b6112f7a46112b2565ba1016040a5101561130657a1b0505b61131a611312a56112b2565ba301a26112c1565b50505b505050565b5fa2a21cb050b2b15050565b5f61133d5f19a4600802611322565b19a0a316b15050b2b15050565b5f611355a3a361132e565bb150a2600202a217b050b2b15050565b61136fa3a36111ef565b67ffffffffffffffffa1111561138857611387611206565b5b611392a254611270565b61139da2a2a56112dc565b5f603fa3116001a1146113ca575fa4156113b857a2a70135b0505b6113c2a5a261134a565ba65550611429565b603f19a4166113d8a66112a0565b5f5ba2a110156113ff57a4a90135a2556001a201b1506040a501b4506040a101b0506113da565ba6a3101561141c57a4a90135611418603fa916a261132e565ba355505b60016002a80201a8555050505b50505050505050565b5fa2b050b2b15050565b5fa1b050a15f5260405f20b050b1b050565b603fa2111561148f57611460a161143c565b611469a46112b2565ba1016040a5101561147857a1b0505b61148c611484a56112b2565ba301a26112c1565b50505b505050565b61149ea3a3611432565b67ffffffffffffffffa111156114b7576114b6611206565b5b6114c1a254611270565b6114cca2a2a561144e565b5f603fa3116001a1146114f9575fa4156114e757a2a70135b0505b6114f1a5a261134a565ba65550611558565b603f19a416611507a661143c565b5f5ba2a1101561152e57a4a90135a2556001a201b1506040a501b4506040a101b050611509565ba6a3101561154b57a4a90135611547603fa916a261132e565ba355505b60016002a80201a8555050505b50505050505050565ba2a1a3375fa3a30152505050565b5f61157aa3a5610b32565bb350611587a3a5a4611561565b611590a3610b80565ba401b050b3b2505050565b5f6115a6a3a5610bd2565bb3506115b3a3a5a4611561565b6115bca3610b80565ba401b050b3b2505050565b5f610100a201b0506115db5fa301a9610b0a565ba1a1036040a301526115eea1a7a961156f565bb050a1a1036080a30152611603a1a5a761159b565bb05061161260c0a301a4610c1a565bb7b650505050505050565b5fa1b050b2b15050565b5f611632a3a561161d565bb35061163fa3a5a4611561565ba2a401b050b3b2505050565b5f611657a2a4a6611627565bb150a1b050b3b2505050565b5fa1b050b2b15050565b5f611678a3a5611663565bb350611685a3a5a4611561565ba2a401b050b3b2505050565b5f61169da2a4a661166d565bb150a1b050b3b2505050565b5f6040a201b0506116bc5fa301a4610aec565bb2b15050565b5fa0fd5b6116cfa2610b80565ba101a1a11067ffffffffffffffffa21117156116ee576116ed611206565b5ba0608052505050565b5f61170061083c565bb05061170ca2a26116c6565bb1b050565b61171aa261084d565ba1525050565b611729a26108e6565ba1525050565b611738a26108b3565ba1525050565b5f60c0a2a4031215611753576117526116c2565b5b61175d60c06116f7565bb0505f61176ca4a2a50161086c565b611778a15fa501611711565b50506040611788a4a2a50161090d565b611795a16040a501611720565b505060806117a5a4a2a5016108d2565b6117b2a16080a50161172f565b5050b2b15050565b5f60c0a2a40312156117cf576117ce610845565b5b5f6117dca4a2a50161173e565bb15050b2b1505056",
}

// EventEmitterABI is the input ABI used to generate the binding from.
// Deprecated: Use EventEmitterMetaData.ABI instead.
var EventEmitterABI = EventEmitterMetaData.ABI

// EventEmitterBin is the compiled bytecode used for deploying new contracts.
// Deprecated: Use EventEmitterMetaData.Bin instead.
var EventEmitterBin = EventEmitterMetaData.Bin

// DeployEventEmitter deploys a new QRL contract, binding an instance of EventEmitter to it.
func DeployEventEmitter(auth *bind.TransactOpts, backend bind.ContractBackend) (common.Address, *types.Transaction, *EventEmitter, error) {
	parsed, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	if parsed == nil {
		return common.Address{}, nil, nil, errors.New("GetABI returned nil")
	}

	address, tx, contract, err := bind.DeployContract(auth, *parsed, common.FromHex(EventEmitterBin), backend)
	if err != nil {
		return common.Address{}, nil, nil, err
	}
	return address, tx, &EventEmitter{EventEmitterCaller: EventEmitterCaller{contract: contract}, EventEmitterTransactor: EventEmitterTransactor{contract: contract}, EventEmitterFilterer: EventEmitterFilterer{contract: contract}}, nil
}

// EventEmitter is an auto generated Go binding around a QRL contract.
type EventEmitter struct {
	EventEmitterCaller     // Read-only binding to the contract
	EventEmitterTransactor // Write-only binding to the contract
	EventEmitterFilterer   // Log filterer for contract events
}

// EventEmitterCaller is an auto generated read-only Go binding around a QRL contract.
type EventEmitterCaller struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// EventEmitterTransactor is an auto generated write-only Go binding around a QRL contract.
type EventEmitterTransactor struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// EventEmitterFilterer is an auto generated log filtering Go binding around a QRL contract events.
type EventEmitterFilterer struct {
	contract *bind.BoundContract // Generic contract wrapper for the low level calls
}

// EventEmitterSession is an auto generated Go binding around a QRL contract,
// with pre-set call and transact options.
type EventEmitterSession struct {
	Contract     *EventEmitter     // Generic contract binding to set the session for
	CallOpts     bind.CallOpts     // Call options to use throughout this session
	TransactOpts bind.TransactOpts // Transaction auth options to use throughout this session
}

// EventEmitterCallerSession is an auto generated read-only Go binding around a QRL contract,
// with pre-set call options.
type EventEmitterCallerSession struct {
	Contract *EventEmitterCaller // Generic contract caller binding to set the session for
	CallOpts bind.CallOpts       // Call options to use throughout this session
}

// EventEmitterTransactorSession is an auto generated write-only Go binding around a QRL contract,
// with pre-set transact options.
type EventEmitterTransactorSession struct {
	Contract     *EventEmitterTransactor // Generic contract transactor binding to set the session for
	TransactOpts bind.TransactOpts       // Transaction auth options to use throughout this session
}

// EventEmitterRaw is an auto generated low-level Go binding around a QRL contract.
type EventEmitterRaw struct {
	Contract *EventEmitter // Generic contract binding to access the raw methods on
}

// EventEmitterCallerRaw is an auto generated low-level read-only Go binding around a QRL contract.
type EventEmitterCallerRaw struct {
	Contract *EventEmitterCaller // Generic read-only contract binding to access the raw methods on
}

// EventEmitterTransactorRaw is an auto generated low-level write-only Go binding around a QRL contract.
type EventEmitterTransactorRaw struct {
	Contract *EventEmitterTransactor // Generic write-only contract binding to access the raw methods on
}

// NewEventEmitter creates a new instance of EventEmitter, bound to a specific deployed contract.
func NewEventEmitter(address common.Address, backend bind.ContractBackend) (*EventEmitter, error) {
	contract, err := bindEventEmitter(address, backend, backend, backend)
	if err != nil {
		return nil, err
	}
	return &EventEmitter{EventEmitterCaller: EventEmitterCaller{contract: contract}, EventEmitterTransactor: EventEmitterTransactor{contract: contract}, EventEmitterFilterer: EventEmitterFilterer{contract: contract}}, nil
}

// NewEventEmitterCaller creates a new read-only instance of EventEmitter, bound to a specific deployed contract.
func NewEventEmitterCaller(address common.Address, caller bind.ContractCaller) (*EventEmitterCaller, error) {
	contract, err := bindEventEmitter(address, caller, nil, nil)
	if err != nil {
		return nil, err
	}
	return &EventEmitterCaller{contract: contract}, nil
}

// NewEventEmitterTransactor creates a new write-only instance of EventEmitter, bound to a specific deployed contract.
func NewEventEmitterTransactor(address common.Address, transactor bind.ContractTransactor) (*EventEmitterTransactor, error) {
	contract, err := bindEventEmitter(address, nil, transactor, nil)
	if err != nil {
		return nil, err
	}
	return &EventEmitterTransactor{contract: contract}, nil
}

// NewEventEmitterFilterer creates a new log filterer instance of EventEmitter, bound to a specific deployed contract.
func NewEventEmitterFilterer(address common.Address, filterer bind.ContractFilterer) (*EventEmitterFilterer, error) {
	contract, err := bindEventEmitter(address, nil, nil, filterer)
	if err != nil {
		return nil, err
	}
	return &EventEmitterFilterer{contract: contract}, nil
}

// bindEventEmitter binds a generic wrapper to an already deployed contract.
func bindEventEmitter(address common.Address, caller bind.ContractCaller, transactor bind.ContractTransactor, filterer bind.ContractFilterer) (*bind.BoundContract, error) {
	parsed, err := EventEmitterMetaData.GetAbi()
	if err != nil {
		return nil, err
	}
	return bind.NewBoundContract(address, *parsed, caller, transactor, filterer), nil
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_EventEmitter *EventEmitterRaw) Call(opts *bind.CallOpts, result *[]any, method string, params ...any) error {
	return _EventEmitter.Contract.EventEmitterCaller.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_EventEmitter *EventEmitterRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _EventEmitter.Contract.EventEmitterTransactor.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_EventEmitter *EventEmitterRaw) Transact(opts *bind.TransactOpts, method string, params ...any) (*types.Transaction, error) {
	return _EventEmitter.Contract.EventEmitterTransactor.contract.Transact(opts, method, params...)
}

// Call invokes the (constant) contract method with params as input values and
// sets the output to result. The result type might be a single field for simple
// returns, a slice of interfaces for anonymous returns and a struct for named
// returns.
func (_EventEmitter *EventEmitterCallerRaw) Call(opts *bind.CallOpts, result *[]any, method string, params ...any) error {
	return _EventEmitter.Contract.contract.Call(opts, result, method, params...)
}

// Transfer initiates a plain transaction to move funds to the contract, calling
// its default method if one is available.
func (_EventEmitter *EventEmitterTransactorRaw) Transfer(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _EventEmitter.Contract.contract.Transfer(opts)
}

// Transact invokes the (paid) contract method with params as input values.
func (_EventEmitter *EventEmitterTransactorRaw) Transact(opts *bind.TransactOpts, method string, params ...any) (*types.Transaction, error) {
	return _EventEmitter.Contract.contract.Transact(opts, method, params...)
}

// Echo is a free data retrieval call binding the contract method 0x4b79d0e3.
//
// Hyperion: function echo(uint512 amount, int512 delta, bytes64 tag, address recipient, bytes payload, string note, bool enabled) pure returns(uint512, int512, bytes64, address, bytes, string, bool)
func (_EventEmitter *EventEmitterCaller) Echo(opts *bind.CallOpts, amount *big.Int, delta *big.Int, tag [64]byte, recipient common.Address, payload []byte, note string, enabled bool) (*big.Int, *big.Int, [64]byte, common.Address, []byte, string, bool, error) {
	var out []any
	err := _EventEmitter.contract.Call(opts, &out, "echo", amount, delta, tag, recipient, payload, note, enabled)

	if err != nil {
		return *new(*big.Int), *new(*big.Int), *new([64]byte), *new(common.Address), *new([]byte), *new(string), *new(bool), err
	}

	out0 := *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)
	out1 := *abi.ConvertType(out[1], new(*big.Int)).(**big.Int)
	out2 := *abi.ConvertType(out[2], new([64]byte)).(*[64]byte)
	out3 := *abi.ConvertType(out[3], new(common.Address)).(*common.Address)
	out4 := *abi.ConvertType(out[4], new([]byte)).(*[]byte)
	out5 := *abi.ConvertType(out[5], new(string)).(*string)
	out6 := *abi.ConvertType(out[6], new(bool)).(*bool)

	return out0, out1, out2, out3, out4, out5, out6, err

}

// Echo is a free data retrieval call binding the contract method 0x4b79d0e3.
//
// Hyperion: function echo(uint512 amount, int512 delta, bytes64 tag, address recipient, bytes payload, string note, bool enabled) pure returns(uint512, int512, bytes64, address, bytes, string, bool)
func (_EventEmitter *EventEmitterSession) Echo(amount *big.Int, delta *big.Int, tag [64]byte, recipient common.Address, payload []byte, note string, enabled bool) (*big.Int, *big.Int, [64]byte, common.Address, []byte, string, bool, error) {
	return _EventEmitter.Contract.Echo(&_EventEmitter.CallOpts, amount, delta, tag, recipient, payload, note, enabled)
}

// Echo is a free data retrieval call binding the contract method 0x4b79d0e3.
//
// Hyperion: function echo(uint512 amount, int512 delta, bytes64 tag, address recipient, bytes payload, string note, bool enabled) pure returns(uint512, int512, bytes64, address, bytes, string, bool)
func (_EventEmitter *EventEmitterCallerSession) Echo(amount *big.Int, delta *big.Int, tag [64]byte, recipient common.Address, payload []byte, note string, enabled bool) (*big.Int, *big.Int, [64]byte, common.Address, []byte, string, bool, error) {
	return _EventEmitter.Contract.Echo(&_EventEmitter.CallOpts, amount, delta, tag, recipient, payload, note, enabled)
}

// EchoArrays is a free data retrieval call binding the contract method 0xeae49aa9.
//
// Hyperion: function echoArrays(uint512[] values, bytes64[2] tags) pure returns(uint512[], bytes64[2])
func (_EventEmitter *EventEmitterCaller) EchoArrays(opts *bind.CallOpts, values []*big.Int, tags [2][64]byte) ([]*big.Int, [2][64]byte, error) {
	var out []any
	err := _EventEmitter.contract.Call(opts, &out, "echoArrays", values, tags)

	if err != nil {
		return *new([]*big.Int), *new([2][64]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([]*big.Int)).(*[]*big.Int)
	out1 := *abi.ConvertType(out[1], new([2][64]byte)).(*[2][64]byte)

	return out0, out1, err

}

// EchoArrays is a free data retrieval call binding the contract method 0xeae49aa9.
//
// Hyperion: function echoArrays(uint512[] values, bytes64[2] tags) pure returns(uint512[], bytes64[2])
func (_EventEmitter *EventEmitterSession) EchoArrays(values []*big.Int, tags [2][64]byte) ([]*big.Int, [2][64]byte, error) {
	return _EventEmitter.Contract.EchoArrays(&_EventEmitter.CallOpts, values, tags)
}

// EchoArrays is a free data retrieval call binding the contract method 0xeae49aa9.
//
// Hyperion: function echoArrays(uint512[] values, bytes64[2] tags) pure returns(uint512[], bytes64[2])
func (_EventEmitter *EventEmitterCallerSession) EchoArrays(values []*big.Int, tags [2][64]byte) ([]*big.Int, [2][64]byte, error) {
	return _EventEmitter.Contract.EchoArrays(&_EventEmitter.CallOpts, values, tags)
}

// EchoFixed is a free data retrieval call binding the contract method 0x563d38a1.
//
// Hyperion: function echoFixed(bytes1 value1, bytes32 value32, bytes33 value33, bytes64 value64) pure returns(bytes1, bytes32, bytes33, bytes64)
func (_EventEmitter *EventEmitterCaller) EchoFixed(opts *bind.CallOpts, value1 [1]byte, value32 [32]byte, value33 [33]byte, value64 [64]byte) ([1]byte, [32]byte, [33]byte, [64]byte, error) {
	var out []any
	err := _EventEmitter.contract.Call(opts, &out, "echoFixed", value1, value32, value33, value64)

	if err != nil {
		return *new([1]byte), *new([32]byte), *new([33]byte), *new([64]byte), err
	}

	out0 := *abi.ConvertType(out[0], new([1]byte)).(*[1]byte)
	out1 := *abi.ConvertType(out[1], new([32]byte)).(*[32]byte)
	out2 := *abi.ConvertType(out[2], new([33]byte)).(*[33]byte)
	out3 := *abi.ConvertType(out[3], new([64]byte)).(*[64]byte)

	return out0, out1, out2, out3, err

}

// EchoFixed is a free data retrieval call binding the contract method 0x563d38a1.
//
// Hyperion: function echoFixed(bytes1 value1, bytes32 value32, bytes33 value33, bytes64 value64) pure returns(bytes1, bytes32, bytes33, bytes64)
func (_EventEmitter *EventEmitterSession) EchoFixed(value1 [1]byte, value32 [32]byte, value33 [33]byte, value64 [64]byte) ([1]byte, [32]byte, [33]byte, [64]byte, error) {
	return _EventEmitter.Contract.EchoFixed(&_EventEmitter.CallOpts, value1, value32, value33, value64)
}

// EchoFixed is a free data retrieval call binding the contract method 0x563d38a1.
//
// Hyperion: function echoFixed(bytes1 value1, bytes32 value32, bytes33 value33, bytes64 value64) pure returns(bytes1, bytes32, bytes33, bytes64)
func (_EventEmitter *EventEmitterCallerSession) EchoFixed(value1 [1]byte, value32 [32]byte, value33 [33]byte, value64 [64]byte) ([1]byte, [32]byte, [33]byte, [64]byte, error) {
	return _EventEmitter.Contract.EchoFixed(&_EventEmitter.CallOpts, value1, value32, value33, value64)
}

// EchoRecord is a free data retrieval call binding the contract method 0x64f3072f.
//
// Hyperion: function echoRecord((uint512,address,bytes64) record) pure returns((uint512,address,bytes64))
func (_EventEmitter *EventEmitterCaller) EchoRecord(opts *bind.CallOpts, record EventEmitterRecord) (EventEmitterRecord, error) {
	var out []any
	err := _EventEmitter.contract.Call(opts, &out, "echoRecord", record)

	if err != nil {
		return *new(EventEmitterRecord), err
	}

	out0 := *abi.ConvertType(out[0], new(EventEmitterRecord)).(*EventEmitterRecord)

	return out0, err

}

// EchoRecord is a free data retrieval call binding the contract method 0x64f3072f.
//
// Hyperion: function echoRecord((uint512,address,bytes64) record) pure returns((uint512,address,bytes64))
func (_EventEmitter *EventEmitterSession) EchoRecord(record EventEmitterRecord) (EventEmitterRecord, error) {
	return _EventEmitter.Contract.EchoRecord(&_EventEmitter.CallOpts, record)
}

// EchoRecord is a free data retrieval call binding the contract method 0x64f3072f.
//
// Hyperion: function echoRecord((uint512,address,bytes64) record) pure returns((uint512,address,bytes64))
func (_EventEmitter *EventEmitterCallerSession) EchoRecord(record EventEmitterRecord) (EventEmitterRecord, error) {
	return _EventEmitter.Contract.EchoRecord(&_EventEmitter.CallOpts, record)
}

// Read is a free data retrieval call binding the contract method 0x57de26a4.
//
// Hyperion: function read() view returns(uint512 amount, int512 delta, bytes64 tag, address recipient, bytes payload, string note, bool enabled)
func (_EventEmitter *EventEmitterCaller) Read(opts *bind.CallOpts) (struct {
	Amount    *big.Int
	Delta     *big.Int
	Tag       [64]byte
	Recipient common.Address
	Payload   []byte
	Note      string
	Enabled   bool
}, error) {
	var out []any
	err := _EventEmitter.contract.Call(opts, &out, "read")

	outstruct := new(struct {
		Amount    *big.Int
		Delta     *big.Int
		Tag       [64]byte
		Recipient common.Address
		Payload   []byte
		Note      string
		Enabled   bool
	})
	if err != nil {
		return *outstruct, err
	}

	outstruct.Amount = *abi.ConvertType(out[0], new(*big.Int)).(**big.Int)
	outstruct.Delta = *abi.ConvertType(out[1], new(*big.Int)).(**big.Int)
	outstruct.Tag = *abi.ConvertType(out[2], new([64]byte)).(*[64]byte)
	outstruct.Recipient = *abi.ConvertType(out[3], new(common.Address)).(*common.Address)
	outstruct.Payload = *abi.ConvertType(out[4], new([]byte)).(*[]byte)
	outstruct.Note = *abi.ConvertType(out[5], new(string)).(*string)
	outstruct.Enabled = *abi.ConvertType(out[6], new(bool)).(*bool)

	return *outstruct, err

}

// Read is a free data retrieval call binding the contract method 0x57de26a4.
//
// Hyperion: function read() view returns(uint512 amount, int512 delta, bytes64 tag, address recipient, bytes payload, string note, bool enabled)
func (_EventEmitter *EventEmitterSession) Read() (struct {
	Amount    *big.Int
	Delta     *big.Int
	Tag       [64]byte
	Recipient common.Address
	Payload   []byte
	Note      string
	Enabled   bool
}, error) {
	return _EventEmitter.Contract.Read(&_EventEmitter.CallOpts)
}

// Read is a free data retrieval call binding the contract method 0x57de26a4.
//
// Hyperion: function read() view returns(uint512 amount, int512 delta, bytes64 tag, address recipient, bytes payload, string note, bool enabled)
func (_EventEmitter *EventEmitterCallerSession) Read() (struct {
	Amount    *big.Int
	Delta     *big.Int
	Tag       [64]byte
	Recipient common.Address
	Payload   []byte
	Note      string
	Enabled   bool
}, error) {
	return _EventEmitter.Contract.Read(&_EventEmitter.CallOpts)
}

// Clear is a paid mutator transaction binding the contract method 0x52efea6e.
//
// Hyperion: function clear() returns()
func (_EventEmitter *EventEmitterTransactor) Clear(opts *bind.TransactOpts) (*types.Transaction, error) {
	return _EventEmitter.contract.Transact(opts, "clear")
}

// Clear is a paid mutator transaction binding the contract method 0x52efea6e.
//
// Hyperion: function clear() returns()
func (_EventEmitter *EventEmitterSession) Clear() (*types.Transaction, error) {
	return _EventEmitter.Contract.Clear(&_EventEmitter.TransactOpts)
}

// Clear is a paid mutator transaction binding the contract method 0x52efea6e.
//
// Hyperion: function clear() returns()
func (_EventEmitter *EventEmitterTransactorSession) Clear() (*types.Transaction, error) {
	return _EventEmitter.Contract.Clear(&_EventEmitter.TransactOpts)
}

// Store is a paid mutator transaction binding the contract method 0x3d0e1089.
//
// Hyperion: function store(uint512 amount, int512 delta, bytes64 tag, address recipient, bytes payload, string note, bool enabled) returns()
func (_EventEmitter *EventEmitterTransactor) Store(opts *bind.TransactOpts, amount *big.Int, delta *big.Int, tag [64]byte, recipient common.Address, payload []byte, note string, enabled bool) (*types.Transaction, error) {
	return _EventEmitter.contract.Transact(opts, "store", amount, delta, tag, recipient, payload, note, enabled)
}

// Store is a paid mutator transaction binding the contract method 0x3d0e1089.
//
// Hyperion: function store(uint512 amount, int512 delta, bytes64 tag, address recipient, bytes payload, string note, bool enabled) returns()
func (_EventEmitter *EventEmitterSession) Store(amount *big.Int, delta *big.Int, tag [64]byte, recipient common.Address, payload []byte, note string, enabled bool) (*types.Transaction, error) {
	return _EventEmitter.Contract.Store(&_EventEmitter.TransactOpts, amount, delta, tag, recipient, payload, note, enabled)
}

// Store is a paid mutator transaction binding the contract method 0x3d0e1089.
//
// Hyperion: function store(uint512 amount, int512 delta, bytes64 tag, address recipient, bytes payload, string note, bool enabled) returns()
func (_EventEmitter *EventEmitterTransactorSession) Store(amount *big.Int, delta *big.Int, tag [64]byte, recipient common.Address, payload []byte, note string, enabled bool) (*types.Transaction, error) {
	return _EventEmitter.Contract.Store(&_EventEmitter.TransactOpts, amount, delta, tag, recipient, payload, note, enabled)
}

// EventEmitterDeployedIterator is returned from FilterDeployed and is used to iterate over the raw logs and unpacked data for Deployed events raised by the EventEmitter contract.
type EventEmitterDeployedIterator struct {
	Event *EventEmitterDeployed // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log   // Log channel receiving the found contract events
	sub  qrl.Subscription // Subscription for errors, completion and termination
	done bool             // Whether the subscription completed delivering logs
	fail error            // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *EventEmitterDeployedIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(EventEmitterDeployed)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(EventEmitterDeployed)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *EventEmitterDeployedIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *EventEmitterDeployedIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// EventEmitterDeployed represents a Deployed event raised by the EventEmitter contract.
type EventEmitterDeployed struct {
	Value *big.Int
	Raw   types.Log // Blockchain specific contextual infos
}

// FilterDeployed is a free log retrieval operation binding the contract event 0xb94ae47ec9f4248692e2ecf9740b67ab493f3dcc8452bedc7d9cd911c28d1ca5.
//
// Hyperion: event Deployed(uint256 value)
func (_EventEmitter *EventEmitterFilterer) FilterDeployed(opts *bind.FilterOpts) (*EventEmitterDeployedIterator, error) {

	logs, sub, err := _EventEmitter.contract.FilterLogs(opts, "Deployed")
	if err != nil {
		return nil, err
	}
	return &EventEmitterDeployedIterator{contract: _EventEmitter.contract, event: "Deployed", logs: logs, sub: sub}, nil
}

// WatchDeployed is a free log subscription operation binding the contract event 0xb94ae47ec9f4248692e2ecf9740b67ab493f3dcc8452bedc7d9cd911c28d1ca5.
//
// Hyperion: event Deployed(uint256 value)
func (_EventEmitter *EventEmitterFilterer) WatchDeployed(opts *bind.WatchOpts, sink chan<- *EventEmitterDeployed) (event.Subscription, error) {

	logs, sub, err := _EventEmitter.contract.WatchLogs(opts, "Deployed")
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(EventEmitterDeployed)
				if err := _EventEmitter.contract.UnpackLog(event, "Deployed", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseDeployed is a log parse operation binding the contract event 0xb94ae47ec9f4248692e2ecf9740b67ab493f3dcc8452bedc7d9cd911c28d1ca5.
//
// Hyperion: event Deployed(uint256 value)
func (_EventEmitter *EventEmitterFilterer) ParseDeployed(log types.Log) (*EventEmitterDeployed, error) {
	event := new(EventEmitterDeployed)
	if err := _EventEmitter.contract.UnpackLog(event, "Deployed", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// EventEmitterDynamicIterator is returned from FilterDynamic and is used to iterate over the raw logs and unpacked data for Dynamic events raised by the EventEmitter contract.
type EventEmitterDynamicIterator struct {
	Event *EventEmitterDynamic // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log   // Log channel receiving the found contract events
	sub  qrl.Subscription // Subscription for errors, completion and termination
	done bool             // Whether the subscription completed delivering logs
	fail error            // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *EventEmitterDynamicIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(EventEmitterDynamic)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(EventEmitterDynamic)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *EventEmitterDynamicIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *EventEmitterDynamicIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// EventEmitterDynamic represents a Dynamic event raised by the EventEmitter contract.
type EventEmitterDynamic struct {
	Payload common.Hash
	Note    common.Hash
	Amount  *big.Int
	Raw     types.Log // Blockchain specific contextual infos
}

// FilterDynamic is a free log retrieval operation binding the contract event 0x4ef7447df163d4aaeab9c66fa93651de5eebb002dcf9b60da1ebaa28ae95e825.
//
// Hyperion: event Dynamic(bytes indexed payload, string indexed note, uint512 amount)
func (_EventEmitter *EventEmitterFilterer) FilterDynamic(opts *bind.FilterOpts, payload [][]byte, note []string) (*EventEmitterDynamicIterator, error) {

	var payloadRule []any
	for _, payloadItem := range payload {
		payloadRule = append(payloadRule, payloadItem)
	}
	var noteRule []any
	for _, noteItem := range note {
		noteRule = append(noteRule, noteItem)
	}

	logs, sub, err := _EventEmitter.contract.FilterLogs(opts, "Dynamic", payloadRule, noteRule)
	if err != nil {
		return nil, err
	}
	return &EventEmitterDynamicIterator{contract: _EventEmitter.contract, event: "Dynamic", logs: logs, sub: sub}, nil
}

// WatchDynamic is a free log subscription operation binding the contract event 0x4ef7447df163d4aaeab9c66fa93651de5eebb002dcf9b60da1ebaa28ae95e825.
//
// Hyperion: event Dynamic(bytes indexed payload, string indexed note, uint512 amount)
func (_EventEmitter *EventEmitterFilterer) WatchDynamic(opts *bind.WatchOpts, sink chan<- *EventEmitterDynamic, payload [][]byte, note []string) (event.Subscription, error) {

	var payloadRule []any
	for _, payloadItem := range payload {
		payloadRule = append(payloadRule, payloadItem)
	}
	var noteRule []any
	for _, noteItem := range note {
		noteRule = append(noteRule, noteItem)
	}

	logs, sub, err := _EventEmitter.contract.WatchLogs(opts, "Dynamic", payloadRule, noteRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(EventEmitterDynamic)
				if err := _EventEmitter.contract.UnpackLog(event, "Dynamic", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseDynamic is a log parse operation binding the contract event 0x4ef7447df163d4aaeab9c66fa93651de5eebb002dcf9b60da1ebaa28ae95e825.
//
// Hyperion: event Dynamic(bytes indexed payload, string indexed note, uint512 amount)
func (_EventEmitter *EventEmitterFilterer) ParseDynamic(log types.Log) (*EventEmitterDynamic, error) {
	event := new(EventEmitterDynamic)
	if err := _EventEmitter.contract.UnpackLog(event, "Dynamic", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}

// EventEmitterStoredIterator is returned from FilterStored and is used to iterate over the raw logs and unpacked data for Stored events raised by the EventEmitter contract.
type EventEmitterStoredIterator struct {
	Event *EventEmitterStored // Event containing the contract specifics and raw log

	contract *bind.BoundContract // Generic contract to use for unpacking event data
	event    string              // Event name to use for unpacking event data

	logs chan types.Log   // Log channel receiving the found contract events
	sub  qrl.Subscription // Subscription for errors, completion and termination
	done bool             // Whether the subscription completed delivering logs
	fail error            // Occurred error to stop iteration
}

// Next advances the iterator to the subsequent event, returning whether there
// are any more events found. In case of a retrieval or parsing error, false is
// returned and Error() can be queried for the exact failure.
func (it *EventEmitterStoredIterator) Next() bool {
	// If the iterator failed, stop iterating
	if it.fail != nil {
		return false
	}
	// If the iterator completed, deliver directly whatever's available
	if it.done {
		select {
		case log := <-it.logs:
			it.Event = new(EventEmitterStored)
			if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
				it.fail = err
				return false
			}
			it.Event.Raw = log
			return true

		default:
			return false
		}
	}
	// Iterator still in progress, wait for either a data or an error event
	select {
	case log := <-it.logs:
		it.Event = new(EventEmitterStored)
		if err := it.contract.UnpackLog(it.Event, it.event, log); err != nil {
			it.fail = err
			return false
		}
		it.Event.Raw = log
		return true

	case err := <-it.sub.Err():
		it.done = true
		it.fail = err
		return it.Next()
	}
}

// Error returns any retrieval or parsing error occurred during filtering.
func (it *EventEmitterStoredIterator) Error() error {
	return it.fail
}

// Close terminates the iteration process, releasing any pending underlying
// resources.
func (it *EventEmitterStoredIterator) Close() error {
	it.sub.Unsubscribe()
	return nil
}

// EventEmitterStored represents a Stored event raised by the EventEmitter contract.
type EventEmitterStored struct {
	Recipient common.Address
	Amount    *big.Int
	Delta     *big.Int
	Tag       [64]byte
	Payload   []byte
	Note      string
	Enabled   bool
	Raw       types.Log // Blockchain specific contextual infos
}

// FilterStored is a free log retrieval operation binding the contract event 0x0971a927eb69632cd5aced366c9dd3ee5626b6c0a27cb781139eeffab9e5372f.
//
// Hyperion: event Stored(address indexed recipient, uint512 indexed amount, int512 indexed delta, bytes64 tag, bytes payload, string note, bool enabled)
func (_EventEmitter *EventEmitterFilterer) FilterStored(opts *bind.FilterOpts, recipient []common.Address, amount []*big.Int, delta []*big.Int) (*EventEmitterStoredIterator, error) {

	var recipientRule []any
	for _, recipientItem := range recipient {
		recipientRule = append(recipientRule, recipientItem)
	}
	var amountRule []any
	for _, amountItem := range amount {
		amountRule = append(amountRule, amountItem)
	}
	var deltaRule []any
	for _, deltaItem := range delta {
		deltaRule = append(deltaRule, deltaItem)
	}

	logs, sub, err := _EventEmitter.contract.FilterLogs(opts, "Stored", recipientRule, amountRule, deltaRule)
	if err != nil {
		return nil, err
	}
	return &EventEmitterStoredIterator{contract: _EventEmitter.contract, event: "Stored", logs: logs, sub: sub}, nil
}

// WatchStored is a free log subscription operation binding the contract event 0x0971a927eb69632cd5aced366c9dd3ee5626b6c0a27cb781139eeffab9e5372f.
//
// Hyperion: event Stored(address indexed recipient, uint512 indexed amount, int512 indexed delta, bytes64 tag, bytes payload, string note, bool enabled)
func (_EventEmitter *EventEmitterFilterer) WatchStored(opts *bind.WatchOpts, sink chan<- *EventEmitterStored, recipient []common.Address, amount []*big.Int, delta []*big.Int) (event.Subscription, error) {

	var recipientRule []any
	for _, recipientItem := range recipient {
		recipientRule = append(recipientRule, recipientItem)
	}
	var amountRule []any
	for _, amountItem := range amount {
		amountRule = append(amountRule, amountItem)
	}
	var deltaRule []any
	for _, deltaItem := range delta {
		deltaRule = append(deltaRule, deltaItem)
	}

	logs, sub, err := _EventEmitter.contract.WatchLogs(opts, "Stored", recipientRule, amountRule, deltaRule)
	if err != nil {
		return nil, err
	}
	return event.NewSubscription(func(quit <-chan struct{}) error {
		defer sub.Unsubscribe()
		for {
			select {
			case log := <-logs:
				// New log arrived, parse the event and forward to the user
				event := new(EventEmitterStored)
				if err := _EventEmitter.contract.UnpackLog(event, "Stored", log); err != nil {
					return err
				}
				event.Raw = log

				select {
				case sink <- event:
				case err := <-sub.Err():
					return err
				case <-quit:
					return nil
				}
			case err := <-sub.Err():
				return err
			case <-quit:
				return nil
			}
		}
	}), nil
}

// ParseStored is a log parse operation binding the contract event 0x0971a927eb69632cd5aced366c9dd3ee5626b6c0a27cb781139eeffab9e5372f.
//
// Hyperion: event Stored(address indexed recipient, uint512 indexed amount, int512 indexed delta, bytes64 tag, bytes payload, string note, bool enabled)
func (_EventEmitter *EventEmitterFilterer) ParseStored(log types.Log) (*EventEmitterStored, error) {
	event := new(EventEmitterStored)
	if err := _EventEmitter.contract.UnpackLog(event, "Stored", log); err != nil {
		return nil, err
	}
	event.Raw = log
	return event, nil
}
