package discover

import (
	"encoding/json"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/peuf0u/simsquad/internal/util"
)

// IosDeviceType is one entry from `xcrun simctl list devicetypes -j`.
type IosDeviceType struct {
	Identifier string // com.apple.CoreSimulator.SimDeviceType.iPhone-17
	Name       string // "iPhone 17"
}

// IosRuntime is one entry from `xcrun simctl list runtimes -j`.
type IosRuntime struct {
	Identifier string // com.apple.CoreSimulator.SimRuntime.iOS-26-4
	Version    string // "26.4"
	Name       string // "iOS 26.4"
	Available  bool
}

var versionNumRx = regexp.MustCompile(`\b(\d+)\b`)

// deviceSortKey sorts iPhones first, then iPads, then everything else; within a
// family newest model number first.
func deviceSortKey(name string) (int, int, string) {
	var family int
	switch {
	case strings.HasPrefix(name, "iPhone"):
		family = 0
	case strings.HasPrefix(name, "iPad"):
		family = 1
	default:
		family = 2
	}
	version := 0
	if m := versionNumRx.FindStringSubmatch(name); m != nil {
		n, _ := strconv.Atoi(m[1])
		version = -n // negative → larger numbers first
	}
	return family, version, name
}

// ListIosDeviceTypes returns every installed iOS device type with iPhones
// first (newest first), then iPads, then everything else. On failure (simctl
// missing, malformed JSON) it returns nil — the wizard treats that as "no
// devices known" and falls back to user free-text input.
func ListIosDeviceTypes() []IosDeviceType {
	r, err := util.Run("xcrun", []string{"simctl", "list", "devicetypes", "-j"}, util.RunOpts{})
	if err != nil || r.Code != 0 {
		return nil
	}
	var data struct {
		DeviceTypes []struct {
			Identifier string `json:"identifier"`
			Name       string `json:"name"`
		} `json:"devicetypes"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &data); err != nil {
		return nil
	}
	out := make([]IosDeviceType, 0, len(data.DeviceTypes))
	for _, d := range data.DeviceTypes {
		if !strings.Contains(d.Identifier, "SimDeviceType") {
			continue
		}
		out = append(out, IosDeviceType{Identifier: d.Identifier, Name: d.Name})
	}
	sort.Slice(out, func(i, j int) bool {
		fi, vi, ni := deviceSortKey(out[i].Name)
		fj, vj, nj := deviceSortKey(out[j].Name)
		if fi != fj {
			return fi < fj
		}
		if vi != vj {
			return vi < vj
		}
		return ni < nj
	})
	return out
}

// ListIosRuntimes returns every installed iOS runtime, newest version first.
// Same nil-on-failure semantics as ListIosDeviceTypes.
func ListIosRuntimes() []IosRuntime {
	r, err := util.Run("xcrun", []string{"simctl", "list", "runtimes", "-j"}, util.RunOpts{})
	if err != nil || r.Code != 0 {
		return nil
	}
	return parseIosRuntimes([]byte(r.Stdout))
}

func parseIosRuntimes(raw []byte) []IosRuntime {
	var data struct {
		Runtimes []struct {
			Identifier  string `json:"identifier"`
			Version     string `json:"version"`
			Name        string `json:"name"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil
	}
	out := make([]IosRuntime, 0, len(data.Runtimes))
	for _, rt := range data.Runtimes {
		if !strings.Contains(rt.Identifier, "SimRuntime.iOS") {
			continue
		}
		out = append(out, IosRuntime{
			Identifier: rt.Identifier,
			Version:    rt.Version,
			Name:       rt.Name,
			Available:  rt.IsAvailable,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return versionKey(out[i].Version) > versionKey(out[j].Version)
	})
	return out
}

// versionKey turns "26.4" into 26_000_004 so we can compare dotted versions as
// a single integer for sort purposes. Two-segment versions are the common case;
// any extra segments are ignored.
func versionKey(v string) int64 {
	parts := strings.Split(v, ".")
	var key int64
	for i, p := range parts {
		if i >= 3 {
			break
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0
		}
		key = key*1_000_000 + int64(n)
	}
	// Pad so "26" and "26.4" sort as 26_000_000 < 26_000_004.
	for i := len(parts); i < 3; i++ {
		key *= 1_000_000
	}
	return key
}

// ResolveRuntimeIdentifier converts any runtime form (name, version, short id,
// full id) to the full com.apple.CoreSimulator.SimRuntime.iOS-N-N identifier.
//
// Landmine: simctl create rejects short forms like "iOS 26.4" but accepts the
// full identifier. This makes config files tolerant of either format — the
// lookup happens once at create time, not on every resolver call. Returns
// value unchanged if no match is found, so callers always have something
// usable to feed to simctl (and surface the real error from simctl itself).
func ResolveRuntimeIdentifier(value string) string {
	if strings.Contains(value, "SimRuntime") {
		return value
	}
	r, err := util.Run("xcrun", []string{"simctl", "list", "runtimes", "-j"}, util.RunOpts{})
	if err != nil || r.Code != 0 {
		return value
	}
	return resolveRuntimeIdentifierFrom([]byte(r.Stdout), value)
}

func resolveRuntimeIdentifierFrom(raw []byte, value string) string {
	var data struct {
		Runtimes []struct {
			Identifier  string `json:"identifier"`
			Version     string `json:"version"`
			Name        string `json:"name"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"runtimes"`
	}
	if err := json.Unmarshal(raw, &data); err != nil {
		return value
	}
	type cand struct{ id, name, version string }
	var candidates []cand
	for _, rt := range data.Runtimes {
		if !rt.IsAvailable || !strings.Contains(rt.Identifier, "iOS") {
			continue
		}
		candidates = append(candidates, cand{rt.Identifier, rt.Name, rt.Version})
	}
	for _, c := range candidates {
		if value == c.id || value == c.name || value == c.version {
			return c.id
		}
	}
	for _, c := range candidates {
		if strings.HasSuffix(c.id, value) {
			return c.id
		}
	}
	return value
}

// ResolveDeviceTypeIdentifier converts any device-type form to the full
// SimDeviceType identifier. simctl create accepts short forms for device
// types but we normalise for consistency with the runtime helper.
func ResolveDeviceTypeIdentifier(value string) string {
	if strings.Contains(value, "SimDeviceType") {
		return value
	}
	r, err := util.Run("xcrun", []string{"simctl", "list", "devicetypes", "-j"}, util.RunOpts{})
	if err != nil || r.Code != 0 {
		return value
	}
	var data struct {
		DeviceTypes []struct {
			Identifier string `json:"identifier"`
			Name       string `json:"name"`
		} `json:"devicetypes"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &data); err != nil {
		return value
	}
	for _, d := range data.DeviceTypes {
		if value == d.Identifier || value == d.Name {
			return d.Identifier
		}
	}
	return value
}
