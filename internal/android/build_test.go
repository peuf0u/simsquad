package android

import (
	"path/filepath"
	"testing"
)

// The Gradle task decides both how the app is packaged and where Gradle
// writes it: the project path in front of the task name is the module dir.
func TestTaskModeAndOutputDir(t *testing.T) {
	cases := []struct {
		task string
		mode BuildMode
		dir  string
	}{
		{":app:assembleDebug", ModeAPK, "app/build/outputs/apk"},
		{":app:assembleDevDebug", ModeAPK, "app/build/outputs/apk"},
		{":mobile:assembleDebug", ModeAPK, "mobile/build/outputs/apk"},
		{":app:bundleDebug", ModeBundle, "app/build/outputs/bundle"},
		{":app:bundleDevDebug", ModeBundle, "app/build/outputs/bundle"},
		{":foo:bar:bundleX", ModeBundle, "foo/bar/build/outputs/bundle"},
		{":foo:assembleX", ModeAPK, "foo/build/outputs/apk"},
		// No project path: the app module, as before the task decided it.
		{"assembleDebug", ModeAPK, "app/build/outputs/apk"},
		{"bundleDebug", ModeBundle, "app/build/outputs/bundle"},
		{":assembleDebug", ModeAPK, "app/build/outputs/apk"},
		{"", ModeAPK, "app/build/outputs/apk"},
	}
	for _, c := range cases {
		if got := TaskMode(c.task); got != c.mode {
			t.Errorf("TaskMode(%q) = %v, want %v", c.task, got, c.mode)
		}
		if got := OutputDir(c.task); got != c.dir {
			t.Errorf("OutputDir(%q) = %q, want %q", c.task, got, c.dir)
		}
	}
}

// fakeAAPT2 installs an SDK whose aapt2 reports pkg for any APK.
func fakeAAPT2(t *testing.T, pkg string) {
	t.Helper()
	sdk := t.TempDir()
	t.Setenv("ANDROID_HOME", sdk)
	writeFile(t, filepath.Join(sdk, "build-tools", "35.0.0", "aapt2"),
		"#!/bin/sh\necho \"package: name='"+pkg+"' versionCode='1'\"\n")
}

// An app module not named app is found through the task's project path.
func TestBuildFindsTheAPKOfAModuleNotNamedApp(t *testing.T) {
	fakeAAPT2(t, "com.example.mobile")
	repo := t.TempDir()
	apk := filepath.Join(repo, "mobile", "build", "outputs", "apk", "debug", "mobile-debug.apk")
	writeFile(t, apk, "")
	writeFile(t, filepath.Join(repo, "app", "build", "outputs", "apk", "debug", "app-debug.apk"), "")

	res, err := Build(BuildOptions{Repo: repo, NoBuild: true, GradleTask: ":mobile:assembleDebug"})
	if err != nil {
		t.Fatal(err)
	}
	if res.APKPath != apk || res.BundleID != "com.example.mobile" {
		t.Fatalf("Build = %+v, want %s and com.example.mobile", res, apk)
	}
}
