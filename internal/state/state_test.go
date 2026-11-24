package state

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/util"
)

func TestUpdateRecordAppliesOnTopOfDisk(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	if err := SaveRecord(&contract.SquadRecord{Name: "s", Context: map[string]string{"user": "alice"}}); err != nil {
		t.Fatal(err)
	}
	// A writer that loaded the record earlier must not clobber later saves:
	// UpdateRecord reloads before applying.
	stale, err := LoadRecord("s")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UpdateRecord("s", func(r *contract.SquadRecord) error {
		r.Context["token"] = "x"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	got, err := UpdateRecord("s", func(r *contract.SquadRecord) error {
		r.Ports = map[string]int{"a": 5554}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Context["token"] != "x" || got.Context["user"] != "alice" || got.Ports["a"] != 5554 {
		t.Fatalf("merged record = %+v (stale snapshot %+v)", got, stale)
	}
	disk, _ := LoadRecord("s")
	if disk.Context["token"] != "x" || disk.Ports["a"] != 5554 {
		t.Fatalf("disk record = %+v", disk)
	}
}

func TestUpdateRecordSerialisesConcurrentWriters(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	if err := SaveRecord(&contract.SquadRecord{Name: "s"}); err != nil {
		t.Fatal(err)
	}
	const writers = 12
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := UpdateRecord("s", func(r *contract.SquadRecord) error {
				if r.Ports == nil {
					r.Ports = map[string]int{}
				}
				r.Ports[fmt.Sprintf("dev-%d", i)] = i
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	rec, _ := LoadRecord("s")
	if len(rec.Ports) != writers {
		t.Fatalf("ports = %v, want %d entries (a concurrent write was lost)", rec.Ports, writers)
	}
}

func TestUpdateRecordMissingRecord(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	called := false
	_, err := UpdateRecord("gone", func(*contract.SquadRecord) error { called = true; return nil })
	if !errors.Is(err, ErrNoRecord) || called {
		t.Fatalf("err %v called %v, want an error without applying", err, called)
	}
}

func TestUpdateRecordKeepsDiskOnError(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	if err := SaveRecord(&contract.SquadRecord{Name: "s", CreatedAt: "t0"}); err != nil {
		t.Fatal(err)
	}
	_, err := UpdateRecord("s", func(r *contract.SquadRecord) error {
		r.CreatedAt = "t1"
		return fmt.Errorf("nope")
	})
	if err == nil {
		t.Fatal("want the closure's error")
	}
	if rec, _ := LoadRecord("s"); rec.CreatedAt != "t0" {
		t.Fatalf("record saved despite error: %+v", rec)
	}
}

// Records hold secret values, so the cache root is created private and the
// record, the registry and the lock file are owner-only.
func TestCacheFilesArePrivate(t *testing.T) {
	root := filepath.Join(t.TempDir(), "fresh", "cache")
	t.Setenv("SIMSQUAD_CACHE_DIR", root)
	if err := RegisterSquad(RegisterSquadInput{Name: "s", StateFile: util.StateFileFor("s")}); err != nil {
		t.Fatal(err)
	}
	if err := SaveRecord(&contract.SquadRecord{Name: "s", Context: map[string]string{"password": "hunter2"}}); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]os.FileMode{
		root:                   0o700,
		util.SquadIndexPath():  0o600,
		util.StateFileFor("s"): 0o600,
		util.IndexLockPath():   0o600,
	} {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := fi.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", filepath.Base(path), got, want)
		}
	}
}

// A record first written by an older simsquad (0644) turns private on its
// next save.
func TestSaveRecordTightensAnOldRecord(t *testing.T) {
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	path := util.StateFileFor("s")
	if err := os.WriteFile(path, []byte(`{"name":"s"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".tmp", []byte(`stale`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveRecord(&contract.SquadRecord{Name: "s"}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Fatalf("record mode = %o, want 600", got)
	}
}
