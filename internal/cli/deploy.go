package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"

	"github.com/peuf0u/simsquad/internal/android"
	"github.com/peuf0u/simsquad/internal/config"
	"github.com/peuf0u/simsquad/internal/contract"
	"github.com/peuf0u/simsquad/internal/discover"
	"github.com/peuf0u/simsquad/internal/ios"
	"github.com/peuf0u/simsquad/internal/progress"
	"github.com/peuf0u/simsquad/internal/state"
	"github.com/peuf0u/simsquad/internal/teardown"
	"github.com/peuf0u/simsquad/internal/util"
)

func newDeployCmd() *cobra.Command {
	var (
		iosRepo     string
		androidRepo string
		iosScheme   string
		gradleTask  string
		name        string
		forceBuild  bool
		noBuild     bool
		preserve    bool
		iosSpecs    []string
		androidSpec []string
		envEntries  []string
		clearEnv    bool
	)

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Build, install, and emit a ready squad descriptor (idempotent by name)",
		Args:  cobra.NoArgs,
		Long: "Builds the iOS and/or Android app, provisions the requested device\n" +
			"matrix, installs the build, and emits the squad descriptor on stdout.\n\n" +
			"Idempotent by squad name: deploying an existing name with the same\n" +
			"matrix REUSES the running sims/emulators and just reinstalls the build\n" +
			"(no reboot) — the cheap inner-loop path for iterating on a fix. A fresh\n" +
			"matrix is created only when the name is new or its devices are gone.\n\n" +
			"Spec flags (repeatable):\n" +
			"  --ios-spec     DEVICE:RUNTIME:COUNT          e.g. 'iPhone 17:iOS 26.4:1'\n" +
			"  --android-spec DEVICE:IMAGE:COUNT[:TARGET]   e.g. 'pixel_7:system-images;android-34;google_apis;arm64-v8a:1'\n\n" +
			"Passing *any* spec flag (iOS or Android) replaces the TOML matrix\n" +
			"entirely. Omitting a platform's flag in that case skips that platform\n" +
			"for this run. With no spec flags, both [[ios.sims]] and [[android.sims]]\n" +
			"from simsquad.toml are used.\n\n" +
			"Redirect stdout for the JSON contract (never `2>&1`, which would mix\n" +
			"progress events into the squad descriptor):\n" +
			"  simsquad deploy --name=foo --ios-spec 'iPhone 17:iOS 26.4:1' > squad.json",
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runDeploy(cmd, deployOpts{
				iosRepo:     iosRepo,
				androidRepo: androidRepo,
				iosScheme:   iosScheme,
				gradleTask:  gradleTask,
				name:        name,
				forceBuild:  forceBuild,
				noBuild:     noBuild,
				preserve:    preserve,
				iosSpecs:    iosSpecs,
				androidSpec: androidSpec,
				envEntries:  envEntries,
				clearEnv:    clearEnv,
			})
		},
	}

	cmd.Flags().StringVar(&iosRepo, "ios-repo", "", "absolute path to the iOS project (overrides config/env)")
	cmd.Flags().StringVar(&androidRepo, "android-repo", "", "absolute path to the Android project (overrides config/env)")
	cmd.Flags().StringVar(&iosScheme, "ios-scheme", "", "xcodebuild scheme (overrides config; default 'App')")
	cmd.Flags().StringVar(&gradleTask, "gradle-task", "", "Gradle assemble task (overrides config; default ':app:assembleDebug')")
	cmd.Flags().StringVar(&name, "name", "", "squad name (required)")
	cmd.Flags().BoolVar(&forceBuild, "force-build", false, "always invoke xcodebuild/gradle even if outputs exist")
	cmd.Flags().BoolVar(&noBuild, "no-build", false, "require an existing build artifact; never invoke xcodebuild/gradle")
	cmd.Flags().BoolVar(&preserve, "preserve-data", false, "on reuse: install without wiping app data")
	cmd.Flags().StringArrayVar(&iosSpecs, "ios-spec", nil, "iOS slot spec, repeatable: 'DEVICE:RUNTIME:COUNT'")
	cmd.Flags().StringArrayVar(&androidSpec, "android-spec", nil, "Android slot spec, repeatable: 'DEVICE:IMAGE:COUNT[:TARGET]'")
	cmd.Flags().StringArrayVar(&envEntries, "env", nil, "squad env entry, repeatable: 'KEY=VALUE' (any --env replaces TOML [env] entirely)")
	cmd.Flags().BoolVar(&clearEnv, "clear-env", false, "on reuse: wipe the squad's existing env before applying --env / TOML")
	cmd.MarkFlagsMutuallyExclusive("force-build", "no-build")
	_ = cmd.MarkFlagRequired("name")

	return cmd
}

type deployOpts struct {
	iosRepo     string
	androidRepo string
	iosScheme   string
	gradleTask  string
	name        string
	forceBuild  bool
	noBuild     bool
	preserve    bool
	iosSpecs    []string
	androidSpec []string
	envEntries  []string
	clearEnv    bool
}

// runDeploy is the deploy command entry point: build, provision, persist,
// emit. Per-slot failures populate Device.ErrorMessage; the function only
// returns an error when inputs are unusable. Exit code is derived from the
// final device set via exitCodeFromDevices and applied through os.Exit when
// non-zero so callers redirecting stdout get the JSON contract regardless.
func runDeploy(cmd *cobra.Command, opts deployOpts) error {
	if opts.name == "" {
		return errors.New("deploy: --name is required")
	}

	cfg, err := config.Load(config.FindConfigDir("."))
	if err != nil {
		return fmt.Errorf("deploy: load config: %w", err)
	}

	// Any spec flag (iOS or Android) opts into "the CLI defines the matrix
	// for this run" — both platforms ignore TOML. Omitting a platform's flag
	// in that case means "skip this platform" rather than "fall back to TOML".
	cliSpecMode := len(opts.iosSpecs) > 0 || len(opts.androidSpec) > 0

	iosSpecs, err := resolveIosSpecs(cfg, opts.iosSpecs, cliSpecMode)
	if err != nil {
		return err
	}
	androidSpecs, err := resolveAndroidSpecs(cfg, opts.androidSpec, cliSpecMode)
	if err != nil {
		return err
	}
	iosRepo := resolveIosRepo(cfg, opts.iosRepo)
	androidRepo := resolveAndroidRepo(cfg, opts.androidRepo)

	hasIos := iosRepo != "" && len(iosSpecs) > 0
	hasAndroid := androidRepo != "" && len(androidSpecs) > 0
	if !hasIos && !hasAndroid {
		return errors.New("deploy: no platform configured. Set ios.sims or android.sims in simsquad.toml or pass --ios-spec/--android-spec")
	}

	envOverrides, err := parseEnvEntries(opts.envEntries)
	if err != nil {
		return err
	}

	logger := progress.New()
	defer logger.Close()

	squadName := canonName(opts.name)

	reused := false
	createdThisRun := false
	var existingIOS []contract.Device
	var existingAndroid []contract.Device
	var rec *contract.SquadRecord

	entry, ok, err := state.FindSquad(squadName)
	if err != nil {
		return fmt.Errorf("deploy: lookup squad: %w", err)
	}
	if ok {
		existing, err := state.LoadRecord(entry.Name)
		if err != nil {
			return fmt.Errorf("deploy: load squad %s: %w", entry.Name, err)
		}
		if existing == nil {
			return fmt.Errorf("deploy: squad %q exists in registry but has no state file; run `simsquad dismiss --name %s` first", squadName, squadName)
		}
		switch {
		case specMatches(*existing, iosSpecs, androidSpecs):
			// Reuse path: --clear-env wipes first. Then env layers on top — CLI
			// flags if any are present (TOML ignored), otherwise TOML. Either way
			// the *source* overrides matching keys in the existing env; non-matched
			// existing keys are preserved (so "rotate one cred" stays cheap).
			if opts.clearEnv {
				existing.Env = nil
			}
			existing.Env = applyEnvSource(existing.Env, cfg.Env(), envOverrides)
			reused = true
			rec = existing
			for _, dev := range existing.Devices {
				switch dev.Platform {
				case contract.PlatformIOS:
					existingIOS = append(existingIOS, dev)
				case contract.PlatformAndroid:
					existingAndroid = append(existingAndroid, dev)
				}
			}
			logger.Info("deploy: reuse", progress.F("name", rec.Name))
		case hasReadyDevice(*existing):
			// A working squad on a different matrix — refuse to clobber it.
			return fmt.Errorf("deploy: squad %q is not reusable with the requested matrix/status; run `simsquad dismiss --name %s` first", squadName, squadName)
		default:
			// No ready devices — e.g. a previous deploy failed at build. Reset
			// it instead of forcing a manual dismiss in the build-debug retry
			// loop. rec stays nil so the create branch below rebuilds it.
			logger.Info("deploy: reset-empty", progress.F("name", existing.Name))
			if terr := teardown.Squad(existing.Name, logger); terr != nil {
				logger.Warn("deploy: reset-empty", progress.F("error", terr.Error()))
			}
		}
	}

	if rec == nil {
		createdThisRun = true
		rec = &contract.SquadRecord{
			Name:      squadName,
			CreatedAt: util.NowISO(),
			Devices:   []contract.Device{},
			Env:       applyEnvSource(nil, cfg.Env(), envOverrides),
			Ports:     map[string]int{},
		}
		// Persist an empty record + registry entry before provisioning kicks
		// off so concurrent simsquad invocations see our claim on ports and
		// physical serials. The record gets a final SaveRecord after
		// provisioning with the full Devices list.
		if err := state.SaveRecord(rec); err != nil {
			return fmt.Errorf("deploy: initial save: %w", err)
		}
		if err := state.RegisterSquad(state.RegisterSquadInput{
			Name:      rec.Name,
			StateFile: util.StateFileFor(rec.Name),
		}); err != nil {
			return fmt.Errorf("deploy: register squad: %w", err)
		}
	} else if rec.Ports == nil {
		rec.Ports = map[string]int{}
	}
	squadName = rec.Name

	// ---------------------------------------------------------------------
	// iOS build destination: reuse an existing matching sim or stand up a
	// throwaway tagged with the squad name so concurrent deploys don't
	// collide on the name.
	// ---------------------------------------------------------------------
	var (
		iosDestUDID    string
		iosDestCreated bool
	)
	if hasIos && !opts.noBuild {
		for _, dev := range existingIOS {
			if dev.UDID != "" {
				iosDestUDID = dev.UDID
				break
			}
		}
		if iosDestUDID == "" {
			spec := iosSpecs[0]
			udid, created, derr := ios.EnsureDestinationUDID(spec.Device, spec.Runtime, squadName)
			if derr != nil {
				logger.Warn("ios-build: destination-error", progress.F("error", derr.Error()))
			} else {
				iosDestUDID = udid
				iosDestCreated = created
			}
		}
	}
	defer func() {
		if iosDestCreated && iosDestUDID != "" {
			_ = ios.DeleteDestinationSim(iosDestUDID)
		}
	}()

	builds := contract.Builds{}
	var buildGroup errgroup.Group

	if hasIos {
		iosBuild := &contract.IosBuild{Specs: iosSpecs}
		builds.IOS = iosBuild
		scheme := opts.iosScheme
		if scheme == "" {
			scheme = cfg.GetString("project.ios_scheme")
		}
		if scheme == "" {
			scheme = discover.DetectIosScheme(iosRepo)
		}
		repo := iosRepo
		buildGroup.Go(func() error {
			done := logger.Step("ios-build", progress.F("repo", repo), progress.F("scheme", scheme))
			res, berr := ios.Build(ios.BuildOptions{
				Repo:            repo,
				DestinationUDID: iosDestUDID,
				Force:           opts.forceBuild,
				NoBuild:         opts.noBuild,
				Scheme:          scheme,
				Logger:          logger,
			})
			done(berr)
			if berr != nil {
				iosBuild.Error = berr.Error()
				return nil
			}
			iosBuild.AppPath = res.AppPath
			iosBuild.BundleID = res.BundleID
			iosBuild.GitSHA = res.GitSHA
			iosBuild.Reused = res.Reused || reused
			return nil
		})
	}

	if hasAndroid {
		androidBuild := &contract.AndroidBuild{Specs: androidSpecs}
		builds.Android = androidBuild
		task := opts.gradleTask
		if task == "" {
			task = cfg.GetString("project.android_gradle_task")
		}
		repo := androidRepo
		buildGroup.Go(func() error {
			done := logger.Step("android-build", progress.F("repo", repo), progress.F("task", task))
			res, berr := android.Build(android.BuildOptions{
				Repo:       repo,
				Force:      opts.forceBuild,
				NoBuild:    opts.noBuild,
				GradleTask: task,
				Logger:     logger,
			})
			done(berr)
			if berr != nil {
				androidBuild.Error = berr.Error()
				return nil
			}
			androidBuild.APKPath = res.APKPath
			androidBuild.BundleID = res.BundleID
			androidBuild.GitSHA = res.GitSHA
			androidBuild.Reused = res.Reused || reused
			return nil
		})
	}
	_ = buildGroup.Wait()
	rec.Builds = builds

	// ---------------------------------------------------------------------
	// Provisioning — iOS and Android run in parallel. Each platform owns
	// its own per-slot parallelism and writes per-slot failures into the
	// returned Device records (never into errgroup errors).
	// ---------------------------------------------------------------------
	var (
		iosDevices     []contract.Device
		androidDevices []contract.Device
		androidPorts   map[string]int
	)
	var provGroup errgroup.Group

	if hasIos && builds.IOS != nil && builds.IOS.Error == "" && builds.IOS.AppPath != "" {
		iosIn := ios.ProvisionInput{
			Name:            squadName,
			Specs:           iosSpecs,
			AppPath:         builds.IOS.AppPath,
			BundleID:        builds.IOS.BundleID,
			ExistingDevices: existingIOS,
			PreserveData:    opts.preserve,
			Logger:          logger,
		}
		provGroup.Go(func() error {
			done := logger.Step("ios-provision", progress.F("slots", iosSlotCount(iosSpecs)))
			iosDevices = ios.Provision(iosIn)
			done(nil)
			return nil
		})
	}

	if hasAndroid && builds.Android != nil && builds.Android.Error == "" && builds.Android.APKPath != "" {
		andIn := android.ProvisionInput{
			Name:            squadName,
			Specs:           androidSpecs,
			APKPath:         builds.Android.APKPath,
			BundleID:        builds.Android.BundleID,
			ExistingDevices: existingAndroid,
			PreserveData:    opts.preserve,
			Logger:          logger,
		}
		provGroup.Go(func() error {
			done := logger.Step("android-provision", progress.F("slots", androidSlotCount(androidSpecs)))
			res := android.Provision(andIn)
			androidDevices = res.Devices
			androidPorts = res.Ports
			done(nil)
			return nil
		})
	}
	_ = provGroup.Wait()

	rec.Devices = append([]contract.Device{}, iosDevices...)
	rec.Devices = append(rec.Devices, androidDevices...)
	// Every provision path (fresh, reuse, physical) installs the app, so a
	// ready device's ready_at is now — reuse must not keep the old stamp.
	readyAt := util.NowISO()
	for i := range rec.Devices {
		if rec.Devices[i].Status == contract.StatusReady {
			rec.Devices[i].ReadyAt = readyAt
		}
	}
	for k, v := range androidPorts {
		rec.Ports[k] = v
	}
	if err := state.SaveRecord(rec); err != nil {
		return fmt.Errorf("deploy: final save: %w", err)
	}

	code := exitCodeFromDevices(rec.Devices)
	if createdThisRun && code == 1 {
		// Build/provision produced nothing usable and we created this squad
		// this run — don't leave a stale empty record to block or confuse the
		// next deploy. (Done with the logger still open so it narrates.)
		if terr := teardown.Squad(rec.Name, logger); terr != nil {
			logger.Warn("deploy: cleanup-empty", progress.F("error", terr.Error()))
		}
	} else if err := state.TouchSquad(rec.Name); err != nil {
		logger.Warn("deploy: touch-squad", progress.F("error", err.Error()))
	}
	logger.Close()

	pub := rec.Public()
	pub.Reused = reused
	if err := writeJSON(cmd, pub); err != nil {
		return err
	}
	// All defers up to this point have a chance to fire because os.Exit below
	// is conditional. SilenceUsage avoids cobra reprinting --help on the
	// non-zero exit; we already printed the JSON contract.
	cmd.SilenceErrors = true
	cmd.SilenceUsage = true
	if code != 0 {
		// Run the deferred destination-sim cleanup before exiting.
		if iosDestCreated && iosDestUDID != "" {
			_ = ios.DeleteDestinationSim(iosDestUDID)
			iosDestCreated = false
		}
		logger.Close()
		os.Exit(code)
	}
	return nil
}

// exitCodeFromDevices encodes the squad-readiness summary as a Unix exit code:
//
//	0 — every device is ready
//	2 — mixed: at least one device ready and at least one error
//	1 — none ready (empty squad, or every slot errored)
//
// hasReadyDevice reports whether the squad has at least one provisioned,
// ready device — the test for "is this squad worth preserving" when its matrix
// no longer matches the requested one.
func hasReadyDevice(rec contract.SquadRecord) bool {
	for _, d := range rec.Devices {
		if d.Status == contract.StatusReady && d.UDID != "" {
			return true
		}
	}
	return false
}

func exitCodeFromDevices(devs []contract.Device) int {
	if len(devs) == 0 {
		return 1
	}
	ready := 0
	for _, d := range devs {
		if d.Status == contract.StatusReady {
			ready++
		}
	}
	if ready == 0 {
		return 1
	}
	if ready < len(devs) {
		return 2
	}
	return 0
}

func iosSlotCount(specs []contract.IosSpec) int {
	n := 0
	for _, s := range specs {
		n += s.Count
	}
	return n
}

func androidSlotCount(specs []contract.AndroidSpec) int {
	n := 0
	for _, s := range specs {
		n += s.Count
	}
	return n
}

func specMatches(record contract.SquadRecord, iosSpecs []contract.IosSpec, androidSpecs []contract.AndroidSpec) bool {
	haveIOS := map[[2]string]int{}
	for _, dev := range record.Devices {
		if dev.Platform != contract.PlatformIOS {
			continue
		}
		if dev.Status != contract.StatusReady || dev.UDID == "" {
			return false
		}
		haveIOS[[2]string{dev.DeviceType, dev.Runtime}]++
	}
	wantIOS := map[[2]string]int{}
	for _, spec := range iosSpecs {
		wantIOS[[2]string{spec.Device, spec.Runtime}] += spec.Count
	}
	if !reflectString2IntMaps(haveIOS, wantIOS) {
		return false
	}

	haveAndroid := map[[3]string]int{}
	for _, dev := range record.Devices {
		if dev.Platform != contract.PlatformAndroid {
			continue
		}
		if dev.Status != contract.StatusReady || dev.UDID == "" {
			return false
		}
		haveAndroid[androidDeviceKey(dev)]++
	}
	wantAndroid := map[[3]string]int{}
	for _, spec := range androidSpecs {
		wantAndroid[androidSpecKey(spec)] += spec.Count
	}
	return reflectString3IntMaps(haveAndroid, wantAndroid)
}

func androidDeviceKey(dev contract.Device) [3]string {
	if dev.Physical {
		return [3]string{"physical", "", ""}
	}
	return [3]string{"emulator", dev.DeviceProfile, dev.SystemImage}
}

func androidSpecKey(spec contract.AndroidSpec) [3]string {
	if spec.Target == contract.TargetPhysical {
		return [3]string{"physical", "", ""}
	}
	return [3]string{"emulator", spec.Device, spec.Image}
}

func reflectString2IntMaps(a, b map[[2]string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if b[k] != av {
			return false
		}
	}
	return true
}

func reflectString3IntMaps(a, b map[[3]string]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, av := range a {
		if b[k] != av {
			return false
		}
	}
	return true
}

// resolveIosRepo picks the iOS repo path with precedence: CLI flag > env var >
// config > nothing. Empty result means "no iOS platform requested".
func resolveIosRepo(cfg *config.Config, cliValue string) string {
	if cliValue != "" {
		return cfg.ResolveProjectPath(cliValue)
	}
	if env := os.Getenv("SIMSQUAD_IOS_REPO"); env != "" {
		return cfg.ResolveProjectPath(env)
	}
	if v := cfg.GetString("project.ios_repo"); v != "" {
		return cfg.ResolveProjectPath(v)
	}
	return ""
}

func resolveAndroidRepo(cfg *config.Config, cliValue string) string {
	if cliValue != "" {
		return cfg.ResolveProjectPath(cliValue)
	}
	if env := os.Getenv("SIMSQUAD_ANDROID_REPO"); env != "" {
		return cfg.ResolveProjectPath(env)
	}
	if v := cfg.GetString("project.android_repo"); v != "" {
		return cfg.ResolveProjectPath(v)
	}
	return ""
}

// resolveIosSpecs returns the iOS matrix for this deploy. When cliSpecMode is
// true (any --ios-spec or --android-spec was passed), TOML's [[ios.sims]] is
// ignored — an empty cliSpecs slice then means "skip iOS for this run".
// Without cliSpecMode the TOML matrix is used as the project default.
func resolveIosSpecs(cfg *config.Config, cliSpecs []string, cliSpecMode bool) ([]contract.IosSpec, error) {
	if len(cliSpecs) == 0 {
		if cliSpecMode {
			return nil, nil
		}
		return cfg.IOSSpecs(), nil
	}
	out := make([]contract.IosSpec, 0, len(cliSpecs))
	for _, raw := range cliSpecs {
		spec, err := parseIosSpec(raw)
		if err != nil {
			return nil, fmt.Errorf("deploy: --ios-spec %q: %w", raw, err)
		}
		out = append(out, spec)
	}
	return out, nil
}

func resolveAndroidSpecs(cfg *config.Config, cliSpecs []string, cliSpecMode bool) ([]contract.AndroidSpec, error) {
	if len(cliSpecs) == 0 {
		if cliSpecMode {
			return nil, nil
		}
		return cfg.AndroidSpecs(), nil
	}
	out := make([]contract.AndroidSpec, 0, len(cliSpecs))
	for _, raw := range cliSpecs {
		spec, err := parseAndroidSpec(raw)
		if err != nil {
			return nil, fmt.Errorf("deploy: --android-spec %q: %w", raw, err)
		}
		out = append(out, spec)
	}
	return out, nil
}

// parseEnvEntries parses `--env KEY=VALUE` flags into a string map. Empty
// keys, missing `=`, or duplicate keys are rejected so misconfigured calls
// fail fast with a clear message rather than silently dropping data.
func parseEnvEntries(entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(entries))
	for _, raw := range entries {
		idx := strings.Index(raw, "=")
		if idx <= 0 {
			return nil, fmt.Errorf("deploy: --env %q: expected KEY=VALUE", raw)
		}
		key := strings.TrimSpace(raw[:idx])
		value := raw[idx+1:]
		if key == "" {
			return nil, fmt.Errorf("deploy: --env %q: key must be non-empty", raw)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("deploy: --env %q: duplicate key", key)
		}
		out[key] = value
	}
	return out, nil
}

// applyEnvSource resolves the env for a deploy. CLI flags and TOML [env] are
// mutually exclusive sources — if any --env flag is passed, the TOML layer is
// ignored entirely (mirrors how --ios-spec/--android-spec replace the TOML
// matrix). Whichever source applies then overlays the base env (the existing
// squad's env on reuse, or nil on a new deploy).
//
// Returns nil when the result is empty so callers don't carry an empty map
// through omitempty serialisation.
func applyEnvSource(base, toml, overrides map[string]string) map[string]string {
	source := toml
	if len(overrides) > 0 {
		source = overrides
	}
	if len(base) == 0 && len(source) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(source))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range source {
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseIosSpec accepts the canonical `DEVICE:RUNTIME:COUNT` form. iOS device
// and runtime names never contain ':', so a strict 3-field split is safe.
func parseIosSpec(raw string) (contract.IosSpec, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 {
		return contract.IosSpec{}, errors.New("expected DEVICE:RUNTIME:COUNT")
	}
	device := strings.TrimSpace(parts[0])
	runtime := strings.TrimSpace(parts[1])
	if device == "" || runtime == "" {
		return contract.IosSpec{}, errors.New("device and runtime must be non-empty")
	}
	count, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil || count < 1 {
		return contract.IosSpec{}, errors.New("count must be a positive integer")
	}
	return contract.IosSpec{Device: device, Runtime: runtime, Count: count}, nil
}

// parseAndroidSpec accepts `DEVICE:IMAGE:COUNT[:TARGET]`. The image
// identifier is the avdmanager `system-images;...` form which itself contains
// no colons, so a right-side split on ':' is unambiguous.
func parseAndroidSpec(raw string) (contract.AndroidSpec, error) {
	parts := strings.Split(raw, ":")
	if len(parts) != 3 && len(parts) != 4 {
		return contract.AndroidSpec{}, errors.New("expected DEVICE:IMAGE:COUNT[:TARGET]")
	}
	device := strings.TrimSpace(parts[0])
	image := strings.TrimSpace(parts[1])
	if device == "" || image == "" {
		return contract.AndroidSpec{}, errors.New("device and image must be non-empty")
	}
	count, err := strconv.Atoi(strings.TrimSpace(parts[2]))
	if err != nil || count < 1 {
		return contract.AndroidSpec{}, errors.New("count must be a positive integer")
	}
	target := contract.TargetEmulator
	if len(parts) == 4 {
		switch strings.ToLower(strings.TrimSpace(parts[3])) {
		case "emulator":
			target = contract.TargetEmulator
		case "physical":
			target = contract.TargetPhysical
		default:
			return contract.AndroidSpec{}, errors.New("target must be 'emulator' or 'physical'")
		}
	}
	return contract.AndroidSpec{Device: device, Image: image, Count: count, Target: target}, nil
}
