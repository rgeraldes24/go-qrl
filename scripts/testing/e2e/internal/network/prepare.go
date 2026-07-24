// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package network

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type preparedNetwork struct {
	Params       string
	ParamsDigest string
	Images       []ImageIdentity
}

type localImageSpec struct {
	role, environmentName string
}

var localImageSpecs = [...]localImageSpec{
	{role: "execution", environmentName: "E2E_LOCAL_EL_IMAGE"},
	{role: "consensus", environmentName: "E2E_LOCAL_CL_IMAGE"},
	{role: "validator", environmentName: "E2E_LOCAL_VC_IMAGE"},
	{role: "genesis", environmentName: "E2E_LOCAL_GENESIS_IMAGE"},
}

var imageLockValuePattern = regexp.MustCompile(`^[A-Za-z0-9./:@_-]+$`)

func prepareNetwork(ctx context.Context, runner commandRunner, request StartRequest, sourceCommit, walletAddress string, stdout, stderr io.Writer) (preparedNetwork, error) {
	if request.BuildTool == "" {
		return preparedNetwork{}, errors.New("network image build tool is required")
	}
	if request.DockerBin == "" {
		request.DockerBin = "docker"
	}
	isolatedRefs, err := isolatedLocalImageRefs(localImageTemplates, sourceCommit, request.NetworkDir)
	if err != nil {
		return preparedNetwork{}, err
	}
	params, err := effectiveParameters(walletAddress, isolatedRefs)
	if err != nil {
		return preparedNetwork{}, err
	}
	seed, err := os.ReadFile(walletSeedPath(request.NetworkDir))
	if err != nil {
		return preparedNetwork{}, err
	}
	if secret := strings.TrimSpace(string(seed)); secret != "" && strings.Contains(params, secret) {
		return preparedNetwork{}, errors.New("refusing package parameters containing wallet seed")
	}
	pathEnv := os.Getenv("PATH")
	if filepath.IsAbs(request.DockerBin) {
		pathEnv = filepath.Dir(request.DockerBin) + string(os.PathListSeparator) + pathEnv
	}
	buildEnvironment, err := isolatedImageBuildEnvironment(pathEnv, isolatedRefs)
	if err != nil {
		return preparedNetwork{}, err
	}
	buildEnvironment = append(buildEnvironment, pinnedBuildEnvironment()...)
	buildEnvironment = append(buildEnvironment, "E2E_DOCKER_BIN="+request.DockerBin)
	if err := runner.Run(ctx, command{Path: request.BuildTool, Dir: request.RepoRoot, Env: buildEnvironment, Stdout: stdout, Stderr: stderr}); err != nil {
		return preparedNetwork{}, fmt.Errorf("build pinned network images: %w", err)
	}

	images := make([]ImageIdentity, 0, len(isolatedRefs))
	for role, ref := range isolatedRefs {
		image, err := inspectImage(ctx, runner, request.DockerBin, role, ref)
		if err != nil {
			return preparedNetwork{}, err
		}
		images = append(images, image)
	}
	sort.Slice(images, func(i, j int) bool { return images[i].Role < images[j].Role })
	if revision := imageByRole(images, "execution").Labels["org.opencontainers.image.revision"]; revision != sourceCommit {
		return preparedNetwork{}, fmt.Errorf("execution image revision %q differs from source %q", revision, sourceCommit)
	}
	for _, role := range []string{"consensus", "validator"} {
		if revision := imageByRole(images, role).Labels["org.opencontainers.image.revision"]; revision != qrysmCommit {
			return preparedNetwork{}, fmt.Errorf("%s image revision %q differs from pinned source", role, revision)
		}
	}
	if revision := imageByRole(images, "genesis").Labels["org.opencontainers.image.revision"]; revision != genesisCommit {
		return preparedNetwork{}, fmt.Errorf("genesis image revision %q differs from pinned source", revision)
	}
	return preparedNetwork{Params: params, ParamsDigest: digestCanonicalJSON(params), Images: images}, nil
}

// isolatedLocalImageRefs converts the human-readable refs kept in the tracked
// configuration into network-private refs. The complete clean source
// commit and canonical network directory are included so separate checkouts
// and network directories cannot retag images used by another live network.
func isolatedLocalImageRefs(templates map[string]string, sourceCommit, networkDir string) (map[string]string, error) {
	if !commitPattern.MatchString(sourceCommit) {
		return nil, errors.New("source identity is invalid for isolated image refs")
	}
	canonicalNetworkDir, err := canonicalExistingDirectory(networkDir, "network directory")
	if err != nil {
		return nil, err
	}
	tag := "e2e-" + networkInstanceID(sourceCommit, canonicalNetworkDir)
	refs := make(map[string]string, len(localImageSpecs))
	for _, specification := range localImageSpecs {
		base := templates[specification.role]
		lastSlash, lastColon := strings.LastIndex(base, "/"), strings.LastIndex(base, ":")
		if lastColon <= lastSlash || lastColon == 0 || lastColon == len(base)-1 || strings.Contains(base, "@") {
			return nil, fmt.Errorf("%s image template must be a tagged mutable local reference", specification.role)
		}
		ref := base[:lastColon] + ":" + tag
		if !imageLockValuePattern.MatchString(ref) {
			return nil, fmt.Errorf("isolated %s image reference is invalid", specification.role)
		}
		refs[specification.role] = ref
	}
	return refs, nil
}

// networkInstanceID requires an exact commit and canonical network directory.
func networkInstanceID(commit, canonicalNetworkDir string) string {
	digest := sha256.Sum256([]byte(commit + "\x00" + canonicalNetworkDir))
	return hex.EncodeToString(digest[:])
}

func isolatedImageBuildEnvironment(pathEnv string, refs map[string]string) ([]string, error) {
	if len(refs) != len(localImageSpecs) {
		return nil, errors.New("isolated image reference set is incomplete")
	}
	environment := []string{"PATH=" + pathEnv}
	for _, specification := range localImageSpecs {
		ref := refs[specification.role]
		if ref == "" || !imageLockValuePattern.MatchString(ref) || strings.Contains(ref, "@") {
			return nil, fmt.Errorf("isolated %s image reference is invalid", specification.role)
		}
		environment = append(environment, specification.environmentName+"="+ref)
	}
	return environment, nil
}

func effectiveParameters(address string, refs map[string]string) (string, error) {
	if _, err := isolatedImageBuildEnvironment("", refs); err != nil {
		return "", err
	}
	if !addressPattern.MatchString(address) {
		return "", errors.New("wallet address is invalid")
	}
	parameters := map[string]any{
		"participants": []any{map[string]any{
			"el_type": "gqrl", "el_image": refs["execution"],
			"el_extra_params": []any{"--graphql", "--graphql.vhosts=*"},
			"cl_type":         "qrysm", "cl_image": refs["consensus"],
			"cl_extra_params":   []any{"--min-sync-peers=0", "--minimum-peers-per-subnet=0"},
			"vc_type":           "qrysm",
			"vc_image":          refs["validator"],
			"count":             1,
			"use_remote_signer": false,
		}},
		"network_params": map[string]any{
			"preset": "mainnet", "network_id": "1337",
			"seconds_per_slot": 5, "slots_per_epoch": 128,
			"execution_follow_distance": 8,
			"prefunded_accounts":        map[string]any{address: map[string]any{"balance": prefundBalance}},
			"withdrawal_address":        address,
			"light_kdf_enabled":         true,
		},
		"qrl_genesis_generator_params": map[string]any{"image": refs["genesis"]},
		"additional_services":          []any{},
	}
	canonical, err := json.Marshal(parameters)
	if err != nil {
		return "", err
	}
	return string(canonical), nil
}

type dockerInspection struct {
	ID     string `json:"Id"`
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
}

func inspectImage(ctx context.Context, runner commandRunner, dockerBin, role, ref string) (ImageIdentity, error) {
	output, err := runner.CombinedOutput(ctx, command{Path: dockerBin, Args: []string{"image", "inspect", ref}})
	if err != nil {
		return ImageIdentity{}, fmt.Errorf("inspect %s image %q: %w: %s", role, ref, err, strings.TrimSpace(string(output)))
	}
	var inspections []dockerInspection
	if err := json.Unmarshal(output, &inspections); err != nil || len(inspections) != 1 {
		return ImageIdentity{}, fmt.Errorf("decode %s image inspection: %w", role, err)
	}
	if !strings.HasPrefix(inspections[0].ID, "sha256:") {
		return ImageIdentity{}, fmt.Errorf("%s image has invalid Docker ID %q", role, inspections[0].ID)
	}
	labels := inspections[0].Config.Labels
	if labels == nil {
		labels = map[string]string{}
	}
	return ImageIdentity{Role: role, Ref: ref, ID: inspections[0].ID, Labels: labels}, nil
}

func imageByRole(images []ImageIdentity, role string) ImageIdentity {
	for _, image := range images {
		if image.Role == role {
			return image
		}
	}
	return ImageIdentity{}
}

func digestBytes(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func digestCanonicalJSON(value string) string {
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return ""
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return ""
	}
	return digestBytes(canonical)
}
