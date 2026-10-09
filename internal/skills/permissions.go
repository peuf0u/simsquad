package skills

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Headless workers can't answer permission prompts, so a prompt stalls them
// until their deadline. Install grants the minimum a worker needs, and
// nothing more: running mobilecli and simsquad, and writing its results
// under .simsquad/runs/.

// ClaudeSettingsFile is the shared Claude Code project settings file. It
// belongs to the team, so Install merges its entries in rather than owning
// the file.
const ClaudeSettingsFile = ".claude/settings.json"

// ClaudeAllow lists the permission rules Install adds to ClaudeSettingsFile.
// A leading "/" in Edit/Write rules anchors the path at the project root.
var ClaudeAllow = []string{
	"Bash(mobilecli:*)",
	"Bash(simsquad:*)",
	"Edit(/.simsquad/runs/**)",
	"Write(/.simsquad/runs/**)",
}

// CodexRulesFile is the Codex execution-policy file Install owns. Codex
// confines writes to the workspace through its sandbox, which already
// covers .simsquad/runs/, so the rules only need to allow the commands.
const CodexRulesFile = ".codex/rules/simsquad.rules"

const codexRules = `# Lets headless simsquad-test workers run the device driver and simsquad
# without an approval prompt.
prefix_rule(pattern = ["mobilecli"], decision = "allow")
prefix_rule(pattern = ["simsquad"], decision = "allow")
`

func (in *installer) planCodexRules() error {
	in.planned = append(in.planned, plannedFile{
		abs:     filepath.Join(in.root, filepath.FromSlash(CodexRulesFile)),
		content: stamp("simsquad.rules", codexRules, in.opts.Version),
	})
	return nil
}

// planClaudeSettings merges ClaudeAllow into permissions.allow, keeping
// every other setting and existing rule. The file is only rewritten when an
// entry was missing. It runs in the plan phase so a malformed settings file
// stops Install before anything is written.
func (in *installer) planClaudeSettings() error {
	p := filepath.Join(in.root, filepath.FromSlash(ClaudeSettingsFile))
	settings := map[string]any{}
	b, err := os.ReadFile(p)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("skills: read %s: %w", ClaudeSettingsFile, err)
	default:
		dec := json.NewDecoder(bytes.NewReader(b))
		dec.UseNumber()
		if err := dec.Decode(&settings); err != nil {
			return fmt.Errorf("skills: parse %s: %w", ClaudeSettingsFile, err)
		}
	}

	perms, _ := settings["permissions"].(map[string]any)
	if perms == nil {
		if _, present := settings["permissions"]; present {
			return fmt.Errorf("skills: %s: permissions is not an object", ClaudeSettingsFile)
		}
		perms = map[string]any{}
	}
	allow, _ := perms["allow"].([]any)
	have := map[string]bool{}
	for _, a := range allow {
		if s, ok := a.(string); ok {
			have[s] = true
		}
	}
	changed := false
	for _, rule := range ClaudeAllow {
		if !have[rule] {
			allow = append(allow, rule)
			changed = true
		}
	}
	if !changed {
		in.touched = append(in.touched, in.display(p))
		return nil
	}
	perms["allow"] = allow
	settings["permissions"] = perms

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(settings); err != nil {
		return fmt.Errorf("skills: encode %s: %w", ClaudeSettingsFile, err)
	}
	in.merged = append(in.merged, plannedFile{abs: p, content: buf.String()})
	return nil
}
