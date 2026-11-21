package android

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
)

// dynamicFeaturePlugin is the Gradle plugin a dynamic feature module
// applies. Its APK is a split of the app's, so an assemble* build, which
// installs the base APK only, leaves the feature out.
const dynamicFeaturePlugin = "com.android.dynamic-feature"

// pluginIDRx matches the plugin id applied by id("…"), id '…',
// apply plugin: '…' or apply(plugin = "…").
var pluginIDRx = regexp.MustCompile(`["']` + regexp.QuoteMeta(dynamicFeaturePlugin) + `["']`)

// DynamicFeatureModules returns the module folders, relative to the repo and
// sorted, whose build.gradle[.kts] applies the dynamic-feature plugin: by id
// or through a version-catalog alias. A line with `apply false` only
// declares the plugin (the root build file does) and doesn't count. build/
// and hidden folders are skipped.
func DynamicFeatureModules(repo string) []string {
	accessors := dynamicFeatureAccessors(repo)
	var mods []string
	_ = filepath.WalkDir(repo, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		name := d.Name()
		if d.IsDir() {
			if path != repo && (name == "build" || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if name != "build.gradle" && name != "build.gradle.kts" {
			return nil
		}
		if dir := filepath.Dir(path); dir != repo && appliesDynamicFeature(path, accessors) {
			rel, _ := filepath.Rel(repo, dir)
			mods = append(mods, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(mods)
	return mods
}

func appliesDynamicFeature(buildFile string, accessors []string) bool {
	b, err := os.ReadFile(buildFile)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "//") || strings.Contains(line, "apply false") {
			continue
		}
		if pluginIDRx.MatchString(line) {
			return true
		}
		for _, acc := range accessors {
			if strings.Contains(line, "alias("+acc+")") {
				return true
			}
		}
	}
	return false
}

// dynamicFeatureAccessors returns the catalog accessors that name the
// dynamic-feature plugin, from the [plugins] of every gradle/*.versions.toml:
// a libs.versions.toml entry `android-dynamic-feature = { id = … }` is
// applied as alias(libs.plugins.android.dynamic.feature). Gradle turns -, _
// and . in an alias into the same accessor separator.
func dynamicFeatureAccessors(repo string) []string {
	catalogs, _ := filepath.Glob(filepath.Join(repo, "gradle", "*.versions.toml"))
	var out []string
	for _, path := range catalogs {
		var doc struct {
			Plugins map[string]any `toml:"plugins"`
		}
		if _, err := toml.DecodeFile(path, &doc); err != nil {
			continue
		}
		catalog := strings.TrimSuffix(filepath.Base(path), ".versions.toml")
		for alias, entry := range doc.Plugins {
			var id string
			switch e := entry.(type) {
			case string: // "com.android.dynamic-feature:8.5.0"
				id, _, _ = strings.Cut(e, ":")
			case map[string]any:
				id, _ = e["id"].(string)
			}
			if id == dynamicFeaturePlugin {
				acc := strings.NewReplacer("-", ".", "_", ".").Replace(alias)
				out = append(out, catalog+".plugins."+acc)
			}
		}
	}
	return out
}

// BundleTaskFor is the bundle* task matching an assemble* task, which
// installs the feature modules too: :app:assembleDevDebug →
// :app:bundleDevDebug. Any other task gets its module's bundleDebug.
func BundleTaskFor(task string) string {
	if task == "" {
		task = DefaultGradleTask
	}
	if TaskMode(task) == ModeBundle {
		return task
	}
	project, name := splitTask(task)
	variant, ok := strings.CutPrefix(name, "assemble")
	if !ok || variant == "" {
		variant = "Debug"
	}
	return ":" + strings.ReplaceAll(project, "/", ":") + ":bundle" + variant
}
