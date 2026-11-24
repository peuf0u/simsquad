package android

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
)

// Headless boots keep today's arguments exactly; a windowed boot drops
// -no-window, lets the emulator pick its GPU, and cold boots so a freshly
// written config.ini (hw.keyboard) is read (issue #33).
func TestEmulatorArgs(t *testing.T) {
	t.Parallel()

	base := []string{"-avd", "simsquad-x-android-0", "-port", "5556", "-no-snapshot-save"}
	cases := []struct {
		name         string
		window, wipe bool
		want         []string
	}{
		{"headless", false, false, append(slices.Clone(base),
			"-no-window", "-no-boot-anim", "-gpu", "swiftshader_indirect")},
		{"headless wipe", false, true, append(slices.Clone(base),
			"-no-window", "-no-boot-anim", "-gpu", "swiftshader_indirect", "-wipe-data")},
		{"windowed", true, false, append(slices.Clone(base),
			"-no-snapshot-load", "-no-boot-anim", "-gpu", "auto")},
		{"windowed wipe", true, true, append(slices.Clone(base),
			"-no-snapshot-load", "-no-boot-anim", "-gpu", "auto", "-wipe-data")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := emulatorArgs("simsquad-x-android-0", 5556, c.window, c.wipe)
			if !slices.Equal(got, c.want) {
				t.Fatalf("emulatorArgs = %q, want %q", got, c.want)
			}
		})
	}
}

// enableHWKeyboard turns the hardware keyboard on in an AVD's config.ini so
// the Mac keyboard types into a windowed emulator, keeping every other line
// and its order.
func TestEnableHWKeyboard(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name, in, want string
	}{
		{"replaces no", "avd.ini.encoding=UTF-8\nhw.keyboard=no\nhw.lcd.density=420\n",
			"avd.ini.encoding=UTF-8\nhw.keyboard=yes\nhw.lcd.density=420\n"},
		{"keeps yes", "hw.keyboard=yes\nhw.ramSize=2048\n", "hw.keyboard=yes\nhw.ramSize=2048\n"},
		{"appends when missing", "hw.lcd.density=420\nhw.ramSize=2048\n",
			"hw.lcd.density=420\nhw.ramSize=2048\nhw.keyboard=yes\n"},
		{"appends after a last line without newline", "hw.ramSize=2048",
			"hw.ramSize=2048\nhw.keyboard=yes\n"},
		{"leaves similar keys alone", "hw.keyboard.lid=no\nhw.keyboard = no\n",
			"hw.keyboard.lid=no\nhw.keyboard=yes\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config.ini")
			if err := os.WriteFile(path, []byte(c.in), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := enableHWKeyboard(path); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != c.want {
				t.Fatalf("config.ini =\n%q\nwant\n%q", got, c.want)
			}
		})
	}
}

func TestEnableHWKeyboardMissingConfig(t *testing.T) {
	t.Parallel()

	if err := enableHWKeyboard(filepath.Join(t.TempDir(), "config.ini")); err == nil {
		t.Fatal("err = nil for a missing config.ini, want an error")
	}
}

func TestAVDConfigPath(t *testing.T) {
	home := t.TempDir()
	t.Setenv("ANDROID_AVD_HOME", home)
	got, err := avdConfigPath("simsquad-x-android-0")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(home, "simsquad-x-android-0.avd", "config.ini")
	if got != want {
		t.Fatalf("avdConfigPath = %s, want %s", got, want)
	}
}

// A reused emulator is restarted only to gain a window: --window never
// leaves a reachable headless emulator as it is, and a deploy without it
// never touches a running one (issue #33).
func TestReuseBootDecision(t *testing.T) {
	t.Parallel()

	headless := contract.Device{UDID: "emulator-5556"}
	windowed := contract.Device{UDID: "emulator-5556", Window: true}
	physical := contract.Device{UDID: "R58M", Physical: true}
	cases := []struct {
		name      string
		dev       contract.Device
		reachable bool
		window    bool
		want      reuseBoot
	}{
		{"window, running headless", headless, true, true, reuseRestartWindowed},
		{"window, running windowed", windowed, true, true, reuseKeep},
		{"window, unreachable", headless, false, true, reuseBootWindowed},
		{"window, unreachable, recorded windowed", windowed, false, true, reuseBootWindowed},
		{"no window, running windowed", windowed, true, false, reuseKeep},
		{"no window, running headless", headless, true, false, reuseKeep},
		{"no window, unreachable", headless, false, false, reuseBootHeadless},
		{"no window, unreachable, recorded windowed", windowed, false, false, reuseBootHeadless},
		{"physical, window", physical, true, true, reuseKeep},
		{"physical, unreachable", physical, false, true, reusePhysicalUnreachable},
		{"physical, unreachable, no window", physical, false, false, reusePhysicalUnreachable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := reuseBootDecision(c.dev, c.reachable, c.window); got != c.want {
				t.Fatalf("reuseBootDecision = %v, want %v", got, c.want)
			}
		})
	}
}
