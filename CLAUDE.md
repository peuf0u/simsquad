# simsquad — Project Instructions for Claude Code

`simsquad` is a single-binary device-squad provisioner for parallel mobile QA
on macOS. It builds an iOS or Android app, creates a matrix of fresh
simulators or emulators, installs the build, and emits a machine-readable
squad descriptor on stdout. A downstream test driver (e.g. the `qa-run` skill)
consumes the squad via `simsquad devices` — the contract is the CLI itself,
not a JSON file on disk.

This repo is a **Go rewrite of an existing Python tool** living in a sibling
codebase (`qa-pool`, internal — its local path lives in the gitignored
`CLAUDE.local.md`). That Python reference is the **behaviour parity
baseline** — when you're unsure how something should work, read the
Python source first.

## Locked decisions (don't re-litigate without strong reason)

- **Module path:** `github.com/peuf0u/simsquad`. Owner: `peuf0u/simsquad`. Brew
  tap is `peuf0u/homebrew-tap` (`brew install peuf0u/tap/simsquad`).
- **License:** MIT.
- **Go version:** 1.26 (the plan said 1.23; bumped because a transitive Charm
  dep needs ≥1.25, and there's no reason to lag behind the toolchain's latest).
- **CLI stack:** `charmbracelet/fang` (cobra wrapper) + `charmbracelet/huh`
  (wizard forms) + `charmbracelet/bubbletea` + `charmbracelet/bubbles`
  (equip re-edit TUI) + `charmbracelet/lipgloss` (styling). The real
  invariant is **stdout = JSON contract, stderr = everything human** — any
  TUI must use `tea.WithOutput(os.Stderr)`.
- **Verbs (8):** `deploy`, `status`, `devices`, `set-env`, `reset`, `dismiss`,
  `sweep`, `equip`. Each emits JSON on stdout; nothing else does. (`reset`
  was added after two consumers — a first real-app integration and simsquad-pilot
  — needed a clean app instance between scenarios without re-provisioning.)
  Two command groups sit beside them for agent testing (spec #1), each
  emitting JSON on stdout too: `skill` (`install`, `status`) and `run`
  (`new`, `worker`, `validate`, `report`).
  Lease/claim/release and TTL/GC machinery were considered and cut — they're
  not in any of the real usage scenarios (see "Usage scenarios" below).
- **Squad = named test context = env × device matrix.** A squad has a `name`
  (its sole identifier), a free-form `env` blob (credentials, API endpoint,
  feature flags), and a set of provisioned devices. Multi-squad is meaningful
  because each squad is a distinct test context — keep `alice-staging` /
  `bob-staging` / `alice-prod` around in parallel.
- **One identifier: the name.** `--name` is required on `deploy`, `dismiss`,
  `set-env`. State file is `~/.cache/simsquad/<name>.json`; the registry is
  keyed by name. There are no ephemeral squads, no auto-generated handles,
  no `--squad-id` anywhere.
- **Naming convention provides isolation.** Each consumer picks a unique
  squad name (`agent-{session_id}`, `ci-pr-{num}`, `dev-{user}`). simsquad
  has no lease primitive; isolation is the caller's responsibility. If two
  callers pick the same name they'll collide on `deploy` (spec-match reuse)
  or `dismiss` (one wins).
- **Env is decoupled from execution.** `internal/ios/` and `internal/android/`
  never read `Env`. It's consumer-facing metadata only, surfaced on
  `SquadRecord` / `SquadPublic` / `DeviceView`. Mutating env via `set-env`
  never re-provisions sims.
- **Spec flags replace the TOML matrix wholesale, across both platforms.**
  Passing any `--ios-spec` *or* `--android-spec` to `deploy` ignores
  `[[ios.sims]]` and `[[android.sims]]` from TOML entirely; omitting a
  platform's flag in that mode skips that platform for this run. With no
  spec flags, both TOML matrices are used as the project default. Same
  shape for `--env` vs TOML `[env]`: any `--env` flag replaces the TOML
  block. Scalar flags (`--ios-repo`, `--ios-scheme`, …) override only
  their own key.
- **Sim/AVD name prefix:** `simsquad-`. Orphan pruner matches
  `^simsquad-[a-z0-9-]+-(ios|android)-\d+$`. Sim names are
  `simsquad-<squad-name>-<platform>-<index>`.
- **Cache root:** `~/.cache/simsquad/` (overridable via
  `SIMSQUAD_CACHE_DIR` for tests).

## Usage scenarios (drives every design choice)

These are the only scenarios simsquad must support. Any future feature must
trace back to one of them.

1. **Local dev — 1–2 sims continuously.** Developer keeps a named squad
   (`dev-sim` or similar) running on their Mac for manual exploration,
   one-off smoke runs via an agent, and quick "launch the app on this design"
   loops. Single user, single machine, long-lived.
2. **CI / PR.** A CI job deploys a matrix squad with env per run, fans out
   tests, posts a report on the PR, dismisses at end of job. Squad name
   per-PR (`ci-pr-1234`). Strongest argument for the matrix + env story.
3. **AI agent (main use case).** An orchestrator agent deploys one or more
   named squads, gets their device lists via `simsquad devices`, spawns
   sub-agents in parallel (one per device, allocated *in-process* — simsquad
   doesn't track sub-agents), aggregates a report, dismisses.
4. **Concurrent users on one machine.** Rare. Naming discipline prevents
   collisions; no machinery beyond that.
5. **Multi-persona within an agent run.** The agent deploys multiple squads
   with different env blobs (`alice-staging`, `bob-staging`), one persona per
   squad, running in parallel.

## Non-negotiable JSON contract

Downstream consumers (qa-run, AI agents, CI scripts) parse simsquad's CLI
output by JSON key. Renaming a field is a breaking change.

| Surface | Where it's defined | Notes |
|---|---|---|
| Public stdout (`deploy`, `status --name=<squad>`) | `internal/contract.SquadPublic` | Squad record minus internal `Ports`. |
| Per-squad record file | `internal/contract.SquadRecord` | Persisted at `~/.cache/simsquad/<name>.json`. `Name` is the sole identifier. Includes `Ports` (internal). |
| Registry | `internal/contract.SquadIndex` / `SquadIndexEntry` | `~/.cache/simsquad/squads.json`. Map keyed by squad name. |
| Consumer device view (`devices`) | `internal/contract.DeviceView` | Uniform `{platform, id, name, model, os_version, bundle_id?, status, ready_at, env?}`. Platform-tagged; same shape iOS + Android. `env` is the squad's env copied onto every row. |
| Env block | `map[string]string` on `SquadRecord` / `SquadPublic` / `DeviceView` | Free-form test context. Set via `deploy --env KEY=VALUE` or `simsquad.toml [env]`; mutated via `simsquad set-env --set/--unset/--clear`. Execution layer never reads it. |
| Exit codes | `internal/cli/deploy.go::exitCodeFromDevices` | `0` = all ready; `2` = mixed; `1` = none ready / bad args / build failed. |
| Run record | `internal/contract.RunRecord` | `<repo>/.simsquad/runs/<run-id>/run.json`, written by `run new`. |
| Worker result | `internal/contract.WorkerResult` + `internal/contract/schemas/result.schema.json` | `workers/<device-id>/result.json`; checked by `run validate`. |
| Report | `internal/contract.Report` + `internal/contract/schemas/report.schema.json` | `report.json` from `run report`; exit `0` passed / `1` failed / `2` infra. |

`devices` (no `--name`) emits `{"squads": [{name, devices}, …]}` — mirrors
how `status` lists all squads.

## Build / lint / test

```sh
make build      # produces ./bin/simsquad
make install    # installs to $GOBIN
make test       # go test ./...
make lint       # golangci-lint (brew install golangci-lint)
make fmt        # gofmt + goimports -local github.com/peuf0u/simsquad
make tidy       # go mod tidy
```

A typical post-change smoke test:

```sh
make build
SIMSQUAD_CACHE_DIR=/tmp/simsquad-smoke ./bin/simsquad status   # should emit {"squads": []}
```

## Directory layout

```
cmd/simsquad/main.go          # fang.Execute wiring (entrypoint)
internal/
  cli/                        # one file per verb; root.go assembles the tree
  contract/                   # JSON shapes — the documented surface area
  config/                     # two-layer TOML loader (simsquad.toml + .local.toml)
  state/                      # flock-protected squads.json + port allocator
  util/                       # paths, time, IDs, exec, JSON IO, git
  progress/                   # stderr event logger (channel-drained)
  tui/                        # shared lipgloss styles
  ios/                        # (Day 2-3) build + provision + idb wrapper
  android/                    # (Day 2-3) build + provision + sdk discovery
  discover/                   # (Day 2) sim/runtime/repo discovery
  teardown/                   # (Day 3) shared by dismiss + sweep
  wizard/                     # equip TUI — huh for first-run, bubbletea
                              # editor (bubble_editor.go) for re-edits
  agenttest/                  # run worker supervision, result validation,
                              # report + verdict
  feature/                    # Gherkin feature files → scenarios
  runsetup/                   # `run new`: squad/platform choice, run folder
  skills/                     # embedded agent skills + `skill install/status`
```

## Plan & roadmap

The full port plan, including day-by-day order, smoke ladders, and landmines,
lives in
`~/.claude/plans/buzzing-singing-gadget.md` (outside the repo). Refer to it
when planning the next module port.

Day-by-day:

- **Day 1 (done):** scaffolding, `contract/`, `util/`, `progress/`, `tui/`,
  `config/`, `state/`, `cli/status` and stubs for the other verbs. Gate:
  `simsquad status` returns `{"squads": []}` end-to-end.
- **Day 2 (done):** `internal/ios/build.go`, `internal/android/{build.go,sdk.go}`,
  `internal/discover/*`, idb wrapper. Gate: parallel builds via `errgroup` in
  a stubbed `deploy`.
- **Day 3 (in flight, uncommitted in working tree):** `internal/ios/provision.go`
  (the `installd` race — see Landmines below), `internal/android/provision.go`,
  `internal/teardown`. Gates: Ladder 1 (cold iOS) and Ladder 2 (multi-sim matrix).
- **Day 4:** CLI wiring (real `deploy`, `dismiss`, `sweep`), `equip` polish
  (huh first-run + bubbletea re-edit), goreleaser tag → brew tap. Gate:
  Ladder 3 (qa-run integration).

## Critical landmines (do not regress)

These are recorded in detail in the plan; the highlights:

1. **`installd` race on iOS provisioning.** `simctl bootstatus -b` returns
   before `com.apple.installd` is registered. Poll
   `simctl spawn <udid> launchctl print system | grep com.apple.installd`
   with a 15s timeout before invoking `simctl install`. Skipping this causes
   `IXErrorDomain 2 "Failed to set metadata"`.
2. **`idb install` broken on Apple Silicon.** Use `simctl install` for install,
   `idb file rm` only for the data-container wipe. Don't "fix" this backwards.
3. **`simctl create` rejects runtime short forms.** Normalise via
   `discover.ResolveRuntimeIdentifier` before invoking simctl.
4. **`avdmanager` chokes on `android-36.1`** (any system image with a `.N`
   suffix). Pre-flight warning + README "Known limitations" entry.
5. **Orphan emulator on boot timeout.** Only `Setpgid` after the boot poll
   passes; on timeout, kill the captured `*exec.Cmd`.
6. **`2>&1` would break stdout JSON parsing.** Docs and `--help` text must
   show `> squad.json` (stdout-only) redirection explicitly.
7. **Concurrent physical-device claim race.** Hold the index flock across the
   full `list → claim → save record` block, not just per-step.

## Conventions

- Stdout is the JSON contract; stderr is for human-readable progress (via
  `internal/progress`). Never mix them.
- TUI rows mark selection with a left-side bar (`▌`) in cyan, not full-row
  background fill. Selected label gets accent color; the row keeps its
  normal background.
- Form fields never truncate values. Widen the panel to terminal width,
  substitute `$HOME` → `~` for paths, soft-wrap onto continuation lines
  under the value column if still too long.
- No `select` / type chips on form-field rows — the picker opens on Enter
  and is self-explanatory.
- Every package has a one-paragraph `// Package …` doc comment.
- Field names in `internal/contract` are part of the surface area — changing
  them is a breaking change. Don't rename without a contract bump.
- Tests live next to the code they cover (`foo_test.go`). High-risk modules
  (Day 3+) should get golden-input tests; the plan calls out specifically
  `_spec_matches` (Day 4) and runtime-identifier resolution (Day 2).
- Comments explain *why*, not *what*. Code that names a landmine cites the
  Python source line and the failure mode it prevents.

## Agent skills

### Issue tracker

Issues live in GitHub Issues on `peuf0u/simsquad`, managed with the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five default triage labels (`needs-triage`, `needs-info`, `ready-for-agent`, `ready-for-human`, `wontfix`), plus `spec` for spec issues, which get only the `spec` label and never a triage label. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: root `GLOSSARY.md` plus `docs/adr/`. See `docs/agents/domain.md`.
