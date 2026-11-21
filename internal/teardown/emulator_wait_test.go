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
