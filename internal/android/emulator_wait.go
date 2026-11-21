package android

import (
	"strconv"
	"strings"
	"time"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/util"
)

// `adb emu kill` returns once the request is sent, not when the emulator has
// exited. Teardown waits for it before deleting the AVD (issue #39), and a
// windowed restart waits for it before booting on the same port (issue #33).
const (
	EmulatorExitTimeout = 15 * time.Second
	EmulatorExitPoll    = 300 * time.Millisecond
)

// WaitGone polls gone every poll until it reports true (returns true) or
// timeout passes (returns false).
func WaitGone(gone func() bool, timeout, poll time.Duration) bool {
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

// EmulatorGoneProbe picks how to tell that dev's emulator has exited: its
// recorded PID when there is one, otherwise its serial dropping out of
// `adb devices`. With neither available there is nothing to wait on.
func EmulatorGoneProbe(dev contract.Device, adb string) func() bool {
	if dev.EmulatorPID > 0 {
		return func() bool { return !emulatorProcessAlive(dev.EmulatorPID) }
	}
	if adb == "" {
		return func() bool { return true }
	}
	return func() bool { return !ADBListsSerial(adb, dev.UDID) }
}

// ADBListsSerial reports whether serial appears, in any state, in `adb
// devices`. A failing adb counts as not listed.
func ADBListsSerial(adb, serial string) bool {
	r, err := util.Run(adb, []string{"devices"}, util.RunOpts{Timeout: 5 * time.Second})
	if err != nil || r.Code != 0 {
		return false
	}
	return serialListed(r.Stdout, serial)
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
