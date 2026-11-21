package cli

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
)

// reset installs the squad's APK set (its own copy, which deploy keeps under
// the cache root) again after clearing. Without it on disk it says why
// on-demand modules won't install until the next deploy.
func TestResetAPKSet(t *testing.T) {
	set := filepath.Join(t.TempDir(), "s.apks")
	writeTestFile(t, set, "")
	bundleRec := func(path string) *contract.SquadRecord {
		return &contract.SquadRecord{Builds: contract.Builds{Android: &contract.AndroidBuild{APKSetPath: path}}}
	}

	cases := []struct {
		name     string
		rec      *contract.SquadRecord
		wantSet  string
		wantSkip string
	}{
		{"APK squad", &contract.SquadRecord{Builds: contract.Builds{Android: &contract.AndroidBuild{APKPath: "/a.apk"}}}, "", ""},
		{"no Android build", &contract.SquadRecord{}, "", ""},
		{"set there", bundleRec(set), set, ""},
		{"set gone", bundleRec(filepath.Join(t.TempDir(), "gone.apks")), "", "no longer exists"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotSet, skip := resetAPKSet(c.rec)
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
