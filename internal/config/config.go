// Package config loads the two-layer TOML configuration used by simsquad:
//
//	simsquad.toml        — checked into git, team defaults
//	simsquad.local.toml  — gitignored, per-developer overrides
//
// Either file is optional. Precedence at every resolver (in this package's
// callers) is:
//
//	CLI flag > env var > local TOML > project TOML > hardcoded default
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/peuf0u/simsquad/internal/contract"
)

// Filenames the loader looks for.
const (
	ProjectFile = "simsquad.toml"
	LocalFile   = "simsquad.local.toml"
)

// Config is the deep-merged view of both TOMLs. ConfigDir holds the directory
// the files were resolved from — relative paths inside the config (e.g.
// `[project].ios_repo`) are resolved against it.
type Config struct {
	merged    map[string]any
	configDir string
	// project is simsquad.toml on its own. The merged view can't tell which
	// file a device set came from, and equip writes each set back to its file.
	project map[string]any
	local   map[string]any
}

// Load reads simsquad.toml and simsquad.local.toml from configDir and returns
// a deep-merged Config. Missing files are tolerated (treated as empty).
// configDir of "" defaults to the current working directory.
func Load(configDir string) (*Config, error) {
	if configDir == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("config.Load: getwd: %w", err)
		}
		configDir = wd
	}
	project, err := readTOML(filepath.Join(configDir, ProjectFile))
	if err != nil {
		return nil, err
	}
	local, err := readTOML(filepath.Join(configDir, LocalFile))
	if err != nil {
		return nil, err
	}
	return &Config{
		merged:    mergeLayers(project, local),
		configDir: configDir,
		project:   project,
		local:     local,
	}, nil
}

// FindConfigDir walks up from start looking for the first directory that
// contains simsquad.toml or simsquad.local.toml. Returns the empty string if
// neither is found before reaching the filesystem root — callers should fall
// back to cwd in that case.
func FindConfigDir(start string) string {
	dir, err := filepath.Abs(start)
	if err != nil {
		return ""
	}
	for {
		for _, name := range []string{ProjectFile, LocalFile} {
			if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// ConfigDir returns the directory the loader resolved the TOMLs from.
func (c *Config) ConfigDir() string { return c.configDir }

// Get looks up a dotted key (e.g. "project.ios_scheme") in the merged config
// and returns the value as an interface{}, or nil if absent. Intended for
// scalar lookups; callers cast as needed.
func (c *Config) Get(dotted string) any {
	if c == nil || c.merged == nil {
		return nil
	}
	var cur any = c.merged
	for _, part := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		v, ok := m[part]
		if !ok {
			return nil
		}
		cur = v
	}
	return cur
}

// GetString returns the value at dotted as a string, or "" if absent or of a
// different type.
func (c *Config) GetString(dotted string) string {
	v, _ := c.Get(dotted).(string)
	return v
}

// ResolveProjectPath turns a config-stored path into an absolute path. Relative
// paths are resolved against ConfigDir; absolute paths and ~-prefixed paths
// pass through (after expansion). Returns "" for the empty input.
func (c *Config) ResolveProjectPath(value string) string {
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			value = filepath.Join(home, value[2:])
		}
	}
	if filepath.IsAbs(value) {
		abs, _ := filepath.Abs(value)
		return abs
	}
	abs, _ := filepath.Abs(filepath.Join(c.configDir, value))
	return abs
}

// IOSSpecs reads `[[ios.sims]]` and returns the iOS provisioning matrix.
// Malformed entries (missing device/runtime, non-positive count) are skipped.
func (c *Config) IOSSpecs() []contract.IosSpec { return parseIOSRows(c.Get("ios.sims")) }

func parseIOSRows(v any) []contract.IosSpec {
	raw := tableRows(v)
	out := make([]contract.IosSpec, 0, len(raw))
	for _, s := range raw {
		device := asString(s["device"])
		runtime := asString(s["runtime"])
		count := asInt(s["count"], 1)
		if device == "" || runtime == "" || count < 1 {
			continue
		}
		out = append(out, contract.IosSpec{Device: device, Runtime: runtime, Count: count})
	}
	return out
}

// tableRows returns an array of tables as its rows. BurntSushi unmarshals
// array-of-tables to []map[string]any, but inside a generic map we may
// instead get []any. Handle both shapes.
func tableRows(v any) []map[string]any {
	if raw, ok := v.([]map[string]any); ok {
		return raw
	}
	var raw []map[string]any
	generic, _ := v.([]any)
	for _, e := range generic {
		if m, ok := e.(map[string]any); ok {
			raw = append(raw, m)
		}
	}
	return raw
}

// Context returns the [context] table from simsquad.toml as a string map.
// Missing section or non-string values yield nil/skipped entries. The result
// is the project-wide baseline context that deploy merges its --context flags
// over.
//
// legacy is true when the table came from [env], the pre-0.5.0 name, which
// is still read (when [context] is absent) so callers can warn about it.
func (c *Config) Context() (ctx map[string]string, legacy bool) {
	raw, _ := c.Get("context").(map[string]any)
	if raw == nil {
		raw, _ = c.Get("env").(map[string]any)
		legacy = raw != nil
	}
	if raw == nil {
		return nil, false
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil, legacy
	}
	return out, legacy
}

// LegacyEnvWarning is the stderr line printed when simsquad.toml still uses
// [env].
const LegacyEnvWarning = "simsquad: [env] in simsquad.toml is deprecated; rename it to [context]"

// Secrets returns the [secrets] table merged across both files (a local key
// overrides the project's), or nil. Its keys join the squad context as
// secrets: kept in the squad record, masked in every output. It is a source
// separate from [context]; deploy rejects a key that is in both.
func (c *Config) Secrets() map[string]string {
	return stringTable(c.Get("secrets"))
}

// CommittedSecretKeys returns the sorted keys of simsquad.toml's own
// [secrets] table: secrets in the file that is checked into git.
func (c *Config) CommittedSecretKeys() []string {
	table := stringTable(c.project["secrets"])
	if len(table) == 0 {
		return nil
	}
	keys := make([]string, 0, len(table))
	for k := range table {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// CommittedSecretsWarning is the stderr line for secrets in simsquad.toml.
// It names the keys, never their values.
func CommittedSecretsWarning(keys []string) string {
	return fmt.Sprintf("simsquad: [secrets] in %s (checked into git) holds %s; move it to %s",
		ProjectFile, strings.Join(keys, ", "), LocalFile)
}

// stringTable returns a TOML table's string values; nil for a missing or
// empty table. Non-string values are skipped.
func stringTable(v any) map[string]string {
	raw, _ := v.(map[string]any)
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// AgentWorkerModel returns `[agent].worker_model`: the model id workers run
// on, passed to the worker command as given and never validated.
func (c *Config) AgentWorkerModel() string { return c.GetString("agent.worker_model") }

// AgentTestSquad returns `[agent].test_squad`, the squad name test runs use
// instead of `qa-<repo>`; "" when unset.
func (c *Config) AgentTestSquad() string { return c.GetString("agent.test_squad") }

// AgentDevSquad returns `[agent].dev_squad`, the squad name everyday agent
// use takes instead of `dev-<repo>`; "" when unset.
func (c *Config) AgentDevSquad() string { return c.GetString("agent.dev_squad") }

// AndroidSpecs reads `[[android.sims]]` and returns the Android provisioning
// matrix. Emulator entries require non-empty device + image; physical entries
// tolerate placeholders.
func (c *Config) AndroidSpecs() []contract.AndroidSpec {
	return parseAndroidRows(c.Get("android.sims"))
}

func parseAndroidRows(v any) []contract.AndroidSpec {
	raw := tableRows(v)
	out := make([]contract.AndroidSpec, 0, len(raw))
	for _, s := range raw {
		target := strings.ToLower(asString(s["target"]))
		if target == "" {
			target = string(contract.TargetEmulator)
		}
		if target != string(contract.TargetEmulator) && target != string(contract.TargetPhysical) {
			continue
		}
		count := asInt(s["count"], 1)
		if count < 1 {
			continue
		}
		device := asString(s["device"])
		image := asString(s["image"])
		if target == string(contract.TargetEmulator) && (device == "" || image == "") {
			continue
		}
		out = append(out, contract.AndroidSpec{
			Device: device,
			Image:  image,
			Count:  count,
			Target: contract.AndroidTarget(target),
		})
	}
	return out
}

// DeviceSet is a named device matrix from `[device_sets.<name>]`: rows shaped
// like the default `[[ios.sims]]` / `[[android.sims]]`. It holds devices
// only; build settings stay in [project].
type DeviceSet struct {
	Name    string
	IOS     []contract.IosSpec
	Android []contract.AndroidSpec
}

// DeviceSet returns the named set as deploy uses it: the simsquad.local.toml
// set when that file defines one, else the simsquad.toml one.
func (c *Config) DeviceSet(name string) (DeviceSet, bool) {
	sets, _ := c.Get("device_sets").(map[string]any)
	return deviceSetFrom(sets, name)
}

// SharedDeviceSet returns the set as simsquad.toml defines it, even when a
// local set of the same name replaces it.
func (c *Config) SharedDeviceSet(name string) (DeviceSet, bool) {
	sets, _ := c.project["device_sets"].(map[string]any)
	return deviceSetFrom(sets, name)
}

// DeviceSetIsLocal reports whether simsquad.local.toml defines the set.
func (c *Config) DeviceSetIsLocal(name string) bool {
	sets, _ := c.local["device_sets"].(map[string]any)
	_, ok := sets[name].(map[string]any)
	return ok
}

// DeviceSetNames lists every set either file defines, sorted.
func (c *Config) DeviceSetNames() []string {
	sets, _ := c.Get("device_sets").(map[string]any)
	var names []string
	for name, v := range sets {
		if _, ok := v.(map[string]any); ok {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func deviceSetFrom(sets map[string]any, name string) (DeviceSet, bool) {
	table, ok := sets[name].(map[string]any)
	if !ok {
		return DeviceSet{}, false
	}
	var iosRows, androidRows any
	if t, ok := table["ios"].(map[string]any); ok {
		iosRows = t["sims"]
	}
	if t, ok := table["android"].(map[string]any); ok {
		androidRows = t["sims"]
	}
	return DeviceSet{Name: name, IOS: parseIOSRows(iosRows), Android: parseAndroidRows(androidRows)}, true
}

func readTOML(path string) (map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return map[string]any{}, nil
		}
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	var m map[string]any
	if _, err := toml.Decode(string(data), &m); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}
	return m, nil
}

// mergeLayers deep-merges the local file over the project file, except that a
// device set the local file defines replaces the project's set of the same
// name wholesale: a deep merge would mix a local set's iOS rows with the
// shared set's Android rows into a set neither file describes.
func mergeLayers(project, local map[string]any) map[string]any {
	merged := deepMerge(project, local)
	localSets, _ := local["device_sets"].(map[string]any)
	mergedSets, _ := merged["device_sets"].(map[string]any)
	for name, v := range localSets {
		if _, ok := v.(map[string]any); ok && mergedSets != nil {
			mergedSets[name] = v
		}
	}
	return merged
}

func deepMerge(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		if vMap, ok := v.(map[string]any); ok {
			if bMap, ok := out[k].(map[string]any); ok {
				out[k] = deepMerge(bMap, vMap)
				continue
			}
		}
		out[k] = v
	}
	return out
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}

func asInt(v any, fallback int) int {
	switch n := v.(type) {
	case int:
		return n
	case int32:
		return int(n)
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return fallback
}
