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
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
)

const (
	executionDataDir = "/data/gqrl/execution-data"
	beaconDataDir    = "/data/qrysm/beacon-data"
	genesisMount     = "/network-configs"
	jwtMount         = "/jwt"
	elIPPlaceholder  = "FRESHSYNC_EL_IP_ADDR"
	clIPPlaceholder  = "FRESHSYNC_CL_IP_ADDR"
)

var (
	syncModeArg  = regexp.MustCompile(`(^|[[:space:]])--syncmode(?:=|[[:space:]]+)(full|snap)([[:space:]]|$)`)
	natArg       = regexp.MustCompile(`(^|[[:space:]])--nat=extip:[^[:space:]]+([[:space:]]|$)`)
	bootnodes    = regexp.MustCompile(`(^|[[:space:]])--bootnodes=[^[:space:]]+`)
	verbosityArg = regexp.MustCompile(`(^|[[:space:]])--verbosity(?:=|[[:space:]]+)[^[:space:]]+([[:space:]]|$)`)
)

type rawServiceConfig map[string]json.RawMessage

type portSpec struct {
	Number uint32 `json:"number"`
}

func parseServiceConfig(output string) (rawServiceConfig, error) {
	for offset, char := range output {
		if char != '{' {
			continue
		}
		var cfg rawServiceConfig
		decoder := json.NewDecoder(strings.NewReader(output[offset:]))
		if err := decoder.Decode(&cfg); err != nil {
			continue
		}
		if _, ok := cfg["image"]; ok {
			return cfg, nil
		}
	}
	return nil, fmt.Errorf("output did not contain a Kurtosis JSON service config")
}

func (cfg rawServiceConfig) marshal() ([]byte, error) {
	return json.MarshalIndent(cfg, "", "  ")
}

func (cfg rawServiceConfig) decode(key string, value any) error {
	raw, ok := cfg[key]
	if !ok {
		return fmt.Errorf("service config is missing %q", key)
	}
	if err := json.Unmarshal(raw, value); err != nil {
		return fmt.Errorf("decode service config %q: %w", key, err)
	}
	return nil
}

func (cfg rawServiceConfig) set(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	cfg[key] = raw
	return nil
}

func (cfg rawServiceConfig) requirePort(id string, want uint32) error {
	var ports map[string]portSpec
	if err := cfg.decode("ports", &ports); err != nil {
		return err
	}
	port, ok := ports[id]
	if !ok {
		return fmt.Errorf("service config is missing %q port", id)
	}
	if port.Number != want {
		return fmt.Errorf("service config port %q is %d, want %d", id, port.Number, want)
	}
	return nil
}

func (cfg rawServiceConfig) requireSafeCloneSurface() error {
	for _, key := range []string{"privileged", "host_pid_namespace"} {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		var enabled bool
		if err := json.Unmarshal(raw, &enabled); err != nil {
			return fmt.Errorf("decode service config %q: %w", key, err)
		}
		if enabled {
			return fmt.Errorf("refusing to clone service with %s enabled", key)
		}
	}
	if raw, ok := cfg["bind_mounts"]; ok {
		var mounts map[string]string
		if err := json.Unmarshal(raw, &mounts); err != nil {
			return fmt.Errorf("decode service config %q: %w", "bind_mounts", err)
		}
		if len(mounts) != 0 {
			return fmt.Errorf("refusing to clone service with host bind mounts")
		}
	}
	for key, raw := range cfg {
		lowerKey := strings.ToLower(key)
		if key == "files" || key == "bind_mounts" ||
			(!strings.Contains(lowerKey, "persistent") &&
				!strings.Contains(lowerKey, "director") &&
				!strings.Contains(lowerKey, "volume") &&
				!strings.Contains(lowerKey, "mount")) {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err != nil {
			return fmt.Errorf("decode persistence-like field %q: %w", key, err)
		}
		if !jsonValueEmpty(value) {
			return fmt.Errorf("refusing unknown non-empty persistence-like field %q; update the clone sanitizer for this Kurtosis schema", key)
		}
	}
	return nil
}

func jsonValueEmpty(value any) bool {
	switch value := value.(type) {
	case nil:
		return true
	case bool:
		return !value
	case string:
		return value == ""
	case []any:
		return len(value) == 0
	case map[string]any:
		return len(value) == 0
	default:
		return false
	}
}

func (cfg rawServiceConfig) prepareFiles(dataDir string) error {
	var files map[string][]string
	if err := cfg.decode("files", &files); err != nil {
		return err
	}
	for _, required := range []string{genesisMount, jwtMount} {
		artifacts, ok := files[required]
		if !ok || len(artifacts) == 0 {
			return fmt.Errorf("service config must preserve non-empty %s artifact mount", required)
		}
	}
	cleanDataDir := path.Clean(dataDir)
	cleaned := make(map[string][]string, len(files))
	for mount, artifacts := range files {
		cleanMount := path.Clean(mount)
		if !strings.HasPrefix(cleanMount, "/") {
			return fmt.Errorf("service config contains non-absolute mount %q", mount)
		}
		if cleanMount == cleanDataDir || strings.HasPrefix(cleanMount, cleanDataDir+"/") {
			continue
		}
		if (cleanMount != "/" && strings.HasPrefix(cleanDataDir, cleanMount+"/")) || cleanMount == "/" {
			return fmt.Errorf("mount %q is a parent of %s and could seed the fresh datadir", mount, dataDir)
		}
		cleaned[mount] = append([]string(nil), artifacts...)
	}
	for _, required := range []string{genesisMount, jwtMount} {
		if !equalStrings(cleaned[required], files[required]) {
			return fmt.Errorf("required artifact mount %s changed while stripping datadir state", required)
		}
	}
	return cfg.set("files", cleaned)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func mutateExecutionConfig(cfg rawServiceConfig, syncMode string) error {
	if err := cfg.requireSafeCloneSurface(); err != nil {
		return err
	}
	var image string
	if err := cfg.decode("image", &image); err != nil {
		return err
	}
	if strings.TrimSpace(image) == "" {
		return fmt.Errorf("execution template has an empty image")
	}
	if err := cfg.requirePort("rpc", 8545); err != nil {
		return err
	}
	if err := cfg.requirePort("engine-rpc", 8551); err != nil {
		return err
	}
	if err := cfg.prepareFiles(executionDataDir); err != nil {
		return err
	}
	delete(cfg, "public_ports")
	if err := cfg.set("private_ip_address_placeholder", elIPPlaceholder); err != nil {
		return err
	}

	var entrypoint []string
	if err := cfg.decode("entrypoint", &entrypoint); err != nil {
		return err
	}
	if len(entrypoint) != 2 || entrypoint[0] != "sh" || entrypoint[1] != "-c" {
		return fmt.Errorf("execution template entrypoint is %q, want [sh -c] for the fail-closed datadir guard", entrypoint)
	}
	var cmd []string
	if err := cfg.decode("cmd", &cmd); err != nil {
		return err
	}
	if len(cmd) != 1 {
		return fmt.Errorf("execution template command has %d elements, want one shell command", len(cmd))
	}
	command := cmd[0]
	if !strings.Contains(command, "gqrl init") || !strings.Contains(command, "--datadir="+executionDataDir) {
		return fmt.Errorf("execution template does not initialize and use %s", executionDataDir)
	}
	if len(syncModeArg.FindAllStringIndex(command, -1)) != 1 {
		return fmt.Errorf("execution template must contain exactly one --syncmode=full|snap flag")
	}
	command = syncModeArg.ReplaceAllString(command, "${1}--syncmode="+syncMode+"${3}")
	if syncMode == "snap" {
		// The fresh-sync gate verifies the snap state-download and pivot markers
		// from this service's own logs. Both markers are debug-level messages.
		matches := verbosityArg.FindAllStringIndex(command, -1)
		if len(matches) > 1 {
			return fmt.Errorf("execution template contains more than one --verbosity flag")
		}
		if len(matches) == 1 {
			command = verbosityArg.ReplaceAllString(command, "${1}--verbosity=4${2}")
		} else {
			command += " --verbosity=4"
		}
	}
	if len(natArg.FindAllStringIndex(command, -1)) != 1 {
		return fmt.Errorf("execution template must contain exactly one --nat=extip flag")
	}
	command = natArg.ReplaceAllString(command, "${1}--nat=extip:"+elIPPlaceholder+"${2}")
	if !bootnodes.MatchString(command) {
		return fmt.Errorf("execution template has no bootnodes; clone a non-first EL service such as el-2-gqrl-qrysm")
	}
	if !strings.Contains(command, "--signer=") {
		return fmt.Errorf("execution template is not connected to the topology remote signer")
	}
	guard := "if [ -e " + executionDataDir + " ]; then echo 'freshsync: execution datadir existed before initialization' >&2; exit 97; fi; "
	cmd[0] = guard + command
	return cfg.set("cmd", cmd)
}

func mutateBeaconConfig(cfg rawServiceConfig, engineEndpoint string) error {
	if err := cfg.requireSafeCloneSurface(); err != nil {
		return err
	}
	var image string
	if err := cfg.decode("image", &image); err != nil {
		return err
	}
	if strings.TrimSpace(image) == "" {
		return fmt.Errorf("beacon template has an empty image")
	}
	if err := cfg.requirePort("http", 3500); err != nil {
		return err
	}
	if err := cfg.prepareFiles(beaconDataDir); err != nil {
		return err
	}
	delete(cfg, "public_ports")
	if err := cfg.set("private_ip_address_placeholder", clIPPlaceholder); err != nil {
		return err
	}

	var cmd []string
	if err := cfg.decode("cmd", &cmd); err != nil {
		return err
	}
	var endpointCount, hostCount, bootstrapCount int
	mutated := make([]string, 0, len(cmd)+2)
	for _, arg := range cmd {
		switch {
		case strings.HasPrefix(arg, "--execution-endpoint="):
			endpointCount++
			mutated = append(mutated, "--execution-endpoint="+engineEndpoint)
		case strings.HasPrefix(arg, "--p2p-host-ip="):
			hostCount++
			mutated = append(mutated, "--p2p-host-ip="+clIPPlaceholder)
		case arg == "--p2p-static-id=true":
			// A cloned empty beacon datadir must generate its own peer identity.
		case strings.HasPrefix(arg, "--min-sync-peers=") || strings.HasPrefix(arg, "--sync-from="):
			// Normalize these below so the final occurrence cannot be overridden.
		default:
			if strings.HasPrefix(arg, "--bootstrap-node=") && len(strings.TrimPrefix(arg, "--bootstrap-node=")) > 0 {
				bootstrapCount++
			}
			mutated = append(mutated, arg)
		}
	}
	if endpointCount != 1 {
		return fmt.Errorf("beacon template must contain exactly one --execution-endpoint flag, got %d", endpointCount)
	}
	if hostCount != 1 {
		return fmt.Errorf("beacon template must contain exactly one --p2p-host-ip flag, got %d", hostCount)
	}
	if bootstrapCount == 0 {
		return fmt.Errorf("beacon template has no bootstrap node; clone a non-first CL service such as cl-2-qrysm-gqrl")
	}
	if !containsPrefix(mutated, "--datadir="+beaconDataDir) {
		return fmt.Errorf("beacon template does not use %s", beaconDataDir)
	}
	mutated = append(mutated, "--min-sync-peers=0", "--sync-from=head")
	return cfg.set("cmd", mutated)
}

func containsPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func engineURL(ip string, port uint32) (string, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil || parsed.IsUnspecified() || parsed.IsLoopback() {
		return "", fmt.Errorf("admin_nodeInfo returned unusable private IP %q", ip)
	}
	endpoint := url.URL{Scheme: "http", Host: net.JoinHostPort(ip, fmt.Sprintf("%d", port))}
	return endpoint.String(), nil
}
