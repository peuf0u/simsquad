package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/progress"
	"github.com/peuf0u/simsquad/internal/teardown"
)

func newSweepCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sweep",
		Short: "Dismiss squads whose repo is gone and prune orphan simsquad-* sims/AVDs",
		Args:  cobra.NoArgs,
		Long: "First dismisses every squad whose recorded iOS or Android repo path\n" +
			"no longer exists — typically a removed git worktree — and deletes the\n" +
			"iOS derived data built from it.\n\n" +
			"Then scans the host for any sim/AVD whose name matches\n" +
			"^simsquad-[a-z0-9-]+-(ios|android)-\\d+$ but has no entry in the\n" +
			"registry — debris from a crashed deploy or an interrupted simsquad.\n\n" +
			"Squads whose repo still exists are untouched; for those, use\n" +
			"`simsquad dismiss`.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSweep(cmd)
		},
	}
	return cmd
}

func runSweep(cmd *cobra.Command) error {
	logger := progress.New()
	defer logger.Close()

	dismissed, err := teardown.DismissRemovedRepos(logger)
	if err != nil {
		return fmt.Errorf("sweep: %w", err)
	}
	if dismissed == nil {
		dismissed = []teardown.RemovedRepoSquad{}
	}

	pruned, err := teardown.PruneOrphans(logger)
	if err != nil {
		return fmt.Errorf("sweep: %w", err)
	}
	if pruned.IOS == nil {
		pruned.IOS = []string{}
	}
	if pruned.Android == nil {
		pruned.Android = []string{}
	}
	logger.Close()
	return writeJSON(cmd, map[string]any{
		"dismissed": dismissed,
		"pruned": map[string]any{
			"ios":     pruned.IOS,
			"android": pruned.Android,
		},
	})
}
