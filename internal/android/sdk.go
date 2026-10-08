package android

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// CmdlineToolsInstallHint is the 3-option remediation string surfaced whenever
// avdmanager/sdkmanager can't be located. Promoted to a public constant so
// every caller (build, provision, equip wizard) shows the same hint.
//
// Reason: Android's cmdline-tools live under a subdirectory not on $PATH and
// require manual installation — telling users they're missing isn't enough,
// they also need to know how to fix it.
const CmdlineToolsInstallHint = "Install Android command-line tools via one of:\n" +
	"  • brew install --cask android-commandlinetools\n" +
	"  • Android Studio → Settings → Languages & Frameworks → " +
	"Android SDK → SDK Tools → \"Android SDK Command-line Tools (latest)\"\n" +
	"  • Manual: https://developer.android.com/studio#command-tools " +
	"(extract under <SDK>/cmdline-tools/latest/)"

// SDKError signals that we couldn't locate the Android SDK or one of its
// tools. Callers wrap with extra context as needed.
type SDKError struct{ msg string }

func (e *SDKError) Error() string { return e.msg }

func sdkErr(format string, args ...any) error {
	return &SDKError{msg: fmt.Sprintf(format, args...)}
}

// SDKRoot returns the absolute path to the Android SDK, preferring
// ANDROID_HOME, then ANDROID_SDK_ROOT, then ~/Library/Android/sdk.
func SDKRoot() (string, error) {
	candidate := os.Getenv("ANDROID_HOME")
	if candidate == "" {
		candidate = os.Getenv("ANDROID_SDK_ROOT")
	}
	if candidate == "" {
		home, err := os.UserHomeDir()
		if err == nil {
			candidate = filepath.Join(home, "Library", "Android", "sdk")
		}
	}
	if candidate == "" {
		return "", sdkErr("Android SDK not found. Set ANDROID_HOME or install the SDK.")
	}
	fi, err := os.Stat(candidate)
	if err != nil || !fi.IsDir() {
		return "", sdkErr("Android SDK not found at %s. Set ANDROID_HOME or install the SDK.", candidate)
	}
	return candidate, nil
}

// whichOrUnder returns the absolute path to binary by first consulting $PATH
// and then walking the given roots. roots are tolerated when missing.
func whichOrUnder(binary string, roots ...string) string {
	if p, err := exec.LookPath(binary); err == nil {
		return p
	}
	for _, root := range roots {
		if _, err := os.Stat(root); err != nil {
			continue
		}
		var found string
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			if filepath.Base(path) != binary {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return nil
			}
			if info.Mode()&0o111 == 0 {
				return nil
			}
			found = path
			return errors.New("stop") // shortcut WalkDir
		})
		if found != "" {
			return found
		}
	}
	return ""
}

// AVDManager locates avdmanager. Missing tools return CmdlineToolsInstallHint.
func AVDManager() (string, error) {
	root, err := SDKRoot()
	if err != nil {
		return "", err
	}
	p := whichOrUnder("avdmanager",
		filepath.Join(root, "cmdline-tools"),
		filepath.Join(root, "tools", "bin"),
	)
	if p == "" {
		return "", sdkErr("avdmanager not found under %s/cmdline-tools.\n%s", root, CmdlineToolsInstallHint)
	}
	return p, nil
}

// SDKManager locates sdkmanager. Missing tools return CmdlineToolsInstallHint.
func SDKManager() (string, error) {
	root, err := SDKRoot()
	if err != nil {
		return "", err
	}
	p := whichOrUnder("sdkmanager",
		filepath.Join(root, "cmdline-tools"),
		filepath.Join(root, "tools", "bin"),
	)
	if p == "" {
		return "", sdkErr("sdkmanager not found under %s/cmdline-tools.\n%s", root, CmdlineToolsInstallHint)
	}
	return p, nil
}

// AAPT2 returns the path to the highest-version aapt2 under build-tools.
// Required by the Android build module to read applicationId from the APK.
func AAPT2() (string, error) {
	root, err := SDKRoot()
	if err != nil {
		return "", err
	}
	buildTools := filepath.Join(root, "build-tools")
	entries, err := os.ReadDir(buildTools)
	if err != nil {
		return "", sdkErr("build-tools missing under %s", root)
	}
	dirs := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Slice(dirs, func(i, j int) bool {
		return buildToolsVersionKey(dirs[i]) > buildToolsVersionKey(dirs[j])
	})
	for _, d := range dirs {
		candidate := filepath.Join(buildTools, d, "aapt2")
		if isExec(candidate) {
			return candidate, nil
		}
	}
	return "", sdkErr("no aapt2 found under %s", buildTools)
}

// Emulator locates the emulator binary under <sdk>/emulator/. Falls back to
// PATH for installs that surface the binary there.
func Emulator() (string, error) {
	root, err := SDKRoot()
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, "emulator", "emulator")
	if isExec(p) {
		return p, nil
	}
	if p, err := exec.LookPath("emulator"); err == nil {
		return p, nil
	}
	return "", sdkErr("emulator binary not found under %s/emulator", root)
}

// ADB locates the adb binary under <sdk>/platform-tools/. Falls back to PATH.
func ADB() (string, error) {
	root, err := SDKRoot()
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, "platform-tools", "adb")
	if isExec(p) {
		return p, nil
	}
	if p, err := exec.LookPath("adb"); err == nil {
		return p, nil
	}
	return "", sdkErr("adb not found under %s/platform-tools", root)
}

// HasSystemImage checks for a folder matching the avdmanager-style image id
// ("system-images;android-34;google_apis;arm64-v8a"). Returns false on any
// malformed input rather than erroring — callers treat as "not installed".
func HasSystemImage(imageID string) bool {
	root, err := SDKRoot()
	if err != nil {
		return false
	}
	parts := strings.Split(imageID, ";")
	if len(parts) != 4 || parts[0] != "system-images" {
		return false
	}
	folder := filepath.Join(root, parts[0], parts[1], parts[2], parts[3])
	fi, err := os.Stat(folder)
	return err == nil && fi.IsDir()
}

func isExec(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || fi.IsDir() {
		return false
	}
	return fi.Mode()&0o111 != 0
}

// buildToolsVersionKey turns "34.0.0" into 34_000_000_000 so versions can be
// compared as a single integer. Non-numeric segments are treated as 0 so
// future suffixes don't crash the sort.
func buildToolsVersionKey(s string) int64 {
	parts := strings.Split(s, ".")
	var key int64
	for i, p := range parts {
		if i >= 3 {
			break
		}
		n, _ := strconv.Atoi(p)
		key = key*1_000_000 + int64(n)
	}
	for i := len(parts); i < 3; i++ {
		key *= 1_000_000
	}
	return key
}
