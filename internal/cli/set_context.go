package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/state"
)

func newSetContextCmd() *cobra.Command {
	return setContextCmd("set-context", false)
}

// newSetEnvCmd is set-context under its pre-0.5.0 name, kept for one
// release so existing scripts warn instead of break.
func newSetEnvCmd() *cobra.Command {
	return setContextCmd("set-env", true)
}

func setContextCmd(use string, deprecated bool) *cobra.Command {
	var (
		name     string
		setVals  []string
		unsetKey []string
		clear    bool
	)

	cmd := &cobra.Command{
		Use:    use,
		Short:  "Mutate the context on a named squad",
		Args:   cobra.NoArgs,
		Hidden: deprecated,
		Long: "Updates the context map on a squad: free-form test context such as\n" +
			"persona, stage or test-account login. Applied in this order:\n" +
			"  --clear   wipe all keys\n" +
			"  --unset   remove specific keys (repeatable)\n" +
			"  --set     add or overwrite keys (repeatable, KEY=VALUE)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deprecated {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "simsquad: set-env is deprecated; use set-context")
			}
			return runSetContext(cmd, setContextOpts{
				name:     name,
				setVals:  setVals,
				unsetKey: unsetKey,
				clear:    clear,
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "squad to mutate (required)")
	cmd.Flags().StringArrayVar(&setVals, "set", nil, "add or overwrite a context key: KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&unsetKey, "unset", nil, "remove a context key (repeatable)")
	cmd.Flags().BoolVar(&clear, "clear", false, "wipe every existing key before --unset / --set apply")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

type setContextOpts struct {
	name     string
	setVals  []string
	unsetKey []string
	clear    bool
}

func runSetContext(cmd *cobra.Command, opts setContextOpts) error {
	if opts.name == "" {
		return errors.New("set-context: --name is required")
	}
	if !opts.clear && len(opts.setVals) == 0 && len(opts.unsetKey) == 0 {
		return errors.New("set-context: pass --set, --unset, or --clear")
	}

	setMap, err := parseSetEntries(opts.setVals)
	if err != nil {
		return err
	}

	entry, ok, err := state.FindSquad(canonName(opts.name))
	if err != nil {
		return fmt.Errorf("set-context: lookup squad: %w", err)
	}
	if !ok {
		return fmt.Errorf("set-context: no squad named %q", opts.name)
	}

	var updated map[string]string
	lockErr := state.WithIndexLockE(func() error {
		rec, err := state.LoadRecord(entry.Name)
		if err != nil {
			return err
		}
		if rec == nil {
			return fmt.Errorf("squad %s has no state file", entry.Name)
		}
		if opts.clear {
			rec.Context = nil
		}
		for _, k := range opts.unsetKey {
			delete(rec.Context, k)
		}
		if len(setMap) > 0 {
			if rec.Context == nil {
				rec.Context = make(map[string]string, len(setMap))
			}
			for k, v := range setMap {
				rec.Context[k] = v
			}
		}
		if len(rec.Context) == 0 {
			rec.Context = nil // honour omitempty
		}
		updated = rec.Context
		return state.SaveRecord(rec)
	})
	if lockErr != nil {
		return fmt.Errorf("set-context: %w", lockErr)
	}

	return writeJSON(cmd, map[string]any{
		"name":    entry.Name,
		"context": updated,
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
			return nil, fmt.Errorf("set-context: --set %q: expected KEY=VALUE", raw)
		}
		key := strings.TrimSpace(raw[:idx])
		if key == "" {
			return nil, fmt.Errorf("set-context: --set %q: key must be non-empty", raw)
		}
		out[key] = raw[idx+1:]
	}
	return out, nil
}
