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

// worker.log is private to the user: it can hold secret values the
// redactor skips (short name-rule values) or can't recognise (a value the
// worker's shell quoted).
func TestRunWorkerWritesLogPrivate(t *testing.T) {
	dir := t.TempDir()
	if got := runShellWorker(t, dir, "echo hello"); got != StatusNoResult {
		t.Fatalf("status = %q, want %q", got, StatusNoResult)
	}
	fi, err := os.Stat(filepath.Join(dir, LogFile))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("worker.log mode = %o, want 600", perm)
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

// A secret that a write splits right after a newline it contains, or that
// straddles the cut of an over-long unfinished line, is still caught whole.
func TestLineRedactorSplitSecrets(t *testing.T) {
	long := strings.Repeat("x", maxPartialLine-3)
	cases := map[string]struct {
		secret string
		chunks []string
	}{
		"newline inside":   {"ab\ncd", []string{"pre ab\n", "cd post\n"}},
		"partial-line cut": {"hunter2", []string{long + "hun", "ter2 end"}},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			var out strings.Builder
			r := newLineRedactor(&out, []string{c.secret})
			for _, chunk := range c.chunks {
				if _, err := r.Write([]byte(chunk)); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Flush(); err != nil {
				t.Fatal(err)
			}
			got := out.String()
			if strings.Contains(got, c.secret) || !strings.Contains(got, "<secret>") {
				t.Fatalf("redacted output still holds the secret or lacks the mask (len %d): %q", len(got), got[max(0, len(got)-40):])
			}
			want := strings.ReplaceAll(strings.Join(c.chunks, ""), c.secret, "<secret>")
			if got != want {
				t.Fatalf("redacted output differs from the whole text redacted at once")
			}
		})
	}
}

// simsquad prints a secret JSON-escaped (devices --reveal), and a worker
// log repeats it so: the escaped form is redacted too.
func TestLineRedactorJSONEscapedForm(t *testing.T) {
	var out strings.Builder
	r := newLineRedactor(&out, []string{`pa"ss\1<x>`})
	if _, err := r.Write([]byte(`"password": "pa\"ss\\1<x>"` + "\n" + `html "pa\"ss\\1\u003cx\u003e"` + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	if want := "\"password\": \"<secret>\"\nhtml \"<secret>\"\n"; out.String() != want {
		t.Fatalf("redacted = %q, want %q", out.String(), want)
	}
}

// redactWrites feeds chunks to a fresh redactor and returns what it wrote.
func redactWrites(t *testing.T, secrets []string, chunks ...string) string {
	t.Helper()
	var out strings.Builder
	r := newLineRedactor(&out, secrets)
	for _, c := range chunks {
		if _, err := r.Write([]byte(c)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	return out.String()
}

// A secret holding a shorter secret, split by a write, still goes whole:
// the held-back tail must not be masked before the longer one completes.
// Nor is a mask masked again when a secret appears inside "<secret>".
func TestLineRedactorNestedSecrets(t *testing.T) {
	cases := []struct {
		name    string
		secrets []string
		chunks  []string
		want    string
	}{
		{"prefix", []string{"hunter2xyz", "hunter2"}, []string{"foo\nlogin hunter2x", "yz\n"}, "foo\nlogin <secret>\n"},
		{"inside", []string{"Xab12345Y", "ab12345"}, []string{"Xab12345", "Y\n"}, "<secret>\n"},
		{"mask holds a secret", []string{"secret", "hunter22"}, []string{"client_secret=secret\n", "and hunter22\n"}, "client_<secret>=<secret>\nand <secret>\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redactWrites(t, c.secrets, c.chunks...); got != c.want {
				t.Fatalf("redacted = %q, want %q", got, c.want)
			}
		})
	}
}

// Secrets that overlap in the text are masked as one: the union of their
// matches, one mask per contiguous run, so no part of either is left.
func TestLineRedactorOverlappingSecrets(t *testing.T) {
	cases := []struct {
		name    string
		secrets []string
		chunks  []string
		want    string
	}{
		{"shorter first", []string{"xyz12", "12345678"}, []string{"token xyz12345678 end\n"}, "token <secret> end\n"},
		{"longer first", []string{"abcab", "cabx"}, []string{"abcabx cabx\n"}, "<secret> <secret>\n"},
		{"adjacent", []string{"abc", "def"}, []string{"x abcdef y\n"}, "x <secret> y\n"},
		{"chain split by a write", []string{"aab", "bcc", "ccd"}, []string{"1 aabc", "cd 2\n"}, "1 <secret> 2\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := redactWrites(t, c.secrets, c.chunks...); got != c.want {
				t.Fatalf("redacted = %q, want %q", got, c.want)
			}
		})
	}
}

// Wherever the worker's writes split its output, the log is the same as
// for one write, and it holds no secret.
func TestLineRedactorSplitInvariant(t *testing.T) {
	text := "foo hunter2xyz bar hunter2\nXab12345Y ab12345 abcabx cabx\n" +
		`"pa\"ss\\1" pa"ss\1 client_secret=secret ab` + "\ncd end\ntoken xyz12345678 aabccd abcdef\ntail hunter2x"
	sets := [][]string{
		{"hunter2xyz", "hunter2"},
		{"Xab12345Y", "ab12345"},
		{"abcab", "cabx"},
		{"secret", `pa"ss\1`},
		{"ab\ncd", "hunter2"},
		{"xyz12", "12345678"},
		{"aab", "bcc", "ccd"},
		{"abc", "def", "cabx"},
	}
	for _, secrets := range sets {
		whole := redactWrites(t, secrets, text)
		masked := strings.ReplaceAll(whole, "<secret>", "\x00")
		for _, s := range secrets {
			for _, form := range append([]string{s}, func() []string {
				var out []string
				for _, b := range jsonEscaped(s) {
					out = append(out, string(b))
				}
				return out
			}()...) {
				if strings.Contains(masked, form) {
					t.Errorf("%q: one write leaks %q: %q", secrets, form, whole)
				}
			}
		}
		for i := 1; i < len(text); i++ {
			if got := redactWrites(t, secrets, text[:i], text[i:]); got != whole {
				t.Fatalf("%q split at %d: %q, want %q", secrets, i, got, whole)
			}
			for j := i + 1; j < len(text); j++ {
				if got := redactWrites(t, secrets, text[:i], text[i:j], text[j:]); got != whole {
					t.Fatalf("%q split at %d,%d: %q, want %q", secrets, i, j, got, whole)
				}
			}
		}
	}
}

// Review round 5, item 4: touching matches of one secret ("ab" in
// "ababab…") with no newline form one run that always reaches the cut.
// Held back whole, it would grow the buffer without limit; past
// maxPartialLine the run is written out masked instead, and no secret
// reaches the log.
func TestLineRedactorBoundsATilingSecretRun(t *testing.T) {
	var out strings.Builder
	r := newLineRedactor(&out, []string{"ab"})
	chunk := strings.Repeat("ab", 2048)
	for written := 0; written < 4*maxPartialLine; written += len(chunk) {
		if _, err := r.Write([]byte(chunk)); err != nil {
			t.Fatal(err)
		}
		if len(r.buf) > maxPartialLine+len(chunk) {
			t.Fatalf("buffer holds %d bytes after %d written, want at most %d",
				len(r.buf), written+len(chunk), maxPartialLine+len(chunk))
		}
	}
	if _, err := r.Write([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Write([]byte("b end\n")); err != nil {
		t.Fatal(err)
	}
	if err := r.Flush(); err != nil {
		t.Fatal(err)
	}
	got := strings.ReplaceAll(out.String(), "<secret>", "")
	if got != " end\n" || !strings.HasPrefix(out.String(), "<secret>") {
		t.Fatalf("log less its masks = %q, want only \" end\\n\" left", got)
	}
}

// Review round 6, item 1: when a run held past maxPartialLine is forced
// out, a longer secret that starts inside the run and isn't whole yet must
// still be found whole once the next write completes it: no part of it may
// reach the log under or after a mask.
func TestLineRedactorForcedRunKeepsALongerSecretWhole(t *testing.T) {
	cases := []struct {
		secrets []string
		chunks  []string
	}{
		{[]string{"ab", "bSECRETVALUE"}, []string{strings.Repeat("ab", 32763) + "abSECRETVALU", "E\n"}},
		{[]string{"aba", "abSECRETVALUE"}, []string{strings.Repeat("ab", 32763) + "abSECRETVALU", "E\n"}},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.secrets, ","), func(t *testing.T) {
			got := redactWrites(t, c.secrets, c.chunks...)
			masked := strings.ReplaceAll(got, "<secret>", "\x00")
			for _, s := range c.secrets {
				for i := 0; i+4 <= len(s); i++ {
					for j := i + 4; j <= len(s); j++ {
						if strings.Contains(masked, s[i:j]) {
							t.Fatalf("log holds %q of secret %q unmasked; it ends %q", s[i:j], s, got[max(0, len(got)-40):])
						}
					}
				}
			}
			if !strings.HasSuffix(got, "\n") || strings.Count(got, "<secret>") != 1 {
				t.Fatalf("log = …%q, want one run masked as one <secret> and the newline", got[max(0, len(got)-40):])
			}
		})
	}
}
