package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/peuf0u/simsquad/internal/agenttest"
	"github.com/peuf0u/simsquad/internal/contract"
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
			"says `blocked: timeout`. A non-zero exit writes `error: worker exited <code>`.\n" +
			"A clean exit writes no status file; a status file left by an earlier\n" +
			"attempt is deleted before the worker starts.\n\n" +
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
