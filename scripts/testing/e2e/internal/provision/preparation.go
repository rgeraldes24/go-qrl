// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package provision

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var (
	shaPattern    = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

type Source struct {
	Repository string `json:"repository"`
	Revision   string `json:"revision"`
}

type EffectiveParams struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type Image struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type BuildFlags struct {
	Execution bool `json:"execution"`
	Qrysm     bool `json:"qrysm"`
	Genesis   bool `json:"genesis"`
}

type Preparation struct {
	Schema          int               `json:"schema"`
	SourceSHA       string            `json:"source_sha"`
	WorktreeDirty   bool              `json:"worktree_dirty"`
	QRLPackage      Source            `json:"qrl_package"`
	Qrysm           Source            `json:"qrysm"`
	Generator       Source            `json:"generator"`
	EffectiveParams EffectiveParams   `json:"effective_params"`
	Images          map[string]Image  `json:"images"`
	Build           BuildFlags        `json:"build"`
	Versions        map[string]string `json:"versions"`
}

func (preparation Preparation) Validate(expectedSource string, requireClean bool) error {
	if preparation.Schema != 1 {
		return fmt.Errorf("preparation schema is %d, want 1", preparation.Schema)
	}
	if !shaPattern.MatchString(preparation.SourceSHA) || preparation.SourceSHA != expectedSource {
		return fmt.Errorf("preparation source %q does not match expected source %q", preparation.SourceSHA, expectedSource)
	}
	if requireClean && preparation.WorktreeDirty {
		return errors.New("certified preparation was produced from a dirty worktree")
	}
	for label, source := range map[string]Source{"qrl package": preparation.QRLPackage, "Qrysm": preparation.Qrysm, "generator": preparation.Generator} {
		if source.Repository == "" || !shaPattern.MatchString(source.Revision) {
			return fmt.Errorf("%s source is not pinned to an exact commit", label)
		}
	}
	if preparation.EffectiveParams.Path == "" || !digestPattern.MatchString(preparation.EffectiveParams.SHA256) {
		return errors.New("effective network parameters path or digest is invalid")
	}
	for _, role := range []string{"execution", "alltools", "consensus", "validator", "genesis"} {
		image, ok := preparation.Images[role]
		if !ok || image.Name == "" || !strings.HasPrefix(image.ID, "sha256:") {
			return fmt.Errorf("prepared %s image name or ID is missing", role)
		}
	}
	for _, tool := range []string{"docker", "kurtosis", "yq", "go"} {
		if strings.TrimSpace(preparation.Versions[tool]) == "" {
			return fmt.Errorf("preparation did not record %s version", tool)
		}
	}
	return verifyFileDigest(preparation.EffectiveParams.Path, preparation.EffectiveParams.SHA256)
}

func (preparation Preparation) PackageLocator() string {
	return preparation.QRLPackage.Repository + "@" + preparation.QRLPackage.Revision
}

func (preparation Preparation) SerializedParams() (string, error) {
	payload, err := os.ReadFile(preparation.EffectiveParams.Path)
	if err != nil {
		return "", err
	}
	if err := verifyDigest(payload, preparation.EffectiveParams.SHA256); err != nil {
		return "", err
	}
	return string(payload), nil
}

type CommandRunner interface {
	Run(context.Context, string, []string, []string, io.Writer, io.Writer) error
}

type ExecRunner struct{}

func (ExecRunner) Run(ctx context.Context, command string, args, environment []string, stdout, stderr io.Writer) error {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = environment
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

type Options struct {
	RepoRoot          string
	NetworkParams     string
	EffectiveOutput   string
	PreparationOutput string
	SourceSHA         string
	CI                bool
	ExtraEnvironment  map[string]string
}

func Prepare(ctx context.Context, runner CommandRunner, options Options, output io.Writer) (Preparation, error) {
	if runner == nil {
		runner = ExecRunner{}
	}
	if !shaPattern.MatchString(options.SourceSHA) {
		return Preparation{}, errors.New("preparation source SHA must be exact")
	}
	if options.RepoRoot == "" || options.NetworkParams == "" || options.EffectiveOutput == "" || options.PreparationOutput == "" {
		return Preparation{}, errors.New("repository, input params, effective output, and preparation output are required")
	}
	script := filepath.Join(options.RepoRoot, "scripts", "local_testnet", "prepare_local_testnet.sh")
	args := []string{"-n", options.NetworkParams}
	if options.CI {
		args = append(args, "-c")
	}
	values := make(map[string]string)
	for _, item := range os.Environ() {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	for key, value := range map[string]string{
		"SOURCE_SHA":              options.SourceSHA,
		"EFFECTIVE_PARAMS_OUTPUT": options.EffectiveOutput,
		"PREPARATION_OUTPUT":      options.PreparationOutput,
	} {
		values[key] = value
	}
	for key, value := range options.ExtraEnvironment {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	environment := make([]string, 0, len(keys))
	for _, key := range keys {
		environment = append(environment, key+"="+values[key])
	}
	if err := runner.Run(ctx, script, args, environment, output, output); err != nil {
		return Preparation{}, fmt.Errorf("prepare local testnet: %w", err)
	}
	preparation, err := Load(options.PreparationOutput)
	if err != nil {
		return Preparation{}, err
	}
	if err := preparation.Validate(options.SourceSHA, options.CI); err != nil {
		return Preparation{}, err
	}
	return preparation, nil
}

func Load(path string) (Preparation, error) {
	file, err := os.Open(path)
	if err != nil {
		return Preparation{}, err
	}
	defer file.Close()
	var preparation Preparation
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&preparation); err != nil {
		return Preparation{}, fmt.Errorf("decode preparation metadata: %w", err)
	}
	if decoder.Decode(new(any)) != io.EOF {
		return Preparation{}, errors.New("preparation metadata contains trailing data")
	}
	return preparation, nil
}

func verifyFileDigest(path, expected string) error {
	payload, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return verifyDigest(payload, expected)
}

func verifyDigest(payload []byte, expected string) error {
	digest := sha256.Sum256(payload)
	actual := hex.EncodeToString(digest[:])
	if actual != expected {
		return fmt.Errorf("content digest %s does not match recorded digest %s", actual, expected)
	}
	return nil
}
