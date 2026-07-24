// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package network owns the lifecycle and immutable identity of separately
// managed E2E networks. Suite execution receives only Authenticator.
package network

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
)

var (
	commitPattern  = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	addressPattern = regexp.MustCompile(`^Q[0-9a-fA-F]{128}$`)
)

var requiredImageRoles = map[string]struct{}{
	"execution": {},
	"consensus": {},
	"validator": {},
	"genesis":   {},
}

type SourceIdentity struct {
	Commit string
}

type WalletIdentity struct {
	Address string
}

type ImageIdentity struct {
	Role   string            `json:"role"`
	Ref    string            `json:"ref"`
	ID     string            `json:"id"`
	Labels map[string]string `json:"-"`
}

// State is the small public authentication baseline for a ready network. It
// deliberately omits fixed configuration, package parameters, and secrets.
type State struct {
	ParamsSHA256  string            `json:"params_sha256"`
	SourceCommit  string            `json:"source_commit"`
	WalletAddress string            `json:"wallet_address"`
	Topology      topology.Snapshot `json:"topology"`
	Images        []ImageIdentity   `json:"images"`
	BinarySHA256  string            `json:"binary_sha256"`
	GenesisHash   string            `json:"genesis_hash"`
}

func (state State) Validate() error {
	if !digestPattern.MatchString(state.ParamsSHA256) {
		return errors.New("network parameter digest is invalid")
	}
	if !commitPattern.MatchString(state.SourceCommit) {
		return errors.New("network source identity is invalid")
	}
	if !addressPattern.MatchString(state.WalletAddress) {
		return errors.New("network wallet identity is invalid")
	}
	if err := state.Topology.Validate(); err != nil {
		return fmt.Errorf("network topology: %w", err)
	}
	if err := validateImageIdentities(state.Images); err != nil {
		return err
	}
	if executionImage := imageByRole(state.Images, "execution"); executionImage.ID == "" || executionImage.Ref != state.Topology.Execution.Image {
		return errors.New("network execution image differs from the image identity set")
	}
	if !digestPattern.MatchString(state.BinarySHA256) {
		return errors.New("network execution identity is incomplete")
	}
	if state.GenesisHash == "" {
		return errors.New("network chain identity is incomplete")
	}
	return nil
}

type Result struct {
	state   State
	Ready   bool   `json:"ready"`
	Message string `json:"message,omitempty"`
}

type Environment struct {
	NetworkDir   string
	RPCURL       string
	GraphQLURL   string
	WebSocketURL string
	SeedFile     string
}

type StartRequest struct {
	RepoRoot     string
	NetworkDir   string
	EnclaveName  string
	BuildTool    string
	DockerBin    string
	StartTimeout time.Duration
}

// OwnershipRecord is the sole private lifecycle record. A name-only creation
// intent prevents replay if Kurtosis loses the create response; the exact UUID
// is captured as soon as creation returns and retained until destruction is
// confirmed.
type OwnershipRecord struct {
	NetworkDir    string               `json:"network_dir"`
	RequestedName string               `json:"requested_name"`
	Enclave       *kurtosis.EnclaveRef `json:"enclave,omitempty"`
}

func (record OwnershipRecord) Validate() error {
	if !filepath.IsAbs(record.NetworkDir) || filepath.Clean(record.NetworkDir) != record.NetworkDir {
		return errors.New("invalid ownership directory")
	}
	if strings.TrimSpace(record.RequestedName) == "" {
		return errors.New("ownership requested name is empty")
	}
	if record.Enclave == nil {
		return nil
	}
	if record.Enclave.Name != record.RequestedName ||
		record.Enclave.Validate() != nil ||
		!record.Enclave.Owned {
		return errors.New("ownership enclave identity is invalid or not owned")
	}
	return nil
}

func (record OwnershipRecord) OwnedEnclave() (kurtosis.EnclaveRef, error) {
	if err := record.Validate(); err != nil {
		return kurtosis.EnclaveRef{}, err
	}
	if record.Enclave == nil {
		return kurtosis.EnclaveRef{}, fmt.Errorf(
			"enclave creation outcome for %q is ambiguous: exact UUID was not captured",
			record.RequestedName,
		)
	}
	return *record.Enclave, nil
}

type Authenticator interface {
	Authenticate(context.Context, string, string) (Environment, error)
}

type Controller interface {
	Start(context.Context, StartRequest) (Result, error)
	Status(context.Context, string) (Result, error)
	Stop(context.Context, string) (Result, error)
}

func validateImageIdentities(images []ImageIdentity) error {
	if len(images) != len(requiredImageRoles) {
		return fmt.Errorf("network image identities must contain exactly %d roles", len(requiredImageRoles))
	}
	seen := make(map[string]struct{}, len(images))
	for _, image := range images {
		if _, required := requiredImageRoles[image.Role]; !required {
			return fmt.Errorf("network image role %q is not declared", image.Role)
		}
		if image.Ref == "" || !strings.HasPrefix(image.ID, "sha256:") {
			return fmt.Errorf("network image identity is invalid: %+v", image)
		}
		if _, exists := seen[image.Role]; exists {
			return fmt.Errorf("network image role %q is duplicated", image.Role)
		}
		seen[image.Role] = struct{}{}
	}
	return nil
}
