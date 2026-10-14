package cli

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/util"
)

func seedSquadForContextTest(t *testing.T, name string, ctx map[string]string) {
	t.Helper()
	rec := &contract.SquadRecord{
		Name:      name,
		CreatedAt: util.NowISO(),
		Devices:   []contract.Device{},
		Context:   ctx,
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

func runSetContextCmd(t *testing.T, args ...string) map[string]any {
	t.Helper()
	cmd := newSetContextCmd()
	out := &bytes.Buffer{}
	cmd.SetOut(out)
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set-context %v: %v", args, err)
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal set-context output: %v\nout=%s", err, out.String())
	}
	return got
}

func TestSetContextSetAddsKey(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForContextTest(t, "set-target",
		map[string]string{"existing": "v"})

	got := runSetContextCmd(t, "--name=set-target", "--set", "user=alice")
	env, _ := got["context"].(map[string]any)
	want := map[string]any{"existing": "v", "user": "alice"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env = %+v, want %+v", env, want)
	}
}

func TestSetContextUnsetRemovesKey(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForContextTest(t, "unset-target",
		map[string]string{"keep": "v", "drop": "v"})

	got := runSetContextCmd(t, "--name=unset-target", "--unset", "drop")
	env, _ := got["context"].(map[string]any)
	if _, present := env["drop"]; present {
		t.Fatalf("drop still present after --unset: %+v", env)
	}
	if env["keep"] != "v" {
		t.Fatalf("keep dropped unexpectedly: %+v", env)
	}
}

func TestSetContextClearWipes(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForContextTest(t, "clear-target",
		map[string]string{"a": "1", "b": "2"})

	got := runSetContextCmd(t, "--name=clear-target", "--clear")
	if env := got["context"]; env != nil {
		t.Fatalf("env not cleared: %v", env)
	}
}

func TestSetContextClearThenSetReplaces(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForContextTest(t, "replace-target",
		map[string]string{"old": "v"})

	got := runSetContextCmd(t, "--name=replace-target", "--clear", "--set", "fresh=v")
	env, _ := got["context"].(map[string]any)
	want := map[string]any{"fresh": "v"}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env = %+v, want %+v", env, want)
	}
}

func TestSetContextRequiresMutation(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForContextTest(t, "noop-target", nil)

	cmd := newSetContextCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--name=noop-target"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("set-context with no mutations succeeded; want error")
	}
}

func TestSetContextMalformedSetEntry(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForContextTest(t, "bad-target", nil)

	cmd := newSetContextCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{"--name=bad-target", "--set", "noequals"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("set-context with malformed --set succeeded; want error")
	}
}

func TestSetEnvAliasWarnsAndWritesContext(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	seedSquadForContextTest(t, "alias-target", nil)

	cmd := newSetEnvCmd()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs([]string{"--name=alias-target", "--set", "user=alice"})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("set-env: %v", err)
	}
	if !strings.Contains(errOut.String(), "deprecated") {
		t.Errorf("stderr = %q, want a deprecation warning", errOut.String())
	}
	var got map[string]any
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, out.String())
	}
	if ctx, _ := got["context"].(map[string]any); ctx["user"] != "alice" {
		t.Fatalf("context = %v, want user=alice", got["context"])
	}
}
