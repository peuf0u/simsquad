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
		// A copy of /bin/sleep is killed by code signing on macOS; the Go
		// test binary is ad-hoc signed, so a renamed copy still runs.
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		fake := filepath.Join(t.TempDir(), "qemu-fake")
		linkOrCopy(t, self, fake)
		cmd := exec.Command(fake, "-test.run=^TestHelperSleep$")
		cmd.Env = append(os.Environ(), "SIMSQUAD_TEST_HELPER_SLEEP=1")
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })

		dev := contract.Device{UDID: "emulator-5554", EmulatorPID: cmd.Process.Pid}
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
