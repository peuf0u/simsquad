package runsetup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/util"
)

// writeRun creates a run folder holding run.json for the given squad and
// platforms, and a report.json when reported is true.
func writeRun(t *testing.T, runsRoot, id, squad string, platforms []string, reported bool) string {
	t.Helper()
	dir := filepath.Join(runsRoot, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	rec := contract.RunRecord{RunID: id, SquadName: squad, Platforms: platforms}
	if err := util.WriteJSON(filepath.Join(dir, contract.RunRecordFile), rec); err != nil {
		t.Fatal(err)
	}
	if reported {
		if err := os.WriteFile(filepath.Join(dir, contract.RunReportFile), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestRefuseUnfinishedComparesPlatforms(t *testing.T) {
	ios := []string{"ios"}
	android := []string{"android"}
	both := []string{"ios", "android"}

	cases := []struct {
		name string
		// other run on squad "qa-app"
		otherPlatforms []string
		otherReported  bool
		otherSquad     string
		newPlatforms   []string
		wantRefused    bool
		wantShared     string
	}{
		{name: "disjoint platforms allowed", otherPlatforms: ios, newPlatforms: android},
		{name: "same platform refused", otherPlatforms: ios, newPlatforms: ios, wantRefused: true, wantShared: "ios"},
		{name: "all equipped vs ios refused", otherPlatforms: ios, newPlatforms: both, wantRefused: true, wantShared: "ios"},
		{name: "reported run never blocks", otherPlatforms: ios, otherReported: true, newPlatforms: ios},
		{name: "legacy run without platforms refused", otherPlatforms: nil, newPlatforms: android, wantRefused: true, wantShared: "android"},
		{name: "other squad never blocks", otherPlatforms: ios, otherSquad: "qa-other", newPlatforms: ios},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runsRoot := t.TempDir()
			squad := tc.otherSquad
			if squad == "" {
				squad = "qa-app"
			}
			dir := writeRun(t, runsRoot, "20260101-000000-first", squad, tc.otherPlatforms, tc.otherReported)

			err := refuseUnfinished(runsRoot, "qa-app", tc.newPlatforms)
			if !tc.wantRefused {
				if err != nil {
					t.Fatalf("err = %v, want allowed", err)
				}
				return
			}
			if err == nil {
				t.Fatal("want refusal")
			}
			msg := err.Error()
			// The run id and its shared platforms sit together, so a
			// platform name in the temp path can't satisfy the check.
			for _, want := range []string{"run 20260101-000000-first on " + tc.wantShared + " ", "simsquad run report " + dir, "--fresh"} {
				if !strings.Contains(msg, want) {
					t.Fatalf("err = %q, want it to contain %q", msg, want)
				}
			}
		})
	}
}

func TestRefuseUnfinishedWithoutRunsFolder(t *testing.T) {
	if err := refuseUnfinished(filepath.Join(t.TempDir(), "missing"), "qa-app", []string{"ios"}); err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
}
