package wizard

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/config"
	"github.com/peuf0u/simsquad/internal/contract"
)

const handWrittenSharedSets = `[project]
ios_scheme = "MyApp"
android_gradle_task = ":app:assembleDebug"

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

[device_sets.empty]

[[device_sets."old phones".ios.sims]]
device = "iPhone 15"
runtime = "iOS 17.5"
count = 1
`

const handWrittenLocalSets = `[project]
ios_repo = "ios"

[[device_sets.tablet.ios.sims]]
device = "iPad Air 11-inch (M3)"
runtime = "iOS 26.4"
count = 1

[[device_sets.mine.android.sims]]
device = "physical"
image = "physical"
target = "physical"
count = 1
`

// snapshotSets is what deploy and equip read of every device set: the
// effective set, whether it is local, and the simsquad.toml version under it.
func snapshotSets(t *testing.T, dir string) map[string][]any {
	t.Helper()
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string][]any{}
	for _, name := range cfg.DeviceSetNames() {
		set, _ := cfg.DeviceSet(name)
		shared, hasShared := cfg.SharedDeviceSet(name)
		out[name] = []any{set, cfg.DeviceSetIsLocal(name), shared, hasShared}
	}
	return out
}

// Opening and saving equip keeps every hand-written set with the same
// meaning: shared sets stay in simsquad.toml, local ones in
// simsquad.local.toml, and a local set keeps the shared set it replaces.
func TestEquipSaveRoundTripsDeviceSets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ProjectFile), handWrittenSharedSets)
	writeFile(t, filepath.Join(dir, config.LocalFile), handWrittenLocalSets)
	before := snapshotSets(t, dir)
	if len(before) != 4 {
		t.Fatalf("fixture has %d sets, want 4: %v", len(before), before)
	}

	state, err := loadState(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeState(dir, state, true); err != nil {
		t.Fatal(err)
	}
	if after := snapshotSets(t, dir); !reflect.DeepEqual(before, after) {
		t.Fatalf("sets changed by equip load+save:\nbefore %v\nafter  %v\n--- simsquad.toml\n%s\n--- simsquad.local.toml\n%s",
			before, after, readTestFile(t, filepath.Join(dir, config.ProjectFile)), readTestFile(t, filepath.Join(dir, config.LocalFile)))
	}
	local := readTestFile(t, filepath.Join(dir, config.LocalFile))
	if !strings.Contains(local, "mine") || strings.Contains(readTestFile(t, filepath.Join(dir, config.ProjectFile)), "mine") {
		t.Fatalf("local-only set mine moved out of simsquad.local.toml:\n%s", local)
	}
}

func TestEquipAddSpecToDeviceSet(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ProjectFile), handWrittenSharedSets)
	ipad := contract.IosSpec{Device: "iPad mini (A17 Pro)", Runtime: "iOS 26.4", Count: 1}

	// Appending to an existing set keeps its rows and the default matrix.
	if _, err := RunEquipWizard(Options{CWD: dir, AddIOS: []contract.IosSpec{ipad}, DeviceSet: "tablet"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	tablet, _ := cfg.DeviceSet("tablet")
	if len(tablet.IOS) != 2 || tablet.IOS[1] != ipad || len(tablet.Android) != 1 {
		t.Fatalf("tablet = %+v, want its rows plus the iPad mini", tablet)
	}
	if got := cfg.IOSSpecs(); len(got) != 1 || got[0].Device != "iPhone 17" {
		t.Fatalf("default matrix = %+v, want it untouched", got)
	}

	// A new name creates the set in simsquad.toml.
	pixel := contract.AndroidSpec{Device: "pixel_9", Image: "system-images;android-35;google_apis;arm64-v8a", Count: 1, Target: contract.TargetEmulator}
	res, err := RunEquipWizard(Options{CWD: dir, AddAndroid: []contract.AndroidSpec{pixel}, DeviceSet: "new-phones"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readTestFile(t, filepath.Join(dir, config.ProjectFile)), "[[device_sets.new-phones.android.sims]]") {
		t.Fatal("new set not written to simsquad.toml")
	}
	var got *DeviceSetSummary
	for i := range res.Summary.DeviceSets {
		if res.Summary.DeviceSets[i].Name == "new-phones" {
			got = &res.Summary.DeviceSets[i]
		}
	}
	if got == nil || !reflect.DeepEqual(got.Android, []contract.AndroidSpec{pixel}) || len(got.IOS) != 0 {
		t.Fatalf("summary device sets = %+v, want new-phones with the Pixel 9", res.Summary.DeviceSets)
	}
}

func TestEquipAddSpecRejectsBadDeviceSetName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ProjectFile), handWrittenSharedSets)
	_, err := RunEquipWizard(Options{CWD: dir, DeviceSet: "two words",
		AddIOS: []contract.IosSpec{{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 1}}})
	if err == nil || !strings.Contains(err.Error(), "two words") {
		t.Fatalf("err = %v, want the bad name refused", err)
	}
}

func TestEquipShowListsDeviceSets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ProjectFile), handWrittenSharedSets)
	writeFile(t, filepath.Join(dir, config.LocalFile), handWrittenLocalSets)
	res, err := RunEquipWizard(Options{CWD: dir, Show: true})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, s := range res.Summary.DeviceSets {
		names = append(names, s.Name)
		if s.Name == "tablet" && (!s.Local || len(s.IOS) != 1 || len(s.Android) != 0) {
			t.Fatalf("tablet summary = %+v, want the local set", s)
		}
	}
	if want := []string{"empty", "mine", "old phones", "tablet"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("summary sets = %v, want %v", names, want)
	}
}
