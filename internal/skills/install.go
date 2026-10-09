package skills

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Options configures Install.
type Options struct {
	// Root is the app repo root. Empty means the current directory.
	Root string
	// Dir is where the skills are written. Empty means Root/DefaultDir; a
	// relative path resolves against Root. When Dir is not the default, no
	// .claude/skills links are made: they exist so Claude Code sees the
	// default install, and other agent tools find --dir themselves.
	Dir string
	// Force overwrites generated files that were edited by hand.
	Force bool
	// Version is the binary version stamped into each file header.
	Version string
}

// Result is what Install wrote; it is also the JSON emitted by
// `simsquad skill install`.
type Result struct {
	Dir      string   `json:"dir"`
	Version  string   `json:"version"`
	Contract int      `json:"contract"`
	Files    []string `json:"files"`
}

// HandEditedError reports generated files (or link paths) that were changed
// by hand. Install refuses to touch any of them unless Options.Force is set.
type HandEditedError struct {
	Paths []string
}

func (e *HandEditedError) Error() string {
	return fmt.Sprintf("refusing to overwrite hand-edited files (pass --force to overwrite): %s",
		strings.Join(e.Paths, ", "))
}

// installer carries one Install call's state through its steps.
type installer struct {
	opts    Options
	root    string
	dir     string
	planned []plannedFile // generated files, guarded by the hand-edit check
	merged  []plannedFile // team-owned files with simsquad's entries merged in
	links   []plannedLink
	touched []string
}

// plannedFile is a generated file to write. Every generated file goes
// through the same hand-edit check before anything is written.
type plannedFile struct {
	abs     string
	content string
}

type plannedLink struct {
	abs    string
	target string
}

// Install writes the embedded skills and everything around them into an
// app repo. It plans every write first and refuses with *HandEditedError
// before changing anything when a generated file was edited by hand.
// Re-running it is idempotent.
//
// To add a new artefact, append a step to the plan or apply list below.
func Install(opts Options) (*Result, error) {
	in, err := newInstaller(opts)
	if err != nil {
		return nil, err
	}
	plan := []func() error{in.planSkills, in.planLinks, in.planCodexRules, in.planClaudeSettings}
	for _, step := range plan {
		if err := step(); err != nil {
			return nil, err
		}
	}
	if err := in.checkHandEdits(); err != nil {
		return nil, err
	}
	apply := []func() error{in.writeFiles, in.writeLinks, in.ignoreRunArtifacts}
	for _, step := range apply {
		if err := step(); err != nil {
			return nil, err
		}
	}
	files := append([]string(nil), in.touched...)
	sort.Strings(files)
	return &Result{Dir: in.dir, Version: in.opts.Version, Contract: Contract, Files: files}, nil
}

func newInstaller(opts Options) (*installer, error) {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	root := opts.Root
	if root == "" {
		wd, err := os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("skills: working directory: %w", err)
		}
		root = wd
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("skills: repo root: %w", err)
	}
	dir := opts.Dir
	if dir == "" {
		dir = DefaultDir
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return &installer{opts: opts, root: root, dir: filepath.Clean(dir)}, nil
}

func (in *installer) isDefaultDir() bool {
	return in.dir == filepath.Join(in.root, DefaultDir)
}

// display renders abs relative to the repo root when it lives inside it.
func (in *installer) display(abs string) string {
	if rel, err := filepath.Rel(in.root, abs); err == nil && !strings.HasPrefix(rel, "..") {
		return filepath.ToSlash(rel)
	}
	return abs
}

func (in *installer) planSkills() error {
	for _, name := range Names {
		files, err := skillFiles(name)
		if err != nil {
			return fmt.Errorf("skills: list %s: %w", name, err)
		}
		for _, f := range files {
			raw, err := embedded.ReadFile(path.Join("files", name, f))
			if err != nil {
				return fmt.Errorf("skills: read %s/%s: %w", name, f, err)
			}
			in.planned = append(in.planned, plannedFile{
				abs:     filepath.Join(in.dir, name, filepath.FromSlash(f)),
				content: stamp(f, string(raw), in.opts.Version),
			})
		}
	}
	return nil
}

func (in *installer) planLinks() error {
	if !in.isDefaultDir() {
		return nil
	}
	linkDir := filepath.Join(in.root, ClaudeSkillsDir)
	for _, name := range Names {
		target, err := filepath.Rel(linkDir, filepath.Join(in.dir, name))
		if err != nil {
			return fmt.Errorf("skills: link target: %w", err)
		}
		in.links = append(in.links, plannedLink{abs: filepath.Join(linkDir, name), target: target})
	}
	return nil
}

// checkHandEdits collects every planned path whose current content was not
// written by simsquad (or was edited since). A missing file is fine.
func (in *installer) checkHandEdits() error {
	if in.opts.Force {
		return nil
	}
	var edited []string
	for _, f := range in.planned {
		b, err := os.ReadFile(f.abs)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return fmt.Errorf("skills: read %s: %w", f.abs, err)
		}
		if !pristine(string(b)) {
			edited = append(edited, in.display(f.abs))
		}
	}
	for _, l := range in.links {
		target, err := os.Readlink(l.abs)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || target != l.target {
			edited = append(edited, in.display(l.abs))
		}
	}
	if len(edited) > 0 {
		return &HandEditedError{Paths: edited}
	}
	return nil
}

func (in *installer) writeFiles() error {
	for _, f := range append(append([]plannedFile(nil), in.planned...), in.merged...) {
		if err := os.MkdirAll(filepath.Dir(f.abs), 0o755); err != nil {
			return fmt.Errorf("skills: mkdir: %w", err)
		}
		if err := os.WriteFile(f.abs, []byte(f.content), 0o644); err != nil {
			return fmt.Errorf("skills: write %s: %w", f.abs, err)
		}
		in.touched = append(in.touched, in.display(f.abs))
	}
	return nil
}

// RunsIgnore is the .gitignore entry that keeps test-run artifacts
// (.simsquad/runs/) out of commits.
const RunsIgnore = ".simsquad/"

// ignoreRunArtifacts appends RunsIgnore to the repo's .gitignore unless an
// equivalent line is already there.
func (in *installer) ignoreRunArtifacts() error {
	p := filepath.Join(in.root, ".gitignore")
	b, err := os.ReadFile(p)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("skills: read .gitignore: %w", err)
	}
	existing := string(b)
	for _, line := range strings.Split(existing, "\n") {
		switch strings.TrimSpace(line) {
		case ".simsquad", ".simsquad/", "/.simsquad", "/.simsquad/":
			in.touched = append(in.touched, in.display(p))
			return nil
		}
	}
	if existing != "" && !strings.HasSuffix(existing, "\n") {
		existing += "\n"
	}
	if err := os.WriteFile(p, []byte(existing+RunsIgnore+"\n"), 0o644); err != nil {
		return fmt.Errorf("skills: write .gitignore: %w", err)
	}
	in.touched = append(in.touched, in.display(p))
	return nil
}

func (in *installer) writeLinks() error {
	for _, l := range in.links {
		if err := os.MkdirAll(filepath.Dir(l.abs), 0o755); err != nil {
			return fmt.Errorf("skills: mkdir: %w", err)
		}
		if target, err := os.Readlink(l.abs); err != nil || target != l.target {
			// Only reached for a missing path or under Force.
			if err := os.RemoveAll(l.abs); err != nil {
				return fmt.Errorf("skills: replace %s: %w", l.abs, err)
			}
			if err := os.Symlink(l.target, l.abs); err != nil {
				return fmt.Errorf("skills: link %s: %w", l.abs, err)
			}
		}
		in.touched = append(in.touched, in.display(l.abs))
	}
	return nil
}
