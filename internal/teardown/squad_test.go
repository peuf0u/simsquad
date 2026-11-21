package teardown

import (
	"errors"
	"os"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

// A deploy whose final merge lands while dismiss is still tearing devices
// down must find the record gone (and tear down its own devices) rather
// than save into a record dismiss deletes a moment later, which would leave
// the deploy's new devices running untracked.
func TestSquadRemovesRecordBeforeTearingDownDevices(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	dev := contract.Device{Platform: contract.PlatformIOS, UDID: "ios-1", Status: contract.StatusReady}
	if err := state.SaveRecord(&contract.SquadRecord{Name: "s", Devices: []contract.Device{dev}}); err != nil {
		t.Fatal(err)
	}
	if err := state.RegisterSquad(state.RegisterSquadInput{Name: "s", StateFile: util.StateFileFor("s")}); err != nil {
		t.Fatal(err)
	}
	var mergeErr error
	var tornDown []string
	prev := tearDownDevice
	t.Cleanup(func() { tearDownDevice = prev })
	tearDownDevice = func(d contract.Device) error {
		tornDown = append(tornDown, d.UDID)
		_, mergeErr = state.UpdateRecord("s", func(*contract.SquadRecord) error { return nil })
		return nil
	}

	if err := Squad("s", nil); err != nil {
		t.Fatal(err)
	}
	if len(tornDown) != 1 || tornDown[0] != "ios-1" {
		t.Fatalf("torn down %v, want the record's device", tornDown)
	}
	if !errors.Is(mergeErr, state.ErrNoRecord) {
		t.Fatalf("merge during teardown: %v, want ErrNoRecord", mergeErr)
	}
	if rec, _ := state.LoadRecord("s"); rec != nil {
		t.Fatalf("record left: %+v", rec)
	}
	if _, ok, _ := state.FindSquad("s"); ok {
		t.Fatal("squad still registered")
	}
}

// Review round 4 (#38): deploy keeps each squad's own APK set under the
// cache root; dismissing the squad removes it.
func TestSquadRemovesTheSquadsAPKSet(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	set := util.SquadAPKSetPath("s")
	rec := &contract.SquadRecord{Name: "s", Builds: contract.Builds{Android: &contract.AndroidBuild{APKSetPath: set}}}
	if err := state.SaveRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := state.RegisterSquad(state.RegisterSquadInput{Name: "s", StateFile: util.StateFileFor("s")}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(set, []byte("set"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Squad("s", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(set); !os.IsNotExist(err) {
		t.Fatalf("stat the squad's APK set: %v, want it removed", err)
	}
}
