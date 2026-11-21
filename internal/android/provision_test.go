package android

import (
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

// The squad's record can vanish while its deploy builds (a concurrent
// deploy's failed-create cleanup or whole reset removes it). The emulator
// port reservation must still land in a registered record — recreated from
// the deploy's seed — or other deploys would be handed the same ports.
func TestReservePortsRecreatesMissingRecord(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seed := func() *contract.SquadRecord {
		return &contract.SquadRecord{Name: "s", CreatedAt: "t0", Context: map[string]string{"user": "bob"}}
	}
	slots, ports := reserveEmulatorSlots(emulatorContext{
		Name:  "s",
		Specs: []contract.AndroidSpec{{Device: "pixel_7", Image: "img", Count: 1}},
		Seed:  seed,
	})
	name := util.SimName("s", "android", 0)
	if len(slots) != 1 || ports[name] == 0 {
		t.Fatalf("slots %+v ports %v, want one reserved slot", slots, ports)
	}
	rec, err := state.LoadRecord("s")
	if err != nil || rec == nil || rec.Ports[name] != ports[name] || rec.Context["user"] != "bob" {
		t.Fatalf("record = %+v err %v, want the seed with the reservation", rec, err)
	}
	if _, ok, _ := state.FindSquad("s"); !ok {
		t.Fatal("recreated squad not registered, so the allocator can't see its ports")
	}
}
