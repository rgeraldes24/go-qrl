// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package beacon provides typed, bounded reads for the QRL beacon APIs.
package beacon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/rpc"
)

const (
	DefaultAPIPrefix     = "/qrl/v1"
	DefaultSlotsPerEpoch = uint64(128)
)

type Options struct {
	HTTP          rpc.HTTPOptions
	APIPrefix     string
	SlotsPerEpoch uint64
}

type Client struct {
	http          *rpc.HTTP
	prefix        string
	slotsPerEpoch uint64
}

func New(endpoint string, options Options) (*Client, error) {
	transport, err := rpc.NewHTTP(endpoint, options.HTTP)
	if err != nil {
		return nil, err
	}
	return NewClient(transport, options)
}

func NewClient(transport *rpc.HTTP, options Options) (*Client, error) {
	if transport == nil {
		return nil, errors.New("beacon HTTP transport is nil")
	}
	prefix := options.APIPrefix
	if prefix == "" {
		prefix = DefaultAPIPrefix
	}
	if !strings.HasPrefix(prefix, "/") || strings.ContainsAny(prefix, "?#") || strings.Contains(prefix, "..") {
		return nil, fmt.Errorf("invalid beacon API prefix %q", prefix)
	}
	slotsPerEpoch := options.SlotsPerEpoch
	if slotsPerEpoch == 0 {
		slotsPerEpoch = DefaultSlotsPerEpoch
	}
	return &Client{http: transport, prefix: strings.TrimRight(prefix, "/"), slotsPerEpoch: slotsPerEpoch}, nil
}

func (c *Client) Syncing(ctx context.Context) (SyncStatus, error) {
	var response struct {
		Data *struct {
			HeadSlot     decimalUint64 `json:"head_slot"`
			SyncDistance decimalUint64 `json:"sync_distance"`
			IsSyncing    bool          `json:"is_syncing"`
			IsOptimistic bool          `json:"is_optimistic"`
			ELOffline    bool          `json:"el_offline"`
		} `json:"data"`
	}
	if err := c.get(ctx, "node/syncing", &response); err != nil {
		return SyncStatus{}, err
	}
	if response.Data == nil {
		return SyncStatus{}, errors.New("beacon syncing response has no data")
	}
	return SyncStatus{
		HeadSlot: uint64(response.Data.HeadSlot), SyncDistance: uint64(response.Data.SyncDistance),
		IsSyncing: response.Data.IsSyncing, IsOptimistic: response.Data.IsOptimistic, ELOffline: response.Data.ELOffline,
	}, nil
}

func (c *Client) Peers(ctx context.Context) (PeerCount, error) {
	var response struct {
		Data *struct {
			Connected     decimalUint64 `json:"connected"`
			Connecting    decimalUint64 `json:"connecting"`
			Disconnected  decimalUint64 `json:"disconnected"`
			Disconnecting decimalUint64 `json:"disconnecting"`
		} `json:"data"`
	}
	if err := c.get(ctx, "node/peer_count", &response); err != nil {
		return PeerCount{}, err
	}
	if response.Data == nil {
		return PeerCount{}, errors.New("beacon peer-count response has no data")
	}
	return PeerCount{
		Connected: uint64(response.Data.Connected), Connecting: uint64(response.Data.Connecting),
		Disconnected: uint64(response.Data.Disconnected), Disconnecting: uint64(response.Data.Disconnecting),
	}, nil
}

func (c *Client) Genesis(ctx context.Context) (Genesis, error) {
	var response struct {
		Data *struct {
			GenesisTime           decimalUint64 `json:"genesis_time"`
			GenesisValidatorsRoot string        `json:"genesis_validators_root"`
			GenesisForkVersion    string        `json:"genesis_fork_version"`
		} `json:"data"`
	}
	if err := c.get(ctx, "beacon/genesis", &response); err != nil {
		return Genesis{}, err
	}
	if response.Data == nil {
		return Genesis{}, errors.New("beacon genesis response has no data")
	}
	return Genesis{
		GenesisTime: uint64(response.Data.GenesisTime), GenesisValidatorsRoot: strings.ToLower(response.Data.GenesisValidatorsRoot),
		GenesisForkVersion: strings.ToLower(response.Data.GenesisForkVersion),
	}, nil
}

func (c *Client) Spec(ctx context.Context) (map[string]string, error) {
	var response struct {
		Data map[string]string `json:"data"`
	}
	if err := c.get(ctx, "config/spec", &response); err != nil {
		return nil, err
	}
	if response.Data == nil {
		return nil, errors.New("beacon spec response has no data")
	}
	return response.Data, nil
}

func (c *Client) Finality(ctx context.Context, stateID string) (Finality, error) {
	if stateID == "" {
		return Finality{}, errors.New("beacon state ID is required")
	}
	type rawCheckpoint struct {
		Epoch decimalUint64 `json:"epoch"`
		Root  string        `json:"root"`
	}
	var response struct {
		ExecutionOptimistic bool `json:"execution_optimistic"`
		Data                *struct {
			PreviousJustified rawCheckpoint `json:"previous_justified"`
			CurrentJustified  rawCheckpoint `json:"current_justified"`
			Finalized         rawCheckpoint `json:"finalized"`
		} `json:"data"`
	}
	if err := c.get(ctx, "beacon/states/"+url.PathEscape(stateID)+"/finality_checkpoints", &response); err != nil {
		return Finality{}, err
	}
	if response.Data == nil {
		return Finality{}, errors.New("beacon finality response has no data")
	}
	convert := func(raw rawCheckpoint) Checkpoint {
		return Checkpoint{Epoch: uint64(raw.Epoch), Root: strings.ToLower(raw.Root)}
	}
	return Finality{
		PreviousJustified: convert(response.Data.PreviousJustified), CurrentJustified: convert(response.Data.CurrentJustified),
		Finalized: convert(response.Data.Finalized), ExecutionOptimistic: response.ExecutionOptimistic,
	}, nil
}

func (c *Client) ProposerDuties(ctx context.Context, epoch uint64) (ProposerDutySet, error) {
	if epoch > ^uint64(0)/c.slotsPerEpoch {
		return ProposerDutySet{}, fmt.Errorf("proposer duty epoch %d overflows slot range", epoch)
	}
	firstSlot := epoch * c.slotsPerEpoch
	if firstSlot > ^uint64(0)-c.slotsPerEpoch {
		return ProposerDutySet{}, fmt.Errorf("proposer duty epoch %d overflows slot range", epoch)
	}
	var response struct {
		DependentRoot       string `json:"dependent_root"`
		ExecutionOptimistic bool   `json:"execution_optimistic"`
		Data                []struct {
			Pubkey         string        `json:"pubkey"`
			ValidatorIndex decimalUint64 `json:"validator_index"`
			Slot           decimalUint64 `json:"slot"`
		} `json:"data"`
	}
	if err := c.get(ctx, "validator/duties/proposer/"+strconv.FormatUint(epoch, 10), &response); err != nil {
		return ProposerDutySet{}, err
	}
	lastSlot := firstSlot + c.slotsPerEpoch
	seen := make(map[uint64]struct{}, len(response.Data))
	duties := make([]ProposerDuty, 0, len(response.Data))
	for index, raw := range response.Data {
		slot := uint64(raw.Slot)
		if slot < firstSlot || slot >= lastSlot {
			return ProposerDutySet{}, fmt.Errorf("proposer duty %d slot %d is outside epoch %d", index, slot, epoch)
		}
		if _, exists := seen[slot]; exists {
			return ProposerDutySet{}, fmt.Errorf("proposer duties repeat slot %d", slot)
		}
		if strings.TrimSpace(raw.Pubkey) == "" {
			return ProposerDutySet{}, fmt.Errorf("proposer duty %d has an empty pubkey", index)
		}
		seen[slot] = struct{}{}
		duties = append(duties, ProposerDuty{
			Pubkey: strings.ToLower(raw.Pubkey), ValidatorIndex: uint64(raw.ValidatorIndex), Slot: slot,
		})
	}
	return ProposerDutySet{
		DependentRoot: strings.ToLower(response.DependentRoot), ExecutionOptimistic: response.ExecutionOptimistic, Duties: duties,
	}, nil
}

func (c *Client) Header(ctx context.Context, blockID string) (Header, error) {
	if blockID == "" {
		return Header{}, errors.New("beacon block ID is required")
	}
	var response struct {
		ExecutionOptimistic bool `json:"execution_optimistic"`
		Data                *struct {
			Root      string `json:"root"`
			Canonical bool   `json:"canonical"`
			Header    *struct {
				Message *struct {
					Slot          decimalUint64 `json:"slot"`
					ProposerIndex decimalUint64 `json:"proposer_index"`
				} `json:"message"`
			} `json:"header"`
		} `json:"data"`
	}
	if err := c.get(ctx, "beacon/headers/"+url.PathEscape(blockID), &response); err != nil {
		return Header{}, err
	}
	if response.Data == nil || response.Data.Header == nil || response.Data.Header.Message == nil {
		return Header{}, errors.New("beacon header response is incomplete")
	}
	return Header{
		Root: strings.ToLower(response.Data.Root), Canonical: response.Data.Canonical,
		Slot: uint64(response.Data.Header.Message.Slot), ProposerIndex: uint64(response.Data.Header.Message.ProposerIndex),
		ExecutionOptimistic: response.ExecutionOptimistic,
	}, nil
}

func (c *Client) HeaderBySlot(ctx context.Context, slot uint64) (Header, error) {
	return c.Header(ctx, strconv.FormatUint(slot, 10))
}

func (c *Client) FinalizedBlock(ctx context.Context) (FinalizedBlock, error) {
	var response struct {
		Version             string `json:"version"`
		ExecutionOptimistic bool   `json:"execution_optimistic"`
		Finalized           bool   `json:"finalized"`
		Data                *struct {
			Message *struct {
				Body *struct {
					ExecutionPayload *struct {
						BlockNumber decimalUint64 `json:"block_number"`
						BlockHash   string        `json:"block_hash"`
					} `json:"execution_payload"`
				} `json:"body"`
			} `json:"message"`
		} `json:"data"`
	}
	if err := c.get(ctx, "beacon/blocks/finalized", &response); err != nil {
		return FinalizedBlock{}, err
	}
	if response.Data == nil || response.Data.Message == nil || response.Data.Message.Body == nil || response.Data.Message.Body.ExecutionPayload == nil {
		return FinalizedBlock{}, errors.New("finalized beacon block has no execution payload")
	}
	payload := response.Data.Message.Body.ExecutionPayload
	return FinalizedBlock{
		Version: strings.ToLower(response.Version), ExecutionOptimistic: response.ExecutionOptimistic, Finalized: response.Finalized,
		ExecutionPayload: ExecutionPayload{BlockNumber: uint64(payload.BlockNumber), BlockHash: strings.ToLower(payload.BlockHash)},
	}, nil
}

// Validator reads the QRL validator-status compatibility endpoint used by the
// deposit suite. The public key is base64 encoded in the query string and is
// never included in client errors.
func (c *Client) Validator(ctx context.Context, publicKey []byte) (ValidatorStatus, error) {
	if len(publicKey) == 0 {
		return ValidatorStatus{}, errors.New("validator public key is required")
	}
	query := url.Values{"public_key": {base64.StdEncoding.EncodeToString(publicKey)}}
	var fields map[string]json.RawMessage
	if err := c.http.GetJSON(ctx, "/qrl/v1alpha1/validator/status?"+query.Encode(), &fields); err != nil {
		return ValidatorStatus{}, err
	}
	statusRaw, ok := fields["status"]
	if !ok {
		return ValidatorStatus{}, errors.New("validator-status response has no status")
	}
	status, err := parseValidatorStatus(statusRaw)
	if err != nil {
		return ValidatorStatus{}, err
	}
	blockRaw, ok := fields["executionDepositBlockNumber"]
	if !ok {
		blockRaw = fields["execution_deposit_block_number"]
	}
	var block decimalUint64
	if len(blockRaw) != 0 && string(blockRaw) != "null" {
		if err := json.Unmarshal(blockRaw, &block); err != nil {
			return ValidatorStatus{}, fmt.Errorf("decode execution deposit block: %w", err)
		}
	}
	return ValidatorStatus{Status: status, ExecutionDepositBlock: uint64(block)}, nil
}

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.http.GetJSON(ctx, c.prefix+"/"+path, out)
}
