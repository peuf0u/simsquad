package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
)

const equipBoth = `[project]
ios_scheme = "App"

[[ios.sims]]
device = "iPhone 17"
runtime = "iOS 26.4"
count = 1

[[android.sims]]
device = "pixel_7"
image = "system-images;android-34;google_apis;arm64-v8a"
count = 1
`

const equipIOSOnly = `[project]
ios_scheme = "App"

[[ios.sims]]
device = "iPhone 17"
runtime = "iOS 26.4"
count = 1
`

const loginFeature = `@auth
Feature: Login
  Users sign in with email and password.
  Source: https://github.com/acme/app/issues/456

  Background:
    Given the app is launched

  Scenario: Valid credentials
    When I sign in as "alice"
    Then I see the home screen

  @slow @ios
  Scenario Outline: Wrong password for <user>
    When I sign in as "<user>" with a wrong password
    Then I see "Invalid password"

    Examples:
      | user  |
      | alice |
      | bob   |

  @explore
  Scenario: Poke at the login form
    Charter: try odd inputs in the login form.
    Risks: keyboard covers the submit button.
`

// newEquippedRepo creates a temp app repo (named appName) with the given
// simsquad.toml and chdirs into it.
func newEquippedRepo(t *testing.T, appName, toml string) string {
	t.Helper()
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(base, appName)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, "simsquad.toml"), toml)
	t.Chdir(repo)
	return repo
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runRunNew(t *testing.T, args ...string) (contract.RunNewOutput, error) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"run", "new"}, args...))
	err := root.Execute()
	var got contract.RunNewOutput
	if err == nil {
		if uerr := json.Unmarshal(out.Bytes(), &got); uerr != nil {
			t.Fatalf("unmarshal: %v\nout=%s", uerr, out.String())
		}
	} else if out.Len() > 0 {
		t.Fatalf("stdout must be empty on refusal, got %s", out.String())
	}
	return got, err
}

func mustRunNew(t *testing.T, args ...string) contract.RunNewOutput {
	t.Helper()
	got, err := runRunNew(t, args...)
	if err != nil {
		t.Fatalf("run new %v: %v", args, err)
	}
	return got
}

func readRunRecord(t *testing.T, runDir string) contract.RunRecord {
	t.Helper()
	var rec contract.RunRecord
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(runDir, "run.json"))), &rec); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestRunNewCreatesRunFolderAndPrintsJSON(t *testing.T) {
	repo := newEquippedRepo(t, "MyApp", equipBoth)
	writeTestFile(t, filepath.Join(repo, "qa/features/login.feature"), loginFeature)

	got := mustRunNew(t, "qa/features/login.feature")

	if got.RunID == "" {
		t.Fatal("empty run_id")
	}
	wantDir := filepath.Join(repo, ".simsquad", "runs", got.RunID)
	if got.RunDir != wantDir {
		t.Fatalf("run_dir = %q, want %q", got.RunDir, wantDir)
	}
	if got.SquadName != "qa-myapp" || got.Fresh {
		t.Fatalf("squad = %q fresh=%v, want qa-myapp/false", got.SquadName, got.Fresh)
	}
	if strings.Join(got.Platforms, ",") != "ios,android" {
		t.Fatalf("platforms = %v", got.Platforms)
	}
	if got.DeadlineSeconds <= 0 {
		t.Fatalf("deadline_seconds = %d", got.DeadlineSeconds)
	}
	if readFile(t, filepath.Join(wantDir, "feature.feature")) != loginFeature {
		t.Fatal("feature.feature is not a verbatim copy")
	}
	rec := readRunRecord(t, wantDir)
	if rec.FeatureTitle != "Login" || rec.Source != "https://github.com/acme/app/issues/456" {
		t.Fatalf("title/source = %q / %q", rec.FeatureTitle, rec.Source)
	}
	if rec.SquadName != got.SquadName || rec.DeadlineSeconds != got.DeadlineSeconds || rec.RunID != got.RunID {
		t.Fatalf("run.json disagrees with stdout: %+v", rec)
	}
	if rec.CreatedAt == "" {
		t.Fatal("created_at missing")
	}
}

func TestRunNewRecordsExpandedScenarios(t *testing.T) {
	repo := newEquippedRepo(t, "MyApp", equipBoth)
	writeTestFile(t, filepath.Join(repo, "login.feature"), loginFeature)

	rec := readRunRecord(t, mustRunNew(t, "login.feature").RunDir)

	if len(rec.Scenarios) != 3 {
		t.Fatalf("want 3 scenarios (1 + outline x2), got %d: %+v", len(rec.Scenarios), rec.Scenarios)
	}
	first := rec.Scenarios[0]
	if first.Name != "Valid credentials" {
		t.Fatalf("first name = %q", first.Name)
	}
	wantSteps := []string{"Given the app is launched", `When I sign in as "alice"`, "Then I see the home screen"}
	if strings.Join(first.Steps, "|") != strings.Join(wantSteps, "|") {
		t.Fatalf("steps = %q, want %q", first.Steps, wantSteps)
	}
	if strings.Join(first.Tags, ",") != "@auth" {
		t.Fatalf("tags = %v, want inherited @auth", first.Tags)
	}
	if strings.Join(first.Platforms, ",") != "ios,android" {
		t.Fatalf("untagged scenario platforms = %v", first.Platforms)
	}
	bob := rec.Scenarios[2]
	if bob.Name != "Wrong password for bob" {
		t.Fatalf("outline row name = %q", bob.Name)
	}
	if bob.Steps[0] != "Given the app is launched" || bob.Steps[1] != `When I sign in as "bob" with a wrong password` {
		t.Fatalf("outline steps = %q", bob.Steps)
	}
	if strings.Join(bob.Tags, ",") != "@auth,@slow,@ios" {
		t.Fatalf("outline tags = %v", bob.Tags)
	}
	if strings.Join(bob.Platforms, ",") != "ios" {
		t.Fatalf("@ios scenario platforms = %v", bob.Platforms)
	}
	if rec.Exploration == nil {
		t.Fatal("exploration missing")
	}
	if !strings.Contains(rec.Exploration.Charter, "try odd inputs") || !strings.Contains(rec.Exploration.Charter, "Risks: keyboard") {
		t.Fatalf("charter = %q", rec.Exploration.Charter)
	}
	if rec.Exploration.Name != "Poke at the login form" {
		t.Fatalf("exploration name = %q", rec.Exploration.Name)
	}
}

func TestRunNewAcceptsUnknownTags(t *testing.T) {
	repo := newEquippedRepo(t, "app", equipBoth)
	writeTestFile(t, filepath.Join(repo, "x.feature"), `@release @persona:alice @timebox:5m
Feature: X
  @wip @model:opus
  Scenario: one
    Given something
`)
	rec := readRunRecord(t, mustRunNew(t, "x.feature").RunDir)
	if strings.Join(rec.Scenarios[0].Tags, ",") != "@release,@persona:alice,@timebox:5m,@wip,@model:opus" {
		t.Fatalf("tags = %v", rec.Scenarios[0].Tags)
	}
}

func TestRunNewRestrictsPlatformsByTags(t *testing.T) {
	repo := newEquippedRepo(t, "app", equipBoth)
	writeTestFile(t, filepath.Join(repo, "x.feature"), `@android
Feature: X
  Scenario: one
    Given something
`)
	got := mustRunNew(t, "x.feature")
	if strings.Join(got.Platforms, ",") != "android" {
		t.Fatalf("platforms = %v", got.Platforms)
	}
}

func TestRunNewRefusesUnequippedPlatform(t *testing.T) {
	repo := newEquippedRepo(t, "app", equipIOSOnly)
	writeTestFile(t, filepath.Join(repo, "x.feature"), `Feature: X
  @android
  Scenario: one
    Given something
`)
	_, err := runRunNew(t, "x.feature")
	if err == nil || !strings.Contains(err.Error(), "android") || !strings.Contains(err.Error(), "simsquad equip") {
		t.Fatalf("err = %v, want refusal naming android and simsquad equip", err)
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".simsquad")); !os.IsNotExist(statErr) {
		t.Fatal("refused run must not create .simsquad/")
	}
}

func TestRunNewRefusesWithoutEquipment(t *testing.T) {
	repo := newAppRepo(t)
	writeTestFile(t, filepath.Join(repo, "x.feature"), "Feature: X\n  Scenario: one\n    Given something\n")
	_, err := runRunNew(t, "x.feature")
	if err == nil || !strings.Contains(err.Error(), "simsquad equip") {
		t.Fatalf("err = %v, want pointer to simsquad equip", err)
	}
}

func TestRunNewRefusesMalformedGherkin(t *testing.T) {
	repo := newEquippedRepo(t, "app", equipBoth)
	writeTestFile(t, filepath.Join(repo, "bad.feature"), "Feature: X\n  Scenario: one\n    Given ok\n  Bogus line\n")
	_, err := runRunNew(t, "bad.feature")
	if err == nil || !strings.Contains(err.Error(), "(4:") {
		t.Fatalf("err = %v, want parser line 4", err)
	}
}

func TestRunNewRefusesFeatureWithoutScenarios(t *testing.T) {
	repo := newEquippedRepo(t, "app", equipBoth)
	writeTestFile(t, filepath.Join(repo, "empty.feature"), "Feature: Empty\n")
	if _, err := runRunNew(t, "empty.feature"); err == nil {
		t.Fatal("want refusal for a feature with no scenarios")
	}
}

func TestRunNewRefusesSecondRunUntilReported(t *testing.T) {
	repo := newEquippedRepo(t, "app", equipBoth)
	writeTestFile(t, filepath.Join(repo, "x.feature"), "Feature: X\n  Scenario: one\n    Given something\n")

	first := mustRunNew(t, "x.feature")
	_, err := runRunNew(t, "x.feature")
	if err == nil || !strings.Contains(err.Error(), first.RunID) {
		t.Fatalf("err = %v, want refusal naming %s", err, first.RunID)
	}

	writeTestFile(t, filepath.Join(first.RunDir, "report.json"), "{}")
	second := mustRunNew(t, "x.feature")
	if second.RunID == first.RunID {
		t.Fatal("second run reused the first run id")
	}
}

func TestRunNewFreshUsesUniqueSquadAndSkipsConcurrencyCheck(t *testing.T) {
	repo := newEquippedRepo(t, "app", equipBoth)
	writeTestFile(t, filepath.Join(repo, "x.feature"), "Feature: X\n  Scenario: one\n    Given something\n")

	mustRunNew(t, "x.feature")
	a := mustRunNew(t, "x.feature", "--fresh")
	b := mustRunNew(t, "x.feature", "--fresh")
	if !a.Fresh || !b.Fresh {
		t.Fatal("fresh flag not reported")
	}
	if a.SquadName == "qa-app" || a.SquadName == b.SquadName {
		t.Fatalf("fresh squads not unique: %q %q", a.SquadName, b.SquadName)
	}
	if !strings.HasPrefix(a.SquadName, "qa-app-") {
		t.Fatalf("fresh squad = %q, want qa-app-<suffix>", a.SquadName)
	}
	if !readRunRecord(t, a.RunDir).Fresh {
		t.Fatal("run.json fresh = false")
	}
}

func TestRunNewAgentTableOverridesSquadAndSetsWorkerModel(t *testing.T) {
	repo := newEquippedRepo(t, "app", equipBoth+`
[agent]
worker_model = "some-model-that-does-not-exist"
test_squad = "Team QA"
`)
	writeTestFile(t, filepath.Join(repo, "x.feature"), "Feature: X\n  Scenario: one\n    Given something\n")

	got := mustRunNew(t, "x.feature")
	if got.SquadName != "team-qa" {
		t.Fatalf("squad = %q, want team-qa", got.SquadName)
	}
	if got.WorkerModel != "some-model-that-does-not-exist" {
		t.Fatalf("worker_model = %q", got.WorkerModel)
	}
	if readRunRecord(t, got.RunDir).WorkerModel != got.WorkerModel {
		t.Fatal("run.json worker_model mismatch")
	}
}

func TestRunNewDeadline(t *testing.T) {
	// Worked examples of the documented formula:
	//   120s base + per scenario (60s + 30s per step) + 600s exploration,
	//   taking the platform whose worker has the most work.
	cases := []struct {
		name    string
		feature string
		want    int
	}{
		{"one step", "Feature: X\n  Scenario: a\n    Given s\n", 120 + 60 + 30},
		{"explore only", "Feature: X\n  @explore\n  Scenario: e\n    look around\n", 120 + 600},
		{"background counts per scenario", `Feature: X
  Background:
    Given b
  Scenario: a
    Given s
  Scenario: c
    Given s
    Then t
`, 120 + (60 + 2*30) + (60 + 3*30)},
		{"busiest platform wins", `Feature: X
  @ios
  Scenario: a
    Given s
  @android
  Scenario: b
    Given s
    Then t
`, 120 + 60 + 2*30},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := newEquippedRepo(t, "app", equipBoth)
			writeTestFile(t, filepath.Join(repo, "x.feature"), tc.feature)
			if got := mustRunNew(t, "x.feature", "--fresh").DeadlineSeconds; got != tc.want {
				t.Fatalf("deadline = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRunNewRequiresExactlyOneArg(t *testing.T) {
	newEquippedRepo(t, "app", equipBoth)
	_, err := runRunNew(t)
	if err == nil {
		t.Fatal("want error without a feature file")
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		t.Fatal("usage errors are plain errors, not ExitError")
	}
}
