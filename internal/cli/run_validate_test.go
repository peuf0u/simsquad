package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

type validateOut struct {
	Valid  bool     `json:"valid"`
	Errors []string `json:"errors"`
}

// runValidate executes `simsquad run validate <dir>` through the real root
// command and returns the decoded stdout JSON and the exit code.
func runValidate(t *testing.T, dir string) (validateOut, int) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs([]string{"run", "validate", dir})
	err := root.Execute()
	code := 0
	if err != nil {
		var ee *ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("run validate: unexpected error %v", err)
		}
		code = ee.Code
	}
	var got validateOut
	if jerr := json.Unmarshal(out.Bytes(), &got); jerr != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", jerr, out.String())
	}
	return got, code
}

// TestRunValidateRejectsInvalidResults runs the golden fixtures ported from
// pilot's tests/test_validate.py (smoke checks rewritten as scenario
// results) plus the evidence-rule cases. Each fixture dir is a worker dir;
// want lists fragments that must each appear in some error message.
func TestRunValidateRejectsInvalidResults(t *testing.T) {
	cases := []struct {
		fixture string
		want    []string
		notWant []string
	}{
		{
			// pilot test_result_structural_errors
			fixture: "structural",
			want:    []string{"$.scenarios[0].status", "one of", "extra_key"},
			// Semantic checks only run on structurally valid input.
			notWant: []string{"does not exist", "requires"},
		},
		{
			// pilot test_result_semantic_errors
			fixture: "semantic",
			want: []string{
				"$.findings[0]: bug finding requires severity",
				"$.findings[0]: bug finding requires non-empty evidence",
				"$.exploration.actions[0].screenshot: file does not exist: screenshots/ghost.png",
			},
		},
		{
			fixture: "blocked-scenario-without-evidence",
			want:    []string{"$.scenarios[1]: blocked scenario requires non-empty evidence"},
		},
		{
			fixture: "scenario-evidence-missing",
			want:    []string{"$.scenarios[0].evidence[0]: file does not exist: screenshots/missing.png"},
		},
		{
			fixture: "question-without-evidence",
			want:    []string{"$.findings[2]: question finding requires non-empty evidence"},
		},
		{
			// "blocked" is a scenario status now, not a finding type.
			fixture: "finding-type-blocked",
			want:    []string{"$.findings[1].type"},
		},
		{
			fixture: "evidence-outside-worker-dir",
			want:    []string{"$.findings[0].evidence[0]: path is outside the worker folder"},
		},
		{
			fixture: "missing-required",
			want:    []string{"scenarios", "findings", "$.device", "id"},
		},
		{
			fixture: "unparseable",
			want:    []string{"cannot parse result.json"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			got, code := runValidate(t, filepath.Join("testdata/validate", tc.fixture))
			if got.Valid || code != 1 {
				t.Fatalf("got valid=%v exit %d, want invalid with exit 1", got.Valid, code)
			}
			all := strings.Join(got.Errors, "\n")
			for _, w := range tc.want {
				if !strings.Contains(all, w) {
					t.Errorf("errors missing %q:\n%s", w, all)
				}
			}
			for _, nw := range tc.notWant {
				if strings.Contains(all, nw) {
					t.Errorf("errors unexpectedly contain %q:\n%s", nw, all)
				}
			}
		})
	}
}

func TestRunValidateReportsMissingResultAsInvalid(t *testing.T) {
	got, code := runValidate(t, t.TempDir())
	if got.Valid || code != 1 || len(got.Errors) != 1 ||
		!strings.Contains(got.Errors[0], "cannot read result.json") {
		t.Fatalf("got %+v exit %d, want one 'cannot read result.json' error and exit 1", got, code)
	}
}

// TestRunValidateAcceptsGoodResults covers a run with an @explore scenario
// and a scripted-only run, whose result has no exploration section.
func TestRunValidateAcceptsGoodResults(t *testing.T) {
	for _, fixture := range []string{"good", "good-without-exploration"} {
		t.Run(fixture, func(t *testing.T) {
			got, code := runValidate(t, filepath.Join("testdata/validate", fixture))
			if code != 0 || !got.Valid || got.Errors == nil || len(got.Errors) != 0 {
				t.Fatalf("got %+v exit %d, want valid with errors [] and exit 0", got, code)
			}
		})
	}
}
