package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

func seedSquadForEnvTest(t *testing.T, name string, env map[string]string) {
	t.Helper()
	rec := &contract.SquadRecord{
		Name:      name,
		CreatedAt: util.NowISO(),
		Devices:   []contract.Device{},
		Env:       env,
	}
	if err := state.SaveRecord(rec); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
	if err := state.RegisterSquad(state.RegisterSquadInput{
		Name:      name,
		StateFile: util.StateFileFor(name),
	}); err != nil {
		t.Fatalf("RegisterSquad: %v", err)
	}
}

func runSetEnvCmd(t *testing.T, args ...string) map[string]any {
	t.Helper()
	cmd := newSetEnvCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set-env %v: %v", args, err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal set-env output: %v\nout=%s", err, out.String())
	}
	return got
}

func TestSetEnvSetAddsKey(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForEnvTest(t, "set-target",
		map[string]string{"existing": "v"})

	got := runSetEnvCmd(t, "--name=set-target", "--set", "user=alice")
	env, _ := got["env"].(map[string]any)
	want := map[string]any{"existing": "v", "user": "alice"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env = %+v, want %+v", env, want)
	}
}

func TestSetEnvUnsetRemovesKey(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForEnvTest(t, "unset-target",
		map[string]string{"keep": "v", "drop": "v"})

	got := runSetEnvCmd(t, "--name=unset-target", "--unset", "drop")
	env, _ := got["env"].(map[string]any)
	if _, present := env["drop"]; present {
		t.Fatalf("drop still present after --unset: %+v", env)
	}
	if env["keep"] != "v" {
		t.Fatalf("keep dropped unexpectedly: %+v", env)
	}
}

func TestSetEnvClearWipes(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForEnvTest(t, "clear-target",
		map[string]string{"a": "1", "b": "2"})

	got := runSetEnvCmd(t, "--name=clear-target", "--clear")
	if env := got["env"]; env != nil {
		t.Fatalf("env not cleared: %v", env)
	}
}

func TestSetEnvClearThenSetReplaces(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForEnvTest(t, "replace-target",
		map[string]string{"old": "v"})

	got := runSetEnvCmd(t, "--name=replace-target", "--clear", "--set", "fresh=v")
	env, _ := got["env"].(map[string]any)
	want := map[string]any{"fresh": "v"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env = %+v, want %+v", env, want)
	}
}

func TestSetEnvRequiresMutation(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForEnvTest(t, "noop-target", nil)

	cmd := newSetEnvCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--name=noop-target"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("set-env with no mutations succeeded; want error")
	}
}

func TestSetEnvMalformedSetEntry(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForEnvTest(t, "bad-target", nil)

	cmd := newSetEnvCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--name=bad-target", "--set", "noequals"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("set-env with malformed --set succeeded; want error")
	}
}
