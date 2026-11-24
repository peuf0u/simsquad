package wizard

import (
	"github.com/peuf0u/simsquad/internal/android"
	"github.com/peuf0u/simsquad/internal/config"
)

// featureModules returns the dynamic feature modules of the Android repo,
// resolved against configDir the way deploy resolves android_repo. An
// assemble* task installs none of them, so equip proposes a bundle* task.
func featureModules(configDir, repo string) []string {
	if repo == "" {
		return nil
	}
	return android.DynamicFeatureModules(config.ResolvePath(configDir, repo))
}

// defaultGradleTask is the task a new config starts with: the bundle task
// when the repo has dynamic feature modules, else the plain assemble task.
func defaultGradleTask(configDir, repo string) string {
	if len(featureModules(configDir, repo)) > 0 {
		return android.BundleTaskFor(android.DefaultGradleTask)
	}
	return android.DefaultGradleTask
}

// gradleTaskChoices is the editor's picker for the Gradle task: the current
// task and its bundle task when the repo has dynamic feature modules; nil
// (a plain text field) otherwise.
func gradleTaskChoices(configDir, repo, current string) []editorChoice {
	if len(featureModules(configDir, repo)) == 0 {
		return nil
	}
	bundle := android.BundleTaskFor(current)
	return withCurrentChoice([]editorChoice{
		{label: bundle + " (installs the dynamic feature modules)", value: bundle},
	}, current)
}

// gradleTaskHint is the first-run form's note under the Gradle task, ""
// when the repo has no dynamic feature modules.
func gradleTaskHint(configDir, repo string) string {
	if len(featureModules(configDir, repo)) == 0 {
		return ""
	}
	return "This repo has dynamic feature modules: a bundle* task (needs bundletool) installs them, an assemble* task only the base APK."
}
