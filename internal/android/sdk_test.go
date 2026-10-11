package android

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fakeTool(t *testing.T, dir, name string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Dry run 002: Homebrew's avdmanager on $PATH won over the SDK's own copy and
// read Homebrew's SDK, which lacked the equipped system image.
func TestAVDManagerPrefersTheSDKsOwnCmdlineTools(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ANDROID_HOME", root)
	fakeTool(t, filepath.Join(root, "cmdline-tools", "9.0", "bin"), "avdmanager")
	want := fakeTool(t, filepath.Join(root, "cmdline-tools", "latest", "bin"), "avdmanager")
	pathDir := t.TempDir()
	fakeTool(t, pathDir, "avdmanager")
	t.Setenv("PATH", pathDir)

	got, err := AVDManager()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("AVDManager() = %s, want %s", got, want)
	}
	if env := AVDManagerEnv(got); env != nil {
		t.Errorf("AVDManagerEnv(SDK copy) = %v, want nil (inherit)", env)
	}
}

func TestAVDManagerOnPathIsPointedAtTheSDK(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ANDROID_HOME", root)
	pathDir := t.TempDir()
	want := fakeTool(t, pathDir, "avdmanager")
	t.Setenv("PATH", pathDir)
	t.Setenv("AVDMANAGER_OPTS", "-Xmx1g")

	got, err := AVDManager()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("AVDManager() = %s, want %s", got, want)
	}
	wantOpts := "AVDMANAGER_OPTS=-Xmx1g -Dcom.android.sdkmanager.toolsdir=" + filepath.Join(root, "cmdline-tools", "latest")
	var opts []string
	for _, kv := range AVDManagerEnv(got) {
		if strings.HasPrefix(kv, "AVDMANAGER_OPTS=") {
			opts = append(opts, kv)
		}
	}
	if len(opts) != 1 || opts[0] != wantOpts {
		t.Errorf("AVDMANAGER_OPTS entries = %v, want [%s]", opts, wantOpts)
	}
}

// An interrupted download leaves an empty image folder; dry run 002 equipped
// one by hand and deploy passed it to avdmanager, which rejected it.
func TestHasSystemImageNeedsAnInstalledImage(t *testing.T) {
	root := t.TempDir()
	t.Setenv("ANDROID_HOME", root)
	empty := filepath.Join(root, "system-images", "android-37.0", "google_apis", "arm64-v8a")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	if HasSystemImage("system-images;android-37.0;google_apis;arm64-v8a") {
		t.Error("an empty image folder counts as installed")
	}
	fakeTool(t, filepath.Join(root, "system-images", "android-36", "google_apis", "arm64-v8a"), "package.xml")
	if !HasSystemImage("system-images;android-36;google_apis;arm64-v8a") {
		t.Error("an image with package.xml doesn't count as installed")
	}
}
