package conformance

// Vectors is the canonical corpus of conformance test cases. Both the Go VM
// (see conformance_test.go) and qrvmone (see qrvmone/test/conformance) must
// produce matching Result values for each entry.
//
// Convention used in this corpus:
//   - Programs end with MSTORE + RETURN, writing a 64-byte word to memory
//     offset 0 and returning those 64 bytes. The 64-byte word matches the
//     QRVM memory word width.
//   - Expected return values are left-padded with zeros to 64 bytes (128 hex).
//   - Programs that intentionally fail (out-of-gas, underflow, etc.) omit
//     MSTORE/RETURN: ExpectedReturnHex is "" and ExpectedError is the class.
//
// Opcode reference (relevant excerpt):
//   0x00 STOP
//   0x01 ADD         0x02 MUL         0x03 SUB         0x04 DIV
//   0x05 SDIV        0x06 MOD         0x07 SMOD        0x0a EXP
//   0x10 LT          0x11 GT          0x14 EQ          0x15 ISZERO
//   0x16 AND         0x17 OR          0x18 XOR         0x19 NOT
//   0x1a BYTE        0x1b SHL         0x1c SHR         0x1d SAR
//   0x1e CLZ
//   0x20 KECCAK256   0x21 KECCAK512
//   0x31 BALANCE     0x3b EXTCODESIZE
//   0x50 POP         0x51 MLOAD       0x52 MSTORE      0x53 MSTORE8
//   0x54 SLOAD       0x55 SSTORE
//   0x56 JUMP        0x57 JUMPI       0x59 MSIZE       0x5b JUMPDEST
//   0x5c TLOAD       0x5d TSTORE      0x5e MCOPY
//   0x5f PUSH0
//   0x60..0x7f PUSH1..PUSH32
//   0x80..0x9f PUSH33..PUSH64  (introduced when the VM word grew to 64 bytes)
//   0xa0..0xaf DUP1..DUP16     (shifted from 0x80)
//   0xb0..0xbf SWAP1..SWAP16   (shifted from 0x90)
//   0xf3 RETURN      0xfd REVERT      0xfe INVALID

// storeAndReturn64 is the 5-byte trailer that stores the top of the stack at
// memory[0] (MSTORE writes 64 B) and returns those 64 bytes.
//
//	PUSH1 0x00   ; memory offset
//	MSTORE       ; memory[0..64) = top of stack
//	PUSH1 0x40   ; size = 64
//	PUSH1 0x00   ; offset = 0
//	RETURN
const storeAndReturn64 = "6000" + "52" + "6040" + "6000" + "f3"

// expectedWord returns a 128-char hex string with the given tail right-
// aligned into a 64-byte word.
func expectedWord(tail string) string {
	if len(tail) > 128 {
		panic("expectedWord: value > 64 bytes")
	}
	return leftPad(tail, 128)
}

func leftPad(s string, n int) string {
	if len(s) >= n {
		return s
	}
	pad := make([]byte, n-len(s))
	for i := range pad {
		pad[i] = '0'
	}
	return string(pad) + s
}

// storageKeyLow is a storage key below 2^256.
const storageKeyLow = "05"

// pushStorageKeyHigh pushes 2^256 + 5 (PUSH33): a storage key whose low 256
// bits equal storageKeyLow and whose bit 256 is set.
const pushStorageKeyHigh = "80" + "01" + "00000000000000000000000000000000000000000000000000000000000000" + "05"

// pushStorageKeyTopBit pushes 2^511 (PUSH1 1, PUSH2 511, SHL): a storage key
// whose low 256 bits are zero.
const pushStorageKeyTopBit = "6001" + "6101ff" + "1b"

// sstoreValue42 stores 0x2a at the storage key currently on top of the stack.
// SSTORE pops the key first, so the value is pushed underneath it.
func sstoreValue42(pushKey string) string {
	return "602a" + pushKey + "55"
}

// tstoreValue42 stores 0x2a at the transient storage key currently on top of
// the stack. Like SSTORE, TSTORE pops the key first.
func tstoreValue42(pushKey string) string {
	return "602a" + pushKey + "5d"
}

// ascendingWord is the 64-byte word 0x00, 0x01, ..., 0x3f. No two of its
// bytes are equal, so a copy that lands one byte off shows in the result.
const ascendingWord = "0001020304050607" + "08090a0b0c0d0e0f" +
	"1011121314151617" + "18191a1b1c1d1e1f" +
	"2021222324252627" + "28292a2b2c2d2e2f" +
	"3031323334353637" + "38393a3b3c3d3e3f"

// mstoreAscendingWord sets memory[0..64) to ascendingWord (PUSH64, PUSH1 0,
// MSTORE), leaving memory one word long.
const mstoreAscendingWord = "9f" + ascendingWord + "6000" + "52"

// pushOffset2Pow64 pushes 2^64 (PUSH9), the first memory offset that does not
// fit in 64 bits.
const pushOffset2Pow64 = "68" + "010000000000000000"

// mcopy copies memory[src..src+length) to memory[dst..dst+length). MCOPY pops
// the destination first, then the source, then the length. Each argument is a
// complete push instruction.
func mcopy(pushDst, pushSrc, pushLength string) string {
	return pushLength + pushSrc + pushDst + "5e"
}

// Vectors is the complete corpus exposed to test runners.
var Vectors = []Vector{
	// --- arithmetic ---------------------------------------------------
	{
		Name:              "ADD 1 + 2",
		BytecodeHex:       "6001" + "6002" + "01" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("03"),
	},
	{
		Name:              "SUB 5 - 3",
		BytecodeHex:       "6003" + "6005" + "03" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("02"),
	},
	{
		Name:              "SUB 0 - 1 wraps to 2^512 - 1",
		BytecodeHex:       "6001" + "6000" + "03" + storeAndReturn64,
		ExpectedReturnHex: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	},
	{
		Name:              "MUL 7 * 8",
		BytecodeHex:       "6008" + "6007" + "02" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("38"),
	},
	{
		Name:              "DIV 100 / 7 -> 14",
		BytecodeHex:       "6007" + "6064" + "04" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("0e"),
	},
	{
		Name:              "DIV by zero -> 0",
		BytecodeHex:       "6000" + "6064" + "04" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
	},
	{
		Name:              "MOD 100 % 7 -> 2",
		BytecodeHex:       "6007" + "6064" + "06" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("02"),
	},
	{
		Name:              "EXP 2 ** 10 -> 1024",
		BytecodeHex:       "600a" + "6002" + "0a" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("0400"),
	},
	{
		Name:              "EXP 2 ** 512 -> 0 (mod 2^512)",
		BytecodeHex:       "610200" + "6002" + "0a" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
	},

	// --- comparison ---------------------------------------------------
	{
		Name:              "LT 1 < 2 -> 1",
		BytecodeHex:       "6002" + "6001" + "10" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("01"),
	},
	{
		Name:              "LT 2 < 1 -> 0",
		BytecodeHex:       "6001" + "6002" + "10" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
	},
	{
		Name:              "GT 2 > 1 -> 1",
		BytecodeHex:       "6001" + "6002" + "11" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("01"),
	},
	{
		Name:              "EQ 42 == 42 -> 1",
		BytecodeHex:       "602a" + "602a" + "14" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("01"),
	},
	{
		Name:              "ISZERO 0 -> 1",
		BytecodeHex:       "6000" + "15" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("01"),
	},
	{
		Name:              "ISZERO 1 -> 0",
		BytecodeHex:       "6001" + "15" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
	},

	// --- bitwise ------------------------------------------------------
	{
		Name:              "AND 0xff & 0x0f -> 0x0f",
		BytecodeHex:       "600f" + "60ff" + "16" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("0f"),
	},
	{
		Name:              "OR 0xf0 | 0x0f -> 0xff",
		BytecodeHex:       "600f" + "60f0" + "17" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("ff"),
	},
	{
		Name:              "XOR 0xff ^ 0x0f -> 0xf0",
		BytecodeHex:       "600f" + "60ff" + "18" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("f0"),
	},
	{
		Name:              "NOT 0 -> 2^512 - 1",
		BytecodeHex:       "6000" + "19" + storeAndReturn64,
		ExpectedReturnHex: "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff",
	},

	// --- shifts (512-bit limits) --------------------------------------
	{
		Name:              "SHL 1 by 0 -> 1",
		BytecodeHex:       "6001" + "6000" + "1b" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("01"),
	},
	{
		Name:              "SHL 1 by 256 -> 2^256",
		BytecodeHex:       "6001" + "610100" + "1b" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("010000000000000000000000000000000000000000000000000000000000000000"),
	},
	{
		// 2^511 in a 64-byte big-endian word: byte 0 = 0x80, bytes 1..63 = 0.
		Name:        "SHL 1 by 511 -> 2^511",
		BytecodeHex: "6001" + "6101ff" + "1b" + storeAndReturn64,
		ExpectedReturnHex: "80" + "000000000000000000000000000000000000000000000000000000000000" +
			"000000000000000000000000000000000000000000000000000000000000000000",
	},
	{
		Name:              "SHL 1 by 512 -> 0 (shift >= 512)",
		BytecodeHex:       "6001" + "610200" + "1b" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
	},
	{
		// (2^512 - 1) >> 511 keeps only the top bit, producing 1.
		// We can only PUSH up to 64 immediate bytes (PUSH64), so we build
		// 2^512 - 1 by NOT(0) which needs no immediate.
		Name:              "SHR (2^512 - 1) by 511 -> 1",
		BytecodeHex:       "6000" + "19" + "6101ff" + "1c" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("01"),
	},

	// --- count leading zeros (CLZ, EIP-7939) --------------------------
	//
	// CLZ counts over the 512-bit word: zero gives 512, and a value below
	// 2^256 gives 256 more than it would on a 256-bit word. CLZ costs 5 gas.
	{
		Name:              "CLZ 0 -> 512",
		BytecodeHex:       "6000" + "1e" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("0200"),
		ExpectedGasUsed:   23,
	},
	{
		Name:              "CLZ 1 -> 511",
		BytecodeHex:       "6001" + "1e" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("01ff"),
		ExpectedGasUsed:   23,
	},
	{
		// PUSH32 of 2^255: the top bit of a 256-bit word.
		Name:              "CLZ 2^255 -> 256",
		BytecodeHex:       "7f" + "8000000000000000000000000000000000000000000000000000000000000000" + "1e" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("0100"),
		ExpectedGasUsed:   23,
	},
	{
		// PUSH33 of 2^256: the first value that does not fit in 256 bits.
		Name:              "CLZ 2^256 -> 255",
		BytecodeHex:       "80" + "010000000000000000000000000000000000000000000000000000000000000000" + "1e" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("ff"),
		ExpectedGasUsed:   23,
	},
	{
		Name:              "CLZ 2^511 -> 0",
		BytecodeHex:       "6001" + "6101ff" + "1b" + "1e" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   29,
	},
	{
		Name:              "CLZ (2^512 - 1) -> 0",
		BytecodeHex:       "6000" + "19" + "1e" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   26,
	},
	{
		Name:          "CLZ with no operand",
		BytecodeHex:   "1e",
		ExpectedError: ErrStackUnderflow,
	},

	// --- PUSH0 / PUSH1 / PUSH32 / PUSH33 / PUSH64 ---------------------
	{
		Name:              "PUSH0 -> 0",
		BytecodeHex:       "5f" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
	},
	{
		Name:              "PUSH1 0xAB -> 0xAB",
		BytecodeHex:       "60ab" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("ab"),
	},
	{
		Name:              "PUSH32 ascending bytes",
		BytecodeHex:       "7f" + "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"),
	},
	{
		// PUSH33: opcode 0x80, 33 immediate bytes.
		Name:              "PUSH33 spans one extra byte past PUSH32",
		BytecodeHex:       "80" + "ff" + "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("ff000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"),
	},
	{
		// PUSH64: opcode 0x9F, 64 immediate bytes — full word.
		Name: "PUSH64 full-word value",
		BytecodeHex: "9f" +
			"0001020304050607" + "08090a0b0c0d0e0f" +
			"1011121314151617" + "18191a1b1c1d1e1f" +
			"2021222324252627" + "28292a2b2c2d2e2f" +
			"3031323334353637" + "38393a3b3c3d3e3f" + storeAndReturn64,
		ExpectedReturnHex: "0001020304050607" + "08090a0b0c0d0e0f" +
			"1011121314151617" + "18191a1b1c1d1e1f" +
			"2021222324252627" + "28292a2b2c2d2e2f" +
			"3031323334353637" + "38393a3b3c3d3e3f",
	},

	// --- DUP / SWAP at new offsets ------------------------------------
	{
		// DUP1 is now 0xA0 (was 0x80). Push 42, DUP1, store top, return.
		Name:              "DUP1 at 0xA0",
		BytecodeHex:       "602a" + "a0" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("2a"),
	},
	{
		// SWAP1 is now 0xB0 (was 0x90). Push 1, push 2, swap, store top.
		// After SWAP1 top is 1.
		Name:              "SWAP1 at 0xB0",
		BytecodeHex:       "6001" + "6002" + "b0" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("01"),
	},

	// --- BYTE (64-byte word) ------------------------------------------
	{
		// BYTE operates on 64-byte word, index 0 is MSB.
		// Push constant 0xAA into top byte via PUSH64 with 0xAA at position 0.
		Name: "BYTE index 0 of full-word value",
		BytecodeHex: "9f" + "aa" + "0001020304050607" + "08090a0b0c0d0e0f" +
			"1011121314151617" + "18191a1b1c1d1e1f" +
			"2021222324252627" + "28292a2b2c2d2e2f" +
			"3031323334353637" + "38393a3b3c3d3e" + // 63 immediate bytes after the aa
			"6000" + "1a" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("aa"),
	},
	{
		// BYTE index 63 is the LSB.
		Name:              "BYTE index 63 of 0xFF -> 0xFF",
		BytecodeHex:       "60ff" + "603f" + "1a" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("ff"),
	},
	{
		// BYTE index 64 -> 0.
		Name:              "BYTE index 64 -> 0 (out of range)",
		BytecodeHex:       "60ff" + "6040" + "1a" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
	},

	// --- memory / MLOAD (64-byte word) --------------------------------
	{
		// MSTORE writes 64 B at offset 0, MLOAD at offset 0 reads back
		// the same 64 B (which we then MSTORE + RETURN again).
		Name:              "MSTORE + MLOAD round-trip",
		BytecodeHex:       "607b" + "6000" + "52" + "6000" + "51" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("7b"),
	},

	{
		Name:              "KECCAK512 empty input",
		BytecodeHex:       "6000600021" + storeAndReturn64,
		ExpectedReturnHex: "0eab42de4c3ceb9235fc91acffe746b29c29a8c366b7c60e4e67c466f36a4304c00fa9caf9d87976ba469bcbe06713b435f091ef2769fb160cdab33d3670680e",
		ExpectedGasUsed:   81,
	},
	{
		Name:              "KECCAK512 abc",
		BytecodeHex:       "626162636000526003603d21" + storeAndReturn64,
		ExpectedReturnHex: "18587dc2ea106b9a1563e32b3312421ca164c7f1f07bc922a9c83d77cea3a1e5d0c69910739025372dc14ac9642629379540c17e2a65b19d77aa511a9d00bb96",
		ExpectedGasUsed:   102,
	},
	{
		Name:              "KECCAK512 crosses a VM word boundary",
		BytecodeHex:       "6041600021" + storeAndReturn64,
		ExpectedReturnHex: "a9cdba36670d15adea023e47166cfe67fdc1f94e96f99d5ddf35abeb29987ac77c70808f411a4c03f634f088d5eb858a9aae18af407007de69c3d6735e433902",
		ExpectedGasUsed:   108,
	},
	// --- storage keys (SSTORE / SLOAD) ---------------------------------
	//
	// Storage keys and values are full 64-byte words. Keys differing in
	// their high 256 bits address distinct slots, each with its own warmth.
	{
		Name:              "SSTORE + SLOAD round-trip, key below 2^256",
		BytecodeHex:       sstoreValue42("60"+storageKeyLow) + "60" + storageKeyLow + "54" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("2a"),
		ExpectedGasUsed:   22224,
	},
	{
		Name:              "SSTORE + SLOAD round-trip, key above 2^256",
		BytecodeHex:       sstoreValue42(pushStorageKeyHigh) + pushStorageKeyHigh + "54" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("2a"),
		ExpectedGasUsed:   22224,
	},
	{
		Name:              "SSTORE key 2^256+5, SLOAD key 5 -> distinct slots",
		BytecodeHex:       sstoreValue42(pushStorageKeyHigh) + "60" + storageKeyLow + "54" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   24224,
	},
	{
		Name:              "SSTORE key 5, SLOAD key 2^256+5 -> distinct slots",
		BytecodeHex:       sstoreValue42("60"+storageKeyLow) + pushStorageKeyHigh + "54" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   24224,
	},
	{
		Name:              "SSTORE key 2^511, SLOAD key 0 -> distinct slots",
		BytecodeHex:       sstoreValue42(pushStorageKeyTopBit) + "6000" + "54" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   24230,
	},
	{
		// The value side keeps the full 64-byte word.
		Name: "SSTORE + SLOAD keeps a full 64-byte value",
		BytecodeHex: "9f" +
			"0001020304050607" + "08090a0b0c0d0e0f" +
			"1011121314151617" + "18191a1b1c1d1e1f" +
			"2021222324252627" + "28292a2b2c2d2e2f" +
			"3031323334353637" + "38393a3b3c3d3e3f" +
			"60" + storageKeyLow + "55" + "60" + storageKeyLow + "54" + storeAndReturn64,
		ExpectedReturnHex: "0001020304050607" + "08090a0b0c0d0e0f" +
			"1011121314151617" + "18191a1b1c1d1e1f" +
			"2021222324252627" + "28292a2b2c2d2e2f" +
			"3031323334353637" + "38393a3b3c3d3e3f",
	},

	// --- transient storage (TSTORE / TLOAD, EIP-1153) ------------------
	//
	// Transient storage mirrors persistent storage: 64-byte values under
	// 64-byte keys taken from the full stack operand. Both
	// opcodes cost a flat 100 gas, and the two stores never overlap.
	{
		Name:              "TSTORE + TLOAD round-trip",
		BytecodeHex:       tstoreValue42("60"+storageKeyLow) + "60" + storageKeyLow + "5c" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("2a"),
		ExpectedGasUsed:   224,
	},
	{
		Name:              "TLOAD of an unset key -> 0",
		BytecodeHex:       "60" + storageKeyLow + "5c" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   118,
	},
	{
		Name:              "TSTORE key 2^256+5, TLOAD key 5 -> distinct slots",
		BytecodeHex:       tstoreValue42(pushStorageKeyHigh) + "60" + storageKeyLow + "5c" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   224,
	},
	{
		Name:              "TSTORE, then SLOAD -> persistent storage untouched",
		BytecodeHex:       tstoreValue42("60"+storageKeyLow) + "60" + storageKeyLow + "54" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   2224,
	},
	{
		Name:              "SSTORE, then TLOAD -> transient storage untouched",
		BytecodeHex:       sstoreValue42("60"+storageKeyLow) + "60" + storageKeyLow + "5c" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   22224,
	},
	{
		Name: "TSTORE + TLOAD keeps a full 64-byte value",
		BytecodeHex: "9f" +
			"0001020304050607" + "08090a0b0c0d0e0f" +
			"1011121314151617" + "18191a1b1c1d1e1f" +
			"2021222324252627" + "28292a2b2c2d2e2f" +
			"3031323334353637" + "38393a3b3c3d3e3f" +
			"60" + storageKeyLow + "5d" + "60" + storageKeyLow + "5c" + storeAndReturn64,
		ExpectedReturnHex: "0001020304050607" + "08090a0b0c0d0e0f" +
			"1011121314151617" + "18191a1b1c1d1e1f" +
			"2021222324252627" + "28292a2b2c2d2e2f" +
			"3031323334353637" + "38393a3b3c3d3e3f",
	},

	// --- memory copy (MCOPY, EIP-5656) ---------------------------------
	//
	// MCOPY costs 3 gas, plus 3 gas for each 64-byte word copied, plus the
	// expansion of memory to cover both the source and the destination range.
	// Overlapping ranges are copied as if through an intermediate buffer.
	{
		Name:              "MCOPY a word to fresh memory",
		BytecodeHex:       mstoreAscendingWord + mcopy("6040", "6000", "6040") + "6040" + "6040" + "f3",
		ExpectedReturnHex: ascendingWord,
		ExpectedGasUsed:   36,
	},
	{
		Name:              "MCOPY overlapping, destination one byte after source",
		BytecodeHex:       mstoreAscendingWord + mcopy("6001", "6000", "603f") + "6040" + "6000" + "f3",
		ExpectedReturnHex: "00" + ascendingWord[:126],
		ExpectedGasUsed:   33,
	},
	{
		Name:              "MCOPY overlapping, destination one byte before source",
		BytecodeHex:       mstoreAscendingWord + mcopy("6000", "6001", "603f") + "6040" + "6000" + "f3",
		ExpectedReturnHex: ascendingWord[2:] + "3f",
		ExpectedGasUsed:   33,
	},
	{
		// The source range lies past the end of memory: memory grows to cover
		// it and the destination receives zeros.
		Name:              "MCOPY from past the end of memory -> zeros",
		BytecodeHex:       mstoreAscendingWord + mcopy("6000", "6040", "6040") + "6040" + "6000" + "f3",
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   36,
	},
	{
		// 65 bytes round up to two words, for copy gas and for MSIZE alike.
		Name:              "MCOPY of 65 bytes -> two words",
		BytecodeHex:       mcopy("6000", "6000", "6041") + "59" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("80"),
		ExpectedGasUsed:   38,
	},
	{
		// A zero length never touches memory, whatever the offsets: MSIZE is
		// still 0 afterwards.
		Name:              "MCOPY of zero bytes at offset 2^64 -> no expansion",
		BytecodeHex:       mcopy(pushOffset2Pow64, pushOffset2Pow64, "6000") + "59" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   29,
	},
	{
		Name:          "MCOPY to offset 2^64 -> out of gas",
		BytecodeHex:   mcopy(pushOffset2Pow64, "6000", "6001"),
		ExpectedError: ErrOutOfGas,
	},
	{
		// The source is the larger offset here; it must bound memory too.
		Name:          "MCOPY from offset 2^64 -> out of gas",
		BytecodeHex:   mcopy("6000", pushOffset2Pow64, "6001"),
		ExpectedError: ErrOutOfGas,
	},
	{
		Name:          "MCOPY of 2^64 bytes -> out of gas",
		BytecodeHex:   mcopy("6000", "6000", pushOffset2Pow64),
		ExpectedError: ErrOutOfGas,
	},
	{
		Name:          "MCOPY with two operands",
		BytecodeHex:   "6000" + "6000" + "5e",
		ExpectedError: ErrStackUnderflow,
	},

	// --- account access (EIP-2929 warm / cold) -------------------------
	//
	// The first access to an account costs 2600 gas and every later one 100.
	// The accounts in the access list when the code starts are warm from the
	// start. Other than the origin and the contract, these are the
	// precompiles (vm.ActivePrecompiles: 0x01 to 0x07) and the coinbase, which
	// the runner leaves as the zero address. The addresses after the last
	// precompile are as cold as any other: a runtime or a test host that
	// treats a different range as precompiles is off by 2500 gas here.
	{
		Name:              "BALANCE of the first precompile (0x01) -> warm",
		BytecodeHex:       "6001" + "31" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   118,
	},
	{
		Name:              "BALANCE of the last precompile (0x07) -> warm",
		BytecodeHex:       "6007" + "31" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   118,
	},
	{
		Name:              "BALANCE of 0x08, after the last precompile -> cold",
		BytecodeHex:       "6008" + "31" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   2618,
	},
	{
		Name:              "BALANCE of 0x09 -> cold",
		BytecodeHex:       "6009" + "31" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   2618,
	},
	{
		Name:              "BALANCE of 0x0a -> cold",
		BytecodeHex:       "600a" + "31" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   2618,
	},
	{
		// POP drops the first balance; the second access is warm.
		Name:              "BALANCE of 0x08 twice -> cold, then warm",
		BytecodeHex:       "6008" + "31" + "50" + "6008" + "31" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   2723,
	},
	{
		Name:              "BALANCE of the coinbase (zero address) -> warm",
		BytecodeHex:       "6000" + "31" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   118,
	},
	{
		Name:              "EXTCODESIZE of the last precompile (0x07) -> warm",
		BytecodeHex:       "6007" + "3b" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   118,
	},
	{
		Name:              "EXTCODESIZE of 0x08, after the last precompile -> cold",
		BytecodeHex:       "6008" + "3b" + storeAndReturn64,
		ExpectedReturnHex: expectedWord("00"),
		ExpectedGasUsed:   2618,
	},

	// --- error paths --------------------------------------------------
	{
		// POP on empty stack: stack underflow.
		Name:          "POP on empty stack",
		BytecodeHex:   "50",
		ExpectedError: ErrStackUnderflow,
	},
	{
		// 0xFE is the designated INVALID opcode.
		Name:          "INVALID (0xFE)",
		BytecodeHex:   "fe",
		ExpectedError: ErrInvalidOpcode,
	},
	{
		// REVERT returns an error class but can still leave returndata.
		// We don't set ExpectedReturnHex because REVERT data depends on
		// the host wiring; here we only assert the error class.
		Name:          "REVERT",
		BytecodeHex:   "6000" + "6000" + "fd",
		ExpectedError: ErrExecutionReverted,
	},
	{
		// JUMP to a non-JUMPDEST byte fails.
		Name:          "JUMP to invalid destination",
		BytecodeHex:   "6001" + "56",
		ExpectedError: ErrInvalidJump,
	},
}
