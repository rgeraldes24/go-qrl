// Copyright 2017 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

// qrvmdisTracer returns sufficient information from a trace to perform qrvmdis-style
// disassembly.
{
	stack: [{ops: []}],

	npushes: {1: 1, 2: 1, 3: 1, 4: 1, 5: 1, 6: 1, 7: 1, 8: 1, 9: 1, 10: 1, 11: 1, 16: 1, 17: 1, 18: 1, 19: 1, 20: 1, 21: 1, 22: 1, 23: 1, 24: 1, 25: 1, 26: 1, 32: 1, 48: 1, 49: 1, 50: 1, 51: 1, 52: 1, 53: 1, 54: 1, 56: 1, 58: 1, 59: 1, 64: 1, 65: 1, 66: 1, 67: 1, 68: 1, 69: 1, 81: 1, 84: 1, 88: 1, 89: 1, 90: 1, 240: 1, 241: 1, 242: 1},

	resultCount: function(op) {
		// QRVM extends PUSH through 0x9f and shifts DUP/SWAP to 0xa0/0xb0.
		if (op >= 0x60 && op <= 0x9f) {
			return 1;
		}
		if (op >= 0xa0 && op <= 0xaf) {
			return op - 0xa0 + 2;
		}
		if (op >= 0xb0 && op <= 0xbf) {
			return op - 0xb0 + 2;
		}
		return this.npushes[op] || 0;
	},

	// result is invoked when all the opcodes have been iterated over and returns
	// the final result of the tracing.
	result: function() { return this.stack[0].ops; },

	// fault is invoked when the actual execution of an opcode fails.
	fault: function(log, db) { },

	// step is invoked for every opcode that the VM executes.
	step: function(log, db) {
		var frame = this.stack[this.stack.length - 1];

		var error = log.getError();
		if (error) {
			frame["error"] = error;
		} else if (log.getDepth() == this.stack.length) {
			opinfo = {
				op:     log.op.toNumber(),
				depth : log.getDepth(),
				result: [],
			};
			if (frame.ops.length > 0) {
				var prevop = frame.ops[frame.ops.length - 1];
				for(var i = 0; i < this.resultCount(prevop.op); i++)
					prevop.result.push(log.stack.peek(i).toString(16));
			}
			switch(log.op.toString()) {
			case "CALL":
				var instart = log.stack.peek(3).valueOf();
				var insize = log.stack.peek(4).valueOf();
				opinfo["gas"] = log.stack.peek(0).valueOf();
				opinfo["to"] = log.stack.peek(1).toString(16);
				opinfo["value"] = log.stack.peek(2).toString();
				opinfo["input"] = log.memory.slice(instart, instart + insize);
				opinfo["error"] = null;
				opinfo["return"] = null;
				opinfo["ops"] = [];
				this.stack.push(opinfo);
				break;
			case "DELEGATECALL": case "STATICCALL":
				var instart = log.stack.peek(2).valueOf();
				var insize = log.stack.peek(3).valueOf();
				opinfo["op"] =  log.op.toString();
				opinfo["gas"] =  log.stack.peek(0).valueOf();
				opinfo["to"] =  log.stack.peek(1).toString(16);
				opinfo["input"] =  log.memory.slice(instart, instart + insize);
				opinfo["error"] =  null;
				opinfo["return"] =  null;
				opinfo["ops"] = [];
				this.stack.push(opinfo);
				break;
			case "RETURN": case "REVERT":
				var out = log.stack.peek(0).valueOf();
				var outsize = log.stack.peek(1).valueOf();
				frame.return = log.memory.slice(out, out + outsize);
				break;
			case "STOP":
				frame.return = log.memory.slice(0, 0);
				break;
			case "JUMPDEST":
				opinfo["pc"] = log.getPC();
			}
			if(log.op.isPush()) {
				opinfo["len"] = log.op.toNumber() - 0x5e;
			}
			frame.ops.push(opinfo);
		} else {
			this.stack = this.stack.slice(0, log.getDepth());
		}
	}
}
