package teardown

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

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
	writeScript(t, filepath.Join(sdk, "cmdline-tools", "latest", "bin", "avdmanager"),
		`echo "$1 $2 $4" >> `+log+"\n")
	return log
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
