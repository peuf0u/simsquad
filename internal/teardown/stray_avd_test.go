package teardown

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

func mkdirs(t *testing.T, root string, names ...string) {
	t.Helper()
	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(root, n), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

// Issue #39: a simsquad-*.avd folder left without its .ini is invisible to
// `avdmanager list avd`, so sweep must find it on disk. Only untracked
// simsquad Android names are removed; a folder with its .ini is left to the
// avdmanager pass.
func TestPruneStrayAVDDirs(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	mkdirs(t, home,
		"simsquad-x-android-0.avd",
		"simsquad-kept-android-0.avd",
		"simsquad-x-android-1.avd",
		"Pixel_7.avd",
		"simsquad-x-ios-0.avd",
		"simsquad-x-android-2", // not a .avd folder
	)
	if err := os.WriteFile(filepath.Join(home, "simsquad-x-android-1.ini"), []byte("path=x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "simsquad-x-android-3.avd"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	tracked := map[string]struct{}{"simsquad-kept-android-0": {}}

	got := pruneStrayAVDDirs(home, tracked, nil)
	if !slices.Equal(got, []string{"simsquad-x-android-0"}) {
		t.Fatalf("pruned = %v, want [simsquad-x-android-0]", got)
	}
	for name, want := range map[string]bool{
		"simsquad-x-android-0.avd":    false,
		"simsquad-kept-android-0.avd": true,
		"simsquad-x-android-1.avd":    true,
		"Pixel_7.avd":                 true,
		"simsquad-x-ios-0.avd":        true,
		"simsquad-x-android-2":        true,
		"simsquad-x-android-3.avd":    true,
	} {
		if _, err := os.Stat(filepath.Join(home, name)); (err == nil) != want {
			t.Errorf("%s exists = %v, want %v", name, err == nil, want)
		}
	}
}

func TestPruneStrayAVDDirsMissingHome(t *testing.T) {
	t.Parallel()
	if got := pruneStrayAVDDirs(filepath.Join(t.TempDir(), "nope"), nil, nil); len(got) != 0 {
		t.Fatalf("pruned = %v, want none", got)
	}
}

// The folder scan runs even without avdmanager, and a device tracked by a
// squad in the registry keeps its folder.
func TestPruneAndroidScansAVDHomeWithoutAVDManager(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	// No SDK and nothing on PATH: android.AVDManager fails, so no real
	// avdmanager can touch the machine's AVDs.
	t.Setenv("ANDROID_HOME", filepath.Join(t.TempDir(), "no-sdk"))
	t.Setenv("PATH", t.TempDir())
	home := t.TempDir()
	t.Setenv("ANDROID_AVD_HOME", home)

	rec := &contract.SquadRecord{Name: "kept", Devices: []contract.Device{{
		Platform: contract.PlatformAndroid,
		Name:     "simsquad-kept-android-0",
		AVDName:  "simsquad-kept-android-0",
	}}}
	if err := state.SaveRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := state.RegisterSquad(state.RegisterSquadInput{Name: "kept", StateFile: util.StateFileFor("kept")}); err != nil {
		t.Fatal(err)
	}
	mkdirs(t, home, "simsquad-x-android-0.avd", "simsquad-kept-android-0.avd")

	tracked, err := trackedDeviceNames()
	if err != nil {
		t.Fatal(err)
	}
	got := pruneAndroid(tracked, nil)
	if !slices.Equal(got, []string{"simsquad-x-android-0"}) {
		t.Fatalf("pruned = %v, want [simsquad-x-android-0]", got)
	}
	if _, err := os.Stat(filepath.Join(home, "simsquad-kept-android-0.avd")); err != nil {
		t.Errorf("tracked folder removed: %v", err)
	}
}

// A device whose AVD name differs from its display name is still tracked by
// its AVD name.
func TestTrackedDeviceNamesIncludesAVDName(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	rec := &contract.SquadRecord{Name: "s", Devices: []contract.Device{{
		Platform: contract.PlatformAndroid,
		Name:     "Pixel display name",
		AVDName:  "simsquad-s-android-0",
	}}}
	if err := state.SaveRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := state.RegisterSquad(state.RegisterSquadInput{Name: "s", StateFile: util.StateFileFor("s")}); err != nil {
		t.Fatal(err)
	}
	tracked, err := trackedDeviceNames()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := tracked["simsquad-s-android-0"]; !ok {
		t.Errorf("tracked = %v, want it to hold the AVD name", tracked)
	}
}
