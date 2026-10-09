// Package util holds cross-cutting helpers shared by every subsystem:
// filesystem paths, time, ID generation, atomic JSON IO, process exec, git.
//
// Nothing in this package depends on contract, state, or config — those import
// from here.
package util

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
)

// cacheRoot returns the absolute path to the per-user simsquad cache directory.
// The directory itself is created on first write by callers; reads tolerate
// absence.
func cacheRoot() string {
	if v := os.Getenv("SIMSQUAD_CACHE_DIR"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil {
		// Fall back to /tmp on the (extremely rare) machines where $HOME isn't
		// resolvable — at least keep the binary runnable.
		return filepath.Join(os.TempDir(), "simsquad")
	}
	return filepath.Join(home, ".cache", "simsquad")
}

// CacheRoot is ~/.cache/simsquad (overridable via SIMSQUAD_CACHE_DIR for tests).
func CacheRoot() string { return cacheRoot() }

// SquadIndexPath is the registry file: ~/.cache/simsquad/squads.json
func SquadIndexPath() string { return filepath.Join(CacheRoot(), "squads.json") }

// StateFileFor returns the per-squad record path:
// ~/.cache/simsquad/<name>.json
func StateFileFor(name string) string {
	return filepath.Join(CacheRoot(), name+".json")
}

// IndexLockPath is the flock target guarding squads.json writes.
func IndexLockPath() string { return filepath.Join(CacheRoot(), ".index.lock") }

// EnsureCacheRoot creates ~/.cache/simsquad if it doesn't exist.
func EnsureCacheRoot() error {
	return os.MkdirAll(CacheRoot(), 0o755)
}

// BuildCacheRoot is the parent directory for build caches (xcodebuild derived
// data, gradle logs). Same root as CacheRoot today; broken out so build code
// imports a clearly-named helper instead of hardcoding the layout.
func BuildCacheRoot() string { return CacheRoot() }

// IOSDerivedDataDir is the stable --derivedDataPath xcodebuild writes into
// for one app repo: derived/ios/<repo-basename>-<short hash of abs path>.
// Incremental builds stay fast because the same repo always maps to the same
// tree. It is per repo because a shared tree let one project's products
// shadow another's at link time: a source-built Lottie.swiftmodule left by one
// app made a second app (linking the binary Lottie.framework) fail with
// undefined Lottie symbols (issue #15). The hash keeps two repos with the same
// basename apart; the basename keeps the folder recognisable.
func IOSDerivedDataDir(repo string) string {
	abs, err := filepath.Abs(repo)
	if err != nil {
		abs = filepath.Clean(repo)
	}
	sum := sha256.Sum256([]byte(abs))
	key := filepath.Base(abs) + "-" + hex.EncodeToString(sum[:])[:8]
	return filepath.Join(BuildCacheRoot(), "derived", "ios", key)
}

// IOSBuildLogPath is the file xcodebuild's stdout/stderr is captured into for
// a repo's build. It lives in that repo's derived tree so parallel builds of
// different repos can't overwrite each other's log, while each repo still has
// a stable "Full log: …" pointer.
func IOSBuildLogPath(repo string) string {
	return filepath.Join(IOSDerivedDataDir(repo), "last-build-ios.log")
}

// AndroidBuildLogPath mirrors IOSBuildLogPath for gradle.
func AndroidBuildLogPath() string {
	return filepath.Join(BuildCacheRoot(), "derived", "last-build-android.log")
}
