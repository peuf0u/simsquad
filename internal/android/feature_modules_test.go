package android

import (
	"bytes"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/progress"
)

// A module counts as a dynamic feature when its build file applies the
// plugin by id, by the legacy apply syntax or by a version-catalog alias
// (as the Národní muzeum app did). Declaring it with apply false (the root
// build file) and files under build/ don't count.
func TestDynamicFeatureModules(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "settings.gradle.kts"), "")
	writeFile(t, filepath.Join(repo, "gradle", "libs.versions.toml"), `[versions]
agp = "8.5.0"

[plugins]
android-application = { id = "com.android.application", version.ref = "agp" }
android-dynamic-feature = { id = "com.android.dynamic-feature", version.ref = "agp" }
dfm_short = "com.android.dynamic-feature:8.5.0"
`)
	writeFile(t, filepath.Join(repo, "build.gradle.kts"), `plugins {
    alias(libs.plugins.android.application) apply false
    alias(libs.plugins.android.dynamic.feature) apply false
    id("com.android.dynamic-feature") version "8.5.0" apply false
}
`)
	writeFile(t, filepath.Join(repo, "app", "build.gradle.kts"),
		"plugins {\n    alias(libs.plugins.android.application)\n}\n")
	writeFile(t, filepath.Join(repo, "byid", "build.gradle.kts"),
		"plugins {\n    id(\"com.android.dynamic-feature\")\n}\n")
	writeFile(t, filepath.Join(repo, "legacy", "build.gradle"),
		"apply plugin: 'com.android.dynamic-feature'\n")
	writeFile(t, filepath.Join(repo, "features", "home", "build.gradle.kts"),
		"plugins {\n    alias(libs.plugins.android.dynamic.feature)\n    id(\"nm.android.dynamic-feature\")\n}\n")
	writeFile(t, filepath.Join(repo, "short", "build.gradle.kts"),
		"plugins {\n    alias(libs.plugins.dfm.short)\n}\n")
	writeFile(t, filepath.Join(repo, "commented", "build.gradle.kts"),
		"plugins {\n    // id(\"com.android.dynamic-feature\")\n    id(\"com.android.library\")\n}\n")
	writeFile(t, filepath.Join(repo, "app", "build", "tmp", "build.gradle.kts"),
		"plugins {\n    id(\"com.android.dynamic-feature\")\n}\n")

	got := DynamicFeatureModules(repo)
	want := []string{"byid", "features/home", "legacy", "short"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("DynamicFeatureModules = %v, want %v", got, want)
	}
}

// Only a Gradle root is searched. equip asks on every prefix typed into the
// repo field, and walking / or the home folder would be slow and trip macOS
// privacy prompts.
func TestDynamicFeatureModulesSearchesOnlyAGradleRoot(t *testing.T) {
	parent := t.TempDir()
	writeFile(t, filepath.Join(parent, "android", "home", "build.gradle.kts"),
		"plugins {\n    id(\"com.android.dynamic-feature\")\n}\n")
	if got := DynamicFeatureModules(parent); len(got) != 0 {
		t.Fatalf("DynamicFeatureModules(not a Gradle root) = %v, want none", got)
	}
	for _, marker := range []string{"settings.gradle", "settings.gradle.kts", "gradlew"} {
		repo := t.TempDir()
		writeFile(t, filepath.Join(repo, marker), "")
		writeFile(t, filepath.Join(repo, "home", "build.gradle.kts"),
			"plugins {\n    id(\"com.android.dynamic-feature\")\n}\n")
		if got := DynamicFeatureModules(repo); len(got) != 1 {
			t.Errorf("with %s: DynamicFeatureModules = %v, want [home]", marker, got)
		}
	}
}

func TestDynamicFeatureModulesNoneInAPlainRepo(t *testing.T) {
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "app", "build.gradle.kts"),
		"plugins {\n    id(\"com.android.application\")\n}\n")
	if got := DynamicFeatureModules(repo); len(got) != 0 {
		t.Fatalf("DynamicFeatureModules = %v, want none", got)
	}
}

// An assemble build of a repo with feature modules installs only the base
// APK; the build warns and names the fix, and still succeeds.
func TestAPKBuildWarnsAboutDynamicFeatureModules(t *testing.T) {
	fakeAAPT2(t, "com.example")
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "app", "build", "outputs", "apk", "debug", "app-debug.apk"), "")
	writeFile(t, filepath.Join(repo, "settings.gradle"), "")
	writeFile(t, filepath.Join(repo, "home", "build.gradle.kts"),
		"plugins {\n    id(\"com.android.dynamic-feature\")\n}\n")

	var stderr bytes.Buffer
	logger := progress.NewWith(&stderr)
	res, err := Build(BuildOptions{Repo: repo, NoBuild: true, GradleTask: ":app:assembleDevDebug", Logger: logger})
	logger.Close()
	if err != nil || res.APKPath == "" {
		t.Fatalf("Build = %+v, %v; want the APK", res, err)
	}
	out := stderr.String()
	if !strings.Contains(out, "home") || !strings.Contains(out, ":app:bundleDevDebug") {
		t.Fatalf("stderr = %q, want a warning naming home and :app:bundleDevDebug", out)
	}
}

func TestBundleTaskFor(t *testing.T) {
	cases := map[string]string{
		":app:assembleDevDebug":  ":app:bundleDevDebug",
		":app:assembleDebug":     ":app:bundleDebug",
		":mobile:assembleDebug":  ":mobile:bundleDebug",
		":foo:bar:assembleDebug": ":foo:bar:bundleDebug",
		"assembleStagingDebug":   ":app:bundleStagingDebug",
		":app:bundleDevDebug":    ":app:bundleDevDebug",
		":app:installDebug":      ":app:bundleDebug",
		"":                       ":app:bundleDebug",
	}
	for task, want := range cases {
		if got := BundleTaskFor(task); got != want {
			t.Errorf("BundleTaskFor(%q) = %q, want %q", task, got, want)
		}
	}
}
