package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

// seedSecretSquad registers a squad whose context holds a plain user, a
// secret marked explicitly (pin, as from [secrets]) and one marked by its
// name (password), with one ready iOS device.
func seedSecretSquad(t *testing.T, name string) {
	t.Helper()
	rec := &contract.SquadRecord{
		Name:      name,
		CreatedAt: util.NowISO(),
		Devices: []contract.Device{{
			Platform: contract.PlatformIOS, Name: "simsquad-" + name + "-ios-0", UDID: "udid-1",
			DeviceType: "iPhone 17", Runtime: "iOS 26.4", Status: contract.StatusReady,
		}},
		Context:    map[string]string{"user": "alice", "pin": "4711", "password": "hunter2"},
		SecretKeys: []string{"pin"},
	}
	if err := state.SaveRecord(rec); err != nil {
		t.Fatal(err)
	}
	if err := state.RegisterSquad(state.RegisterSquadInput{Name: name, StateFile: util.StateFileFor(name)}); err != nil {
		t.Fatal(err)
	}
}

// assertMasked checks one output: secrets show as <secret>, the plain key
// keeps its value and no secret value appears anywhere.
func assertMasked(t *testing.T, what, out string) {
	t.Helper()
	for _, want := range []string{`"pin": "<secret>"`, `"password": "<secret>"`, `"user": "alice"`} {
		if !strings.Contains(out, want) {
			t.Errorf("%s lacks %s:\n%s", what, want, out)
		}
	}
	for _, secret := range []string{"4711", "hunter2"} {
		if strings.Contains(out, secret) {
			t.Errorf("%s shows the secret value %q:\n%s", what, secret, out)
		}
	}
}

// Every verb that prints a squad's context masks its secrets, on stdout and
// in the --out copy.
func TestOutputsMaskSecrets(t *testing.T) {
	for name, args := range map[string][]string{
		"status":      {"status", "--name", "sq"},
		"devices":     {"devices", "--name", "sq"},
		"devices all": {"devices"},
		"set-context": {"set-context", "--name", "sq", "--set", "stage=qa"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
			seedSecretSquad(t, "sq")
			path := filepath.Join(t.TempDir(), "out.json")
			stdout, code := runRoot(t, append(args, "--out", path)...)
			if code != 0 {
				t.Fatalf("%v: exit %d", args, code)
			}
			assertMasked(t, "stdout", stdout)
			file, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			assertMasked(t, "--out file", string(file))
		})
	}
}

// set-context keeps the real values in the record: masking happens at the
// output, never in storage.
func TestSetContextKeepsRealSecretValues(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSecretSquad(t, "sq")
	if _, code := runRoot(t, "set-context", "--name", "sq", "--set", "stage=qa"); code != 0 {
		t.Fatalf("set-context: exit %d", code)
	}
	rec := loadTestRecord(t, "sq")
	if rec.Context["pin"] != "4711" || rec.Context["password"] != "hunter2" || !rec.IsSecret("pin") {
		t.Fatalf("record context = %v secret keys %v, want real values and pin still secret", rec.Context, rec.SecretKeys)
	}
}
