package teardown

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/peuf0u/simsquad/internal/contract"
)

const teardownAVD = "simsquad-x-android-0"

// fakeTeardownSDK is an SDK whose adb and avdmanager are shell scripts
// appending what they do to a log. adb lists emulator-5554 for its first
// `listed` `devices` calls; `adb emu avd name` answers avdName, or fails
// when avdName is "".
func fakeTeardownSDK(t *testing.T, listed int, avdName string) (log string) {
	t.Helper()
	sdk := t.TempDir()
	t.Setenv("ANDROID_HOME", sdk)
	t.Setenv("PATH", t.TempDir()+":/bin:/usr/bin")
	dir := t.TempDir()
	log = filepath.Join(dir, "log")
	count := filepath.Join(dir, "count")
	name := `printf '%s\r\nOK\r\n' ` + avdName
	if avdName == "" {
		name = `echo 'error: could not connect to TCP port 5555' >&2; exit 1`
	}
	writeScript(t, filepath.Join(sdk, "platform-tools", "adb"), `
case "$1" in
  devices)
    echo devices >> `+log+`
    n=$(cat `+count+` 2>/dev/null || echo 0); n=$((n+1)); echo $n > `+count+`
    echo 'List of devices attached'
    [ "$n" -le `+strconv.Itoa(listed)+` ] && printf 'emulator-5554\tdevice\n'
    exit 0 ;;
  -s)
    if [ "$3 $4 $5" = "emu avd name" ]; then echo "avd name" >> `+log+`; `+name+`; exit 0; fi
    echo "$3 $4" >> `+log+` ;;
esac
`)
	// With a pid file (startSquadEmulator), the delete also logs whether
	// that process was still running, so tests see the order.
	pidFile := filepath.Join(dir, "pid")
	writeScript(t, filepath.Join(sdk, "cmdline-tools", "latest", "bin", "avdmanager"), `
if [ -e `+pidFile+` ]; then
  st=$(ps -o stat= -p $(cat `+pidFile+`) 2>/dev/null)
  case "$st" in ""|Z*) ;; *) echo "emulator still running" >> `+log+` ;; esac
fi
echo "$1 $2 $4" >> `+log+"\n")
	return log
}

// TestHelperSleep is not a real test: startHelperProcess re-runs the test
// binary with this env var set so it stands in for an emulator process.
func TestHelperSleep(t *testing.T) {
	if os.Getenv("SIMSQUAD_TEST_HELPER_SLEEP") != "1" {
		t.Skip("helper process only")
	}
	time.Sleep(30 * time.Second)
}

// startHelperProcess runs the test binary, linked as qemu-system-aarch64,
// with args after "--", so ps shows an emulator-like process whose
// arguments hold args, and writes its PID next to log for the fake
// avdmanager. It is reaped as soon as it exits.
func startHelperProcess(t *testing.T, log string, args ...string) int {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	// The name only makes it look like an emulator; the arguments decide.
	bin := filepath.Join(t.TempDir(), "qemu-system-aarch64")
	if err := os.Link(self, bin); err != nil {
		bin = self
	}
	cmd := exec.Command(bin, append([]string{"-test.run=^TestHelperSleep$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "SIMSQUAD_TEST_HELPER_SLEEP=1")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = cmd.Wait() }()
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	pid := cmd.Process.Pid
	if err := os.WriteFile(filepath.Join(filepath.Dir(log), "pid"), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		t.Fatal(err)
	}
	return pid
}

func processExited(pid int) bool {
	out, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "stat=").Output()
	stat := strings.TrimSpace(string(out))
	return err != nil || stat == "" || strings.HasPrefix(stat, "Z")
}

func shortEmulatorExitTimeout(t *testing.T) {
	t.Helper()
	old := emulatorExitTimeout
	t.Cleanup(func() { emulatorExitTimeout = old })
	emulatorExitTimeout = 300 * time.Millisecond
}

var squadEmulatorArgs = []string{"-avd", teardownAVD, "-port", "5554", "-no-window"}

// Review round 5, item 3: the squad's own emulator, still booting after an
// interrupted deploy, may not answer the AVD name query or honour emu
// kill. Teardown then stops its recorded PID, verified by its arguments
// as the squad's emulator, and deletes the AVD only after it has exited,
// instead of leaving it running with its AVD gone.
func TestTeardownAndroidEmulatorSignalsTheSquadsVerifiedPID(t *testing.T) {
	cases := []struct {
		name, avdName string
		wantEmuKill   bool
	}{
		{"unreadable AVD name", "", false},
		{"emu kill ignored", teardownAVD, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := fakeTeardownSDK(t, 100, c.avdName)
			shortEmulatorExitTimeout(t)
			pid := startHelperProcess(t, log, squadEmulatorArgs...)

			dev := teardownEmulator()
			dev.EmulatorPID = pid
			if err := teardownAndroidEmulator(dev); err != nil {
				t.Fatal(err)
			}
			if !processExited(pid) {
				t.Fatalf("emulator %d still runs, want it stopped", pid)
			}
			got := readLog(t, log)
			if strings.Contains(got, "emu kill") != c.wantEmuKill {
				t.Fatalf("calls =\n%s\nwant emu kill: %v", got, c.wantEmuKill)
			}
			if strings.Contains(got, "emulator still running") || !strings.HasSuffix(got, "delete avd "+teardownAVD+"\n") {
				t.Fatalf("calls =\n%s\nwant the AVD deleted once the emulator exited", got)
			}
		})
	}
}

// A recorded PID that isn't the squad's emulator (here another AVD's qemu,
// as after a reboot) is never signalled: with the name unreadable the
// emulator is left running with the warning, as before.
func TestTeardownAndroidEmulatorNeverSignalsAnotherAVDsPID(t *testing.T) {
	log := fakeTeardownSDK(t, 100, "")
	shortEmulatorExitTimeout(t)
	pid := startHelperProcess(t, log, "-avd", "Pixel_8_API_35", "-port", "5554")

	dev := teardownEmulator()
	dev.EmulatorPID = pid
	err := teardownAndroidEmulator(dev)
	if err == nil || !strings.Contains(err.Error(), "could not read its AVD name") {
		t.Fatalf("err = %v, want the could-not-read warning", err)
	}
	if processExited(pid) {
		t.Fatal("another AVD's emulator was signalled")
	}
	if got := readLog(t, log); strings.Contains(got, "emu kill") || !strings.HasSuffix(got, "delete avd "+teardownAVD+"\n") {
		t.Fatalf("calls =\n%s\nwant no emu kill and the squad's AVD deleted", got)
	}
}

func teardownEmulator() contract.Device {
	return contract.Device{
		Platform: contract.PlatformAndroid,
		UDID:     "emulator-5554",
		Name:     teardownAVD,
		AVDName:  teardownAVD,
	}
}

func readLog(t *testing.T, log string) string {
	t.Helper()
	b, err := os.ReadFile(log)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(b)
}

// teardownAndroidEmulator only runs avdmanager delete once the emulator's
// serial has left `adb devices` (issue #39), and kills it only once it has
// answered with the squad's AVD name.
func TestTeardownAndroidEmulatorDeletesAfterTheEmulatorIsGone(t *testing.T) {
	// Listed for the ownership check and the first gone-probe, then not.
	log := fakeTeardownSDK(t, 2, teardownAVD)

	if err := teardownAndroidEmulator(teardownEmulator()); err != nil {
		t.Fatal(err)
	}
	want := "devices\navd name\nemu kill\ndevices\ndevices\ndelete avd " + teardownAVD + "\n"
	if got := readLog(t, log); got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
}

// Review round 4 (#33): the recorded serial is only a port. After a reboot
// the user's own emulator can hold it; dismiss, sweep and deploy's platform
// reset must not kill it, nor wait for it to exit. When the name can't be
// read the emulator is left running too (fail safe). Either way the squad's
// own AVD is deleted and the warning names the serial.
func TestTeardownAndroidEmulatorLeavesAnEmulatorItCannotTellIsTheSquads(t *testing.T) {
	cases := []struct {
		name, avdName, wantWarn string
	}{
		{"another AVD on the port", "Pixel_8_API_35", "runs Pixel_8_API_35, not " + teardownAVD},
		{"unreadable AVD name", "", "could not read its AVD name"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			log := fakeTeardownSDK(t, 100, c.avdName)

			err := teardownAndroidEmulator(teardownEmulator())
			if err == nil || !strings.Contains(err.Error(), c.wantWarn) || !strings.Contains(err.Error(), "emulator-5554") {
				t.Fatalf("err = %v, want a warning naming emulator-5554 and %q", err, c.wantWarn)
			}
			got := readLog(t, log)
			if strings.Contains(got, "emu kill") {
				t.Fatalf("calls =\n%s\nwant no emu kill", got)
			}
			if strings.Count(got, "devices\n") != 1 {
				t.Fatalf("calls =\n%s\nwant one devices call: no wait for an emulator left running", got)
			}
			if !strings.HasSuffix(got, "delete avd "+teardownAVD+"\n") {
				t.Fatalf("calls =\n%s\nwant the squad's AVD deleted", got)
			}
		})
	}
}

// Nothing on the serial: no name query (and its retries), no kill, no wait;
// the AVD is deleted.
func TestTeardownAndroidEmulatorDeletesAStoppedEmulatorsAVD(t *testing.T) {
	log := fakeTeardownSDK(t, 0, teardownAVD)

	if err := teardownAndroidEmulator(teardownEmulator()); err != nil {
		t.Fatal(err)
	}
	if got, want := readLog(t, log), "devices\ndelete avd "+teardownAVD+"\n"; got != want {
		t.Fatalf("calls =\n%s\nwant\n%s", got, want)
	}
}

func writeScript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+strings.TrimLeft(body, "\n")), 0o755); err != nil {
		t.Fatal(err)
	}
}
