package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

// seedSquadInCache writes a SquadRecord + registry entry under the current
// SIMSQUAD_CACHE_DIR with one ready iOS device.
func seedSquadInCache(t *testing.T, name string) {
	t.Helper()
	rec := &contract.SquadRecord{
		Name:      name,
		CreatedAt: util.NowISO(),
		Devices: []contract.Device{
			{
				Platform:   contract.PlatformIOS,
				Name:       "simsquad-foo-ios-0",
				UDID:       "ios-udid-0",
				BundleID:   "com.example.app",
				DeviceType: "iPhone 17",
				Runtime:    "iOS 26.4",
				Status:     contract.StatusReady,
			},
		},
	}
	if err := state.SaveRecord(rec); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	if err := state.RegisterSquad(state.RegisterSquadInput{
		Name:      name,
		StateFile: util.StateFileFor(name),
	}); err != nil {
		t.Fatalf("RegisterSquad: %v", err)
	}
}

func TestDevicesFiltering(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	rec := &contract.SquadRecord{
		Name:      "devices-target",
		CreatedAt: util.NowISO(),
		Devices: []contract.Device{
			{Platform: contract.PlatformIOS, Name: "ios-ready", UDID: "udid-1",
				DeviceType: "iPhone 17", Runtime: "iOS 26.4", Status: contract.StatusReady},
			{Platform: contract.PlatformIOS, Name: "ios-err", UDID: "", Status: contract.StatusError},
			{Platform: contract.PlatformAndroid, Name: "android-ready", UDID: "emulator-5554",
				DeviceProfile: "pixel_7", SystemImage: "system-images;android-34;google_apis;arm64-v8a",
				Status: contract.StatusReady},
		},
	}
	if err := state.SaveRecord(rec); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	if err := state.RegisterSquad(state.RegisterSquadInput{
		Name:      "devices-target",
		StateFile: util.StateFileFor("devices-target"),
	}); err != nil {
		t.Fatalf("RegisterSquad: %v", err)
	}

	cases := []struct {
		name    string
		args    []string
		wantLen int
	}{
		{name: "all", args: []string{"--name=devices-target"}, wantLen: 3},
		{name: "ready-only", args: []string{"--name=devices-target", "--ready"}, wantLen: 2},
		{name: "ios-only", args: []string{"--name=devices-target", "--platform=ios"}, wantLen: 2},
		{name: "android-only", args: []string{"--name=devices-target", "--platform=android"}, wantLen: 1},
		{name: "android-ready", args: []string{"--name=devices-target", "--platform=android", "--ready"}, wantLen: 1},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			cmd := newDevicesCmd()
			out := &bytes.Buffer{}
			cmd.SetOut(out)
			cmd.SetErr(&bytes.Buffer{})
			cmd.SetArgs(c.args)
			if err := cmd.Execute(); err != nil {
				t.Fatalf("devices: %v", err)
			}
			var got struct {
				Devices []contract.DeviceView `json:"devices"`
			}
			if err := json.Unmarshal(out.Bytes(), &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(got.Devices) != c.wantLen {
				t.Fatalf("devices = %d, want %d (%+v)", len(got.Devices), c.wantLen, got.Devices)
			}
		})
	}
}

func TestDevicesNoNameListsAllSquads(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadInCache(t, "alpha") // 1 ready iOS device
	seedSquadInCache(t, "bravo") // 1 ready iOS device

	cmd := newDevicesCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(nil) // no --name → whole-fleet view
	if err := cmd.Execute(); err != nil {
		t.Fatalf("devices (no name): %v", err)
	}
	var got struct {
		Squads []struct {
			Name    string                `json:"name"`
			Devices []contract.DeviceView `json:"devices"`
		} `json:"squads"`
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v\nout=%s", err, out.String())
	}
	if len(got.Squads) != 2 {
		t.Fatalf("squads = %d, want 2 (%+v)", len(got.Squads), got.Squads)
	}
	// Sorted by name, so alpha then bravo, each with its one device.
	if got.Squads[0].Name != "alpha" || got.Squads[1].Name != "bravo" {
		t.Fatalf("squad order = %q, %q; want alpha, bravo", got.Squads[0].Name, got.Squads[1].Name)
	}
	for _, s := range got.Squads {
		if len(s.Devices) != 1 {
			t.Fatalf("squad %q devices = %d, want 1", s.Name, len(s.Devices))
		}
	}
}
