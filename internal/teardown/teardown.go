// Package teardown owns per-squad dismantling and orphan pruning for
// simsquad-* sims and AVDs left behind by an interrupted deploy.
//
// Per-platform behaviour:
//
//	iOS                 → simctl shutdown + simctl delete
//	Android emulator    → adb emu kill, wait for it to exit (bounded),
//	                      then avdmanager delete avd; the kill only when
//	                      the serial answers with the squad's AVD name.
//	                      When the name can't be read or emu kill is
//	                      ignored, SIGTERM/SIGKILL on the recorded PID
//	                      while its args name the squad's AVD and port
//	Android physical    → adb uninstall <bundle>      (never delete device)
//	orphan              → any simsquad-* sim/AVD not in the registry, and
//	                      any simsquad-*.avd folder left without its .ini
//	removed repo        → a squad whose recorded repo path no longer exists
//	                      (typically a deleted git worktree)
//
// Nothing in this package writes to stdout — the JSON contract is owned by
// internal/cli. Progress events go through internal/progress when a logger is
// supplied.
package teardown

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
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
// per-squad state file and the squad's own APK set, and removes the squad
// from the registry. Physical
// Android devices are never deleted — only the app is uninstalled.
//
// A missing squad record is not an error; the registry entry (if any) is
// still removed so a stale index doesn't accumulate.
func Squad(name string, logger *progress.Logger) error {
	// Load the record and remove it with its registry entry in one locked
	// step, then tear the devices down outside the lock. A deploy's final
	// merge landing during the (slow) device teardown then finds the record
	// gone and cleans up after itself, instead of saving devices into a
	// record this function deletes a moment later.
	var rec *contract.SquadRecord
	err := state.WithIndexLockE(func() error {
		var err error
		rec, err = state.LoadRecord(name)
		if err != nil {
			return fmt.Errorf("teardown: load record %s: %w", name, err)
		}
		if rec == nil {
			// Still drop the registry entry so a stale index doesn't
			// accumulate; failing to is not worth an error here.
			_ = state.RemoveSquadLocked(name)
			return nil
		}
		if err := state.RemoveRecord(name); err != nil && logger != nil {
			logger.Warn("teardown: remove-record",
				progress.F("name", name), progress.F("error", err.Error()))
		}
		if err := state.RemoveSquadLocked(name); err != nil {
			return fmt.Errorf("teardown: remove squad %s: %w", name, err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// The squad's own APK set (android.StageAPKSet) goes with its record.
	if err := os.Remove(util.SquadAPKSetPath(name)); err != nil && !errors.Is(err, fs.ErrNotExist) && logger != nil {
		logger.Warn("teardown: remove-apk-set", progress.F("name", name), progress.F("error", err.Error()))
	}
	if rec == nil {
		if logger != nil {
			logger.Info("teardown: no-state", progress.F("name", name))
		}
		return nil
	}

	Devices(rec.Devices, logger)

	if logger != nil {
		logger.Success("teardown: removed", progress.F("name", name))
	}
	return nil
}

// Devices shuts down and deletes the given devices (uninstalls the app on
// physical ones) without touching any record or the registry. Deploy uses it
// to reset one platform of a squad while the other platform's devices stay.
// Per-device failures are logged, not returned: the device is gone from the
// caller's point of view either way.
func Devices(devs []contract.Device, logger *progress.Logger) {
	for _, dev := range devs {
		if err := tearDownDevice(dev); err != nil && logger != nil {
			logger.Warn("teardown: device-error",
				progress.F("udid", dev.UDID), progress.F("error", err.Error()))
		}
	}
}

// tearDownDevice is teardownDevice behind a seam, so tests can act while a
// teardown is in progress without touching simctl or adb.
var tearDownDevice = teardownDevice

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

// emulatorExitTimeout bounds the wait for an emulator to exit after `adb emu
// kill`. A variable so tests run in test time.
var emulatorExitTimeout = android.EmulatorExitTimeout

func teardownAndroidEmulator(dev contract.Device) error {
	adb, err := android.ADB()
	if err != nil {
		adb = ""
	}
	name := dev.AVDName
	if name == "" {
		name = dev.Name
	}
	owner, left := squadEmulatorOnSerial(adb, dev.UDID, name)
	kill := func() {
		if owner == serialOurs {
			_, _ = util.Run(adb, []string{"-s", dev.UDID, "emu", "kill"}, util.RunOpts{})
		}
	}
	avd, avdErr := android.AVDManager()
	if name == "" || avdErr != nil {
		// Nothing to delete, so nothing to wait for. Missing cmdline-tools
		// is recoverable for teardown — log via caller.
		kill()
		return left
	}
	del := func() error {
		r, _ := util.Run(avd, []string{"delete", "avd", "-n", name}, util.RunOpts{Env: android.AVDManagerEnv(avd)})
		if r.Code != 0 {
			return fmt.Errorf("avdmanager delete %s: %s", name, strings.TrimSpace(r.Stderr))
		}
		return nil
	}
	gone := android.EmulatorGoneProbe(dev, adb)
	switch owner {
	case serialOurs:
		// An emulator still booting may not honour emu kill; its recorded
		// PID, while verified as the squad's emulator, is signalled before
		// the AVD goes, so it isn't left running with its AVD deleted.
		var signal func()
		if dev.EmulatorPID > 0 {
			signal = func() { android.SignalSquadEmulator(dev, gone) }
		}
		return stopThenDelete(kill, gone, signal, del, emulatorExitTimeout, android.EmulatorExitPoll)
	case serialUnknown:
		// The name can't be read (an emulator still booting, say). Its
		// recorded PID, when verified as the squad's emulator by its
		// arguments, settles it; otherwise it is left running (fail safe).
		if dev.EmulatorPID > 0 && android.SquadEmulatorAlive(dev) && android.SignalSquadEmulator(dev, gone) {
			return del()
		}
		return errors.Join(del(), left)
	default:
		// Nothing on the serial, or another AVD: no kill, so no exit to
		// wait for.
		return errors.Join(del(), left)
	}
}

// serialOwner is what squadEmulatorOnSerial found on a squad emulator's
// serial.
type serialOwner int

const (
	serialFree    serialOwner = iota // adb doesn't list the serial
	serialOurs                       // it runs the squad's AVD
	serialForeign                    // it runs another AVD
	serialUnknown                    // its AVD name can't be read
)

// squadEmulatorOnSerial reports who runs the emulator on serial: the
// squad's AVD (name) is the only case in which teardown may kill it through
// adb. The serial is only a port: after a reboot another emulator, the
// user's own say, can hold it (#33). An emulator that answers with another
// name, or whose name can't be read, is not killed through adb, and left
// says so for the caller's warning. Nothing listed on the serial skips the
// name query and its retries.
func squadEmulatorOnSerial(adb, serial, name string) (owner serialOwner, left error) {
	if adb == "" || name == "" || !android.ADBListsSerial(adb, serial) {
		return serialFree, nil
	}
	running, err := android.RunningAVDName(serial)
	if err != nil {
		return serialUnknown, fmt.Errorf("left the emulator on %s running: could not read its AVD name (%s)", serial, err.Error())
	}
	if running != name {
		return serialForeign, fmt.Errorf("left the emulator on %s running: it runs %s, not %s", serial, running, name)
	}
	return serialOurs, nil
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
// debris from an interrupted deploy or a crashed simsquad binary. On Android
// it also removes such untracked `<name>.avd` folders in the AVD home that
// avdmanager can't see because their .ini is gone.
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

// RemovedRepoSquad is a squad dismissed by DismissRemovedRepos, with the
// recorded repo path that no longer exists.
type RemovedRepoSquad struct {
	Name string `json:"name"`
	Repo string `json:"repo"`
}

// DismissRemovedRepos tears down every squad whose recorded iOS or Android
// repo no longer exists, and deletes the iOS derived data built from it. One
// squad per git worktree is a supported pattern; removing the worktree would
// otherwise leave its devices and gigabytes of derived data behind for good,
// since nothing can redeploy that squad. Records without a repo (written
// before 0.5.0) are left alone.
func DismissRemovedRepos(logger *progress.Logger) ([]RemovedRepoSquad, error) {
	entries, err := state.ListSquads()
	if err != nil {
		return nil, fmt.Errorf("teardown: list squads: %w", err)
	}
	var out []RemovedRepoSquad
	for _, entry := range entries {
		rec, err := state.LoadRecord(entry.Name)
		if err != nil || rec == nil {
			continue
		}
		repo := MissingRepo(*rec)
		if repo == "" {
			continue
		}
		if logger != nil {
			logger.Info("sweep: repo-removed", progress.F("name", rec.Name), progress.F("repo", repo))
		}
		if err := Squad(rec.Name, logger); err != nil {
			return out, err
		}
		if b := rec.Builds.IOS; b != nil && b.Repo != "" && !exists(b.Repo) {
			if err := os.RemoveAll(util.IOSDerivedDataDir(b.Repo)); err != nil && logger != nil {
				logger.Warn("sweep: remove-derived", progress.F("repo", b.Repo), progress.F("error", err.Error()))
			}
		}
		out = append(out, RemovedRepoSquad{Name: rec.Name, Repo: repo})
	}
	return out, nil
}

// MissingRepo returns the first repo recorded on rec's builds that no longer
// exists, or "" when every recorded repo is there (or none is recorded).
// Only a definite not-exist counts: a permission error keeps the squad.
func MissingRepo(rec contract.SquadRecord) string {
	var repos []string
	if b := rec.Builds.IOS; b != nil && b.Repo != "" {
		repos = append(repos, b.Repo)
	}
	if b := rec.Builds.Android; b != nil && b.Repo != "" {
		repos = append(repos, b.Repo)
	}
	for _, r := range repos {
		if !exists(r) {
			return r
		}
	}
	return ""
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return !errors.Is(err, fs.ErrNotExist)
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
			if d.AVDName != "" {
				tracked[d.AVDName] = struct{}{}
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
	removed := pruneAndroidAVDs(tracked, logger)
	home, err := android.AVDHome()
	if err != nil {
		return removed
	}
	for _, name := range pruneStrayAVDDirs(home, tracked, logger) {
		if !slices.Contains(removed, name) {
			removed = append(removed, name)
		}
	}
	return removed
}

// pruneAndroidAVDs deletes untracked simsquad AVDs that avdmanager lists.
func pruneAndroidAVDs(tracked map[string]struct{}, logger *progress.Logger) []string {
	avd, err := android.AVDManager()
	if err != nil {
		return nil
	}
	r, err := util.Run(avd, []string{"list", "avd", "-c"}, util.RunOpts{Env: android.AVDManagerEnv(avd)})
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
		_, _ = util.Run(avd, []string{"delete", "avd", "-n", name}, util.RunOpts{Env: android.AVDManagerEnv(avd)})
		removed = append(removed, name)
		if logger != nil {
			logger.Info("prune: android", progress.F("name", name))
		}
	}
	return removed
}

// pruneStrayAVDDirs removes `<name>.avd` folders in home that have no
// `<name>.ini` next to them, where name is an untracked simsquad Android
// name. avdmanager lists AVDs by their .ini, so such a folder (e.g. one an
// exiting emulator recreated after teardown deleted the AVD, issue #39) is
// invisible to it. A folder with its .ini is left to the avdmanager pass.
func pruneStrayAVDDirs(home string, tracked map[string]struct{}, logger *progress.Logger) []string {
	entries, err := os.ReadDir(home)
	if err != nil {
		return nil
	}
	var removed []string
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".avd")
		if !ok || !e.IsDir() || !orphanRx.MatchString(name) || !strings.Contains(name, "-android-") {
			continue
		}
		if _, ok := tracked[name]; ok {
			continue
		}
		if _, err := os.Lstat(filepath.Join(home, name+".ini")); err == nil {
			continue
		}
		if err := os.RemoveAll(filepath.Join(home, e.Name())); err != nil {
			if logger != nil {
				logger.Warn("prune: android-folder", progress.F("name", name), progress.F("error", err.Error()))
			}
			continue
		}
		removed = append(removed, name)
		if logger != nil {
			logger.Info("prune: android-folder", progress.F("name", name))
		}
	}
	return removed
}
