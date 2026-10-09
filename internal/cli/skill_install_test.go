package cli

import (
	"bytes"
	"encoding/json"
	"os"
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

// newAppRepo chdirs into a fresh temporary app repo and returns its path.
func newAppRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("SIMSQUAD_CACHE_DIR", t.TempDir())
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	return dir
}

// runSkill executes `simsquad skill <args>` through the real root command
// with a fixed binary version.
func runSkill(t *testing.T, args ...string) (string, error) {
	t.Helper()
	prev := Version
	Version = "9.9.9"
	t.Cleanup(func() { Version = prev })
	root := NewRootCmd()
	out := &bytes.Buffer{}
	root.SetOut(out)
	root.SetErr(&bytes.Buffer{})
	root.SetArgs(append([]string{"skill"}, args...))
	err := root.Execute()
	return out.String(), err
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
	if got.Contract != 1 {
		t.Errorf("contract = %d, want 1", got.Contract)
	}
	for _, skill := range []string{"simsquad", "simsquad-test"} {
		body := readFile(t, filepath.Join(wantDir, skill, "SKILL.md"))
		if !strings.HasPrefix(body, "---\nname: "+skill+"\n") {
			t.Errorf("%s SKILL.md lacks frontmatter naming it:\n%s", skill, body)
		}
		for _, want := range []string{"simsquad 9.9.9", "skill contract 1", "qa/README.md"} {
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
		"Bash(simsquad:*)",
		"Edit(/.simsquad/runs/**)",
		"Write(/.simsquad/runs/**)",
	}
	if strings.Join(settings.Permissions.Allow, "|") != strings.Join(wantAllow, "|") {
		t.Errorf("allow = %v, want %v", settings.Permissions.Allow, wantAllow)
	}

	rules := readFile(t, filepath.Join(repo, ".codex", "rules", "simsquad.rules"))
	for _, want := range []string{
		`prefix_rule(pattern = ["mobilecli"], decision = "allow")`,
		`prefix_rule(pattern = ["simsquad"], decision = "allow")`,
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("codex rules lack %s:\n%s", want, rules)
		}
	}
	if n := strings.Count(rules, "prefix_rule("); n != 2 {
		t.Errorf("codex rules allow %d commands, want only mobilecli and simsquad:\n%s", n, rules)
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
