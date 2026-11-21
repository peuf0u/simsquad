package android

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
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
		if !EmulatorGoneProbe(contract.Device{UDID: "emulator-5554"}, "")() {
			t.Fatal("probe = alive, want gone")
		}
	})

	t.Run("pid reused by another process", func(t *testing.T) {
		dev := contract.Device{UDID: "emulator-5554", EmulatorPID: os.Getpid()}
		if !EmulatorGoneProbe(dev, "")() {
			t.Fatal("probe = alive, want gone")
		}
	})

	t.Run("emulator pid alive, then exited", func(t *testing.T) {
		cmd := startHelperProcess(t, "qemu-fake", "-avd", "simsquad-x-android-0", "-port", "5554")
		dev := contract.Device{UDID: "emulator-5554", AVDName: "simsquad-x-android-0", EmulatorPID: cmd.Process.Pid}
		probe := EmulatorGoneProbe(dev, "")
		if probe() {
			t.Fatal("probe = gone while the qemu process runs, want alive")
		}
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if !probe() {
			t.Fatal("probe = alive after the qemu process exited, want gone")
		}
	})

	t.Run("qemu pid running another AVD", func(t *testing.T) {
		cmd := startHelperProcess(t, "qemu-system-aarch64", "-avd", "Pixel_8_API_35", "-port", "5554")
		dev := contract.Device{UDID: "emulator-5554", AVDName: "simsquad-x-android-0", EmulatorPID: cmd.Process.Pid}
		if !EmulatorGoneProbe(dev, "")() {
			t.Fatal("probe = alive for another AVD's qemu, want gone")
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
		probe := EmulatorGoneProbe(contract.Device{UDID: "emulator-5554"}, adb)
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

// startHelperProcess runs the test binary, linked as name, as TestHelperSleep
// with args after "--", so ps shows a process called name whose arguments
// hold args. A copy of /bin/sleep is killed by code signing on macOS; the Go
// test binary is ad-hoc signed, so a renamed link still runs. The fake
// emulator's state dir is cleared so TestMain runs the tests.
func startHelperProcess(t *testing.T, name string, args ...string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), name)
	linkOrCopy(t, self, bin)
	cmd := exec.Command(bin, append([]string{"-test.run=^TestHelperSleep$", "--"}, args...)...)
	cmd.Env = append(os.Environ(), "SIMSQUAD_TEST_HELPER_SLEEP=1", fakeEmulatorEnv+"=")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

// linkOrCopy hard-links src to dst, copying it when the link fails (another
// file system, say). Each fake SDK used to copy the test binary, and macOS
// assesses every new executable it sees before running it: under a busy
// syspolicyd that stalled exec. A link adds no new file content to assess,
// while its path still holds "emulator" for the PID probe.
func linkOrCopy(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.Link(src, dst); err == nil {
		return
	}
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
