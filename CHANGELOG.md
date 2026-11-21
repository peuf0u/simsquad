# Changelog

All notable changes are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`deploy --window` shows the devices, for a squad used by hand.**
  Android emulators it boots start with a window (`-gpu auto` instead of
  headless `swiftshader_indirect`) and with `hw.keyboard=yes` written into
  the AVD's `config.ini`, so the Mac keyboard types into the app. A running
  emulator recorded as headless is restarted with a window: `adb emu kill`,
  then a cold boot without `-wipe-data`, so its app data survives. The mode
  is recorded on the Android device as `"window": true` (additive,
  omitempty). The switch is one way: a deploy without `--window` leaves a
  running windowed emulator alone, and boots new emulators headless with
  the profile's keyboard setting. On iOS, `--window` opens Simulator.app on
  each ready device (Simulator.app also shows other booted sims). The
  `simsquad` skill's dev-squad deploy always passes `--window`;
  `simsquad-test` never does. There is no config key.
- **`deploy --platform ios|android` deploys only the chosen platforms.**
  Repeatable; omitted, it means every equipped platform, as before. Each
  chosen platform takes its `simsquad.toml` matrix, and a platform left out
  gets no build, boot or install: its devices, `builds` entry and ports stay
  in the record. An unequipped platform, or `--platform` with
  `--ios-spec` / `--android-spec`, exits `1`. Reuse is now decided per
  platform: matching devices are reused, a platform the squad lacks is
  added (so a bare deploy on an iOS-only squad adds Android), and a
  mismatched platform is reset when none of its devices is ready and
  refused when one is; a reset never touches the rest of the squad, its
  context or `created_at`. A failed build keeps that platform's devices
  (still running the old build) in the squad as `error`, with their ports,
  and the next deploy resets them. Stdout is still the whole squad; the
  exit code counts only the deployed platforms' devices, with a failed
  build counting as not ready. Deploy now merges its result into the
  record under the index lock and creates a new squad in one locked step,
  so concurrent deploys of different platforms, and a `set-context` during
  a deploy, are no longer lost. A squad dismissed while its deploy runs
  stays dismissed: the deploy tears down what it provisioned and exits
  `1`. The `simsquad-test`
  skill deploys only the run's `platforms`. Skill contract 4; rerun
  `simsquad skill install` to update the installed skills.
- **`run.json` records where the app notes are.** `run new` writes
  `app_notes`, the absolute path of `qa/README.md` in the config dir, into
  `run.json` and its stdout (additive, omitted when the file doesn't
  exist). The `simsquad-test` worker reads the app notes from there, or
  notes that there are none, instead of guessing which repo holds them.
- **README "Project layouts"** says where the config, skills, `qa/` and
  run folders live in a single repo, an umbrella workspace repo and split
  repos with a home repo.
- **A worker can be retried within its run.** Running the same `run
  worker` command again on a worker folder first moves the earlier
  attempt's outputs (everything except `prompt.md`) to
  `<worker-dir>/attempts/<n>/`, so a failed retry is never reported with
  the stale result. `run validate` and `run report` ignore `attempts/`, and
  `run report` can run again after a retry, overwriting `report.json` and
  `report.md`. The `simsquad-test` skill documents the retry: once the
  cause is fixed, the same `run worker` command, then `run validate`, then
  `run report`, preferably before the first report. simsquad never retries
  a worker on its own.

### Changed

- **Reusing an Android emulator checks that it is the squad's own, with or
  without `--window`.** Before installing on or restarting a reachable
  emulator, deploy asks it which AVD it runs (`adb emu avd name`, retried
  up to three times). When another AVD holds the squad's port (after a
  reboot, the user's own emulator can take it), the device fails with
  `reuse: port N is held by another emulator (<name>), not <avd>`; when the
  name can't be read, it fails with `reuse: could not read the AVD name of
  the emulator on port N (<adb error>)`. Either way nothing is killed,
  booted or installed. Before, deploy installed the build on whatever
  answered on the port, and a `--window` restart would have killed it.
- **Reuse stops the squad's own emulator before rebooting it when it is
  alive but not answering adb** (for example still booting after an
  interrupted deploy), instead of starting a second instance that can't
  lock the AVD. A started emulator that exits during boot now fails the
  device with `reuse-boot: …` instead of being recorded.
- **Workers always run on an explicit model.** `run new --worker
  claude|codex` (default `claude`) names the CLI the workers run on; any
  other value exits `1`. `run new --model <id>` sets the model for one
  run. The model is `--model`, else `[agent].worker_model`
  (`simsquad.local.toml` over `simsquad.toml`), else the worker's default:
  `sonnet` for `claude`, `gpt-6.1-sol` for `codex`. `worker_model` on
  `run new`'s stdout and in `run.json` is never empty, and both gain
  `worker` (additive). The `simsquad-test` skill passes `--worker` (the
  CLI it runs in) to `run new` and always passes `--model` to the worker;
  before, an unset `worker_model` left workers on their CLI's
  machine-local default.
- **`run new` refuses a squad only when an unfinished run on it shares a
  platform.** An `@ios` run and an `@android` run can now run on one squad
  at the same time; a second run on a platform an unfinished run uses is
  still refused, and the message names that run, the shared platforms and
  the `simsquad run report` command that finishes it. `--fresh` still
  bypasses the check. A `run.json` written before `platforms` existed
  counts as using every platform. The `simsquad-test` skill says so in
  step 4.
- **One root for skills, runs and app notes: the config dir.** `skill
  install` and `skill status` now act on the folder holding
  `simsquad.toml` (as `run new` does), not the git toplevel, so a config in
  a monorepo subfolder gets its skills beside it; so do the skills `equip`
  offers to install. Without config, `skill install` exits `1` with "run
  `simsquad equip` first", and `skill status` reports not installed with
  that hint (exit `0`). `equip` (when it creates the config) and `skill
  install` warn once on stderr when the config dir isn't inside a git work
  tree. The `simsquad-test` skill lists other unfinished runs from the run
  folder's parent, whatever folder the agent is in. If an older simsquad
  installed your skills at the git toplevel above a config in a
  subfolder, re-run `simsquad skill install` from the config dir and
  remove the old `.agents/`, `.claude/skills/` and `.codex/` copies at the
  git toplevel.
- **`run worker` records why a worker left no result.** A clean exit
  without `result.json` is now `error: no result.json` (it was `ok`), in
  the status file and on stdout. A worker whose `worker.log` ends with a
  known Claude Code or Codex usage-limit message, and that exited non-zero
  or left no result, is `error: usage limit`. Only these fixed
  texts are recorded, never log lines. The `simsquad-test` skill lists
  every status, and its worker writes `result.json` with every scenario
  `blocked` (and the exploration's `blocked_reason`) when it is stopped
  before its first scenario, instead of files like `BLOCKED.md`.

### Fixed

- **Apps with dynamic feature modules install whole with a `bundle*`
  Gradle task.** The task now decides how the Android app is packaged.
  With `android_gradle_task = ":app:bundleDevDebug"` (any `bundle*` task),
  deploy takes the newest `.aab`, reads its application id with
  `bundletool dump manifest`, builds one APK set per deploy
  (`bundletool build-apks --local-testing`, signed with
  `~/.android/debug.keystore`) and installs it on every device with
  `bundletool install-apks`, so the feature modules' code is on the
  device. (`pm path` may show no split per feature: bundletool merges
  install-time modules not marked `dist:removable` into the base APK, as
  it did for the Národní muzeum app.) Data is cleared unless `--preserve-data`, and a
  failed install is retried once after an uninstall, as for an APK. The
  set's path is recorded as `builds.android.apks_path` (additive,
  omitempty) in place of `apk_path`. bundletool isn't a Homebrew
  dependency: a `bundle*` task without it, or without a debug keystore,
  fails the Android build with the fix (exit `1`). An `assemble*` task
  works as before, and now warns on stderr, without changing the device
  status or exit code, when a module applies `com.android.dynamic-feature`:
  only the base APK is installed, so the app fails to open feature
  screens. The build output is now looked for under the task's module
  (`:mobile:assembleDebug` → `mobile/build/outputs/apk`) rather than
  always `app/`. `equip` proposes the bundle task for a repo with dynamic
  feature modules.
- **Dismissed squads no longer leave an AVD folder behind.** Android
  teardown (`dismiss`, `sweep`, a failed `deploy`) now waits up to 15s for
  the emulator to exit before deleting its AVD; an emulator still shutting
  down could recreate `simsquad-*.avd/` holding only `emulator-user.ini`.
  `simsquad sweep` also removes such a folder when it has no `.ini` and no
  squad tracks it (in `$ANDROID_AVD_HOME`, or `~/.android/avd`), and lists
  it under `pruned.android`.
- **A Claude Code worker reads its instructions and writes its results
  from any working directory.** A worker inherits the orchestrator's
  working directory, so the `simsquad-test` skill's worker command now
  grants everything by absolute path: `--add-dir` for the skill folder with
  its symlinks resolved and for the folder holding the app notes, and
  `Edit(//<run dir>/**)` in place of `Edit(.simsquad/runs/**)`. The prompt
  names `worker.md` by its resolved path. Before, a skill folder linked
  from outside the working directory left the worker unable to read
  `worker.md`, and a worker started anywhere but the folder holding
  `.simsquad/runs/` couldn't write `result.json`, so the run ended as infra.
  The Codex command is unchanged. Re-run `simsquad skill install` to pick
  it up. `run new` now resolves symlinks in `run_dir` and `app_notes`, so
  the rules also match for a repo reached through a symlink; the squad
  name still comes from the path you're in.

## [0.5.0] - 2026-10-14

### Added

- **One squad per git worktree.** `simsquad sweep` now first dismisses
  every squad whose recorded iOS or Android repo path no longer exists,
  typically a removed worktree, and deletes the iOS derived data built from
  it. Its output gains `dismissed: [{name, repo}]` next to `pruned`. To
  support this, `deploy` records the repo on each build (`builds.ios.repo`,
  `builds.android.repo`); squads deployed by an older simsquad have none and
  are left alone. [docs/agent-testing.md](docs/agent-testing.md#one-squad-per-git-worktree)
  describes the setup: `ios_repo = "."` in the committed `simsquad.toml`
  and the default `qa-<folder>` squad name.

### Changed

- **Breaking: the squad `env` is now `context`.** It holds test context
  (persona, stage, test-account login), not environment variables, and the
  old name misled. JSON output says `context` everywhere: `deploy`,
  `status`, `devices`, `set-context`, `equip`'s summary, `report.json`
  (`squad.context`, and the report schema) and the `## Context` section of
  `report.md`. Consumers reading `.env` must switch to `.context`.
  The inputs are renamed too: `simsquad.toml` `[context]`,
  `deploy --context KEY=VALUE` / `--clear-context`, and the
  `simsquad set-context` verb. Skill contract 3; rerun
  `simsquad skill install` to update the installed skills.

### Deprecated

- `[env]`, `deploy --env` / `--clear-env` and `simsquad set-env` still work
  for this release and print a warning on stderr. They will be removed in
  the next minor release. `simsquad equip` saves an `[env]` table back as
  `[context]`. Squads deployed by an older simsquad keep their context, and
  `run report` still reads an older run's `deploy.json`.

## [0.4.0] - 2026-10-12

Agent testing: tell your coding agent "verify PROJ-123" and it checks the
feature on your squad's simulators and emulators, one headless worker per
device, and hands back a verdict and a report. See
[docs/agent-testing.md](docs/agent-testing.md).

### Added

- **Agent skills.** `simsquad skill install [--dir] [--force]` writes two
  skills into the app repo: **simsquad** (everyday CLI use) and
  **simsquad-test** (feature verification, with the worker instructions).
  Codex finds them in `.agents/skills/`; Claude Code through
  `.claude/skills/` links. Install also merges worker permissions into
  `.claude/settings.json`, writes `.codex/rules/simsquad.rules`, adds
  `.simsquad/` to `.gitignore` and scaffolds `qa/README.md` (app notes) when
  missing. Generated files carry a do-not-edit header with a skill contract
  number; hand-edited skill files are only overwritten with `--force`.
  `simsquad equip` offers the install after saving.
- `simsquad skill status` checks that the installed skills match this
  binary's skill contract and that mobilecli is on `PATH` at 1.0.13 or
  newer. Exits `1` when the skills are newer than the binary.
- `simsquad run new <feature-file> [--fresh]` reads a Gherkin feature file
  (Background, Scenario Outlines, `@ios` / `@android` / `@explore` tags, a
  `Source:` line), picks the squad (`qa-<repo>`) and platforms, computes the
  worker deadline and creates `.simsquad/runs/<run-id>/` with `run.json` and
  a copy of the feature. Refuses unequipped platforms and a squad busy with
  an unfinished run.
- `simsquad run worker --dir <worker-dir> --timeout <seconds> -- <command…>`
  runs one headless worker in its own process group with stdin closed, kills
  the whole group at the deadline and records `ok`, `blocked: timeout` or
  `error: worker exited <code>`.
- `simsquad run validate <worker-dir>` checks `result.json` against the
  embedded result schema and the evidence rule; emits `{valid, errors}` and
  exits `1` when invalid.
- `simsquad run report <run-dir>` aggregates the workers' results into
  `report.json` (embedded report schema: feature title, source, env,
  scenario × device matrix, findings) and `report.md`, emits
  `{verdict, report_json, report_md, counts}` and exits `0` passed,
  `1` failed, `2` infra. Writing `report.json` marks the run finished.
- `[agent]` table in `simsquad.toml`: `worker_model`, `test_squad`,
  `dev_squad`. `simsquad equip` keeps it when rewriting the config.
- **`--out <file>` on every command** writes the stdout JSON to a file too
  (parent folders created; removed when the command fails before emitting
  JSON). Agent harnesses that ask approval for shell redirection need it.
- Homebrew installs [mobilecli](https://github.com/mobile-next/mobilecli)
  1.0.13 from the same tap as a dependency.

### Changed

- **iOS builds are kept per app repo.** Derived data and the build log live
  under `~/.cache/simsquad/derived/ios/<repo>-<hash>/`, so one project's
  Swift packages can no longer break another project's link step.
- `deploy` returns its exit code instead of exiting directly, so `--out` and
  deferred cleanup run on exit `1` and `2` too.
- **Older `avdmanager` rejects dotted API levels**, not avdmanager in
  general: current command-line tools create `android-36.1` emulators. The
  pre-flight warning now says to update the tools.

### Fixed

- **simsquad used the wrong `avdmanager`.** It took the first one on `PATH`
  (often Homebrew's), which reads images from its own SDK rather than
  `ANDROID_HOME`, so equipped images were reported as invalid. It now
  prefers the SDK's own `cmdline-tools` and points a `PATH` copy at the
  configured SDK.
- An Android system-image folder left empty by an interrupted download no
  longer counts as installed; deploy reports it as missing.
- `simsquad equip` no longer drops render errors, and keeps the `[agent]`
  table when it rewrites `simsquad.toml`.

## [0.3.0] - 2026-10-08

First public release. Installable via `brew install peuf0u/tap/simsquad`.

### Added

- `Device.ready_at` on the persisted record (and `status --name` output),
  stamped whenever a deploy installs the app. Additive, non-breaking.

### Fixed

- **`devices` `ready_at` now moves on reuse.** It was the squad's
  `created_at`, so a spec-match redeploy that reinstalled the app kept the
  original timestamp. Records from older versions fall back to `created_at`.
- **Spurious `installd-wait-timeout` warning on iOS.** The readiness probe
  looked for `com.apple.installd` in `launchctl print system`, which never
  matches on current runtimes (the label is `com.apple.mobile.installd` and
  `print system` no longer lists sim services). Every iOS provision waited
  the full 15 s per sim before an install that then succeeded.
- **`simsquad --version` printed `dev` for `go install` builds.** It now
  falls back to the module build info when release ldflags are absent.

## [0.2.0] - 2026-06-18

### Changed

- **Canonical owner renamed `Patrez` → `peuf0u`.** Module path is now
  `github.com/peuf0u/simsquad`; install with
  `go install github.com/peuf0u/simsquad/cmd/simsquad@v0.2.0`. The old
  `Patrez` path still redirects for `v0.1.0`.

### Added

- `DeviceView.bundle_id` — the installed app's bundle identifier on every
  `devices` row, so a per-device test agent can launch/terminate/wipe the
  app without a second `status --name` lookup. Additive, non-breaking.
- **`reset` verb** — terminate the app and wipe its data on a squad's ready
  devices (iOS data-container wipe / Android `pm clear`) without
  re-provisioning. `--device` restricts to specific ids. Emits
  `{name, reset: [ids], errors?}`. (8th verb.)

### Fixed

- **iOS build now discovers the real product** via `xcodebuild
  -showBuildSettings` (`FULL_PRODUCT_NAME`/`PRODUCT_BUNDLE_IDENTIFIER`/
  `TARGET_BUILD_DIR`) instead of assuming `<scheme>.app`. Unblocks apps whose
  product name differs from the scheme (flavors, white-label, "Beta"/"Dev"
  products — e.g. a `MyApp` scheme building `MyApp Beta.app`). The
  project path is likewise discovered rather than assumed to be
  `<scheme>.xcodeproj`.
- **iOS build honors the committed `Package.resolved`** (passes
  `-onlyUsePackageVersionsFromResolvedFile` when present) so a clean
  derived-data build resolves to the same SwiftPM versions as a dev/Xcode
  build, instead of re-resolving and tripping newer SPM "traits"
  incompatibilities.
- **A failed deploy no longer leaves a stale, non-reusable squad.** If a
  deploy created the squad and nothing came ready, the empty record is
  removed; and a re-deploy now resets a same-name squad with no ready
  devices instead of erroring "not reusable, dismiss first".

## [0.1.0] - 2026-06-12

First internal release. Go rewrite of the Python `qa-pool` reference tool.

### Added

- 7 verbs — `deploy`, `status`, `devices`, `set-env`, `dismiss`, `sweep`,
  `equip` — each emits JSON on stdout, human progress on stderr.
- **Squads:** a named test context = env × device matrix. `--name` is
  required on `deploy`/`dismiss`/`set-env`. Naming discipline gives
  isolation between concurrent consumers; no lease primitive.
- **Squad env** — free-form `map[string]string` carrying credentials, API
  endpoints, feature flags, anything a consumer needs to drive tests. Set
  via `deploy --env KEY=VALUE` (repeatable), via `simsquad.toml [env]`
  (project-wide baseline), or mutated post-deploy with `simsquad set-env`
  (`--set` / `--unset` / `--clear`). The execution layer (`internal/ios/`,
  `internal/android/`) never reads env — it's pure consumer-facing
  metadata, so mutating it never re-provisions sims.
- **`DeviceView`** — uniform per-device JSON shape (`platform`, `id`,
  `name`, `model`, `os_version`, `status`, `ready_at`, `env`) emitted by
  `simsquad devices`. Same shape for iOS and Android; the squad's env is
  copied onto every row so consumers don't need to cross-reference.
- **`devices` with no `--name`** lists every squad's devices grouped by
  squad, mirroring how `status` lists all squads.
- Equip wizard for `simsquad.toml` / `simsquad.local.toml`, including an
  interactive env editor (`Edit env` action) and an alphabetised `[env]`
  block in the rendered TOML.
- iOS provisioning landmines worked around: the `installd` race after
  `simctl bootstatus`, the Apple-Silicon `idb install` bug
  (use `simctl install` + `idb file rm` only for wipe), runtime identifier
  normalisation for `simctl create`.
- Android provisioning landmines worked around: pre-flight warning for
  dotted API levels (`avdmanager` rejects `android-36.1` style),
  emulator-on-boot-timeout cleanup, concurrent physical-device claim race
  (held under the registry flock).
- Two-layer TOML loader: `simsquad.toml` (team defaults, committed) deep-
  merged with `simsquad.local.toml` (per-developer, gitignored).
  Precedence: CLI flag > env var > local TOML > project TOML > default.
- **Spec-flag replacement rule.** Passing any `--ios-spec` or
  `--android-spec` to `deploy` ignores `[[ios.sims]]` and `[[android.sims]]`
  from TOML entirely; omitting a platform's flag in that mode skips that
  platform for this run. `--env` follows the same shape against TOML
  `[env]`. Scalar flags (`--ios-repo`, `--ios-scheme`, …) still override
  only their own key. Locked by `TestResolveSpecsCliMode`.
- `~/.cache/simsquad/squads.json` registry keyed by squad name;
  `<name>.json` per-squad records; `derived/` build cache (xcodebuild
  derived data + build logs).
- `simsquad sweep` prunes orphan `simsquad-*` sims/AVDs left by a crashed
  deploy (devices with no registry entry).

### Documentation

- [`README.md`](README.md) — quickstart, concepts, verb/flag reference,
  configuration, known limitations.
- [`simsquad.toml.example`](simsquad.toml.example) — annotated sample
  config.
- [`docs/integrations/consumer.md`](docs/integrations/consumer.md) — how
  a test driver (AI agent, CI, qa-run) consumes a squad.
- `CLAUDE.md` — contributor reference: locked decisions, the five usage
  scenarios that drive the design, JSON contract table, landmines.

### Notes on the design

The verbs and concepts were pared back from a larger set after enumerating
the actual usage scenarios (see `CLAUDE.md` → "Usage scenarios"). Earlier
drafts carried leases (`claim` / `release`), TTL + auto-GC, and an
ephemeral-vs-named distinction. None of those earned their place against
the real scenarios — every consumer is the sole owner of its squads, so
mutual exclusion via leases isn't needed, and explicit `dismiss` covers
cleanup. Naming discipline (`agent-{session_id}`, `ci-pr-{num}`,
`dev-{user}`) is the isolation mechanism.
