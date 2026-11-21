package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/config"
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

// errNoConfig is the refusal of a skill command that needs a skills root
// but finds no simsquad config to anchor it.
var errNoConfig = errors.New("no simsquad.toml found; run `simsquad equip` first")

// skillsRoot is the folder the skill commands act on: the config dir (the
// nearest folder up from the working directory holding simsquad.toml or
// simsquad.local.toml), the same root `run new` puts runs under and the
// workers read qa/README.md from. Empty when there is no config.
func skillsRoot() string {
	return config.FindConfigDir(".")
}
