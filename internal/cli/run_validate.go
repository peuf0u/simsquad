package cli

import (
	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/agenttest"
	"github.com/peuf0u/simsquad/internal/contract"
)

// newRunValidateCmd builds `simsquad run validate <worker-dir>`: it checks
// one worker's result.json against the embedded schema and the evidence
// rule, prints {valid, errors} and exits 1 when invalid.
func newRunValidateCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "validate <worker-dir>",
		Short: "Validate one worker's result.json",
		Long: "Checks <worker-dir>/result.json against the embedded result schema and\n" +
			"the evidence rule: every bug, question and blocked scenario has\n" +
			"screenshot evidence, and every referenced screenshot exists in the\n" +
			"worker folder.\n\n" +
			"stdout: {\"valid\": bool, \"errors\": [string]}. Exit 1 when invalid.\n\n" +
			"  simsquad run validate <worker-dir> > validation.json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			_, errs := agenttest.ValidateWorkerDir(args[0])
			out := contract.Validation{Valid: len(errs) == 0, Errors: errs}
			if out.Errors == nil {
				out.Errors = []string{}
			}
			if err := writeJSON(cmd, out); err != nil {
				return err
			}
			if !out.Valid {
				return &ExitError{Code: 1}
			}
			return nil
		},
	}
}
