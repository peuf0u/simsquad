package ios

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/discover"
	"github.com/peuf0u/simsquad/internal/progress"
	"github.com/peuf0u/simsquad/internal/util"
)

// Provisioning timeouts. simctl bootstatus -b waits up to 120 s for Springboard
// before returning; the installd-readiness poll is a much shorter follow-up
// because that daemon comes up within a couple of seconds of Springboard.
const (
	bootTimeout          = 120 * time.Second
	installdTimeout      = 15 * time.Second
	installdPollInterval = 500 * time.Millisecond
)

// destinationSimName is the throwaway sim created when no existing sim matches
// xcodebuild's --destination. The name is intentionally NOT shaped like the
// orphan regex (`simsquad-*-(ios|android)-\d+`) so the pruner leaves it alone —
// the destination dance is responsible for cleaning it up via
// DeleteDestinationSim.
const destinationSimName = "simsquad-build-target"

// ProvisionInput is the matrix request handed to Provision: which slots to
// create, the freshly-built app to install on each, and the bundle id used by
// the post-install data-container wipe.
type ProvisionInput struct {
	Name            string
	Specs           []contract.IosSpec
	AppPath         string
	BundleID        string
	ExistingDevices []contract.Device
	PreserveData    bool
	Logger          *progress.Logger
}

// Provision creates a sim per slot defined by Specs, boots each, waits for
// installd, installs the app, and wipes the data container. Per-slot failures
// surface as Device.Status == StatusError with a populated ErrorMessage; the
// function itself only errors when the inputs are unusable (e.g. missing idb
// for wipe). Slots run in parallel via errgroup.
func Provision(in ProvisionInput) []contract.Device {
	if len(in.ExistingDevices) > 0 {
		return reprovision(in)
	}

	type slot struct {
		name       string
		deviceType string
		runtime    string
	}

	var slots []slot
	idx := 0
	for _, spec := range in.Specs {
		for i := 0; i < spec.Count; i++ {
			slots = append(slots, slot{
				name:       util.SimName(in.Name, "ios", idx),
				deviceType: spec.Device,
				runtime:    spec.Runtime,
			})
			idx++
		}
	}
	if len(slots) == 0 {
		return nil
	}

	devices := make([]contract.Device, len(slots))
	var g errgroup.Group
	for i, s := range slots {
		i, s := i, s
		g.Go(func() error {
			devices[i] = provisionOne(provisionOneInput{
				Name:       s.name,
				DeviceType: s.deviceType,
				Runtime:    s.runtime,
				AppPath:    in.AppPath,
				BundleID:   in.BundleID,
				Logger:     in.Logger,
			})
			return nil // per-slot errors live in Device.ErrorMessage
		})
	}
	_ = g.Wait()
	return devices
}

func reprovision(in ProvisionInput) []contract.Device {
	devices := make([]contract.Device, len(in.ExistingDevices))
	var g errgroup.Group
	for i, dev := range in.ExistingDevices {
		i, dev := i, dev
		g.Go(func() error {
			devices[i] = reprovisionOne(dev, in)
			return nil
		})
	}
	_ = g.Wait()
	return devices
}

func reprovisionOne(existing contract.Device, in ProvisionInput) contract.Device {
	dev := existing
	dev.BundleID = in.BundleID
	dev.Status = contract.StatusError
	dev.ErrorMessage = ""
	if dev.UDID == "" {
		dev.ErrorMessage = "reuse: no udid in reuse"
		return dev
	}
	if err := bootExisting(dev.UDID); err != nil {
		dev.ErrorMessage = "reuse: " + err.Error()
		return dev
	}
	if !waitForInstalld(dev.UDID, installdTimeout, installdPollInterval, defaultInstalldProbe) && in.Logger != nil {
		in.Logger.Warn("ios-provision: installd-wait-timeout",
			progress.F("name", dev.Name), progress.F("udid", dev.UDID))
	}
	if err := installWithRetry(dev.UDID, in.AppPath, in.Logger, dev.Name); err != nil {
		dev.ErrorMessage = "reuse: install: " + err.Error()
		return dev
	}
	if !in.PreserveData {
		if err := wipe(dev.UDID, in.BundleID); err != nil {
			dev.ErrorMessage = "reuse: wipe: " + err.Error()
			return dev
		}
	}
	dev.Status = contract.StatusReady
	return dev
}

func bootExisting(udid string) error {
	if err := bootStatus(udid); err == nil {
		return nil
	}
	_, _ = util.Run("xcrun", []string{"simctl", "boot", udid}, util.RunOpts{})
	return bootStatus(udid)
}

type provisionOneInput struct {
	Name       string
	DeviceType string
	Runtime    string
	AppPath    string
	BundleID   string
	Logger     *progress.Logger
}

func provisionOne(in provisionOneInput) contract.Device {
	dev := contract.Device{
		Platform:   contract.PlatformIOS,
		Name:       in.Name,
		BundleID:   in.BundleID,
		DeviceType: in.DeviceType,
		Runtime:    in.Runtime,
		Status:     contract.StatusError,
	}

	udid, err := createSim(in.Name, in.DeviceType, in.Runtime)
	if err != nil {
		dev.ErrorMessage = "create: " + err.Error()
		return dev
	}
	dev.UDID = udid

	if err := bootWithRetry(udid, in.Logger, in.Name); err != nil {
		dev.ErrorMessage = "boot: " + err.Error()
		_ = deleteSim(udid)
		dev.UDID = ""
		return dev
	}

	if !waitForInstalld(udid, installdTimeout, installdPollInterval, defaultInstalldProbe) && in.Logger != nil {
		in.Logger.Warn("ios-provision: installd-wait-timeout",
			progress.F("name", in.Name), progress.F("udid", udid))
	}

	if err := installWithRetry(udid, in.AppPath, in.Logger, in.Name); err != nil {
		dev.ErrorMessage = "install: " + err.Error()
		_ = deleteSim(udid)
		dev.UDID = ""
		return dev
	}

	if err := wipe(udid, in.BundleID); err != nil {
		dev.ErrorMessage = "wipe: " + err.Error()
		return dev
	}

	dev.Status = contract.StatusReady
	return dev
}

func bootWithRetry(udid string, logger *progress.Logger, name string) error {
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		if err := bootStatus(udid); err != nil {
			lastErr = err
			if logger != nil {
				logger.Warn("ios-provision: boot-retry",
					progress.F("name", name), progress.F("error", err.Error()))
			}
			continue
		}
		return nil
	}
	return lastErr
}

func installWithRetry(udid, appPath string, logger *progress.Logger, name string) error {
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		if err := install(udid, appPath); err != nil {
			lastErr = err
			if logger != nil {
				logger.Warn("ios-provision: install-retry",
					progress.F("name", name), progress.F("error", err.Error()))
			}
			// Cycle the sim before retrying — installd may need a fresh start.
			_, _ = util.Run("xcrun", []string{"simctl", "shutdown", udid}, util.RunOpts{})
			_ = bootStatus(udid)
			continue
		}
		return nil
	}
	return lastErr
}

// createSim invokes `simctl create` after normalising the runtime to its full
// identifier — Landmine #3 from the plan: simctl rejects short runtime forms
// like "iOS 26.4" but accepts the full
// com.apple.CoreSimulator.SimRuntime.iOS-26-4 string.
func createSim(name, deviceType, runtime string) (string, error) {
	deviceType = discover.ResolveDeviceTypeIdentifier(deviceType)
	runtime = discover.ResolveRuntimeIdentifier(runtime)
	r, err := util.Run("xcrun", []string{"simctl", "create", name, deviceType, runtime}, util.RunOpts{})
	if err != nil {
		return "", fmt.Errorf("simctl create %q: %w", name, err)
	}
	if r.Code != 0 {
		return "", fmt.Errorf("simctl create %q exited %d: %s",
			name, r.Code, strings.TrimSpace(r.Stderr))
	}
	return strings.TrimSpace(r.Stdout), nil
}

func deleteSim(udid string) error {
	_, _ = util.Run("xcrun", []string{"simctl", "shutdown", udid}, util.RunOpts{})
	r, _ := util.Run("xcrun", []string{"simctl", "delete", udid}, util.RunOpts{})
	if r.Code != 0 {
		return fmt.Errorf("simctl delete %s: %s", udid, strings.TrimSpace(r.Stderr))
	}
	return nil
}

func bootStatus(udid string) error {
	r, err := util.Run("xcrun", []string{"simctl", "bootstatus", udid, "-b"},
		util.RunOpts{Timeout: bootTimeout})
	if err != nil {
		return fmt.Errorf("bootstatus: %w", err)
	}
	if r.Code != 0 {
		return fmt.Errorf("bootstatus exited %d: %s", r.Code, strings.TrimSpace(r.Stderr))
	}
	return nil
}

// installdProbe returns true iff installd is registered with launchd on the
// sim identified by udid. defaultInstalldProbe shells out via simctl; tests
// inject a faster substitute through waitForInstalld's probe parameter.
type installdProbe func(udid string) bool

func defaultInstalldProbe(udid string) bool {
	r, err := util.Run("xcrun",
		[]string{"simctl", "spawn", udid, "launchctl", "list"},
		util.RunOpts{Timeout: 5 * time.Second})
	if err != nil {
		return false
	}
	return r.Code == 0 && installdListed(r.Stdout)
}

// installdLabels are the launchd labels installd registers under. The Python
// baseline (ios_provision.py:81) grepped `launchctl print system` for
// "com.apple.installd", which never matches on current runtimes: the service
// is com.apple.mobile.installd, and `print system` no longer enumerates sim
// services at all. The probe then always ran to its timeout and logged a
// spurious installd-wait-timeout before an install that went on to succeed.
var installdLabels = map[string]bool{
	"com.apple.mobile.installd": true,
	"com.apple.installd":        true,
}

// installdListed reports whether `launchctl list` output (PID, Status, Label
// columns) contains an installd label. Registration is enough — launchd
// starts the daemon on demand, so a "-" PID is not a failure.
func installdListed(out string) bool {
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && installdLabels[f[2]] {
			return true
		}
	}
	return false
}

// waitForInstalld polls probe until it returns true or timeout elapses. Returns
// true when installd is observed, false on timeout.
//
// Landmine #1 (ios_provision.py:67-85): `simctl bootstatus -b` returns when
// SpringBoard is up, but com.apple.installd finishes initialising a beat
// later. Calling `simctl install` against the gap fails with
// `IXErrorDomain 2 "Failed to set metadata"` — silently, with no retry path.
// This poll closes that window; the production timeout is intentionally
// short (15 s) because installd registration that takes longer than that is
// a sign the sim is wedged, not slow.
func waitForInstalld(udid string, timeout, poll time.Duration, probe installdProbe) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if probe(udid) {
			return true
		}
		// Stop one poll interval before the deadline so we don't oversleep it.
		if time.Until(deadline) <= poll {
			break
		}
		time.Sleep(poll)
	}
	return false
}

// install uses `simctl install` exclusively — see internal/ios/idb.go for the
// arm64 idb-install Apple-Silicon bug that ruled out the idb path.
func install(udid, appPath string) error {
	r, err := util.Run("xcrun", []string{"simctl", "install", udid, appPath}, util.RunOpts{})
	if err != nil {
		return fmt.Errorf("simctl install: %w", err)
	}
	if r.Code != 0 {
		stderr := strings.TrimSpace(r.Stderr)
		if stderr == "" {
			stderr = strings.TrimSpace(r.Stdout)
		}
		return fmt.Errorf("simctl install exited %d: %s", r.Code, stderr)
	}
	return nil
}

// ResetApp returns an installed app to a clean state on a booted simulator:
// terminate it (so no live process holds the data container open) then wipe
// the container. Powers the `reset` verb — a consumer gets a virgin app
// instance between scenarios without re-provisioning the sim. Terminate is
// best-effort (it exits non-zero when the app isn't running, which is fine).
func ResetApp(udid, bundle string) error {
	_, _ = util.Run("xcrun", []string{"simctl", "terminate", udid, bundle}, util.RunOpts{})
	return wipe(udid, bundle)
}

// wipe deletes the sim's data-container subdirectories so a returning slot
// looks virgin to the next test run. `idb file rm` is used because computing
// the on-disk path under simctl get_app_container is far more involved.
func wipe(udid, bundle string) error {
	idb, err := LocateIDB()
	if err != nil {
		return err
	}
	for _, sub := range []string{"Documents", "Library", "tmp"} {
		args := []string{"file", "rm", "--bundle-id", bundle, "--udid", udid, "--", sub}
		r, _ := util.Run(idb, args, util.RunOpts{})
		if r.Code == 0 {
			continue
		}
		// A missing subdir is success — the container is already clean (this is
		// the common case on a second wipe, e.g. `reset` after provisioning).
		// idb's phrasing varies by version: "does not exist" (older) and
		// "no such file or directory" (newer, via the NSCocoa/POSIX error).
		combined := strings.ToLower(r.Stderr + r.Stdout)
		if strings.Contains(combined, "does not exist") ||
			strings.Contains(combined, "no such file or directory") {
			continue
		}
		return fmt.Errorf("idb file rm %s: %s", sub, strings.TrimSpace(r.Stderr+r.Stdout))
	}
	return nil
}

// EnsureDestinationUDID returns an iOS simulator udid suitable as
// xcodebuild's `-destination id=<udid>`. If an existing sim matches the given
// device + runtime it is reused (createdForBuild = false). Otherwise a
// throwaway sim is created (createdForBuild = true) and the caller is
// responsible for invoking DeleteDestinationSim once the build is done.
//
// The tag argument distinguishes concurrent deploys' throwaways from each
// other; pass the squad name so collisions across invocations are avoided.
// An empty tag falls back to the bare prefix.
func EnsureDestinationUDID(deviceType, runtime, tag string) (string, bool, error) {
	if udid := firstMatchingSimUDID(deviceType, runtime); udid != "" {
		return udid, false, nil
	}
	name := destinationSimName
	if tag != "" {
		name = destinationSimName + "-" + tag
	}
	udid, err := createSim(name, deviceType, runtime)
	if err != nil {
		return "", false, err
	}
	return udid, true, nil
}

// DeleteDestinationSim tears down a throwaway destination sim. Safe to call
// with an empty udid (no-op) so callers can defer it unconditionally.
func DeleteDestinationSim(udid string) error {
	if udid == "" {
		return nil
	}
	return deleteSim(udid)
}

var (
	runtimeTailRx = regexp.MustCompile(`(?:iOS[-\s])(\d+(?:[-.\d]+)?)`)

	// destinationSimsMu guards an in-process serialisation of the destination
	// sim selection so two goroutines kicking off the iOS + Android tracks
	// don't race on simctl list parsing.
	destinationSimsMu sync.Mutex
)

// firstMatchingSimUDID picks the first available sim whose name contains the
// device token (e.g. "iPhone 17") and whose runtime header contains either the
// dotted or dashed form of the runtime version.
func firstMatchingSimUDID(deviceType, runtime string) string {
	destinationSimsMu.Lock()
	defer destinationSimsMu.Unlock()

	r, err := util.Run("xcrun", []string{"simctl", "list", "devices", "-j"}, util.RunOpts{})
	if err != nil || r.Code != 0 {
		return ""
	}
	var data struct {
		Devices map[string][]struct {
			UDID        string `json:"udid"`
			Name        string `json:"name"`
			IsAvailable bool   `json:"isAvailable"`
		} `json:"devices"`
	}
	if err := json.Unmarshal([]byte(r.Stdout), &data); err != nil {
		return ""
	}
	deviceToken := deviceMatchToken(deviceType)
	runtimeTokens := runtimeMatchTokens(runtime)
	for runtimeKey, devs := range data.Devices {
		if !containsAny(runtimeKey, runtimeTokens) {
			continue
		}
		for _, d := range devs {
			if d.IsAvailable && strings.Contains(d.Name, deviceToken) {
				return d.UDID
			}
		}
	}
	return ""
}

// runtimeMatchTokens covers the three runtime spellings that show up in
// `simctl list devices` runtime keys:
//
//   - full identifier: com.apple.CoreSimulator.SimRuntime.iOS-26-4
//   - dashed short:    iOS-26-4
//   - dotted short:    iOS 26.4
func runtimeMatchTokens(runtime string) []string {
	tokens := []string{runtime}
	if m := runtimeTailRx.FindStringSubmatch(runtime); m != nil {
		ver := m[1]
		tokens = append(tokens,
			"iOS-"+strings.ReplaceAll(ver, ".", "-"),
			"iOS "+strings.ReplaceAll(ver, "-", "."),
		)
	}
	return dedupe(tokens)
}

// deviceMatchToken converts full SimDeviceType identifiers into the
// human-readable form simctl reports under `devices[runtime][].name`
// (e.g. `iPhone 17`). Short forms pass through unchanged.
func deviceMatchToken(deviceType string) string {
	const prefix = "SimDeviceType."
	if i := strings.Index(deviceType, prefix); i >= 0 {
		tail := deviceType[i+len(prefix):]
		if dot := strings.Index(tail, "."); dot >= 0 {
			tail = tail[:dot]
		}
		return strings.ReplaceAll(tail, "-", " ")
	}
	return deviceType
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if n == "" {
			continue
		}
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	seen := map[string]struct{}{}
	out := in[:0]
	for _, v := range in {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}
