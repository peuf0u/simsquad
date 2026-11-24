package util

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// A stale tmp file from a crashed write must not receive the secrets: it
// could be readable (0644) or a link to another file. WritePrivateJSON
// writes a fresh file instead of truncating it.
func TestWritePrivateJSONReplacesStaleTmp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "rec.json")
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(victim, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivateJSON(path, map[string]string{"password": "hunter2"}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "untouched" {
		t.Fatalf("the stale tmp's inode got the record: %q", b)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("record mode = %o, want 600", got)
	}
}

// WriteJSON leaves the mode to the umask, as os.WriteFile does: a user
// with a restrictive umask keeps it.
func TestWriteJSONHonoursUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	path := filepath.Join(t.TempDir(), "run.json")
	if err := WriteJSON(path, map[string]string{}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600 under umask 077", got)
	}
}
