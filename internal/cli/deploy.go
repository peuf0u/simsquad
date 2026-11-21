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
	cmd.Flags().StringVar(&gradleTask, "gradle-task", "", "Gradle assemble task (overrides config; default ':app:assembleDebug')")
	cmd.Flags().StringVar(&name, "name", "", "squad name (required)")
	cmd.Flags().BoolVar(&forceBuild, "force-build", false, "always invoke xcodebuild/gradle even if outputs exist")
	cmd.Flags().BoolVar(&noBuild, "no-build", false, "require an existing build artifact; never invoke xcodebuild/gradle")
	cmd.Flags().BoolVar(&preserve, "preserve-data", false, "on reuse: install without wiping app data")
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
	teardownSquad        func(name string, logger *progress.Logger) error
	teardownDevices      func(devs []contract.Device, logger *progress.Logger)
}

var deployVia = deployOps{
	iosDestination:       ios.EnsureDestinationUDID,
	deleteIosDestination: ios.DeleteDestinationSim,
	iosBuild:             ios.Build,
	androidBuild:         android.Build,
	iosProvision:         ios.Provision,
	androidProvision:     android.Provision,
	teardownSquad:        teardown.Squad,
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

	reused := false
	createdThisRun := false
	var iosAction, androidAction platformAction
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
		plan := planDeploy(*existing, deployedIosSpecs, deployedAndroidSpecs)
		switch {
		case plan.refused() != "":
			// A working platform on a different matrix — refuse to clobber it.
			return fmt.Errorf("deploy: squad %q is not reusable with the requested %s matrix/status; run `simsquad dismiss --name %s` first", squadName, plan.refused(), squadName)
		case plan.wholeReset:
			// Nothing in the squad is ready or matching — e.g. a previous
			// deploy failed at build. Reset it instead of forcing a manual
			// dismiss in the build-debug retry loop. rec stays nil so the
			// create branch below rebuilds it.
			logger.Info("deploy: reset-empty", progress.F("name", existing.Name))
			if terr := deployVia.teardownSquad(existing.Name, logger); terr != nil {
				logger.Warn("deploy: reset-empty", progress.F("error", terr.Error()))
			}
		default:
			// --clear-context wipes first. Then context layers on top — CLI
			// flags if any are present (TOML ignored), otherwise TOML. Either
			// way the *source* overrides matching keys in the existing
			// context; non-matched existing keys are preserved (so "rotate
			// one cred" stays cheap).
			if opts.clearCtx {
				existing.Context = nil
			}
			existing.Context = applyContextSource(existing.Context, tomlCtx, ctxOverrides)
			rec = existing
			iosAction, androidAction = plan.ios, plan.android
			reused = plan.allReused()
			iosDevs, androidDevs := splitByPlatform(existing.Devices)
			switch iosAction {
			case actionReuse:
				existingIOS = iosDevs
			case actionReset:
				logger.Info("deploy: reset-platform", progress.F("name", rec.Name), progress.F("platform", "ios"))
				deployVia.teardownDevices(iosDevs, logger)
				rec.Devices = withoutPlatform(rec.Devices, contract.PlatformIOS)
			}
			switch androidAction {
			case actionReuse:
				existingAndroid = androidDevs
			case actionReset:
				logger.Info("deploy: reset-platform", progress.F("name", rec.Name), progress.F("platform", "android"))
				deployVia.teardownDevices(androidDevs, logger)
				rec.Devices = withoutPlatform(rec.Devices, contract.PlatformAndroid)
			}
			if reused {
				logger.Info("deploy: reuse", progress.F("name", rec.Name))
			}
		}
	}

	if rec == nil {
		createdThisRun = true
		rec = &contract.SquadRecord{
			Name:      squadName,
			CreatedAt: util.NowISO(),
			Devices:   []contract.Device{},
			Context:   applyContextSource(nil, tomlCtx, ctxOverrides),
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
			androidBuild.BundleID = res.BundleID
			androidBuild.GitSHA = res.GitSHA
			androidBuild.Reused = res.Reused || androidAction == actionReuse
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
			iosDevices = deployVia.iosProvision(iosIn)
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
			res := deployVia.androidProvision(andIn)
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
		if terr := deployVia.teardownSquad(rec.Name, logger); terr != nil {
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
	// The JSON contract (and its --out copy) is already written; main turns
	// the ExitError into the process status without an error banner, and
	// the deferred destination-sim cleanup runs on the way out.
	if code != 0 {
		return &ExitError{Code: code}
	}
	return nil
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
// squad. wholeReset is set when every device the squad holds sits on a
// resetting platform: the squad is then torn down and created fresh (new
// created_at, context from this deploy only), which is what a full-matrix
// mismatch with nothing ready did before reuse was decided per platform.
type deployPlan struct {
	ios, android platformAction
	wholeReset   bool
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
	if p.refused() != "" || (p.ios != actionReset && p.android != actionReset) {
		return p
	}
	for _, d := range rec.Devices {
		if (d.Platform == contract.PlatformIOS && p.ios != actionReset) ||
			(d.Platform == contract.PlatformAndroid && p.android != actionReset) {
			return p
		}
	}
	p.wholeReset = true
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

func withoutPlatform(devs []contract.Device, platform contract.Platform) []contract.Device {
	out := make([]contract.Device, 0, len(devs))
	for _, d := range devs {
		if d.Platform != platform {
			out = append(out, d)
		}
	}
	return out
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
