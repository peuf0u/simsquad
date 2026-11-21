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
		window      bool
		iosSpecs    []string
		androidSpec []string
		ctxEntries  []string
		clearCtx    bool
		envEntries  []string // deprecated --env
		clearEnv    bool     // deprecated --clear-env
		platforms   []string
	)

	cmd := &cobra.Command{
		Use:   "deploy",
		Short: "Build, install, and emit a ready squad descriptor (idempotent by name)",
		Args:  cobra.NoArgs,
		Long: "Builds the iOS and/or Android app, provisions the requested device\n" +
			"matrix, installs the build, and emits the squad descriptor on stdout.\n\n" +
			"Idempotent by squad name: deploying an existing name with the same\n" +
			"matrix REUSES the running sims/emulators and just reinstalls the build\n" +
			"(no reboot) — the cheap inner-loop path for iterating on a fix. Reuse\n" +
			"is decided per platform: a platform with no devices in the squad is\n" +
			"added; one whose devices no longer match is reset when none of them is\n" +
			"ready, and refused (dismiss first) when one is.\n\n" +
			"--platform ios|android (repeatable) deploys only those equipped\n" +
			"platforms, each with its simsquad.toml matrix; the default is every\n" +
			"equipped platform. A platform left out is untouched: no build, boot or\n" +
			"install, and its devices stay as they are. It can't be combined with\n" +
			"spec flags.\n\n" +
			"--window shows the devices, for a squad you use by hand. Android\n" +
			"emulators boot with a window and the hardware keyboard on\n" +
			"(hw.keyboard=yes), so the Mac keyboard types into the app; a running\n" +
			"headless emulator is restarted with a window, keeping its data. A\n" +
			"deploy without --window leaves a running windowed emulator as it is\n" +
			"and boots new ones headless. On iOS it opens Simulator.app on each\n" +
			"device; Simulator.app also shows windows for every other booted sim.\n\n" +
			"Stdout is the whole squad, with devices of platforms left out at their\n" +
			"stored status. The exit code counts only this deploy's platforms:\n" +
			"0 = all ready, 2 = mixed, 1 = none ready / build failed / bad args.\n" +
			"A platform whose build failed counts as not ready; its devices stay in\n" +
			"the squad as error and the next deploy resets them.\n\n" +
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
			if len(envEntries) > 0 {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "simsquad: --env is deprecated; use --context")
				ctxEntries = append(ctxEntries, envEntries...)
			}
			if clearEnv {
				_, _ = fmt.Fprintln(cmd.ErrOrStderr(), "simsquad: --clear-env is deprecated; use --clear-context")
				clearCtx = true
			}
			return runDeploy(cmd, deployOpts{
				iosRepo:     iosRepo,
				androidRepo: androidRepo,
				iosScheme:   iosScheme,
				gradleTask:  gradleTask,
				name:        name,
				forceBuild:  forceBuild,
				noBuild:     noBuild,
				preserve:    preserve,
				window:      window,
				iosSpecs:    iosSpecs,
				androidSpec: androidSpec,
				ctxEntries:  ctxEntries,
				clearCtx:    clearCtx,
				platforms:   platforms,
			})
		},
	}

	cmd.Flags().StringVar(&iosRepo, "ios-repo", "", "absolute path to the iOS project (overrides config/env)")
	cmd.Flags().StringVar(&androidRepo, "android-repo", "", "absolute path to the Android project (overrides config/env)")
	cmd.Flags().StringVar(&iosScheme, "ios-scheme", "", "xcodebuild scheme (overrides config; default 'App')")
	cmd.Flags().StringVar(&gradleTask, "gradle-task", "", "Gradle task (overrides config; default ':app:assembleDebug'); a bundle* task installs dynamic feature modules via bundletool")
	cmd.Flags().StringVar(&name, "name", "", "squad name (required)")
	cmd.Flags().BoolVar(&forceBuild, "force-build", false, "always invoke xcodebuild/gradle even if outputs exist")
	cmd.Flags().BoolVar(&noBuild, "no-build", false, "require an existing build artifact; never invoke xcodebuild/gradle")
	cmd.Flags().BoolVar(&preserve, "preserve-data", false, "on reuse: install without wiping app data")
	cmd.Flags().BoolVar(&window, "window", false, "show the devices: emulators get a window and the Mac keyboard, iOS opens Simulator.app (for a hands-on dev squad)")
	cmd.Flags().StringArrayVar(&iosSpecs, "ios-spec", nil, "iOS slot spec, repeatable: 'DEVICE:RUNTIME:COUNT'")
	cmd.Flags().StringArrayVar(&androidSpec, "android-spec", nil, "Android slot spec, repeatable: 'DEVICE:IMAGE:COUNT[:TARGET]'")
	cmd.Flags().StringArrayVar(&platforms, "platform", nil, "deploy only this equipped platform, repeatable: 'ios' or 'android' (default: every equipped platform)")
	cmd.Flags().StringArrayVar(&ctxEntries, "context", nil, "squad context entry, repeatable: 'KEY=VALUE' (any --context replaces TOML [context] entirely)")
	cmd.Flags().BoolVar(&clearCtx, "clear-context", false, "on reuse: wipe the squad's existing context before applying --context / TOML")
	// Pre-0.5.0 names, kept for one release.
	cmd.Flags().StringArrayVar(&envEntries, "env", nil, "deprecated: use --context")
	cmd.Flags().BoolVar(&clearEnv, "clear-env", false, "deprecated: use --clear-context")
	_ = cmd.Flags().MarkHidden("env")
	_ = cmd.Flags().MarkHidden("clear-env")
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
	window      bool
	iosSpecs    []string
	androidSpec []string
	ctxEntries  []string
	clearCtx    bool
	platforms   []string
}

// deployOps is every call deploy makes into Xcode, Gradle, simctl, avdmanager
// or adb. Production uses the real packages; the in-process CLI tests swap in
// fakes so the reuse, merge and exit-code rules run without a simulator.
type deployOps struct {
	iosDestination       func(deviceType, runtime, tag string) (string, bool, error)
	deleteIosDestination func(udid string) error
	iosBuild             func(ios.BuildOptions) (ios.BuildResult, error)
	androidBuild         func(android.BuildOptions) (android.BuildResult, error)
	iosProvision         func(ios.ProvisionInput) []contract.Device
	androidProvision     func(android.ProvisionInput) android.ProvisionResult
	teardownDevices      func(devs []contract.Device, logger *progress.Logger)
}

var deployVia = deployOps{
	iosDestination:       ios.EnsureDestinationUDID,
	deleteIosDestination: ios.DeleteDestinationSim,
	iosBuild:             ios.Build,
	androidBuild:         android.Build,
	iosProvision:         ios.Provision,
	androidProvision:     android.Provision,
	teardownDevices:      teardown.Devices,
}

// runDeploy is the deploy command entry point: build, provision, persist,
// emit. Per-slot failures populate Device.ErrorMessage; the function only
// returns an error when inputs are unusable. Exit code is derived from the
// final device set via exitCodeFromDevices and returned as an ExitError when
// non-zero, after the JSON contract is on stdout (and in the --out file).
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
	if len(opts.platforms) > 0 {
		if cliSpecMode {
			return errors.New("deploy: --platform can't be combined with --ios-spec/--android-spec; --platform picks from the equipped matrix in simsquad.toml")
		}
		wantIos, wantAndroid, err := selectPlatforms(opts.platforms,
			platformEquipment{repo: iosRepo, specs: len(iosSpecs)},
			platformEquipment{repo: androidRepo, specs: len(androidSpecs)})
		if err != nil {
			return err
		}
		hasIos, hasAndroid = wantIos, wantAndroid
	}
	if !hasIos && !hasAndroid {
		return errors.New("deploy: no platform configured. Set ios.sims or android.sims in simsquad.toml or pass --ios-spec/--android-spec")
	}

	ctxOverrides, err := parseContextEntries(opts.ctxEntries)
	if err != nil {
		return err
	}
	tomlCtx, legacyEnv := cfg.Context()
	if legacyEnv && len(ctxOverrides) == 0 {
		_, _ = fmt.Fprintln(cmd.ErrOrStderr(), config.LegacyEnvWarning)
	}

	logger := progress.New()
	defer logger.Close()

	squadName := canonName(opts.name)

	// The matrix of each platform in this deploy; nil for a platform left out,
	// so the reuse decision never looks at it.
	var deployedIosSpecs []contract.IosSpec
	var deployedAndroidSpecs []contract.AndroidSpec
	if hasIos {
		deployedIosSpecs = iosSpecs
	}
	if hasAndroid {
		deployedAndroidSpecs = androidSpecs
	}

	co := claimOpts{
		iosSpecs:     deployedIosSpecs,
		androidSpecs: deployedAndroidSpecs,
		clearCtx:     opts.clearCtx,
		tomlCtx:      tomlCtx,
		ctxOverrides: ctxOverrides,
		iosRepo:      iosRepo,
		androidRepo:  androidRepo,
	}
	claim, err := claimSquad(squadName, co)
	if err != nil {
		return err
	}
	rec := claim.rec
	createdThisRun := claim.created
	// A squad created by this deploy has a zero plan: every platform is
	// provisioned fresh and nothing counts as reused.
	iosAction, androidAction := claim.plan.ios, claim.plan.android
	reused := claim.plan.allReused()
	var existingIOS, existingAndroid []contract.Device
	iosDevs, androidDevs := splitByPlatform(claim.devices)
	switch iosAction {
	case actionReuse:
		existingIOS = iosDevs
	case actionReset:
		resetPlatform(rec.Name, deployResult{ios: true, builds: co.placeholders()}, iosDevs, logger)
	}
	switch androidAction {
	case actionReuse:
		existingAndroid = androidDevs
	case actionReset:
		resetPlatform(rec.Name, deployResult{android: true, builds: co.placeholders()}, androidDevs, logger)
	}
	if reused {
		logger.Info("deploy: reuse", progress.F("name", rec.Name))
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
			udid, created, derr := deployVia.iosDestination(spec.Device, spec.Runtime, squadName)
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
			_ = deployVia.deleteIosDestination(iosDestUDID)
		}
	}()

	builds := contract.Builds{}
	var buildGroup errgroup.Group

	if hasIos {
		iosBuild := &contract.IosBuild{Repo: iosRepo, Specs: iosSpecs}
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
			res, berr := deployVia.iosBuild(ios.BuildOptions{
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
			iosBuild.Reused = res.Reused || iosAction == actionReuse
			return nil
		})
	}

	if hasAndroid {
		androidBuild := &contract.AndroidBuild{Repo: androidRepo, Specs: androidSpecs}
		builds.Android = androidBuild
		task := opts.gradleTask
		if task == "" {
			task = cfg.GetString("project.android_gradle_task")
		}
		repo := androidRepo
		buildGroup.Go(func() error {
			done := logger.Step("android-build", progress.F("repo", repo), progress.F("task", task))
			res, berr := deployVia.androidBuild(android.BuildOptions{
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
			androidBuild.APKSetPath = res.APKSetPath
			androidBuild.BundleID = res.BundleID
			androidBuild.GitSHA = res.GitSHA
			androidBuild.Reused = res.Reused || androidAction == actionReuse
			return nil
		})
	}
	_ = buildGroup.Wait()

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
	iosProvisioned := hasIos && builds.IOS != nil && builds.IOS.Error == "" && builds.IOS.AppPath != ""
	androidProvisioned := hasAndroid && builds.Android != nil && builds.Android.Error == "" &&
		(builds.Android.APKPath != "" || builds.Android.APKSetPath != "")

	if iosProvisioned {
		iosIn := ios.ProvisionInput{
			Name:            squadName,
			Specs:           iosSpecs,
			AppPath:         builds.IOS.AppPath,
			BundleID:        builds.IOS.BundleID,
			ExistingDevices: existingIOS,
			PreserveData:    opts.preserve,
			Window:          opts.window,
			Logger:          logger,
		}
		provGroup.Go(func() error {
			done := logger.Step("ios-provision", progress.F("slots", iosSlotCount(iosSpecs)))
			iosDevices = deployVia.iosProvision(iosIn)
			done(nil)
			return nil
		})
	}

	if androidProvisioned {
		andIn := android.ProvisionInput{
			Name:            squadName,
			Specs:           androidSpecs,
			APKPath:         builds.Android.APKPath,
			APKSetPath:      builds.Android.APKSetPath,
			BundleID:        builds.Android.BundleID,
			ExistingDevices: existingAndroid,
			PreserveData:    opts.preserve,
			Window:          opts.window,
			Logger:          logger,
		}
		provGroup.Go(func() error {
			done := logger.Step("android-provision", progress.F("slots", androidSlotCount(androidSpecs)))
			res := deployVia.androidProvision(andIn)
			androidDevices = res.Devices
			androidPorts = res.Ports
			done(nil)
			return nil
		})
	}
	_ = provGroup.Wait()

	// Every provision path (fresh, reuse, physical) installs the app, so a
	// ready device's ready_at is now — reuse must not keep the old stamp.
	readyAt := util.NowISO()
	for _, devs := range [][]contract.Device{iosDevices, androidDevices} {
		for i := range devs {
			if devs[i].Status == contract.StatusReady {
				devs[i].ReadyAt = readyAt
			}
		}
	}
	result := deployResult{
		ios:                hasIos,
		android:            hasAndroid,
		builds:             builds,
		iosDevices:         iosDevices,
		androidDevices:     androidDevices,
		androidPorts:       androidPorts,
		iosBuildFailed:     hasIos && !iosProvisioned,
		androidBuildFailed: hasAndroid && !androidProvisioned,
	}
	rec, err = state.UpdateRecord(squadName, result.mergeInto)
	if errors.Is(err, state.ErrNoRecord) {
		// The squad was dismissed while this deploy ran. Recreating it
		// would undo the dismiss, so tear down what this run provisioned
		// and print nothing: there is no squad to describe.
		deployVia.teardownDevices(result.devices(), logger)
		return fmt.Errorf("deploy: squad %q was dismissed during the deploy; tore down the %d device(s) this deploy provisioned", squadName, len(result.devices()))
	}
	if err != nil {
		return fmt.Errorf("deploy: final save: %w", err)
	}

	// The exit code reports what this deploy did: only its platforms'
	// devices count, so a broken device on a platform left out of the
	// deploy doesn't change it. Stdout still shows the whole squad.
	code := exitCodeFromDevices(result.deployedDevices(rec))
	if createdThisRun && code == 1 {
		// Build/provision produced nothing usable and we created this squad
		// this run — don't leave a stale empty record to block or confuse the
		// next deploy. (Done with the logger still open so it narrates.)
		cleanupCreatedSquad(squadName, result, logger)
	} else if err := state.TouchSquad(rec.Name); err != nil {
		logger.Warn("deploy: touch-squad", progress.F("error", err.Error()))
	}
	logger.Close()

	pub := rec.Public()
	pub.Reused = reused
	if err := writeJSON(cmd, pub); err != nil {
		return err
	}
	// The JSON contract (and its --out copy) is already written; main turns
	// the ExitError into the process status without an error banner, and
	// the deferred destination-sim cleanup runs on the way out.
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
}

// claimOpts is what claimSquad needs to find or create a squad and decide
// how this deploy treats it. The matrices are the deployed platforms' only
// (nil for a platform left out).
type claimOpts struct {
	iosSpecs     []contract.IosSpec
	androidSpecs []contract.AndroidSpec
	clearCtx     bool
	tomlCtx      map[string]string
	ctxOverrides map[string]string
	iosRepo      string
	androidRepo  string
}

// squadClaim is claimSquad's outcome. devices is the existing squad's device
// list the plan was made from; created means this deploy made the squad.
type squadClaim struct {
	rec     *contract.SquadRecord
	created bool
	plan    deployPlan
	devices []contract.Device
}

// claimSquad finds the named squad or creates it, in one block under the
// index lock, so two deploys of a new name can't both create it: the second
// finds the squad and takes the per-platform path. On an existing squad it
// makes the per-platform plan, refuses when the plan does, and otherwise
// saves this deploy's context. A
// new squad is saved with its context and registered before provisioning, so
// concurrent invocations see its claim on ports and physical serials.
func claimSquad(name string, o claimOpts) (squadClaim, error) {
	var c squadClaim
	err := state.WithIndexLockE(func() error {
		idx, err := state.LoadIndexLocked()
		if err != nil {
			return fmt.Errorf("deploy: lookup squad: %w", err)
		}
		if entry, ok := idx.Squads[name]; ok {
			existing, err := state.LoadRecord(entry.Name)
			if err != nil {
				return fmt.Errorf("deploy: load squad %s: %w", entry.Name, err)
			}
			if existing == nil {
				return fmt.Errorf("deploy: squad %q exists in registry but has no state file; run `simsquad dismiss --name %s` first", name, name)
			}
			plan := planDeploy(*existing, o.iosSpecs, o.androidSpecs)
			if p := plan.refused(); p != "" {
				// A working platform on a different matrix — refuse to clobber it.
				return fmt.Errorf("deploy: squad %q is not reusable with the requested %s matrix/status; run `simsquad dismiss --name %s` first", name, p, name)
			}
			c = squadClaim{rec: existing, plan: plan, devices: existing.Devices}
			// --clear-context wipes first. Then context layers on top — CLI
			// flags if any are present (TOML ignored), otherwise TOML. Either
			// way the *source* overrides matching keys in the existing
			// context; non-matched existing keys are preserved (so "rotate
			// one cred" stays cheap). Saved now so the final merge, which
			// keeps context from disk, carries it along with any set-context
			// made during the deploy.
			if o.clearCtx {
				existing.Context = nil
			}
			existing.Context = applyContextSource(existing.Context, o.tomlCtx, o.ctxOverrides)
			o.markClaim(existing)
			if err := state.SaveRecord(existing); err != nil {
				return fmt.Errorf("deploy: save context: %w", err)
			}
			return nil
		}
		rec := &contract.SquadRecord{
			Name:      name,
			CreatedAt: util.NowISO(),
			Devices:   []contract.Device{},
			Context:   applyContextSource(nil, o.tomlCtx, o.ctxOverrides),
			Ports:     map[string]int{},
		}
		o.markClaim(rec)
		if err := state.SaveRecord(rec); err != nil {
			return fmt.Errorf("deploy: initial save: %w", err)
		}
		if err := state.RegisterSquadLocked(state.RegisterSquadInput{
			Name:      rec.Name,
			StateFile: util.StateFileFor(rec.Name),
		}); err != nil {
			return fmt.Errorf("deploy: register squad: %w", err)
		}
		c = squadClaim{rec: rec, created: true}
		return nil
	})
	return c, err
}

// placeholders are the claim markers for this deploy's platforms: a builds
// entry with repo and specs but no artifact and no error.
func (o claimOpts) placeholders() contract.Builds {
	var b contract.Builds
	if len(o.iosSpecs) > 0 {
		b.IOS = &contract.IosBuild{Repo: o.iosRepo, Specs: o.iosSpecs}
	}
	if len(o.androidSpecs) > 0 {
		b.Android = &contract.AndroidBuild{Repo: o.androidRepo, Specs: o.androidSpecs}
	}
	return b
}

// markClaim writes a claim marker for each platform this deploy is about to
// deploy that has no builds entry yet. From the moment of the claim the
// record then shows a deploy in flight, so another deploy's failed-create
// cleanup doesn't remove a squad this one is still building for. An existing
// entry stays until the final merge replaces it: `status` keeps showing the
// installed build, and a killed deploy doesn't lose its artifact fields.
// (Cleanup runs only for a squad this deploy created, which had no entries,
// so the marker is there wherever cleanup looks for it.)
func (o claimOpts) markClaim(rec *contract.SquadRecord) {
	p := o.placeholders()
	if rec.Builds.IOS == nil {
		rec.Builds.IOS = p.IOS
	}
	if rec.Builds.Android == nil {
		rec.Builds.Android = p.Android
	}
}

func isIosMarker(b *contract.IosBuild) bool {
	return b != nil && b.AppPath == "" && b.Error == ""
}

func isAndroidMarker(b *contract.AndroidBuild) bool {
	return b != nil && b.APKPath == "" && b.APKSetPath == "" && b.Error == ""
}

// deployResult is what one deploy produced for the platforms it deployed.
// It is merged into the record on disk rather than saved over it, so a
// concurrent deploy of the other platform, or a set-context, survives.
type deployResult struct {
	ios, android   bool // the platforms this deploy owns
	builds         contract.Builds
	iosDevices     []contract.Device
	androidDevices []contract.Device
	androidPorts   map[string]int
	// A platform whose build failed (or produced no artifact) was not
	// provisioned: its stored devices are still running the old build.
	iosBuildFailed, androidBuildFailed bool
}

// mergeInto replaces the deployed platforms' devices and builds entries in
// rec with this deploy's, and for Android its port reservations too. The
// other platform's devices, builds entry and ports, and the context, stay
// as rec has them. Devices keep the iOS-then-Android order.
//
// A deployed platform whose build failed only gets its builds entry
// replaced: its stored devices are still running, so dropping them (and
// their ports) would let another squad be handed the same emulator port and
// the next deploy recreate a device on top of a running one. They are
// marked error instead, so `devices --ready` skips a device on a stale build
// and the next deploy resets them.
func (r deployResult) mergeInto(rec *contract.SquadRecord) error {
	iosDevs, androidDevs := splitByPlatform(rec.Devices)
	if r.ios {
		rec.Builds.IOS = r.builds.IOS
		if r.iosBuildFailed {
			iosDevs = markStale(iosDevs, "not reinstalled: the iOS build failed (see builds.ios.error); the next deploy resets this device")
		} else {
			iosDevs = r.iosDevices
		}
	}
	if r.android && r.androidBuildFailed {
		rec.Builds.Android = r.builds.Android
		androidDevs = markStale(androidDevs, "not reinstalled: the Android build failed (see builds.android.error); the next deploy resets this device")
	} else if r.android {
		rec.Builds.Android = r.builds.Android
		androidDevs = r.androidDevices
		// Ports holds only Android emulator reservations, so the deploy's
		// set replaces it: stale names from a reset or a shrunk matrix go.
		rec.Ports = make(map[string]int, len(r.androidPorts))
		for k, v := range r.androidPorts {
			rec.Ports[k] = v
		}
	}
	rec.Devices = append(append([]contract.Device{}, iosDevs...), androidDevs...)
	return nil
}

func markStale(devs []contract.Device, msg string) []contract.Device {
	out := append([]contract.Device{}, devs...)
	for i := range out {
		out[i].Status = contract.StatusError
		out[i].ErrorMessage = msg
	}
	return out
}

// deployedDevices is what the exit code counts: the deployed platforms'
// devices in the merged record, including those kept as error after a
// failed build. A deployed platform whose build failed and that has no
// devices counts as one not-ready slot, so a failed build is never hidden
// behind the other platform's ready devices.
func (r deployResult) deployedDevices(rec *contract.SquadRecord) []contract.Device {
	iosDevs, androidDevs := splitByPlatform(rec.Devices)
	failedSlot := contract.Device{Status: contract.StatusError}
	var out []contract.Device
	if r.ios {
		out = append(out, iosDevs...)
		if r.iosBuildFailed && len(iosDevs) == 0 {
			out = append(out, failedSlot)
		}
	}
	if r.android {
		out = append(out, androidDevs...)
		if r.androidBuildFailed && len(androidDevs) == 0 {
			out = append(out, failedSlot)
		}
	}
	return out
}

// devices is every device this deploy provisioned, iOS first.
func (r deployResult) devices() []contract.Device {
	return append(append([]contract.Device{}, r.iosDevices...), r.androidDevices...)
}

// cleanupCreatedSquad undoes a failed deploy of a squad it created: it tears
// down the deploy's own devices and, under the lock, drops its devices, its
// own builds entries and (for Android) its port reservations from the
// reloaded record. The squad itself goes only when no devices, ports or claim
// markers are left: a concurrent deploy of the other platform still in
// flight shows at least its marker (markClaim). A finished failed build of
// another deploy, with no devices, doesn't keep the squad.
func cleanupCreatedSquad(name string, result deployResult, logger *progress.Logger) {
	deployVia.teardownDevices(result.devices(), logger)
	drop := deployResult{ios: result.ios, android: result.android}
	err := state.WithIndexLockE(func() error {
		rec, err := state.LoadRecord(name)
		if err != nil || rec == nil {
			return err
		}
		_ = drop.mergeInto(rec)
		if len(rec.Devices) > 0 || len(rec.Ports) > 0 || isIosMarker(rec.Builds.IOS) || isAndroidMarker(rec.Builds.Android) {
			return state.SaveRecord(rec)
		}
		if err := state.RemoveRecord(name); err != nil {
			return err
		}
		return state.RemoveSquadLocked(name)
	})
	if err != nil {
		logger.Warn("deploy: cleanup-empty", progress.F("error", err.Error()))
		return
	}
	logger.Info("deploy: cleanup-empty", progress.F("name", name))
}

// resetPlatform tears down one platform's devices and drops them and (for
// Android) its ports from the record, leaving the other platform alone. The
// platform's builds entry becomes the deploy's claim marker (empty.builds),
// so the record still shows the deploy in flight while it rebuilds.
// Dropping the ports before reprovisioning lets the fresh emulators take the
// same port numbers back.
func resetPlatform(name string, empty deployResult, devs []contract.Device, logger *progress.Logger) {
	platform := contract.PlatformIOS
	if empty.android {
		platform = contract.PlatformAndroid
	}
	logger.Info("deploy: reset-platform", progress.F("name", name), progress.F("platform", string(platform)))
	deployVia.teardownDevices(devs, logger)
	if _, err := state.UpdateRecord(name, empty.mergeInto); err != nil {
		logger.Warn("deploy: reset-platform", progress.F("error", err.Error()))
	}
}

// platformAction is what a deploy does with one of its platforms on an
// existing squad. A platform outside the deploy has no action: no build, boot
// or install, and its devices stay as they are.
type platformAction string

const (
	actionAdd    platformAction = "add"    // squad has none of its devices: provision and add them
	actionReuse  platformAction = "reuse"  // devices match the matrix: reinstall on them
	actionReset  platformAction = "reset"  // mismatch and nothing ready: tear down, provision fresh
	actionRefuse platformAction = "refuse" // mismatch with a ready device: don't clobber it
)

// deployPlan is the per-platform reuse decision for a deploy onto an existing
// squad. A resetting platform is reset on its own even when its devices are
// all the squad holds: a deploy changes only its own platforms' part of the
// record and never deletes a record another deploy may be using, so the
// squad, its context and created_at always survive a reset.
type deployPlan struct {
	ios, android platformAction
}

// refused names the first platform the plan refuses, or "" when it refuses
// none.
func (p deployPlan) refused() string {
	switch {
	case p.ios == actionRefuse:
		return "iOS"
	case p.android == actionRefuse:
		return "Android"
	}
	return ""
}

// allReused reports whether every platform in the deploy reuses its devices,
// i.e. this deploy created no device. It feeds SquadPublic.Reused.
func (p deployPlan) allReused() bool {
	return (p.ios == "" || p.ios == actionReuse) && (p.android == "" || p.android == actionReuse) &&
		(p.ios != "" || p.android != "")
}

// planDeploy decides, for each platform with a non-empty matrix, what the
// deploy does with that platform's devices in rec. Pass a nil matrix for a
// platform outside the deploy.
func planDeploy(rec contract.SquadRecord, iosSpecs []contract.IosSpec, androidSpecs []contract.AndroidSpec) deployPlan {
	iosDevs, androidDevs := splitByPlatform(rec.Devices)
	var p deployPlan
	if len(iosSpecs) > 0 {
		p.ios = decidePlatform(iosDevs, iosSpecsMatch(iosDevs, iosSpecs))
	}
	if len(androidSpecs) > 0 {
		p.android = decidePlatform(androidDevs, androidSpecsMatch(androidDevs, androidSpecs))
	}
	return p
}

func decidePlatform(devs []contract.Device, matches bool) platformAction {
	switch {
	case len(devs) == 0:
		return actionAdd
	case matches:
		return actionReuse
	case hasReadyDevice(contract.SquadRecord{Devices: devs}):
		return actionRefuse
	default:
		return actionReset
	}
}

func splitByPlatform(devs []contract.Device) (iosDevs, androidDevs []contract.Device) {
	for _, d := range devs {
		switch d.Platform {
		case contract.PlatformIOS:
			iosDevs = append(iosDevs, d)
		case contract.PlatformAndroid:
			androidDevs = append(androidDevs, d)
		}
	}
	return iosDevs, androidDevs
}

// platformEquipment is what deploy resolved for one platform: the repo to
// build and how many matrix rows simsquad.toml lists for it.
type platformEquipment struct {
	repo  string
	specs int
}

// selectPlatforms turns repeated --platform values into the platforms this
// deploy builds and boots. A platform the repo isn't equipped for is refused
// rather than skipped, mirroring `run new`, so a missing device or repo is an
// explicit setup step instead of a silently empty deploy.
func selectPlatforms(values []string, iosEq, androidEq platformEquipment) (wantIos, wantAndroid bool, err error) {
	for _, v := range values {
		var eq platformEquipment
		var label string
		switch strings.ToLower(strings.TrimSpace(v)) {
		case string(contract.PlatformIOS):
			eq, label, wantIos = iosEq, "iOS", true
		case string(contract.PlatformAndroid):
			eq, label, wantAndroid = androidEq, "Android", true
		default:
			return false, false, fmt.Errorf("deploy: --platform %q: must be ios or android", v)
		}
		if eq.specs == 0 {
			return false, false, fmt.Errorf("deploy: --platform %s: this repo has no %s equipment; add %s devices with `simsquad equip`", v, label, label)
		}
		if eq.repo == "" {
			return false, false, fmt.Errorf("deploy: --platform %s: no %s repo configured; set project.%s_repo in simsquad.toml, SIMSQUAD_%s_REPO or --%s-repo",
				v, label, strings.ToLower(label), strings.ToUpper(label), strings.ToLower(label))
		}
	}
	return wantIos, wantAndroid, nil
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

// specMatches reports whether the squad's devices on both platforms match the
// requested matrices exactly. Deploy decides per platform (planDeploy); this
// whole-squad form is the two per-platform comparisons together.
func specMatches(record contract.SquadRecord, iosSpecs []contract.IosSpec, androidSpecs []contract.AndroidSpec) bool {
	iosDevs, androidDevs := splitByPlatform(record.Devices)
	return iosSpecsMatch(iosDevs, iosSpecs) && androidSpecsMatch(androidDevs, androidSpecs)
}

// iosSpecsMatch reports whether devs (one platform's devices) are all ready
// and form exactly the requested device-type × runtime multiset.
func iosSpecsMatch(devs []contract.Device, iosSpecs []contract.IosSpec) bool {
	haveIOS := map[[2]string]int{}
	for _, dev := range devs {
		if dev.Status != contract.StatusReady || dev.UDID == "" {
			return false
		}
		haveIOS[[2]string{dev.DeviceType, dev.Runtime}]++
	}
	wantIOS := map[[2]string]int{}
	for _, spec := range iosSpecs {
		wantIOS[[2]string{spec.Device, spec.Runtime}] += spec.Count
	}
	return reflectString2IntMaps(haveIOS, wantIOS)
}

// androidSpecsMatch is iosSpecsMatch for Android: emulators compare by
// profile and image, physical devices by count only.
func androidSpecsMatch(devs []contract.Device, androidSpecs []contract.AndroidSpec) bool {
	haveAndroid := map[[3]string]int{}
	for _, dev := range devs {
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

// parseContextEntries parses `--context KEY=VALUE` flags into a string map. Empty
// keys, missing `=`, or duplicate keys are rejected so misconfigured calls
// fail fast with a clear message rather than silently dropping data.
func parseContextEntries(entries []string) (map[string]string, error) {
	if len(entries) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(entries))
	for _, raw := range entries {
		idx := strings.Index(raw, "=")
		if idx <= 0 {
			return nil, fmt.Errorf("deploy: --context %q: expected KEY=VALUE", raw)
		}
		key := strings.TrimSpace(raw[:idx])
		value := raw[idx+1:]
		if key == "" {
			return nil, fmt.Errorf("deploy: --context %q: key must be non-empty", raw)
		}
		if _, dup := out[key]; dup {
			return nil, fmt.Errorf("deploy: --context %q: duplicate key", key)
		}
		out[key] = value
	}
	return out, nil
}

// applyContextSource resolves the context for a deploy. CLI flags and TOML
// [context] are mutually exclusive sources — if any --context flag is passed,
// the TOML layer is ignored entirely (mirrors how --ios-spec/--android-spec
// replace the TOML matrix). Whichever source applies then overlays the base
// context (the existing squad's context on reuse, or nil on a new deploy).
//
// Returns nil when the result is empty so callers don't carry an empty map
// through omitempty serialisation.
func applyContextSource(base, toml, overrides map[string]string) map[string]string {
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
