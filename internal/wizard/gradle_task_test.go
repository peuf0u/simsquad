package wizard

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// featureRepo writes an Android repo under configDir/android with one
// dynamic feature module when withFeature is set.
func featureRepo(t *testing.T, withFeature bool) (configDir string) {
	t.Helper()
	configDir = t.TempDir()
	for _, m := range []string{"app", "home"} {
		if err := os.MkdirAll(filepath.Join(configDir, "android", m), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(configDir, "android", "app", "build.gradle.kts"),
		"plugins {\n    id(\"com.android.application\")\n}\n")
	if withFeature {
		writeFile(t, filepath.Join(configDir, "android", "home", "build.gradle.kts"),
			"plugins {\n    id(\"com.android.dynamic-feature\")\n}\n")
	}
	return configDir
}

func TestDefaultGradleTaskIsABundleTaskForFeatureModules(t *testing.T) {
	t.Parallel()
	if got := defaultGradleTask(featureRepo(t, true), "android"); got != ":app:bundleDebug" {
		t.Errorf("with a feature module: %q, want :app:bundleDebug", got)
	}
	if got := defaultGradleTask(featureRepo(t, false), "android"); got != ":app:assembleDebug" {
		t.Errorf("without: %q, want :app:assembleDebug", got)
	}
	if got := defaultGradleTask(t.TempDir(), ""); got != ":app:assembleDebug" {
		t.Errorf("no repo: %q, want :app:assembleDebug", got)
	}
}

// The editor's Gradle task opens a picker offering the matching bundle task
// when the repo has feature modules, and stays a text field otherwise.
func TestBubbleEditorGradleTaskOffersTheBundleTask(t *testing.T) {
	t.Parallel()
	open := func(configDir string) editorModel {
		model := editorModel{
			configDir: configDir,
			state:     wizardState{androidRepo: "android", gradleTask: ":app:assembleDevDebug"},
			mode:      editorMenu,
			cursor:    1, // Edit project settings.
		}
		next, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		model = next.(editorModel)
		model.fieldIdx = 3 // Gradle task.
		next, _ = model.Update(tea.KeyMsg{Type: tea.KeyEnter})
		return next.(editorModel)
	}

	model := open(featureRepo(t, true))
	if model.mode != editorSelect {
		t.Fatalf("mode = %v, want the picker", model.mode)
	}
	var values []string
	for _, c := range model.fields[3].choices {
		values = append(values, c.value)
	}
	if want := []string{":app:assembleDevDebug", ":app:bundleDevDebug"}; !reflect.DeepEqual(values, want) {
		t.Fatalf("choices = %v, want %v", values, want)
	}

	if model := open(featureRepo(t, false)); model.mode != editorValue {
		t.Fatalf("mode = %v, want the text field", model.mode)
	}
}
