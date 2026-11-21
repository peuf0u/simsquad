package cli

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/util"
)

const (
	testTabletIOS     = "iPad Pro 13-inch (M5)"
	testTabletAndroid = "pixel_tablet"
)

// deployTomlWithSets is deployToml plus a tablet set (both platforms) and a
// phone set (iOS only, a different device from the default matrix).
const deployTomlWithSets = deployToml + `
[[device_sets.tablet.ios.sims]]
device = "iPad Pro 13-inch (M5)"
runtime = "iOS 26.4"
count = 1

[[device_sets.tablet.android.sims]]
device = "pixel_tablet"
image = "system-images;android-34;google_apis;arm64-v8a"
count = 1

[[device_sets.phone.ios.sims]]
device = "iPhone 16"
runtime = "iOS 26.4"
count = 1
`

func TestDeployDeviceSetDeploysTheSetAndBareRedeployReusesIt(t *testing.T) {
	newDeployRepo(t, deployTomlWithSets)
	f := installFakeDeploy(t)

	pub, code, err := deployCLI(t, "--name", "s", "--device-set", "tablet")
	if err != nil || code != 0 {
		t.Fatalf("deploy --device-set tablet: code %d err %v", code, err)
	}
	if len(f.iosProvisions) != 1 || f.iosProvisions[0].Specs[0].Device != testTabletIOS {
		t.Fatalf("iOS provision = %+v, want the tablet set's iPad", f.iosProvisions)
	}
	if len(f.androidProvisions) != 1 || f.androidProvisions[0].Specs[0].Device != testTabletAndroid {
		t.Fatalf("Android provision = %+v, want the tablet set's emulator", f.androidProvisions)
	}
	if pub.DeviceSet != "tablet" {
		t.Fatalf("stdout device_set = %q, want tablet", pub.DeviceSet)
	}
	if rec := loadTestRecord(t, "s"); rec.DeviceSet != "tablet" {
		t.Fatalf("record device_set = %q, want tablet", rec.DeviceSet)
	}

	pub, code, err = deployCLI(t, "--name", "s")
	if err != nil || code != 0 {
		t.Fatalf("bare redeploy: code %d err %v", code, err)
	}
	if !pub.Reused {
		t.Fatal("bare redeploy of a device-set squad did not reuse it")
	}
	if len(f.iosProvisions) != 2 || len(f.iosProvisions[1].ExistingDevices) != 1 ||
		len(f.androidProvisions) != 2 || len(f.androidProvisions[1].ExistingDevices) != 1 {
		t.Fatalf("redeploy provisions ios %+v android %+v, want both reused", f.iosProvisions, f.androidProvisions)
	}
	if len(f.deviceTeardowns) != 0 {
		t.Fatalf("bare redeploy tore down %+v", f.deviceTeardowns)
	}
	if pub.DeviceSet != "tablet" {
		t.Fatalf("redeploy stdout device_set = %q, want tablet kept", pub.DeviceSet)
	}
}

// A squad without a recorded set keeps deploying the default matrix and
// records no set.
func TestDeployWithoutDeviceSetUsesDefaultMatrix(t *testing.T) {
	newDeployRepo(t, deployTomlWithSets)
	f := installFakeDeploy(t)
	for i := range 2 {
		pub, code, err := deployCLI(t, "--name", "s")
		if err != nil || code != 0 {
			t.Fatalf("deploy %d: code %d err %v", i, code, err)
		}
		if pub.DeviceSet != "" {
			t.Fatalf("deploy %d: device_set = %q, want none", i, pub.DeviceSet)
		}
	}
	if f.iosProvisions[0].Specs[0].Device != testIOSDevice || len(f.iosProvisions[1].ExistingDevices) != 1 {
		t.Fatalf("iOS provisions = %+v, want the default matrix, then reuse", f.iosProvisions)
	}
}

func TestDeployDeviceSetRefusals(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		wantErr []string
	}{
		{"with --ios-spec", []string{"--device-set", "tablet", "--ios-spec", "iPhone 17:iOS 26.4:1"},
			[]string{"--device-set", "can't be combined"}},
		{"with --android-spec", []string{"--device-set", "tablet", "--android-spec", "pixel_7:" + testAndImage + ":1"},
			[]string{"--device-set", "can't be combined"}},
		{"unknown set", []string{"--device-set", "watch"},
			[]string{`"watch"`, "phone, tablet"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newDeployRepo(t, deployTomlWithSets)
			f := installFakeDeploy(t)
			_, code, err := deployCLI(t, append([]string{"--name", "s"}, c.args...)...)
			if code != -1 || err == nil {
				t.Fatalf("deploy %v: code %d err %v, want an error", c.args, code, err)
			}
			for _, want := range c.wantErr {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q does not contain %q", err, want)
				}
			}
			if f.iosBuilds+f.androidBuilds != 0 {
				t.Fatal("refused deploy still built")
			}
			if rec := loadTestRecord(t, "s"); rec != nil {
				t.Fatalf("refused deploy created a squad record: %+v", rec)
			}
		})
	}
}

func TestDeployDeviceSetFilteredByPlatform(t *testing.T) {
	newDeployRepo(t, deployTomlWithSets)
	f := installFakeDeploy(t)
	_, code, err := deployCLI(t, "--name", "s", "--device-set", "tablet", "--platform", "ios")
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v", code, err)
	}
	if f.androidBuilds != 0 || len(f.iosProvisions) != 1 || f.iosProvisions[0].Specs[0].Device != testTabletIOS {
		t.Fatalf("android builds %d, iOS provisions %+v; want the tablet set's iOS only", f.androidBuilds, f.iosProvisions)
	}

	// The phone set has no Android rows, so --platform android names the set.
	_, code, err = deployCLI(t, "--name", "p", "--device-set", "phone", "--platform", "android")
	if code != -1 || err == nil || !strings.Contains(err.Error(), `device set "phone" has no Android devices`) {
		t.Fatalf("code %d err %v, want the set's missing Android devices named", code, err)
	}
}

// A bare redeploy re-resolves the recorded set from the current config;
// when the config no longer defines it, the error names it and the way out.
func TestDeployRecordedDeviceSetMissing(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)
	seedSquad(t, &contract.SquadRecord{
		Name: "s", DeviceSet: "tablet",
		Devices: []contract.Device{seededIOS("s", contract.StatusReady)},
	})
	_, code, err := deployCLI(t, "--name", "s")
	if code != -1 || err == nil {
		t.Fatalf("code %d err %v, want an error", code, err)
	}
	for _, want := range []string{`"tablet"`, "pass `--device-set`", "`dismiss`"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %q does not contain %q", err, want)
		}
	}
	if f.iosBuilds+f.androidBuilds != 0 {
		t.Fatal("deploy built without a matrix")
	}
}

// Editing a recorded set changes what a bare redeploy compares against:
// ready devices that no longer match are refused, as for any mismatch.
func TestDeployRecordedDeviceSetEditedIsNotReusable(t *testing.T) {
	newDeployRepo(t, deployTomlWithSets)
	installFakeDeploy(t)
	sim := seededIOS("s", contract.StatusReady) // an iPhone 17, not the tablet's iPad
	seedSquad(t, &contract.SquadRecord{Name: "s", DeviceSet: "tablet", Devices: []contract.Device{sim}})
	_, code, err := deployCLI(t, "--name", "s", "--platform", "ios")
	if code != -1 || err == nil || !strings.Contains(err.Error(), "not reusable") {
		t.Fatalf("code %d err %v, want a not-reusable refusal", code, err)
	}
}

// An explicit set other than the recorded one goes through the normal
// per-platform mismatch rule: refused over ready devices (record
// untouched), reset and recorded when nothing is ready.
func TestDeployDifferentDeviceSetFollowsMismatchRule(t *testing.T) {
	newDeployRepo(t, deployTomlWithSets)
	f := installFakeDeploy(t)
	ipad := seededIOS("s", contract.StatusReady)
	ipad.DeviceType = testTabletIOS
	seedSquad(t, &contract.SquadRecord{Name: "s", DeviceSet: "tablet", Devices: []contract.Device{ipad}})
	before, _ := os.ReadFile(util.StateFileFor("s"))

	_, code, err := deployCLI(t, "--name", "s", "--device-set", "phone")
	if code != -1 || err == nil || !strings.Contains(err.Error(), "not reusable") {
		t.Fatalf("code %d err %v, want a not-reusable refusal", code, err)
	}
	if after, _ := os.ReadFile(util.StateFileFor("s")); !bytes.Equal(before, after) {
		t.Fatalf("refused deploy changed the record:\n%s\n→\n%s", before, after)
	}

	ipad.Status = contract.StatusError
	seedSquad(t, &contract.SquadRecord{Name: "s", DeviceSet: "tablet", Devices: []contract.Device{ipad}})
	pub, code, err := deployCLI(t, "--name", "s", "--device-set", "phone")
	if err != nil || code != 0 {
		t.Fatalf("deploy over errored devices: code %d err %v", code, err)
	}
	if len(f.deviceTeardowns) != 1 || pub.DeviceSet != "phone" {
		t.Fatalf("teardowns %+v, device_set %q; want the iPad reset and phone recorded", f.deviceTeardowns, pub.DeviceSet)
	}
}

// Spec flags record nothing: a new squad gets no set, and a squad with a
// recorded set keeps it, so its other platform still redeploys from it.
func TestDeploySpecFlagsRecordNoDeviceSet(t *testing.T) {
	newDeployRepo(t, deployTomlWithSets)
	installFakeDeploy(t)
	pub, code, err := deployCLI(t, "--name", "n", "--ios-spec", "iPhone 17:iOS 26.4:1")
	if err != nil || code != 0 || pub.DeviceSet != "" {
		t.Fatalf("spec-flag deploy: code %d err %v device_set %q, want none", code, err, pub.DeviceSet)
	}

	if _, code, err := deployCLI(t, "--name", "s", "--device-set", "tablet"); err != nil || code != 0 {
		t.Fatalf("deploy --device-set tablet: code %d err %v", code, err)
	}
	pub, code, err = deployCLI(t, "--name", "s", "--ios-spec", testTabletIOS+":iOS 26.4:1")
	if err != nil || code != 0 || pub.DeviceSet != "tablet" {
		t.Fatalf("spec-flag redeploy: code %d err %v device_set %q, want tablet kept", code, err, pub.DeviceSet)
	}
}
