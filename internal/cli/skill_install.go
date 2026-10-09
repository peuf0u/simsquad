package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/skills"
)

func newSkillInstallCmd() *cobra.Command {
	var (
		dir   string
		force bool
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Write the simsquad and simsquad-test skills into this app repo",
		Long: "Writes the agent skills embedded in this binary into the app repo so the\n" +
			"team commits them:\n" +
			"  .agents/skills/{simsquad,simsquad-test}/   the skills (Codex reads them here)\n" +
			"  .claude/skills/{simsquad,simsquad-test}    links to them (Claude Code)\n" +
			"  .claude/settings.json                      worker permissions (merged)\n" +
			"  .codex/rules/simsquad.rules                worker permissions for Codex\n" +
			"  .gitignore                                 gains a .simsquad/ line\n" +
			"  qa/README.md                               app notes scaffold, only when missing\n" +
			"\n" +
			"Every generated file carries a do-not-edit header with the binary version\n" +
			"and the skill contract number. Install refuses to overwrite a file that was\n" +
			"edited by hand unless --force is given. Re-running it is safe.\n" +
			"\n" +
			"The repo root is the enclosing git work tree, or the current directory.\n" +
			"stdout: {dir, version, contract, files}. Redirect stdout only (> out.json).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dir != "" {
				abs, err := filepath.Abs(dir)
				if err != nil {
					return fmt.Errorf("skill install: --dir: %w", err)
				}
				dir = abs
			}
			res, err := skills.Install(skills.Options{
				Root:    repoRoot(),
				Dir:     dir,
				Force:   force,
				Version: Version,
			})
			if err != nil {
				return fmt.Errorf("skill install: %w", err)
			}
			return writeJSON(cmd, res)
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "write the skills to this directory instead of .agents/skills (for other agent tools)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite generated files that were edited by hand")
	return cmd
}
