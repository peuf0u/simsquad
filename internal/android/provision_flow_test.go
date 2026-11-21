package android

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
)

const flowAVD = "simsquad-x-android-0"

// fakeSDK is an Android SDK whose adb, emulator and avdmanager are shell
// scripts appending their arguments to a log. The fake emulator is "up" while
// a marker file exists: the emulator script creates it and `adb emu kill`
// removes it.
type fakeSDK struct {
	log, up, config string
}

func newFakeSDK(t *testing.T, running bool, config string) fakeSDK {
	t.Helper()
	sdk := t.TempDir()
	avdHome := t.TempDir()
	t.Setenv("ANDROID_HOME", sdk)
	t.Setenv("ANDROID_AVD_HOME", avdHome)
	t.Setenv("PATH", t.TempDir()+":/bin:/usr/bin")
	oldSettle := emulatorSettleAfterBoot
	t.Cleanup(func() { emulatorSettleAfterBoot = oldSettle })
	emulatorSettleAfterBoot = 0

	dir := t.TempDir()
	f := fakeSDK{
		log:    filepath.Join(dir, "log"),
		up:     filepath.Join(dir, "up"),
		config: filepath.Join(avdHome, flowAVD+".avd", "config.ini"),
	}
	if running {
		writeFile(t, f.up, "")
	}
	writeFile(t, f.config, config)

	writeFile(t, filepath.Join(sdk, "platform-tools", "adb"), `#!/bin/sh
log=`+f.log+`; up=`+f.up+`
if [ "$1" = devices ]; then
  echo 'List of devices attached'
  [ -e $up ] && printf 'emulator-5556\tdevice\n'
  exit 0
fi
shift 2
case "$*" in
  "shell true") [ -e $up ] ;;
  "emu kill") echo "adb emu kill" >> $log; rm -f $up ;;
  wait-for-device) n=0; while [ ! -e $up ] && [ $n -lt 50 ]; do sleep 0.1; n=$((n+1)); done; [ -e $up ] ;;
  "shell getprop sys.boot_completed") echo 1 ;;
  *) echo "adb $*" >> $log ;;
esac
`)
	writeFile(t, filepath.Join(sdk, "emulator", "emulator"),
		"#!/bin/sh\necho \"emulator $*\" >> "+f.log+"\ntouch "+f.up+"\n")
	writeFile(t, filepath.Join(sdk, "cmdline-tools", "latest", "bin", "avdmanager"),
		"#!/bin/sh\necho "+flowAVD+"\n")
	return f
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
