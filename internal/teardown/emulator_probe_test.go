package teardown

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/peuf0u/simsquad/internal/contract"
)

// TestHelperSleep is not a real test: TestEmulatorGoneProbe re-runs the test
// binary, copied under an emulator-like name, with this env var set so it
// stands in for a running qemu process.
func TestHelperSleep(t *testing.T) {
	if os.Getenv("SIMSQUAD_TEST_HELPER_SLEEP") != "1" {
		t.Skip("helper process only")
	}
	time.Sleep(30 * time.Second)
}

func TestEmulatorGoneProbe(t *testing.T) {
	t.Run("nothing to probe", func(t *testing.T) {
		if !emulatorGoneProbe(contract.Device{UDID: "emulator-5554"}, "")() {
			t.Fatal("probe = alive, want gone")
		}
	})

	t.Run("pid reused by another process", func(t *testing.T) {
		dev := contract.Device{UDID: "emulator-5554", EmulatorPID: os.Getpid()}
		if !emulatorGoneProbe(dev, "")() {
			t.Fatal("probe = alive, want gone")
		}
	})

	t.Run("emulator pid alive, then exited", func(t *testing.T) {
		// A copy of /bin/sleep is killed by code signing on macOS; the Go
		// test binary is ad-hoc signed, so a renamed copy still runs.
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		fake := filepath.Join(t.TempDir(), "qemu-fake")
		copyFile(t, self, fake)
		cmd := exec.Command(fake, "-test.run=^TestHelperSleep$")
		cmd.Env = append(os.Environ(), "SIMSQUAD_TEST_HELPER_SLEEP=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

		dev := contract.Device{UDID: "emulator-5554", EmulatorPID: cmd.Process.Pid}
		probe := emulatorGoneProbe(dev, "")
		if probe() {
			t.Fatal("probe = gone while the qemu process runs, want alive")
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if !probe() {
			t.Fatal("probe = alive after the qemu process exited, want gone")
		}
	})

	t.Run("no pid, serial leaves adb devices", func(t *testing.T) {
		dir := t.TempDir()
		adb := filepath.Join(dir, "adb")
		// Lists the serial until the marker file exists.
		script := "#!/bin/sh\necho 'List of devices attached'\n" +
			"[ -e " + filepath.Join(dir, "gone") + " ] || printf 'emulator-5554\\tdevice\\n'\n"
		if err := os.WriteFile(adb, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		probe := emulatorGoneProbe(contract.Device{UDID: "emulator-5554"}, adb)
		if probe() {
			t.Fatal("probe = gone while adb lists the serial, want alive")
		}
		if err := os.WriteFile(filepath.Join(dir, "gone"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
		if !probe() {
			t.Fatal("probe = alive after the serial left adb devices, want gone")
		}
	})
}

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

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
}
