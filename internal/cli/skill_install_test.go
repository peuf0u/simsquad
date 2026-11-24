package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type skillInstallOut struct {
	Dir      string   `json:"dir"`
	Version  string   `json:"version"`
	Contract int      `json:"contract"`
	Files    []string `json:"files"`
}

// newBareDir chdirs into a fresh temporary folder with no simsquad config
// and returns its path.
func newBareDir(t *testing.T) string {
	t.Helper()
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// newAppRepo chdirs into a fresh temporary app repo, equipped with a
// minimal simsquad.toml so the skill commands find their root, and returns
// its path.
func newAppRepo(t *testing.T) string {
	t.Helper()
	dir := newBareDir(t)
	writeTestFile(t, filepath.Join(dir, "simsquad.toml"), "[project]\nios_scheme = \"App\"\n")
	return dir
}

// gitInit makes dir a git work tree.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v\n%s", dir, err, out)
	}
}

// runSkill executes `simsquad skill <args>` through the real root command
// with a fixed binary version.
func runSkill(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, _, err := runSkillIO(t, args...)
	return out, err
}

// runSkillIO is runSkill that also returns stderr.
func runSkillIO(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	prev := Version
	Version = "9.9.9"
	t.Cleanup(func() { Version = prev })
	root := NewRootCmd()
	out, errOut := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(errOut)
	root.SetArgs(append([]string{"skill"}, args...))
	err = root.Execute()
	return out.String(), errOut.String(), err
}

func mustInstall(t *testing.T, args ...string) skillInstallOut {
	t.Helper()
	raw, err := runSkill(t, append([]string{"install"}, args...)...)
	if err != nil {
		t.Fatalf("skill install %v: %v", args, err)
	}
	var got skillInstallOut
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("unmarshal: %v\nout=%s", err, raw)
	}
	return got
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func contains(xs []string, x string) bool {
	for _, v := range xs {
		if v == x {
			return true
		}
	}
	return false
}

func TestSkillInstallWritesBothSkillsAndLinksClaude(t *testing.T) {
	repo := newAppRepo(t)

	got := mustInstall(t)

	wantDir := filepath.Join(repo, ".agents", "skills")
	if got.Dir != wantDir {
		t.Errorf("dir = %q, want %q", got.Dir, wantDir)
	}
	if got.Version != "9.9.9" {
		t.Errorf("version = %q, want 9.9.9", got.Version)
	}
	if got.Contract != 4 {
		t.Errorf("contract = %d, want 4", got.Contract)
	}
	for _, skill := range []string{"simsquad", "simsquad-test"} {
		body := readFile(t, filepath.Join(wantDir, skill, "SKILL.md"))
		if !strings.HasPrefix(body, "---\nname: "+skill+"\n") {
			t.Errorf("%s SKILL.md lacks frontmatter naming it:\n%s", skill, body)
		}
		for _, want := range []string{"simsquad 9.9.9", "skill contract 4", "qa/README.md"} {
			if !strings.Contains(body, want) {
				t.Errorf("%s SKILL.md header lacks %q", skill, want)
			}
		}
		// Claude Code reaches the same file through .claude/skills.
		viaClaude := readFile(t, filepath.Join(repo, ".claude", "skills", skill, "SKILL.md"))
		if viaClaude != body {
			t.Errorf(".claude/skills/%s does not resolve to the installed skill", skill)
		}
		fi, err := os.Lstat(filepath.Join(repo, ".claude", "skills", skill))
		if err != nil || fi.Mode()&os.ModeSymlink == 0 {
			t.Errorf(".claude/skills/%s is not a symlink (err=%v)", skill, err)
		}
	}
	for _, want := range []string{
		".agents/skills/simsquad/SKILL.md",
		".agents/skills/simsquad-test/SKILL.md",
		".claude/skills/simsquad",
		".claude/skills/simsquad-test",
	} {
		if !contains(got.Files, want) {
			t.Errorf("files %v lacks %q", got.Files, want)
		}
	}
}

func TestSkillInstallRefusesHandEditedSkillUnlessForced(t *testing.T) {
	repo := newAppRepo(t)
	mustInstall(t)
	skillPath := filepath.Join(repo, ".agents", "skills", "simsquad-test", "SKILL.md")
	edited := readFile(t, skillPath) + "\nMy own notes.\n"
	if err := os.WriteFile(skillPath, []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := runSkill(t, "install")
	if err == nil {
		t.Fatal("install over a hand-edited skill succeeded; want refusal")
	}
	if !strings.Contains(err.Error(), ".agents/skills/simsquad-test/SKILL.md") ||
		!strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal should name the file and --force: %v", err)
	}
	if readFile(t, skillPath) != edited {
		t.Error("refused install still changed the hand-edited file")
	}

	mustInstall(t, "--force")
	if strings.Contains(readFile(t, skillPath), "My own notes.") {
		t.Error("--force did not overwrite the hand-edited file")
	}
	mustInstall(t) // pristine again: a plain re-run succeeds
}

func TestSkillInstallTreatsUnstampedExistingSkillAsHandEdited(t *testing.T) {
	repo := newAppRepo(t)
	skillPath := filepath.Join(repo, ".agents", "skills", "simsquad", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("my own simsquad skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := runSkill(t, "install"); err == nil {
		t.Fatal("install over an unstamped skill file succeeded; want refusal")
	}
}

func TestSkillInstallWritesWorkerPermissionsForClaudeAndCodex(t *testing.T) {
	repo := newAppRepo(t)
	settingsPath := filepath.Join(repo, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	existing := `{"model": "opus", "permissions": {"allow": ["Bash(make:*)"], "deny": ["Read(./.env)"]}}`
	if err := os.WriteFile(settingsPath, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	got := mustInstall(t)
	mustInstall(t) // merging twice must not duplicate entries

	var settings struct {
		Model       string `json:"model"`
		Permissions struct {
			Allow []string `json:"allow"`
			Deny  []string `json:"deny"`
		} `json:"permissions"`
	}
	if err := json.Unmarshal([]byte(readFile(t, settingsPath)), &settings); err != nil {
		t.Fatalf("settings.json: %v", err)
	}
	if settings.Model != "opus" || len(settings.Permissions.Deny) != 1 {
		t.Errorf("existing settings not preserved: %+v", settings)
	}
	wantAllow := []string{
		"Bash(make:*)",
		"Bash(mobilecli:*)",
		"Bash(simsquad reset:*)",
		"Bash(simsquad run validate:*)",
		"Bash(simsquad devices:*)",
		"Edit(.simsquad/runs/**)",
	}
	if strings.Join(settings.Permissions.Allow, "|") != strings.Join(wantAllow, "|") {
		t.Errorf("allow = %v, want %v", settings.Permissions.Allow, wantAllow)
	}

	rules := readFile(t, filepath.Join(repo, ".codex", "rules", "simsquad.rules"))
	for _, want := range []string{
		`prefix_rule(pattern = ["mobilecli"], decision = "allow")`,
		`prefix_rule(pattern = ["simsquad", "reset"], decision = "allow")`,
		`prefix_rule(pattern = ["simsquad", "run", "validate"], decision = "allow")`,
		`prefix_rule(pattern = ["simsquad", "devices"], decision = "allow")`,
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("codex rules lack %s:\n%s", want, rules)
		}
	}
	if n := strings.Count(rules, "prefix_rule("); n != 4 {
		t.Errorf("codex rules allow %d commands, want only mobilecli, simsquad reset, simsquad run validate and simsquad devices:\n%s", n, rules)
	}
	for _, want := range []string{".claude/settings.json", ".codex/rules/simsquad.rules"} {
		if !contains(got.Files, want) {
			t.Errorf("files %v lacks %q", got.Files, want)
		}
	}
}

func TestSkillInstallDirWritesSkillsToGivenDirectory(t *testing.T) {
	repo := newAppRepo(t)

	got := mustInstall(t, "--dir", "tools/pi-skills")

	wantDir := filepath.Join(repo, "tools", "pi-skills")
	if got.Dir != wantDir {
		t.Errorf("dir = %q, want %q", got.Dir, wantDir)
	}
	for _, skill := range []string{"simsquad", "simsquad-test"} {
		readFile(t, filepath.Join(wantDir, skill, "SKILL.md"))
		if !contains(got.Files, "tools/pi-skills/"+skill+"/SKILL.md") {
			t.Errorf("files %v lacks tools/pi-skills/%s/SKILL.md", got.Files, skill)
		}
	}
	if _, err := os.Stat(filepath.Join(repo, ".agents")); !os.IsNotExist(err) {
		t.Errorf("--dir install still wrote .agents/ (err=%v)", err)
	}
	if _, err := os.Lstat(filepath.Join(repo, ".claude", "skills")); !os.IsNotExist(err) {
		t.Errorf("--dir install still linked .claude/skills (err=%v)", err)
	}
	// Repo-level pieces are still written for the workers.
	readFile(t, filepath.Join(repo, ".gitignore"))
	readFile(t, filepath.Join(repo, ".claude", "settings.json"))
}

func TestSkillInstallIsIdempotentAndIgnoresRunArtifactsOnce(t *testing.T) {
	repo := newAppRepo(t)
	if err := os.WriteFile(filepath.Join(repo, ".gitignore"), []byte("build/"), 0o644); err != nil {
		t.Fatal(err)
	}

	first := mustInstall(t)
	second := mustInstall(t)

	if got := readFile(t, filepath.Join(repo, ".gitignore")); got != "build/\n.simsquad/\n" {
		t.Errorf(".gitignore = %q, want existing line plus one .simsquad/ line", got)
	}
	if !contains(second.Files, ".gitignore") {
		t.Errorf("files %v lacks .gitignore", second.Files)
	}
	if strings.Join(first.Files, ",") != strings.Join(second.Files, ",") {
		t.Errorf("re-run reported different files:\n%v\n%v", first.Files, second.Files)
	}
}

func TestSkillInstallScaffoldsAppNotesWhenMissing(t *testing.T) {
	repo := newAppRepo(t)

	got := mustInstall(t)

	notes := readFile(t, filepath.Join(repo, "qa", "README.md"))
	if !strings.Contains(notes, "App notes") {
		t.Errorf("qa/README.md scaffold has no App notes heading:\n%s", notes)
	}
	if strings.Contains(notes, "Generated by simsquad") {
		t.Error("qa/README.md belongs to the team and must not carry the generated-file header")
	}
	if !contains(got.Files, "qa/README.md") {
		t.Errorf("files %v lacks qa/README.md", got.Files)
	}
}

func TestSkillInstallNeverOverwritesAppNotes(t *testing.T) {
	repo := newAppRepo(t)
	notes := filepath.Join(repo, "qa", "README.md")
	if err := os.MkdirAll(filepath.Dir(notes), 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "# Our app\n\nLog in with alice@example.com.\n"
	if err := os.WriteFile(notes, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}

	mustInstall(t)
	mustInstall(t, "--force")

	if got := readFile(t, notes); got != mine {
		t.Errorf("qa/README.md was changed:\n%s", got)
	}
}

// TestSkillInstallRootsAtConfigDir: with the config in a repo subfolder,
// the skills land beside the config, where `run new` puts its runs, not at
// the git toplevel, from the config dir and from a folder below it.
func TestSkillInstallRootsAtConfigDir(t *testing.T) {
	for _, sub := range []string{"", "src"} {
		t.Run("cwd mobile/"+sub, func(t *testing.T) {
			base := newBareDir(t)
			gitInit(t, base)
			mobile := filepath.Join(base, "mobile")
			writeTestFile(t, filepath.Join(mobile, "simsquad.toml"), equipIOSOnly)
			writeTestFile(t, filepath.Join(mobile, "qa", "features", "login.feature"), loginFeature)
			cwd := filepath.Join(mobile, sub)
			if err := os.MkdirAll(cwd, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(cwd)

			got := mustInstall(t)

			if want := filepath.Join(mobile, ".agents", "skills"); got.Dir != want {
				t.Errorf("dir = %q, want %q", got.Dir, want)
			}
			readFile(t, filepath.Join(mobile, ".agents", "skills", "simsquad-test", "SKILL.md"))
			readFile(t, filepath.Join(mobile, "qa", "README.md"))
			if !strings.Contains(readFile(t, filepath.Join(mobile, ".gitignore")), ".simsquad/") {
				t.Error("mobile/.gitignore lacks .simsquad/")
			}
			if _, err := os.Stat(filepath.Join(base, ".agents")); !os.IsNotExist(err) {
				t.Errorf("skills also written at the git toplevel (err=%v)", err)
			}

			run := mustRunNew(t, filepath.Join(mobile, "qa", "features", "login.feature"))
			if want := filepath.Join(mobile, ".simsquad", "runs"); filepath.Dir(run.RunDir) != want {
				t.Errorf("run_dir = %q, want it under %q beside the skills", run.RunDir, want)
			}
		})
	}
}

func TestSkillInstallWithoutConfigRefusesWithEquipHint(t *testing.T) {
	dir := newBareDir(t)

	out, err := runSkill(t, "install")

	if err == nil {
		t.Fatal("skill install without config succeeded; want refusal")
	}
	if !strings.Contains(err.Error(), "run `simsquad equip` first") {
		t.Errorf("error = %v, want the equip hint", err)
	}
	if out != "" {
		t.Errorf("stdout must be empty on refusal, got %s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, ".agents")); !os.IsNotExist(err) {
		t.Errorf("refused install still wrote .agents/ (err=%v)", err)
	}
}

// notSharedWarning is the warning for a config dir outside git.
const notSharedWarning = "skills, app notes and feature files here won't be shared; see README › Project layouts"

// TestSkillInstallWarnsOutsideGit: a config dir outside a git work tree
// gets one stderr warning, and the install still succeeds.
func TestSkillInstallWarnsOutsideGit(t *testing.T) {
	t.Run("outside git", func(t *testing.T) {
		repo := newAppRepo(t)
		out, stderr, err := runSkillIO(t, "install")
		if err != nil {
			t.Fatalf("skill install: %v", err)
		}
		if n := strings.Count(stderr, notSharedWarning); n != 1 {
			t.Errorf("warning printed %d times, want once; stderr:\n%s", n, stderr)
		}
		if strings.Contains(out, "shared") {
			t.Errorf("warning leaked onto stdout:\n%s", out)
		}
		readFile(t, filepath.Join(repo, ".agents", "skills", "simsquad", "SKILL.md"))
	})
	t.Run("inside git", func(t *testing.T) {
		repo := newAppRepo(t)
		gitInit(t, repo)
		_, stderr, err := runSkillIO(t, "install")
		if err != nil {
			t.Fatalf("skill install: %v", err)
		}
		if strings.Contains(stderr, notSharedWarning) {
			t.Errorf("warned inside a git work tree:\n%s", stderr)
		}
	})
}
