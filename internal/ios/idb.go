package ios

import (
	"fmt"
	"os/exec"
)

// IDBInstallHint is the user-facing message when idb isn't on PATH. The tool
// is needed for data-container wipe — not for install — see LocateIDB below.
const IDBInstallHint = "idb not found on PATH. Install via:\n" +
	"  • brew tap facebook/fb && brew install idb-companion fb-idb\n" +
	"  • or: pip install fb-idb (then ensure idb-companion is on PATH)"

// LocateIDB returns the absolute path to the idb binary or an error with an
// install hint.
//
// Why we keep idb around even though it can't install on Apple Silicon:
// `idb install` is broken on arm64 Macs — Apple-Silicon arch detection
// trips up the idb companion and the install silently fails. We use
// `simctl install` for the install path and reserve idb solely for the
// data-container wipe (`idb file rm` inside ~/Library/Application Support
// and ~/Documents on the sim) where simctl has no equivalent. Don't
// "fix" this backwards by re-adopting idb for install.
func LocateIDB() (string, error) {
	p, err := exec.LookPath("idb")
	if err != nil {
		return "", fmt.Errorf("%w; %s", err, IDBInstallHint)
	}
	return p, nil
}
