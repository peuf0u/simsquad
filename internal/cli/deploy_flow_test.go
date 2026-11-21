package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/android"
	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/ios"
	"github.com/peuf0u/simsquad/internal/progress"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

const (
	testIOSDevice   = "iPhone 17"
	testIOSRuntime  = "iOS 26.4"
	testAndProfile  = "pixel_7"
	testAndImage    = "system-images;android-34;google_apis;arm64-v8a"
	testAndImageOld = "system-images;android-33;google_apis;arm64-v8a"
)

// deployToml equips both platforms with one device each and points the repos
// at subfolders of the config dir, so nothing reads the env or the cwd.
const deployToml = `[project]
ios_repo = "ios"
android_repo = "android"
ios_scheme = "App"

[[ios.sims]]
device = "iPhone 17"
runtime = "iOS 26.4"
count = 1

[[android.sims]]
device = "pixel_7"
image = "system-images;android-34;google_apis;arm64-v8a"
count = 1
`

const deployTomlIOSOnly = `[project]
ios_repo = "ios"
ios_scheme = "App"

[[ios.sims]]
device = "iPhone 17"
runtime = "iOS 26.4"
count = 1
`

// fakeDeploy replaces every call deploy makes into Xcode, Gradle, simctl,
// avdmanager and adb, and records what deploy asked for.
type fakeDeploy struct {
	iosBuilds, androidBuilds int
	iosBuildErr              error
	// duringIOSBuild runs inside the fake iOS build, to interleave another
	// writer with the deploy.
	duringIOSBuild    func()
	androidBuildErr   error
	iosProvisions     []ios.ProvisionInput
	androidProvisions []android.ProvisionInput
	// androidFail makes fresh Android slots come back errored.
	androidFail     bool
	squadTeardowns  []string
	deviceTeardowns []contract.Device
}

func installFakeDeploy(t *testing.T) *fakeDeploy {
	t.Helper()
	f := &fakeDeploy{}
	prev := deployVia
	t.Cleanup(func() { deployVia = prev })
	deployVia = deployOps{
		iosDestination: func(_, _, _ string) (string, bool, error) {
			return "dest-udid", false, nil
		},
		deleteIosDestination: func(string) error { return nil },
		iosBuild: func(o ios.BuildOptions) (ios.BuildResult, error) {
			f.iosBuilds++
			if f.duringIOSBuild != nil {
				f.duringIOSBuild()
			}
			if f.iosBuildErr != nil {
				return ios.BuildResult{}, f.iosBuildErr
			}
			return ios.BuildResult{AppPath: filepath.Join(o.Repo, "App.app"), BundleID: "com.example.app"}, nil
		},
		androidBuild: func(o android.BuildOptions) (android.BuildResult, error) {
			f.androidBuilds++
			if f.androidBuildErr != nil {
				return android.BuildResult{}, f.androidBuildErr
			}
			return android.BuildResult{APKPath: filepath.Join(o.Repo, "app.apk"), BundleID: "com.example.app"}, nil
		},
		iosProvision: func(in ios.ProvisionInput) []contract.Device {
			f.iosProvisions = append(f.iosProvisions, in)
			if len(in.ExistingDevices) > 0 {
				return readyAgain(in.ExistingDevices)
			}
			var out []contract.Device
			i := 0
			for _, s := range in.Specs {
				for range s.Count {
					out = append(out, contract.Device{
						Platform: contract.PlatformIOS, Name: util.SimName(in.Name, "ios", i),
						UDID: fmt.Sprintf("ios-new-%d", i), BundleID: in.BundleID, Status: contract.StatusReady,
						DeviceType: s.Device, Runtime: s.Runtime,
					})
					i++
				}
			}
			return out
		},
		androidProvision: func(in android.ProvisionInput) android.ProvisionResult {
			f.androidProvisions = append(f.androidProvisions, in)
			res := android.ProvisionResult{Ports: map[string]int{}}
			if len(in.ExistingDevices) > 0 {
				res.Devices = readyAgain(in.ExistingDevices)
				for _, d := range res.Devices {
					res.Ports[d.Name] = d.Port
				}
				return res
			}
			i := 0
			for _, s := range in.Specs {
				for range s.Count {
					name := util.SimName(in.Name, "android", i)
					port := 5560 + 2*i
					d := contract.Device{
						Platform: contract.PlatformAndroid, Name: name, AVDName: name,
						UDID: fmt.Sprintf("emulator-%d", port), Port: port, BundleID: in.BundleID,
						Status: contract.StatusReady, DeviceProfile: s.Device, SystemImage: s.Image,
					}
					if f.androidFail {
						d.Status = contract.StatusError
						d.ErrorMessage = "boot timeout"
					}
					res.Devices = append(res.Devices, d)
					res.Ports[name] = port
					i++
				}
			}
			return res
		},
		teardownSquad: func(name string, _ *progress.Logger) error {
			f.squadTeardowns = append(f.squadTeardowns, name)
			_ = state.RemoveRecord(name)
			return state.RemoveSquad(name)
		},
		teardownDevices: func(devs []contract.Device, _ *progress.Logger) {
			f.deviceTeardowns = append(f.deviceTeardowns, devs...)
		},
	}
	return f
}

func readyAgain(devs []contract.Device) []contract.Device {
	out := append([]contract.Device{}, devs...)
	for i := range out {
		out[i].Status = contract.StatusReady
		out[i].ErrorMessage = ""
	}
	return out
}

// newDeployRepo writes simsquad.toml into a fresh repo, chdirs into it and
// points the cache at a temp dir. The repo env vars are cleared so the
// user's shell can't change which platforms are equipped.
func newDeployRepo(t *testing.T, toml string) {
	t.Helper()
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	t.Setenv("SIMSQUAD_IOS_REPO", "")
	t.Setenv("SIMSQUAD_ANDROID_REPO", "")
	repo, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(repo, "simsquad.toml"), toml)
	t.Chdir(repo)
}

// deployCLI runs the real `simsquad deploy` in-process. It returns the
// parsed stdout (when any), the exit code (0, an ExitError code, or -1 for a
// plain error) and the plain error.
func deployCLI(t *testing.T, args ...string) (contract.SquadPublic, int, error) {
	t.Helper()
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"deploy"}, args...))
	err := root.Execute()
	var pub contract.SquadPublic
	if out.Len() > 0 {
		if uerr := json.Unmarshal(out.Bytes(), &pub); uerr != nil {
			t.Fatalf("unmarshal deploy stdout: %v\n%s", uerr, out.String())
		}
	}
	if err == nil {
		return pub, 0, nil
	}
	var ee *ExitError
	if errors.As(err, &ee) {
		return pub, ee.Code, nil
	}
	return pub, -1, err
}

func loadTestRecord(t *testing.T, name string) *contract.SquadRecord {
	t.Helper()
	rec, err := state.LoadRecord(name)
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func devicesOf(rec *contract.SquadRecord, platform contract.Platform) []contract.Device {
	var out []contract.Device
	for _, d := range rec.Devices {
		if d.Platform == platform {
			out = append(out, d)
		}
	}
	return out
}

func TestDeployPlatformRefusals(t *testing.T) {
	cases := []struct {
		name    string
		toml    string
		args    []string
		wantErr string
	}{
		{"unknown platform", deployToml, []string{"--platform", "windows"}, `--platform "windows"`},
		{"unequipped platform", deployTomlIOSOnly, []string{"--platform", "android"}, "no Android equipment"},
		{"with --ios-spec", deployToml,
			[]string{"--platform", "ios", "--ios-spec", "iPhone 17:iOS 26.4:1"}, "can't be combined"},
		{"with --android-spec", deployToml,
			[]string{"--platform", "android", "--android-spec", "pixel_7:" + testAndImage + ":1"}, "can't be combined"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newDeployRepo(t, c.toml)
			f := installFakeDeploy(t)
			_, code, err := deployCLI(t, append([]string{"--name", "s"}, c.args...)...)
			if code != -1 || err == nil || !strings.Contains(err.Error(), c.wantErr) {
				t.Fatalf("deploy %v: code %d err %v, want an error containing %q", c.args, code, err, c.wantErr)
			}
			if f.iosBuilds+f.androidBuilds != 0 {
				t.Fatalf("refused deploy still built (ios %d, android %d)", f.iosBuilds, f.androidBuilds)
			}
			if rec := loadTestRecord(t, "s"); rec != nil {
				t.Fatalf("refused deploy created a squad record: %+v", rec)
			}
		})
	}
}

func TestDeployPlatformRefusesMissingRepo(t *testing.T) {
	// The Android matrix is equipped but no repo resolves for it.
	newDeployRepo(t, strings.Replace(deployToml, `android_repo = "android"`, "", 1))
	installFakeDeploy(t)
	_, code, err := deployCLI(t, "--name", "s", "--platform", "android")
	if code != -1 || err == nil || !strings.Contains(err.Error(), "no Android repo") {
		t.Fatalf("code %d err %v, want a missing-repo error", code, err)
	}
}

func TestDeployPlatformBuildsOnlyChosenPlatformOnNewSquad(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)

	pub, code, err := deployCLI(t, "--name", "s", "--platform", "ios")
	if err != nil || code != 0 {
		t.Fatalf("deploy --platform ios: code %d err %v", code, err)
	}
	if f.iosBuilds != 1 || f.androidBuilds != 0 || len(f.androidProvisions) != 0 {
		t.Fatalf("builds ios %d android %d, android provisions %d; want iOS only",
			f.iosBuilds, f.androidBuilds, len(f.androidProvisions))
	}
	rec := loadTestRecord(t, "s")
	if len(rec.Devices) != 1 || rec.Devices[0].Platform != contract.PlatformIOS {
		t.Fatalf("devices = %+v, want one iOS device", rec.Devices)
	}
	if rec.Builds.Android != nil || rec.Builds.IOS == nil {
		t.Fatalf("builds = %+v, want iOS only", rec.Builds)
	}
	if len(pub.Devices) != 1 {
		t.Fatalf("stdout devices = %+v", pub.Devices)
	}
}

func TestDeployPlatformRepeatableDeploysBoth(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)
	_, code, err := deployCLI(t, "--name", "s", "--platform", "ios", "--platform", "android")
	if err != nil || code != 0 {
		t.Fatalf("code %d err %v", code, err)
	}
	if f.iosBuilds != 1 || f.androidBuilds != 1 {
		t.Fatalf("builds ios %d android %d, want both", f.iosBuilds, f.androidBuilds)
	}
}

func seedSquad(t *testing.T, rec *contract.SquadRecord) {
	t.Helper()
	if rec.CreatedAt == "" {
		rec.CreatedAt = "2026-01-01T00:00:00Z"
	}
	if err := state.SaveRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := state.RegisterSquad(state.RegisterSquadInput{Name: rec.Name, StateFile: util.StateFileFor(rec.Name)}); err != nil {
		t.Fatal(err)
	}
}

func seededIOS(name string, status contract.DeviceStatus) contract.Device {
	return contract.Device{
		Platform: contract.PlatformIOS, Name: util.SimName(name, "ios", 0), UDID: "ios-old-0",
		BundleID: "com.example.app", Status: status, ReadyAt: "2026-01-01T00:00:00Z",
		DeviceType: testIOSDevice, Runtime: testIOSRuntime,
	}
}

func seededAndroid(name, image string, status contract.DeviceStatus) contract.Device {
	n := util.SimName(name, "android", 0)
	return contract.Device{
		Platform: contract.PlatformAndroid, Name: n, AVDName: n, UDID: "emulator-5554", Port: 5554,
		BundleID: "com.example.app", Status: status, ReadyAt: "2026-01-01T00:00:00Z",
		DeviceProfile: testAndProfile, SystemImage: image,
	}
}

// A squad that already holds iOS gets Android added by a bare deploy, and
// its iOS devices are reused rather than recreated.
func TestDeployAddsMissingPlatformAndReusesTheOther(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)

	if _, code, err := deployCLI(t, "--name", "s", "--platform", "ios"); err != nil || code != 0 {
		t.Fatalf("deploy --platform ios: code %d err %v", code, err)
	}
	pub, code, err := deployCLI(t, "--name", "s")
	if err != nil || code != 0 {
		t.Fatalf("bare deploy: code %d err %v", code, err)
	}
	if len(f.iosProvisions) != 2 || len(f.iosProvisions[1].ExistingDevices) != 1 {
		t.Fatalf("second iOS provision got existing %+v, want the first deploy's sim reused", f.iosProvisions)
	}
	if len(f.androidProvisions) != 1 || len(f.androidProvisions[0].ExistingDevices) != 0 {
		t.Fatalf("android provisions = %+v, want one fresh", f.androidProvisions)
	}
	rec := loadTestRecord(t, "s")
	if len(devicesOf(rec, contract.PlatformIOS)) != 1 || len(devicesOf(rec, contract.PlatformAndroid)) != 1 {
		t.Fatalf("devices = %+v, want one per platform", rec.Devices)
	}
	if !rec.Builds.IOS.Reused || rec.Builds.Android.Reused {
		t.Fatalf("builds reused ios=%v android=%v, want true/false", rec.Builds.IOS.Reused, rec.Builds.Android.Reused)
	}
	if pub.Reused {
		t.Fatal("stdout reused = true, but Android was provisioned fresh")
	}
}

// A platform that no longer matches and has nothing ready is torn down and
// reprovisioned on its own; the matching platform is reused.
func TestDeployResetsOnlyTheMismatchedPlatform(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)
	staleAndroid := seededAndroid("s", testAndImageOld, contract.StatusError)
	seedSquad(t, &contract.SquadRecord{
		Name:    "s",
		Devices: []contract.Device{seededIOS("s", contract.StatusReady), staleAndroid},
		Ports:   map[string]int{staleAndroid.Name: 5554},
	})

	_, code, err := deployCLI(t, "--name", "s")
	if err != nil || code != 0 {
		t.Fatalf("deploy: code %d err %v", code, err)
	}
	if len(f.squadTeardowns) != 0 {
		t.Fatalf("squad torn down %v, want only the Android devices reset", f.squadTeardowns)
	}
	if len(f.deviceTeardowns) != 1 || f.deviceTeardowns[0].UDID != staleAndroid.UDID {
		t.Fatalf("device teardowns = %+v, want the stale emulator only", f.deviceTeardowns)
	}
	if len(f.iosProvisions) != 1 || len(f.iosProvisions[0].ExistingDevices) != 1 {
		t.Fatalf("iOS provision = %+v, want reuse of the ready sim", f.iosProvisions)
	}
	if len(f.androidProvisions) != 1 || len(f.androidProvisions[0].ExistingDevices) != 0 {
		t.Fatalf("android provision = %+v, want a fresh one", f.androidProvisions)
	}
	rec := loadTestRecord(t, "s")
	and := devicesOf(rec, contract.PlatformAndroid)
	if len(and) != 1 || and[0].SystemImage != testAndImage || and[0].Status != contract.StatusReady {
		t.Fatalf("android devices = %+v, want one fresh ready emulator", and)
	}
}

// A platform that no longer matches but has a ready device is refused, and
// the squad record is left exactly as it was.
func TestDeployRefusesMismatchedPlatformWithReadyDevice(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)
	seedSquad(t, &contract.SquadRecord{
		Name:    "s",
		Devices: []contract.Device{seededIOS("s", contract.StatusReady), seededAndroid("s", testAndImageOld, contract.StatusReady)},
		Context: map[string]string{"user": "alice"},
	})
	before, _ := os.ReadFile(util.StateFileFor("s"))

	_, code, err := deployCLI(t, "--name", "s", "--platform", "android", "--context", "user=bob")
	if code != -1 || err == nil || !strings.Contains(err.Error(), "not reusable") {
		t.Fatalf("code %d err %v, want a not-reusable refusal", code, err)
	}
	if f.iosBuilds+f.androidBuilds != 0 || len(f.deviceTeardowns)+len(f.squadTeardowns) != 0 {
		t.Fatal("refused deploy built or tore something down")
	}
	after, _ := os.ReadFile(util.StateFileFor("s"))
	if !bytes.Equal(before, after) {
		t.Fatalf("refused deploy changed the record:\n%s\n→\n%s", before, after)
	}
}

// Without --platform, a squad whose every platform mismatches with nothing
// ready resets as a whole, exactly as before per-platform reuse: devices
// gone, created_at fresh, old context dropped.
func TestDeployWholeResetWhenEveryPlatformResets(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)
	seedSquad(t, &contract.SquadRecord{
		Name: "s",
		Devices: []contract.Device{
			func() contract.Device { d := seededIOS("s", contract.StatusError); d.Runtime = "iOS 18.0"; return d }(),
			seededAndroid("s", testAndImageOld, contract.StatusError),
		},
		Context: map[string]string{"old": "1"},
	})

	pub, code, err := deployCLI(t, "--name", "s", "--context", "user=bob")
	if err != nil || code != 0 {
		t.Fatalf("deploy: code %d err %v", code, err)
	}
	if len(f.squadTeardowns) != 1 {
		t.Fatalf("squad teardowns = %v, want one whole reset", f.squadTeardowns)
	}
	rec := loadTestRecord(t, "s")
	if rec.CreatedAt == "2026-01-01T00:00:00Z" {
		t.Fatal("created_at kept, want a fresh squad")
	}
	if len(rec.Context) != 1 || rec.Context["user"] != "bob" {
		t.Fatalf("context = %v, want only the new source", rec.Context)
	}
	if len(rec.Devices) != 2 || pub.Reused {
		t.Fatalf("devices = %+v reused=%v", rec.Devices, pub.Reused)
	}
}

// On a squad with one iOS sim and one Android emulator, --platform ios
// builds and installs iOS only; the Android device, its builds entry and its
// ports stay exactly as they were.
func TestDeployPlatformLeavesOtherPlatformUntouched(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)
	and := seededAndroid("s", testAndImage, contract.StatusReady)
	andBuild := &contract.AndroidBuild{Repo: "/old/android", APKPath: "/old/app.apk", BundleID: "com.example.app", GitSHA: "abc"}
	seedSquad(t, &contract.SquadRecord{
		Name:    "s",
		Builds:  contract.Builds{Android: andBuild},
		Devices: []contract.Device{seededIOS("s", contract.StatusReady), and},
		Ports:   map[string]int{and.Name: 5554},
	})

	pub, code, err := deployCLI(t, "--name", "s", "--platform", "ios")
	if err != nil || code != 0 {
		t.Fatalf("deploy --platform ios: code %d err %v", code, err)
	}
	if f.androidBuilds != 0 || len(f.androidProvisions) != 0 || f.iosBuilds != 1 || len(f.iosProvisions) != 1 {
		t.Fatalf("ios build %d provision %d, android build %d provision %d; want iOS only",
			f.iosBuilds, len(f.iosProvisions), f.androidBuilds, len(f.androidProvisions))
	}
	rec := loadTestRecord(t, "s")
	gotAnd := devicesOf(rec, contract.PlatformAndroid)
	if len(gotAnd) != 1 || gotAnd[0] != and {
		t.Fatalf("android devices = %+v, want unchanged %+v", gotAnd, and)
	}
	if !reflect.DeepEqual(rec.Builds.Android, andBuild) {
		t.Fatalf("builds.android = %+v, want unchanged", rec.Builds.Android)
	}
	if len(rec.Ports) != 1 || rec.Ports[and.Name] != 5554 {
		t.Fatalf("ports = %v, want unchanged", rec.Ports)
	}
	if rec.Builds.IOS == nil || !rec.Builds.IOS.Reused {
		t.Fatalf("builds.ios = %+v, want a reused iOS build", rec.Builds.IOS)
	}
	ios := devicesOf(rec, contract.PlatformIOS)
	if len(ios) != 1 || ios[0].ReadyAt == "2026-01-01T00:00:00Z" {
		t.Fatalf("ios devices = %+v, want reinstalled with a fresh ready_at", ios)
	}
	if !pub.Reused || len(pub.Devices) != 2 {
		t.Fatalf("stdout reused=%v devices=%d, want the whole reused squad", pub.Reused, len(pub.Devices))
	}
}

// Two deploys of different platforms on one squad, interleaved as load A,
// load B, save A, save B: both platforms end up in the record because each
// save merges into what is on disk instead of writing its stale copy.
func TestDeployMergeKeepsConcurrentPlatform(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	staleAnd := seededAndroid("s", testAndImageOld, contract.StatusError)
	staleAnd.Name = util.SimName("s", "android", 7)
	seedSquad(t, &contract.SquadRecord{
		Name:    "s",
		Devices: []contract.Device{staleAnd},
		Context: map[string]string{"user": "alice"},
		Ports:   map[string]int{staleAnd.Name: 5570},
	})
	loadA, _ := state.LoadRecord("s")
	loadB, _ := state.LoadRecord("s")
	_, _ = loadA, loadB // each deploy holds its own stale copy from here on

	iosDev := seededIOS("s", contract.StatusReady)
	andDev := seededAndroid("s", testAndImage, contract.StatusReady)
	a := deployResult{
		ios:        true,
		builds:     contract.Builds{IOS: &contract.IosBuild{AppPath: "/a/App.app"}},
		iosDevices: []contract.Device{iosDev},
	}
	b := deployResult{
		android:        true,
		builds:         contract.Builds{Android: &contract.AndroidBuild{APKPath: "/b/app.apk"}},
		androidDevices: []contract.Device{andDev},
		androidPorts:   map[string]int{andDev.Name: 5554},
	}
	if _, err := state.UpdateRecord("s", a.mergeInto); err != nil {
		t.Fatal(err)
	}
	if _, err := state.UpdateRecord("s", b.mergeInto); err != nil {
		t.Fatal(err)
	}

	rec := loadTestRecord(t, "s")
	if len(rec.Devices) != 2 || rec.Devices[0] != iosDev || rec.Devices[1] != andDev {
		t.Fatalf("devices = %+v, want A's iOS sim then B's emulator", rec.Devices)
	}
	if rec.Builds.IOS == nil || rec.Builds.Android == nil {
		t.Fatalf("builds = %+v, want both", rec.Builds)
	}
	if len(rec.Ports) != 1 || rec.Ports[andDev.Name] != 5554 {
		t.Fatalf("ports = %v, want only B's reservation (stale Android port dropped)", rec.Ports)
	}
	if rec.Context["user"] != "alice" {
		t.Fatalf("context = %v, want kept from disk", rec.Context)
	}
}

// A set-context that lands while a deploy is building survives the deploy's
// final save.
func TestDeployKeepsContextSetDuringDeploy(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)
	seedSquad(t, &contract.SquadRecord{
		Name:    "s",
		Devices: []contract.Device{seededIOS("s", contract.StatusReady)},
		Context: map[string]string{"user": "alice"},
	})
	f.duringIOSBuild = func() {
		if _, err := state.UpdateRecord("s", func(r *contract.SquadRecord) error {
			r.Context["token"] = "fresh"
			return nil
		}); err != nil {
			t.Error(err)
		}
	}

	if _, code, err := deployCLI(t, "--name", "s", "--platform", "ios", "--context", "user=bob"); err != nil || code != 0 {
		t.Fatalf("deploy: code %d err %v", code, err)
	}
	rec := loadTestRecord(t, "s")
	if rec.Context["user"] != "bob" || rec.Context["token"] != "fresh" {
		t.Fatalf("context = %v, want deploy's user=bob plus the mid-deploy token", rec.Context)
	}
}

func testClaimOpts() claimOpts {
	return claimOpts{
		iosSpecs:        []contract.IosSpec{{Device: testIOSDevice, Runtime: testIOSRuntime, Count: 1}},
		ctxOverrides:    map[string]string{"user": "alice"},
		allowWholeReset: true,
	}
}

// A deploy that finds the squad already created — here by an earlier claim
// whose deploy is still provisioning — takes the reuse path instead of
// creating it again.
func TestClaimSquadSecondCreateTakesReusePath(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	first, err := claimSquad("s", testClaimOpts())
	if err != nil || !first.created {
		t.Fatalf("first claim: %+v err %v, want created", first, err)
	}
	second, err := claimSquad("s", testClaimOpts())
	if err != nil {
		t.Fatal(err)
	}
	if second.created || second.plan.ios != actionAdd || second.plan.wholeReset {
		t.Fatalf("second claim = %+v, want the existing squad with iOS to add", second)
	}
	if second.rec.CreatedAt != first.rec.CreatedAt {
		t.Fatal("second claim replaced the squad")
	}
}

func TestClaimSquadCreatesOnceUnderContention(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	const deploys = 8
	results := make(chan bool, deploys)
	errs := make(chan error, deploys)
	for range deploys {
		go func() {
			c, err := claimSquad("s", testClaimOpts())
			errs <- err
			results <- c.created
		}()
	}
	created := 0
	for range deploys {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
		if <-results {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("%d claims created the squad, want exactly 1", created)
	}
	if _, ok, _ := state.FindSquad("s"); !ok {
		t.Fatal("squad not registered")
	}
}

// The exit code counts only the deployed platforms' devices: a broken
// Android emulator doesn't turn a clean iOS deploy into a mixed result.
// Stdout still shows the whole squad, the emulator at its stored status.
func TestDeployExitCodeCountsOnlyDeployedPlatforms(t *testing.T) {
	newDeployRepo(t, deployToml)
	installFakeDeploy(t)
	brokenAnd := seededAndroid("s", testAndImage, contract.StatusError)
	seedSquad(t, &contract.SquadRecord{
		Name:    "s",
		Devices: []contract.Device{seededIOS("s", contract.StatusReady), brokenAnd},
	})

	pub, code, err := deployCLI(t, "--name", "s", "--platform", "ios")
	if err != nil || code != 0 {
		t.Fatalf("deploy --platform ios: code %d err %v, want exit 0", code, err)
	}
	if len(pub.Devices) != 2 || pub.Devices[1] != brokenAnd {
		t.Fatalf("stdout devices = %+v, want the whole squad with the emulator as stored", pub.Devices)
	}
}

// A squad this deploy created and left with nothing ready is removed again.
func TestDeployCleanupRemovesSquadItCreated(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)
	f.iosBuildErr = errors.New("xcodebuild failed")

	pub, code, err := deployCLI(t, "--name", "s", "--platform", "ios")
	if err != nil || code != 1 {
		t.Fatalf("code %d err %v, want exit 1", code, err)
	}
	if pub.Builds.IOS == nil || pub.Builds.IOS.Error == "" {
		t.Fatalf("stdout builds = %+v, want the iOS build error", pub.Builds)
	}
	if rec := loadTestRecord(t, "s"); rec != nil {
		t.Fatalf("record left behind: %+v", rec)
	}
	if _, ok, _ := state.FindSquad("s"); ok {
		t.Fatal("squad still registered")
	}
}

// Cleanup tears down only the deployed platform: when a concurrent deploy
// added the other platform meanwhile, the squad stays with those devices.
func TestDeployCleanupKeepsOtherPlatformsDevices(t *testing.T) {
	newDeployRepo(t, deployToml)
	f := installFakeDeploy(t)
	f.iosBuildErr = errors.New("xcodebuild failed")
	and := seededAndroid("s", testAndImage, contract.StatusReady)
	f.duringIOSBuild = func() {
		if _, err := state.UpdateRecord("s", deployResult{
			android:        true,
			builds:         contract.Builds{Android: &contract.AndroidBuild{APKPath: "/b/app.apk"}},
			androidDevices: []contract.Device{and},
			androidPorts:   map[string]int{and.Name: and.Port},
		}.mergeInto); err != nil {
			t.Error(err)
		}
	}

	if _, code, err := deployCLI(t, "--name", "s", "--platform", "ios"); err != nil || code != 1 {
		t.Fatalf("code %d err %v, want exit 1", code, err)
	}
	if len(f.squadTeardowns) != 0 {
		t.Fatalf("whole squad torn down: %v", f.squadTeardowns)
	}
	rec := loadTestRecord(t, "s")
	if rec == nil || len(rec.Devices) != 1 || rec.Devices[0] != and {
		t.Fatalf("record = %+v, want the squad kept with the concurrent emulator", rec)
	}
	if rec.Builds.IOS != nil || rec.Builds.Android == nil || rec.Ports[and.Name] != and.Port {
		t.Fatalf("builds = %+v ports = %v, want the failed iOS build dropped and Android kept", rec.Builds, rec.Ports)
	}
	if _, ok, _ := state.FindSquad("s"); !ok {
		t.Fatal("squad unregistered")
	}
}
