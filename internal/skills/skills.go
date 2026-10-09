// Package skills packages the simsquad and simsquad-test agent skills that
// are embedded in the binary and installs them into an app repo (ADR 0001):
// the skill files stamped with a generated-file header, the .claude/skills
// links, the worker permissions for Claude Code and Codex, and the
// .gitignore entry for run artifacts, and a qa/README.md app notes scaffold
// when the repo has none. It also reads the header of an
// installed skill back, so callers can compare the installed skill contract
// with Contract. The binary never interprets the skill text; it only copies
// it out.
package skills

import (
	"embed"
	"io/fs"
	"path"
	"strings"
)

// Contract is the skill contract number: an integer that changes only when
// the interface between the skills and the CLI changes (commands, flags or
// JSON fields the skills rely on). A patch release that leaves that
// interface alone keeps the number, so committed skills keep working.
const Contract = 2

// DefaultDir is where Install writes the skills, relative to the app repo
// root. Codex discovers skills there; Claude Code reaches them through the
// links under ClaudeSkillsDir.
const DefaultDir = ".agents/skills"

// ClaudeSkillsDir holds one symlink per skill pointing into DefaultDir.
const ClaudeSkillsDir = ".claude/skills"

// Names lists the embedded skills in install order.
var Names = []string{"simsquad", "simsquad-test"}

//go:embed all:files
var embedded embed.FS

// skillFiles returns the embedded files of one skill as paths relative to
// the skill's own directory.
func skillFiles(name string) ([]string, error) {
	root := path.Join("files", name)
	var out []string
	err := fs.WalkDir(embedded, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		out = append(out, strings.TrimPrefix(p, root+"/"))
		return nil
	})
	return out, err
}
