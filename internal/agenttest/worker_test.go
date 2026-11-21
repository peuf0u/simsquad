package agenttest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/peuf0u/simsquad/internal/contract"
)

// runShellWorker supervises `sh -c script` in dir. The worker's working
// directory is the test's, so scripts name worker files by absolute path,
// as real workers do.
func runShellWorker(t *testing.T, dir, script string) string {
	t.Helper()
	status, err := RunWorker(context.Background(), Worker{
		Dir:     dir,
		Timeout: 10 * time.Second,
		Command: []string{"sh", "-c", script},
	})
	if err != nil {
		t.Fatalf("RunWorker: %v", err)
	}
	return status
}

// writeResultScript is a worker script that writes a result.json into dir.
func writeResultScript(dir string) string {
	return "echo '{}' > '" + filepath.Join(dir, contract.ResultFileName) + "'"
}

// statusFile returns the worker's status file content, or "" when absent.
func statusFile(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, StatusFile))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatalf("read status file: %v", err)
	}
	return string(b)
}

func TestRunWorkerCleanExitWithResultIsOK(t *testing.T) {
	dir := t.TempDir()
	if got := runShellWorker(t, dir, writeResultScript(dir)); got != StatusOK {
		t.Errorf("status = %q, want %q", got, StatusOK)
	}
	if got := statusFile(t, dir); got != "" {
		t.Errorf("status file = %q, want none after a clean exit with a result", got)
	}
}

func TestRunWorkerCleanExitWithoutResultIsError(t *testing.T) {
	dir := t.TempDir()
	if got := runShellWorker(t, dir, "echo done"); got != "error: no result.json" {
		t.Errorf("status = %q, want %q", got, "error: no result.json")
	}
	if got := statusFile(t, dir); got != "error: no result.json\n" {
		t.Errorf("status file = %q", got)
	}
}

func TestRunWorkerUsageLimitInLogTail(t *testing.T) {
	cases := map[string]string{
		"claude code":        "Claude AI usage limit reached|1767225600",
		"claude code 5-hour": "5-hour limit reached ∙ resets 3pm",
		"claude code weekly": "Opus weekly limit reached ∙ resets Oct 9, 10am",
		"codex":              "ERROR: You've hit your usage limit. Upgrade to Pro or try again later.",
		"codex curly quote":  "You’ve hit your usage limit.",
	}
	for name, msg := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			// Plenty of earlier output: only the tail is read.
			script := "i=0; while [ $i -lt 2000 ]; do echo working on step $i; i=$((i+1)); done; " +
				"printf '%s\\n' '" + strings.ReplaceAll(msg, "'", `'\''`) + "'; exit 1"
			if got := runShellWorker(t, dir, script); got != StatusUsageLimit {
				t.Errorf("status = %q, want %q", got, StatusUsageLimit)
			}
			if got := statusFile(t, dir); got != StatusUsageLimit+"\n" {
				t.Errorf("status file = %q", got)
			}
		})
	}
}

func TestRunWorkerUsageLimitOnlyInTail(t *testing.T) {
	dir := t.TempDir()
	// The message scrolled far out of the tail: some later failure ended
	// the worker, so the exit code is the honest reason.
	script := "echo 'usage limit reached'; i=0; while [ $i -lt 2000 ]; do echo later output line $i; i=$((i+1)); done; exit 1"
	if got := runShellWorker(t, dir, script); got != "error: worker exited 1" {
		t.Errorf("status = %q, want error: worker exited 1", got)
	}
}

func TestRunWorkerOtherFailureKeepsExitCode(t *testing.T) {
	dir := t.TempDir()
	got := runShellWorker(t, dir, "echo 'API key sk-secret rejected' >&2; exit 1")
	if got != "error: worker exited 1" {
		t.Errorf("status = %q, want error: worker exited 1", got)
	}
	// Only fixed reason text is recorded: log lines can hold secrets.
	if s := statusFile(t, dir); strings.Contains(s, "secret") || s != "error: worker exited 1\n" {
		t.Errorf("status file = %q", s)
	}
}
