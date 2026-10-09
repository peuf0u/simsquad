package cli

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// outFlag is the persistent root flag that tees a command's stdout JSON to a
// file. It exists because agent harnesses (Claude Code) gate shell `>`
// redirection behind an approval prompt even when the binary itself is
// allowed, so a skill that redirects stdout loses the JSON (issue #16).
const outFlag = "out"

// addOutFlag registers --out on root and clears a stale target before any
// verb runs, so a command that fails before writing JSON leaves no file that
// could pass for this run's result.
func addOutFlag(root *cobra.Command) {
	root.PersistentFlags().String(outFlag, "",
		"also write the command's stdout JSON to this file (created or truncated; parent dirs created)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		path := outPath(cmd)
		if path == "" {
			return nil
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("--out: clear %s: %w", path, err)
		}
		return nil
	}
}

// outPath returns the --out value seen by cmd, or "" when unset.
func outPath(cmd *cobra.Command) string {
	f := cmd.Flags().Lookup(outFlag)
	if f == nil {
		return ""
	}
	return f.Value.String()
}

// writeOutFile writes data to the --out file when one was given.
func writeOutFile(cmd *cobra.Command, data []byte) error {
	path := outPath(cmd)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("--out: create parent of %s: %w", path, err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("--out: write %s: %w", path, err)
	}
	return nil
}
