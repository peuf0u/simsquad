package android

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/peuf0u/simsquad/internal/progress"
	"github.com/peuf0u/simsquad/internal/util"
)

// DefaultGradleTask is the assemble task invoked when neither config nor CLI
// overrides it. ":app:assembleDebug" works for any plain template; projects
// with product flavors override via [project].android_gradle_task in their
// simsquad.toml.
const DefaultGradleTask = ":app:assembleDebug"

// APKOutputRel is the relative directory under the Android repo where Gradle
// writes APKs. We walk the subtree (`app/build/outputs/apk/**/*.apk`) rather
// than hardcoding a flavor/variant subdir — the wizard picks the newest APK
// post-build, which lets any project layout work without configuration.
const APKOutputRel = "app/build/outputs/apk"

// BuildResult captures the success output of a Gradle build invocation.
type BuildResult struct {
	APKPath  string
	BundleID string
	GitSHA   string
	Reused   bool
}

// BuildOptions configure a single Gradle invocation.
type BuildOptions struct {
	Repo       string
	Force      bool
	NoBuild    bool
	GradleTask string
	Logger     *progress.Logger // optional
}

var aapt2PackageRx = regexp.MustCompile(`package:\s+name='([^']+)'`)

// ReadApplicationID extracts the applicationId from an APK via `aapt2 dump
// badging`. Returns an error if aapt2 isn't installed, the APK is malformed,
// or the package field is missing.
func ReadApplicationID(apk string) (string, error) {
	aapt2, err := AAPT2()
	if err != nil {
		return "", err
	}
	r, err := util.Run(aapt2, []string{"dump", "badging", apk}, util.RunOpts{})
	if err != nil {
		return "", fmt.Errorf("aapt2: %w", err)
	}
	if r.Code != 0 {
		return "", fmt.Errorf("aapt2 dump badging failed: %s", strings.TrimSpace(r.Stderr))
	}
	m := aapt2PackageRx.FindStringSubmatch(r.Stdout)
	if m == nil {
		return "", fmt.Errorf("package name not found in aapt2 output for %s", apk)
	}
	return m[1], nil
}

// latestAPK walks <repo>/app/build/outputs/apk for *.apk files and returns
// the most recently modified one. Returns empty string when none exist.
//
// We intentionally don't constrain by flavor/variant subdir — `assembleDebug`
// vs `assembleDevDebug` vs `assembleStagingDebug` all produce APKs under
// different subdirs of the same parent, and picking newest works across all
// configurations. The Python predecessor hardcoded `dev/debug/`; the constant
// was deliberately dropped on port.
func latestAPK(repo string) string {
	root := filepath.Join(repo, APKOutputRel)
	if !isDir(root) {
		return ""
	}
	var newest string
	var newestMtime int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ".apk" {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if mt := info.ModTime().Unix(); mt > newestMtime {
			newestMtime = mt
			newest = path
		}
		return nil
	})
	return newest
}

// Build invokes Gradle and returns the produced APK path. Flag semantics
// mirror the iOS path: Force always invokes, NoBuild never invokes, default
// invokes incrementally.
func Build(opts BuildOptions) (BuildResult, error) {
	if _, err := SDKRoot(); err != nil {
		return BuildResult{}, err
	}
	task := opts.GradleTask
	if task == "" {
		task = DefaultGradleTask
	}

	sha := util.GitHeadSHA(opts.Repo)
	if sha == "" {
		sha = "nogit"
	}
	preExisting := latestAPK(opts.Repo)

	if opts.NoBuild {
		if preExisting == "" {
			return BuildResult{}, fmt.Errorf(
				"--no-build set but no APK under %s. Drop --no-build or run with --force-build first.",
				filepath.Join(opts.Repo, APKOutputRel),
			)
		}
		bid, err := ReadApplicationID(preExisting)
		if err != nil {
			return BuildResult{}, err
		}
		return BuildResult{APKPath: preExisting, BundleID: bid, GitSHA: sha, Reused: true}, nil
	}

	gradlew := filepath.Join(opts.Repo, "gradlew")
	if !isFile(gradlew) {
		return BuildResult{}, fmt.Errorf("gradlew not found at %s", gradlew)
	}

	switch {
	case opts.Logger != nil && preExisting != "" && !opts.Force:
		opts.Logger.Info("android-build: incremental", progress.F("sha", sha))
	case opts.Logger != nil && preExisting == "":
		opts.Logger.Info("android-build: cold-start", progress.F("sha", sha))
	case opts.Logger != nil:
		opts.Logger.Info("android-build: forced", progress.F("sha", sha))
	}

	logPath := util.AndroidBuildLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return BuildResult{}, fmt.Errorf("mkdir log dir: %w", err)
	}

	if err := runGradle(opts.Repo, gradlew, task, logPath); err != nil {
		return BuildResult{}, err
	}

	apk := latestAPK(opts.Repo)
	if apk == "" {
		return BuildResult{}, fmt.Errorf(
			"gradle reported success but no APK under %s. Log: %s",
			filepath.Join(opts.Repo, APKOutputRel), logPath,
		)
	}
	bid, err := ReadApplicationID(apk)
	if err != nil {
		return BuildResult{}, err
	}
	return BuildResult{
		APKPath:  apk,
		BundleID: bid,
		GitSHA:   sha,
		Reused:   preExisting != "",
	}, nil
}

func runGradle(repo, gradlew, task, logPath string) error {
	logf, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create gradle log: %w", err)
	}
	defer logf.Close()

	args := []string{task, "-x", "test", "--no-daemon", "--console=plain"}
	r, err := util.Run(gradlew, args, util.RunOpts{Dir: repo})
	if r.Stdout != "" {
		_, _ = logf.WriteString(r.Stdout)
	}
	if r.Stderr != "" {
		_, _ = logf.WriteString(r.Stderr)
	}
	if err != nil {
		return fmt.Errorf("gradle: %w (log: %s)", err, logPath)
	}
	if r.Code != 0 {
		return fmt.Errorf("gradle %s failed (exit %d). Full log: %s", task, r.Code, logPath)
	}
	return nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}
