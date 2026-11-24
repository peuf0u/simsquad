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
// `adb devices`. With neither available there is nothing to wait on. A PID
// that is no longer dev's emulator (see SquadEmulatorAlive) counts as gone.
func EmulatorGoneProbe(dev contract.Device, adb string) func() bool {
	if dev.EmulatorPID > 0 {
		return func() bool { return !SquadEmulatorAlive(dev) }
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

// SquadEmulatorAlive reports whether dev's recorded EmulatorPID is still
// dev's own emulator: a live process whose arguments hold `-avd <its AVD>`
// and, when its port is known, `-port <port>`. Only then may simsquad
// signal it. The recorded PID is most likely stale right when it is needed
// (after a reboot), and by then it can belong to another squad's emulator,
// the user's Android Studio one, or a colima/lima/UTM qemu VM, all of which
// a command-name check would accept. The emulator launcher execs qemu with
// its own arguments, so the PID keeps them.
func SquadEmulatorAlive(dev contract.Device) bool {
	port, err := emulatorPort(dev)
	if err != nil {
		port = 0
	}
	return squadEmulatorPIDAlive(dev.EmulatorPID, avdNameOf(dev), port)
}

func squadEmulatorPIDAlive(pid int, avd string, port int) bool {
	if pid <= 0 || avd == "" {
		return false
	}
	r, err := util.Run("ps", []string{"-ww", "-p", strconv.Itoa(pid), "-o", "stat=,args="},
		util.RunOpts{Timeout: 5 * time.Second})
	if err != nil || r.Code != 0 {
		return false
	}
	return squadEmulatorLine(r.Stdout, avd, port)
}

// squadEmulatorLine reports whether `ps -o stat=,args=` output describes a
// live process started with `-avd avd` and, when port > 0, `-port port`. A
// zombie has already exited.
func squadEmulatorLine(out, avd string, port int) bool {
	fields := strings.Fields(out)
	if len(fields) < 2 || strings.HasPrefix(fields[0], "Z") {
		return false
	}
	args := fields[1:]
	if !hasArgPair(args, "-avd", avd) {
		return false
	}
	return port <= 0 || hasArgPair(args, "-port", strconv.Itoa(port))
}

func hasArgPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
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
