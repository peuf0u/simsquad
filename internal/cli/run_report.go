package cli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/agenttest"
	"github.com/peuf0u/simsquad/internal/contract"
)

// verdictExitCode maps a verdict to `run report`'s exit code.
var verdictExitCode = map[string]int{
	contract.VerdictPassed: 0,
	contract.VerdictFailed: 1,
	contract.VerdictInfra:  2,
}

// newRunReportCmd builds `simsquad run report <run-dir>`: it aggregates the
// workers' results into report.json and report.md, prints a summary and
// exits with the verdict.
func newRunReportCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "report <run-dir>",
		Short: "Aggregate a test run into report.json + report.md and decide the verdict",
		Long: "Reads run.json, deploy.json and workers/<device-id>/{result.json,status}\n" +
			"in <run-dir>, writes report.json (valid against the embedded report\n" +
			"schema) and report.md, and decides the verdict:\n\n" +
			"  passed (exit 0)  every scenario passed on every device, zero bugs\n" +
			"                   and every device deployed\n" +
			"  failed (exit 1)  a failed or blocked scenario, a bug, a lost worker or\n" +
			"                   an undeployed device\n" +
			"  infra  (exit 2)  deploy failed, no ready device, or no worker left a\n" +
			"                   usable result\n\n" +
			"Writing report.json marks the run finished.\n\n" +
			"stdout: {\"verdict\", \"report_json\", \"report_md\", \"counts\"}.\n\n" +
			"  simsquad run report .simsquad/runs/<run-id> > report-summary.json",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runDir := args[0]
			rep, err := agenttest.BuildReport(runDir, time.Now())
			if err != nil {
				return err
			}
			raw, err := marshalReport(rep)
			if err != nil {
				return err
			}
			// Self-check: a report that fails its own schema is a simsquad
			// bug, and must not mark the run finished.
			if errs := agenttest.ValidateReport(raw); len(errs) > 0 {
				return fmt.Errorf("%w: %s", agenttest.ErrInvalidReport, strings.Join(errs, "; "))
			}
			jsonPath := filepath.Join(runDir, contract.RunReportFile)
			mdPath := filepath.Join(runDir, contract.RunReportMarkdown)
			// report.md first: report.json is the "finished" marker, so it
			// only appears once the whole report is on disk.
			if err := os.WriteFile(mdPath, []byte(agenttest.RenderReport(rep)), 0o644); err != nil {
				return fmt.Errorf("write %s: %w", contract.RunReportMarkdown, err)
			}
			if err := os.WriteFile(jsonPath, raw, 0o644); err != nil {
				return fmt.Errorf("write %s: %w", contract.RunReportFile, err)
			}
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "simsquad run report: %s → %s\n", rep.Summary.Verdict, mdPath)
			if err := writeJSON(cmd, contract.ReportOutput{
				Verdict:    rep.Summary.Verdict,
				ReportJSON: jsonPath,
				ReportMD:   mdPath,
				Counts:     rep.Summary.Counts,
			}); err != nil {
				return err
			}
			if code := verdictExitCode[rep.Summary.Verdict]; code != 0 {
				return &ExitError{Code: code}
			}
			return nil
		},
	}
}

// marshalReport encodes the report like every simsquad JSON.
func marshalReport(rep *contract.Report) ([]byte, error) {
	var buf bytes.Buffer
	if err := encodeJSON(&buf, rep); err != nil {
		return nil, fmt.Errorf("encode report: %w", err)
	}
	return buf.Bytes(), nil
}
