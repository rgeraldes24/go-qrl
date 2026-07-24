// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

// Package network owns the lifecycle and immutable identity of separately
// managed E2E networks. Suite execution receives only Authenticator.
package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/kurtosis"
	"github.com/theQRL/go-qrl/scripts/testing/e2e/internal/topology"
)

const (
	StateSchemaVersion       = 1
	BackendKurtosis          = "kurtosis"
	PhaseRunning             = "running"
	PhaseStopped             = "stopped"
	LifecycleCreateIntent    = "create_intent"
	LifecyclePackageIntent   = "package_intent"
	LifecyclePackageAccepted = "package_accepted"
	LifecycleReady           = "ready"
	LifecycleDestroyIntent   = "destroy_intent"
	LifecycleStopped         = "stopped"
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

type PackageIdentity struct {
	// Locator is the revision-pinned remote reference passed to Kurtosis.
	Locator string `json:"locator"`
	// ID is the package name retained from the package's kurtosis.yml manifest.
	ID           string `json:"id"`
	ParamsSHA256 string `json:"params_sha256"`
}

type SourceIdentity struct {
	Commit string `json:"commit"`
}

type WalletIdentity struct {
	Address string `json:"address"`
}

type ExecutionIdentity struct {
	BinaryPath   string `json:"binary_path"`
	BinarySHA256 string `json:"binary_sha256"`
}

type ImageIdentity struct {
	Role   string            `json:"role"`
	Ref    string            `json:"ref"`
	ID     string            `json:"id"`
	Labels map[string]string `json:"labels"`
}

type ChainIdentity struct {
	ChainID     string `json:"chain_id"`
	GenesisHash string `json:"genesis_hash"`
}

// State is sanitized and safe to upload. It never contains wallet secret
// paths, package output, or serialized package parameters.
type State struct {
	SchemaVersion int                 `json:"schema_version"`
	Backend       string              `json:"backend"`
	Phase         string              `json:"phase"`
	NetworkDir    string              `json:"network_dir"`
	Enclave       kurtosis.EnclaveRef `json:"enclave"`
	Package       PackageIdentity     `json:"package"`
	Source        SourceIdentity      `json:"source"`
	Wallet        WalletIdentity      `json:"wallet"`
	Topology      topology.Snapshot   `json:"topology"`
	Images        []ImageIdentity     `json:"images"`
	Execution     ExecutionIdentity   `json:"execution"`
	Chain         ChainIdentity       `json:"chain"`
	Fingerprint   string              `json:"fingerprint"`
	CreatedAt     time.Time           `json:"created_at"`
	StoppedAt     *time.Time          `json:"stopped_at,omitempty"`
}

func (state State) Validate() error {
	if state.SchemaVersion != StateSchemaVersion || state.Backend != BackendKurtosis {
		return fmt.Errorf("unsupported network schema %d or backend %q", state.SchemaVersion, state.Backend)
	}
	if state.Phase != PhaseRunning && state.Phase != PhaseStopped {
		return fmt.Errorf("unsupported network phase %q", state.Phase)
	}
	if !filepath.IsAbs(state.NetworkDir) || filepath.Clean(state.NetworkDir) != state.NetworkDir {
		return errors.New("network directory must be absolute and canonical")
	}
	if err := state.Enclave.Validate(); err != nil || !state.Enclave.Owned {
		return errors.New("network enclave identity is invalid or not owned")
	}
	if state.Package.Locator == "" || state.Package.ID == "" || !digestPattern.MatchString(state.Package.ParamsSHA256) {
		return errors.New("network package identity is incomplete")
	}
	if !commitPattern.MatchString(state.Source.Commit) {
		return errors.New("network source identity is invalid")
	}
	if !addressPattern.MatchString(state.Wallet.Address) {
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
	if !strings.HasPrefix(state.Execution.BinaryPath, "/") || !digestPattern.MatchString(state.Execution.BinarySHA256) {
		return errors.New("network execution identity is incomplete")
	}
	if state.Chain.ChainID == "" || state.Chain.GenesisHash == "" {
		return errors.New("network chain identity is incomplete")
	}
	if state.CreatedAt.IsZero() {
		return errors.New("network creation time is missing")
	}
	if state.Phase == PhaseRunning && state.StoppedAt != nil || state.Phase == PhaseStopped && state.StoppedAt == nil {
		return errors.New("network phase and stop time disagree")
	}
	fingerprint, err := state.IdentityFingerprint()
	if err != nil {
		return err
	}
	if state.Fingerprint != fingerprint {
		return errors.New("network fingerprint does not match immutable identity")
	}
	return nil
}

// IdentityFingerprint excludes phase and timestamps so stopping a network
// does not change its immutable identity. Every backend/source/topology field
// is included.
func (state State) IdentityFingerprint() (string, error) {
	type identity struct {
		Backend, NetworkDir string
		Enclave             kurtosis.EnclaveRef
		Package             PackageIdentity
		Source              SourceIdentity
		Wallet              WalletIdentity
		Topology            topology.Snapshot
		Images              []ImageIdentity
		Execution           ExecutionIdentity
		Chain               ChainIdentity
	}
	immutable := identity{
		Backend: state.Backend, NetworkDir: state.NetworkDir,
		Enclave: state.Enclave, Package: state.Package, Source: state.Source, Wallet: state.Wallet,
		Topology: state.Topology, Images: slices.Clone(state.Images), Execution: state.Execution, Chain: state.Chain,
	}
	payload, err := json.Marshal(immutable)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

type Result struct {
	State     State            `json:"state"`
	Ready     bool             `json:"ready"`
	Message   string           `json:"message,omitempty"`
	Lifecycle *LifecycleRecord `json:"lifecycle,omitempty"`
}

type Environment struct {
	NetworkDir   string
	RPCURL       string
	GraphQLURL   string
	WebSocketURL string
	SeedFile     string
}

// Requirements is the least-privilege network surface needed by an ordered
// suite plan. RPC identity and chain advancement are always authenticated.
type Requirements struct {
	Signer    bool
	GraphQL   bool
	WebSocket bool
}

func FullRequirements() Requirements {
	return Requirements{Signer: true, GraphQL: true, WebSocket: true}
}

type StartRequest struct {
	RepoRoot     string
	NetworkDir   string
	EnclaveName  string
	BuildTool    string
	DockerBin    string
	StartTimeout time.Duration
}

// LifecycleRecord is the single private write-ahead record for every external
// network mutation. Create intent is published exclusively before Kurtosis is
// contacted; later phases are replaced atomically. An exact enclave UUID is
// mandatory after create_intent and is never inferred from a name.
type LifecycleRecord struct {
	SchemaVersion      int                  `json:"schema_version"`
	Phase              string               `json:"phase"`
	NetworkDir         string               `json:"network_dir"`
	RequestedName      string               `json:"requested_name"`
	Enclave            *kurtosis.EnclaveRef `json:"enclave,omitempty"`
	Package            PackageIdentity      `json:"package"`
	Source             SourceIdentity       `json:"source"`
	Images             []ImageIdentity      `json:"images"`
	CreatedAt          time.Time            `json:"created_at"`
	EnclaveCapturedAt  *time.Time           `json:"enclave_captured_at,omitempty"`
	PackageAcceptedAt  *time.Time           `json:"package_accepted_at,omitempty"`
	NetworkFingerprint string               `json:"network_fingerprint,omitempty"`
	DestroyRequestedAt *time.Time           `json:"destroy_requested_at,omitempty"`
	DestroyedAt        *time.Time           `json:"destroyed_at,omitempty"`
}

func (record LifecycleRecord) Validate() error {
	validPhase := record.Phase == LifecycleCreateIntent || record.Phase == LifecyclePackageIntent ||
		record.Phase == LifecyclePackageAccepted || record.Phase == LifecycleReady ||
		record.Phase == LifecycleDestroyIntent || record.Phase == LifecycleStopped
	if record.SchemaVersion != 1 || !validPhase {
		return errors.New("invalid lifecycle schema or phase")
	}
	if !filepath.IsAbs(record.NetworkDir) || filepath.Clean(record.NetworkDir) != record.NetworkDir ||
		strings.TrimSpace(record.RequestedName) == "" {
		return errors.New("invalid lifecycle directory or requested name")
	}
	if record.Package.Locator == "" || record.Package.ID == "" ||
		!digestPattern.MatchString(record.Package.ParamsSHA256) ||
		!commitPattern.MatchString(record.Source.Commit) {
		return errors.New("lifecycle identity is incomplete")
	}
	if err := validateImageIdentities(record.Images); err != nil {
		return fmt.Errorf("lifecycle: %w", err)
	}
	if record.CreatedAt.IsZero() || !isUTC(record.CreatedAt) {
		return errors.New("lifecycle creation time is missing or not UTC")
	}
	if record.Phase == LifecycleCreateIntent {
		if record.Enclave != nil || record.EnclaveCapturedAt != nil || record.PackageAcceptedAt != nil ||
			record.NetworkFingerprint != "" || record.DestroyRequestedAt != nil || record.DestroyedAt != nil {
			return errors.New("create intent contains outcome state")
		}
		return nil
	}
	if record.Enclave == nil || record.Enclave.Name != record.RequestedName ||
		record.Enclave.Validate() != nil || !record.Enclave.Owned ||
		record.EnclaveCapturedAt == nil || record.EnclaveCapturedAt.Before(record.CreatedAt) ||
		!isUTC(*record.EnclaveCapturedAt) {
		return errors.New("lifecycle exact enclave ownership is invalid")
	}
	accepted := record.Phase == LifecyclePackageAccepted || record.Phase == LifecycleReady
	if accepted && record.PackageAcceptedAt == nil {
		return errors.New("lifecycle package acceptance and phase disagree")
	}
	if record.Phase == LifecyclePackageIntent && record.PackageAcceptedAt != nil {
		return errors.New("package intent already contains an acceptance outcome")
	}
	if record.PackageAcceptedAt != nil &&
		(record.PackageAcceptedAt.Before(*record.EnclaveCapturedAt) || !isUTC(*record.PackageAcceptedAt)) {
		return errors.New("lifecycle package acceptance time is invalid")
	}
	if (record.Phase == LifecyclePackageIntent || record.Phase == LifecyclePackageAccepted) &&
		record.NetworkFingerprint != "" {
		return errors.New("pre-ready lifecycle contains a network fingerprint")
	}
	ready := record.Phase == LifecycleReady || record.Phase == LifecycleDestroyIntent || record.Phase == LifecycleStopped
	if ready && record.NetworkFingerprint != "" && !digestPattern.MatchString(record.NetworkFingerprint) {
		return errors.New("lifecycle network fingerprint is invalid")
	}
	if record.Phase == LifecycleReady && record.NetworkFingerprint == "" {
		return errors.New("ready lifecycle has no network fingerprint")
	}
	destroying := record.Phase == LifecycleDestroyIntent || record.Phase == LifecycleStopped
	if destroying != (record.DestroyRequestedAt != nil) {
		return errors.New("lifecycle destroy intent and phase disagree")
	}
	if record.DestroyRequestedAt != nil &&
		(record.DestroyRequestedAt.Before(*record.EnclaveCapturedAt) || !isUTC(*record.DestroyRequestedAt)) {
		return errors.New("lifecycle destroy request time is invalid")
	}
	if (record.Phase == LifecycleStopped) != (record.DestroyedAt != nil) {
		return errors.New("lifecycle destroy completion and phase disagree")
	}
	if record.DestroyedAt != nil &&
		(record.DestroyedAt.Before(*record.DestroyRequestedAt) || !isUTC(*record.DestroyedAt)) {
		return errors.New("lifecycle destroy completion time is invalid")
	}
	return nil
}

func (record LifecycleRecord) OwnedEnclave() (kurtosis.EnclaveRef, error) {
	if err := record.Validate(); err != nil {
		return kurtosis.EnclaveRef{}, err
	}
	if record.Enclave == nil {
		return kurtosis.EnclaveRef{}, errors.New("lifecycle has no durably captured enclave UUID")
	}
	return *record.Enclave, nil
}

func isUTC(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0
}

type Authenticator interface {
	Authenticate(context.Context, string, string, Requirements) (Environment, error)
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
