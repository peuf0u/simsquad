package cli

import "github.com/spf13/cobra"

// newSkillCmd assembles the `skill` group: installing the embedded agent
// skills into an app repo and checking they fit this binary.
func newSkillCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "skill",
		Short: "Install and check the agent skills in an app repo",
		Long: "Manages the simsquad and simsquad-test agent skills embedded in this\n" +
			"binary. Each subcommand emits JSON on stdout.",
		Args: cobra.NoArgs,
	}
	cmd.AddCommand(skillSubcommands()...)
	return cmd
}

// skillSubcommands lists the `skill` subcommands. Each ticket appends its
// own constructor here.
func skillSubcommands() []*cobra.Command {
	return []*cobra.Command{}
}
