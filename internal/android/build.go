package android

import (
	"errors"
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

// BuildMode is how the Gradle task packages the app, which decides how
// simsquad installs it.
type BuildMode int

const (
	// ModeAPK is an assemble* task: one APK, installed with adb.
	ModeAPK BuildMode = iota
	// ModeBundle is a bundle* task: an app bundle (.aab), turned into an APK
	// set and installed with bundletool, so dynamic feature modules come
	// along with the base APK.
	ModeBundle
)

func (m BuildMode) String() string {
	if m == ModeBundle {
		return "bundle"
	}
	return "apk"
}

// splitTask splits a Gradle task path into its project path and task name:
// ":foo:bar:bundleX" → ("foo/bar", "bundleX"). A task without a project path
// ("assembleDebug", ":assembleDebug") is taken as the app module's, as
// simsquad assumed before the task decided the folder.
func splitTask(task string) (project, name string) {
	parts := strings.Split(strings.TrimPrefix(task, ":"), ":")
	name = parts[len(parts)-1]
	project = strings.Join(parts[:len(parts)-1], "/")
	if project == "" {
		project = "app"
	}
	return project, name
}

// TaskMode tells a bundle* task from any other, which builds APKs.
func TaskMode(task string) BuildMode {
	if _, name := splitTask(task); strings.HasPrefix(name, "bundle") {
		return ModeBundle
	}
	return ModeAPK
}

// OutputDir is the folder, relative to the repo, where the task's module
// writes its output: <module>/build/outputs/apk or …/bundle. We walk that
// subtree rather than hardcoding a flavor/variant subdir and take the newest
// file, which lets any variant work without configuration. A custom
// buildDir/projectDir in settings.gradle isn't followed.
func OutputDir(task string) string {
	project, _ := splitTask(task)
	kind := "apk"
	if TaskMode(task) == ModeBundle {
		kind = "bundle"
	}
	return project + "/build/outputs/" + kind
}

// BuildResult captures the success output of a Gradle build invocation.
// Exactly one of APKPath (an assemble* task) and APKSetPath (a bundle* task:
// the .apks bundletool built from the app bundle) is set.
type BuildResult struct {
	APKPath    string
	APKSetPath string
	BundleID   string
	GitSHA     string
	Reused     bool
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

// latestArtifact walks <repo>/<rel> for files with the extension ext (".apk"
// or ".aab") and returns the most recently modified one. Returns empty string
// when none exist.
//
// We intentionally don't constrain by flavor/variant subdir — `assembleDebug`
// vs `assembleDevDebug` vs `assembleStagingDebug` all produce APKs under
// different subdirs of the same parent, and picking newest works across all
// configurations. The Python predecessor hardcoded `dev/debug/`; the constant
// was deliberately dropped on port.
func latestArtifact(repo, rel, ext string) string {
	root := filepath.Join(repo, rel)
	if !isDir(root) {
		return ""
	}
	var newest string
	var newestMtime int64
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() || filepath.Ext(path) != ext {
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

// Build invokes Gradle and returns the produced APK, or for a bundle* task
// the APK set built from the produced bundle. Flag semantics mirror the iOS
// path: Force always invokes, NoBuild never invokes, default invokes
// incrementally. The APK set is built on every call, once per deploy, so
// every device gets the same one.
func Build(opts BuildOptions) (BuildResult, error) {
	if _, err := SDKRoot(); err != nil {
		return BuildResult{}, err
	}
	task := opts.GradleTask
	if task == "" {
		task = DefaultGradleTask
	}
	mode := TaskMode(task)
	outRel := OutputDir(task)
	ext, kind := ".apk", "APK"
	var bundletool string
	if mode == ModeBundle {
		ext, kind = ".aab", "app bundle"
		// Both are checked before Gradle runs: without them the bundle it
		// builds can't be installed.
		var err error
		if bundletool, err = Bundletool(); err != nil {
			return BuildResult{}, err
		}
		if DebugKeystore() == "" {
			return BuildResult{}, errors.New(DebugKeystoreHint)
		}
	}

	sha := util.GitHeadSHA(opts.Repo)
	if sha == "" {
		sha = "nogit"
	}
	preExisting := latestArtifact(opts.Repo, outRel, ext)

	artifact := preExisting
	var logPath string
	if opts.NoBuild {
		if preExisting == "" {
			return BuildResult{}, fmt.Errorf(
				"--no-build set but no %s under %s. drop --no-build or run with --force-build first",
				kind, filepath.Join(opts.Repo, outRel),
			)
		}
	} else {
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

		logPath = util.AndroidBuildLogPath()
		if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
			return BuildResult{}, fmt.Errorf("mkdir log dir: %w", err)
		}

		if err := runGradle(opts.Repo, gradlew, task, logPath); err != nil {
			return BuildResult{}, err
		}

		artifact = latestArtifact(opts.Repo, outRel, ext)
		if artifact == "" {
			return BuildResult{}, fmt.Errorf(
				"gradle reported success but no %s under %s. Log: %s",
				kind, filepath.Join(opts.Repo, outRel), logPath,
			)
		}
	}
	res := BuildResult{GitSHA: sha, Reused: preExisting != ""}

	if mode == ModeAPK {
		bid, err := ReadApplicationID(artifact)
		if err != nil {
			return BuildResult{}, err
		}
		res.APKPath, res.BundleID = artifact, bid
		// No install signal tells a missing feature split apart (the app
		// installs and starts), so this warns and leaves the device ready.
		if mods := DynamicFeatureModules(opts.Repo); len(mods) > 0 && opts.Logger != nil {
			opts.Logger.Warn("android-build: only the base APK is installed; the dynamic feature modules are left out",
				progress.F("modules", strings.Join(mods, ",")),
				progress.F("fix", "set android_gradle_task to "+BundleTaskFor(task)))
		}
		return res, nil
	}

	bid, err := readBundleApplicationID(bundletool, artifact)
	if err != nil {
		return BuildResult{}, err
	}
	set, err := buildAPKSet(bundletool, artifact)
	if err != nil {
		return BuildResult{}, err
	}
	res.APKSetPath, res.BundleID = set, bid
	return res, nil
}

// readBundleApplicationID reads the application id from an app bundle's
// base manifest; aapt2 can't read an .aab.
func readBundleApplicationID(bundletool, aab string) (string, error) {
	r, err := util.Run(bundletool, []string{
		"dump", "manifest", "--bundle=" + aab, "--xpath=/manifest/@package",
	}, util.RunOpts{})
	if err != nil {
		return "", fmt.Errorf("bundletool: %w", err)
	}
	id := strings.TrimSpace(r.Stdout)
	if r.Code != 0 || id == "" {
		return "", fmt.Errorf("bundletool dump manifest found no package in %s: %s", aab, strings.TrimSpace(r.Stderr))
	}
	return id, nil
}

// buildAPKSet turns the bundle into an APK set next to it, signed with the
// debug keystore (bundletool's default). --local-testing lets an on-demand
// module install without Play: install-apks pushes its splits where the Play
// Core library looks for them.
func buildAPKSet(bundletool, aab string) (string, error) {
	set := strings.TrimSuffix(aab, ".aab") + ".apks"
	r, err := util.Run(bundletool, []string{
		"build-apks", "--bundle=" + aab, "--output=" + set, "--local-testing", "--overwrite",
	}, util.RunOpts{})
	if err != nil {
		return "", fmt.Errorf("bundletool: %w", err)
	}
	if r.Code != 0 {
		return "", fmt.Errorf("bundletool build-apks failed: %s", strings.TrimSpace(r.Stderr+r.Stdout))
	}
	return set, nil
}

func runGradle(repo, gradlew, task, logPath string) error {
	logf, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create gradle log: %w", err)
	}
	defer func() { _ = logf.Close() }()

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
