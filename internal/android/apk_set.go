package android

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// StageAPKSet makes dst the squad's own copy of the APK set at src, replacing
// dst atomically. The build renames every new set over the repo's
// <bundle>.apks, so a later deploy of another squad from the same repo
// rewrites src; reset installs dst, which only this squad's next deploy
// replaces (#38). dst is a hard link to src where it can be (a set runs to
// hundreds of MB, and the rename leaves the linked file alone), else a 0600
// copy, as across file systems.
func StageAPKSet(src, dst string) error {
	f, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+".*.tmp")
	if err != nil {
		return fmt.Errorf("stage APK set: %w", err)
	}
	tmp := f.Name()
	_ = f.Close()
	_ = os.Remove(tmp)
	if err := os.Link(src, tmp); err != nil {
		if err := copyAPKSet(src, tmp); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("stage APK set: %w", err)
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("stage APK set: %w", err)
	}
	return nil
}

// RemoveAPKSet deletes the squad's APK set at dst (StageAPKSet) and any
// temporary file a stage killed before its rename left next to it
// (<dst>.<digits>.tmp, os.CreateTemp's pattern). Neither existing is not an
// error.
func RemoveAPKSet(dst string) error {
	var errs []error
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		errs = append(errs, err)
	}
	prefix := filepath.Base(dst) + "."
	for _, e := range entries {
		mid, ok := strings.CutPrefix(e.Name(), prefix)
		if !ok {
			continue
		}
		mid, ok = strings.CutSuffix(mid, ".tmp")
		if !ok || mid == "" || strings.Trim(mid, "0123456789") != "" {
			continue
		}
		if err := os.Remove(filepath.Join(filepath.Dir(dst), e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func copyAPKSet(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
