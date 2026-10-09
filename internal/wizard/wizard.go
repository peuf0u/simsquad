package wizard

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/charmbracelet/huh"

	"github.com/peuf0u/simsquad/internal/config"
	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/discover"
)

// Options configures the equip wizard.
type Options struct {
	CWD        string
	Force      bool
	Show       bool
	AddIOS     []contract.IosSpec
	AddAndroid []contract.AndroidSpec
}

// Result reports the files the wizard wrote.
type Result struct {
	ProjectFile string
	LocalFile   string
	Gitignore   string
	Summary     ConfigSummary
	Wrote       bool
}

// ConfigSummary is the read-only shape emitted by `equip --show` and included
// in the result after edits. It deliberately mirrors the deploy config fields
// instead of exposing internal TOML parser maps.
type ConfigSummary struct {
	ConfigDir   string                 `json:"config_dir"`
	ProjectFile string                 `json:"project_file"`
	LocalFile   string                 `json:"local_file"`
	Project     map[string]string      `json:"project"`
	Env         map[string]string      `json:"env,omitempty"`
	IOS         []contract.IosSpec     `json:"ios"`
	Android     []contract.AndroidSpec `json:"android"`
}

type wizardState struct {
	iosRepo     string
	androidRepo string
	iosScheme   string
	gradleTask  string
	env         map[string]string
	iosSpecs    []contract.IosSpec
	androidSpec []contract.AndroidSpec
	// projectAgent / localAgent are the raw [agent] tables of each file.
	// The wizard doesn't edit them, but it rewrites both files wholesale, so
	// it carries each table back into the file it came from (a local
	// worker_model must not leak into the committed simsquad.toml).
	projectAgent map[string]any
	localAgent   map[string]any
}

var dottedAndroidImageRx = regexp.MustCompile(`;android-\d+\.\d+;`)

// RunEquipWizard prompts for project defaults and writes simsquad.toml,
// simsquad.local.toml, plus an idempotent .gitignore entry for the local file.
func RunEquipWizard(opts Options) (Result, error) {
	cwd := opts.CWD
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return Result{}, fmt.Errorf("equip: getwd: %w", err)
		}
	}

	configDir := config.FindConfigDir(cwd)
	hasConfig := configDir != ""
	if !hasConfig {
		configDir = cwd
	}
	state, err := loadState(configDir, hasConfig)
	if err != nil {
		return Result{}, err
	}
	projectPath := filepath.Join(configDir, config.ProjectFile)
	localPath := filepath.Join(configDir, config.LocalFile)

	if opts.Show {
		return Result{
			ProjectFile: projectPath,
			LocalFile:   localPath,
			Summary:     summarize(configDir, state),
		}, nil
	}
	if len(opts.AddIOS) > 0 || len(opts.AddAndroid) > 0 {
		state.iosSpecs = append(state.iosSpecs, opts.AddIOS...)
		state.androidSpec = append(state.androidSpec, opts.AddAndroid...)
		return writeState(configDir, state, true)
	}

	if !stdinIsTerminal() {
		return Result{}, errors.New("equip: interactive wizard requires stdin to be a terminal")
	}
	if hasConfig {
		if err := runBubbleEditor(&state, configDir); err != nil {
			return Result{}, err
		}
		return writeState(configDir, state, true)
	}

	if err := runProjectForm(&state); err != nil {
		return Result{}, err
	}
	if err := runIOSMatrix(&state); err != nil {
		return Result{}, err
	}
	if err := runAndroidMatrix(&state); err != nil {
		return Result{}, err
	}
	return writeState(configDir, state, opts.Force)
}

func loadState(configDir string, hasConfig bool) (wizardState, error) {
	state := defaults(configDir)
	if !hasConfig {
		return state, nil
	}
	cfg, err := config.Load(configDir)
	if err != nil {
		return wizardState{}, err
	}
	if v := cfg.GetString("project.ios_repo"); v != "" {
		state.iosRepo = v
	}
	if v := cfg.GetString("project.android_repo"); v != "" {
		state.androidRepo = v
	}
	if v := cfg.GetString("project.ios_scheme"); v != "" {
		state.iosScheme = v
	}
	if v := cfg.GetString("project.android_gradle_task"); v != "" {
		state.gradleTask = v
	}
	if env := cfg.Env(); len(env) > 0 {
		state.env = make(map[string]string, len(env))
		for k, v := range env {
			state.env[k] = v
		}
	}
	state.iosSpecs = cfg.IOSSpecs()
	state.androidSpec = cfg.AndroidSpecs()
	if state.projectAgent, err = readAgentTable(filepath.Join(configDir, config.ProjectFile)); err != nil {
		return wizardState{}, err
	}
	if state.localAgent, err = readAgentTable(filepath.Join(configDir, config.LocalFile)); err != nil {
		return wizardState{}, err
	}
	return state, nil
}

// readAgentTable returns the [agent] table of one TOML file, or nil when the
// file or the table is absent.
func readAgentTable(path string) (map[string]any, error) {
	var doc map[string]any
	if _, err := toml.DecodeFile(path, &doc); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("equip: parse %s: %w", filepath.Base(path), err)
	}
	agent, _ := doc["agent"].(map[string]any)
	return agent, nil
}

// renderAgentTable encodes a preserved [agent] table, preceded by a blank
// line; "" when there is none.
func renderAgentTable(agent map[string]any) (string, error) {
	if len(agent) == 0 {
		return "", nil
	}
	var b strings.Builder
	enc := toml.NewEncoder(&b)
	enc.Indent = ""
	if err := enc.Encode(map[string]any{"agent": agent}); err != nil {
		return "", fmt.Errorf("encode [agent] table: %w", err)
	}
	return "\n" + b.String(), nil
}

func writeState(configDir string, state wizardState, overwrite bool) (Result, error) {
	projectPath := filepath.Join(configDir, config.ProjectFile)
	localPath := filepath.Join(configDir, config.LocalFile)
	if !overwrite {
		for _, path := range []string{projectPath, localPath} {
			if _, err := os.Stat(path); err == nil {
				return Result{}, fmt.Errorf("equip: %s exists; pass --force to overwrite", filepath.Base(path))
			}
		}
	}
	// Render both files before writing either, so an encode error leaves
	// the existing config untouched.
	project, err := renderProjectTOML(state)
	if err != nil {
		return Result{}, fmt.Errorf("equip: %s: %w", config.ProjectFile, err)
	}
	local, err := renderLocalTOML(state)
	if err != nil {
		return Result{}, fmt.Errorf("equip: %s: %w", config.LocalFile, err)
	}
	if err := os.WriteFile(projectPath, []byte(project), 0o644); err != nil {
		return Result{}, fmt.Errorf("equip: write %s: %w", config.ProjectFile, err)
	}
	if err := os.WriteFile(localPath, []byte(local), 0o644); err != nil {
		return Result{}, fmt.Errorf("equip: write %s: %w", config.LocalFile, err)
	}
	gitignore := filepath.Join(configDir, ".gitignore")
	if err := ensureGitignoreEntry(gitignore, config.LocalFile); err != nil {
		return Result{}, err
	}
	return Result{
		ProjectFile: projectPath,
		LocalFile:   localPath,
		Gitignore:   gitignore,
		Summary:     summarize(configDir, state),
		Wrote:       true,
	}, nil
}

func defaults(cwd string) wizardState {
	iosRepo := discover.DetectIosRepo(cwd)
	androidRepo := discover.DetectAndroidRepo(cwd)
	iosScheme := discover.DetectIosScheme(iosRepo)
	if iosScheme == "" {
		iosScheme = "App"
	}
	return wizardState{
		iosRepo:     iosRepo,
		androidRepo: androidRepo,
		iosScheme:   iosScheme,
		gradleTask:  ":app:assembleDebug",
	}
}

func promptPhysicalAndroidSpec() (contract.AndroidSpec, error) {
	count := "1"
	if err := huh.NewInput().
		Title("Physical Android count").
		Value(&count).
		Validate(validatePositiveInt).
		Run(); err != nil {
		return contract.AndroidSpec{}, err
	}
	n, _ := strconv.Atoi(strings.TrimSpace(count))
	return contract.AndroidSpec{
		Device: "physical",
		Image:  "physical",
		Count:  n,
		Target: contract.TargetPhysical,
	}, nil
}

func runProjectForm(state *wizardState) error {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("iOS repo").
				Value(&state.iosRepo),
			huh.NewInput().
				Title("Android repo").
				Value(&state.androidRepo),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("iOS scheme").
				Value(&state.iosScheme),
			huh.NewInput().
				Title("Gradle task").
				Value(&state.gradleTask),
		),
	).WithInput(os.Stdin).WithOutput(os.Stderr).Run()
}

func runIOSMatrix(state *wizardState) error {
	add := state.iosRepo != ""
	if err := huh.NewConfirm().Title("Configure iOS simulators").Value(&add).Run(); err != nil {
		return err
	}
	deviceTypes := discover.ListIosDeviceTypes()
	runtimes := discover.ListIosRuntimes()
	for add {
		spec, err := promptIOSSpecWithChoices(deviceTypes, runtimes)
		if err != nil {
			return err
		}
		state.iosSpecs = append(state.iosSpecs, spec)
		add = false
		if err := huh.NewConfirm().Title("Add another iOS spec").Value(&add).Run(); err != nil {
			return err
		}
	}
	return nil
}

func promptIOSSpecWithChoices(deviceTypes []discover.IosDeviceType, runtimes []discover.IosRuntime) (contract.IosSpec, error) {
	var spec contract.IosSpec
	if err := promptIOSDevice(&spec, deviceTypes); err != nil {
		return contract.IosSpec{}, err
	}
	if err := promptIOSRuntime(&spec, runtimes); err != nil {
		return contract.IosSpec{}, err
	}
	count := "1"
	if err := huh.NewInput().
		Title("iOS count").
		Value(&count).
		Validate(validatePositiveInt).
		Run(); err != nil {
		return contract.IosSpec{}, err
	}
	spec.Count, _ = strconv.Atoi(strings.TrimSpace(count))
	return spec, nil
}

func promptIOSDevice(spec *contract.IosSpec, deviceTypes []discover.IosDeviceType) error {
	if len(deviceTypes) == 0 {
		spec.Device = "iPhone 15"
		return huh.NewInput().
			Title("iOS device").
			Value(&spec.Device).
			Validate(validateNonEmpty).
			Run()
	}
	opts := make([]huh.Option[string], 0, len(deviceTypes))
	for _, d := range deviceTypes {
		opts = append(opts, huh.NewOption(d.Name, d.Name))
	}
	spec.Device = deviceTypes[0].Name
	return huh.NewSelect[string]().
		Title("iOS device").
		Options(opts...).
		Value(&spec.Device).
		Run()
}

func promptIOSRuntime(spec *contract.IosSpec, runtimes []discover.IosRuntime) error {
	if len(runtimes) == 0 {
		spec.Runtime = "iOS 17.0"
		return huh.NewInput().
			Title("iOS runtime").
			Value(&spec.Runtime).
			Validate(validateNonEmpty).
			Run()
	}
	opts := make([]huh.Option[string], 0, len(runtimes))
	for _, rt := range runtimes {
		label := rt.Name
		if !rt.Available {
			label += " (unavailable)"
		}
		opts = append(opts, huh.NewOption(label, rt.Name))
	}
	spec.Runtime = runtimes[0].Name
	return huh.NewSelect[string]().
		Title("iOS runtime").
		Options(opts...).
		Value(&spec.Runtime).
		Run()
}

func runAndroidMatrix(state *wizardState) error {
	add := state.androidRepo != ""
	if err := huh.NewConfirm().Title("Configure Android emulators").Value(&add).Run(); err != nil {
		return err
	}
	profiles := discover.ListAVDDeviceProfiles()
	images := discover.ListInstalledAndroidImages()
	for add {
		spec, err := promptAndroidEmulatorSpecWithChoices(profiles, images)
		if err != nil {
			return err
		}
		state.androidSpec = append(state.androidSpec, spec)
		add = false
		if err := huh.NewConfirm().Title("Add another Android emulator spec").Value(&add).Run(); err != nil {
			return err
		}
	}
	addPhysical := false
	if err := huh.NewConfirm().Title("Add physical Android devices").Value(&addPhysical).Run(); err != nil {
		return err
	}
	if addPhysical {
		spec, err := promptPhysicalAndroidSpec()
		if err != nil {
			return err
		}
		state.androidSpec = append(state.androidSpec, spec)
	}
	return nil
}

func promptAndroidEmulatorSpecWithChoices(profiles []string, images []discover.AndroidImage) (contract.AndroidSpec, error) {
	spec := contract.AndroidSpec{Target: contract.TargetEmulator}
	if err := promptAndroidDevice(&spec, profiles); err != nil {
		return contract.AndroidSpec{}, err
	}
	if err := promptAndroidImage(&spec, images); err != nil {
		return contract.AndroidSpec{}, err
	}
	count := "1"
	if err := huh.NewInput().
		Title("Android emulator count").
		Value(&count).
		Validate(validatePositiveInt).
		Run(); err != nil {
		return contract.AndroidSpec{}, err
	}
	spec.Count, _ = strconv.Atoi(strings.TrimSpace(count))
	return spec, nil
}

func promptAndroidDevice(spec *contract.AndroidSpec, profiles []string) error {
	if len(profiles) == 0 {
		spec.Device = "pixel_7"
		return huh.NewInput().
			Title("Android device profile").
			Value(&spec.Device).
			Validate(validateNonEmpty).
			Run()
	}
	opts := make([]huh.Option[string], 0, len(profiles))
	for _, p := range profiles {
		opts = append(opts, huh.NewOption(p, p))
	}
	spec.Device = profiles[0]
	return huh.NewSelect[string]().
		Title("Android device profile").
		Options(opts...).
		Value(&spec.Device).
		Run()
}

func promptAndroidImage(spec *contract.AndroidSpec, images []discover.AndroidImage) error {
	if len(images) == 0 {
		spec.Image = "system-images;android-34;google_apis;arm64-v8a"
		return huh.NewInput().
			Title("Android system image").
			Value(&spec.Image).
			Validate(validateNonEmpty).
			Run()
	}
	images = preferredAndroidImages(images)
	opts := make([]huh.Option[string], 0, len(images))
	for _, img := range images {
		opts = append(opts, huh.NewOption(img.Identifier, img.Identifier))
	}
	spec.Image = images[0].Identifier
	return huh.NewSelect[string]().
		Title("Android system image").
		Options(opts...).
		Value(&spec.Image).
		Run()
}

func preferredAndroidImages(images []discover.AndroidImage) []discover.AndroidImage {
	out := append([]discover.AndroidImage(nil), images...)
	sort.SliceStable(out, func(i, j int) bool {
		di := dottedAndroidImageRx.MatchString(out[i].Identifier)
		dj := dottedAndroidImageRx.MatchString(out[j].Identifier)
		if di != dj {
			return !di
		}
		return out[i].Identifier < out[j].Identifier
	})
	return out
}

func summarize(configDir string, state wizardState) ConfigSummary {
	iosSpecs := append([]contract.IosSpec(nil), state.iosSpecs...)
	if iosSpecs == nil {
		iosSpecs = []contract.IosSpec{}
	}
	androidSpecs := append([]contract.AndroidSpec(nil), state.androidSpec...)
	if androidSpecs == nil {
		androidSpecs = []contract.AndroidSpec{}
	}
	var env map[string]string
	if len(state.env) > 0 {
		env = make(map[string]string, len(state.env))
		for k, v := range state.env {
			env[k] = v
		}
	}
	return ConfigSummary{
		ConfigDir:   configDir,
		ProjectFile: filepath.Join(configDir, config.ProjectFile),
		LocalFile:   filepath.Join(configDir, config.LocalFile),
		Project: map[string]string{
			"ios_repo":            state.iosRepo,
			"android_repo":        state.androidRepo,
			"ios_scheme":          state.iosScheme,
			"android_gradle_task": state.gradleTask,
		},
		Env:     env,
		IOS:     iosSpecs,
		Android: androidSpecs,
	}
}

func validateNonEmpty(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("required")
	}
	return nil
}

func validatePositiveInt(value string) error {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < 1 {
		return errors.New("must be a positive integer")
	}
	return nil
}

func renderProjectTOML(state wizardState) (string, error) {
	var b strings.Builder
	b.WriteString("[project]\n")
	b.WriteString("ios_scheme = " + strconv.Quote(state.iosScheme) + "\n")
	b.WriteString("android_gradle_task = " + strconv.Quote(state.gradleTask) + "\n")
	if len(state.env) > 0 {
		b.WriteString("\n[env]\n")
		keys := make([]string, 0, len(state.env))
		for k := range state.env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(k + " = " + strconv.Quote(state.env[k]) + "\n")
		}
	}
	agent, err := renderAgentTable(state.projectAgent)
	if err != nil {
		return "", err
	}
	b.WriteString(agent)
	for _, spec := range state.iosSpecs {
		b.WriteString("\n[[ios.sims]]\n")
		b.WriteString("device = " + strconv.Quote(spec.Device) + "\n")
		b.WriteString("runtime = " + strconv.Quote(spec.Runtime) + "\n")
		b.WriteString("count = " + strconv.Itoa(spec.Count) + "\n")
	}
	for _, spec := range state.androidSpec {
		b.WriteString("\n[[android.sims]]\n")
		b.WriteString("device = " + strconv.Quote(spec.Device) + "\n")
		b.WriteString("image = " + strconv.Quote(spec.Image) + "\n")
		b.WriteString("count = " + strconv.Itoa(spec.Count) + "\n")
		b.WriteString("target = " + strconv.Quote(string(spec.Target)) + "\n")
	}
	return b.String(), nil
}

func renderLocalTOML(state wizardState) (string, error) {
	var b strings.Builder
	b.WriteString("[project]\n")
	b.WriteString("ios_repo = " + strconv.Quote(state.iosRepo) + "\n")
	b.WriteString("android_repo = " + strconv.Quote(state.androidRepo) + "\n")
	agent, err := renderAgentTable(state.localAgent)
	if err != nil {
		return "", err
	}
	b.WriteString(agent)
	return b.String(), nil
}

func ensureGitignoreEntry(path, entry string) error {
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("equip: read .gitignore: %w", err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == entry {
			return nil
		}
	}
	prefix := ""
	if len(data) > 0 && data[len(data)-1] != '\n' {
		prefix = "\n"
	}
	if err := os.WriteFile(path, append(data, []byte(prefix+entry+"\n")...), 0o644); err != nil {
		return fmt.Errorf("equip: write .gitignore: %w", err)
	}
	return nil
}

func stdinIsTerminal() bool {
	fi, err := os.Stdin.Stat()
	return err == nil && (fi.Mode()&os.ModeCharDevice) != 0
}
