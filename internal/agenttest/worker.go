// RunWorker supervises one headless worker (`claude -p …`, `codex exec …`)
// under a hard deadline: the timebox is a kill enforced by code, not a
// request the model may overrun. It is the Go port of simsquad-pilot's
// scripts/run_worker.py.

package agenttest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// Worker outcomes. StatusOK is reported on stdout only; the other values are
// also written to the worker's status file, which the report step reads and
// which overrides even a valid result.json.
const (
	StatusOK      = "ok"
	StatusTimeout = "blocked: timeout"
)

// StatusFile is the worker-dir file holding a blocked/error outcome.
const StatusFile = "status"

// LogFile is the worker-dir file that receives the worker's stdout and
// stderr. simsquad's own stdout is reserved for the {status} JSON.
const LogFile = "worker.log"

// Worker describes one supervised worker run.
type Worker struct {
	Dir      string        // worker dir; created if missing
	Timeout  time.Duration // hard deadline, measured from start
	Command  []string      // argv; Command[0] is looked up on PATH
	Progress io.Writer     // human progress lines (stderr); may be nil
}

// RunWorker starts w.Command and waits for it. It returns the outcome
// status; an error means the worker could not be supervised at all (bad
// arguments, unstartable command, unwritable dir, ctx cancelled), not that
// the worker failed: a failed or timed-out worker is a recorded outcome.
func RunWorker(ctx context.Context, w Worker) (string, error) {
	if len(w.Command) == 0 {
		return "", errors.New("no worker command given after --")
	}
	if w.Timeout <= 0 {
		return "", errors.New("--timeout must be positive")
	}
	if err := os.MkdirAll(w.Dir, 0o755); err != nil {
		return "", fmt.Errorf("create worker dir: %w", err)
	}
	// A status file overrides even a valid result.json, so a line left by an
	// earlier attempt would make a successful retry read as blocked/error.
	if err := os.Remove(filepath.Join(w.Dir, StatusFile)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("clear old worker status: %w", err)
	}
	logf, err := os.OpenFile(filepath.Join(w.Dir, LogFile), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return "", fmt.Errorf("open worker log: %w", err)
	}
	defer func() { _ = logf.Close() }()

	cmd := exec.Command(w.Command[0], w.Command[1:]...)
	// Stdin stays nil, i.e. /dev/null: `codex exec` waits forever on an open
	// input, and a headless worker has nobody to talk to anyway.
	cmd.Stdin = nil
	// Log to a real file, not a pipe: with a pipe, Wait would also block on
	// any grandchild still holding the write end.
	cmd.Stdout = logf
	cmd.Stderr = logf
	// Own process group, so a kill reaches every idb/simctl/adb process the
	// worker started, not just its top process.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	started := time.Now()
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start worker: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	timer := time.NewTimer(w.Timeout)
	defer timer.Stop()

	select {
	case err := <-done:
		if err == nil {
			return StatusOK, nil
		}
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return "", fmt.Errorf("wait for worker: %w", err)
		}
		status := fmt.Sprintf("error: worker exited %d", exitCode(exitErr))
		w.progress("%s", status)
		return status, writeStatus(w.Dir, status)
	case <-timer.C:
		killGroup(cmd.Process.Pid, done)
		w.progress("worker killed after %ds (timebox %ds)",
			int(time.Since(started).Seconds()), int(w.Timeout.Seconds()))
		return StatusTimeout, writeStatus(w.Dir, StatusTimeout)
	case <-ctx.Done():
		// simsquad itself is being interrupted: don't leave the worker
		// running unsupervised. No status is recorded; the run is aborted.
		killGroup(cmd.Process.Pid, done)
		return "", ctx.Err()
	}
}

// termGrace is how long the worker gets to exit after SIGTERM before the
// group is sent SIGKILL (pilot's TERM_GRACE_SECONDS).
const termGrace = 5 * time.Second

// killGroup sends SIGTERM to the worker's process group, waits up to
// termGrace for the worker to exit, then sends SIGKILL to the group
// regardless, so children that outlive the leader or ignore SIGTERM die too.
// ESRCH (group already gone) is not an error.
func killGroup(pgid int, done <-chan error) {
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	select {
	case <-done:
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	case <-time.After(termGrace):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
		<-done // reap the leader
	}
}

// exitCode reports a signal death as the negative signal number, matching
// pilot (Python's returncode), since ExitCode() collapses every signal to -1.
func exitCode(err *exec.ExitError) int {
	if ws, ok := err.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return -int(ws.Signal())
	}
	return err.ExitCode()
}

func (w Worker) progress(format string, args ...any) {
	if w.Progress != nil {
		_, _ = fmt.Fprintf(w.Progress, "simsquad run worker: "+format+"\n", args...)
	}
}

// writeStatus records a blocked/error outcome as one line in the worker's
// status file, the format pilot's aggregate step reads.
func writeStatus(dir, status string) error {
	if err := os.WriteFile(filepath.Join(dir, StatusFile), []byte(status+"\n"), 0o644); err != nil {
		return fmt.Errorf("write worker status: %w", err)
	}
	return nil
}
