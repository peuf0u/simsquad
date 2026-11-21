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
	return writeJSON(path, v, 0o755, 0o644)
}

// WritePrivateJSON is WriteJSON for files only the user may read: the squad
// records (which hold secret context values) and the registry beside them.
// The file is written 0600, and a missing parent is created 0700.
func WritePrivateJSON(path string, v any) error {
	return writeJSON(path, v, 0o700, 0o600)
}

func writeJSON(path string, v any, dirPerm, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("util.WriteJSON: mkdir parent: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("util.WriteJSON: marshal: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("util.WriteJSON: write tmp: %w", err)
	}
	// WriteFile sets perm only on a file it creates; a tmp file left by a
	// crashed write keeps its old mode, which the rename would carry over.
	if err := os.Chmod(tmp, perm); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("util.WriteJSON: chmod tmp: %w", err)
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
