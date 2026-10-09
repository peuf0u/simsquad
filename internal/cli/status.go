package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
)

func newStatusCmd() *cobra.Command {
	var name string

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show squad registry or a single squad record",
		Args:  cobra.NoArgs,
		Long: "Without arguments, prints the registry as JSON on stdout. With --name,\n" +
			"prints the full public record for one squad.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if name == "" {
				return runStatusList(cmd)
			}
			return runStatusOne(cmd, name)
		},
	}

	cmd.Flags().StringVar(&name, "name", "", "squad to look up (omit to list all)")

	return cmd
}

func runStatusList(cmd *cobra.Command) error {
	squads, err := state.ListSquads()
	if err != nil {
		return fmt.Errorf("status: list squads: %w", err)
	}
	if squads == nil {
		squads = []contract.SquadIndexEntry{}
	}
	sort.Slice(squads, func(i, j int) bool { return squads[i].Name < squads[j].Name })
	return writeJSON(cmd, map[string]any{"squads": squads})
}

func runStatusOne(cmd *cobra.Command, name string) error {
	name = canonName(name)
	rec, err := state.LoadRecord(name)
	if err != nil {
		return fmt.Errorf("status: load record %s: %w", name, err)
	}
	if rec == nil {
		return fmt.Errorf("status: no squad named %q", name)
	}
	return writeJSON(cmd, rec.Public())
}

// writeJSON encodes v as 2-space indented JSON with a trailing newline to the
// command's stdout, and tees the same bytes to the --out file when set. Used
// by every verb that produces a JSON contract.
func writeJSON(cmd *cobra.Command, v any) error {
	var buf bytes.Buffer
	if err := encodeJSON(&buf, v); err != nil {
		return err
	}
	if _, err := cmd.OutOrStdout().Write(buf.Bytes()); err != nil {
		return err
	}
	return writeOutFile(cmd, buf.Bytes())
}

// encodeJSON writes v as every simsquad JSON is written: 2-space indent,
// trailing newline, non-ASCII and HTML characters verbatim.
func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}
