package contract

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestNewDeviceViewCopiesContext(t *testing.T) {
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

	if !reflect.DeepEqual(view.Context, src) {
		t.Fatalf("view.Context = %+v, want %+v", view.Context, src)
	}
	// Mutating the source must NOT affect the view — confirms the defensive
	// copy in NewDeviceView is actually a copy.
	src["user"] = "bob"
	if view.Context["user"] != "alice" {
		t.Fatalf("view.Context aliased the source map (view.Context[user] = %q)",
			view.Context["user"])
	}
}

func TestNewDeviceViewNilContext(t *testing.T) {
	t.Parallel()

	view := NewDeviceView(Device{
		Platform: PlatformIOS,
		UDID:     "udid",
		Status:   StatusReady,
	}, "now", nil)
	if view.Context != nil {
		t.Fatalf("view.Context = %+v, want nil for empty input", view.Context)
	}
}

func TestSquadRecordPublicProjectsContext(t *testing.T) {
	t.Parallel()

	rec := SquadRecord{
		Name:    "test",
		Context: map[string]string{"locale": "en_US"},
	}
	pub := rec.Public()
	if !reflect.DeepEqual(pub.Context, rec.Context) {
		t.Fatalf("Public().Context = %+v, want %+v", pub.Context, rec.Context)
	}
}

// Records and deploy output written before 0.5.0 carry the context under
// "env"; reading them must keep it.
func TestLegacyEnvKeyReadsAsContext(t *testing.T) {
	t.Parallel()

	legacy := []byte(`{"name":"old","created_at":"","builds":{},"devices":[],"env":{"PERSONA":"alice"}}`)
	want := map[string]string{"PERSONA": "alice"}

	var rec SquadRecord
	if err := json.Unmarshal(legacy, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Name != "old" || !reflect.DeepEqual(rec.Context, want) {
		t.Fatalf("SquadRecord = %+v, want name old and context %v", rec, want)
	}
	var pub SquadPublic
	if err := json.Unmarshal(legacy, &pub); err != nil {
		t.Fatal(err)
	}
	if pub.Name != "old" || !reflect.DeepEqual(pub.Context, want) {
		t.Fatalf("SquadPublic = %+v, want name old and context %v", pub, want)
	}

	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), `"context":{"PERSONA":"alice"}`) || strings.Contains(string(out), `"env"`) {
		t.Fatalf("re-marshalled record = %s, want context and no env", out)
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
