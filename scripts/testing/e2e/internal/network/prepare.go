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
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	e2eprocess "github.com/theQRL/go-qrl/scripts/testing/e2e/internal/process"
)

const networkCommandOutputLimit = e2eprocess.DefaultMaxOutputBytes

type command struct {
	Path, Dir string
	Args      []string
	Env       []string
	Stdout    io.Writer
	Stderr    io.Writer
}

type commandRunner interface {
	Run(context.Context, command) error
	CombinedOutput(context.Context, command) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, specification command) error {
	_, err := runNetworkCommand(ctx, specification)
	return err
}

func (execRunner) CombinedOutput(ctx context.Context, specification command) ([]byte, error) {
	result, err := runNetworkCommand(ctx, specification)
	output := make([]byte, 0, len(result.Stdout)+len(result.Stderr))
	output = append(output, result.Stdout...)
	output = append(output, result.Stderr...)
	return output, err
}

func runNetworkCommand(ctx context.Context, specification command) (e2eprocess.Result, error) {
	return e2eprocess.Run(ctx, e2eprocess.Command{
		Path:              specification.Path,
		Args:              specification.Args,
		Dir:               specification.Dir,
		Env:               specification.Env,
		EnvRemovePrefixes: []string{"E2E_"},
		Stdout:            specification.Stdout,
		Stderr:            specification.Stderr,
		MaxOutputBytes:    networkCommandOutputLimit,
	})
}

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

func resumePreparedNetwork(ctx context.Context, runner commandRunner, request StartRequest, record LifecycleRecord) (preparedNetwork, error) {
	if record.Package.Locator != packageLocator || record.Package.ID != packageID {
		return preparedNetwork{}, errors.New("lifecycle differs from the built-in qrl-package")
	}
	paramsData, err := os.ReadFile(filepath.Join(privatePath(record.NetworkDir), "effective-params.json"))
	if err != nil {
		return preparedNetwork{}, err
	}
	params := strings.TrimSpace(string(paramsData))
	if digestCanonicalJSON(params) != record.Package.ParamsSHA256 {
		return preparedNetwork{}, errors.New("effective parameters differ from lifecycle")
	}
	dockerBin := request.DockerBin
	if dockerBin == "" {
		dockerBin = "docker"
	}
	for _, expected := range record.Images {
		actual, err := inspectImage(ctx, runner, dockerBin, expected.Role, expected.Ref)
		if err != nil {
			return preparedNetwork{}, err
		}
		if actual.ID != expected.ID || !maps.Equal(actual.Labels, expected.Labels) {
			return preparedNetwork{}, fmt.Errorf("%s image changed since lifecycle intent", expected.Role)
		}
	}
	return preparedNetwork{Params: params, ParamsDigest: record.Package.ParamsSHA256, Images: slices.Clone(record.Images)}, nil
}

func prepareNetwork(ctx context.Context, runner commandRunner, request StartRequest, source SourceIdentity, wallet WalletIdentity, stdout, stderr io.Writer) (preparedNetwork, error) {
	if request.BuildTool == "" {
		return preparedNetwork{}, errors.New("network image build tool is required")
	}
	if request.DockerBin == "" {
		request.DockerBin = "docker"
	}
	isolatedRefs, err := isolatedLocalImageRefs(localImageTemplates, source, request.NetworkDir)
	if err != nil {
		return preparedNetwork{}, err
	}
	params, err := effectiveParameters(wallet.Address, isolatedRefs)
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

	effectivePath := filepath.Join(privatePath(request.NetworkDir), "effective-params.json")
	if err := writePrivateFile(effectivePath, []byte(params+"\n")); err != nil {
		return preparedNetwork{}, err
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
	if revision := imageByRole(images, "execution").Labels["org.opencontainers.image.revision"]; revision != source.Commit {
		return preparedNetwork{}, fmt.Errorf("execution image revision %q differs from source %q", revision, source.Commit)
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
// configuration into lifecycle-private refs. The complete clean source
// commit and canonical network directory are included so separate checkouts
// and network directories cannot retag images used by another live network.
func isolatedLocalImageRefs(templates map[string]string, source SourceIdentity, networkDir string) (map[string]string, error) {
	if !commitPattern.MatchString(source.Commit) {
		return nil, errors.New("source identity is invalid for isolated image refs")
	}
	canonicalNetworkDir, err := canonicalExistingDirectory(networkDir, "network directory")
	if err != nil {
		return nil, err
	}
	tag := "e2e-" + networkInstanceID(source.Commit, canonicalNetworkDir)
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

func writePrivateFile(path string, data []byte) error {
	parent, err := os.Lstat(filepath.Dir(path))
	if err != nil || parent.Mode()&os.ModeSymlink != 0 || !parent.IsDir() {
		return errors.New("private file parent must be a non-symlink directory")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return fmt.Errorf("private file %s has an unexpected type", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return syncDirectory(filepath.Dir(path))
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
