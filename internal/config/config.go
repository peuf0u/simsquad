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
		merged:    deepMerge(project, local),
		configDir: configDir,
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
func (c *Config) IOSSpecs() []contract.IosSpec {
	raw, _ := c.Get("ios.sims").([]map[string]any)
	if raw == nil {
		// BurntSushi unmarshals array-of-tables to []map[string]any but inside
		// a generic map we may instead get []any. Handle both shapes.
		generic, _ := c.Get("ios.sims").([]any)
		for _, e := range generic {
			if m, ok := e.(map[string]any); ok {
				raw = append(raw, m)
			}
		}
	}
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

// Env returns the [env] table from simsquad.toml as a string map. Missing
// section or non-string values yield nil/skipped entries. The result is the
// project-wide baseline env that deploy merges its --env flags over.
func (c *Config) Env() map[string]string {
	raw, _ := c.Get("env").(map[string]any)
	if raw == nil {
		return nil
	}
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

// AndroidSpecs reads `[[android.sims]]` and returns the Android provisioning
// matrix. Emulator entries require non-empty device + image; physical entries
// tolerate placeholders.
func (c *Config) AndroidSpecs() []contract.AndroidSpec {
	raw, _ := c.Get("android.sims").([]map[string]any)
	if raw == nil {
		generic, _ := c.Get("android.sims").([]any)
		for _, e := range generic {
			if m, ok := e.(map[string]any); ok {
				raw = append(raw, m)
			}
		}
	}
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
