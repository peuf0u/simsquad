package android

import (
	"os"
	"path/filepath"
	"testing"
)

// StageAPKSet gives a squad its own APK set: the build renames each new set
// over the repo's, and the squad's copy keeps the set its deploy installed.
// An existing copy (the squad's previous deploy) is replaced.
func TestStageAPKSetKeepsTheSetWhenTheRepoSetIsReplaced(t *testing.T) {
	repo, cache := t.TempDir(), t.TempDir()
	src := filepath.Join(repo, "app.apks")
	dst := filepath.Join(cache, "s.apks")
	writeFile(t, dst, "previous deploy")
	writeFile(t, src, "set 1")

	if err := StageAPKSet(src, dst); err != nil {
		t.Fatal(err)
	}
	writeFile(t, src+".tmp", "set 2")
	if err := os.Rename(src+".tmp", src); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "set 1" {
		t.Fatalf("squad's set = %q, want %q", b, "set 1")
	}
	if left, _ := filepath.Glob(filepath.Join(cache, "*.tmp")); len(left) != 0 {
		t.Fatalf("temporary files left: %v", left)
	}
}

func TestStageAPKSetFailsForAMissingSet(t *testing.T) {
	cache := t.TempDir()
	if err := StageAPKSet(filepath.Join(t.TempDir(), "gone.apks"), filepath.Join(cache, "s.apks")); err == nil {
		t.Fatal("err = nil, want an error")
	}
	if left, _ := os.ReadDir(cache); len(left) != 0 {
		t.Fatalf("cache holds %v, want nothing", left)
	}
}

// Where no link can be made (another file system) the copy is private, like
// the squad records next to it.
func TestCopyAPKSetWritesAPrivateCopy(t *testing.T) {
	src := filepath.Join(t.TempDir(), "app.apks")
	dst := filepath.Join(t.TempDir(), "s.apks")
	writeFile(t, src, "set 1")
	if err := copyAPKSet(src, dst); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "set 1" || info.Mode().Perm() != 0o600 {
		t.Fatalf("copy = %q, mode %v; want %q, 0600", b, info.Mode().Perm(), "set 1")
	}
}
