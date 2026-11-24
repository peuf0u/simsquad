package android

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/peuf0u/simsquad/internal/contract"
)

const flowAVD = "simsquad-x-android-0"

// fakeSDK is an Android SDK whose adb and avdmanager are shell scripts and
// whose emulator is this test binary (see TestMain), so a booted emulator is
// a live process named "emulator" that the PID probe recognises. State lives
// in marker files under dir:
//
//   - up: an emulator runs on emulator-5556 (adb lists it, its console
//     answers). A fake emulator creates it at start and exits once it is
//     gone; `adb emu kill` removes it.
//   - offline: adbd doesn't answer `shell true` although the emulator runs.
//   - avdname: the AVD the emulator on the port runs (default flowAVD).
//   - noname: `adb emu avd name` fails although the emulator runs; every
//     query appends a line to namecalls.
//   - noname1: as noname, for the first query only (the marker goes with it).
//   - noboot: a started emulator never comes up and wait-for-device fails,
//     once that emulator has started (it writes bootstarted after logging),
//     so the kill that follows never comes before its log line.
//   - exitearly: a started emulator can't lock its AVD (another instance
//     holds it): it writes exited, then exits fakeExitDelay later, as a
//     real one exits a moment after start. wait-for-device answers once
//     exited is there, so the boot poll passes while it still runs.
//   - nokill: `adb emu kill` is logged but the emulator keeps running, as
//     one still booting may not honour it.
//   - ignoreterm: an emulator started while it exists ignores SIGTERM.
//
// Every command that changes something is appended to log.
type fakeSDK struct {
	dir, log, up, config string
}

const fakeEmulatorEnv = "SIMSQUAD_FAKE_EMULATOR_DIR"

// fakeExitDelay is how long an exitearly fake emulator outlives the boot
// poll; fakeSettle, the settle window the fake SDK sets, is well above it,
// so the started emulator has always exited inside the window and the
// outcome doesn't hang on the scheduler.
const (
	fakeExitDelay = 200 * time.Millisecond
	fakeSettle    = 1500 * time.Millisecond
)

// TestMain lets the test binary stand in for the emulator: linked to
// <sdk>/emulator/emulator and started with the fake's state dir in the
// environment, it runs runFakeEmulator instead of the tests.
func TestMain(m *testing.M) {
	if dir := os.Getenv(fakeEmulatorEnv); dir != "" {
		runFakeEmulator(dir)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runFakeEmulator(dir string) {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(dir, name))
		return err == nil
	}
	if os.Getenv("SIMSQUAD_FAKE_EMULATOR_NOLOG") == "" {
		f, err := os.OpenFile(filepath.Join(dir, "log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
		if err == nil {
			_, _ = f.WriteString("emulator " + strings.Join(os.Args[1:], " ") + "\n")
			_ = f.Close()
		}
		_ = os.WriteFile(filepath.Join(dir, "lastpid"), []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	if exists("ignoreterm") {
		signal.Ignore(syscall.SIGTERM)
	}
	if exists("exitearly") {
		_ = os.WriteFile(filepath.Join(dir, "exited"), nil, 0o644)
		time.Sleep(fakeExitDelay)
		return
	}
	noboot := exists("noboot")
	if noboot {
		_ = os.WriteFile(filepath.Join(dir, "bootstarted"), nil, 0o644)
	} else {
		_ = os.WriteFile(filepath.Join(dir, "up"), nil, 0o644)
	}
	for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
		if !noboot && !exists("up") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func newFakeSDK(t *testing.T, running bool, config string) fakeSDK {
	t.Helper()
	sdk := t.TempDir()
	avdHome := t.TempDir()
	t.Setenv("ANDROID_HOME", sdk)
	t.Setenv("ANDROID_AVD_HOME", avdHome)
	t.Setenv("PATH", t.TempDir()+":/bin:/usr/bin")
	oldSettle, oldRetry := emulatorSettleAfterBoot, avdNameRetryDelay
	t.Cleanup(func() { emulatorSettleAfterBoot, avdNameRetryDelay = oldSettle, oldRetry })
	emulatorSettleAfterBoot, avdNameRetryDelay = fakeSettle, 0

	dir := t.TempDir()
	t.Setenv(fakeEmulatorEnv, dir)
	f := fakeSDK{
		dir:    dir,
		log:    filepath.Join(dir, "log"),
		up:     filepath.Join(dir, "up"),
		config: filepath.Join(avdHome, flowAVD+".avd", "config.ini"),
	}
	// Removing up stops every fake emulator a test left running.
	t.Cleanup(func() { _ = os.Remove(f.up) })
	if running {
		f.touch(t, "up")
	}
	writeFile(t, f.config, config)

	writeFile(t, filepath.Join(sdk, "platform-tools", "adb"), `#!/bin/sh
d=`+dir+`
if [ "$1" = devices ]; then
  echo x >> $d/devicecalls
  echo 'List of devices attached'
  [ -e $d/up ] && printf 'emulator-5556\tdevice\n'
  exit 0
fi
shift 2
case "$*" in
  "shell true") [ -e $d/up ] && [ ! -e $d/offline ] ;;
  "emu kill") echo "adb emu kill" >> $d/log; [ -e $d/nokill ] || rm -f $d/up $d/offline ;;
  "emu avd name")
    echo x >> $d/namecalls
    [ -e $d/up ] || exit 1
    if [ -e $d/noname1 ]; then rm -f $d/noname1; echo 'error: could not connect to TCP port 5557' >&2; exit 1; fi
    if [ -e $d/noname ]; then echo 'error: could not connect to TCP port 5557' >&2; exit 1; fi
    name=`+flowAVD+`; [ -e $d/avdname ] && name=$(cat $d/avdname)
    printf '%s\r\nOK\r\n' "$name" ;;
  wait-for-device)
    if [ -e $d/noboot ]; then
      n=0; while [ ! -e $d/bootstarted ] && [ $n -lt 200 ]; do sleep 0.05; n=$((n+1)); done
      rm -f $d/bootstarted; exit 1
    fi
    if [ -e $d/exitearly ]; then
      # Answer once the started emulator has failed to lock its AVD,
      # while it is still exiting, as the instance holding the AVD would.
      n=0; while [ ! -e $d/exited ] && [ $n -lt 100 ]; do sleep 0.05; n=$((n+1)); done
    fi
    n=0; while [ ! -e $d/up ] && [ $n -lt 50 ]; do sleep 0.1; n=$((n+1)); done; [ -e $d/up ] ;;
  "shell getprop sys.boot_completed") [ -e $d/up ] && echo 1 ;;
  *) echo "adb $*" >> $d/log ;;
esac
`)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(sdk, "emulator"), 0o755); err != nil {
		t.Fatal(err)
	}
	linkOrCopy(t, self, filepath.Join(sdk, "emulator", "emulator"))
	writeFile(t, filepath.Join(sdk, "cmdline-tools", "latest", "bin", "avdmanager"),
		"#!/bin/sh\necho "+flowAVD+"\n")
	return f
}

func (f fakeSDK) touch(t *testing.T, name string) {
	t.Helper()
	writeFile(t, filepath.Join(f.dir, name), "")
}

// startEmulator starts a fake emulator outside simsquad, as one a previous
// deploy left running, and returns its PID. It isn't logged.
func (f fakeSDK) startEmulator(t *testing.T) int {
	t.Helper()
	emu, err := Emulator()
	if err != nil {
		t.Fatal(err)
	}
	// Started with the squad's arguments, as simsquad starts it, so the
	// PID check that reads them recognises it.
	cmd := exec.Command(emu, emulatorArgs(flowAVD, 5556, false, false)...)
	cmd.Env = append(os.Environ(), "SIMSQUAD_FAKE_EMULATOR_NOLOG=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// Reap it once it exits, so the PID probe sees it gone.
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if _, err := os.Stat(f.up); err == nil {
			return cmd.Process.Pid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("fake emulator did not come up")
	return 0
}

func (f fakeSDK) lastPID(t *testing.T) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(f.dir, "lastpid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return pid
}

func (f fakeSDK) calls(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

func (f fakeSDK) configText(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.config)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func reusedEmulator(window bool) contract.Device {
	return contract.Device{
		Platform: contract.PlatformAndroid,
		Name:     flowAVD,
		AVDName:  flowAVD,
		Port:     5556,
		UDID:     "emulator-5556",
		Status:   contract.StatusReady,
		Window:   window,
	}
}

// --window on a running headless emulator restarts it windowed without
// wiping it, so hand-set data survives, then installs as reuse always does.
func TestReuseRestartsARunningHeadlessEmulatorWindowed(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=no\nhw.ramSize=2048\n")

	dev := reprovisionExistingOne(reusedEmulator(false), ProvisionInput{
		APKPath: "/app.apk", BundleID: "com.example", PreserveData: true, Window: true,
	})
	if dev.Status != contract.StatusReady {
		t.Fatalf("status = %s (%s), want ready", dev.Status, dev.ErrorMessage)
	}
	if !dev.Window || dev.EmulatorPID == 0 {
		t.Fatalf("window = %v, emulator_pid = %d; want true and the new boot's pid", dev.Window, dev.EmulatorPID)
	}
	want := "adb emu kill\n" +
		"emulator -avd " + flowAVD + " -port 5556 -no-snapshot-save -no-snapshot-load -no-boot-anim -gpu auto\n" +
		"adb install -r -t /app.apk\n"
	if got := f.calls(t); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
	if got := f.configText(t); got != "hw.keyboard=yes\nhw.ramSize=2048\n" {
		t.Fatalf("config.ini = %q, want hw.keyboard=yes", got)
	}
}

// Without --window a running windowed emulator is left alone: no kill, no
// boot, and its record keeps the window.
func TestReuseLeavesARunningWindowedEmulatorAlone(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=yes\n")

	dev := reprovisionExistingOne(reusedEmulator(true), ProvisionInput{
		APKPath: "/app.apk", BundleID: "com.example",
	})
	if dev.Status != contract.StatusReady || !dev.Window {
		t.Fatalf("status = %s (%s), window = %v; want ready and true", dev.Status, dev.ErrorMessage, dev.Window)
	}
	want := "adb install -r -t /app.apk\nadb shell pm clear com.example\n"
	if got := f.calls(t); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
}

// An unreachable emulator boots in the mode this deploy asks for, and its
// record follows: here headless, though it was windowed before.
func TestReuseBootsAnUnreachableEmulatorHeadlessWithoutWindow(t *testing.T) {
	f := newFakeSDK(t, false, "hw.keyboard=yes\n")

	dev := reprovisionExistingOne(reusedEmulator(true), ProvisionInput{
		APKPath: "/app.apk", BundleID: "com.example", PreserveData: true,
	})
	if dev.Status != contract.StatusReady || dev.Window {
		t.Fatalf("status = %s (%s), window = %v; want ready and false", dev.Status, dev.ErrorMessage, dev.Window)
	}
	if got := f.calls(t); !strings.Contains(got, "-no-window") {
		t.Fatalf("calls =\n%s\nwant a headless boot", got)
	}
	if got := f.configText(t); got != "hw.keyboard=yes\n" {
		t.Fatalf("config.ini = %q, want it untouched", got)
	}
}

// A fresh windowed boot turns the hardware keyboard on before the first boot
// and records the window; a headless one leaves the profile's setting.
func TestFreshEmulatorBootFollowsWindow(t *testing.T) {
	for _, window := range []bool{false, true} {
		t.Run(map[bool]string{false: "headless", true: "windowed"}[window], func(t *testing.T) {
			f := newFakeSDK(t, false, "hw.keyboard=no\n")

			dev := provisionEmulatorSlot(
				emulatorSlot{name: flowAVD, port: 5556},
				emulatorContext{APKPath: "/app.apk", BundleID: "com.example", Window: window},
			)
			if dev.Status != contract.StatusReady || dev.Window != window {
				t.Fatalf("status = %s (%s), window = %v; want ready and %v",
					dev.Status, dev.ErrorMessage, dev.Window, window)
			}
			wantArgs := strings.Join(emulatorArgs(flowAVD, 5556, window, false), " ")
			if got := f.calls(t); !strings.Contains(got, "emulator "+wantArgs+"\n") {
				t.Fatalf("calls =\n%s\nwant emulator %s", got, wantArgs)
			}
			wantConfig := map[bool]string{false: "hw.keyboard=no\n", true: "hw.keyboard=yes\n"}[window]
			if got := f.configText(t); got != wantConfig {
				t.Fatalf("config.ini = %q, want %q", got, wantConfig)
			}
		})
	}
}

// Review #33 item 1: the recorded serial answering is not proof it is the
// squad's emulator. After a reboot another emulator (here the user's own)
// can hold the port; reuse must neither kill it nor install on it.
func TestReuseRefusesAnotherEmulatorOnTheSquadsPort(t *testing.T) {
	for _, window := range []bool{false, true} {
		t.Run(fmt.Sprint("window=", window), func(t *testing.T) {
			f := newFakeSDK(t, true, "hw.keyboard=no\n")
			writeFile(t, filepath.Join(f.dir, "avdname"), "Pixel_8_API_35")

			old := reusedEmulator(false)
			old.EmulatorPID = 4242
			dev := reprovisionExistingOne(old, ProvisionInput{
				APKPath: "/app.apk", BundleID: "com.example", PreserveData: true, Window: window,
			})
			if dev.Status != contract.StatusError ||
				!strings.Contains(dev.ErrorMessage, "Pixel_8_API_35") ||
				!strings.Contains(dev.ErrorMessage, "5556") {
				t.Fatalf("status = %s (%q), want an error naming port 5556 and Pixel_8_API_35",
					dev.Status, dev.ErrorMessage)
			}
			if got := f.calls(t); got != "" {
				t.Fatalf("calls =\n%s\nwant none: no kill, boot or install", got)
			}
			if _, err := os.Stat(f.up); err != nil {
				t.Fatal("the other emulator was stopped")
			}
			if dev.Window || dev.EmulatorPID != 4242 {
				t.Fatalf("window = %v, emulator_pid = %d; want the record's false and 4242", dev.Window, dev.EmulatorPID)
			}
		})
	}
}

// Review #33 item 2: the squad's own emulator can be alive but not answering
// adb (still booting after an interrupted deploy). Booting a second instance
// would fail on the locked AVD, so reuse stops the live one first.
func TestReuseStopsTheSquadsAliveButUnreachableEmulatorBeforeBooting(t *testing.T) {
	f := newFakeSDK(t, false, "hw.keyboard=no\n")
	oldPID := f.startEmulator(t)
	f.touch(t, "offline")

	old := reusedEmulator(false)
	old.EmulatorPID = oldPID
	dev := reprovisionExistingOne(old, ProvisionInput{
		APKPath: "/app.apk", BundleID: "com.example", PreserveData: true,
	})
	if dev.Status != contract.StatusReady {
		t.Fatalf("status = %s (%s), want ready", dev.Status, dev.ErrorMessage)
	}
	want := "adb emu kill\n" +
		"emulator " + strings.Join(emulatorArgs(flowAVD, 5556, false, false), " ") + "\n" +
		"adb install -r -t /app.apk\n"
	if got := f.calls(t); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
	if dev.EmulatorPID != f.lastPID(t) || dev.EmulatorPID == oldPID {
		t.Fatalf("emulator_pid = %d, want the new boot's %d (old %d)", dev.EmulatorPID, f.lastPID(t), oldPID)
	}
}

// Review #33 item 2: if the emulator reuse started exits while another
// instance answers on the port, the boot is not this deploy's and must not
// be recorded as such (a dead PID, a window it doesn't have).
func TestReuseFailsWhenTheStartedEmulatorExitsDuringBoot(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=no\n")
	f.touch(t, "offline")
	f.touch(t, "exitearly")

	dev := reprovisionExistingOne(reusedEmulator(false), ProvisionInput{
		APKPath: "/app.apk", BundleID: "com.example", PreserveData: true, Window: true,
	})
	if dev.Status != contract.StatusError || !strings.HasPrefix(dev.ErrorMessage, "reuse-boot: ") {
		t.Fatalf("status = %s (%q), want a reuse-boot error", dev.Status, dev.ErrorMessage)
	}
	if dev.Window || dev.EmulatorPID != 0 {
		t.Fatalf("window = %v, emulator_pid = %d; want the record's false and 0", dev.Window, dev.EmulatorPID)
	}
	if strings.Contains(f.calls(t), "install") {
		t.Fatalf("calls =\n%s\nwant no install", f.calls(t))
	}
}

// Review #33 item 3, landmine 5: when the windowed restart's boot fails, the
// emulator it started is killed, and the device keeps its recorded mode and
// PID instead of claiming a window.
func TestReuseRestartBootFailureKillsTheStartedEmulator(t *testing.T) {
	f := newFakeSDK(t, false, "hw.keyboard=no\n")
	oldPID := f.startEmulator(t)
	f.touch(t, "noboot")

	old := reusedEmulator(false)
	old.EmulatorPID = oldPID
	dev := reprovisionExistingOne(old, ProvisionInput{
		APKPath: "/app.apk", BundleID: "com.example", PreserveData: true, Window: true,
	})
	if dev.Status != contract.StatusError || !strings.HasPrefix(dev.ErrorMessage, "reuse-restart: ") {
		t.Fatalf("status = %s (%q), want a reuse-restart error", dev.Status, dev.ErrorMessage)
	}
	if dev.Window || dev.EmulatorPID != oldPID {
		t.Fatalf("window = %v, emulator_pid = %d; want the record's false and %d", dev.Window, dev.EmulatorPID, oldPID)
	}
	want := "adb emu kill\n" +
		"emulator " + strings.Join(emulatorArgs(flowAVD, 5556, true, false), " ") + "\n"
	if got := f.calls(t); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
	started := f.lastPID(t)
	if err := syscall.Kill(started, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("started emulator %d still exists (kill -0: %v), want it killed and reaped", started, err)
	}
}

// Review #33 round 2: an AVD name that can't be read is not proof of another
// emulator. Reuse retries the query, then fails safe (no kill, boot or
// install) with its own reason rather than "held by another emulator".
func TestReuseFailsSafeWhenTheAVDNameCannotBeRead(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=no\n")
	f.touch(t, "noname")

	dev := reprovisionExistingOne(reusedEmulator(false), ProvisionInput{
		APKPath: "/app.apk", BundleID: "com.example", PreserveData: true, Window: true,
	})
	want := "reuse: could not read the AVD name of the emulator on port 5556 (error: could not connect to TCP port 5557)"
	if dev.Status != contract.StatusError || dev.ErrorMessage != want {
		t.Fatalf("status = %s (%q), want error %q", dev.Status, dev.ErrorMessage, want)
	}
	if got := f.calls(t); got != "" {
		t.Fatalf("calls =\n%s\nwant none: no kill, boot or install", got)
	}
	if _, err := os.Stat(f.up); err != nil {
		t.Fatal("the emulator was stopped")
	}
	b, err := os.ReadFile(filepath.Join(f.dir, "namecalls"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n != avdNameAttempts {
		t.Fatalf("emu avd name ran %d times, want %d", n, avdNameAttempts)
	}
}

// Review #33 round 3: a console that misses one AVD name query under load
// answers the retry, and reuse goes on as for any reachable emulator of the
// squad's own.
func TestReuseProceedsWhenTheAVDNameAnswersOnRetry(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=no\n")
	f.touch(t, "noname1")

	dev := reprovisionExistingOne(reusedEmulator(false), ProvisionInput{
		APKPath: "/app.apk", BundleID: "com.example", PreserveData: true,
	})
	if dev.Status != contract.StatusReady {
		t.Fatalf("status = %s (%s), want ready", dev.Status, dev.ErrorMessage)
	}
	if got, want := f.calls(t), "adb install -r -t /app.apk\n"; got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
	b, err := os.ReadFile(filepath.Join(f.dir, "namecalls"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n != 2 {
		t.Fatalf("emu avd name ran %d times, want 2", n)
	}
}

// shortStopWaits makes stopEmulator's waits test-sized.
func shortStopWaits(t *testing.T) {
	t.Helper()
	oldExit, oldSignal := stopExitTimeout, stopSignalTimeout
	t.Cleanup(func() { stopExitTimeout, stopSignalTimeout = oldExit, oldSignal })
	stopExitTimeout, stopSignalTimeout = 500*time.Millisecond, time.Second
}

func processGone(pid int) bool {
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

// processExited reports whether pid has exited, reaped or not: a child the
// test hasn't waited for stays a zombie, which kill -0 still finds.
func processExited(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	stat := strings.TrimSpace(string(out))
	return err != nil || stat == "" || strings.HasPrefix(stat, "Z")
}

// Review round 4: an emulator still booting after an interrupted deploy may
// not honour `adb emu kill`, and qemu would keep the AVD locked. stopEmulator
// then signals the recorded PID, still a verified emulator process: SIGTERM
// first, SIGKILL when that is ignored too.
func TestStopEmulatorSignalsTheRecordedPIDWhenEmuKillIsIgnored(t *testing.T) {
	for _, ignoreTerm := range []bool{false, true} {
		t.Run(fmt.Sprint("ignoreterm=", ignoreTerm), func(t *testing.T) {
			f := newFakeSDK(t, false, "hw.keyboard=no\n")
			shortStopWaits(t)
			if ignoreTerm {
				f.touch(t, "ignoreterm")
			}
			pid := f.startEmulator(t)
			f.touch(t, "nokill")

			dev := reusedEmulator(false)
			dev.EmulatorPID = pid
			if err := stopEmulator(dev); err != nil {
				t.Fatal(err)
			}
			for deadline := time.Now().Add(time.Second); !processGone(pid) && time.Now().Before(deadline); {
				time.Sleep(10 * time.Millisecond)
			}
			if !processGone(pid) {
				t.Fatalf("emulator %d still exists, want it stopped by a signal", pid)
			}
			if got := f.calls(t); got != "adb emu kill\n" {
				t.Fatalf("calls =\n%s\nwant only the emu kill", got)
			}
		})
	}
}

// A recorded PID that is no longer an emulator (reused by another program)
// is never signalled; the stop fails as before.
func TestStopEmulatorLeavesAPIDThatIsNotAnEmulator(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=no\n")
	shortStopWaits(t)
	f.touch(t, "nokill")
	other := exec.Command("sleep", "30")
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = other.Process.Kill(); _ = other.Wait() })

	dev := reusedEmulator(false)
	dev.EmulatorPID = other.Process.Pid
	// The PID probe says the emulator is gone (the PID isn't one), so the
	// stop succeeds without signalling anything.
	if err := stopEmulator(dev); err != nil {
		t.Fatal(err)
	}
	if processExited(other.Process.Pid) {
		t.Fatal("a process that is not an emulator was signalled")
	}
}

// Review round 5: the PID fallback runs when the recorded PID is most
// likely stale (after a reboot). A process whose arguments don't name the
// squad's AVD and port (another emulator, a colima or UTM qemu VM) is never
// signalled, however emulator-like its name.
func TestStopEmulatorNeverSignalsAProcessRunningAnotherAVDOrPort(t *testing.T) {
	for _, args := range [][]string{
		{"-avd", "Pixel_8_API_35", "-port", "5556"},
		{"-avd", flowAVD, "-port", "5558"},
		{"-M", "virt", "-accel", "hvf"},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := newFakeSDK(t, true, "hw.keyboard=no\n")
			shortStopWaits(t)
			f.touch(t, "nokill")
			other := startHelperProcess(t, "qemu-system-aarch64", args...)

			dev := reusedEmulator(false)
			dev.EmulatorPID = other.Process.Pid
			// The PID isn't the squad's emulator, so the probe counts it
			// gone and the stop returns without a signal.
			if err := stopEmulator(dev); err != nil {
				t.Fatal(err)
			}
			if processExited(other.Process.Pid) {
				t.Fatal("a process that is not the squad's emulator was signalled")
			}
		})
	}
}

// Review round 5, item 1: after a reboot the user's own emulator can take
// a port no squad records. Allocation skips every port adb lists, with one
// `adb devices` call for the whole batch.
func TestReserveEmulatorSlotsSkipsPortsADBLists(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=no\n")
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())

	slots, _ := reserveEmulatorSlots(emulatorContext{
		Name:  "x",
		Specs: []contract.AndroidSpec{{Count: 3}},
	})
	var got []int
	for _, s := range slots {
		got = append(got, s.port)
	}
	if fmt.Sprint(got) != "[5554 5558 5560]" {
		t.Fatalf("ports = %v, want [5554 5558 5560]: 5556 is listed by adb", got)
	}
	b, err := os.ReadFile(filepath.Join(f.dir, "devicecalls"))
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(b), "\n"); n != 1 {
		t.Fatalf("adb devices ran %d times, want once", n)
	}
}

// Without adb the allocator goes by the recorded ports alone, as before.
func TestReserveEmulatorSlotsWithoutADB(t *testing.T) {
	t.Setenv("ANDROID_HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())

	slots, _ := reserveEmulatorSlots(emulatorContext{
		Name:  "x",
		Specs: []contract.AndroidSpec{{Count: 2}},
	})
	if len(slots) != 2 || slots[0].port != 5554 || slots[1].port != 5556 {
		t.Fatalf("slots = %+v, want ports 5554 and 5556", slots)
	}
}

// Review round 5, item 1: a fresh boot on a port another emulator holds
// can't bind it, and the boot poll then passes against the other emulator.
// The slot fails naming the port and the other AVD, the started emulator
// is killed (landmine 5), and nothing is installed on or killed through
// the other emulator.
func TestFreshBootFailsWhenAnotherEmulatorAnswersOnThePort(t *testing.T) {
	cases := []struct {
		name      string
		running   bool
		markers   []string
		avdName   string
		wantError string
	}{
		{"started emulator exits", true, []string{"exitearly"}, "Pixel_8_API_35",
			"boot: port 5556 is held by another emulator (Pixel_8_API_35), not " + flowAVD},
		{"started emulator stays", false, nil, "Pixel_8_API_35",
			"boot: port 5556 is held by another emulator (Pixel_8_API_35), not " + flowAVD},
		{"name unreadable, started emulator exited", true, []string{"exitearly", "noname"}, "",
			"boot: the emulator started for " + flowAVD + " exited during boot, and the AVD name of the emulator on port 5556 could not be read (error: could not connect to TCP port 5557)"},
		{"another instance of the squad's AVD", true, []string{"exitearly"}, "",
			"boot: the emulator started for " + flowAVD + " exited during boot; another instance of it holds port 5556"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFakeSDK(t, c.running, "hw.keyboard=no\n")
			for _, m := range c.markers {
				f.touch(t, m)
			}
			if c.avdName != "" {
				writeFile(t, filepath.Join(f.dir, "avdname"), c.avdName)
			}

			dev := provisionEmulatorSlot(
				emulatorSlot{name: flowAVD, port: 5556},
				emulatorContext{APKPath: "/app.apk", BundleID: "com.example"},
			)
			if dev.Status != contract.StatusError || dev.ErrorMessage != c.wantError {
				t.Fatalf("status = %s (%q), want error %q", dev.Status, dev.ErrorMessage, c.wantError)
			}
			if dev.EmulatorPID != 0 {
				t.Fatalf("emulator_pid = %d, want 0", dev.EmulatorPID)
			}
			calls := f.calls(t)
			if strings.Contains(calls, "install") || strings.Contains(calls, "pm clear") || strings.Contains(calls, "emu kill") {
				t.Fatalf("calls =\n%s\nwant no install, clear or emu kill", calls)
			}
			if strings.Count(calls, "emulator -avd") != 1 {
				t.Fatalf("calls =\n%s\nwant one boot, no wipe retry", calls)
			}
			started := f.lastPID(t)
			for deadline := time.Now().Add(5 * time.Second); !processExited(started) && time.Now().Before(deadline); {
				time.Sleep(10 * time.Millisecond)
			}
			if !processExited(started) {
				t.Fatalf("started emulator %d still runs, want it killed", started)
			}
		})
	}
}

// Review round 6, item 2: under a wide matrix the console can miss the
// name query. A started emulator still alive with the squad's AVD and port
// in its arguments is provably the one answering, so the boot stands.
func TestFreshBootAcceptsItsLiveEmulatorWhenTheNameQueryFails(t *testing.T) {
	f := newFakeSDK(t, false, "hw.keyboard=no\n")
	f.touch(t, "noname")

	dev := provisionEmulatorSlot(
		emulatorSlot{name: flowAVD, port: 5556},
		emulatorContext{APKPath: "/app.apk", BundleID: "com.example"},
	)
	if dev.Status != contract.StatusReady {
		t.Fatalf("status = %s (%q), want ready", dev.Status, dev.ErrorMessage)
	}
	if dev.EmulatorPID != f.lastPID(t) {
		t.Fatalf("emulator_pid = %d, want the started emulator's %d", dev.EmulatorPID, f.lastPID(t))
	}
	if got := f.calls(t); !strings.HasSuffix(got, "adb install -r -t /app.apk\nadb shell pm clear com.example\n") {
		t.Fatalf("calls =\n%s\nwant the install and clear", got)
	}
}

// withBundletool puts a bundletool on $PATH that logs like the fake adb. Its
// install fails once while the installfail marker exists.
func (f fakeSDK) withBundletool(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	writeFile(t, filepath.Join(bin, "bundletool"), `#!/bin/sh
d=`+f.dir+`
echo "bundletool $*" >> $d/log
if [ -e $d/installfail ]; then rm $d/installfail; echo 'INSTALL_FAILED_UPDATE_INCOMPATIBLE' >&2; exit 1; fi
`)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
}

func installAPKSetCall(t *testing.T) string {
	t.Helper()
	adb, err := ADB()
	if err != nil {
		t.Fatal(err)
	}
	return "bundletool install-apks --apks=/app.apks --device-id=emulator-5556 --adb=" + adb + " --allow-test-only\n"
}

// A bundle build clears the app's data before installing its APK set, never
// after: pm clear also deletes external app data, where a local-testing
// install-apks leaves the on-demand modules' splits.
func TestReuseInstallsTheAPKSetAndClearsData(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=no\n")
	f.withBundletool(t)

	dev := reprovisionExistingOne(reusedEmulator(false), ProvisionInput{
		APKSetPath: "/app.apks", BundleID: "com.example",
	})
	if dev.Status != contract.StatusReady {
		t.Fatalf("status = %s (%s), want ready", dev.Status, dev.ErrorMessage)
	}
	want := "adb shell pm clear com.example\n" + installAPKSetCall(t)
	if got := f.calls(t); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
}

func TestReuseInstallsTheAPKSetKeepingDataWithPreserveData(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=no\n")
	f.withBundletool(t)

	dev := reprovisionExistingOne(reusedEmulator(false), ProvisionInput{
		APKSetPath: "/app.apks", BundleID: "com.example", PreserveData: true,
	})
	if dev.Status != contract.StatusReady {
		t.Fatalf("status = %s (%s), want ready", dev.Status, dev.ErrorMessage)
	}
	if got, want := f.calls(t), installAPKSetCall(t); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
}

// A failed APK set install (here a signature mismatch) uninstalls the app
// and retries once, as the APK path does.
func TestFreshEmulatorRetriesAFailedAPKSetInstallAfterUninstall(t *testing.T) {
	f := newFakeSDK(t, false, "hw.keyboard=no\n")
	f.withBundletool(t)
	f.touch(t, "installfail")

	dev := provisionEmulatorSlot(
		emulatorSlot{name: flowAVD, port: 5556},
		emulatorContext{APKSetPath: "/app.apks", BundleID: "com.example"},
	)
	if dev.Status != contract.StatusReady {
		t.Fatalf("status = %s (%s), want ready", dev.Status, dev.ErrorMessage)
	}
	install := installAPKSetCall(t)
	want := "adb shell pm clear com.example\n" + install + "adb uninstall com.example\n" + install
	if got := f.calls(t); !strings.HasSuffix(got, want) {
		t.Fatalf("calls =\n%s\nwant them to end with\n%s", got, want)
	}
}

// reset on a bundle squad clears the data, then installs the APK set again
// so the on-demand splits the clear deleted are back.
func TestResetAppReinstallsTheAPKSetAfterClearing(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=no\n")
	f.withBundletool(t)
	set := filepath.Join(t.TempDir(), "app.apks")
	writeFile(t, set, "")

	if err := ResetApp("emulator-5556", "com.example", set); err != nil {
		t.Fatal(err)
	}
	adb, _ := ADB()
	want := "adb shell am force-stop com.example\n" +
		"adb shell pm clear com.example\n" +
		"bundletool install-apks --apks=" + set + " --device-id=emulator-5556 --adb=" + adb + " --allow-test-only\n"
	if got := f.calls(t); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
}

// Without an APK set (an APK squad, or a set reset can't vouch for) reset
// only clears.
func TestResetAppOnlyClearsWithoutAnAPKSet(t *testing.T) {
	f := newFakeSDK(t, true, "hw.keyboard=no\n")
	f.withBundletool(t)
	if err := ResetApp("emulator-5556", "com.example", ""); err != nil {
		t.Fatal(err)
	}
	want := "adb shell am force-stop com.example\nadb shell pm clear com.example\n"
	if got := f.calls(t); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
}
