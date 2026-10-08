package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/state"
)

func newSetEnvCmd() *cobra.Command {
	var (
		name     string
		setVals  []string
		unsetKey []string
		clear    bool
	)

	cmd := &cobra.Command{
		Use:   "set-env",
		Short: "Mutate the env on a named squad",
		Args:  cobra.NoArgs,
		Long: "Updates the env map on a squad. Applied in this order:\n" +
			"  --clear   wipe all keys\n" +
			"  --unset   remove specific keys (repeatable)\n" +
			"  --set     add or overwrite keys (repeatable, KEY=VALUE)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runSetEnv(cmd, setEnvOpts{
				name:     name,
				setVals:  setVals,
				unsetKey: unsetKey,
				clear:    clear,
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "squad to mutate (required)")
	cmd.Flags().StringArrayVar(&setVals, "set", nil, "add or overwrite an env key: KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&unsetKey, "unset", nil, "remove an env key (repeatable)")
	cmd.Flags().BoolVar(&clear, "clear", false, "wipe every existing key before --unset / --set apply")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

type setEnvOpts struct {
	name     string
	setVals  []string
	unsetKey []string
	clear    bool
}

func runSetEnv(cmd *cobra.Command, opts setEnvOpts) error {
	if opts.name == "" {
		return errors.New("set-env: --name is required")
	}
	if !opts.clear && len(opts.setVals) == 0 && len(opts.unsetKey) == 0 {
		return errors.New("set-env: pass --set, --unset, or --clear")
	}

	setMap, err := parseSetEntries(opts.setVals)
	if err != nil {
		return err
	}

	entry, ok, err := state.FindSquad(canonName(opts.name))
	if err != nil {
		return fmt.Errorf("set-env: lookup squad: %w", err)
	}
	if !ok {
		return fmt.Errorf("set-env: no squad named %q", opts.name)
	}

	var updatedEnv map[string]string
	lockErr := state.WithIndexLockE(func() error {
		rec, err := state.LoadRecord(entry.Name)
		if err != nil {
			return err
		}
		if rec == nil {
			return fmt.Errorf("squad %s has no state file", entry.Name)
		}
		if opts.clear {
			rec.Env = nil
		}
		for _, k := range opts.unsetKey {
			delete(rec.Env, k)
		}
		if len(setMap) > 0 {
			if rec.Env == nil {
				rec.Env = make(map[string]string, len(setMap))
			}
			for k, v := range setMap {
				rec.Env[k] = v
			}
		}
		if len(rec.Env) == 0 {
			rec.Env = nil // honour omitempty
		}
		updatedEnv = rec.Env
		return state.SaveRecord(rec)
	})
	if lockErr != nil {
		return fmt.Errorf("set-env: %w", lockErr)
	}

	return writeJSON(cmd, map[string]any{
		"name": entry.Name,
		"env":  updatedEnv,
	})
}

// parseSetEntries parses `--set KEY=VALUE` flags into a string map.
// Duplicates within a single invocation are tolerated (last wins) since
// interactive shell history may repeat a key intentionally. Mismatched
// KEY=VALUE entries still error.
func parseSetEntries(entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(entries))
	for _, raw := range entries {
		idx := strings.Index(raw, "=")
		if idx <= 0 {
			return nil, fmt.Errorf("set-env: --set %q: expected KEY=VALUE", raw)
		}
		key := strings.TrimSpace(raw[:idx])
		if key == "" {
			return nil, fmt.Errorf("set-env: --set %q: key must be non-empty", raw)
		}
		out[key] = raw[idx+1:]
	}
	return out, nil
}
