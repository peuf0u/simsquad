package teardown

import (
	"errors"
	"strings"
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
		func() { events = append(events, "fallback") },
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
		nil,
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

// An emulator that hasn't exited by the timeout (one still booting may
// ignore emu kill) gets the fallback before the delete.
func TestStopThenDeleteRunsTheFallbackBeforeDeletingAfterTheTimeout(t *testing.T) {
	t.Parallel()

	var events []string
	err := stopThenDelete(
		func() { events = append(events, "kill") },
		func() bool { return false },
		func() { events = append(events, "fallback") },
		func() error { events = append(events, "delete"); return nil },
		10*time.Millisecond, time.Millisecond,
	)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(events, " "); got != "kill fallback delete" {
		t.Fatalf("events = %s, want kill fallback delete", got)
	}
}
