// Copyright 2026 The go-qrl Authors
// This file is part of the go-qrl library.

package doctor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
)

const RequiredKurtosisVersion = "1.20.0"

var (
	cliVersionPattern    = regexp.MustCompile(`(?im)^\s*CLI\s+Version\s*:\s*v?([0-9]+\.[0-9]+\.[0-9]+)\b`)
	engineVersionPattern = regexp.MustCompile(`(?im)^\s*(?:Engine\s+)?Version\s*:\s*v?([0-9]+\.[0-9]+\.[0-9]+)\b`)
	shaPattern           = regexp.MustCompile(`^[0-9a-f]{40}$`)
	digestImagePattern   = regexp.MustCompile(`@sha256:[0-9a-f]{64}$`)
)

type Result struct {
	Name    string `json:"name"`
	Passed  bool   `json:"passed"`
	Details string `json:"details"`
}

type Report struct {
	RequiredKurtosisVersion string   `json:"required_kurtosis_version"`
	Results                 []Result `json:"results"`
}

func (report Report) Validate() error {
	var failed []string
	for _, result := range report.Results {
		if !result.Passed {
			failed = append(failed, result.Name+": "+result.Details)
		}
	}
	if len(failed) > 0 {
		return errors.New(strings.Join(failed, "; "))
	}
	return nil
}

type Runner interface {
	LookPath(string) (string, error)
	Output(context.Context, string, ...string) ([]byte, error)
}

type ExecRunner struct{}

func (ExecRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (ExecRunner) Output(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

type Options struct {
	RepoRoot       string
	SourceSHA      string
	NetworkParams  string
	RequireEngine  bool
	RequiredTools  []string
	PinnedImages   []string
	BuiltBinaries  []string
	KurtosisBinary string
}

func Run(ctx context.Context, runner Runner, options Options) Report {
	if runner == nil {
		runner = ExecRunner{}
	}
	if options.KurtosisBinary == "" {
		options.KurtosisBinary = "kurtosis"
	}
	if len(options.RequiredTools) == 0 {
		options.RequiredTools = []string{"bash", "docker", "git", "go", "node", "openssl", "python3", "yq"}
	}
	report := Report{RequiredKurtosisVersion: RequiredKurtosisVersion}
	add := func(name string, err error, details string) {
		result := Result{Name: name, Passed: err == nil, Details: details}
		if err != nil {
			result.Details = err.Error()
		}
		report.Results = append(report.Results, result)
	}

	root, err := filepath.Abs(options.RepoRoot)
	if err == nil {
		if info, statErr := os.Stat(filepath.Join(root, "go.mod")); statErr != nil || info.IsDir() {
			err = errors.New("repository root does not contain go.mod")
		}
	}
	add("repository", err, root)

	if !shaPattern.MatchString(options.SourceSHA) {
		add("source SHA", errors.New("source SHA must be an exact lowercase commit"), options.SourceSHA)
	} else {
		output, commandErr := runner.Output(ctx, "git", "-C", root, "rev-parse", "HEAD")
		actual := strings.TrimSpace(string(output))
		if commandErr != nil {
			add("source SHA", fmt.Errorf("git rev-parse HEAD: %w: %s", commandErr, actual), actual)
		} else if actual != options.SourceSHA {
			add("source SHA", fmt.Errorf("checkout is %s, want %s", actual, options.SourceSHA), actual)
		} else {
			add("source SHA", nil, actual)
		}
	}

	if options.NetworkParams == "" {
		add("network parameters", errors.New("network parameters path is required"), "")
	} else if info, statErr := os.Stat(options.NetworkParams); statErr != nil || info.IsDir() {
		add("network parameters", fmt.Errorf("network parameters are unreadable: %w", statErr), options.NetworkParams)
	} else {
		add("network parameters", nil, options.NetworkParams)
	}

	tools := append([]string(nil), options.RequiredTools...)
	sort.Strings(tools)
	for _, tool := range tools {
		path, lookErr := runner.LookPath(tool)
		add("tool "+tool, lookErr, path)
	}

	kurtosisPath, lookErr := runner.LookPath(options.KurtosisBinary)
	if lookErr != nil {
		add("Kurtosis CLI", lookErr, "")
	} else {
		output, commandErr := runner.Output(ctx, kurtosisPath, "version")
		versionOutput := stripANSI(strings.TrimSpace(string(output)))
		if commandErr != nil {
			add("Kurtosis CLI version", fmt.Errorf("kurtosis version: %w: %s", commandErr, versionOutput), versionOutput)
		} else if versionErr := requireVersion(versionOutput, cliVersionPattern, "CLI"); versionErr != nil {
			add("Kurtosis CLI version", versionErr, versionOutput)
		} else {
			add("Kurtosis CLI version", nil, versionOutput)
		}
		if options.RequireEngine {
			engineOutput, engineErr := runner.Output(ctx, kurtosisPath, "engine", "status")
			engineVersionOutput := stripANSI(strings.TrimSpace(string(engineOutput)))
			if engineErr != nil {
				add("Kurtosis engine version", fmt.Errorf("kurtosis engine status: %w: %s", engineErr, engineVersionOutput), engineVersionOutput)
			} else if versionErr := requireVersion(engineVersionOutput, engineVersionPattern, "engine"); versionErr != nil {
				add("Kurtosis engine version", versionErr, engineVersionOutput)
			} else {
				add("Kurtosis engine version", nil, engineVersionOutput)
			}
		}
	}

	for index, image := range options.PinnedImages {
		err := error(nil)
		if !digestImagePattern.MatchString(image) {
			err = errors.New("external image is not pinned by sha256 digest")
		}
		add(fmt.Sprintf("pinned image %d", index+1), err, image)
	}
	for _, binary := range options.BuiltBinaries {
		info, statErr := os.Stat(binary)
		if statErr == nil && info.Mode()&0o111 == 0 {
			statErr = errors.New("binary is not executable")
		}
		add("binary "+filepath.Base(binary), statErr, binary)
	}

	add("platform", nil, runtime.GOOS+"/"+runtime.GOARCH)
	return report
}

func requireVersion(output string, pattern *regexp.Regexp, kind string) error {
	match := pattern.FindStringSubmatch(output)
	if len(match) != 2 {
		return fmt.Errorf("Kurtosis %s output did not identify its version", kind)
	}
	if match[1] != RequiredKurtosisVersion {
		return fmt.Errorf("Kurtosis %s version %s does not equal required %s", kind, match[1], RequiredKurtosisVersion)
	}
	return nil
}

func stripANSI(value string) string {
	ansi := regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	return ansi.ReplaceAllString(value, "")
}
