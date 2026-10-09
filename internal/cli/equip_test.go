package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

type equipOut struct {
	Written []string         `json:"written"`
	Skills  *skillInstallOut `json:"skills"`
}

// runEquip executes `simsquad equip <args>` through the real root command,
// with the skill-install offer answered by answer (nil means the offer must
// not be shown). It returns stdout and the error.
func runEquip(t *testing.T, answer *bool, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(append([]string{"equip"}, args...))

	prevVersion, prevOffer := Version, offerSkillInstall
	Version = "9.9.9"
	offerSkillInstall = func(w io.Writer) (bool, error) {
		if answer == nil {
			t.Error("skill install offer shown; want none")
			return false, nil
		}
		if w != io.Writer(errOut) {
			t.Error("offer does not render on the command's stderr")
		}
		return *answer, nil
	}
	t.Cleanup(func() { Version, offerSkillInstall = prevVersion, prevOffer })

	err := root.Execute()
	return out.String(), err
}

func decodeEquip(t *testing.T, raw string) equipOut {
	t.Helper()
	var got equipOut
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("equip stdout is not JSON: %v\n%s", err, raw)
	}
	return got
}

// tree maps every regular file and symlink under root to its contents (or
// link target), keyed by path relative to root.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			out[rel] = "-> " + target
		case fi.Mode().IsRegular():
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[rel] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// skillTree is tree minus the files equip itself owns.
func skillTree(t *testing.T, repo string) map[string]string {
	t.Helper()
	out := tree(t, repo)
	for k := range out {
		if strings.HasPrefix(k, "simsquad.") || k == ".gitignore" {
			delete(out, k)
		}
	}
	return out
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const equipSpec = "iPhone 16:iOS 26.1:1"

func TestEquipAcceptingOfferInstallsSkillsExactlyAsSkillInstall(t *testing.T) {
	// Reference: a plain `skill install` in its own repo.
	refRepo := newAppRepo(t)
	mustInstall(t)
	want := tree(t, refRepo)
	delete(want, ".gitignore") // shared with equip; checked below

	repo := newAppRepo(t)
	yes := true
	stdout, err := runEquip(t, &yes, "--add-ios-spec", equipSpec)
	if err != nil {
		t.Fatalf("equip: %v", err)
	}
	got := decodeEquip(t, stdout)
	if got.Skills == nil {
		t.Fatalf("equip stdout lacks skills result:\n%s", stdout)
	}
	if got.Skills.Dir != filepath.Join(repo, ".agents", "skills") || got.Skills.Version != "9.9.9" {
		t.Errorf("skills result = %+v", got.Skills)
	}
	if len(got.Written) != 2 {
		t.Errorf("equip result lost its written files: %v", got.Written)
	}

	have := skillTree(t, repo)
	if strings.Join(keys(have), "|") != strings.Join(keys(want), "|") {
		t.Fatalf("equip wrote %v\nskill install wrote %v", keys(have), keys(want))
	}
	for k, v := range want {
		if have[k] != v {
			t.Errorf("%s differs from skill install output", k)
		}
	}
	gi := readFile(t, filepath.Join(repo, ".gitignore"))
	if !strings.Contains(gi, ".simsquad/") || !strings.Contains(gi, "simsquad.local.toml") {
		t.Errorf(".gitignore lacks skill or equip entries:\n%s", gi)
	}
}

func TestEquipDecliningOfferWritesNoSkillFiles(t *testing.T) {
	repo := newAppRepo(t)
	no := false
	stdout, err := runEquip(t, &no, "--add-ios-spec", equipSpec)
	if err != nil {
		t.Fatalf("equip: %v", err)
	}
	if got := decodeEquip(t, stdout); got.Skills != nil {
		t.Errorf("declined offer still reported skills: %+v", got.Skills)
	}
	if left := keys(skillTree(t, repo)); len(left) != 0 {
		t.Errorf("declined offer wrote %v", left)
	}
	if strings.Contains(readFile(t, filepath.Join(repo, ".gitignore")), ".simsquad/") {
		t.Error("declined offer added the skill .gitignore entry")
	}
}

func TestEquipInstallSkillsFlagAnswersTheOfferWithoutPrompting(t *testing.T) {
	newAppRepo(t)
	stdout, err := runEquip(t, nil, "--add-ios-spec", equipSpec, "--install-skills")
	if err != nil {
		t.Fatalf("equip: %v", err)
	}
	if decodeEquip(t, stdout).Skills == nil {
		t.Error("--install-skills did not install")
	}

	repo := newAppRepo(t)
	stdout, err = runEquip(t, nil, "--add-ios-spec", equipSpec, "--install-skills=false")
	if err != nil {
		t.Fatalf("equip: %v", err)
	}
	if decodeEquip(t, stdout).Skills != nil || len(skillTree(t, repo)) != 0 {
		t.Error("--install-skills=false installed skills")
	}
}

func TestEquipDoesNotReofferSkillsAlreadyInstalled(t *testing.T) {
	newAppRepo(t)
	mustInstall(t)
	stdout, err := runEquip(t, nil, "--add-ios-spec", equipSpec)
	if err != nil {
		t.Fatalf("equip: %v", err)
	}
	if decodeEquip(t, stdout).Skills != nil {
		t.Error("equip reinstalled skills that were already current")
	}
}

func TestEquipShowNeverOffersSkills(t *testing.T) {
	newAppRepo(t)
	if _, err := runEquip(t, nil, "--show"); err != nil {
		t.Fatalf("equip --show: %v", err)
	}
}
