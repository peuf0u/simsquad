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
	if got := renderProjectTOML(state); got != want {
		t.Fatalf("renderProjectTOML() =\n%s\nwant:\n%s", got, want)
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

	res, err := RunEquipWizard(WizardOptions{CWD: dir, Show: true})
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
	res, err := RunEquipWizard(WizardOptions{CWD: dir, Show: true})
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

	res, err := RunEquipWizard(WizardOptions{
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
