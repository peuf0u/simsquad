package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/agenttest"
	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
)

// newRunWorkerCmd builds `simsquad run worker`: supervise one headless
// worker under a hard deadline.
func newRunWorkerCmd() *cobra.Command {
	var (
		dir     string
		timeout int
	)
	cmd := &cobra.Command{
		Use:   "worker --dir <worker-dir> --timeout <seconds> -- <command…>",
		Short: "Run one headless worker under a hard time limit",
		Long: "Supervise one headless worker (`claude -p …`, `codex exec …`) so its\n" +
			"deadline is enforced by code, not by the model.\n\n" +
			"The command runs in its own process group with its input closed; its\n" +
			"output goes to <worker-dir>/worker.log. When --timeout passes, the whole\n" +
			"group (every process the worker started) is killed and <worker-dir>/status\n" +
			"says `blocked: timeout`. A clean exit with <worker-dir>/result.json writes\n" +
			"no status file. Otherwise, when the end of worker.log holds a known harness\n" +
			"usage-limit message, it writes `error: usage limit`; else a clean exit\n" +
			"writes `error: no result.json` and a non-zero exit `error: worker exited <code>`.\n" +
			"Only these fixed texts are recorded, never log lines.\n\n" +
			"When the worker folder sits in a run folder (<run>/workers/<id>), the\n" +
			"squad named by <run>/run.json has its secret context values replaced\n" +
			"by \"<secret>\" in worker.log as it is written: a Codex worker's output\n" +
			"repeats the commands it ran and their output, a revealed secret too.\n\n" +
			"Running it again on the same folder is a retry: before the worker starts,\n" +
			"the earlier attempt's outputs (everything but prompt.md and attempts/)\n" +
			"move to <worker-dir>/attempts/<n>/, so a stale result or status is never\n" +
			"read as the retry's. `run validate` and `run report` ignore attempts/.\n\n" +
			"stdout is only {\"status\": …}: `ok`, or the status line written. The\n" +
			"command exits 0 whenever the outcome was recorded, so one bad worker\n" +
			"doesn't derail a fan-out; non-zero means simsquad itself was misused.",
		Example: "  simsquad run worker --dir .simsquad/runs/<run-id>/workers/<device-id> \\\n" +
			"    --timeout 720 -- claude -p \"$(cat prompt.md)\" > worker.json",
		Args: func(cmd *cobra.Command, args []string) error {
			if at := cmd.ArgsLenAtDash(); at > 0 {
				return fmt.Errorf("unexpected arguments before --: %v", args[:at])
			}
			if len(args) == 0 {
				return errors.New("no worker command given; pass it after --")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			status, err := agenttest.RunWorker(cmd.Context(), agenttest.Worker{
				Dir:      dir,
				Timeout:  time.Duration(timeout) * time.Second,
				Command:  args,
				Progress: cmd.ErrOrStderr(),
				Redact:   runSquadSecrets(cmd, dir),
			})
			if err != nil {
				return err
			}
			return writeJSON(cmd, contract.WorkerStatus{Status: status})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "worker directory (created if missing; receives status and worker.log)")
	cmd.Flags().IntVar(&timeout, "timeout", 0, "deadline in seconds; the worker's whole process group is killed when it passes")
	_ = cmd.MarkFlagRequired("dir")
	_ = cmd.MarkFlagRequired("timeout")
	return cmd
}

// runSquadSecrets returns the secret values of the squad the worker folder's
// run tests: <dir>/../../run.json names it, and its record holds them. A
// worker folder outside a run folder has none. When run.json names a squad
// whose record can't be read, worker.log can't be redacted, so stderr says
// so; the worker still runs.
func runSquadSecrets(cmd *cobra.Command, dir string) []string {
	raw, err := os.ReadFile(filepath.Join(dir, "..", "..", contract.RunRecordFile))
	if err != nil {
		return nil
	}
	var run contract.RunRecord
	if err := json.Unmarshal(raw, &run); err != nil || run.SquadName == "" {
		return nil
	}
	rec, err := state.LoadRecord(run.SquadName)
	if err != nil {
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "simsquad run worker: can't read squad %s (%v); worker.log keeps any secret the worker prints\n", run.SquadName, err)
		return nil
	}
	if rec == nil {
		return nil
	}
	return rec.SecretValues()
}
