package android

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/progress"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

const (
	// Boot is slow on first launch (DEX optimisation runs); 180 s matches the
	// Python reference and the empirical worst case on a cold M-series Mac.
	androidBootTimeout      = 180 * time.Second
	androidBootPollInterval = 3 * time.Second
)

// emulatorSettleAfterBoot is the grace window between "boot poll passes"
// and "ready to install" — the system_server fully settling after
// sys.boot_completed flips. install starts immediately after, but adb
// install occasionally races against pm if we move zero-wait. A variable so
// tests with a fake SDK skip the wait.
var emulatorSettleAfterBoot = 2 * time.Second

// dottedAPIRx matches API levels with a `.N` suffix — `android-36.1`,
// `android-34.0`. Older cmdline-tools reject these images ("Package path is
// not valid"); current ones, run against the configured SDK, accept them
// (dry run 002 deployed android-36.1). The "test: : integer expression
// expected" line avdmanager prints is a harmless script warning, not the
// cause. The pre-flight warn names the fix before avdmanager's error does.
var dottedAPIRx = regexp.MustCompile(`^android-\d+\.\d+`)

// ProvisionInput is the matrix request handed to Provision.
type ProvisionInput struct {
	Name    string
	Specs   []contract.AndroidSpec
	APKPath string
	// APKSetPath is set instead of APKPath for a bundle build: the APK set
	// bundletool installs, base plus feature modules.
	APKSetPath      string
	BundleID        string
	ExistingDevices []contract.Device
	PreserveData    bool
	// Window boots emulators with a window and the hardware keyboard on
	// (deploy --window), restarting a reused headless one to get it.
	Window bool
	Logger *progress.Logger
}

// ProvisionResult bundles per-slot devices with the port reservations made
// during provisioning. The deploy orchestrator merges Ports back into the
// SquadRecord before the final SaveRecord so the registry persists which
// emulator-port each AVD owns.
type ProvisionResult struct {
	Devices []contract.Device
	Ports   map[string]int
}

// Provision dispatches emulator and physical specs to their respective paths
// and concatenates the results. Per-slot failures surface as Device.Status ==
// StatusError; the function itself does not error on per-slot issues.
//
// Emulator slots reserve ADB ports under the index lock and persist the
// reservation early so concurrent simsquad invocations don't pick the same
// port. Physical slots claim adb-listed serials under the index lock so two
// invocations can't both grab the same plugged-in device.
func Provision(in ProvisionInput) ProvisionResult {
	res := ProvisionResult{Ports: map[string]int{}}
	if len(in.ExistingDevices) > 0 {
		res.Devices = reprovisionExisting(in)
		for _, dev := range res.Devices {
			if dev.Port > 0 && dev.Name != "" {
				res.Ports[dev.Name] = dev.Port
			}
		}
		return res
	}

	var emuSpecs, physSpecs []contract.AndroidSpec
	for _, s := range in.Specs {
		if s.Target == contract.TargetPhysical {
			physSpecs = append(physSpecs, s)
		} else {
			emuSpecs = append(emuSpecs, s)
		}
	}

	preflightAPIWarn(in.Logger)

	if len(emuSpecs) > 0 {
		emuDevices, emuPorts := provisionEmulators(emulatorContext{
			Name:       in.Name,
			Specs:      emuSpecs,
			APKPath:    in.APKPath,
			APKSetPath: in.APKSetPath,
			BundleID:   in.BundleID,
			Window:     in.Window,
			Logger:     in.Logger,
		})
		res.Devices = append(res.Devices, emuDevices...)
		for k, v := range emuPorts {
			res.Ports[k] = v
		}
	}
	if len(physSpecs) > 0 {
		total := 0
		for _, s := range physSpecs {
			total += s.Count
		}
		physDevices := provisionPhysical(physicalContext{
			Name:       in.Name,
			Count:      total,
			APKPath:    in.APKPath,
			APKSetPath: in.APKSetPath,
			BundleID:   in.BundleID,
			Logger:     in.Logger,
		})
		res.Devices = append(res.Devices, physDevices...)
	}
	return res
}

func reprovisionExisting(in ProvisionInput) []contract.Device {
	devices := make([]contract.Device, len(in.ExistingDevices))
	var g errgroup.Group
	for i, dev := range in.ExistingDevices {
		i, dev := i, dev
		g.Go(func() error {
			devices[i] = reprovisionExistingOne(dev, in)
			return nil
		})
	}
	_ = g.Wait()
	return devices
}

// reuseBoot is what reuse does with an existing device before installing.
type reuseBoot int

const (
	reuseKeep                reuseBoot = iota // reachable: install on it as it runs
	reuseBootHeadless                         // unreachable emulator: boot it headless
	reuseBootWindowed                         // unreachable emulator: boot it with a window
	reuseRestartWindowed                      // running headless, --window: restart it windowed
	reusePhysicalUnreachable                  // a physical device simsquad can't boot
)

func (b reuseBoot) String() string {
	switch b {
	case reuseKeep:
		return "keep"
	case reuseBootHeadless:
		return "boot-headless"
	case reuseBootWindowed:
		return "boot-windowed"
	case reuseRestartWindowed:
		return "restart-windowed"
	case reusePhysicalUnreachable:
		return "physical-unreachable"
	}
	return "reuseBoot(" + strconv.Itoa(int(b)) + ")"
}

// reuseBootDecision picks how reuse treats dev. The window switch is one
// way on purpose (issue #33): --window restarts a running headless emulator
// so the dev squad gains a window, while a deploy without it leaves a
// running windowed emulator alone. An emulator that has to be booted anyway
// gets the mode this deploy asked for.
func reuseBootDecision(dev contract.Device, reachable, window bool) reuseBoot {
	switch {
	case dev.Physical && !reachable:
		return reusePhysicalUnreachable
	case dev.Physical:
		return reuseKeep
	case reachable && window && !dev.Window:
		return reuseRestartWindowed
	case reachable:
		return reuseKeep
	case window:
		return reuseBootWindowed
	default:
		return reuseBootHeadless
	}
}

func reprovisionExistingOne(existing contract.Device, in ProvisionInput) contract.Device {
	dev := existing
	dev.BundleID = in.BundleID
	dev.Status = contract.StatusError
	dev.ErrorMessage = ""
	if dev.UDID == "" {
		dev.ErrorMessage = "reuse: no udid in reuse"
		return dev
	}
	reachable := adbShellTrue(dev.UDID)
	if reachable && !dev.Physical {
		// The serial is only a port. After a reboot another emulator (the
		// user's own, say) can hold it, and reuse must neither kill nor
		// install on it. Booting the squad's AVD instead would hit the busy
		// port, so the device fails with the reason.
		running, err := runningAVDName(dev.UDID)
		if err != nil {
			dev.ErrorMessage = fmt.Sprintf("reuse: could not read the AVD name of the emulator on %s (%s)",
				portLabel(dev), err.Error())
			return dev
		}
		if running != avdNameOf(dev) {
			dev.ErrorMessage = fmt.Sprintf("reuse: %s is held by another emulator (%s), not %s",
				portLabel(dev), running, avdNameOf(dev))
			return dev
		}
	}
	switch decision := reuseBootDecision(dev, reachable, in.Window); decision {
	case reusePhysicalUnreachable:
		dev.ErrorMessage = "reuse-boot: physical device is not reachable"
		return dev
	case reuseBootHeadless, reuseBootWindowed:
		// adb not answering doesn't mean the squad's emulator has exited:
		// it may still be booting after an interrupted deploy. A second
		// instance can't start on its locked AVD, so stop the live one.
		if dev.EmulatorPID > 0 && emulatorProcessAlive(dev.EmulatorPID) {
			if err := stopEmulator(dev); err != nil {
				dev.ErrorMessage = "reuse-boot: " + err.Error()
				return dev
			}
		}
		if err := bootReused(&dev, decision == reuseBootWindowed, in.Logger); err != nil {
			dev.ErrorMessage = "reuse-boot: " + err.Error()
			return dev
		}
	case reuseRestartWindowed:
		if err := stopEmulator(dev); err != nil {
			dev.ErrorMessage = "reuse-restart: " + err.Error()
			return dev
		}
		if err := bootReused(&dev, true, in.Logger); err != nil {
			dev.ErrorMessage = "reuse-restart: " + err.Error()
			return dev
		}
	}
	if in.PreserveData {
		if err := installOnly(dev.UDID, in.pkg()); err != nil {
			dev.ErrorMessage = "reuse-install: " + err.Error()
			return dev
		}
	} else if err := installAndClear(dev.UDID, in.pkg(), in.BundleID); err != nil {
		dev.ErrorMessage = "reuse-install: " + err.Error()
		return dev
	}
	dev.Status = contract.StatusReady
	return dev
}

// bootReused boots an existing emulator's AVD on its recorded port, without
// wiping it, in the requested mode, and records the new process and mode on
// dev. A windowed boot turns the hardware keyboard on first. On a boot
// timeout the captured process is killed (landmine 5) so no qemu is left
// running.
func bootReused(dev *contract.Device, window bool, logger *progress.Logger) error {
	port, err := emulatorPort(*dev)
	if err != nil {
		return err
	}
	avd := avdNameOf(*dev)
	if !avdExists(avd) {
		return fmt.Errorf("AVD %s does not exist", avd)
	}
	if window {
		turnOnHWKeyboard(avd, logger)
	}
	cmd, err := emulatorStart(avd, port, window, false)
	if err != nil {
		return err
	}
	if err := waitForBoot(dev.UDID); err != nil {
		killEmulatorProcess(cmd)
		return err
	}
	// An emulator that can't lock its AVD exits at once, and the boot poll
	// then passes against whatever instance still answers on the port.
	// Recording that boot would store a dead PID and a mode that isn't so.
	if !emulatorProcessAlive(cmd.Process.Pid) {
		killEmulatorProcess(cmd)
		return fmt.Errorf("the emulator started for %s exited during boot; another instance of it may still be running", avd)
	}
	time.Sleep(emulatorSettleAfterBoot)
	dev.EmulatorPID = cmd.Process.Pid
	dev.Window = window
	return nil
}

func avdNameOf(dev contract.Device) string {
	if dev.AVDName != "" {
		return dev.AVDName
	}
	return dev.Name
}

func portLabel(dev contract.Device) string {
	if port, err := emulatorPort(dev); err == nil {
		return "port " + strconv.Itoa(port)
	}
	return dev.UDID
}

// The emulator console behind `adb emu avd name` can miss a query under
// load, so reuse asks a few times before giving up on a working emulator.
// avdNameRetryDelay is a variable so fake-SDK tests skip the wait.
const avdNameAttempts = 3

var avdNameRetryDelay = time.Second

// runningAVDName asks the emulator on serial which AVD it runs (`adb emu
// avd name` prints the name, then OK), retrying an empty or failed answer.
// The error carries adb's own message from the last attempt.
func runningAVDName(serial string) (string, error) {
	adb, err := ADB()
	if err != nil {
		return "", err
	}
	var lastErr error
	for attempt := 1; attempt <= avdNameAttempts; attempt++ {
		if attempt > 1 {
			time.Sleep(avdNameRetryDelay)
		}
		r, err := util.Run(adb, []string{"-s", serial, "emu", "avd", "name"}, util.RunOpts{Timeout: 5 * time.Second})
		first, _, _ := strings.Cut(r.Stdout, "\n")
		name := strings.TrimSpace(first)
		switch {
		case err != nil:
			lastErr = err
		case r.Code != 0:
			msg := strings.TrimSpace(r.Stderr + r.Stdout)
			if msg == "" {
				msg = fmt.Sprintf("adb emu avd name exited %d", r.Code)
			}
			lastErr = errors.New(msg)
		case name == "":
			lastErr = errors.New("adb emu avd name printed no name")
		default:
			return name, nil
		}
	}
	return "", lastErr
}

// stopEmulator asks a running emulator to exit and waits until it has, so
// the windowed boot that follows can take its port.
func stopEmulator(dev contract.Device) error {
	adb, err := ADB()
	if err != nil {
		return err
	}
	_ = killEmulatorADB(dev.UDID)
	if !WaitGone(EmulatorGoneProbe(dev, adb), EmulatorExitTimeout, EmulatorExitPoll) {
		return fmt.Errorf("emulator %s did not exit within %s", dev.UDID, EmulatorExitTimeout)
	}
	return nil
}

// turnOnHWKeyboard sets hw.keyboard=yes on avd before a windowed boot. A
// failure only costs typing from the Mac keyboard, so it warns and the boot
// goes on.
func turnOnHWKeyboard(avd string, logger *progress.Logger) {
	path, err := avdConfigPath(avd)
	if err == nil {
		err = enableHWKeyboard(path)
	}
	if err != nil && logger != nil {
		logger.Warn("android-provision: hw-keyboard",
			progress.F("name", avd), progress.F("error", err.Error()))
	}
}

// preflightAPIWarn surfaces Landmine #4: when every installed system image has
// a `.N` API suffix, avdmanager errors with a particularly unhelpful "test: :
// integer expression expected" message. This warning gives the user a
// specific actionable cause before they hit the failure.
func preflightAPIWarn(logger *progress.Logger) {
	root, err := SDKRoot()
	if err != nil {
		return
	}
	base := root + "/system-images"
	entries, err := os.ReadDir(base)
	if err != nil || len(entries) == 0 {
		return
	}
	allDotted := true
	any := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		any++
		if !dottedAPIRx.MatchString(e.Name()) {
			allDotted = false
			break
		}
	}
	if any > 0 && allDotted && logger != nil {
		logger.Warn("android-provision: dotted-api-levels",
			progress.F("hint", "older cmdline-tools reject images like android-36.1; update them (brew upgrade --cask android-commandlinetools) or install a plain level like android-34"))
	}
}

// ---------------------------------------------------------------------------
// Emulator path
// ---------------------------------------------------------------------------

type emulatorContext struct {
	Name       string
	Specs      []contract.AndroidSpec
	APKPath    string
	APKSetPath string
	BundleID   string
	Window     bool
	Logger     *progress.Logger
}

type emulatorSlot struct {
	name          string
	port          int // -1 sentinels mean "no port available"
	systemImage   string
	deviceProfile string
}

func provisionEmulators(ctx emulatorContext) ([]contract.Device, map[string]int) {
	slots := make([]emulatorSlot, 0)
	ports := map[string]int{}

	// Phase 1: reserve ports under the index lock. Persist the reservations
	// to the squad's record so concurrent simsquad invocations see our claim.
	lockErr := state.WithIndexLockE(func() error {
		reserved := map[int]struct{}{}
		idx := 0
		for _, spec := range ctx.Specs {
			for i := 0; i < spec.Count; i++ {
				name := util.SimName(ctx.Name, "android", idx)
				idx++
				port, err := state.AllocateAndroidPortLocked(reserved)
				if err != nil {
					slots = append(slots, emulatorSlot{
						name: name, port: -1,
						systemImage: spec.Image, deviceProfile: spec.Device,
					})
					continue
				}
				reserved[port] = struct{}{}
				ports[name] = port
				slots = append(slots, emulatorSlot{
					name: name, port: port,
					systemImage: spec.Image, deviceProfile: spec.Device,
				})
			}
		}
		// Persist reservations to the record so other invocations see them.
		// Missing record (the orchestrator may not have created it yet on a
		// degenerate code path) is tolerated by skipping the save — the
		// in-memory result still carries the ports for the orchestrator to
		// persist at the final SaveRecord.
		rec, err := state.LoadRecord(ctx.Name)
		if err != nil || rec == nil {
			return nil
		}
		if rec.Ports == nil {
			rec.Ports = map[string]int{}
		}
		for k, v := range ports {
			rec.Ports[k] = v
		}
		return state.SaveRecord(rec)
	})
	if lockErr != nil && ctx.Logger != nil {
		ctx.Logger.Warn("android-provision: reserve-error",
			progress.F("error", lockErr.Error()))
	}

	// Phase 2: provision each slot in parallel.
	devices := make([]contract.Device, len(slots))
	var g errgroup.Group
	for i, s := range slots {
		i, s := i, s
		g.Go(func() error {
			devices[i] = provisionEmulatorSlot(s, ctx)
			return nil
		})
	}
	_ = g.Wait()
	return devices, ports
}

func provisionEmulatorSlot(s emulatorSlot, ctx emulatorContext) contract.Device {
	serial := ""
	if s.port > 0 {
		serial = "emulator-" + strconv.Itoa(s.port)
	}
	dev := contract.Device{
		Platform:      contract.PlatformAndroid,
		Name:          s.name,
		AVDName:       s.name,
		Port:          s.port,
		UDID:          serial,
		BundleID:      ctx.BundleID,
		DeviceProfile: s.deviceProfile,
		SystemImage:   s.systemImage,
		Physical:      false,
		Status:        contract.StatusError,
	}
	if s.port < 0 {
		dev.ErrorMessage = "no free Android port in 5554..5582"
		return dev
	}

	if err := createAVD(s.name, s.systemImage, s.deviceProfile); err != nil {
		dev.ErrorMessage = "avd-create: " + err.Error()
		return dev
	}
	if ctx.Window {
		turnOnHWKeyboard(s.name, ctx.Logger)
	}

	var lastErr error
	bootAttempts := []struct {
		num  int
		wipe bool
	}{{1, false}, {2, true}}
	for _, a := range bootAttempts {
		cmd, err := emulatorStart(s.name, s.port, ctx.Window, a.wipe)
		if err != nil {
			lastErr = err
			if ctx.Logger != nil {
				ctx.Logger.Warn("android-provision: boot-fail",
					progress.F("name", s.name), progress.F("attempt", a.num),
					progress.F("error", err.Error()))
			}
			continue
		}
		if err := waitForBoot(serial); err != nil {
			lastErr = err
			if ctx.Logger != nil {
				ctx.Logger.Warn("android-provision: boot-fail",
					progress.F("name", s.name), progress.F("attempt", a.num),
					progress.F("error", err.Error()))
			}
			// Boot timeout on a started emulator — clean up the captured Cmd
			// so we don't leave qemu running. SIGTERM first (the wrapper
			// handles a graceful qemu shutdown); SIGKILL if it's still alive
			// a moment later.
			killEmulatorProcess(cmd)
			_ = killEmulatorADB(serial)
			continue
		}
		// Booted. Record the PID so dismiss can SIGTERM if needed later.
		dev.EmulatorPID = cmd.Process.Pid
		dev.Window = ctx.Window
		lastErr = nil
		break
	}
	if lastErr != nil {
		dev.ErrorMessage = "boot: " + lastErr.Error()
		return dev
	}

	time.Sleep(emulatorSettleAfterBoot)

	if err := installAndClear(serial, appPackage{apk: ctx.APKPath, apkSet: ctx.APKSetPath}, ctx.BundleID); err != nil {
		dev.ErrorMessage = "install: " + err.Error()
		return dev
	}

	dev.Status = contract.StatusReady
	return dev
}

func createAVD(name, image, deviceProfile string) error {
	if avdExists(name) {
		return nil
	}
	if !HasSystemImage(image) {
		return fmt.Errorf("system image %s not installed (sdkmanager '%s')", image, image)
	}
	avd, err := AVDManager()
	if err != nil {
		return err
	}
	// avdmanager prompts on stdin for skin/path; feed a newline.
	cmd := exec.Command(avd, "create", "avd", "-n", name, "-k", image, "-d", deviceProfile, "--force")
	cmd.Stdin = strings.NewReader("\n")
	cmd.Env = AVDManagerEnv(avd)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("avdmanager create %s: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}

func avdExists(name string) bool {
	avd, err := AVDManager()
	if err != nil {
		return false
	}
	r, err := util.Run(avd, []string{"list", "avd", "-c"}, util.RunOpts{Env: AVDManagerEnv(avd)})
	if err != nil || r.Code != 0 {
		return false
	}
	for _, line := range strings.Split(r.Stdout, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

// emulatorStart launches the emulator detached enough to survive simsquad's
// exit (Setsid → its own session) but with the *exec.Cmd captured so we can
// SIGTERM/SIGKILL it on boot failure. The Python predecessor used start_new_session
// always; the Day 3 landmine fix is to retain the handle for the timeout path.
// On boot success we simply stop tracking the process — the OS keeps it
// running because nothing reaps detached children of a now-exited parent.
func emulatorStart(name string, port int, window, wipeData bool) (*exec.Cmd, error) {
	emu, err := Emulator()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command(emu, emulatorArgs(name, port, window, wipeData)...)
	// Detach from controlling terminal — emulator must survive simsquad exit.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("emulator start: %w", err)
	}
	return cmd, nil
}

// emulatorArgs builds the emulator command line. Headless is the default
// for test squads. A windowed boot (deploy --window, issue #33) drops
// -no-window and uses -gpu auto, since swiftshader is a software renderer
// meant for headless runs. It also adds -no-snapshot-load: every windowed boot
// follows a config.ini write (hw.keyboard=yes), which the emulator reads only
// on a cold boot.
func emulatorArgs(name string, port int, window, wipeData bool) []string {
	args := []string{
		"-avd", name,
		"-port", strconv.Itoa(port),
		"-no-snapshot-save",
	}
	if window {
		args = append(args, "-no-snapshot-load", "-no-boot-anim", "-gpu", "auto")
	} else {
		args = append(args, "-no-window", "-no-boot-anim", "-gpu", "swiftshader_indirect")
	}
	if wipeData {
		args = append(args, "-wipe-data")
	}
	return args
}

func killEmulatorProcess(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{}, 1)
	go func() {
		_, _ = cmd.Process.Wait()
		done <- struct{}{}
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
	}
}

func killEmulatorADB(serial string) error {
	adb, err := ADB()
	if err != nil {
		return err
	}
	_, _ = util.Run(adb, []string{"-s", serial, "emu", "kill"}, util.RunOpts{Timeout: 5 * time.Second})
	return nil
}

func waitForBoot(serial string) error {
	adb, err := ADB()
	if err != nil {
		return err
	}
	// adb wait-for-device returns when adbd is reachable — still pre-zygote.
	r, err := util.Run(adb, []string{"-s", serial, "wait-for-device"},
		util.RunOpts{Timeout: androidBootTimeout})
	if err != nil {
		return fmt.Errorf("wait-for-device: %w", err)
	}
	if r.Code != 0 {
		return fmt.Errorf("wait-for-device exited %d: %s", r.Code, strings.TrimSpace(r.Stderr))
	}
	deadline := time.Now().Add(androidBootTimeout)
	for time.Now().Before(deadline) {
		r, _ := util.Run(adb, []string{"-s", serial, "shell", "getprop", "sys.boot_completed"},
			util.RunOpts{Timeout: 10 * time.Second})
		if r.Code == 0 && strings.TrimSpace(r.Stdout) == "1" {
			return nil
		}
		time.Sleep(androidBootPollInterval)
	}
	return fmt.Errorf("boot did not complete within %s", androidBootTimeout)
}

func adbShellTrue(serial string) bool {
	adb, err := ADB()
	if err != nil {
		return false
	}
	r, err := util.Run(adb, []string{"-s", serial, "shell", "true"}, util.RunOpts{Timeout: 5 * time.Second})
	return err == nil && r.Code == 0
}

func emulatorPort(dev contract.Device) (int, error) {
	if dev.Port > 0 {
		return dev.Port, nil
	}
	parts := strings.Split(dev.UDID, "-")
	if len(parts) == 2 && parts[0] == "emulator" {
		port, err := strconv.Atoi(parts[1])
		if err == nil && port > 0 {
			return port, nil
		}
	}
	return 0, fmt.Errorf("cannot infer emulator port from %q", dev.UDID)
}

// appPackage is what a build installs: one APK, or for a bundle build the
// APK set built from the app bundle.
type appPackage struct {
	apk, apkSet string
}

func (in ProvisionInput) pkg() appPackage {
	return appPackage{apk: in.APKPath, apkSet: in.APKSetPath}
}

// install installs pkg on serial, replacing an installed copy: `adb install
// -r -t` for an APK, `bundletool install-apks` for an APK set, which picks
// the splits the device needs and, for a local-testing set, pushes the
// on-demand modules where Play Core finds them. --allow-test-only matches
// adb's -t.
func install(serial string, pkg appPackage) error {
	adb, err := ADB()
	if err != nil {
		return err
	}
	if pkg.apkSet == "" {
		r, _ := util.Run(adb, []string{"-s", serial, "install", "-r", "-t", pkg.apk}, util.RunOpts{})
		if r.Code != 0 {
			return fmt.Errorf("adb install: %s", strings.TrimSpace(r.Stderr+r.Stdout))
		}
		return nil
	}
	bundletool, err := Bundletool()
	if err != nil {
		return err
	}
	r, _ := util.Run(bundletool, []string{
		"install-apks", "--apks=" + pkg.apkSet, "--device-id=" + serial, "--adb=" + adb, "--allow-test-only",
	}, util.RunOpts{})
	if r.Code != 0 {
		return fmt.Errorf("bundletool install-apks: %s", strings.TrimSpace(r.Stderr+r.Stdout))
	}
	return nil
}

func installOnly(serial string, pkg appPackage) error {
	return install(serial, pkg)
}

func installAndClear(serial string, pkg appPackage, bundle string) error {
	adb, err := ADB()
	if err != nil {
		return err
	}
	if err := install(serial, pkg); err != nil {
		// One retry after uninstall — covers signature-mismatch on reuse.
		_, _ = util.Run(adb, []string{"-s", serial, "uninstall", bundle}, util.RunOpts{})
		if err := install(serial, pkg); err != nil {
			return err
		}
	}
	r, _ := util.Run(adb, []string{"-s", serial, "shell", "pm", "clear", bundle}, util.RunOpts{})
	if r.Code != 0 {
		return fmt.Errorf("pm clear: %s", strings.TrimSpace(r.Stderr))
	}
	return nil
}

// ResetApp force-stops the app and clears its data on a device or emulator —
// the Android analogue of the iOS data-container wipe, powering the `reset`
// verb. `pm clear` already restores the app to first-install state.
func ResetApp(serial, bundle string) error {
	adb, err := ADB()
	if err != nil {
		return err
	}
	_, _ = util.Run(adb, []string{"-s", serial, "shell", "am", "force-stop", bundle}, util.RunOpts{})
	r, _ := util.Run(adb, []string{"-s", serial, "shell", "pm", "clear", bundle}, util.RunOpts{})
	if r.Code != 0 {
		return fmt.Errorf("pm clear: %s", strings.TrimSpace(r.Stderr+r.Stdout))
	}
	return nil
}

// ---------------------------------------------------------------------------
// Physical path
// ---------------------------------------------------------------------------

type physicalContext struct {
	Name       string
	Count      int
	APKPath    string
	APKSetPath string
	BundleID   string
	Logger     *progress.Logger
}

func provisionPhysical(ctx physicalContext) []contract.Device {
	// Phase 1: claim under the index lock — list adb devices, exclude
	// already-claimed serials, pick the first N available, persist into the
	// squad record. Holding the lock across the full block (Landmine #7) means
	// no two concurrent simsquad invocations can both pick the same plugged-in
	// device.
	type claimed struct {
		name   string
		serial string // empty when no free device was available
	}
	claims := make([]claimed, ctx.Count)

	lockErr := state.WithIndexLockE(func() error {
		other, err := state.SnapshotSquadsLocked()
		if err != nil {
			return err
		}
		taken := map[string]struct{}{}
		for _, rec := range other {
			if rec.Name == ctx.Name {
				continue
			}
			for _, d := range rec.Devices {
				if d.Physical && d.UDID != "" {
					taken[d.UDID] = struct{}{}
				}
			}
		}
		available := []string{}
		for _, s := range listPhysicalSerials() {
			if _, ok := taken[s]; ok {
				continue
			}
			available = append(available, s)
		}
		for i := 0; i < ctx.Count; i++ {
			name := util.SimName(ctx.Name, "android", i)
			c := claimed{name: name}
			if i < len(available) {
				c.serial = available[i]
			}
			claims[i] = c
		}
		// Persist our claim into the record so concurrent invocations see it.
		rec, err := state.LoadRecord(ctx.Name)
		if err != nil || rec == nil {
			return nil
		}
		for _, c := range claims {
			if c.serial == "" {
				continue
			}
			rec.Devices = append(rec.Devices, contract.Device{
				Platform: contract.PlatformAndroid,
				Name:     c.name,
				UDID:     c.serial,
				BundleID: ctx.BundleID,
				Physical: true,
				Status:   contract.StatusError, // overwritten by install phase
			})
		}
		return state.SaveRecord(rec)
	})
	if lockErr != nil && ctx.Logger != nil {
		ctx.Logger.Warn("android-provision: physical-claim-error",
			progress.F("error", lockErr.Error()))
	}

	// Phase 2: install on each claimed serial in parallel (no lock).
	devices := make([]contract.Device, ctx.Count)
	var g errgroup.Group
	for i, c := range claims {
		i, c := i, c
		g.Go(func() error {
			dev := contract.Device{
				Platform: contract.PlatformAndroid,
				Name:     c.name,
				UDID:     c.serial,
				BundleID: ctx.BundleID,
				Physical: true,
				Status:   contract.StatusError,
			}
			if c.serial == "" {
				dev.ErrorMessage = "no free physical Android device"
				devices[i] = dev
				return nil
			}
			if err := installAndClear(c.serial, appPackage{apk: ctx.APKPath, apkSet: ctx.APKSetPath}, ctx.BundleID); err != nil {
				dev.ErrorMessage = "install: " + err.Error()
				devices[i] = dev
				return nil
			}
			dev.Status = contract.StatusReady
			devices[i] = dev
			return nil
		})
	}
	_ = g.Wait()
	return devices
}

// listPhysicalSerials enumerates plugged-in devices via `adb devices -l`,
// filtering to `device`-state serials that aren't the emulator-N pattern.
// Offline / unauthorized devices are excluded.
func listPhysicalSerials() []string {
	adb, err := ADB()
	if err != nil {
		return nil
	}
	r, err := util.Run(adb, []string{"devices", "-l"}, util.RunOpts{})
	if err != nil || r.Code != 0 {
		return nil
	}
	var serials []string
	lines := strings.Split(r.Stdout, "\n")
	if len(lines) <= 1 {
		return nil
	}
	for _, line := range lines[1:] { // skip "List of devices attached" header
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[1] != "device" {
			continue
		}
		if strings.HasPrefix(fields[0], "emulator-") {
			continue
		}
		serials = append(serials, fields[0])
	}
	return serials
}
