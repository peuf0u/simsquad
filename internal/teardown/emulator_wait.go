package teardown

import (
	"strconv"
	"strings"
	"time"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/util"
)

// `adb emu kill` returns once the request is sent, not when the emulator has
// exited. An emulator still shutting down writes emulator-user.ini into its
// AVD folder, so deleting the AVD right away can leave a stray
// simsquad-*.avd folder holding only that file (issue #39). Teardown waits up
// to emulatorExitTimeout for the emulator to be gone; past that it deletes
// anyway, as it did before the wait existed.
const (
	emulatorExitTimeout = 15 * time.Second
	emulatorExitPoll    = 300 * time.Millisecond
)

// stopThenDelete runs kill, waits until gone reports the emulator has exited
// or timeout passes, then runs del and returns its error.
func stopThenDelete(kill func(), gone func() bool, del func() error, timeout, poll time.Duration) error {
	kill()
	waitGone(gone, timeout, poll)
	return del()
}

// waitGone polls gone every poll until it reports true (returns true) or
// timeout passes (returns false).
func waitGone(gone func() bool, timeout, poll time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for {
		if gone() {
			return true
		}
		if !time.Now().Before(deadline) {
			return false
		}
		time.Sleep(poll)
	}
}

// emulatorGoneProbe picks how to tell that dev's emulator has exited: its
// recorded PID when there is one, otherwise its serial dropping out of
// `adb devices`. With neither available there is nothing to wait on.
func emulatorGoneProbe(dev contract.Device, adb string) func() bool {
	if dev.EmulatorPID > 0 {
		return func() bool { return !emulatorProcessAlive(dev.EmulatorPID) }
	}
	if adb == "" {
		return func() bool { return true }
	}
	return func() bool {
		r, err := util.Run(adb, []string{"devices"}, util.RunOpts{Timeout: 5 * time.Second})
		if err != nil || r.Code != 0 {
			return true
		}
		return !serialListed(r.Stdout, dev.UDID)
	}
}

func emulatorProcessAlive(pid int) bool {
	r, err := util.Run("ps", []string{"-p", strconv.Itoa(pid), "-o", "stat=,comm="},
		util.RunOpts{Timeout: 5 * time.Second})
	if err != nil || r.Code != 0 {
		return false
	}
	return emulatorProcessLine(r.Stdout)
}

// emulatorProcessLine reports whether `ps -o stat=,comm=` output describes a
// live emulator. PIDs are reused, so the PID only counts while its command is
// still the emulator (or its qemu child); a zombie has already exited.
func emulatorProcessLine(out string) bool {
	fields := strings.Fields(out)
	if len(fields) < 2 || strings.HasPrefix(fields[0], "Z") {
		return false
	}
	comm := strings.ToLower(strings.Join(fields[1:], " "))
	return strings.Contains(comm, "emulator") || strings.Contains(comm, "qemu")
}

// serialListed reports whether serial appears, in any state, in
// `adb devices` output.
func serialListed(out, serial string) bool {
	for _, line := range strings.Split(out, "\n") {
		if fields := strings.Fields(line); len(fields) > 0 && fields[0] == serial {
			return true
		}
	}
	return false
}
