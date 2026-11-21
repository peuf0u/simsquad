package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
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
	androidBuildErr          error
	iosProvisions            []ios.ProvisionInput
	androidProvisions        []android.ProvisionInput
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
