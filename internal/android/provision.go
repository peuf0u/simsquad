package android

import (
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

	// emulatorSettleAfterBoot is the grace window between "boot poll passes"
	// and "ready to install" — the system_server fully settling after
	// sys.boot_completed flips. install starts immediately after, but adb
	// install occasionally races against pm if we move zero-wait.
	emulatorSettleAfterBoot = 2 * time.Second
)

// dottedAPIRx matches API levels with a `.N` suffix — `android-36.1`,
// `android-34.0`. avdmanager errors with "test: : integer expression expected"
// when every installed image is suffixed this way; the pre-flight warn lets
// the user know before they wait for an error message that doesn't name the
// root cause.
var dottedAPIRx = regexp.MustCompile(`^android-\d+\.\d+`)

// ProvisionInput is the matrix request handed to Provision.
type ProvisionInput struct {
	Name            string
	Specs           []contract.AndroidSpec
	APKPath         string
	BundleID        string
	ExistingDevices []contract.Device
	PreserveData    bool
	Logger          *progress.Logger
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
			Name:     in.Name,
			Specs:    emuSpecs,
			APKPath:  in.APKPath,
			BundleID: in.BundleID,
			Logger:   in.Logger,
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
			Name:     in.Name,
			Count:    total,
			APKPath:  in.APKPath,
			BundleID: in.BundleID,
			Logger:   in.Logger,
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

func reprovisionExistingOne(existing contract.Device, in ProvisionInput) contract.Device {
	dev := existing
	dev.BundleID = in.BundleID
	dev.Status = contract.StatusError
	dev.ErrorMessage = ""
	if dev.UDID == "" {
		dev.ErrorMessage = "reuse: no udid in reuse"
		return dev
	}
	if !adbShellTrue(dev.UDID) {
		if dev.Physical {
			dev.ErrorMessage = "reuse-boot: physical device is not reachable"
			return dev
		}
		port, err := emulatorPort(dev)
		if err != nil {
			dev.ErrorMessage = "reuse-boot: " + err.Error()
			return dev
		}
		avd := dev.AVDName
		if avd == "" {
			avd = dev.Name
		}
		if !avdExists(avd) {
			dev.ErrorMessage = "reuse-boot: AVD " + avd + " does not exist"
			return dev
		}
		cmd, err := emulatorStart(avd, port, false)
		if err != nil {
			dev.ErrorMessage = "reuse-boot: " + err.Error()
			return dev
		}
		if err := waitForBoot(dev.UDID); err != nil {
			killEmulatorProcess(cmd)
			dev.ErrorMessage = "reuse-boot: " + err.Error()
			return dev
		}
		dev.EmulatorPID = cmd.Process.Pid
	}
	if in.PreserveData {
		if err := installOnly(dev.UDID, in.APKPath); err != nil {
			dev.ErrorMessage = "reuse-install: " + err.Error()
			return dev
		}
	} else if err := installAndClear(dev.UDID, in.APKPath, in.BundleID); err != nil {
		dev.ErrorMessage = "reuse-install: " + err.Error()
		return dev
	}
	dev.Status = contract.StatusReady
	return dev
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
			progress.F("hint", "avdmanager rejects images like android-36.1; install android-34 via sdkmanager"))
	}
}

// ---------------------------------------------------------------------------
// Emulator path
// ---------------------------------------------------------------------------

type emulatorContext struct {
	Name     string
	Specs    []contract.AndroidSpec
	APKPath  string
	BundleID string
	Logger   *progress.Logger
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

	var lastErr error
	bootAttempts := []struct {
		num  int
		wipe bool
	}{{1, false}, {2, true}}
	for _, a := range bootAttempts {
		cmd, err := emulatorStart(s.name, s.port, a.wipe)
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
		lastErr = nil
		break
	}
	if lastErr != nil {
		dev.ErrorMessage = "boot: " + lastErr.Error()
		return dev
	}

	time.Sleep(emulatorSettleAfterBoot)

	if err := installAndClear(serial, ctx.APKPath, ctx.BundleID); err != nil {
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
	r, err := util.Run(avd, []string{"list", "avd", "-c"}, util.RunOpts{})
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
func emulatorStart(name string, port int, wipeData bool) (*exec.Cmd, error) {
	emu, err := Emulator()
	if err != nil {
		return nil, err
	}
	args := []string{
		"-avd", name,
		"-port", strconv.Itoa(port),
		"-no-snapshot-save",
		"-no-window",
		"-no-boot-anim",
		"-gpu", "swiftshader_indirect",
	}
	if wipeData {
		args = append(args, "-wipe-data")
	}
	cmd := exec.Command(emu, args...)
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

func installOnly(serial, apk string) error {
	adb, err := ADB()
	if err != nil {
		return err
	}
	r, _ := util.Run(adb, []string{"-s", serial, "install", "-r", "-t", apk}, util.RunOpts{})
	if r.Code != 0 {
		return fmt.Errorf("adb install -r: %s", strings.TrimSpace(r.Stderr+r.Stdout))
	}
	return nil
}

func installAndClear(serial, apk, bundle string) error {
	adb, err := ADB()
	if err != nil {
		return err
	}
	install := func() (util.RunResult, error) {
		return util.Run(adb, []string{"-s", serial, "install", "-r", "-t", apk}, util.RunOpts{})
	}
	r, _ := install()
	if r.Code != 0 {
		// One retry after uninstall — covers signature-mismatch on reuse.
		_, _ = util.Run(adb, []string{"-s", serial, "uninstall", bundle}, util.RunOpts{})
		r, _ = install()
		if r.Code != 0 {
			return fmt.Errorf("adb install: %s", strings.TrimSpace(r.Stderr+r.Stdout))
		}
	}
	r, _ = util.Run(adb, []string{"-s", serial, "shell", "pm", "clear", bundle}, util.RunOpts{})
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
	Name     string
	Count    int
	APKPath  string
	BundleID string
	Logger   *progress.Logger
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
			if err := installAndClear(c.serial, ctx.APKPath, ctx.BundleID); err != nil {
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
