package wizard

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/discover"
)

func TestRenderProjectTOMLGolden(t *testing.T) {
	t.Parallel()

	state := wizardState{
		iosScheme:  "MyApp",
		gradleTask: ":app:assembleDevDebug",
		env: map[string]string{
			"user": "alice",
			"api":  "staging",
		},
		iosSpecs: []contract.IosSpec{
			{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 2},
			{Device: "iPhone 16 Pro", Runtime: "iOS 26.1", Count: 1},
		},
		androidSpec: []contract.AndroidSpec{
			{
				Device: "pixel_7",
				Image:  "system-images;android-34;google_apis;arm64-v8a",
				Count:  1,
				Target: contract.TargetEmulator,
			},
			{Device: "physical", Image: "physical", Count: 1, Target: contract.TargetPhysical},
		},
	}

	// Env keys are emitted in alphabetised order so this golden stays stable.
	want := `[project]
ios_scheme = "MyApp"
android_gradle_task = ":app:assembleDevDebug"

[env]
api = "staging"
user = "alice"

[[ios.sims]]
device = "iPhone 17"
runtime = "iOS 26.4"
count = 2

[[ios.sims]]
device = "iPhone 16 Pro"
runtime = "iOS 26.1"
count = 1

[[android.sims]]
device = "pixel_7"
image = "system-images;android-34;google_apis;arm64-v8a"
count = 1
target = "emulator"

[[android.sims]]
device = "physical"
image = "physical"
count = 1
target = "physical"
`
	got, err := renderProjectTOML(state)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("renderProjectTOML() =\n%s\nwant:\n%s", got, want)
	}
}

// TestWriteStateFailsOnUnencodableAgentTable: an [agent] table that can't
// be encoded fails equip instead of being silently dropped from the file.
func TestWriteStateFailsOnUnencodableAgentTable(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	state := wizardState{projectAgent: map[string]any{"bad": func() {}}}
	if _, err := writeState(dir, state, true); err == nil || !strings.Contains(err.Error(), "[agent]") {
		t.Fatalf("writeState err = %v, want an [agent] encode error", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "simsquad.toml")); err == nil {
		t.Error("simsquad.toml written without its [agent] table")
	}
}

func TestRunEquipWizardShowLoadsExistingConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "simsquad.toml"), `[project]
ios_scheme = "MyApp"
android_gradle_task = ":app:assembleDebug"

[[ios.sims]]
device = "iPhone 17"
runtime = "iOS 26.4"
count = 1
`)
	writeFile(t, filepath.Join(dir, "simsquad.local.toml"), `[project]
ios_repo = "../myapp-ios"
android_repo = "../myapp-android"
`)

	res, err := RunEquipWizard(Options{CWD: dir, Show: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.Wrote {
		t.Fatal("RunEquipWizard(show) wrote files")
	}
	if res.Summary.ConfigDir != dir {
		t.Fatalf("ConfigDir = %q, want %q", res.Summary.ConfigDir, dir)
	}
	if got := res.Summary.Project["ios_scheme"]; got != "MyApp" {
		t.Fatalf("ios_scheme = %q", got)
	}
	wantIOS := []contract.IosSpec{{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 1}}
	if !reflect.DeepEqual(res.Summary.IOS, wantIOS) {
		t.Fatalf("IOS = %+v, want %+v", res.Summary.IOS, wantIOS)
	}
}

func TestRunEquipWizardLoadsEnvSection(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "simsquad.toml"), `[project]
ios_scheme = "App"
android_gradle_task = ":app:assembleDebug"

[env]
user = "alice"
api = "staging"
`)
	res, err := RunEquipWizard(Options{CWD: dir, Show: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"user": "alice", "api": "staging"}
	if !reflect.DeepEqual(res.Summary.Env, want) {
		t.Fatalf("Summary.Env = %+v, want %+v", res.Summary.Env, want)
	}
}

func TestRunEquipWizardAppendFlagsRewriteConfig(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "simsquad.toml"), `[project]
ios_scheme = "MyApp"
android_gradle_task = ":app:assembleDebug"
`)

	res, err := RunEquipWizard(Options{
		CWD: dir,
		AddIOS: []contract.IosSpec{
			{Device: "iPhone 16 Pro", Runtime: "iOS 26.1", Count: 2},
		},
		AddAndroid: []contract.AndroidSpec{
			{
				Device: "pixel_7",
				Image:  "system-images;android-34;google_apis;arm64-v8a",
				Count:  1,
				Target: contract.TargetEmulator,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Wrote {
		t.Fatal("RunEquipWizard(add flags) did not write files")
	}
	data, err := os.ReadFile(filepath.Join(dir, "simsquad.toml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`device = "iPhone 16 Pro"`,
		`runtime = "iOS 26.1"`,
		`image = "system-images;android-34;google_apis;arm64-v8a"`,
	} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("simsquad.toml missing %q:\n%s", want, string(data))
		}
	}
}

func TestPreferredAndroidImagesPutsDottedAPILast(t *testing.T) {
	t.Parallel()

	got := preferredAndroidImages([]discover.AndroidImage{
		{Identifier: "system-images;android-36.1;google_apis_playstore;arm64-v8a"},
		{Identifier: "system-images;android-34;google_apis;arm64-v8a"},
	})
	if got[0].Identifier != "system-images;android-34;google_apis;arm64-v8a" {
		t.Fatalf("first image = %q", got[0].Identifier)
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureGitignoreEntryIsIdempotent(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), ".gitignore")
	if err := os.WriteFile(path, []byte("bin/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureGitignoreEntry(path, "simsquad.local.toml"); err != nil {
		t.Fatal(err)
	}
	if err := ensureGitignoreEntry(path, "simsquad.local.toml"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "bin/\nsimsquad.local.toml\n"
	if string(data) != want {
		t.Fatalf(".gitignore = %q, want %q", string(data), want)
	}
}

func TestRunEquipWizardPreservesAgentTables(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "simsquad.toml"), `[project]
ios_scheme = "MyApp"

[agent]
worker_model = "team-model"
test_squad = "qa-shared"
`)
	writeFile(t, filepath.Join(dir, "simsquad.local.toml"), `[project]
ios_repo = "."

[agent]
worker_model = "my-model"
`)

	if _, err := RunEquipWizard(Options{
		CWD:    dir,
		AddIOS: []contract.IosSpec{{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 1}},
	}); err != nil {
		t.Fatal(err)
	}

	project := readTestFile(t, filepath.Join(dir, "simsquad.toml"))
	for _, want := range []string{"[agent]", `worker_model = "team-model"`, `test_squad = "qa-shared"`, `device = "iPhone 17"`} {
		if !strings.Contains(project, want) {
			t.Fatalf("simsquad.toml missing %q:\n%s", want, project)
		}
	}
	if strings.Contains(project, "my-model") {
		t.Fatalf("local [agent] leaked into simsquad.toml:\n%s", project)
	}
	local := readTestFile(t, filepath.Join(dir, "simsquad.local.toml"))
	if !strings.Contains(local, `worker_model = "my-model"`) || strings.Contains(local, "team-model") {
		t.Fatalf("simsquad.local.toml [agent] not preserved as-is:\n%s", local)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}
