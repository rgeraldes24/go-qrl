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

	npushes: {1: 1, 2: 1, 3: 1, 4: 1, 5: 1, 6: 1, 7: 1, 8: 1, 9: 1, 10: 1, 11: 1, 16: 1, 17: 1, 18: 1, 19: 1, 20: 1, 21: 1, 22: 1, 23: 1, 24: 1, 25: 1, 26: 1, 32: 1, 48: 1, 49: 1, 50: 1, 51: 1, 52: 1, 53: 1, 54: 1, 56: 1, 58: 1, 59: 1, 64: 1, 65: 1, 66: 1, 67: 1, 68: 1, 69: 1, 81: 1, 84: 1, 88: 1, 89: 1, 90: 1, 96: 1, 97: 1, 98: 1, 99: 1, 100: 1, 101: 1, 102: 1, 103: 1, 104: 1, 105: 1, 106: 1, 107: 1, 108: 1, 109: 1, 110: 1, 111: 1, 112: 1, 113: 1, 114: 1, 115: 1, 116: 1, 117: 1, 118: 1, 119: 1, 120: 1, 121: 1, 122: 1, 123: 1, 124: 1, 125: 1, 126: 1, 127: 1, 128: 1, 129: 1, 130: 1, 131: 1, 132: 1, 133: 1, 134: 1, 135: 1, 136: 1, 137: 1, 138: 1, 139: 1, 140: 1, 141: 1, 142: 1, 143: 1, 144: 1, 145: 1, 146: 1, 147: 1, 148: 1, 149: 1, 150: 1, 151: 1, 152: 1, 153: 1, 154: 1, 155: 1, 156: 1, 157: 1, 158: 1, 159: 1, 160: 2, 161: 3, 162: 4, 163: 5, 164: 6, 165: 7, 166: 8, 167: 9, 168: 10, 169: 11, 170: 12, 171: 13, 172: 14, 173: 15, 174: 16, 175: 17, 176: 2, 177: 3, 178: 4, 179: 5, 180: 6, 181: 7, 182: 8, 183: 9, 184: 10, 185: 11, 186: 12, 187: 13, 188: 14, 189: 15, 190: 16, 191: 17, 240: 1, 241: 1, 242: 1},

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
				for(var i = 0; i < this.npushes[prevop.op]; i++)
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
