package ios

import (
	"sync/atomic"
	"testing"
	"time"
)

// TestWaitForInstalld_Ready exercises the success path: the probe flips to
// true on its third invocation. waitForInstalld should return true; the
// elapsed time should land near 2 * poll (two failed polls before the third
// succeeds).
func TestWaitForInstalld_Ready(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	probe := func(_ string) bool {
		return calls.Add(1) >= 3
	}

	start := time.Now()
	ok := waitForInstalld("udid-1", 200*time.Millisecond, 10*time.Millisecond, probe)
	elapsed := time.Since(start)

	if !ok {
		t.Fatal("waitForInstalld returned false, want true")
	}
	if calls.Load() != 3 {
		t.Fatalf("probe call count = %d, want 3", calls.Load())
	}
	if elapsed < 20*time.Millisecond {
		t.Fatalf("elapsed = %s, expected at least ~20ms after 2 polls", elapsed)
	}
}

// TestWaitForInstalld_ImmediateReady verifies no unnecessary sleeping on the
// happy path: a probe that returns true on the first call should return in
// well under one poll interval.
func TestWaitForInstalld_ImmediateReady(t *testing.T) {
	t.Parallel()

	probe := func(_ string) bool { return true }

	start := time.Now()
	ok := waitForInstalld("udid-2", 200*time.Millisecond, 50*time.Millisecond, probe)
	elapsed := time.Since(start)

	if !ok {
		t.Fatal("waitForInstalld returned false on always-ready probe")
	}
	if elapsed > 40*time.Millisecond {
		t.Fatalf("elapsed = %s, expected well under one poll interval (50ms)", elapsed)
	}
}

// TestWaitForInstalld_Timeout exercises the landmine path: probe never reports
// installd ready. The function must NOT sleep past the deadline, must return
// false, and must have called the probe at least once.
func TestWaitForInstalld_Timeout(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	probe := func(_ string) bool {
		calls.Add(1)
		return false
	}

	start := time.Now()
	ok := waitForInstalld("udid-3", 60*time.Millisecond, 20*time.Millisecond, probe)
	elapsed := time.Since(start)

	if ok {
		t.Fatal("waitForInstalld returned true on never-ready probe")
	}
	if calls.Load() < 1 {
		t.Fatal("probe never invoked")
	}
	// Should not run substantially past the timeout — the loop is designed to
	// stop polling within one poll interval of the deadline.
	if elapsed > 100*time.Millisecond {
		t.Fatalf("elapsed = %s, expected well under 100ms (timeout 60ms)", elapsed)
	}
}

func TestInstalldListed(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want bool
	}{
		{"on-demand, not running", "PID\tStatus\tLabel\n-\t-9\tcom.apple.mobile.installd\n", true},
		{"running", "PID\tStatus\tLabel\n412\t0\tcom.apple.mobile.installd\n", true},
		{"legacy label", "-\t0\tcom.apple.installd\n", true},
		{"only neighbours", "-\t-9\tcom.apple.installcoordinationd\n17348\t0\tcom.apple.MobileInstallationHelperService\n", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		if got := installdListed(tc.out); got != tc.want {
			t.Errorf("%s: installdListed = %v, want %v", tc.name, got, tc.want)
		}
	}
}
