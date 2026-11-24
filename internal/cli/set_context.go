package cli

import (
	"errors"
	"fmt"

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
		name       string
		setVals    []string
		setSecrets []string
		unsetKey   []string
		clear      bool
	)

	cmd := &cobra.Command{
		Use:    use,
		Short:  "Mutate the context on a named squad",
		Args:   cobra.NoArgs,
		Hidden: deprecated,
		Long: "Updates the context map on a squad: free-form test context such as\n" +
			"persona, stage or test-account login. Applied in this order:\n" +
			"  --clear        wipe all keys, secrets included\n" +
			"  --unset        remove specific keys, secret or not (repeatable)\n" +
			"  --set          add or overwrite keys (repeatable, KEY=VALUE)\n" +
			"  --set-secret   add or overwrite secret keys (repeatable, KEY=VALUE)\n\n" +
			"A secret (set with --set-secret, from [secrets] or --secret at deploy,\n" +
			"or any key whose name holds password, secret or token) shows as\n" +
			"\"<secret>\" in the output; `simsquad devices --name NAME --reveal`\n" +
			"prints the real value. A secret overwritten with --set stays a secret;\n" +
			"--unset it first to make it plain context.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if deprecated {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "simsquad: set-env is deprecated; use set-context")
			}
			return runSetContext(cmd, setContextOpts{
				name:       name,
				setVals:    setVals,
				setSecrets: setSecrets,
				unsetKey:   unsetKey,
				clear:      clear,
			})
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "squad to mutate (required)")
	cmd.Flags().StringArrayVar(&setVals, "set", nil, "add or overwrite a context key: KEY=VALUE (repeatable)")
	cmd.Flags().StringArrayVar(&setSecrets, "set-secret", nil, "add or overwrite a secret context key: KEY=VALUE, shown as \"<secret>\" (repeatable)")
	cmd.Flags().StringArrayVar(&unsetKey, "unset", nil, "remove a context key, secret or not (repeatable)")
	cmd.Flags().BoolVar(&clear, "clear", false, "wipe every existing key before --unset / --set apply")
	_ = cmd.MarkFlagRequired("name")
	return cmd
}

type setContextOpts struct {
	name       string
	setVals    []string
	setSecrets []string
	unsetKey   []string
	clear      bool
}

func runSetContext(cmd *cobra.Command, opts setContextOpts) error {
	if opts.name == "" {
		return errors.New("set-context: --name is required")
	}
	if !opts.clear && len(opts.setVals) == 0 && len(opts.setSecrets) == 0 && len(opts.unsetKey) == 0 {
		return errors.New("set-context: pass --set, --set-secret, --unset, or --clear")
	}

	setMap, err := parseSetEntries("--set", opts.setVals)
	if err != nil {
		return err
	}
	secretMap, err := parseSetEntries("--set-secret", opts.setSecrets)
	if err != nil {
		return err
	}
	for k := range secretMap {
		if _, ok := setMap[k]; ok {
			return fmt.Errorf("set-context: key %q is given to both --set and --set-secret; pick one", k)
		}
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
			rec.ClearContext()
		}
		for _, k := range opts.unsetKey {
			rec.UnsetContext(k)
		}
		for k, v := range setMap {
			rec.SetContext(k, v, false)
		}
		for k, v := range secretMap {
			rec.SetContext(k, v, true)
		}
		updated = rec.MaskedContext()
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

// parseSetEntries parses `--set` / `--set-secret` KEY=VALUE flags into a
// string map. Duplicates within a single invocation are tolerated (last
// wins) since interactive shell history may repeat a key intentionally.
// Mismatched KEY=VALUE entries still error, without echoing the entry.
func parseSetEntries(flag string, entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(entries))
	for i, raw := range entries {
		key, value, err := splitContextEntry(flag, i, raw)
		if err != nil {
			return nil, fmt.Errorf("set-context: %w", err)
		}
		out[key] = value
	}
	return out, nil
}
