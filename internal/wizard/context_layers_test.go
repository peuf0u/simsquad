package wizard

import (
	"path/filepath"
	"reflect"
	"testing"

	"github.com/BurntSushi/toml"

	"github.com/peuf0u/simsquad/internal/config"
	"github.com/peuf0u/simsquad/internal/contract"
)

const layeredProject = `[project]
ios_scheme = "App"
android_gradle_task = ":app:assembleDebug"

[context]
user = "alice"
api = "https://staging"
`

const layeredLocal = `[project]
ios_repo = "ios"

[context]
password = "hunter2"
api = "http://localhost"

[secrets]
pin = "4711"
`

// fileContext returns one file's own [context] table.
func fileContext(t *testing.T, path string) map[string]string {
	t.Helper()
	var doc struct {
		Context map[string]string `toml:"context"`
	}
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Context
}

func layeredDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ProjectFile), layeredProject)
	writeFile(t, filepath.Join(dir, config.LocalFile), layeredLocal)
	return dir
}

func assertContextFiles(t *testing.T, dir string, shared, local map[string]string) {
	t.Helper()
	if got := fileContext(t, filepath.Join(dir, config.ProjectFile)); !reflect.DeepEqual(got, shared) {
		t.Errorf("simsquad.toml [context] = %v, want %v", got, shared)
	}
	if got := fileContext(t, filepath.Join(dir, config.LocalFile)); !reflect.DeepEqual(got, local) {
		t.Errorf("simsquad.local.toml [context] = %v, want %v", got, local)
	}
}

// Saving keeps each [context] key in the file it came from: a local-only
// key (a password) stays out of the committed simsquad.toml, a local key
// over a shared one keeps both values in their files, a shared key stays
// shared, and [secrets] still goes to the local file.
func TestEquipSaveKeepsContextKeysInTheirFile(t *testing.T) {
	t.Parallel()
	dir := layeredDir(t)
	res, err := RunEquipWizard(Options{
		CWD:       dir,
		AddIOS:    []contract.IosSpec{{Device: "iPad Air 11-inch (M3)", Runtime: "iOS 26.4", Count: 1}},
		DeviceSet: "tablets",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Wrote {
		t.Fatal("equip --add-ios-spec wrote nothing")
	}
	assertContextFiles(t, dir,
		map[string]string{"user": "alice", "api": "https://staging"},
		map[string]string{"password": "hunter2", "api": "http://localhost"})
	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.Secrets(); !reflect.DeepEqual(got, map[string]string{"pin": "4711"}) || cfg.CommittedSecretKeys() != nil {
		t.Errorf("Secrets() = %v committed %v, want pin in the local file only", got, cfg.CommittedSecretKeys())
	}
}

// openContext moves the editor's cursor to "Edit context" and opens it.
func openContext(t *testing.T, m editorModel) editorModel {
	t.Helper()
	m.cursor = -1
	for i, item := range m.currentMenuItems() {
		if item.action == actionEditContext {
			m.cursor = i
		}
	}
	if m.cursor < 0 {
		t.Fatal("main menu has no Edit context action")
	}
	m = press(t, m, "enter")
	if m.mode != editorFields || m.inputKind != actionEditContext {
		t.Fatalf("mode %v kind %v, want the context fields", m.mode, m.inputKind)
	}
	return m
}

// setField sets the context screen's field for key (or the "Add new" field
// for key "") to value.
func setField(t *testing.T, m *editorModel, key, value string) {
	t.Helper()
	for i := range m.fields {
		if m.fields[i].label == key || (key == "" && m.fields[i].label == addKeyValueLabel) {
			m.fields[i].value = value
			return
		}
	}
	t.Fatalf("no context field %q", key)
}

// In the editor, an edited key stays in its file and a new key goes to
// simsquad.toml. Clearing a local key that overrides a shared one removes
// the override, so the shared value is back; clearing a local-only key
// removes it.
func TestBubbleEditorContextKeysKeepTheirFile(t *testing.T) {
	t.Parallel()
	dir := layeredDir(t)
	state, err := loadState(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	m := openContext(t, editorModel{configDir: dir, width: 120, mode: editorMenu, state: state})
	setField(t, &m, "user", "bob")
	setField(t, &m, "api", "")
	setField(t, &m, "password", "rotated")
	setField(t, &m, "", "region=eu")
	m.fieldIdx = len(m.fields)
	m = press(t, m, "enter")
	want := map[string]string{"user": "bob", "api": "https://staging", "password": "rotated", "region": "eu"}
	if !reflect.DeepEqual(m.state.testContext, want) {
		t.Fatalf("context after the edit = %v, want %v", m.state.testContext, want)
	}
	if _, err := writeState(dir, m.state, true); err != nil {
		t.Fatal(err)
	}
	assertContextFiles(t, dir,
		map[string]string{"user": "bob", "api": "https://staging", "region": "eu"},
		map[string]string{"password": "rotated"})

	// Next session: clear the local-only password.
	state, err = loadState(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	m = openContext(t, editorModel{configDir: dir, width: 120, mode: editorMenu, state: state})
	setField(t, &m, "password", "")
	m.fieldIdx = len(m.fields)
	m = press(t, m, "enter")
	if _, err := writeState(dir, m.state, true); err != nil {
		t.Fatal(err)
	}
	assertContextFiles(t, dir,
		map[string]string{"user": "bob", "api": "https://staging", "region": "eu"},
		nil)
}
