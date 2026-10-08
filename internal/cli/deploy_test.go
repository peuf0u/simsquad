package cli

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
)

func TestParseIosSpec(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    contract.IosSpec
		wantErr bool
	}{
		{
			name: "canonical",
			in:   "iPhone 17:iOS 26.4:1",
			want: contract.IosSpec{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 1},
		},
		{
			name: "multi-slot",
			in:   "iPhone 16 Pro:iOS 26.1:3",
			want: contract.IosSpec{Device: "iPhone 16 Pro", Runtime: "iOS 26.1", Count: 3},
		},
		{
			name: "trim whitespace",
			in:   "  iPhone 17 : iOS 26.4 : 2 ",
			want: contract.IosSpec{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 2},
		},
		{name: "wrong field count", in: "iPhone 17:iOS 26.4", wantErr: true},
		{name: "empty device", in: ":iOS 26.4:1", wantErr: true},
		{name: "empty runtime", in: "iPhone 17::1", wantErr: true},
		{name: "non-numeric count", in: "iPhone 17:iOS 26.4:lots", wantErr: true},
		{name: "zero count", in: "iPhone 17:iOS 26.4:0", wantErr: true},
		{name: "negative count", in: "iPhone 17:iOS 26.4:-1", wantErr: true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseIosSpec(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseIosSpec(%q) = %+v, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseIosSpec(%q) returned error: %v", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("parseIosSpec(%q) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

func TestCommandsRejectPositionalArgs(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"deploy", "extra"},
		{"dismiss", "extra", "--all-ephemeral"},
		{"status", "extra"},
		{"sweep", "extra"},
		{"equip", "extra"},
		{"claim", "extra", "--name=foo"},
		{"release", "extra", "--name=foo"},
		{"devices", "extra", "--name=foo"},
		{"reset", "extra", "--name=foo"},
		{"set-env", "extra", "--name=foo"},
	} {
		args := args
		t.Run(args[0], func(t *testing.T) {
			t.Parallel()
			cmd := NewRootCmd()
			cmd.SetArgs(args)
			cmd.SetOut(&bytes.Buffer{})
			cmd.SetErr(&bytes.Buffer{})
			if err := cmd.Execute(); err == nil {
				t.Fatalf("NewRootCmd(%v) succeeded, want error", args)
			}
		})
	}
}

func TestParseAndroidSpec(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      string
		want    contract.AndroidSpec
		wantErr bool
	}{
		{
			name: "emulator default",
			in:   "pixel_7:system-images;android-34;google_apis;arm64-v8a:1",
			want: contract.AndroidSpec{
				Device: "pixel_7",
				Image:  "system-images;android-34;google_apis;arm64-v8a",
				Count:  1, Target: contract.TargetEmulator,
			},
		},
		{
			name: "explicit emulator",
			in:   "pixel_8:system-images;android-34;google_apis;arm64-v8a:2:emulator",
			want: contract.AndroidSpec{
				Device: "pixel_8",
				Image:  "system-images;android-34;google_apis;arm64-v8a",
				Count:  2, Target: contract.TargetEmulator,
			},
		},
		{
			name: "physical",
			in:   "any:any:1:physical",
			want: contract.AndroidSpec{
				Device: "any", Image: "any", Count: 1, Target: contract.TargetPhysical,
			},
		},
		{name: "too few fields", in: "pixel_7:1", wantErr: true},
		{name: "too many fields", in: "pixel_7:image:1:emulator:extra", wantErr: true},
		{name: "invalid target", in: "pixel_7:image:1:nope", wantErr: true},
		{name: "non-numeric count", in: "pixel_7:image:lots", wantErr: true},
		{name: "empty device", in: ":image:1", wantErr: true},
		{name: "empty image", in: "pixel_7::1", wantErr: true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseAndroidSpec(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseAndroidSpec(%q) = %+v, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseAndroidSpec(%q) returned error: %v", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("parseAndroidSpec(%q) = %+v, want %+v", c.in, got, c.want)
			}
		})
	}
}

// TestExitCodeFromDevices locks the 0 / 2 / 1 contract. Downstream qa-run
// branches on these codes — a regression here changes observable behaviour
// for every consumer.
func TestExitCodeFromDevices(t *testing.T) {
	t.Parallel()

	ready := contract.Device{Status: contract.StatusReady}
	bad := contract.Device{Status: contract.StatusError}

	cases := []struct {
		name string
		devs []contract.Device
		want int
	}{
		{"empty → none-ready", nil, 1},
		{"all ready", []contract.Device{ready, ready}, 0},
		{"one ready one error → mixed", []contract.Device{ready, bad}, 2},
		{"all error → none-ready", []contract.Device{bad, bad}, 1},
		{"single ready", []contract.Device{ready}, 0},
		{"single error", []contract.Device{bad}, 1},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := exitCodeFromDevices(c.devs); got != c.want {
				t.Fatalf("exitCodeFromDevices(%v) = %d, want %d", c.devs, got, c.want)
			}
		})
	}
}

func TestParseEnvEntries(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		in      []string
		want    map[string]string
		wantErr bool
	}{
		{name: "nil input", in: nil, want: nil},
		{name: "single", in: []string{"user=alice"}, want: map[string]string{"user": "alice"}},
		{name: "multi", in: []string{"user=alice", "api=staging"},
			want: map[string]string{"user": "alice", "api": "staging"}},
		{name: "value with equals signs",
			in:   []string{"creds=user:bob,pass=hunter2"},
			want: map[string]string{"creds": "user:bob,pass=hunter2"}},
		{name: "value with spaces", in: []string{"label=hello world"},
			want: map[string]string{"label": "hello world"}},
		{name: "empty value allowed", in: []string{"feature_flag="},
			want: map[string]string{"feature_flag": ""}},
		{name: "missing equals", in: []string{"foo"}, wantErr: true},
		{name: "empty key", in: []string{"=bar"}, wantErr: true},
		{name: "duplicate key", in: []string{"user=alice", "user=bob"}, wantErr: true},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseEnvEntries(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("parseEnvEntries(%v) succeeded with %v, want error", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseEnvEntries(%v): %v", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("parseEnvEntries(%v) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

func TestApplyEnvSource(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		base      map[string]string
		toml      map[string]string
		overrides map[string]string
		want      map[string]string
	}{
		{name: "all empty", want: nil},
		{name: "base only, no source",
			base: map[string]string{"a": "1"},
			want: map[string]string{"a": "1"}},
		{name: "TOML used when no CLI overrides",
			toml: map[string]string{"api": "staging"},
			want: map[string]string{"api": "staging"}},
		{name: "CLI overrides used; TOML ignored entirely",
			toml:      map[string]string{"api": "staging", "locale": "en_US"},
			overrides: map[string]string{"user": "alice"},
			want:      map[string]string{"user": "alice"}},
		{name: "reuse: TOML layers on existing (no CLI)",
			base: map[string]string{"a": "old", "b": "keep"},
			toml: map[string]string{"a": "new"},
			want: map[string]string{"a": "new", "b": "keep"}},
		{name: "reuse: CLI layers on existing; TOML skipped",
			base:      map[string]string{"a": "old", "b": "keep"},
			toml:      map[string]string{"c": "ignored"},
			overrides: map[string]string{"a": "new"},
			want:      map[string]string{"a": "new", "b": "keep"}},
		{name: "CLI replaces TOML on new deploy",
			toml:      map[string]string{"api": "staging"},
			overrides: map[string]string{"api": "prod", "user": "alice"},
			want:      map[string]string{"api": "prod", "user": "alice"}},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			got := applyEnvSource(c.base, c.toml, c.overrides)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("applyEnvSource = %v, want %v", got, c.want)
			}
		})
	}
}

// TestResolveSpecsCliMode locks the "any spec flag replaces TOML matrix for
// both platforms" rule. When cliSpecMode is true and a platform has no CLI
// specs, that platform is skipped (resolver returns nil) — TOML is not
// consulted, so passing nil for the config is safe and asserts the rule.
func TestResolveSpecsCliMode(t *testing.T) {
	t.Parallel()

	t.Run("ios spec only → android skipped", func(t *testing.T) {
		t.Parallel()
		ios, err := resolveIosSpecs(nil, []string{"iPhone 17:iOS 26.4:1"}, true)
		if err != nil {
			t.Fatalf("resolveIosSpecs: %v", err)
		}
		if len(ios) != 1 || ios[0].Device != "iPhone 17" {
			t.Fatalf("ios = %+v", ios)
		}
		android, err := resolveAndroidSpecs(nil, nil, true)
		if err != nil {
			t.Fatalf("resolveAndroidSpecs: %v", err)
		}
		if android != nil {
			t.Fatalf("android = %+v, want nil", android)
		}
	})

	t.Run("android spec only → ios skipped", func(t *testing.T) {
		t.Parallel()
		ios, err := resolveIosSpecs(nil, nil, true)
		if err != nil {
			t.Fatalf("resolveIosSpecs: %v", err)
		}
		if ios != nil {
			t.Fatalf("ios = %+v, want nil", ios)
		}
		android, err := resolveAndroidSpecs(nil,
			[]string{"pixel_7:system-images;android-34;google_apis;arm64-v8a:1"}, true)
		if err != nil {
			t.Fatalf("resolveAndroidSpecs: %v", err)
		}
		if len(android) != 1 || android[0].Device != "pixel_7" {
			t.Fatalf("android = %+v", android)
		}
	})

	t.Run("both flags → both from CLI", func(t *testing.T) {
		t.Parallel()
		ios, err := resolveIosSpecs(nil, []string{"iPhone 17:iOS 26.4:1"}, true)
		if err != nil || len(ios) != 1 {
			t.Fatalf("ios = %+v err=%v", ios, err)
		}
		android, err := resolveAndroidSpecs(nil,
			[]string{"pixel_7:system-images;android-34;google_apis;arm64-v8a:1"}, true)
		if err != nil || len(android) != 1 {
			t.Fatalf("android = %+v err=%v", android, err)
		}
	})
}

func TestSpecMatches(t *testing.T) {
	t.Parallel()

	record := contract.SquadRecord{
		Devices: []contract.Device{
			{
				Platform:   contract.PlatformIOS,
				DeviceType: "iPhone 17",
				Runtime:    "iOS 26.4",
				UDID:       "ios-1",
				Status:     contract.StatusReady,
			},
			{
				Platform:   contract.PlatformIOS,
				DeviceType: "iPhone 16 Pro",
				Runtime:    "iOS 26.1",
				UDID:       "ios-2",
				Status:     contract.StatusReady,
			},
			{
				Platform:   contract.PlatformIOS,
				DeviceType: "iPhone 17",
				Runtime:    "iOS 26.4",
				UDID:       "ios-3",
				Status:     contract.StatusReady,
			},
			{
				Platform:      contract.PlatformAndroid,
				UDID:          "emulator-5554",
				Status:        contract.StatusReady,
				DeviceProfile: "pixel_7",
				SystemImage:   "system-images;android-34;google_apis;arm64-v8a",
			},
			{
				Platform: contract.PlatformAndroid,
				Physical: true,
				UDID:     "R5CT1234",
				Status:   contract.StatusReady,
			},
		},
	}

	cases := []struct {
		name    string
		ios     []contract.IosSpec
		android []contract.AndroidSpec
		want    bool
	}{
		{
			name: "same multiset regardless of requested order",
			ios: []contract.IosSpec{
				{Device: "iPhone 16 Pro", Runtime: "iOS 26.1", Count: 1},
				{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 2},
			},
			android: []contract.AndroidSpec{
				{
					Device: "pixel_7",
					Image:  "system-images;android-34;google_apis;arm64-v8a",
					Count:  1,
					Target: contract.TargetEmulator,
				},
				{Count: 1, Target: contract.TargetPhysical},
			},
			want: true,
		},
		{
			name: "ios count mismatch",
			ios: []contract.IosSpec{
				{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 1},
				{Device: "iPhone 16 Pro", Runtime: "iOS 26.1", Count: 1},
			},
			android: []contract.AndroidSpec{
				{
					Device: "pixel_7",
					Image:  "system-images;android-34;google_apis;arm64-v8a",
					Count:  1,
					Target: contract.TargetEmulator,
				},
				{Count: 1, Target: contract.TargetPhysical},
			},
			want: false,
		},
		{
			name: "android emulator image mismatch",
			ios: []contract.IosSpec{
				{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 2},
				{Device: "iPhone 16 Pro", Runtime: "iOS 26.1", Count: 1},
			},
			android: []contract.AndroidSpec{
				{
					Device: "pixel_7",
					Image:  "system-images;android-35;google_apis;arm64-v8a",
					Count:  1,
					Target: contract.TargetEmulator,
				},
				{Count: 1, Target: contract.TargetPhysical},
			},
			want: false,
		},
		{
			name: "physical devices compare by count only",
			ios: []contract.IosSpec{
				{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 2},
				{Device: "iPhone 16 Pro", Runtime: "iOS 26.1", Count: 1},
			},
			android: []contract.AndroidSpec{
				{
					Device: "pixel_7",
					Image:  "system-images;android-34;google_apis;arm64-v8a",
					Count:  1,
					Target: contract.TargetEmulator,
				},
				{Device: "ignored", Image: "ignored", Count: 2, Target: contract.TargetPhysical},
			},
			want: false,
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := specMatches(record, c.ios, c.android); got != c.want {
				t.Fatalf("specMatches() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestSpecMatchesRejectsNonReadyDevices(t *testing.T) {
	t.Parallel()

	record := contract.SquadRecord{
		Devices: []contract.Device{
			{
				Platform:   contract.PlatformIOS,
				DeviceType: "iPhone 17",
				Runtime:    "iOS 26.4",
				UDID:       "ios-1",
				Status:     contract.StatusReady,
			},
			{
				Platform:      contract.PlatformAndroid,
				UDID:          "emulator-5554",
				Status:        contract.StatusError,
				DeviceProfile: "pixel_7",
				SystemImage:   "system-images;android-36.1;google_apis_playstore;arm64-v8a",
			},
		},
	}

	got := specMatches(record,
		[]contract.IosSpec{{Device: "iPhone 17", Runtime: "iOS 26.4", Count: 1}},
		[]contract.AndroidSpec{{
			Device: "pixel_7",
			Image:  "system-images;android-36.1;google_apis_playstore;arm64-v8a",
			Count:  1,
			Target: contract.TargetEmulator,
		}},
	)
	if got {
		t.Fatal("specMatches() = true for a squad with an errored Android device")
	}
}

func TestHasReadyDevice(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		devs []contract.Device
		want bool
	}{
		{"empty squad (build failed before provisioning)", nil, false},
		{
			"only an errored slot with no udid",
			[]contract.Device{{Platform: contract.PlatformIOS, Status: contract.StatusError}},
			false,
		},
		{
			"ready but udid somehow empty",
			[]contract.Device{{Platform: contract.PlatformIOS, Status: contract.StatusReady}},
			false,
		},
		{
			"one ready device",
			[]contract.Device{{Platform: contract.PlatformIOS, Status: contract.StatusReady, UDID: "ios-1"}},
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := hasReadyDevice(contract.SquadRecord{Devices: c.devs}); got != c.want {
				t.Fatalf("hasReadyDevice() = %v, want %v", got, c.want)
			}
		})
	}
}
