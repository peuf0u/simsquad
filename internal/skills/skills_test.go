package skills_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/peuf0u/simsquad/internal/skills"
)

func TestReadInstalledReportsNothingBeforeInstall(t *testing.T) {
	got, err := skills.ReadInstalled(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("ReadInstalled on an empty repo = %+v, want nil", got)
	}
}

func TestReadInstalledReturnsStampedVersionAndContract(t *testing.T) {
	root := t.TempDir()
	if _, err := skills.Install(skills.Options{Root: root, Version: "0.4.0"}); err != nil {
		t.Fatal(err)
	}

	got, err := skills.ReadInstalled(root, "")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.Version != "0.4.0" || got.Contract != skills.Contract || got.Edited {
		t.Fatalf("ReadInstalled = %+v, want version 0.4.0, contract %d, unedited", got, skills.Contract)
	}
}

func TestReadInstalledFlagsHandEditedSkill(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "custom")
	if _, err := skills.Install(skills.Options{Root: root, Dir: dir, Version: "0.4.0"}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "simsquad", "SKILL.md")
	b, _ := os.ReadFile(p)
	if err := os.WriteFile(p, append(b, "edit\n"...), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := skills.ReadInstalled(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || !got.Edited {
		t.Fatalf("ReadInstalled = %+v, want Edited", got)
	}
}

// TestReadInstalledChecksEveryGeneratedFile: hand edits and deletions are
// caught in any generated file of a skill, not only its SKILL.md.
func TestReadInstalledChecksEveryGeneratedFile(t *testing.T) {
	install := func(t *testing.T) (root, worker string) {
		root = t.TempDir()
		if _, err := skills.Install(skills.Options{Root: root, Version: "0.4.0"}); err != nil {
			t.Fatal(err)
		}
		return root, filepath.Join(root, skills.DefaultDir, "simsquad-test", "worker.md")
	}
	t.Run("edited", func(t *testing.T) {
		root, p := install(t)
		b, _ := os.ReadFile(p)
		if err := os.WriteFile(p, append(b, "edit\n"...), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := skills.ReadInstalled(root, "")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || !got.Edited {
			t.Fatalf("ReadInstalled = %+v, want Edited", got)
		}
	})
	t.Run("missing", func(t *testing.T) {
		root, p := install(t)
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		got, err := skills.ReadInstalled(root, "")
		if err != nil {
			t.Fatal(err)
		}
		if got == nil || len(got.Missing) != 1 || got.Missing[0] != "simsquad-test/worker.md" {
			t.Fatalf("ReadInstalled = %+v, want Missing [simsquad-test/worker.md]", got)
		}
	})
}
