package discover

import (
	"os"
	"path/filepath"
	"strings"
)

// walkUpCandidates returns sibling-or-cousin paths whose name matches one of
// the hints, searching up to 5 levels above start. Useful for "where is the
// app repo?" detection without committing to a single layout.
func walkUpCandidates(start string, hints []string) []string {
	abs, err := filepath.Abs(start)
	if err != nil {
		return nil
	}
	cur := abs
	seen := map[string]bool{}
	var out []string
	for i := 0; i < 5; i++ {
		for _, hint := range hints {
			c, err := filepath.Abs(filepath.Join(cur, hint))
			if err != nil {
				continue
			}
			if seen[c] {
				continue
			}
			seen[c] = true
			out = append(out, c)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			break
		}
		cur = parent
	}
	return out
}

// repoHints derives sibling-repo name candidates from the current directory's
// basename. For a working dir like "myapp-arch" the iOS candidates become
// ["myapp-arch-ios", "ios", "app-ios"]. Generic across projects without
// any per-project configuration.
func repoHints(start, suffix string) []string {
	base := filepath.Base(start)
	base = strings.TrimSuffix(base, "-arch")
	base = strings.TrimSuffix(base, "-android")
	base = strings.TrimSuffix(base, "-ios")
	base = strings.TrimSuffix(base, "-app")
	hints := []string{base + "-" + suffix, suffix, "app-" + suffix}
	seen := map[string]bool{}
	out := hints[:0]
	for _, h := range hints {
		if h == "" || h == "-"+suffix || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// DetectIosRepo searches sibling/cousin directories for an iOS Xcode project.
// Returns the first directory containing any *.xcodeproj.
func DetectIosRepo(start string) string {
	hints := repoHints(start, "ios")
	for _, c := range walkUpCandidates(start, hints) {
		if !isDir(c) {
			continue
		}
		entries, err := os.ReadDir(c)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() && strings.HasSuffix(e.Name(), ".xcodeproj") {
				return c
			}
		}
	}
	return ""
}

// DetectAndroidRepo searches sibling/cousin directories for a Gradle root
// (gradlew + settings.gradle[.kts]).
func DetectAndroidRepo(start string) string {
	hints := repoHints(start, "android")
	for _, c := range walkUpCandidates(start, hints) {
		if !isDir(c) {
			continue
		}
		if !isFile(filepath.Join(c, "gradlew")) {
			continue
		}
		if isFile(filepath.Join(c, "settings.gradle.kts")) || isFile(filepath.Join(c, "settings.gradle")) {
			return c
		}
	}
	return ""
}

// DetectIosScheme returns the *.xcodeproj stem inside repo as a strong default
// scheme guess (most Xcode projects ship with a matching auto-generated scheme).
// Returns empty when no project is found.
func DetectIosScheme(repo string) string {
	if !isDir(repo) {
		return ""
	}
	entries, err := os.ReadDir(repo)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() && strings.HasSuffix(e.Name(), ".xcodeproj") {
			return strings.TrimSuffix(e.Name(), ".xcodeproj")
		}
	}
	return ""
}
