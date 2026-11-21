package android

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// bundleEnv gives a bundle build an SDK, a $PATH holding only what the test
// puts there, and a home without a debug keystore. It returns the $PATH dir
// and the home.
func bundleEnv(t *testing.T) (pathDir, home string) {
	t.Helper()
	t.Setenv("ANDROID_HOME", t.TempDir())
	t.Setenv("ANDROID_SDK_HOME", "")
	pathDir = t.TempDir()
	t.Setenv("PATH", pathDir+":/bin:/usr/bin")
	home = t.TempDir()
	t.Setenv("HOME", home)
	return pathDir, home
}

// fakeBundletool puts a bundletool on $PATH that logs every call to the
// returned file, reports pkg as the bundle's package and writes the APK set
// build-apks is asked for.
func fakeBundletool(t *testing.T, pathDir, pkg string) string {
	t.Helper()
	log := filepath.Join(t.TempDir(), "log")
	writeFile(t, filepath.Join(pathDir, "bundletool"), `#!/bin/sh
echo "bundletool $*" >> `+log+`
case "$1" in
  dump) echo `+pkg+` ;;
  build-apks) for a; do case "$a" in --output=*) : > "${a#--output=}" ;; esac; done ;;
esac
`)
	return log
}

func TestBundleBuildWithoutBundletoolNamesTheInstall(t *testing.T) {
	_, home := bundleEnv(t)
	writeFile(t, filepath.Join(home, ".android", "debug.keystore"), "")
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "app", "build", "outputs", "bundle", "debug", "app-debug.aab"), "")

	_, err := Build(BuildOptions{Repo: repo, NoBuild: true, GradleTask: ":app:bundleDebug"})
	if err == nil || !strings.Contains(err.Error(), "brew install bundletool") {
		t.Fatalf("err = %v, want the brew install bundletool hint", err)
	}
}

// bundletool signs the APK set with the debug keystore and leaves it
// unsigned when there is none, which fails every install; the build names
// the missing keystore instead.
func TestBundleBuildWithoutDebugKeystoreSaysHowToCreateIt(t *testing.T) {
	pathDir, _ := bundleEnv(t)
	fakeBundletool(t, pathDir, "com.example")
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "app", "build", "outputs", "bundle", "debug", "app-debug.aab"), "")

	_, err := Build(BuildOptions{Repo: repo, NoBuild: true, GradleTask: ":app:bundleDebug"})
	if err == nil || !strings.Contains(err.Error(), "debug.keystore") || !strings.Contains(err.Error(), "keytool") {
		t.Fatalf("err = %v, want the missing debug.keystore and the keytool command", err)
	}
}

// A bundle build reads the package from the newest bundle and turns that
// bundle into one APK set in local-testing mode, next to it.
func TestBundleBuildMakesTheAPKSetFromTheNewestBundle(t *testing.T) {
	pathDir, home := bundleEnv(t)
	writeFile(t, filepath.Join(home, ".android", "debug.keystore"), "")
	log := fakeBundletool(t, pathDir, "com.example.bundle")
	repo := t.TempDir()
	out := filepath.Join(repo, "app", "build", "outputs")
	old := filepath.Join(out, "bundle", "release", "app-release.aab")
	writeFile(t, old, "")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	aab := filepath.Join(out, "bundle", "devDebug", "app-dev-debug.aab")
	writeFile(t, aab, "")
	// An APK from an earlier assemble build is not what a bundle task made.
	writeFile(t, filepath.Join(out, "apk", "debug", "app-debug.apk"), "")

	res, err := Build(BuildOptions{Repo: repo, NoBuild: true, GradleTask: ":app:bundleDevDebug"})
	if err != nil {
		t.Fatal(err)
	}
	set := filepath.Join(out, "bundle", "devDebug", "app-dev-debug.apks")
	if res.APKSetPath != set || res.APKPath != "" || res.BundleID != "com.example.bundle" {
		t.Fatalf("Build = %+v, want APK set %s and com.example.bundle", res, set)
	}
	want := "bundletool dump manifest --bundle=" + aab + " --xpath=/manifest/@package\n" +
		"bundletool build-apks --bundle=" + aab + " --output=" + set + " --local-testing --overwrite\n"
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != want {
		t.Fatalf("bundletool calls =\n%s\nwant\n%s", b, want)
	}
}

// --no-build with a bundle task and no bundle yet says where it looked.
func TestBundleNoBuildWithoutABundleNamesTheFolder(t *testing.T) {
	pathDir, home := bundleEnv(t)
	writeFile(t, filepath.Join(home, ".android", "debug.keystore"), "")
	fakeBundletool(t, pathDir, "com.example")
	repo := t.TempDir()

	_, err := Build(BuildOptions{Repo: repo, NoBuild: true, GradleTask: ":mobile:bundleDebug"})
	want := filepath.Join(repo, "mobile", "build", "outputs", "bundle")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to name %s", err, want)
	}
}
