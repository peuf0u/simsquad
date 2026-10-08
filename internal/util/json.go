package util

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// WriteJSON writes v to path as indented UTF-8 JSON via a write-then-rename
// dance so concurrent readers never see a half-written file.
//
// The trailing newline matches the Python reference's `+ "\n"` so byte-level
// diffs against captured Python output stay clean.
func WriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("util.WriteJSON: mkdir parent: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("util.WriteJSON: marshal: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("util.WriteJSON: write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		// Best-effort cleanup; the rename failure is the real error.
		_ = os.Remove(tmp)
		return fmt.Errorf("util.WriteJSON: rename: %w", err)
	}
	return nil
}

// ReadJSON unmarshals the file at path into out. Returns os.ErrNotExist
// unwrapped so callers can distinguish "missing" from "malformed".
func ReadJSON(path string, out any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("util.ReadJSON %s: %w", path, err)
	}
	return nil
}
