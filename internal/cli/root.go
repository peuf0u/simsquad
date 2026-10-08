// Package cli wires every simsquad subcommand onto a cobra root suitable for
// fang.Execute. Each verb has its own file in this package.
package cli

import (
	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/util"
)

// canonName canonicalises a user-supplied --name the same way deploy does
// before registering a squad. Every verb that looks a squad up by name must
// apply it, or `deploy --name "My Squad"` (stored as "my-squad") becomes
// unreachable from `dismiss --name "My Squad"`.
func canonName(name string) string {
	return util.SanitiseName(name)
}

// NewRootCmd builds the simsquad root command tree. The root itself does
// nothing; users always invoke a verb.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "simsquad",
		Short: "Single-binary device-squad provisioner for parallel mobile QA",
		Long: "simsquad builds your iOS or Android app, creates a matrix of fresh\n" +
			"simulators or emulators, installs the build, and emits a machine-readable\n" +
			"squad descriptor on stdout. A downstream test driver (e.g. the qa-run\n" +
			"skill) consumes the squad via `simsquad devices`.",
		SilenceUsage: true,
	}

	// Hide cobra's auto-injected `completion` verb — simsquad's stdout is a
	// strict JSON contract, and a verb that dumps a shell script there is
	// off-brand. Users who want completions can still generate them via the
	// cobra API; we just don't advertise it as a top-level verb.
	root.CompletionOptions.DisableDefaultCmd = true

	root.AddCommand(
		newDeployCmd(),
		newDismissCmd(),
		newStatusCmd(),
		newSweepCmd(),
		newEquipCmd(),
		newDevicesCmd(),
		newSetEnvCmd(),
		newResetCmd(),
	)

	return root
}
