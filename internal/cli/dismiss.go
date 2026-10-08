package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/progress"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/teardown"
)

func newDismissCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "dismiss",
		Short: "Tear down a squad: shutdown + delete its sims/AVDs",
		Args:  cobra.NoArgs,
		Long: "Removes a squad's devices and its registry entry. The sims/AVDs are\n" +
			"shut down and deleted from the host.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDismiss(cmd, name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "squad to tear down (required)")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

func runDismiss(cmd *cobra.Command, name string) error {
	if name == "" {
		return errors.New("dismiss: --name is required")
	}
	name = canonName(name)

	logger := progress.New()
	defer logger.Close()

	_, ok, err := state.FindSquad(name)
	if err != nil {
		return fmt.Errorf("dismiss: lookup squad: %w", err)
	}
	if !ok {
		return fmt.Errorf("dismiss: no squad named %q", name)
	}
	if err := teardown.Squad(name, logger); err != nil {
		return err
	}
	logger.Close()
	return writeJSON(cmd, map[string]any{"removed": []string{name}})
}
