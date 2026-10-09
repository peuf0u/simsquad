package util

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Two app repos sharing one derived-data tree let one project's build
// products (e.g. a source-built Lottie.swiftmodule) shadow another's binary
// framework at link time (issue #15), so each repo needs its own folder.
func TestIOSDerivedDataDirIsPerRepo(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", "/cache")

	a := IOSDerivedDataDir("/work/smsticket/ios")
	b := IOSDerivedDataDir("/work/bitplus/ios-app")
	if a == b {
		t.Fatalf("different repos share derived data dir %q", a)
	}
	for _, d := range []string{a, b} {
		if filepath.Dir(d) != filepath.Join("/cache", "derived", "ios") {
			t.Errorf("%q is not directly under the iOS derived root", d)
		}
	}
	if !strings.HasPrefix(filepath.Base(a), "ios-") {
		t.Errorf("%q should start with the repo basename", a)
	}
	if !strings.HasPrefix(filepath.Base(b), "ios-app-") {
		t.Errorf("%q should start with the repo basename", b)
	}
}

func TestIOSDerivedDataDirSameBasenameDiffers(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", "/cache")
	if IOSDerivedDataDir("/a/app") == IOSDerivedDataDir("/b/app") {
		t.Fatal("repos with the same basename must not collide")
	}
}

func TestIOSDerivedDataDirIsStableForSameRepo(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", "/cache")
	want := IOSDerivedDataDir("/work/smsticket")
	for _, p := range []string{"/work/smsticket", "/work/smsticket/", "/work/x/../smsticket"} {
		if got := IOSDerivedDataDir(p); got != want {
			t.Errorf("IOSDerivedDataDir(%q) = %q, want %q", p, got, want)
		}
	}
}

func TestIOSDerivedDataDirResolvesRelativeRepo(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", "/cache")
	t.Chdir(t.TempDir())
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := IOSDerivedDataDir("app"), IOSDerivedDataDir(filepath.Join(wd, "app")); got != want {
		t.Errorf("relative %q vs absolute %q", got, want)
	}
}

func TestIOSBuildLogPathLivesInRepoDerivedDir(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", "/cache")
	repo := "/work/smsticket"
	want := filepath.Join(IOSDerivedDataDir(repo), "last-build-ios.log")
	if got := IOSBuildLogPath(repo); got != want {
		t.Errorf("IOSBuildLogPath = %q, want %q", got, want)
	}
}
