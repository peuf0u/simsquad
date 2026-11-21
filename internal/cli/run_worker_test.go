package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

type runWorkerOut struct {
	Status string `json:"status"`
}

// runWorker executes `simsquad run worker <args>` through the real root
// command and returns stdout, stderr and the command error.
func runWorker(t *testing.T, args ...string) (string, string, error) {
	t.Helper()
	root := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(append([]string{"run", "worker"}, args...))
	err := root.Execute()
	return out.String(), errOut.String(), err
}

func decodeWorkerStatus(t *testing.T, stdout string) string {
	t.Helper()
	var got runWorkerOut
	if err := json.Unmarshal([]byte(stdout), &got); err != nil {
		t.Fatalf("stdout is not {status} JSON: %v\n%s", err, stdout)
	}
	return got.Status
}

// writesResult is a worker shell script that writes result.json into dir by
// absolute path (a worker runs in the caller's directory, not its own).
func writesResult(dir string) string {
	return "echo '{}' > '" + filepath.Join(dir, "result.json") + "'"
}

func TestRunWorkerCleanExitWritesNoStatus(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workers", "ios-ABC")
	stdout, _, err := runWorker(t, "--dir", dir, "--timeout", "10", "--", "sh", "-c", "echo hello; "+writesResult(dir))
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if got := decodeWorkerStatus(t, stdout); got != "ok" {
		t.Errorf("status = %q, want ok", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "status")); !os.IsNotExist(err) {
		t.Errorf("status file exists after a clean exit (stat err %v)", err)
	}
}

func TestRunWorkerSuccessfulRetryClearsOldStatus(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runWorker(t, "--dir", dir, "--timeout", "10", "--", "sh", "-c", "exit 3"); err != nil {
		t.Fatalf("first attempt: %v", err)
	}
	stdout, _, err := runWorker(t, "--dir", dir, "--timeout", "10", "--", "sh", "-c", writesResult(dir))
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if got := decodeWorkerStatus(t, stdout); got != "ok" {
		t.Errorf("retry status = %q, want ok", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "status")); !os.IsNotExist(err) {
		t.Errorf("stale status file survived a successful retry (stat err %v)", err)
	}
}

func readStatusFile(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, "status"))
	if err != nil {
		t.Fatalf("read status file: %v", err)
	}
	return string(b)
}

func TestRunWorkerNonZeroExitRecordsError(t *testing.T) {
	dir := t.TempDir()
	stdout, _, err := runWorker(t, "--dir", dir, "--timeout", "10", "--", "sh", "-c", "exit 3")
	if err != nil {
		t.Fatalf("run worker should record the failure and succeed, got %v", err)
	}
	if got := decodeWorkerStatus(t, stdout); got != "error: worker exited 3" {
		t.Errorf("stdout status = %q", got)
	}
	if got := readStatusFile(t, dir); got != "error: worker exited 3\n" {
		t.Errorf("status file = %q", got)
	}
}

func TestRunWorkerCleanExitWithoutResultRecordsError(t *testing.T) {
	dir := t.TempDir()
	stdout, _, err := runWorker(t, "--dir", dir, "--timeout", "10", "--", "sh", "-c", "echo done")
	if err != nil {
		t.Fatalf("run worker should record the missing result and succeed, got %v", err)
	}
	if got := decodeWorkerStatus(t, stdout); got != "error: no result.json" {
		t.Errorf("stdout status = %q", got)
	}
	if got := readStatusFile(t, dir); got != "error: no result.json\n" {
		t.Errorf("status file = %q", got)
	}
}

func TestRunWorkerClosesInput(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	// `cat` copies its input until EOF; with an open stdin it would hang
	// until the deadline and come back blocked.
	stdout, _, err := runWorker(t, "--dir", dir, "--timeout", "5", "--", "sh", "-c", "cat; "+writesResult(dir))
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if got := decodeWorkerStatus(t, stdout); got != "ok" {
		t.Errorf("status = %q, want ok (input was not closed)", got)
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Errorf("worker waited on input for %s", elapsed)
	}
}

func TestRunWorkerMissingCommandIsUsageError(t *testing.T) {
	dir := t.TempDir()
	stdout, _, err := runWorker(t, "--dir", dir, "--timeout", "5", "--")
	if err == nil {
		t.Fatal("expected a usage error for an empty command")
	}
	if stdout != "" {
		t.Errorf("stdout should be empty on misuse, got %q", stdout)
	}
}

func TestRunWorkerRejectsArgsBeforeDash(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runWorker(t, "--dir", dir, "--timeout", "5", "true", "--", "true"); err == nil {
		t.Fatal("expected an error for a positional argument before --")
	}
}

func TestRunWorkerTimeoutKillsAndMarksBlocked(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	stdout, _, err := runWorker(t, "--dir", dir, "--timeout", "1", "--", "sleep", "30")
	if err != nil {
		t.Fatalf("run worker: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Errorf("worker not killed in time: took %s", elapsed)
	}
	if got := decodeWorkerStatus(t, stdout); got != "blocked: timeout" {
		t.Errorf("stdout status = %q", got)
	}
	if got := readStatusFile(t, dir); got != "blocked: timeout\n" {
		t.Errorf("status file = %q", got)
	}
}

func TestRunWorkerTimeoutKillsChildProcesses(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	script := "sleep 60 & echo $! > " + pidFile + "; sleep 30"
	if _, _, err := runWorker(t, "--dir", dir, "--timeout", "1", "--", "sh", "-c", script); err != nil {
		t.Fatalf("run worker: %v", err)
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("worker never recorded its child pid: %v", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatalf("bad pid %q: %v", b, err)
	}
	// The orphaned child is re-parented to launchd/init, which reaps it
	// shortly after it dies; allow a moment before declaring a survivor.
	deadline := time.Now().Add(3 * time.Second)
	for {
		if err := syscall.Kill(pid, 0); errors.Is(err, syscall.ESRCH) {
			return
		}
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("child process %d survived the timeout", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// run worker finds the squad from the worker folder's run.json and redacts
// that squad's secrets from worker.log, e.g. a revealed password a Codex
// worker's output repeats.
func TestRunWorkerRedactsTheSquadsSecrets(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSecretSquad(t, "sq")
	run := t.TempDir()
	writeTestFile(t, filepath.Join(run, "run.json"), `{"run_id": "r", "squad_name": "sq"}`)
	dir := filepath.Join(run, "workers", "udid-1")
	script := "echo 'exec simsquad devices --name sq --reveal'; echo '\"pin\": \"4711\", \"password\": \"hunter2\", \"user\": \"alice\"'"
	if _, _, err := runWorker(t, "--dir", dir, "--timeout", "10", "--", "sh", "-c", script); err != nil {
		t.Fatalf("run worker: %v", err)
	}
	log := readFile(t, filepath.Join(dir, "worker.log"))
	if strings.Contains(log, "4711") || strings.Contains(log, "hunter2") {
		t.Fatalf("worker.log shows a secret:\n%s", log)
	}
	if !strings.Contains(log, `"pin": "<secret>"`) || !strings.Contains(log, `"user": "alice"`) {
		t.Fatalf("worker.log lost its other text:\n%s", log)
	}
}
