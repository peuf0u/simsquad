package android

import (
	"slices"
	"testing"
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
