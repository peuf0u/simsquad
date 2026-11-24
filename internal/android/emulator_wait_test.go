package android

import (
	"testing"
	"time"
)

func TestWaitGoneReturnsAsSoonAsTheProbeReportsGone(t *testing.T) {
	t.Parallel()

	start := time.Now()
	if !WaitGone(func() bool { return true }, time.Minute, time.Minute) {
		t.Fatal("WaitGone = false, want true")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("WaitGone took %s, want an immediate return", d)
	}
}

func TestWaitGoneGivesUpAfterTheTimeout(t *testing.T) {
	t.Parallel()

	timeout := 30 * time.Millisecond
	start := time.Now()
	if WaitGone(func() bool { return false }, timeout, 5*time.Millisecond) {
		t.Fatal("WaitGone = true, want false")
	}
	if d := time.Since(start); d < timeout {
		t.Fatalf("WaitGone returned after %s, before the %s timeout", d, timeout)
	}
}

// PIDs are reused, and after a reboot the recorded one can belong to
// another emulator or a qemu VM (colima, lima, UTM). It counts as the
// squad's emulator only while its arguments name the squad's AVD and port.
func TestSquadEmulatorLine(t *testing.T) {
	t.Parallel()

	const qemu = "/sdk/emulator/qemu/darwin-aarch64/qemu-system-aarch64"
	cases := []struct {
		name string
		ps   string
		port int
		want bool
	}{
		{"squad's qemu", "S    " + qemu + " -avd simsquad-x-android-0 -port 5556 -no-window\n", 5556, true},
		{"squad's launcher, port unknown", "R    /sdk/emulator/emulator -avd simsquad-x-android-0 -port 5556", 0, true},
		{"another AVD", "S    " + qemu + " -avd Pixel_8_API_35 -port 5556", 5556, false},
		{"AVD name only a prefix", "S    " + qemu + " -avd simsquad-x-android-01 -port 5556", 5556, false},
		{"another port", "S    " + qemu + " -avd simsquad-x-android-0 -port 5558", 5556, false},
		{"qemu VM", "S    /opt/homebrew/bin/qemu-system-aarch64 -M virt -accel hvf -m 4096", 5556, false},
		{"pid reused", "S    /bin/zsh", 5556, false},
		{"exited", "", 5556, false},
		{"zombie", "Z    (qemu-system-aarc)", 5556, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := squadEmulatorLine(c.ps, "simsquad-x-android-0", c.port); got != c.want {
				t.Fatalf("squadEmulatorLine(%q) = %v, want %v", c.ps, got, c.want)
			}
		})
	}
}

func TestSerialListed(t *testing.T) {
	t.Parallel()

	out := "List of devices attached\nemulator-5556\tdevice\nR58M\tdevice\n\n"
	if !serialListed(out, "emulator-5556") {
		t.Error("emulator-5556 not found, want listed")
	}
	if serialListed(out, "emulator-5554") {
		t.Error("emulator-5554 found, want not listed")
	}
}
