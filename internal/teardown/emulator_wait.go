package teardown

import (
	"time"

	"github.com/peuf0u/simsquad/internal/android"
)

// stopThenDelete runs kill, waits until gone reports the emulator has exited
// or timeout passes, runs fallback (when not nil) if it hasn't, then runs
// del and returns its error.
//
// An emulator still shutting down writes emulator-user.ini into its AVD
// folder, so deleting the AVD right away can leave a stray simsquad-*.avd
// folder holding only that file (issue #39). One still booting may not
// honour the kill at all, hence the fallback. Past both, teardown deletes
// anyway, as it did before the wait existed.
func stopThenDelete(kill func(), gone func() bool, fallback func(), del func() error, timeout, poll time.Duration) error {
	kill()
	if !android.WaitGone(gone, timeout, poll) && fallback != nil {
		fallback()
	}
	return del()
}
