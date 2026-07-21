// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package beacon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type SyncStatus struct {
	HeadSlot     uint64
	SyncDistance uint64
	IsSyncing    bool
	IsOptimistic bool
	ELOffline    bool
}

type PeerCount struct {
	Connected     uint64
	Connecting    uint64
	Disconnected  uint64
	Disconnecting uint64
}

type Checkpoint struct {
	Epoch uint64
	Root  string
}

type Finality struct {
	PreviousJustified   Checkpoint
	CurrentJustified    Checkpoint
	Finalized           Checkpoint
	ExecutionOptimistic bool
}

type Genesis struct {
	GenesisTime           uint64
	GenesisValidatorsRoot string
	GenesisForkVersion    string
}

type ProposerDuty struct {
	Pubkey         string
	ValidatorIndex uint64
	Slot           uint64
}

type ProposerDutySet struct {
	DependentRoot       string
	ExecutionOptimistic bool
	Duties              []ProposerDuty
}

type Header struct {
	Root                string
	Canonical           bool
	Slot                uint64
	ProposerIndex       uint64
	ExecutionOptimistic bool
}

type ExecutionPayload struct {
	BlockNumber uint64
	BlockHash   string
}

type FinalizedBlock struct {
	Version             string
	ExecutionOptimistic bool
	Finalized           bool
	ExecutionPayload    ExecutionPayload
}

type ValidatorStatus struct {
	Status                string
	ExecutionDepositBlock uint64
}

const (
	ValidatorUnknown            = "UNKNOWN_STATUS"
	ValidatorDeposited          = "DEPOSITED"
	ValidatorPending            = "PENDING"
	ValidatorActive             = "ACTIVE"
	ValidatorExiting            = "EXITING"
	ValidatorSlashing           = "SLASHING"
	ValidatorExited             = "EXITED"
	ValidatorInvalid            = "INVALID"
	ValidatorPartiallyDeposited = "PARTIALLY_DEPOSITED"
)

var validatorStatusNumbers = map[string]uint64{
	ValidatorUnknown:            0,
	ValidatorDeposited:          1,
	ValidatorPending:            2,
	ValidatorActive:             3,
	ValidatorExiting:            4,
	ValidatorSlashing:           5,
	ValidatorExited:             6,
	ValidatorInvalid:            7,
	ValidatorPartiallyDeposited: 8,
}

type decimalUint64 uint64

func (value *decimalUint64) UnmarshalJSON(raw []byte) error {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var number json.Number
		if err := decoder.Decode(&number); err != nil {
			return fmt.Errorf("expected decimal string or integer: %w", err)
		}
		text = number.String()
	}
	parsed, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid unsigned decimal %q: %w", text, err)
	}
	*value = decimalUint64(parsed)
	return nil
}

func parseValidatorStatus(raw json.RawMessage) (string, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		text = strings.ToUpper(text)
		if _, ok := validatorStatusNumbers[text]; !ok {
			return "", fmt.Errorf("unknown validator status %q", text)
		}
		return text, nil
	}
	var number decimalUint64
	if err := json.Unmarshal(raw, &number); err != nil {
		return "", fmt.Errorf("decode validator status: %w", err)
	}
	for status, value := range validatorStatusNumbers {
		if value == uint64(number) {
			return status, nil
		}
	}
	return "", fmt.Errorf("unknown validator status number %d", number)
}
