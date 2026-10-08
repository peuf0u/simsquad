package contract

import (
	"reflect"
	"testing"
)

func TestNewDeviceViewCopiesEnv(t *testing.T) {
	t.Parallel()

	src := map[string]string{"user": "alice", "api": "staging"}
	view := NewDeviceView(Device{
		Platform:   PlatformIOS,
		UDID:       "udid-1",
		Name:       "simsquad-foo-ios-0",
		DeviceType: "iPhone 17",
		Runtime:    "iOS 26.4",
		Status:     StatusReady,
	}, "2026-05-26T10:00:00Z", src)

	if !reflect.DeepEqual(view.Env, src) {
		t.Fatalf("view.Env = %+v, want %+v", view.Env, src)
	}
	// Mutating the source must NOT affect the view — confirms the defensive
	// copy in NewDeviceView is actually a copy.
	src["user"] = "bob"
	if view.Env["user"] != "alice" {
		t.Fatalf("view.Env aliased the source map (view.Env[user] = %q)",
			view.Env["user"])
	}
}

func TestNewDeviceViewNilEnv(t *testing.T) {
	t.Parallel()

	view := NewDeviceView(Device{
		Platform: PlatformIOS,
		UDID:     "udid",
		Status:   StatusReady,
	}, "now", nil)
	if view.Env != nil {
		t.Fatalf("view.Env = %+v, want nil for empty input", view.Env)
	}
}

func TestSquadRecordPublicProjectsEnv(t *testing.T) {
	t.Parallel()

	rec := SquadRecord{
		Name: "test",
		Env:  map[string]string{"locale": "en_US"},
	}
	pub := rec.Public()
	if !reflect.DeepEqual(pub.Env, rec.Env) {
		t.Fatalf("Public().Env = %+v, want %+v", pub.Env, rec.Env)
	}
}

func TestAndroidOSVersionParsing(t *testing.T) {
	t.Parallel()

	cases := []struct {
		image, want string
	}{
		{"system-images;android-34;google_apis;arm64-v8a", "Android API 34"},
		{"system-images;android-30;default;x86_64", "Android API 30"},
		{"", ""},
		{"weird-input", "weird-input"},
	}
	for _, c := range cases {
		if got := androidOSVersion(c.image); got != c.want {
			t.Fatalf("androidOSVersion(%q) = %q, want %q", c.image, got, c.want)
		}
	}
}

func TestNewDeviceViewReadyAt(t *testing.T) {
	const created, stamped = "2026-01-01T00:00:00Z", "2026-02-02T00:00:00Z"
	cases := []struct {
		name string
		dev  Device
		want string
	}{
		{"device stamp wins", Device{Status: StatusReady, ReadyAt: stamped}, stamped},
		{"legacy record falls back", Device{Status: StatusReady}, created},
		{"not ready omits", Device{Status: StatusError, ReadyAt: stamped}, ""},
	}
	for _, tc := range cases {
		if got := NewDeviceView(tc.dev, created, nil).ReadyAt; got != tc.want {
			t.Errorf("%s: ReadyAt = %q, want %q", tc.name, got, tc.want)
		}
	}
}
