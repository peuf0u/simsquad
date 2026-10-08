package util

import (
	"os"
	"strings"
)

// GitHeadSHA returns the full SHA at HEAD for the repo at dir, or empty string
// if dir isn't a git repo / git isn't installed. Never returns an error —
// callers treat missing SHA as "unknown" and continue.
func GitHeadSHA(dir string) string {
	r, err := Run("git", []string{"-C", dir, "rev-parse", "HEAD"}, RunOpts{})
	if err != nil || r.Code != 0 {
		return ""
	}
	return strings.TrimSpace(r.Stdout)
}

// GitLSFiles returns absolute paths to every tracked file under the given
// paths within the repo. Used by the build-cache freshness checker to compute
// the latest mtime across tracked source files.
func GitLSFiles(dir string, paths []string) []string {
	args := append([]string{"-C", dir, "ls-files", "--"}, paths...)
	r, err := Run("git", args, RunOpts{})
	if err != nil || r.Code != 0 {
		return nil
	}
	var out []string
	for _, line := range strings.Split(r.Stdout, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		out = append(out, dir+"/"+line)
	}
	return out
}

// LatestMTime returns the most recent mtime (Unix seconds) across the given
// files, or 0 if none exist.
func LatestMTime(paths []string) int64 {
	var max int64
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		if m := fi.ModTime().Unix(); m > max {
			max = m
		}
	}
	return max
}
