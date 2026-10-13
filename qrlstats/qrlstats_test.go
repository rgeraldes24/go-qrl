// Copyright 2021 The go-ethereum Authors
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

package qrlstats

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	qrl "github.com/theQRL/go-qrl"
	"github.com/theQRL/go-qrl/crypto"
	"github.com/theQRL/go-qrl/p2p"
)

func TestParseQRLstatsURL(t *testing.T) {
	cases := []struct {
		url              string
		node, pass, host string
	}{
		{
			url:  `"debug meowsbits":mypass@ws://mordor.dash.fault.dev:3000`,
			node: "debug meowsbits", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `"debug @meowsbits":mypass@ws://mordor.dash.fault.dev:3000`,
			node: "debug @meowsbits", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `"debug: @meowsbits":mypass@ws://mordor.dash.fault.dev:3000`,
			node: "debug: @meowsbits", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `name:@ws://mordor.dash.fault.dev:3000`,
			node: "name", pass: "", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `name@ws://mordor.dash.fault.dev:3000`,
			node: "name", pass: "", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `:mypass@ws://mordor.dash.fault.dev:3000`,
			node: "", pass: "mypass", host: "ws://mordor.dash.fault.dev:3000",
		},
		{
			url:  `:@ws://mordor.dash.fault.dev:3000`,
			node: "", pass: "", host: "ws://mordor.dash.fault.dev:3000",
		},
	}

	for i, c := range cases {
		parts, err := parseQRLstatsURL(c.url)
		if err != nil {
			t.Fatal(err)
		}
		node, pass, host := parts[0], parts[1], parts[2]

		// unquote because the value provided will be used as a CLI flag value, so unescaped quotes will be removed
		nodeUnquote, err := strconv.Unquote(node)
		if err == nil {
			node = nodeUnquote
		}

		if node != c.node {
			t.Errorf("case=%d mismatch node value, got: %v ,want: %v", i, node, c.node)
		}
		if pass != c.pass {
			t.Errorf("case=%d mismatch pass value, got: %v ,want: %v", i, pass, c.pass)
		}
		if host != c.host {
			t.Errorf("case=%d mismatch host value, got: %v ,want: %v", i, host, c.host)
		}
	}
}

type syncBackend struct {
	backend
	progress qrl.SyncProgress
}

func (b *syncBackend) SyncProgress() qrl.SyncProgress {
	return b.progress
}

func TestReportStatsSyncing(t *testing.T) {
	reports := make(chan json.RawMessage)
	ws := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := new(websocket.Upgrader).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		defer close(reports)
		for {
			var msg map[string][]json.RawMessage
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			reports <- msg["emit"][1]
		}
	}))
	defer ws.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ws.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	key, _ := crypto.GenerateKey()
	server := &p2p.Server{Config: p2p.Config{PrivateKey: key, NoDial: true, NoDiscovery: true}}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	defer server.Stop()

	for _, c := range []struct {
		progress qrl.SyncProgress
		syncing  bool
	}{
		{qrl.SyncProgress{}, false},
		{qrl.SyncProgress{CurrentBlock: 5, HighestBlock: 10}, true},
		{qrl.SyncProgress{CurrentBlock: 10, HighestBlock: 10}, false},
	} {
		s := &Service{backend: &syncBackend{progress: c.progress}, server: server}
		if err := s.reportStats(newConnectionWrapper(conn)); err != nil {
			t.Fatal(err)
		}
		var msg struct {
			Stats nodeStats `json:"stats"`
		}
		if err := json.Unmarshal(<-reports, &msg); err != nil {
			t.Fatal(err)
		}
		if msg.Stats.Syncing != c.syncing {
			t.Errorf("syncing mismatch at block %d of %d, got: %v, want: %v", c.progress.CurrentBlock, c.progress.HighestBlock, msg.Stats.Syncing, c.syncing)
		}
	}
}
