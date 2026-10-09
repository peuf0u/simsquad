package cli

import "github.com/spf13/cobra"

// newRunCmd assembles the `run` group: the deterministic steps of a test
// run (setup, worker supervision, validation, reporting). The agent drives
// the procedure; each subcommand lives in its own run_<verb>.go file.
func newRunCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Test-run steps: new, worker, validate, report",
		Long: "Commands an agent calls while verifying a feature on a squad. Each\n" +
			"emits JSON on stdout; human progress goes to stderr.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(runSubcommands()...)
	return cmd
}

// runSubcommands lists the `run` subcommands. Each ticket appends its own
// constructor here.
func runSubcommands() []*cobra.Command {
	return []*cobra.Command{
		newRunNewCmd(),
	}
}
