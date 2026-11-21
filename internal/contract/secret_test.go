package contract

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestSecretByName(t *testing.T) {
	t.Parallel()
	for key, want := range map[string]bool{
		"password":       true,
		"Password":       true,
		"api_token":      true,
		"CLIENT_SECRET":  true,
		"secretAnswer":   true,
		"user":           false,
		"api":            false,
		"pass":           false,
		"tok":            false,
		"stage_password": true,
	} {
		if got := SecretByName(key); got != want {
			t.Errorf("SecretByName(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestMaskContext(t *testing.T) {
	t.Parallel()
	ctx := map[string]string{"user": "alice", "pin": "1234", "api_token": "t0k", "Password": "hunter2"}
	got := MaskContext(ctx, []string{"pin"})
	want := map[string]string{"user": "alice", "pin": SecretMask, "api_token": SecretMask, "Password": SecretMask}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("MaskContext = %v, want %v", got, want)
	}
	if ctx["pin"] != "1234" {
		t.Fatal("MaskContext changed its input")
	}
	if MaskContext(nil, nil) != nil || MaskContext(map[string]string{}, []string{"x"}) != nil {
		t.Fatal("MaskContext of an empty context should be nil")
	}
}

func TestSquadRecordPublicMasksSecrets(t *testing.T) {
	t.Parallel()
	rec := SquadRecord{
		Name:       "s",
		Context:    map[string]string{"user": "alice", "pin": "1234", "Password": "hunter2"},
		SecretKeys: []string{"pin"},
	}
	want := map[string]string{"user": "alice", "pin": SecretMask, "Password": SecretMask}
	if got := rec.Public().Context; !reflect.DeepEqual(got, want) {
		t.Fatalf("Public().Context = %v, want %v", got, want)
	}
	if rec.Context["pin"] != "1234" {
		t.Fatal("Public() changed the record's context")
	}
}

// A record written before secrets existed has no secret_keys; the name
// rule still masks its password without any migration.
func TestLegacyRecordPasswordMasked(t *testing.T) {
	t.Parallel()
	var rec SquadRecord
	if err := json.Unmarshal([]byte(`{"name":"old","created_at":"x","builds":{},"devices":[],"env":{"user":"alice","password":"hunter2"}}`), &rec); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"user": "alice", "password": SecretMask}
	if got := rec.Public().Context; !reflect.DeepEqual(got, want) {
		t.Fatalf("Public().Context = %v, want %v", got, want)
	}
}

func TestSquadRecordDeviceViews(t *testing.T) {
	t.Parallel()
	rec := SquadRecord{
		Name:       "s",
		CreatedAt:  "2026-05-26T10:00:00Z",
		Context:    map[string]string{"user": "alice", "pin": "1234"},
		SecretKeys: []string{"pin"},
	}
	d := Device{Platform: PlatformIOS, UDID: "u", Status: StatusReady}
	if got := rec.DeviceView(d).Context; !reflect.DeepEqual(got, map[string]string{"user": "alice", "pin": SecretMask}) {
		t.Fatalf("DeviceView context = %v, want pin masked", got)
	}
	if got := rec.RevealedDeviceView(d).Context; !reflect.DeepEqual(got, rec.Context) {
		t.Fatalf("RevealedDeviceView context = %v, want %v", got, rec.Context)
	}
}

func TestSquadRecordContextMutators(t *testing.T) {
	t.Parallel()
	var rec SquadRecord
	rec.SetContext("user", "alice", false)
	rec.SetContext("pin", "1234", true)
	rec.SetContext("zone", "eu", true)
	if !reflect.DeepEqual(rec.SecretKeys, []string{"pin", "zone"}) {
		t.Fatalf("SecretKeys = %v, want [pin zone]", rec.SecretKeys)
	}
	// Setting a secret again as plain context keeps it a secret: only
	// removing the key un-marks it.
	rec.SetContext("pin", "5678", false)
	if !rec.IsSecret("pin") || rec.Context["pin"] != "5678" {
		t.Fatalf("pin = %q secret %v, want 5678 still secret", rec.Context["pin"], rec.IsSecret("pin"))
	}
	rec.UnsetContext("pin")
	if _, ok := rec.Context["pin"]; ok || rec.IsSecret("pin") {
		t.Fatalf("after UnsetContext: context %v secret keys %v", rec.Context, rec.SecretKeys)
	}
	rec.ClearContext()
	if rec.Context != nil || rec.SecretKeys != nil {
		t.Fatalf("after ClearContext: context %v secret keys %v, want both nil", rec.Context, rec.SecretKeys)
	}
	rec.UnsetContext("missing") // no-op on an empty record
}

func TestSquadRecordSecretValues(t *testing.T) {
	t.Parallel()
	rec := SquadRecord{
		Context:    map[string]string{"user": "alice", "pin": "1234", "api_token": "t0k", "empty_secret": ""},
		SecretKeys: []string{"pin"},
	}
	got := rec.SecretValues()
	want := []string{"1234", "t0k"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SecretValues = %v, want %v", got, want)
	}
}
