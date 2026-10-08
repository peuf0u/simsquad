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
		Short: "Prune orphan simsquad-* sims/AVDs left by crashed deploys",
		Args:  cobra.NoArgs,
		Long: "Scans the host for any sim/AVD whose name matches\n" +
			"^simsquad-[a-z0-9-]+-(ios|android)-\\d+$ but has no entry in the\n" +
			"registry — debris from a crashed deploy or an interrupted simsquad.\n" +
			"Tracked squads are untouched; for those, use `simsquad dismiss`.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSweep(cmd)
		},
	}
	return cmd
}

func runSweep(cmd *cobra.Command) error {
	logger := progress.New()
	defer logger.Close()

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
		"pruned": map[string]any{
			"ios":     pruned.IOS,
			"android": pruned.Android,
		},
	})
}
