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

// writeTree creates each file (path relative to dir) with its content; a
// path ending in "/" is an empty directory.
func writeTree(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// readTree returns every file under dir (relative path → content), and
// every empty directory as "<path>/" → "".
func readTree(t *testing.T, dir string) map[string]string {
	t.Helper()
	got := map[string]string{}
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		if d.IsDir() {
			if entries, _ := os.ReadDir(p); len(entries) == 0 && rel != "." {
				got[rel+"/"] = ""
			}
			return nil
		}
		b, err := os.ReadFile(p)
		got[rel] = string(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func assertTree(t *testing.T, dir string, want map[string]string) {
	t.Helper()
	got := readTree(t, dir)
	for k, v := range want {
		if g, ok := got[k]; !ok {
			t.Errorf("missing %s", k)
		} else if g != v {
			t.Errorf("%s = %q, want %q", k, g, v)
		}
	}
	for k := range got {
		if _, ok := want[k]; !ok {
			t.Errorf("unexpected %s", k)
		}
	}
}

func TestRunWorkerFirstAttemptMovesNothing(t *testing.T) {
	dir := t.TempDir()
	// What the skill prepares before the first run.
	writeTree(t, dir, map[string]string{"prompt.md": "test", "screenshots/": ""})
	runShellWorker(t, dir, writeResultScript(dir))
	assertTree(t, dir, map[string]string{
		"prompt.md":    "test",
		"screenshots/": "",
		"result.json":  "{}\n",
		"worker.log":   "",
	})
}

func TestRunWorkerRetryMovesEarlierAttempts(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"prompt.md": "test", "screenshots/": ""})
	first := "echo first; echo '# first' > '" + filepath.Join(dir, "log.md") + "'; " +
		"echo png > '" + filepath.Join(dir, "screenshots", "scenario-01.png") + "'; " +
		writeResultScript(dir)
	if got := runShellWorker(t, dir, first); got != StatusOK {
		t.Fatalf("first attempt = %q", got)
	}
	writeTree(t, dir, map[string]string{"validation.json": `{"valid": true}`})

	if got := runShellWorker(t, dir, "echo second; exit 3"); got != "error: worker exited 3" {
		t.Fatalf("second attempt = %q", got)
	}
	if got := runShellWorker(t, dir, "echo third"); got != StatusNoResult {
		t.Fatalf("third attempt = %q, want %q: a retry must never read the stale result", got, StatusNoResult)
	}
	assertTree(t, dir, map[string]string{
		"prompt.md":    "test",
		"screenshots/": "",
		"worker.log":   "third\n",
		"status":       StatusNoResult + "\n",

		"attempts/1/result.json":                 "{}\n",
		"attempts/1/log.md":                      "# first\n",
		"attempts/1/worker.log":                  "first\n",
		"attempts/1/screenshots/scenario-01.png": "png\n",
		"attempts/1/validation.json":             `{"valid": true}`,

		"attempts/2/worker.log": "second\n",
		"attempts/2/status":     "error: worker exited 3\n",
	})
}

func TestRunWorkerRetryRecordsItsOwnFailure(t *testing.T) {
	dir := t.TempDir()
	if got := runShellWorker(t, dir, writeResultScript(dir)); got != StatusOK {
		t.Fatalf("first attempt = %q", got)
	}
	if got := runShellWorker(t, dir, "exit 3"); got != "error: worker exited 3" {
		t.Errorf("retry = %q, want error: worker exited 3", got)
	}
	if _, err := os.Stat(filepath.Join(dir, contract.ResultFileName)); !os.IsNotExist(err) {
		t.Errorf("the first attempt's result.json is still current (stat err %v)", err)
	}
}

func TestRunWorkerFailsWhenAttemptsIsNotAFolder(t *testing.T) {
	dir := t.TempDir()
	writeTree(t, dir, map[string]string{"attempts": "not a folder", "result.json": "{}"})
	done := make(chan error, 1)
	go func() {
		_, err := RunWorker(context.Background(), Worker{
			Dir: dir, Timeout: 10 * time.Second, Command: []string{"true"},
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "archive earlier attempt") {
			t.Errorf("err = %v, want an archive error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWorker hung numbering attempts under a file named attempts")
	}
}

func TestRunWorkerUsageLimitOnCleanExitWithoutResult(t *testing.T) {
	dir := t.TempDir()
	if got := runShellWorker(t, dir, "echo 'Claude usage limit reached. Your limit will reset at 3pm'"); got != StatusUsageLimit {
		t.Errorf("status = %q, want %q", got, StatusUsageLimit)
	}
	if got := statusFile(t, dir); got != StatusUsageLimit+"\n" {
		t.Errorf("status file = %q", got)
	}
}

func TestRunWorkerCleanExitWithResultIgnoresUsageLimitText(t *testing.T) {
	dir := t.TempDir()
	script := "echo 'usage limit reached'; " + writeResultScript(dir)
	if got := runShellWorker(t, dir, script); got != StatusOK {
		t.Errorf("status = %q, want %q: a written result wins", got, StatusOK)
	}
}

// Issue #37: a Codex worker's output carries the commands it ran and their
// output, so a `simsquad devices --reveal` puts the squad's secrets in
// worker.log unless run worker redacts them as it writes the log.
func TestRunWorkerRedactsSecretsInLog(t *testing.T) {
	dir := t.TempDir()
	// The secret arrives split across writes, on stdout and on stderr.
	script := "printf 'login hun'; sleep 0.1; printf 'ter2 ok\n'; echo 'pin 4711' >&2; printf 'tail hunter2'"
	status, err := RunWorker(context.Background(), Worker{
		Dir:     dir,
		Timeout: 10 * time.Second,
		Command: []string{"sh", "-c", script},
		Redact:  []string{"hunter2", "4711"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusNoResult {
		t.Errorf("status = %q, want %q", status, StatusNoResult)
	}
	b, err := os.ReadFile(filepath.Join(dir, LogFile))
	if err != nil {
		t.Fatal(err)
	}
	log := string(b)
	if strings.Contains(log, "hunter2") || strings.Contains(log, "4711") {
		t.Fatalf("worker.log shows a secret:\n%s", log)
	}
	for _, want := range []string{"login <secret> ok\n", "pin <secret>\n", "tail <secret>"} {
		if !strings.Contains(log, want) {
			t.Errorf("worker.log lacks %q:\n%s", want, log)
		}
	}
}

// A worker that leaves a background process holding its output open still
// finishes once its own process exits.
func TestRunWorkerRedactingDoesNotWaitForGrandchildren(t *testing.T) {
	dir := t.TempDir()
	start := time.Now()
	status, err := RunWorker(context.Background(), Worker{
		Dir:     dir,
		Timeout: 30 * time.Second,
		Command: []string{"sh", "-c", "sleep 20 & " + writeResultScript(dir)},
		Redact:  []string{"hunter2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status != StatusOK {
		t.Errorf("status = %q, want ok", status)
	}
	if took := time.Since(start); took > 10*time.Second {
		t.Fatalf("run worker waited %s for a grandchild", took)
	}
}

func TestLineRedactor(t *testing.T) {
	var out strings.Builder
	r := newLineRedactor(&out, []string{"cd", "abcdef"})
	for _, chunk := range []string{"xxab", "cdef yy cd\nzz a", "bcdef"} {
		if _, err := r.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if want := "xx<secret> yy <secret>\nzz <secret>"; out.String() != want {
		t.Fatalf("redacted = %q, want %q", out.String(), want)
	}
}
