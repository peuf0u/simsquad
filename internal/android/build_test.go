package android

import (
	"fmt"
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
  build-apks) for a; do case "$a" in --output=*) echo set > "${a#--output=}" ;; esac; done ;;
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
	log := fakeBundletool(t, pathDir, "com.example")
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "app", "build", "outputs", "bundle", "debug", "app-debug.aab"), "")

	_, err := Build(BuildOptions{Repo: repo, NoBuild: true, GradleTask: ":app:bundleDebug"})
	if err == nil || !strings.Contains(err.Error(), "debug.keystore") || !strings.Contains(err.Error(), "keytool") {
		t.Fatalf("err = %v, want the missing debug.keystore and the keytool command", err)
	}
	if b, _ := os.ReadFile(log); strings.Contains(string(b), "build-apks") {
		t.Fatalf("bundletool calls =\n%s\nwant no build-apks", b)
	}
}

// A bundle build reads the package from the task's own variant bundle and
// turns it into one APK set in local-testing mode, next to it.
func TestBundleBuildMakesTheAPKSetFromTheTasksVariant(t *testing.T) {
	pathDir, home := bundleEnv(t)
	writeFile(t, filepath.Join(home, ".android", "debug.keystore"), "")
	log := fakeBundletool(t, pathDir, "com.example.bundle")
	repo := t.TempDir()
	out := filepath.Join(repo, "app", "build", "outputs")
	aab := filepath.Join(out, "bundle", "devDebug", "app-dev-debug.aab")
	writeFile(t, aab, "")
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(aab, past, past); err != nil {
		t.Fatal(err)
	}
	// Newer bundles of other variants (a flavor switch, a release build) and
	// an APK from an assemble build are not what this task made.
	writeFile(t, filepath.Join(out, "bundle", "mockDebug", "app-mock-debug.aab"), "")
	writeFile(t, filepath.Join(out, "bundle", "release", "app-release.aab"), "")
	writeFile(t, filepath.Join(out, "apk", "debug", "app-debug.apk"), "")

	res, err := Build(BuildOptions{Repo: repo, NoBuild: true, GradleTask: ":app:bundleDevDebug"})
	if err != nil {
		t.Fatal(err)
	}
	set := filepath.Join(out, "bundle", "devDebug", "app-dev-debug.apks")
	if res.APKSetPath != set || res.APKPath != "" || res.BundleID != "com.example.bundle" {
		t.Fatalf("Build = %+v, want APK set %s and com.example.bundle", res, set)
	}
	b, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	calls := strings.Split(strings.TrimSpace(string(b)), "\n")
	if len(calls) != 2 || calls[0] != "bundletool dump manifest --bundle="+aab+" --xpath=/manifest/@package" {
		t.Fatalf("bundletool calls =\n%s\nwant dump manifest of %s, then build-apks", b, aab)
	}
	// The set is written under a name of its own and renamed into place, so
	// a deploy installing the set never reads one half written by another.
	var tmp string
	if _, err := fmt.Sscanf(calls[1], "bundletool build-apks --bundle="+aab+" --output=%s --local-testing --overwrite", &tmp); err != nil {
		t.Fatalf("build-apks call = %q: %v", calls[1], err)
	}
	if tmp == set || filepath.Dir(tmp) != filepath.Dir(set) {
		t.Fatalf("build-apks wrote %s, want a file of its own beside %s", tmp, set)
	}
	if got, err := os.ReadFile(set); err != nil || string(got) != "set\n" {
		t.Fatalf("APK set %s = %q, %v; want what build-apks wrote", set, got, err)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("temporary set %s left behind (%v)", tmp, err)
	}
}

// A bundle task without its variant folder fails naming that folder, even
// when another variant has a bundle.
func TestBundleBuildWithoutTheVariantsBundleNamesItsFolder(t *testing.T) {
	pathDir, home := bundleEnv(t)
	writeFile(t, filepath.Join(home, ".android", "debug.keystore"), "")
	fakeBundletool(t, pathDir, "com.example")
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "app", "build", "outputs", "bundle", "mockDebug", "app-mock-debug.aab"), "")

	_, err := Build(BuildOptions{Repo: repo, NoBuild: true, GradleTask: ":app:bundleDevDebug"})
	want := filepath.Join(repo, "app", "build", "outputs", "bundle", "devDebug")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to name %s", err, want)
	}
}

// AGP creates ~/.android/debug.keystore during a default-signed debug
// build, so the keystore is looked for after Gradle runs.
func TestBundleBuildFindsTheKeystoreGradleCreated(t *testing.T) {
	pathDir, home := bundleEnv(t)
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	fakeBundletool(t, pathDir, "com.example")
	repo := t.TempDir()
	writeFile(t, filepath.Join(repo, "gradlew"), `#!/bin/sh
mkdir -p "`+home+`/.android" app/build/outputs/bundle/debug
: > "`+home+`/.android/debug.keystore"
: > app/build/outputs/bundle/debug/app-debug.aab
`)

	res, err := Build(BuildOptions{Repo: repo, GradleTask: ":app:bundleDebug"})
	if err != nil {
		t.Fatal(err)
	}
	if res.APKSetPath == "" {
		t.Fatalf("Build = %+v, want an APK set", res)
	}
}

// --no-build with a bundle task and no bundle yet says where it looked.
func TestBundleNoBuildWithoutABundleNamesTheFolder(t *testing.T) {
	pathDir, home := bundleEnv(t)
	writeFile(t, filepath.Join(home, ".android", "debug.keystore"), "")
	fakeBundletool(t, pathDir, "com.example")
	repo := t.TempDir()

	_, err := Build(BuildOptions{Repo: repo, NoBuild: true, GradleTask: ":mobile:bundleDebug"})
	want := filepath.Join(repo, "mobile", "build", "outputs", "bundle", "debug")
	if err == nil || !strings.Contains(err.Error(), want) {
		t.Fatalf("err = %v, want it to name %s", err, want)
	}
}
