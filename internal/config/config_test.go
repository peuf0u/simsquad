package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
)

func loadTOML(t *testing.T, body string) *Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ProjectFile), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func TestContext(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		toml       string
		want       map[string]string
		wantLegacy bool
	}{
		{"none", "[project]\n", nil, false},
		{"context", "[context]\nPERSONA = \"alice\"\n", map[string]string{"PERSONA": "alice"}, false},
		{"legacy env", "[env]\nPERSONA = \"alice\"\n", map[string]string{"PERSONA": "alice"}, true},
		{"context wins over env", "[context]\nPERSONA = \"bob\"\n[env]\nPERSONA = \"alice\"\n", map[string]string{"PERSONA": "bob"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, legacy := loadTOML(t, c.toml).Context()
			if !reflect.DeepEqual(got, c.want) || legacy != c.wantLegacy {
				t.Fatalf("Context() = %v, %v; want %v, %v", got, legacy, c.want, c.wantLegacy)
			}
		})
	}
}

func loadLayers(t *testing.T, project, local string) *Config {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ProjectFile), []byte(project), 0o644); err != nil {
		t.Fatal(err)
	}
	if local != "" {
		if err := os.WriteFile(filepath.Join(dir, LocalFile), []byte(local), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cfg, err := Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

const sharedSets = `
[[ios.sims]]
device = "iPhone 17"
runtime = "iOS 26.4"
count = 1

[[device_sets.tablet.ios.sims]]
device = "iPad Pro 13-inch (M5)"
runtime = "iOS 26.4"
count = 2

[[device_sets.tablet.android.sims]]
device = "pixel_tablet"
image = "system-images;android-34;google_apis;arm64-v8a"
count = 1

[[device_sets.old.ios.sims]]
device = "iPhone 15"
runtime = "iOS 17.5"
count = 1
`

func TestDeviceSetResolvesBothPlatforms(t *testing.T) {
	t.Parallel()
	cfg := loadLayers(t, sharedSets, "")
	set, ok := cfg.DeviceSet("tablet")
	if !ok {
		t.Fatal("DeviceSet(tablet) not found")
	}
	wantIOS := []contract.IosSpec{{Device: "iPad Pro 13-inch (M5)", Runtime: "iOS 26.4", Count: 2}}
	wantAndroid := []contract.AndroidSpec{{Device: "pixel_tablet", Image: "system-images;android-34;google_apis;arm64-v8a", Count: 1, Target: contract.TargetEmulator}}
	if set.Name != "tablet" || !reflect.DeepEqual(set.IOS, wantIOS) || !reflect.DeepEqual(set.Android, wantAndroid) {
		t.Fatalf("DeviceSet(tablet) = %+v, want ios %+v android %+v", set, wantIOS, wantAndroid)
	}
	// The default matrix is not touched by a set.
	if got := cfg.IOSSpecs(); len(got) != 1 || got[0].Device != "iPhone 17" {
		t.Fatalf("IOSSpecs() = %+v, want the default matrix only", got)
	}
	if got := cfg.DeviceSetNames(); !reflect.DeepEqual(got, []string{"old", "tablet"}) {
		t.Fatalf("DeviceSetNames() = %v, want [old tablet]", got)
	}
}

func TestDeviceSetUnknown(t *testing.T) {
	t.Parallel()
	cfg := loadLayers(t, sharedSets, "")
	if set, ok := cfg.DeviceSet("phone"); ok {
		t.Fatalf("DeviceSet(phone) = %+v, want not found", set)
	}
	if names := loadLayers(t, "[project]\n", "").DeviceSetNames(); len(names) != 0 {
		t.Fatalf("DeviceSetNames() without sets = %v, want none", names)
	}
}

// A local set replaces the shared set of the same name wholesale: its iOS
// rows don't merge with the shared set's Android rows.
func TestDeviceSetLocalReplacesShared(t *testing.T) {
	t.Parallel()
	local := `
[[device_sets.tablet.ios.sims]]
device = "iPad Air 11-inch (M3)"
runtime = "iOS 26.4"
count = 1

[[device_sets.mine.android.sims]]
target = "physical"
count = 1
`
	cfg := loadLayers(t, sharedSets, local)
	set, ok := cfg.DeviceSet("tablet")
	if !ok {
		t.Fatal("DeviceSet(tablet) not found")
	}
	wantIOS := []contract.IosSpec{{Device: "iPad Air 11-inch (M3)", Runtime: "iOS 26.4", Count: 1}}
	if !reflect.DeepEqual(set.IOS, wantIOS) || len(set.Android) != 0 {
		t.Fatalf("DeviceSet(tablet) = %+v, want only the local iOS row", set)
	}
	if !cfg.DeviceSetIsLocal("tablet") || !cfg.DeviceSetIsLocal("mine") || cfg.DeviceSetIsLocal("old") {
		t.Fatal("DeviceSetIsLocal: want tablet and mine local, old shared")
	}
	shared, ok := cfg.SharedDeviceSet("tablet")
	if !ok || len(shared.Android) != 1 || len(shared.IOS) != 1 || shared.IOS[0].Count != 2 {
		t.Fatalf("SharedDeviceSet(tablet) = %+v, %v; want the simsquad.toml version", shared, ok)
	}
	if got := cfg.DeviceSetNames(); !reflect.DeepEqual(got, []string{"mine", "old", "tablet"}) {
		t.Fatalf("DeviceSetNames() = %v", got)
	}
}

func TestSecretsMergeAcrossLayers(t *testing.T) {
	t.Parallel()
	cfg := loadLayers(t,
		"[context]\nuser = \"alice\"\n\n[secrets]\napi_key = \"shared\"\npin = \"0000\"\n",
		"[secrets]\npin = \"1234\"\npassword = \"hunter2\"\n")
	want := map[string]string{"api_key": "shared", "pin": "1234", "password": "hunter2"}
	if got := cfg.Secrets(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Secrets() = %v, want %v", got, want)
	}
	if got := cfg.CommittedSecretKeys(); !reflect.DeepEqual(got, []string{"api_key", "pin"}) {
		t.Fatalf("CommittedSecretKeys() = %v, want [api_key pin]", got)
	}
	// [secrets] stays out of the plain context.
	if ctx, _ := cfg.Context(); !reflect.DeepEqual(ctx, map[string]string{"user": "alice"}) {
		t.Fatalf("Context() = %v, want only user", ctx)
	}
}

func TestSecretsAbsent(t *testing.T) {
	t.Parallel()
	cfg := loadLayers(t, "[project]\n", "[secrets]\npassword = \"hunter2\"\n")
	if keys := cfg.CommittedSecretKeys(); keys != nil {
		t.Fatalf("CommittedSecretKeys() = %v, want none when only the local file has [secrets]", keys)
	}
	if got := loadLayers(t, "[project]\n", "").Secrets(); got != nil {
		t.Fatalf("Secrets() = %v, want nil", got)
	}
}

func TestCommittedSecretsWarningNamesKeysNotValues(t *testing.T) {
	t.Parallel()
	got := CommittedSecretsWarning([]string{"api_key", "pin"})
	for _, want := range []string{"api_key", "pin", "simsquad.toml", "simsquad.local.toml"} {
		if !strings.Contains(got, want) {
			t.Errorf("warning %q does not mention %q", got, want)
		}
	}
}

// ContextLayers splits [context] by file, reading [env] from both files
// only when Context does (no [context] in either).
func TestContextLayers(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, project, local string
		shared, own          map[string]string
	}{
		{"both", "[context]\nuser = \"alice\"\napi = \"staging\"\n", "[context]\napi = \"local\"\npassword = \"hunter2\"\n",
			map[string]string{"user": "alice", "api": "staging"}, map[string]string{"api": "local", "password": "hunter2"}},
		{"local only", "[project]\n", "[context]\npassword = \"hunter2\"\n",
			nil, map[string]string{"password": "hunter2"}},
		{"legacy env", "[env]\nuser = \"alice\"\n", "[env]\npassword = \"hunter2\"\n",
			map[string]string{"user": "alice"}, map[string]string{"password": "hunter2"}},
		{"context wins over env", "[env]\nuser = \"alice\"\n", "[context]\npassword = \"hunter2\"\n",
			nil, map[string]string{"password": "hunter2"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			shared, own := loadLayers(t, c.project, c.local).ContextLayers()
			if !reflect.DeepEqual(shared, c.shared) || !reflect.DeepEqual(own, c.own) {
				t.Fatalf("ContextLayers() = %v, %v; want %v, %v", shared, own, c.shared, c.own)
			}
		})
	}
}
