package util

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteJSON writes v to path as indented UTF-8 JSON via a write-then-rename
// dance so concurrent readers never see a half-written file.
//
// The trailing newline matches the Python reference's `+ "\n"` so byte-level
// diffs against captured Python output stay clean.
func WriteJSON(path string, v any) error {
	return writeJSON(path, v, 0o755, 0o644, false)
}

// WritePrivateJSON is WriteJSON for files only the user may read: the squad
// records (which hold secret context values) and the registry beside them.
// The file is written 0600, and a missing parent is created 0700.
func WritePrivateJSON(path string, v any) error {
	return writeJSON(path, v, 0o700, 0o600, true)
}

func writeJSON(path string, v any, dirPerm, perm os.FileMode, private bool) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("util.WriteJSON: mkdir parent: %w", err)
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Errorf("util.WriteJSON: marshal: %w", err)
	}
	data = append(data, '\n')

	tmp := path + ".tmp"
	if private {
		// WriteFile sets perm only on a file it creates. A tmp file left by
		// a crashed write keeps its own mode (or is a link to another file),
		// so the secrets would land in it: start from a fresh file instead.
		if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("util.WriteJSON: remove stale tmp: %w", err)
		}
	}
	if err := os.WriteFile(tmp, data, perm); err != nil {
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
