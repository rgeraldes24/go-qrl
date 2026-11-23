// Copyright 2022 The go-ethereum Authors
// This file is part of go-ethereum.
//
// go-ethereum is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// go-ethereum is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with go-ethereum. If not, see <http://www.gnu.org/licenses/>.

package main

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/theQRL/go-qrl/common"
)

func initGqrl(t *testing.T) string {
	args := []string{"--networkid=42", "init", "./testdata/genesis.json"}
	t.Logf("Initializing gqrl: %v ", args)
	g := runGqrl(t, args...)
	datadir := g.Datadir
	g.WaitExit()
	return datadir
}

// TestExport does a basic test of "gqrl export", exporting the test-genesis.
func TestExport(t *testing.T) {
	outfile := fmt.Sprintf("%v/testExport.out", t.TempDir())
	defer os.Remove(outfile)
	gqrl := runGqrl(t, "--datadir", initGqrl(t), "export", outfile)
	gqrl.WaitExit()
	if have, want := gqrl.ExitStatus(), 0; have != want {
		t.Errorf("exit error, have %d want %d", have, want)
	}
	have, err := os.ReadFile(outfile)
	if err != nil {
		t.Fatal(err)
	}
	// Exported RLP includes full 64-byte state, transaction, receipt and
	// withdrawal commitments.
	want := common.FromHex("0xf90318f90313a00000000000000000000000000000000000000000000000000000000000000000b84000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000b840f4f38e7ae52f755f06eada567b0cccf49a5dddb9692cc1f4fcc7ebc656d72eee853b8da1d3ea122ddbbfd63203675f7b29e9bc373ac385208bf69da35b1c94b1b840a088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52eb840a088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52eb901000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000080837a12008080b875000000000000000000000000000000000000000000000000000000000000000002f0d131f1f97aef08aec6e3291b957d9efe71050000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000000a0000000000000000000000000000000000000000000000000000000000000000085174876e800b840a088c353acbb4ba1d73c451acd04ce0c09dc20b5991305555c66d143aa763dc629084179101f69d2fef68731dd99c81a8357595303d53eace341a141df4cb52ec0c0")
	if !bytes.Equal(have, want) {
		t.Fatalf("wrong content exported\nhave: %x\nwant: %x", have, want)
	}
}
