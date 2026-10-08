// Package teardown owns per-squad dismantling and orphan pruning for
// simsquad-* sims and AVDs left behind by an interrupted deploy.
//
// Per-platform behaviour:
//
//	iOS                 → simctl shutdown + simctl delete
//	Android emulator    → adb emu kill + avdmanager delete avd
//	Android physical    → adb uninstall <bundle>      (never delete device)
//	orphan              → any simsquad-* sim/AVD not in the registry
//
// Nothing in this package writes to stdout — the JSON contract is owned by
// internal/cli. Progress events go through internal/progress when a logger is
// supplied.
package teardown

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/peuf0u/simsquad/internal/android"
	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/progress"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

// orphanRx matches names produced by util.SimName.
var orphanRx = regexp.MustCompile(`^simsquad-[a-z0-9-]+-(ios|android)-\d+$`)

// MatchOrphan reports whether name follows the simsquad-managed device naming
// scheme. Exported for unit tests; production code calls orphanRx.MatchString
// directly.
func MatchOrphan(name string) bool { return orphanRx.MatchString(name) }

// Squad shuts down + deletes every device in the squad, drops the
// per-squad state file, and removes the squad from the registry. Physical
// Android devices are never deleted — only the app is uninstalled.
//
// A missing squad record is not an error; the registry entry (if any) is
// still removed so a stale index doesn't accumulate.
func Squad(name string, logger *progress.Logger) error {
	rec, err := state.LoadRecord(name)
	if err != nil {
		return fmt.Errorf("teardown: load record %s: %w", name, err)
	}
	if rec == nil {
		if logger != nil {
			logger.Info("teardown: no-state", progress.F("name", name))
		}
		_ = state.RemoveSquad(name)
		return nil
	}

	for _, dev := range rec.Devices {
		if err := teardownDevice(dev); err != nil && logger != nil {
			logger.Warn("teardown: device-error",
				progress.F("udid", dev.UDID), progress.F("error", err.Error()))
		}
	}

	if err := state.RemoveRecord(name); err != nil && logger != nil {
		logger.Warn("teardown: remove-record",
			progress.F("name", name), progress.F("error", err.Error()))
	}
	if err := state.RemoveSquad(name); err != nil {
		return fmt.Errorf("teardown: remove squad %s: %w", name, err)
	}
	if logger != nil {
		logger.Success("teardown: removed", progress.F("name", name))
	}
	return nil
}

func teardownDevice(dev contract.Device) error {
	if dev.UDID == "" {
		return nil
	}
	switch dev.Platform {
	case contract.PlatformIOS:
		return teardownIOS(dev)
	case contract.PlatformAndroid:
		if dev.Physical {
			return teardownAndroidPhysical(dev)
		}
		return teardownAndroidEmulator(dev)
	default:
		return fmt.Errorf("unknown platform %q", dev.Platform)
	}
}

func teardownIOS(dev contract.Device) error {
	_, _ = util.Run("xcrun", []string{"simctl", "shutdown", dev.UDID}, util.RunOpts{})
	r, _ := util.Run("xcrun", []string{"simctl", "delete", dev.UDID}, util.RunOpts{})
	if r.Code != 0 {
		return fmt.Errorf("simctl delete %s: %s", dev.UDID, strings.TrimSpace(r.Stderr))
	}
	return nil
}

func teardownAndroidEmulator(dev contract.Device) error {
	adb, err := android.ADB()
	if err == nil {
		_, _ = util.Run(adb, []string{"-s", dev.UDID, "emu", "kill"}, util.RunOpts{})
	}
	name := dev.AVDName
	if name == "" {
		name = dev.Name
	}
	if name == "" {
		return nil
	}
	avd, err := android.AVDManager()
	if err != nil {
		// Missing cmdline-tools is recoverable for teardown — log via caller.
		return nil
	}
	r, _ := util.Run(avd, []string{"delete", "avd", "-n", name}, util.RunOpts{})
	if r.Code != 0 {
		return fmt.Errorf("avdmanager delete %s: %s", name, strings.TrimSpace(r.Stderr))
	}
	return nil
}

func teardownAndroidPhysical(dev contract.Device) error {
	if dev.BundleID == "" {
		return nil
	}
	adb, err := android.ADB()
	if err != nil {
		return nil
	}
	r, _ := util.Run(adb, []string{"-s", dev.UDID, "uninstall", dev.BundleID}, util.RunOpts{})
	if r.Code != 0 {
		// Physical uninstall failure is informational, not fatal — the user's
		// device may already be unplugged or the bundle may already be gone.
		return fmt.Errorf("adb uninstall %s on %s: %s",
			dev.BundleID, dev.UDID, strings.TrimSpace(r.Stderr))
	}
	return nil
}

// PruneResult separates iOS and Android orphans so callers can render them
// independently or surface platform-specific exit codes.
type PruneResult struct {
	IOS     []string
	Android []string
}

// PruneOrphans deletes any host sim or AVD whose name matches
// `simsquad-[a-z0-9-]+-(ios|android)-\d+` but isn't tracked by the registry —
// debris from an interrupted deploy or a crashed simsquad binary.
//
// Errors talking to simctl or avdmanager are tolerated: missing tools mean
// "nothing to prune on that platform", which is the right behaviour on a
// machine that has only one SDK installed.
func PruneOrphans(logger *progress.Logger) (PruneResult, error) {
	tracked, err := trackedDeviceNames()
	if err != nil {
		return PruneResult{}, err
	}

	res := PruneResult{}
	res.IOS = pruneIOS(tracked, logger)
	res.Android = pruneAndroid(tracked, logger)
	return res, nil
}

func trackedDeviceNames() (map[string]struct{}, error) {
	entries, err := state.ListSquads()
	if err != nil {
		return nil, fmt.Errorf("teardown: list squads: %w", err)
	}
	tracked := map[string]struct{}{}
	for _, entry := range entries {
		rec, err := state.LoadRecord(entry.Name)
		if err != nil || rec == nil {
			continue
		}
		for _, d := range rec.Devices {
			if d.Name != "" {
				tracked[d.Name] = struct{}{}
			}
		}
	}
	return tracked, nil
}

func pruneIOS(tracked map[string]struct{}, logger *progress.Logger) []string {
	r, err := util.Run("xcrun", []string{"simctl", "list", "devices", "-j"}, util.RunOpts{})
	if err != nil || r.Code != 0 {
		return nil
	}
	var data struct {
		Devices map[string][]struct {
			UDID string `json:"udid"`
			Name string `json:"name"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &data); err != nil {
		return nil
	}
	var removed []string
	for _, devs := range data.Devices {
		for _, d := range devs {
			if !orphanRx.MatchString(d.Name) || !strings.Contains(d.Name, "-ios-") {
				continue
			}
			if _, ok := tracked[d.Name]; ok {
				continue
			}
			if d.UDID == "" {
				continue
			}
			_, _ = util.Run("xcrun", []string{"simctl", "shutdown", d.UDID}, util.RunOpts{})
			_, _ = util.Run("xcrun", []string{"simctl", "delete", d.UDID}, util.RunOpts{})
			removed = append(removed, d.Name)
			if logger != nil {
				logger.Info("prune: ios", progress.F("name", d.Name))
			}
		}
	}
	return removed
}

func pruneAndroid(tracked map[string]struct{}, logger *progress.Logger) []string {
	avd, err := android.AVDManager()
	if err != nil {
		return nil
	}
	r, err := util.Run(avd, []string{"list", "avd", "-c"}, util.RunOpts{})
	if err != nil || r.Code != 0 {
		return nil
	}
	var removed []string
	for _, line := range strings.Split(r.Stdout, "\n") {
		name := strings.TrimSpace(line)
		if !orphanRx.MatchString(name) || !strings.Contains(name, "-android-") {
			continue
		}
		if _, ok := tracked[name]; ok {
			continue
		}
		_, _ = util.Run(avd, []string{"delete", "avd", "-n", name}, util.RunOpts{})
		removed = append(removed, name)
		if logger != nil {
			logger.Info("prune: android", progress.F("name", name))
		}
	}
	return removed
}
