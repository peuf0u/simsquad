package ios

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/peuf0u/simsquad/internal/progress"
	"github.com/peuf0u/simsquad/internal/util"
)

// IOSConfiguration is the xcodebuild -configuration value used by every
// simsquad build. Debug is the only variant simctl can install onto a sim
// without code signing dances.
const IOSConfiguration = "Debug"

// DefaultIOSScheme is the fallback xcodebuild scheme when neither config nor
// CLI provides one. "App" matches the default scheme of a fresh `xcodebuild
// -project ... -list` output across most starter templates — wizard detection
// via *.xcodeproj stem overrides this when available.
const DefaultIOSScheme = "App"

// simulatorProductsSubdir is the relative path under the derived-data tree
// where xcodebuild writes Debug simulator products.
var simulatorProductsSubdir = filepath.Join("Build", "Products", "Debug-iphonesimulator")

// BuildResult captures the success output of an iOS build invocation.
type BuildResult struct {
	AppPath  string
	BundleID string
	GitSHA   string
	Reused   bool
}

// BuildOptions configure a single xcodebuild invocation.
type BuildOptions struct {
	Repo            string
	DestinationUDID string // UDID of a throwaway sim, supplied by provisioning
	Force           bool   // always invoke xcodebuild even if the .app is fresh
	NoBuild         bool   // require an existing .app; never invoke xcodebuild
	Scheme          string
	Logger          *progress.Logger // optional
}

// AppSubpath is the standard relative path xcodebuild writes the .app to under
// the derived data tree, assuming the product is named after the scheme. Used
// only as a last-resort fallback; the real product name is read from
// `xcodebuild -showBuildSettings` (a scheme's product can be named anything —
// "MyApp Beta.app" for the MyApp scheme, say).
func AppSubpath(scheme string) string {
	return filepath.Join(simulatorProductsSubdir, scheme+".app")
}

// ExpectedAppPath returns the fallback .app path for a scheme-named product
// under the repo's IOSDerivedDataDir. See AppSubpath for why this is a fallback.
func ExpectedAppPath(repo, scheme string) string {
	return filepath.Join(util.IOSDerivedDataDir(repo), AppSubpath(scheme))
}

// ReadBundleID extracts CFBundleIdentifier from Info.plist inside the .app.
// Fallback for when -showBuildSettings doesn't yield PRODUCT_BUNDLE_IDENTIFIER.
// Uses plutil rather than a Go plist library because the binary plist Apple
// emits for Debug-iphonesimulator builds requires a tested parser — plutil is
// always available on macOS where xcodebuild itself runs.
func ReadBundleID(app string) (string, error) {
	plist := filepath.Join(app, "Info.plist")
	if _, err := os.Stat(plist); err != nil {
		return "", fmt.Errorf("missing Info.plist under %s", app)
	}
	r, err := util.Run("plutil", []string{"-extract", "CFBundleIdentifier", "raw", "-o", "-", plist}, util.RunOpts{})
	if err != nil {
		return "", fmt.Errorf("plutil: %w", err)
	}
	if r.Code != 0 {
		return "", fmt.Errorf("plutil exited %d reading %s: %s", r.Code, plist, strings.TrimSpace(r.Stderr))
	}
	bid := strings.TrimSpace(r.Stdout)
	if bid == "" {
		return "", fmt.Errorf("CFBundleIdentifier missing in %s", plist)
	}
	return bid, nil
}

// Build invokes xcodebuild and returns the produced .app path. Caller wires
// progress events; non-fatal "log line" output is funneled through opts.Logger.
//
// Flag semantics:
//
//	Force == true    always invoke xcodebuild
//	NoBuild == true  require an existing .app; never invoke xcodebuild
//	default          invoke xcodebuild (incremental — fast no-op on cache hit)
func Build(opts BuildOptions) (BuildResult, error) {
	scheme := opts.Scheme
	if scheme == "" {
		scheme = DefaultIOSScheme
	}
	sha := util.GitHeadSHA(opts.Repo)
	if sha == "" {
		sha = "nogit"
	}

	project, err := resolveProjectPath(opts.Repo, scheme)
	if err != nil {
		return BuildResult{}, err
	}
	// Honor the committed Package.resolved so a clean derived-data build
	// resolves to the same SPM versions a dev/Xcode build uses, instead of
	// re-resolving the graph (which trips newer SwiftPM "traits"
	// incompatibilities).
	pinnedArgs := resolvedFileArgs(opts.Repo, project)

	if opts.NoBuild {
		app, bid, lerr := locateProduct(opts.Repo, project, scheme, pinnedArgs)
		if lerr != nil {
			return BuildResult{}, fmt.Errorf(
				"--no-build set but %w. Drop --no-build or run with --force-build first", lerr)
		}
		return BuildResult{AppPath: app, BundleID: bid, GitSHA: sha, Reused: true}, nil
	}

	preExisting := dirHasApp(filepath.Join(util.IOSDerivedDataDir(opts.Repo), simulatorProductsSubdir))
	switch {
	case opts.Logger != nil && preExisting && !opts.Force:
		opts.Logger.Info("ios-build: incremental", progress.F("sha", sha))
	case opts.Logger != nil && !preExisting:
		opts.Logger.Info("ios-build: cold-start", progress.F("sha", sha))
	case opts.Logger != nil:
		opts.Logger.Info("ios-build: forced", progress.F("sha", sha))
	}

	if opts.DestinationUDID == "" {
		return BuildResult{}, errors.New("no destination UDID for xcodebuild — pair --ios with at least one slot")
	}

	derived := util.IOSDerivedDataDir(opts.Repo)
	if err := os.MkdirAll(derived, 0o755); err != nil {
		return BuildResult{}, fmt.Errorf("mkdir derived: %w", err)
	}
	logPath := util.IOSBuildLogPath(opts.Repo)
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return BuildResult{}, fmt.Errorf("mkdir log dir: %w", err)
	}

	if err := runXcodebuild(opts.Repo, project, scheme, opts.DestinationUDID, derived, logPath, pinnedArgs); err != nil {
		return BuildResult{}, err
	}

	app, bid, lerr := locateProduct(opts.Repo, project, scheme, pinnedArgs)
	if lerr != nil {
		return BuildResult{}, fmt.Errorf("xcodebuild succeeded but %w. Log: %s", lerr, logPath)
	}
	return BuildResult{AppPath: app, BundleID: bid, GitSHA: sha, Reused: preExisting}, nil
}

// resolveProjectPath finds the .xcodeproj to build. It does NOT assume the
// project is named after the scheme (the MyApp scheme lives in
// myapp.xcodeproj). Fast path: <scheme>.xcodeproj if present. Otherwise the
// single *.xcodeproj at the repo root, or the one whose stem matches the scheme
// when several exist.
func resolveProjectPath(repo, scheme string) (string, error) {
	if direct := filepath.Join(repo, scheme+".xcodeproj"); isDir(direct) {
		return direct, nil
	}
	matches, _ := filepath.Glob(filepath.Join(repo, "*.xcodeproj"))
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("no .xcodeproj found in %s", repo)
	case 1:
		return matches[0], nil
	default:
		for _, m := range matches {
			stem := strings.TrimSuffix(filepath.Base(m), ".xcodeproj")
			if strings.EqualFold(stem, scheme) {
				return m, nil
			}
		}
		names := make([]string, len(matches))
		for i, m := range matches {
			names[i] = filepath.Base(m)
		}
		return "", fmt.Errorf("multiple .xcodeproj in %s (%s); none match scheme %q — set project.ios_scheme to disambiguate",
			repo, strings.Join(names, ", "), scheme)
	}
}

// resolvedFileArgs returns the xcodebuild flag that pins SPM to the committed
// Package.resolved, but only when such a file exists — the flag errors if
// passed to a project that has none. Checks the project's embedded resolution
// and a repo-root / workspace one.
func resolvedFileArgs(repo, project string) []string {
	candidates := []string{
		filepath.Join(project, "project.xcworkspace", "xcshareddata", "swiftpm", "Package.resolved"),
		filepath.Join(repo, "Package.resolved"),
	}
	if ws, _ := filepath.Glob(filepath.Join(repo, "*.xcworkspace", "xcshareddata", "swiftpm", "Package.resolved")); len(ws) > 0 {
		candidates = append(candidates, ws...)
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return []string{"-onlyUsePackageVersionsFromResolvedFile"}
		}
	}
	return nil
}

// buildSettings holds the fields we read from `xcodebuild -showBuildSettings`.
type buildSettings struct {
	ProductName string // FULL_PRODUCT_NAME, e.g. "MyApp Beta.app"
	BundleID    string // PRODUCT_BUNDLE_IDENTIFIER
	BuildDir    string // TARGET_BUILD_DIR
}

// parseBuildSettings extracts the product fields from `-showBuildSettings
// -json` output (an array of {target, buildSettings} objects). Pure for tests.
func parseBuildSettings(data []byte) (buildSettings, error) {
	var entries []struct {
		BuildSettings map[string]string `json:"buildSettings"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return buildSettings{}, fmt.Errorf("parse build settings JSON: %w", err)
	}
	for _, e := range entries {
		bs := e.BuildSettings
		if bs == nil {
			continue
		}
		product := bs["FULL_PRODUCT_NAME"]
		if product == "" {
			product = bs["WRAPPER_NAME"]
		}
		if product == "" {
			continue
		}
		return buildSettings{
			ProductName: product,
			BundleID:    bs["PRODUCT_BUNDLE_IDENTIFIER"],
			BuildDir:    bs["TARGET_BUILD_DIR"],
		}, nil
	}
	return buildSettings{}, errors.New("no FULL_PRODUCT_NAME in build settings")
}

// readBuildSettings runs xcodebuild -showBuildSettings for the simulator
// destination. After a successful build, SPM resolution is cached, so this is
// cheap; pinnedArgs keep it safe even when run cold.
func readBuildSettings(repo, project, scheme string, pinnedArgs []string) (buildSettings, error) {
	args := []string{
		"-showBuildSettings", "-json",
		"-project", project,
		"-scheme", scheme,
		"-configuration", IOSConfiguration,
		"-destination", "generic/platform=iOS Simulator",
		// Same derived path as the build, or TARGET_BUILD_DIR resolves to the
		// developer's default DerivedData and we'd locate/install a stale .app
		// from there instead of the one we just built into the cache.
		"-derivedDataPath", util.IOSDerivedDataDir(repo),
		"-skipMacroValidation",
		"-skipPackagePluginValidation",
	}
	args = append(args, pinnedArgs...)
	r, err := util.Run("xcodebuild", args, util.RunOpts{Dir: repo})
	if err != nil {
		return buildSettings{}, fmt.Errorf("xcodebuild -showBuildSettings: %w", err)
	}
	if r.Code != 0 {
		return buildSettings{}, fmt.Errorf("xcodebuild -showBuildSettings exit %d: %s", r.Code, strings.TrimSpace(r.Stderr))
	}
	return parseBuildSettings([]byte(r.Stdout))
}

// locateProduct resolves the real .app path and bundle id, preferring the
// authoritative build settings and falling back to the legacy scheme-named
// assumption + plutil so a -showBuildSettings hiccup can't break a project
// that worked before.
func locateProduct(repo, project, scheme string, pinnedArgs []string) (app, bundleID string, err error) {
	if bs, serr := readBuildSettings(repo, project, scheme, pinnedArgs); serr == nil {
		if a, aerr := appFromSettings(repo, bs); aerr == nil {
			bid := bs.BundleID
			if bid == "" {
				bid, _ = ReadBundleID(a)
			}
			if bid != "" {
				return a, bid, nil
			}
		}
	}
	// Fallback: the historical <scheme>.app assumption.
	fallback := ExpectedAppPath(repo, scheme)
	if !isDir(fallback) {
		return "", "", fmt.Errorf("no .app found for scheme %q (looked via build settings and at %s)", scheme, fallback)
	}
	bid, berr := ReadBundleID(fallback)
	if berr != nil {
		return "", "", berr
	}
	return fallback, bid, nil
}

// appFromSettings turns build settings into an existing .app path, trying the
// reported TARGET_BUILD_DIR first, then the repo's derived products dir.
func appFromSettings(repo string, bs buildSettings) (string, error) {
	if bs.ProductName == "" {
		return "", errors.New("empty product name")
	}
	dirs := []string{}
	if bs.BuildDir != "" {
		dirs = append(dirs, bs.BuildDir)
	}
	dirs = append(dirs, filepath.Join(util.IOSDerivedDataDir(repo), simulatorProductsSubdir))
	for _, d := range dirs {
		app := filepath.Join(d, bs.ProductName)
		if isDir(app) {
			return app, nil
		}
	}
	return "", fmt.Errorf("product %q not found under %s", bs.ProductName, strings.Join(dirs, ", "))
}

func runXcodebuild(repo, project, scheme, destinationUDID, derived, logPath string, pinnedArgs []string) error {
	logf, err := os.Create(logPath)
	if err != nil {
		return fmt.Errorf("create build log: %w", err)
	}
	defer func() { _ = logf.Close() }()

	args := []string{
		"build",
		"-project", project,
		"-scheme", scheme,
		"-configuration", IOSConfiguration,
		"-destination", "id=" + destinationUDID,
		"-derivedDataPath", derived,
		"-skipMacroValidation",
		"-skipPackagePluginValidation",
	}
	args = append(args, pinnedArgs...)
	r, err := util.Run("xcodebuild", args, util.RunOpts{
		Dir: repo,
	})
	if r.Stdout != "" {
		_, _ = logf.WriteString(r.Stdout)
	}
	if r.Stderr != "" {
		_, _ = logf.WriteString(r.Stderr)
	}
	if err != nil {
		return fmt.Errorf("xcodebuild: %w (log: %s)", err, logPath)
	}
	if r.Code != 0 {
		return fmt.Errorf("xcodebuild failed (exit %d). Full log: %s", r.Code, logPath)
	}
	return nil
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// dirHasApp reports whether dir contains at least one *.app bundle.
func dirHasApp(dir string) bool {
	matches, _ := filepath.Glob(filepath.Join(dir, "*.app"))
	return len(matches) > 0
}
