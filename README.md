# simsquad

A single-binary device-squad provisioner for parallel mobile QA on macOS.

`simsquad` builds your iOS or Android app, provisions a matrix of simulators
or emulators, installs the build, and emits a machine-readable **squad
descriptor** on stdout. A downstream test driver (an AI agent, a CI job, or a
developer) then drives parallel tests against those devices via
`simsquad devices`.

Deploys are **idempotent by squad name**: re-deploying the same name with the
same matrix reuses the running sims/emulators and just reinstalls the build —
no reboot, no re-provision. That fast reinstall-in-place loop (iterate on a
fix, redeploy, retest) is the real cost-saver, not cold sim creation. A fresh
matrix is created only when the name is new or its devices are gone.

The integration surface is the **CLI itself** — every verb emits JSON on
stdout, human progress on stderr. There are no sidecar files to plumb.

## Status

Internal pre-release. Go rewrite of the Python `qa-pool` reference tool. macOS
only (it shells out to `xcrun simctl`, `xcodebuild`, `idb`, `adb`,
`avdmanager`, `emulator`).

## Install

```sh
brew install peuf0u/tap/simsquad
# or
go install github.com/peuf0u/simsquad/cmd/simsquad@latest
```

Homebrew also installs [mobilecli](https://github.com/mobile-next/mobilecli)
(from the same tap), which agent testing uses to drive devices. With
`go install` or a source build, install mobilecli 1.0.13 or newer yourself and
put it on `PATH`. Node.js is not required.

From a checkout: `make build` (→ `./bin/simsquad`) or `make install` (→ `$GOBIN`).

## Quickstart

```sh
# Stand up a named squad: one iPhone 17 sim with the app installed.
simsquad deploy --name=login-tests \
  --ios-repo ~/code/myapp-ios \
  --ios-spec 'iPhone 17:iOS 26.4:1' \
  --env user=alice --env api=staging > squad.json
#    stdout is the squad descriptor (redirect it; never use 2>&1).

# Ask for the device list (the qa-run / AI interface).
simsquad devices --name=login-tests --ready

# … run tests against each device's `id` (udid / emulator serial) …

# Tear it down.
simsquad dismiss --name=login-tests
```

## Concepts

- **A squad is a named test context = env × device matrix.** Each squad has a
  name (its sole identifier), an optional free-form `env` blob (credentials,
  API endpoint, feature flags — whatever your tests need), and a set of
  provisioned devices. Keep several squads around (`alice-staging`,
  `bob-staging`, `alice-prod`) to run different personas/environments in
  parallel.

- **Naming discipline = isolation.** simsquad doesn't have lease primitives.
  Pick a unique name per consumer — `agent-{session_id}`, `ci-pr-{num}`,
  `dev-{user}` — and nothing else will touch your squad.

- **stdout = JSON contract, stderr = human progress.** Always redirect stdout
  only (`> squad.json`). Using `2>&1` mixes progress events into the JSON and
  breaks parsers.

## Verbs

| Command | Action | Key flags |
|---|---|---|
| `deploy` | Build + install; reuse the squad if it exists (same name+matrix), else provision fresh; emit the descriptor | `--name` (req), `--ios-repo`/`--android-repo`, `--ios-spec`/`--android-spec`, `--env KEY=VALUE`, `--no-build`, `--force-build`, `--preserve-data`, `--clear-env` |
| `status` | Squad registry (no arg) or one full record (`--name`) | `--name` |
| `devices` | Uniform device list — one squad (`--name`) or all (no arg) | `--name`, `--ready`, `--platform=ios\|android` |
| `set-env` | Mutate a squad's env | `--name` (req), `--set KEY=VALUE`, `--unset KEY`, `--clear` |
| `reset` | Return apps to a clean state (terminate + wipe data) without re-provisioning | `--name` (req), `--device` (repeatable) |
| `dismiss` | Tear down a squad (shutdown + delete its sims/AVDs) | `--name` (req) |
| `sweep` | Prune orphan `simsquad-*` sims/AVDs left by crashed deploys | — |
| `equip` | Interactive wizard: write `simsquad.toml` / `.local.toml` | `--show`, `--force`, `--add-ios-spec`, `--add-android-spec` |

Spec formats (repeatable):

```
--ios-spec     DEVICE:RUNTIME:COUNT          e.g. 'iPhone 17:iOS 26.4:1'
--android-spec DEVICE:IMAGE:COUNT[:TARGET]   e.g. 'pixel_7:system-images;android-34;google_apis;arm64-v8a:1'
```

Passing **any** spec flag replaces the TOML matrix entirely for **both**
platforms — omit a platform's flag to skip it for this run. With no spec
flags, both `[[ios.sims]]` and `[[android.sims]]` from `simsquad.toml` are
used.

`deploy` exit codes: `0` = all devices ready · `2` = mixed (some ready, some
errored) · `1` = none ready / bad args / build failed.

## Agent skills

The `skill` group installs the agent skills embedded in the binary into an
app repo, so the team commits them and every clone gets them. Two skills
ship: **simsquad** (everyday CLI use) and **simsquad-test** (verifying a
feature on a squad).

| Command | Action | Key flags |
|---|---|---|
| `skill install` | Write both skills into the app repo; emit `{dir, version, contract, files}` | `--dir`, `--force` |

Run it from the app repo (the enclosing git work tree is the root):

```sh
simsquad skill install > skill-install.json
```

It writes:

- `.agents/skills/simsquad/` and `.agents/skills/simsquad-test/` (Codex
  discovers them here), with `.claude/skills/<skill>` links for Claude Code.
  `--dir <dir>` writes the skills there instead, for other agent tools, and
  makes no `.claude/skills` links.
- `.claude/settings.json`: allows `mobilecli`, `simsquad` and writes under
  `.simsquad/runs/`, so headless workers never wait on a prompt. Existing
  settings are kept; the entries are merged in.
- `.codex/rules/simsquad.rules`: the same command allowances for Codex.
- A `.simsquad/` line in `.gitignore`, added once.

Every generated file starts with a do-not-edit header carrying the binary
version and the **skill contract** number, which changes only when the
interface between the skills and the CLI changes. Put project knowledge in
`qa/README.md`, not in the skill files: install refuses to overwrite a skill
file that was edited by hand unless `--force` is given. Re-running install
is safe.

## Configuration

simsquad reads two optional TOML files from the nearest ancestor directory
containing either:

- `simsquad.toml` — team defaults, checked into git.
- `simsquad.local.toml` — per-developer overrides, gitignored.

Run `simsquad equip` for an interactive setup wizard, or copy
[`simsquad.toml.example`](simsquad.toml.example) and edit. See that file for the
full annotated schema (`[project]`, `[env]`, `[[ios.sims]]`, `[[android.sims]]`).

**Precedence**, highest to lowest:

```
CLI flag  >  env var  >  simsquad.local.toml  >  simsquad.toml  >  built-in default
```

Environment variables: `SIMSQUAD_IOS_REPO`, `SIMSQUAD_ANDROID_REPO`,
`SIMSQUAD_CACHE_DIR` (cache root; defaults to `~/.cache/simsquad`).

## State

simsquad keeps a per-user registry and per-squad records under
`~/.cache/simsquad/`:

- `squads.json` — the registry, keyed by squad name.
- `<name>.json` — one record per squad.
- `derived/` — xcodebuild derived-data + build logs (the expensive build cache).

To wipe squad state without losing the build cache, remove the JSON files but
keep `derived/`.

## Known limitations

- **macOS only.** Relies on Xcode / Android SDK command-line tools.
- **`avdmanager` rejects dotted API levels.** System images like
  `android-36.1` (any `.N` suffix) break `avdmanager`; install a plain level
  (e.g. `android-34`) via `sdkmanager`. simsquad pre-flights a warning.
- **Never `2>&1`.** It merges stderr progress into the stdout JSON contract.
  Redirect stdout only: `simsquad deploy … > squad.json`.
- **Physical Android devices** are claimed by adb serial; simsquad never
  deletes a physical device, only uninstalls the app on teardown.

## Documentation

- [`docs/integrations/consumer.md`](docs/integrations/consumer.md) — how a test
  driver (qa-run / AI agent / CI) consumes a squad: the deploy → devices →
  dismiss lifecycle, JSON shapes, and the naming convention for isolation.
- [`docs/execution-layer-mvp.md`](docs/execution-layer-mvp.md) — MVP design for
  agent-driven exploratory and smoke testing above the simsquad CLI.
- [`CHANGELOG.md`](CHANGELOG.md) — release notes.
- `CLAUDE.md` — contributor/contract reference (locked decisions, landmines).

## License

MIT — see [`LICENSE`](LICENSE).
