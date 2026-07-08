// Copyright 2026 The go-QRL Authors
// This file is part of the go-QRL library.
//
// The go-QRL library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package abi

import "github.com/theQRL/go-qrl/common/uint512"

// abiWordBytes is the width of one ABI slot, matching the VM64 stack word.
const abiWordBytes = uint512.WordBytes
