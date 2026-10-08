// Package util holds cross-cutting helpers shared by every subsystem:
// filesystem paths, time, ID generation, atomic JSON IO, process exec, git.
//
// Nothing in this package depends on contract, state, or config — those import
// from here.
package util

import (
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

// IOSDerivedDataDir is the stable --derivedDataPath xcodebuild writes into.
// Incremental builds are fast when nothing changed because we keep the same
// derived tree across runs.
func IOSDerivedDataDir() string {
	return filepath.Join(BuildCacheRoot(), "derived", "ios")
}

// IOSBuildLogPath is the file xcodebuild's stdout/stderr is captured into when
// the build fails. Always written to the same location so users have a stable
// "Full log: …" pointer.
func IOSBuildLogPath() string {
	return filepath.Join(BuildCacheRoot(), "derived", "last-build-ios.log")
}

// AndroidBuildLogPath mirrors IOSBuildLogPath for gradle.
func AndroidBuildLogPath() string {
	return filepath.Join(BuildCacheRoot(), "derived", "last-build-android.log")
}
