package android

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// avdConfigPath is where avdmanager writes an AVD's hardware settings:
// <AVD home>/<name>.avd/config.ini.
func avdConfigPath(avd string) (string, error) {
	home, err := AVDHome()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, avd+".avd", "config.ini"), nil
}

// enableHWKeyboard sets hw.keyboard=yes in the config.ini at path, replacing
// an existing hw.keyboard line or appending one, and keeps every other line
// in order. Pixel profiles ship hw.keyboard=no, so a windowed emulator would
// ignore the Mac keyboard (issue #33). Only windowed boots call it: test
// squads type through adb, and a hardware keyboard would hide the on-screen
// keyboard their workers see. The emulator reads the setting at a cold boot.
func enableHWKeyboard(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	lines := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	found := false
	for i, line := range lines {
		key, _, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(key) == "hw.keyboard" {
			lines[i] = "hw.keyboard=yes"
			found = true
		}
	}
	if !found {
		lines = append(lines, "hw.keyboard=yes")
	}
	out := strings.Join(lines, "\n") + "\n"
	if out == string(data) {
		return nil
	}
	if err := os.WriteFile(path, []byte(out), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
