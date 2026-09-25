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

package params

import (
	"testing"

	"github.com/theQRL/go-qrl/common"
)

func TestCheckCompatible(t *testing.T) {
	type test struct {
		stored, new *ChainConfig
		wantErr     bool
	}
	tests := []test{
		{stored: AllBeaconProtocolChanges, new: AllBeaconProtocolChanges, wantErr: false},
		{stored: &ChainConfig{}, new: &ChainConfig{}, wantErr: false},
		{stored: &ChainConfig{ChainID: common.Big1}, new: &ChainConfig{ChainID: common.Big32}, wantErr: true},
		{stored: &ChainConfig{ChainID: common.Big1}, new: &ChainConfig{}, wantErr: true},
	}

	for _, test := range tests {
		err := test.stored.CheckCompatible(test.new)
		if (err != nil) != test.wantErr {
			t.Errorf("error mismatch:\nstored: %v\nnew: %v\nerr: %v\nwantErr: %v", test.stored, test.new, err, test.wantErr)
		}
	}
}
