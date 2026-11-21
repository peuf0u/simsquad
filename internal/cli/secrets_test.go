package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
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

// deployIO runs the real `simsquad deploy` in-process and returns stdout,
// stderr and the error.
func deployIO(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	root := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(append([]string{"deploy"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

// newSecretsRepo is newDeployRepo for the iOS-only config with project
// appended to simsquad.toml and local, when given, as simsquad.local.toml.
func newSecretsRepo(t *testing.T, project, local string) {
	t.Helper()
	newDeployRepo(t, deployTomlIOSOnly+project)
	if local != "" {
		writeTestFile(t, "simsquad.local.toml", local)
	}
}

func TestDeployMasksSecretsFromConfig(t *testing.T) {
	newSecretsRepo(t, "\n[context]\nuser = \"alice\"\n", "[secrets]\npin = \"4711\"\npassword = \"hunter2\"\n")
	installFakeDeploy(t)

	path := filepath.Join(t.TempDir(), "deploy.json")
	stdout, stderr, err := deployIO(t, "--name", "s", "--out", path)
	if err != nil {
		t.Fatalf("deploy: %v\n%s", err, stderr)
	}
	assertMasked(t, "deploy stdout", stdout)
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	assertMasked(t, "deploy --out file", string(file))
	if strings.Contains(stderr, "4711") || strings.Contains(stderr, "hunter2") {
		t.Errorf("stderr shows a secret value:\n%s", stderr)
	}

	rec := loadTestRecord(t, "s")
	want := map[string]string{"user": "alice", "pin": "4711", "password": "hunter2"}
	if !reflect.DeepEqual(rec.Context, want) || !reflect.DeepEqual(rec.SecretKeys, []string{"password", "pin"}) {
		t.Fatalf("record context %v secret keys %v, want %v with password and pin secret", rec.Context, rec.SecretKeys, want)
	}
}

// --secret replaces TOML [secrets] wholesale and leaves [context] alone;
// --context replaces [context] and leaves [secrets] alone.
func TestDeploySecretAndContextSourcesAreSeparate(t *testing.T) {
	cases := []struct {
		name       string
		args       []string
		want       map[string]string
		wantSecret []string
	}{
		{"toml only", nil,
			map[string]string{"user": "alice", "pin": "4711"}, []string{"pin"}},
		{"--secret replaces [secrets]", []string{"--secret", "otp=999"},
			map[string]string{"user": "alice", "otp": "999"}, []string{"otp"}},
		{"--context replaces [context]", []string{"--context", "stage=qa"},
			map[string]string{"stage": "qa", "pin": "4711"}, []string{"pin"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newSecretsRepo(t, "\n[context]\nuser = \"alice\"\n", "[secrets]\npin = \"4711\"\n")
			installFakeDeploy(t)
			if _, stderr, err := deployIO(t, append([]string{"--name", "s"}, c.args...)...); err != nil {
				t.Fatalf("deploy: %v\n%s", err, stderr)
			}
			rec := loadTestRecord(t, "s")
			if !reflect.DeepEqual(rec.Context, c.want) || !reflect.DeepEqual(rec.SecretKeys, c.wantSecret) {
				t.Fatalf("context %v secret keys %v, want %v / %v", rec.Context, rec.SecretKeys, c.want, c.wantSecret)
			}
		})
	}
}

func TestDeployRejectsKeyInContextAndSecrets(t *testing.T) {
	cases := []struct {
		name           string
		project, local string
		args           []string
	}{
		{"both tables", "\n[context]\npin = \"1\"\n", "[secrets]\npin = \"4711\"\n", nil},
		{"both flags", "", "", []string{"--context", "pin=1", "--secret", "pin=4711"}},
		{"--context and [secrets]", "", "[secrets]\npin = \"4711\"\n", []string{"--context", "pin=1"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			newSecretsRepo(t, c.project, c.local)
			f := installFakeDeploy(t)
			_, _, err := deployIO(t, append([]string{"--name", "s"}, c.args...)...)
			if err == nil || !strings.Contains(err.Error(), `"pin"`) {
				t.Fatalf("err = %v, want an error naming pin", err)
			}
			if strings.Contains(err.Error(), "4711") {
				t.Fatalf("error shows the secret value: %v", err)
			}
			if f.iosBuilds != 0 {
				t.Fatal("deploy built after the context error")
			}
		})
	}
}

func TestDeployWarnsAboutCommittedSecrets(t *testing.T) {
	newSecretsRepo(t, "\n[secrets]\npin = \"4711\"\n", "")
	installFakeDeploy(t)
	_, stderr, err := deployIO(t, "--name", "s")
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	if !strings.Contains(stderr, "[secrets]") || !strings.Contains(stderr, "pin") || !strings.Contains(stderr, "simsquad.local.toml") {
		t.Fatalf("stderr lacks the committed-secrets warning naming pin:\n%s", stderr)
	}
	if strings.Contains(stderr, "4711") {
		t.Fatalf("warning shows the value:\n%s", stderr)
	}
}

// A malformed entry's error names the flag and the entry's position, never
// the entry: with no '=' the whole entry may be the value.
func TestMalformedEntriesKeepValuesOutOfErrors(t *testing.T) {
	cases := map[string][]string{
		"--context no equals":    {"deploy", "--name", "s", "--context", "hunter2"},
		"--context empty key":    {"deploy", "--name", "s", "--context", "=hunter2"},
		"--secret no equals":     {"deploy", "--name", "s", "--secret", "hunter2"},
		"--secret empty key":     {"deploy", "--name", "s", "--secret", " =hunter2"},
		"--set no equals":        {"set-context", "--name", "sq", "--set", "hunter2"},
		"--set empty key":        {"set-context", "--name", "sq", "--set", "=hunter2"},
		"--set-secret no equals": {"set-context", "--name", "sq", "--set-secret", "hunter2"},
		"--set-secret empty key": {"set-context", "--name", "sq", "--set-secret", "=hunter2"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			newDeployRepo(t, deployTomlIOSOnly)
			installFakeDeploy(t)
			seedSecretSquad(t, "sq")
			root := NewRootCmd()
			errOut := &bytes.Buffer{}
			root.SetOut(&bytes.Buffer{})
			root.SetErr(errOut)
			root.SetArgs(args)
			err := root.Execute()
			if err == nil {
				t.Fatalf("%v succeeded; want an error", args)
			}
			if strings.Contains(err.Error(), "hunter2") || strings.Contains(errOut.String(), "hunter2") {
				t.Fatalf("error shows the value: %v\n%s", err, errOut.String())
			}
			if !strings.Contains(err.Error(), "entry 1") {
				t.Errorf("error %q does not point at the entry", err)
			}
		})
	}
}

func TestSetContextSetSecret(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSecretSquad(t, "sq")
	stdout, code := runRoot(t, "set-context", "--name", "sq", "--set-secret", "otp=999", "--set", "stage=qa")
	if code != 0 {
		t.Fatalf("set-context: exit %d", code)
	}
	if !strings.Contains(stdout, `"otp": "<secret>"`) || !strings.Contains(stdout, `"stage": "qa"`) || strings.Contains(stdout, "999") {
		t.Fatalf("stdout = %s, want otp masked and stage plain", stdout)
	}
	rec := loadTestRecord(t, "sq")
	if rec.Context["otp"] != "999" || !reflect.DeepEqual(rec.SecretKeys, []string{"otp", "pin"}) {
		t.Fatalf("record context %v secret keys %v", rec.Context, rec.SecretKeys)
	}
}

func TestSetContextUnsetAndClearWorkOnSecrets(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSecretSquad(t, "sq")
	if _, code := runRoot(t, "set-context", "--name", "sq", "--unset", "pin"); code != 0 {
		t.Fatalf("--unset: exit %d", code)
	}
	rec := loadTestRecord(t, "sq")
	if _, ok := rec.Context["pin"]; ok || rec.SecretKeys != nil {
		t.Fatalf("after --unset pin: context %v secret keys %v", rec.Context, rec.SecretKeys)
	}
	// A key set again as plain context after the unset is no longer secret.
	if _, code := runRoot(t, "set-context", "--name", "sq", "--set", "pin=1"); code != 0 {
		t.Fatalf("--set: exit %d", code)
	}
	if rec := loadTestRecord(t, "sq"); rec.IsSecret("pin") {
		t.Fatal("pin still secret after unset and plain set")
	}
	if _, code := runRoot(t, "set-context", "--name", "sq", "--set-secret", "otp=9", "--clear"); code != 0 {
		t.Fatalf("--clear: exit %d", code)
	}
	rec = loadTestRecord(t, "sq")
	if !reflect.DeepEqual(rec.Context, map[string]string{"otp": "9"}) || !reflect.DeepEqual(rec.SecretKeys, []string{"otp"}) {
		t.Fatalf("after --clear --set-secret: context %v secret keys %v", rec.Context, rec.SecretKeys)
	}
}

func TestSetContextRejectsKeyInSetAndSetSecret(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSecretSquad(t, "sq")
	cmd := newSetContextCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--name", "sq", "--set", "otp=1", "--set-secret", "otp=999"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `"otp"`) || strings.Contains(err.Error(), "999") {
		t.Fatalf("err = %v, want an error naming otp without its value", err)
	}
}
