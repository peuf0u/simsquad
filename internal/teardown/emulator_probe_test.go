package teardown

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
)

// teardownAndroidEmulator only runs avdmanager delete once the emulator's
// serial has left `adb devices`. Fake adb and avdmanager append to a log.
func TestTeardownAndroidEmulatorDeletesAfterTheEmulatorIsGone(t *testing.T) {
	sdk := t.TempDir()
	t.Setenv("ANDROID_HOME", sdk)
	t.Setenv("PATH", t.TempDir()+":/bin:/usr/bin")
	log := filepath.Join(t.TempDir(), "log")
	count := filepath.Join(t.TempDir(), "count")

	// adb lists the serial for its first two `devices` calls, then not.
	writeScript(t, filepath.Join(sdk, "platform-tools", "adb"), `
case "$1" in
  devices)
    echo devices >> `+log+`
    n=$(cat `+count+` 2>/dev/null || echo 0); n=$((n+1)); echo $n > `+count+`
    echo 'List of devices attached'
    [ "$n" -le 2 ] && printf 'emulator-5554\tdevice\n'
    exit 0 ;;
  -s) echo "$3 $4" >> `+log+` ;;
esac
`)
	writeScript(t, filepath.Join(sdk, "cmdline-tools", "latest", "bin", "avdmanager"),
		`echo "$1 $2 $4" >> `+log+"\n")

	dev := contract.Device{
		Platform: contract.PlatformAndroid,
		UDID:     "emulator-5554",
		Name:     "simsquad-x-android-0",
		AVDName:  "simsquad-x-android-0",
	}
	if err := teardownAndroidEmulator(dev); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	want := "emu kill\ndevices\ndevices\ndevices\ndelete avd simsquad-x-android-0\n"
	if string(got) != want {
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
