package cli

import "fmt"

// ExitError carries a non-zero exit code out of a command whose JSON has
// already been written to stdout (run validate, run report, skill status).
// Returning it instead of calling os.Exit lets the in-process CLI tests
// assert exit codes; main turns it into the process exit status without
// printing an error banner.
type ExitError struct {
	Code int
}

func (e *ExitError) Error() string { return fmt.Sprintf("exit status %d", e.Code) }

// Version is the binary version, set by main before the root command runs.
// Commands that stamp files with it (skill install) read it here because
// internal packages can't import main.
var Version = "dev"
