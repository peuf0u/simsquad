package skills_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/peuf0u/simsquad/internal/cli"
	"github.com/peuf0u/simsquad/internal/skills"
)

// installedEverydaySkill installs the skills into a temp repo and returns the
// simsquad (everyday) skill as an agent would read it.
func installedEverydaySkill(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if _, err := skills.Install(skills.Options{Root: root, Version: "0.4.0"}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(root, skills.DefaultDir, "simsquad", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var invocationRe = regexp.MustCompile(`simsquad ([a-z-]+)((?: [^>\n]*)?)(>?)`)

// skillInvocations returns every `simsquad <verb> …` command line inside
// the skill's fenced code blocks.
func skillInvocations(skill string) []string {
	var out []string
	inFence := false
	for _, line := range strings.Split(skill, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inFence = !inFence
			continue
		}
		if inFence && strings.Contains(line, "simsquad ") {
			out = append(out, strings.TrimSpace(line))
		}
	}
	return out
}

func TestEverydaySkillCoversDevSquadCommands(t *testing.T) {
	skill := installedEverydaySkill(t)
	invocations := strings.Join(skillInvocations(skill), "\n")

	for _, verb := range []string{"deploy", "devices", "reset", "status", "set-env", "dismiss"} {
		if !strings.Contains(invocations, "simsquad "+verb+" ") {
			t.Errorf("skill has no `simsquad %s` example", verb)
		}
	}
	for _, want := range []string{"dev-<repo>", "dev_squad"} {
		if !strings.Contains(skill, want) {
			t.Errorf("skill does not mention %q", want)
		}
	}
}

func TestEverydaySkillWritesJSONWithOutNotRedirection(t *testing.T) {
	skill := installedEverydaySkill(t)
	for _, line := range shellLines(skill) {
		if redirectRe.MatchString(line) {
			t.Errorf("skill redirects output: %s", line)
		}
	}
	inv := skillInvocations(skill)
	if len(inv) == 0 {
		t.Fatal("skill has no command examples")
	}
	for _, line := range inv {
		if !strings.Contains(line, " --out ") {
			t.Errorf("example does not write its JSON with --out: %s", line)
		}
	}
}

func TestEverydaySkillHandsVerificationToSimsquadTest(t *testing.T) {
	skill := installedEverydaySkill(t)
	if !regexp.MustCompile(`(?i)verif[^\n]*simsquad-test`).MatchString(skill) {
		t.Error("skill does not hand verification requests to the simsquad-test skill")
	}
}

// Every flag the skill shows must exist on that verb, so the skill never
// teaches a command the binary rejects.
func TestEverydaySkillUsesOnlyRealFlags(t *testing.T) {
	skill := installedEverydaySkill(t)
	root := cli.NewRootCmd()
	flagRe := regexp.MustCompile(`--([a-z][a-z-]*)`)
	for _, line := range skillInvocations(skill) {
		m := invocationRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		cmd, _, err := root.Find([]string{m[1]})
		if err != nil || cmd == root {
			t.Errorf("unknown verb %q in: %s", m[1], line)
			continue
		}
		for _, f := range flagRe.FindAllStringSubmatch(m[2], -1) {
			if cmd.Flags().Lookup(f[1]) == nil && cmd.InheritedFlags().Lookup(f[1]) == nil {
				t.Errorf("`simsquad %s` has no --%s flag: %s", m[1], f[1], line)
			}
		}
	}
}
