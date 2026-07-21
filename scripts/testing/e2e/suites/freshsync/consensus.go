// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.
//
// The go-qrl library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-qrl library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-qrl library. If not, see <http://www.gnu.org/licenses/>.

package freshsync

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

const (
	syncStatusPath = "/qrl/v1/node/syncing"
	peerCountPath  = "/qrl/v1/node/peer_count"
)

type beaconSyncResponse struct {
	Data struct {
		HeadSlot     string `json:"head_slot"`
		SyncDistance string `json:"sync_distance"`
		IsSyncing    bool   `json:"is_syncing"`
		IsOptimistic bool   `json:"is_optimistic"`
		ELOffline    bool   `json:"el_offline"`
	} `json:"data"`
}

type beaconPeerCountResponse struct {
	Data struct {
		Connected string `json:"connected"`
	} `json:"data"`
}

type beaconStatus struct {
	headSlot       uint64
	syncDistance   uint64
	connectedPeers uint64
}

type httpReader struct {
	client *http.Client
}

func (r httpReader) getJSON(ctx context.Context, baseURL, requestPath string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+requestPath, nil)
	if err != nil {
		return err
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("GET %s: status %s: %s", req.URL, resp.Status, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", req.URL, err)
	}
	return nil
}

func (r httpReader) beaconStatus(ctx context.Context, baseURL string) (beaconStatus, error) {
	var syncResp beaconSyncResponse
	if err := r.getJSON(ctx, baseURL, syncStatusPath, &syncResp); err != nil {
		return beaconStatus{}, err
	}
	if syncResp.Data.IsSyncing {
		return beaconStatus{}, fmt.Errorf("beacon node is syncing")
	}
	if syncResp.Data.IsOptimistic {
		return beaconStatus{}, fmt.Errorf("beacon node is optimistic")
	}
	if syncResp.Data.ELOffline {
		return beaconStatus{}, fmt.Errorf("beacon node reports its execution client offline")
	}
	headSlot, err := strconv.ParseUint(syncResp.Data.HeadSlot, 10, 64)
	if err != nil {
		return beaconStatus{}, fmt.Errorf("invalid beacon head slot %q: %w", syncResp.Data.HeadSlot, err)
	}
	if headSlot == 0 {
		return beaconStatus{}, fmt.Errorf("beacon node remains at genesis")
	}
	syncDistance, err := strconv.ParseUint(syncResp.Data.SyncDistance, 10, 64)
	if err != nil {
		return beaconStatus{}, fmt.Errorf("invalid beacon sync distance %q: %w", syncResp.Data.SyncDistance, err)
	}
	if syncDistance != 0 {
		return beaconStatus{}, fmt.Errorf("beacon sync distance is %d", syncDistance)
	}

	var peers beaconPeerCountResponse
	if err := r.getJSON(ctx, baseURL, peerCountPath, &peers); err != nil {
		return beaconStatus{}, err
	}
	connected, err := strconv.ParseUint(peers.Data.Connected, 10, 64)
	if err != nil {
		return beaconStatus{}, fmt.Errorf("invalid connected peer count %q: %w", peers.Data.Connected, err)
	}
	if connected == 0 {
		return beaconStatus{}, fmt.Errorf("beacon node has no connected peers")
	}
	return beaconStatus{headSlot: headSlot, syncDistance: syncDistance, connectedPeers: connected}, nil
}
