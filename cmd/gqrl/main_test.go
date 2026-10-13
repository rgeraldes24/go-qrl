// Copyright 2026 The go-ethereum Authors
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
	"flag"
	"testing"

	"github.com/theQRL/go-qrl/cmd/utils"
	"github.com/urfave/cli/v2"
)

// Tests that the default cache allowance is only bumped on mainnet.
func TestPrepareCacheBump(t *testing.T) {
	tests := []struct {
		args []string
		want int
	}{
		{nil, 4096},
		{[]string{"--mainnet"}, 4096},
		{[]string{"--cache", "64"}, 64},
		{[]string{"--networkid", "1337"}, utils.CacheFlag.Value},
		{[]string{"--betanet"}, utils.CacheFlag.Value},
		{[]string{"--testnet"}, utils.CacheFlag.Value},
		{[]string{"--dev"}, utils.CacheFlag.Value},
	}
	for _, tt := range tests {
		set := flag.NewFlagSet("test", flag.ContinueOnError)
		for _, f := range []cli.Flag{utils.CacheFlag, utils.NetworkIdFlag, utils.MainnetFlag, utils.BetaNetFlag, utils.TestnetFlag, utils.DeveloperFlag} {
			if err := f.Apply(set); err != nil {
				t.Fatal(err)
			}
		}
		if err := set.Parse(tt.args); err != nil {
			t.Fatal(err)
		}
		ctx := cli.NewContext(nil, set, nil)
		prepare(ctx)
		if have := ctx.Int(utils.CacheFlag.Name); have != tt.want {
			t.Errorf("args %v: cache is %d, want %d", tt.args, have, tt.want)
		}
	}
}
