package wizard

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/peuf0u/simsquad/internal/config"
	"github.com/peuf0u/simsquad/internal/contract"
)

const secretsProject = `[project]
ios_scheme = "App"
android_gradle_task = ":app:assembleDebug"

[context]
user = "alice"

[secrets]
api_key = "committed \"key\""

[[device_sets.tablet.ios.sims]]
device = "iPad Pro 13-inch (M5)"
runtime = "iOS 26.4"
count = 1
`

const secretsLocal = `[project]
ios_repo = "ios"

[secrets]
password = "hun\\ter2"
"pin code" = "4711"

[[device_sets.mine.android.sims]]
device = "physical"
image = "physical"
target = "physical"
count = 1
`

// Opening and saving equip never drops a secret: every [secrets] key keeps
// its value, all of them land in simsquad.local.toml (none in the committed
// file), and the device sets and plain context are untouched.
func TestEquipSaveRoundTripsSecrets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ProjectFile), secretsProject)
	writeFile(t, filepath.Join(dir, config.LocalFile), secretsLocal)
	setsBefore := snapshotSets(t, dir)

	state, err := loadState(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeState(dir, state, true); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"api_key": `committed "key"`, "password": `hun\ter2`, "pin code": "4711"}
	if got := cfg.Secrets(); !reflect.DeepEqual(got, want) {
		t.Fatalf("Secrets() after save = %v, want %v", got, want)
	}
	if keys := cfg.CommittedSecretKeys(); keys != nil {
		t.Fatalf("simsquad.toml still holds secrets %v after save", keys)
	}
	if ctx, _ := cfg.Context(); !reflect.DeepEqual(ctx, map[string]string{"user": "alice"}) {
		t.Fatalf("Context() after save = %v, want only user", ctx)
	}
	if after := snapshotSets(t, dir); !reflect.DeepEqual(setsBefore, after) {
		t.Fatalf("device sets changed by save:\nbefore %v\nafter  %v", setsBefore, after)
	}
	if project := readTestFile(t, filepath.Join(dir, config.ProjectFile)); strings.Contains(project, "4711") || strings.Contains(project, "committed") {
		t.Fatalf("simsquad.toml holds a secret value:\n%s", project)
	}
}

// equip --show (and equip's result) lists secrets with the context, masked.
func TestEquipShowMasksSecrets(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ProjectFile), secretsProject)
	writeFile(t, filepath.Join(dir, config.LocalFile), secretsLocal)
	res, err := RunEquipWizard(Options{CWD: dir, Show: true})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"user": "alice", "api_key": contract.SecretMask,
		"password": contract.SecretMask, "pin code": contract.SecretMask,
	}
	if !reflect.DeepEqual(res.Summary.Context, want) {
		t.Fatalf("Summary.Context = %v, want %v", res.Summary.Context, want)
	}
}

func secretsEditor() editorModel {
	return editorModel{
		configDir: "/tmp/app",
		width:     120,
		state: wizardState{
			iosScheme:   "App",
			gradleTask:  ":app:assembleDebug",
			testContext: map[string]string{"user": "alice"},
			secrets:     map[string]string{"password": "hunter2"},
		},
		mode: editorMenu,
	}
}

// openSecrets moves the editor's cursor to "Edit secrets" and opens it.
func openSecrets(t *testing.T, m editorModel) editorModel {
	t.Helper()
	m.cursor = -1
	for i, item := range m.currentMenuItems() {
		if item.action == actionEditSecrets {
			m.cursor = i
		}
	}
	if m.cursor < 0 {
		t.Fatal("main menu has no Edit secrets action")
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(editorModel)
	if m.mode != editorFields || m.inputKind != actionEditSecrets {
		t.Fatalf("mode %v kind %v, want the secrets fields", m.mode, m.inputKind)
	}
	return m
}

// The editor shows a secret as •••• and its real value only while that
// field is being edited.
func TestBubbleEditorMasksSecretUntilEdited(t *testing.T) {
	t.Parallel()
	m := openSecrets(t, secretsEditor())
	view := m.View()
	if strings.Contains(view, "hunter2") || !strings.Contains(view, "••••") {
		t.Fatalf("secrets screen shows the value or no mask:\n%s", view)
	}

	m.fieldIdx = 0
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(editorModel)
	if m.mode != editorValue || !strings.Contains(m.View(), "hunter2") {
		t.Fatalf("editing the secret: mode %v, want the real value in the input:\n%s", m.mode, m.View())
	}
	m.edit.SetValue("rotated")
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(editorModel)
	if view := m.View(); strings.Contains(view, "rotated") || !strings.Contains(view, "••••") {
		t.Fatalf("after the edit the value is shown again:\n%s", view)
	}

	// Save the screen: the new value lands in the secrets, not the context.
	m.fieldIdx = len(m.fields)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(editorModel)
	if m.state.secrets["password"] != "rotated" || m.state.testContext["password"] != "" {
		t.Fatalf("secrets %v context %v, want password rotated in secrets only", m.state.secrets, m.state.testContext)
	}
}

func TestBubbleEditorAddsAndRemovesSecrets(t *testing.T) {
	t.Parallel()
	m := openSecrets(t, secretsEditor())
	m.fields[0].value = ""                     // clear password: removes it
	m.fields[len(m.fields)-1].value = "otp=99" // the "Add new" field
	m.fieldIdx = len(m.fields)
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(editorModel)
	if !reflect.DeepEqual(m.state.secrets, map[string]string{"otp": "99"}) {
		t.Fatalf("secrets = %v, want only otp", m.state.secrets)
	}
}

// A key can't be both context and a secret, and a bad entry's message never
// shows the value typed.
func TestBubbleEditorSecretErrors(t *testing.T) {
	t.Parallel()
	for name, entry := range map[string]string{
		"context key": "user=hunter3",
		"no equals":   "hunter3",
		"empty key":   "=hunter3",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := openSecrets(t, secretsEditor())
			m.fields[len(m.fields)-1].value = entry
			m.fieldIdx = len(m.fields)
			next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
			m = next.(editorModel)
			if m.message == "" || strings.Contains(m.message, "hunter3") {
				t.Fatalf("message = %q, want an error without the value", m.message)
			}
			if _, ok := m.state.secrets["user"]; ok {
				t.Fatal("a context key was added as a secret")
			}
		})
	}
}

// The context screen refuses a key that is already a secret.
func TestBubbleEditorContextRejectsSecretKey(t *testing.T) {
	t.Parallel()
	m := secretsEditor()
	m.cursor = -1
	for i, item := range m.currentMenuItems() {
		if item.action == actionEditContext {
			m.cursor = i
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(editorModel)
	m.fields[len(m.fields)-1].value = "password=hunter3"
	m.fieldIdx = len(m.fields)
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(editorModel)
	if m.state.testContext["password"] != "" || m.message == "" || strings.Contains(m.message, "hunter3") {
		t.Fatalf("context %v message %q, want password refused without its value", m.state.testContext, m.message)
	}
}

// simsquad.local.toml holds [secrets], so equip writes it owner-only,
// tightening a file an older equip wrote 0644.
func TestEquipWritesLocalFilePrivate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, config.ProjectFile), secretsProject)
	writeFile(t, filepath.Join(dir, config.LocalFile), secretsLocal)
	if err := os.Chmod(filepath.Join(dir, config.LocalFile), 0o644); err != nil {
		t.Fatal(err)
	}
	state, err := loadState(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writeState(dir, state, true); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, config.LocalFile))
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("simsquad.local.toml mode = %o, want 600", got)
	}
}

// The context screen masks a key whose name marks it secret (a legacy
// password in plain [context]) until that field is edited.
func TestBubbleEditorContextMasksSecretNamedKey(t *testing.T) {
	t.Parallel()
	m := secretsEditor()
	m.state.testContext["Password"] = "hunter2"
	m.cursor = -1
	for i, item := range m.currentMenuItems() {
		if item.action == actionEditContext {
			m.cursor = i
		}
	}
	next, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(editorModel)
	view := m.View()
	if strings.Contains(view, "hunter2") || !strings.Contains(view, "••••") || !strings.Contains(view, "alice") {
		t.Fatalf("context screen: want Password masked and user shown:\n%s", view)
	}
	m.fieldIdx = 0 // Password sorts before user
	next, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = next.(editorModel)
	if m.mode != editorValue || !strings.Contains(m.View(), "hunter2") {
		t.Fatalf("editing Password: want the real value in the input:\n%s", m.View())
	}
}
