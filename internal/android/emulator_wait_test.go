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

// PIDs are reused: a recorded emulator PID that now belongs to another
// process must count as gone, as must one that has exited.
func TestEmulatorProcessAlive(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ps   string
		want bool
	}{
		{"qemu running", "S    /sdk/emulator/qemu/darwin-aarch64/qemu-system-aarch64\n", true},
		{"emulator launcher", "R    /sdk/emulator/emulator", true},
		{"exited", "", false},
		{"pid reused", "S    /bin/zsh", false},
		{"zombie", "Z    (qemu-system-aarc)", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := emulatorProcessLine(c.ps); got != c.want {
				t.Fatalf("emulatorProcessLine(%q) = %v, want %v", c.ps, got, c.want)
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
