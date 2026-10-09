package cli

import (
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/util"
)

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
	return []*cobra.Command{
		newSkillInstallCmd(),
		newSkillStatusCmd(),
	}
}

// repoRoot is the app repo the skill commands act on: the enclosing git
// work tree, or the current directory outside one.
func repoRoot() string {
	r, err := util.Run("git", []string{"rev-parse", "--show-toplevel"}, util.RunOpts{})
	if err == nil && r.Code == 0 {
		if top := strings.TrimSpace(r.Stdout); top != "" {
			return top
		}
	}
	wd, _ := os.Getwd()
	return wd
}
