package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/agenttest"
)

type reportOut struct {
	Verdict    string         `json:"verdict"`
	ReportJSON string         `json:"report_json"`
	ReportMD   string         `json:"report_md"`
	Counts     map[string]int `json:"counts"`
}

// copyRunFixture copies testdata/report/<name> into a fresh temp dir, since
// `run report` writes report.json and report.md into the run dir.
func copyRunFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata/report", name)
	dst := filepath.Join(t.TempDir(), "run")
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, path)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
	if err != nil {
		t.Fatalf("copy fixture %s: %v", name, err)
	}
	return dst
}

// runReport executes `simsquad run report <dir>` through the real root
// command and returns the decoded stdout JSON and the exit code.
func runReport(t *testing.T, dir string) (reportOut, int) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"run", "report", dir})
	err := root.Execute()
	code := 0
	if err != nil {
		var ee *ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run report: unexpected error %v", err)
		}
		code = ee.Code
	}
	var got reportOut
	if jerr := json.Unmarshal(out.Bytes(), &got); jerr != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", jerr, out.String())
	}
	return got, code
}

// reportDevice is the subset of a report.json device row the tests inspect.
type reportDevice struct {
	ID               string   `json:"id"`
	WorkerStatus     string   `json:"worker_status"`
	WorkerReason     string   `json:"worker_reason"`
	ValidationErrors []string `json:"validation_errors"`
	Findings         []struct {
		Evidence []string `json:"evidence"`
	} `json:"findings"`
}

type reportDoc struct {
	FeatureTitle string `json:"feature_title"`
	Source       string `json:"source"`
	Summary      struct {
		Verdict string         `json:"verdict"`
		Reason  string         `json:"reason"`
		Counts  map[string]int `json:"counts"`
	} `json:"summary"`
	Squad struct {
		Name string            `json:"name"`
		Env  map[string]string `json:"env"`
	} `json:"squad"`
	Matrix []struct {
		Scenario string            `json:"scenario"`
		Results  map[string]string `json:"results"`
	} `json:"matrix"`
	Devices []reportDevice `json:"devices"`
}

func loadReport(t *testing.T, path string) reportDoc {
	t.Helper()
	var r reportDoc
	if err := json.Unmarshal([]byte(readFile(t, path)), &r); err != nil {
		t.Fatalf("report.json: %v", err)
	}
	return r
}

func (r reportDoc) device(t *testing.T, id string) reportDevice {
	t.Helper()
	for _, d := range r.Devices {
		if d.ID == id {
			return d
		}
	}
	t.Fatalf("report has no device %s", id)
	return reportDevice{}
}

// TestRunReportPipeline ports pilot's test_aggregate_validate_render: one
// worker with a passed scenario and a high bug, one killed at the timebox.
func TestRunReportPipeline(t *testing.T) {
	dir := copyRunFixture(t, "pipeline")
	got, code := runReport(t, dir)
	if got.Verdict != "failed" || code != 1 {
		t.Fatalf("got verdict %q exit %d, want failed / 1", got.Verdict, code)
	}
	if got.ReportJSON != filepath.Join(dir, "report.json") || got.ReportMD != filepath.Join(dir, "report.md") {
		t.Errorf("report paths = %q, %q", got.ReportJSON, got.ReportMD)
	}
	if got.Counts["devices"] != 2 || got.Counts["bugs"] != 1 {
		t.Errorf("counts = %v, want devices 2, bugs 1", got.Counts)
	}

	rep := loadReport(t, got.ReportJSON)
	if rep.FeatureTitle != "Login with magic link" || rep.Source != "https://github.com/acme/app/issues/456" {
		t.Errorf("title/source = %q / %q", rep.FeatureTitle, rep.Source)
	}
	if rep.Squad.Env["PERSONA"] != "alice | admin" {
		t.Errorf("env not carried as-is: %v", rep.Squad.Env)
	}
	if ok := rep.device(t, "ABC-123"); ok.WorkerStatus != "ok" ||
		ok.Findings[0].Evidence[0] != "workers/ABC-123/screenshots/step-001.png" {
		t.Errorf("ABC-123 = %+v, want ok with evidence rewritten under workers/ABC-123/", ok)
	}
	if bl := rep.device(t, "DEF-456"); bl.WorkerStatus != "blocked" || bl.WorkerReason != "blocked: timeout" {
		t.Errorf("DEF-456 = %+v, want blocked: timeout", bl)
	}
	want := map[string]map[string]string{
		"App launches":             {"ABC-123": "passed", "DEF-456": "blocked"},
		"Explore magic link login": {"ABC-123": "passed", "DEF-456": "blocked"},
	}
	if len(rep.Matrix) != len(want) {
		t.Fatalf("matrix = %+v", rep.Matrix)
	}
	for _, row := range rep.Matrix {
		for id, st := range want[row.Scenario] {
			if row.Results[id] != st {
				t.Errorf("matrix[%s][%s] = %q, want %q", row.Scenario, id, row.Results[id], st)
			}
		}
	}

	md := readFile(t, got.ReportMD)
	for _, fragment := range []string{
		"# Login with magic link — ❌ FAILED",
		"[https://github.com/acme/app/issues/456](https://github.com/acme/app/issues/456)",
		"[high] Magic link stays in browser",
		"blocked: timeout",
		"![evidence](workers/ABC-123/screenshots/step-001.png)",
		"| `PERSONA` | alice \\| admin |",
	} {
		if !strings.Contains(md, fragment) {
			t.Errorf("report.md missing %q:\n%s", fragment, md)
		}
	}
	// Non-English text survives verbatim, not as \u escapes.
	if raw := readFile(t, got.ReportJSON); !strings.Contains(raw, `"target": "Přihlásit se"`) {
		t.Errorf("report.json lost the verbatim Czech label:\n%s", raw)
	}
}

var updateGolden = flag.Bool("update", false, "rewrite run report golden files")

var finishedAtRE = regexp.MustCompile(`\d{4}-\d\d-\d\dT\d\d:\d\d:\d\dZ`)

// TestRunReportGolden compares the pipeline report with reviewed golden
// files (testdata/report/pipeline.report.{json,md}); finished_at is the
// only field that varies and is normalised. Rewrite with -update.
func TestRunReportGolden(t *testing.T) {
	dir := copyRunFixture(t, "pipeline")
	got, _ := runReport(t, dir)
	for path, golden := range map[string]string{
		got.ReportJSON: "testdata/report/pipeline.report.json",
		got.ReportMD:   "testdata/report/pipeline.report.md",
	} {
		// Timestamps from the fixture are kept; only "now" is replaced.
		body := readFile(t, path)
		body = finishedAtRE.ReplaceAllStringFunc(body, func(ts string) string {
			if strings.HasPrefix(ts, "2026-06-12") {
				return ts
			}
			return "FINISHED_AT"
		})
		if *updateGolden {
			if err := os.WriteFile(golden, []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if want := readFile(t, golden); body != want {
			t.Errorf("%s differs from golden %s:\n--- got\n%s\n--- want\n%s", filepath.Base(path), golden, body, want)
		}
	}
}

// TestRunReportValidatesAgainstSchema checks report.json against the
// embedded report schema, and ports pilot's test_report_count_mismatch:
// counts that disagree with the devices fail the semantic check.
func TestRunReportValidatesAgainstSchema(t *testing.T) {
	dir := copyRunFixture(t, "pipeline")
	got, _ := runReport(t, dir)
	raw := []byte(readFile(t, got.ReportJSON))
	if errs := agenttest.ValidateReport(raw); len(errs) != 0 {
		t.Fatalf("report.json is invalid: %v", errs)
	}

	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	doc["summary"].(map[string]any)["counts"].(map[string]any)["bugs"] = 5
	bad, _ := json.Marshal(doc)
	if errs := agenttest.ValidateReport(bad); len(errs) != 1 || !strings.Contains(errs[0], "$.summary.counts") {
		t.Errorf("count mismatch: errors = %v, want one $.summary.counts error", errs)
	}

	delete(doc, "feature_title")
	doc["matrix"].([]any)[0].(map[string]any)["results"].(map[string]any)["ABC-123"] = "maybe"
	bad, _ = json.Marshal(doc)
	all := strings.Join(agenttest.ValidateReport(bad), "\n")
	for _, w := range []string{"feature_title", "$.matrix[0].results.ABC-123"} {
		if !strings.Contains(all, w) {
			t.Errorf("schema errors missing %q:\n%s", w, all)
		}
	}
}

// passingResult is a valid result for device id: every scenario of the
// pipeline fixture passed, the exploration completed, no findings.
func passingResult(id string) string {
	return `{
  "device": {"platform": "ios", "id": "` + id + `"},
  "scenarios": [{"name": "App launches", "status": "passed", "observation": "Home screen shows"}],
  "exploration": {"status": "completed", "actions": []},
  "findings": []
}`
}

func writeWorkerFile(t *testing.T, runDir, id, name, content string) {
	t.Helper()
	dir := filepath.Join(runDir, "workers", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func removeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
}

// TestRunReportVerdicts covers every verdict rule, each case a mutation of
// the pipeline fixture. Every case still writes report.json, which marks
// the run finished.
func TestRunReportVerdicts(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(t *testing.T, dir string)
		verdict string
		code    int
		reason  string
	}{
		{
			name: "all passed",
			mutate: func(t *testing.T, dir string) {
				writeWorkerFile(t, dir, "ABC-123", "result.json", passingResult("ABC-123"))
				removeFile(t, filepath.Join(dir, "workers/DEF-456/status"))
				writeWorkerFile(t, dir, "DEF-456", "result.json", passingResult("DEF-456"))
			},
			verdict: "passed", code: 0,
		},
		{
			name: "one failed scenario, no bugs",
			mutate: func(t *testing.T, dir string) {
				writeWorkerFile(t, dir, "ABC-123", "result.json", passingResult("ABC-123"))
				removeFile(t, filepath.Join(dir, "workers/DEF-456/status"))
				writeWorkerFile(t, dir, "DEF-456", "result.json",
					strings.Replace(passingResult("DEF-456"), `"status": "passed"`, `"status": "failed"`, 1))
			},
			verdict: "failed", code: 1,
		},
		{
			name: "one bug, all scenarios passed",
			mutate: func(t *testing.T, dir string) {
				removeFile(t, filepath.Join(dir, "workers/DEF-456/status"))
				writeWorkerFile(t, dir, "DEF-456", "result.json", passingResult("DEF-456"))
			},
			verdict: "failed", code: 1,
		},
		{
			// pilot test_all_workers_dead_is_infra
			name: "every worker blocked or errored",
			mutate: func(t *testing.T, dir string) {
				removeFile(t, filepath.Join(dir, "workers/ABC-123/result.json"))
				writeWorkerFile(t, dir, "ABC-123", "status", "error: invalid result\n")
			},
			verdict: "infra", code: 2, reason: "every worker was blocked or errored",
		},
		{
			name: "every worker exited with an error",
			mutate: func(t *testing.T, dir string) {
				writeWorkerFile(t, dir, "ABC-123", "status", "error: worker exited 3\n")
				writeWorkerFile(t, dir, "DEF-456", "status", "error: worker exited -9\n")
			},
			verdict: "infra", code: 2, reason: "every worker was blocked or errored",
		},
		{
			name:    "no worker results",
			mutate:  func(t *testing.T, dir string) { removeFile(t, filepath.Join(dir, "workers")) },
			verdict: "infra", code: 2, reason: "no worker results",
		},
		{
			name:    "deploy.json missing",
			mutate:  func(t *testing.T, dir string) { removeFile(t, filepath.Join(dir, "deploy.json")) },
			verdict: "infra", code: 2, reason: "deploy failed",
		},
		{
			name: "deploy.json unparseable",
			mutate: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "deploy.json"), []byte("build failed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			verdict: "infra", code: 2, reason: "deploy failed",
		},
		{
			name: "no device ready",
			mutate: func(t *testing.T, dir string) {
				p := filepath.Join(dir, "deploy.json")
				raw := strings.ReplaceAll(readFile(t, p), `"status": "ready"`, `"status": "error"`)
				if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			verdict: "infra", code: 2, reason: "no ready device",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := copyRunFixture(t, "pipeline")
			tc.mutate(t, dir)
			got, code := runReport(t, dir)
			if got.Verdict != tc.verdict || code != tc.code {
				t.Fatalf("got verdict %q exit %d, want %s / %d", got.Verdict, code, tc.verdict, tc.code)
			}
			rep := loadReport(t, filepath.Join(dir, "report.json"))
			if rep.Summary.Verdict != tc.verdict || !strings.Contains(rep.Summary.Reason, tc.reason) {
				t.Errorf("report summary = %+v, want %s with reason containing %q", rep.Summary, tc.verdict, tc.reason)
			}
			if _, err := os.Stat(filepath.Join(dir, "report.md")); err != nil {
				t.Errorf("report.md not written: %v", err)
			}
		})
	}
}

// TestRunReportListsUndeployedDevices: a device deploy couldn't get ready
// had no worker; it is listed, not counted, and the ready devices decide.
func TestRunReportListsUndeployedDevices(t *testing.T) {
	dir := copyRunFixture(t, "pipeline")
	p := filepath.Join(dir, "deploy.json")
	raw := strings.Replace(readFile(t, p), `"status": "ready",
      "ready_at": "2026-06-12T16:58:30Z",`, `"status": "error",
      "error_message": "simctl install failed",`, 1)
	if err := os.WriteFile(p, []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWorkerFile(t, dir, "ABC-123", "result.json", passingResult("ABC-123"))

	got, code := runReport(t, dir)
	if got.Verdict != "passed" || code != 0 || got.Counts["devices"] != 1 {
		t.Fatalf("got verdict %q exit %d counts %v, want passed / 0 with 1 device", got.Verdict, code, got.Counts)
	}
	if md := readFile(t, got.ReportMD); !strings.Contains(md, "simctl install failed") {
		t.Errorf("report.md doesn't list the undeployed device:\n%s", md)
	}
}

// TestRunReportRecordsInvalidResult: a worker whose result.json fails
// validation is recorded as an errored worker with the validator's errors,
// and the rest of the report is unaffected.
func TestRunReportRecordsInvalidResult(t *testing.T) {
	dir := copyRunFixture(t, "pipeline")
	removeFile(t, filepath.Join(dir, "workers/DEF-456/status"))
	writeWorkerFile(t, dir, "DEF-456", "result.json", `{"device": {"platform": "ios", "id": "DEF-456"}, "scenarios": [`)

	got, code := runReport(t, dir)
	if got.Verdict != "failed" || code != 1 {
		t.Fatalf("got verdict %q exit %d, want failed / 1", got.Verdict, code)
	}
	rep := loadReport(t, got.ReportJSON)
	bad := rep.device(t, "DEF-456")
	if bad.WorkerStatus != "error" || bad.WorkerReason != "error: invalid result" ||
		len(bad.ValidationErrors) == 0 || !strings.Contains(bad.ValidationErrors[0], "cannot parse result.json") {
		t.Errorf("DEF-456 = %+v, want error: invalid result with the parse error", bad)
	}
	if ok := rep.device(t, "ABC-123"); ok.WorkerStatus != "ok" || len(ok.Findings) != 1 {
		t.Errorf("ABC-123 = %+v, want ok with its bug", ok)
	}
	if md := readFile(t, got.ReportMD); !strings.Contains(md, "error: invalid result") {
		t.Errorf("report.md doesn't show the invalid result:\n%s", md)
	}
}
