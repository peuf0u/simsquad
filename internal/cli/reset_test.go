package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/util"
)

// reset installs the squad's APK set again after clearing only when it is
// still the set this squad's deploy installed: there, written no later than
// the device's ready_at. Otherwise it says why on-demand modules won't
// install until the next deploy.
func TestResetAPKSet(t *testing.T) {
	deployed := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	set := filepath.Join(t.TempDir(), "app.apks")
	writeTestFile(t, set, "")
	setMtime := func(at time.Time) {
		t.Helper()
		if err := os.Chtimes(set, at, at); err != nil {
			t.Fatal(err)
		}
	}
	bundleRec := func(path string) *contract.SquadRecord {
		return &contract.SquadRecord{Builds: contract.Builds{Android: &contract.AndroidBuild{APKSetPath: path}}}
	}
	dev := contract.Device{Platform: contract.PlatformAndroid, ReadyAt: util.ISO(deployed)}

	cases := []struct {
		name     string
		rec      *contract.SquadRecord
		dev      contract.Device
		mtime    time.Time
		wantSet  string
		wantSkip string
	}{
		{"APK squad", &contract.SquadRecord{Builds: contract.Builds{Android: &contract.AndroidBuild{APKPath: "/a.apk"}}},
			dev, deployed, "", ""},
		{"no Android build", &contract.SquadRecord{}, dev, deployed, "", ""},
		{"set written before the deploy finished", bundleRec(set), dev, deployed.Add(-time.Minute), set, ""},
		{"set written in the deploy's second", bundleRec(set), dev, deployed.Add(500 * time.Millisecond), set, ""},
		{"set replaced by a later deploy", bundleRec(set), dev, deployed.Add(time.Hour), "", "newer"},
		{"set gone", bundleRec(filepath.Join(t.TempDir(), "gone.apks")), dev, deployed, "", "no longer exists"},
		{"no ready_at", bundleRec(set), contract.Device{Platform: contract.PlatformAndroid}, deployed, "", "ready_at"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			setMtime(c.mtime)
			gotSet, skip := resetAPKSet(c.rec, c.dev)
			if gotSet != c.wantSet {
				t.Errorf("set = %q, want %q", gotSet, c.wantSet)
			}
			if (c.wantSkip == "") != (skip == "") || !strings.Contains(skip, c.wantSkip) {
				t.Errorf("skip = %q, want it to contain %q", skip, c.wantSkip)
			}
			if skip != "" && !strings.Contains(skip, "on-demand modules won't install until the next deploy") {
				t.Errorf("skip = %q, want the consequence", skip)
			}
		})
	}
}
