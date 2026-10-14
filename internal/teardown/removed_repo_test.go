package teardown

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

func TestMissingRepo(t *testing.T) {
	t.Parallel()

	live := t.TempDir()
	gone := filepath.Join(t.TempDir(), "removed-worktree")

	cases := []struct {
		name   string
		builds contract.Builds
		want   string
	}{
		{"no builds", contract.Builds{}, ""},
		{"no repo recorded (pre-0.5.0)", contract.Builds{IOS: &contract.IosBuild{AppPath: "/x.app"}}, ""},
		{"ios repo exists", contract.Builds{IOS: &contract.IosBuild{Repo: live}}, ""},
		{"ios repo gone", contract.Builds{IOS: &contract.IosBuild{Repo: gone}}, gone},
		{"android repo gone", contract.Builds{
			IOS:     &contract.IosBuild{Repo: live},
			Android: &contract.AndroidBuild{Repo: gone},
		}, gone},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := MissingRepo(contract.SquadRecord{Builds: c.builds}); got != c.want {
				t.Fatalf("MissingRepo = %q, want %q", got, c.want)
			}
		})
	}
}

// A squad built from a worktree that has since been removed is dismissed
// along with its derived data; squads whose repo is there, or that predate
// recorded repos, stay.
func TestDismissRemovedRepos(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())

	live := t.TempDir()
	gone := filepath.Join(t.TempDir(), "app-BT-1234")
	derived := util.IOSDerivedDataDir(gone)
	if err := os.MkdirAll(derived, 0o755); err != nil {
		t.Fatal(err)
	}

	seed := func(name string, builds contract.Builds) {
		t.Helper()
		rec := &contract.SquadRecord{Name: name, Builds: builds, Devices: []contract.Device{}}
		if err := state.SaveRecord(rec); err != nil {
			t.Fatal(err)
		}
		if err := state.RegisterSquad(state.RegisterSquadInput{Name: name, StateFile: util.StateFileFor(name)}); err != nil {
			t.Fatal(err)
		}
	}
	seed("qa-app-bt-1234", contract.Builds{IOS: &contract.IosBuild{Repo: gone}})
	seed("qa-app", contract.Builds{IOS: &contract.IosBuild{Repo: live}})
	seed("qa-old", contract.Builds{IOS: &contract.IosBuild{AppPath: "/x.app"}})

	got, err := DismissRemovedRepos(nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Name != "qa-app-bt-1234" || got[0].Repo != gone {
		t.Fatalf("dismissed = %+v, want only qa-app-bt-1234", got)
	}
	for name, want := range map[string]bool{"qa-app-bt-1234": false, "qa-app": true, "qa-old": true} {
		if _, ok, _ := state.FindSquad(name); ok != want {
			t.Errorf("squad %s registered = %v, want %v", name, ok, want)
		}
	}
	if _, err := os.Stat(derived); !os.IsNotExist(err) {
		t.Errorf("derived data %s still there (err %v)", derived, err)
	}
}
