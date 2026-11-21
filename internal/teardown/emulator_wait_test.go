package teardown

import (
	"errors"
	"testing"
	"time"
)

// Issue #39: `adb emu kill` returns before the emulator exits, and the exiting
// emulator writes emulator-user.ini into the AVD folder avdmanager just
// deleted, leaving a stray simsquad-*.avd behind.
func TestStopThenDeleteWaitsForTheEmulatorToBeGone(t *testing.T) {
	t.Parallel()

	var events []string
	probes := 0
	gone := func() bool {
		probes++
		events = append(events, "probe")
		return probes == 3
	}
	err := stopThenDelete(
		func() { events = append(events, "kill") },
		gone,
		func() error { events = append(events, "delete"); return nil },
		time.Minute, time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"kill", "probe", "probe", "probe", "delete"}
	if len(events) != len(want) {
		t.Fatalf("events = %v, want %v", events, want)
	}
	for i := range want {
		if events[i] != want[i] {
			t.Fatalf("events = %v, want %v", events, want)
		}
	}
}

func TestStopThenDeleteDeletesAnywayAfterTheTimeout(t *testing.T) {
	t.Parallel()

	timeout := 50 * time.Millisecond
	start := time.Now()
	var deletedAfter time.Duration
	wantErr := errors.New("delete failed")
	err := stopThenDelete(
		func() {},
		func() bool { return false },
		func() error { deletedAfter = time.Since(start); return wantErr },
		timeout, 5*time.Millisecond,
	)
	if !errors.Is(err, wantErr) {
		t.Fatalf("err = %v, want the delete error", err)
	}
	if deletedAfter < timeout {
		t.Fatalf("deleted after %s, before the %s timeout", deletedAfter, timeout)
	}
}

func TestWaitGoneReturnsAsSoonAsTheProbeReportsGone(t *testing.T) {
	t.Parallel()

	start := time.Now()
	if !waitGone(func() bool { return true }, time.Minute, time.Minute) {
		t.Fatal("waitGone = false, want true")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("waitGone took %s, want an immediate return", d)
	}
}

func TestWaitGoneGivesUpAfterTheTimeout(t *testing.T) {
	t.Parallel()

	timeout := 30 * time.Millisecond
	start := time.Now()
	if waitGone(func() bool { return false }, timeout, 5*time.Millisecond) {
		t.Fatal("waitGone = true, want false")
	}
	if d := time.Since(start); d < timeout {
		t.Fatalf("waitGone returned after %s, before the %s timeout", d, timeout)
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
