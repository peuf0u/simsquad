# Changelog

All notable changes are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
