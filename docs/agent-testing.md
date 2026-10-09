# Agent testing in simsquad — design

Status: **proposed** (2026-10-09). Folds simsquad-pilot into simsquad and
replaces its device driver. Supersedes `docs/execution-layer-mvp.md` and the
simsquad-pilot repo once the slices below land.

## Problem

simsquad-pilot is a *workspace*: you `cd` into a clone of it, start an agent
session there, and point it at your app via `project_dir`. That inverts the
natural flow — the developer is in their app repo and wants to say "test the
login feature" there. Consequences of the inversion:

- you leave your project to run a test;
- reports land in pilot's `runs/`, away from the code they describe;
- feature contexts have no home;
- pilot and the simsquad binary version independently, so the skill can
  expect a contract the installed binary doesn't have (dry run 001 nearly
  misdiagnosed a missing `bundle_id` this way).

Root cause: the split was justified by "`AGENTS.md` at a repo root is what
harnesses discover", which couples *how the agent finds the procedure* to
*where the user must stand*. Skills decouple the two.

## Decisions

| # | Decision | Notes |
|---|---|---|
| 1 | **Pilot merges into simsquad** — one repo, one release, one install. | Re-confirms the 2026-06-18 "ship together" decision, which was never executed. |
| 2 | **Delivered as a skill installed into the app repo** by `simsquad skill install`, files embedded in the binary. | Per project and committed, so colleagues get it on clone and it is pinned with the project. Supersedes the June *plugin marketplace* plan: a plugin tracks its marketplace ref, not the installed binary; copying out of the binary keeps skill ↔ CLI in lockstep. |
| 3 | **Run artifacts live in the app repo** under `.simsquad/runs/<run-id>/` (gitignored). | Next to the code they test. |
| 4 | **Feature contexts live in the app repo** under `qa/features/<feature-id>.json`. | Conventional, committed, reviewable. |
| 5 | **No MCP.** Workers drive devices with **`mobilecli`** directly. | mobile-mcp is a wrapper around mobilecli; the CLI gives the same engine (a11y tree, uniform iOS/Android input) without 33 tool schemas per worker or an MCP-capable harness. |
| 6 | **Deterministic helpers become Go verbs**; python3 is no longer required. | Schemas embedded in the binary; validated by the same code that defines them. |
| 7 | **Headless/CI runs are deferred.** | Exit codes are defined now so CI can come later without a contract change. |

### simsquad's invariants still hold

- The binary **never calls an LLM**. Skill files are inert text it copies out.
- **stdout = JSON, stderr = humans** applies to the new verbs too.
- Provisioning verbs (`deploy`, `devices`, …) are unchanged; consumers that
  only provision never touch the agent layer.
- Env stays decoupled from execution.

## User experience

```sh
brew install peuf0u/tap/simsquad
npm install -g mobilecli          # device driver for agent tests
cd ~/code/my-app
simsquad equip                    # writes simsquad.toml; offers to install the skill
# or explicitly:
simsquad skill install            # → .claude/skills/simsquad-test/, .gitignore += .simsquad/
git add simsquad.toml .claude/skills/simsquad-test && git commit
claude
> test the magic-link login on iPhone
```

The skill turns the request into `qa/features/<id>.json` (or uses an existing
one), shows it, runs it, and replies with the verdict, top findings, and the
path to `.simsquad/runs/<run-id>/report.md`.

## Repository layout (after merge)

```
agent/                              # embedded via go:embed; inert text
  skill/
    SKILL.md                        # orchestrator procedure (was AGENTS.md)
    worker.md                       # single cross-platform worker prompt
  feature-context.example.json
internal/contract/schemas/          # embedded JSON Schemas
  feature-context.schema.json
  result.schema.json
  report.schema.json
internal/agenttest/                 # validate / aggregate / render / worker timeout
internal/cli/skill.go               # `simsquad skill …`
internal/cli/run.go                 # `simsquad run …`
```

The worker prompt becomes **one file** for both platforms: mobilecli's
commands are identical on iOS and Android, so only a short per-platform
quirks section differs (SpringBoard sheets vs Android system dialogs).
The `worker-android.md` slice from the pilot roadmap collapses into this.

## New verbs

Two new verb groups. `CLAUDE.md`'s "8 verbs" locked decision is amended
accordingly.

### `simsquad skill`

| Command | Effect | stdout |
|---|---|---|
| `skill install [--dir .claude/skills] [--force]` | Write `simsquad-test/SKILL.md` + `worker.md`, stamped with the binary version; append `.simsquad/` to `.gitignore`. Refuses to overwrite local edits without `--force`. | `{dir, version, files}` |
| `skill status` | Compare the installed skill's stamp to the binary. | `{installed, skill_version, binary_version, in_sync}` |

`--dir` covers harnesses that read skills from elsewhere. `SKILL.md` step 0
runs `skill status` and tells the user to re-run `skill install` when out of
sync — the drift that bit dry run 001 becomes a one-line diagnosis.

### `simsquad run`

Moves the orchestrator's bookkeeping into deterministic code so the agent
has fewer steps to get wrong.

| Command | Replaces | stdout | Exit |
|---|---|---|---|
| `run new <feature.json>` | `validate.py feature-context` + mkdir + run-id | `{run_id, run_dir, squad_name}` | 1 on invalid context |
| `run validate <worker-dir>` | `validate.py result` | `{valid, errors}` | 1 if invalid |
| `run worker --dir <d> --timeout <s> -- <cmd…>` | `run_worker.py` | `{status}` | worker's exit / 124 on timeout |
| `run report <run-dir>` | `aggregate.py` + `render.py` | `{verdict, report_json, report_md, counts}` | 0 passed / 1 failed / 2 infra |

`run report` writes `report.json` and `report.md` to the run dir and puts
only the summary on stdout (keeps the stdout-is-JSON rule). `run worker`
keeps run_worker.py's semantics: kill the whole process group on timeout,
write `blocked: timeout` / `error: worker exited <code>` to the worker's
`status` file.

## Orchestrator procedure (SKILL.md, abridged)

1. `simsquad skill status` — stop on drift; check `mobilecli` is on PATH.
2. Resolve the feature context (existing `qa/features/*.json`, or draft one
   from the request and confirm it with the user).
3. `simsquad run new <ctx>`.
4. `simsquad deploy --name <squad_name> … > <run_dir>/squad.json` (cwd = app
   repo, so `simsquad.toml` is found without `project_dir`).
5. `simsquad devices --name <squad_name> --ready` → `run.json`.
6. Fan out one worker per device (background + deadline wakeup on Claude
   Code; `simsquad run worker` for subprocess harnesses). Unchanged timebox
   rules from pilot.
7. `simsquad run validate` per worker.
8. `simsquad run report <run_dir>`.
9. `simsquad dismiss` — always, unless `--keep` / `--keep-on-failure`.
10. Report verdict + findings + path.

`project_dir` is dropped from the feature-context schema: the app repo *is*
the working directory.

## Worker driving with mobilecli

Verified 2026-10-09 against mobilecli 1.0.13 on an iOS 26.4 simulator:

| Need | Command |
|---|---|
| Observe | `mobilecli dump ui --device <id> --format text` — compact, `@eN` refs (≈2 KB vs ≈14 KB JSON for a 44-element screen) |
| Act | `io tap <x,y>`, `io text "<s>"` (diacritics OK), `io swipe`, `io button HOME` |
| Evidence | `screenshot --device <id> -o <run_dir>/screenshots/step-NNN.png --max-size 800` |
| App | `apps launch` / `apps terminate` |
| Reset | `simsquad reset --name <squad> --device <id>` (replaces the raw simctl wipe in the current prompt) |

Behaviour to account for:

- First use on an iOS simulator auto-installs an XCUITest runner
  (`com.mobilenext.devicekit-iosUITests.xctrunner`, ~4.5 s once) and starts
  a per-user background daemon. The runner dies with the sim on `dismiss`;
  the daemon is shared and left alone.
- On Android, mobilecli holds the device's only UiAutomation connection —
  nothing else may run `uiautomator` concurrently. Fine while mobilecli is
  the sole driver.
- Distributed via npm (`mobilecli` package, a Go binary inside). Node is
  still needed to *install* it, not to run workers.
- License FSL-1.1-ALv2: using it is fine; **simsquad does not bundle or
  redistribute it** — it is a documented prerequisite.

## Migration

- Port pilot's python tests as golden tests: same fixtures, Go
  implementation must produce identical `report.json` / `report.md`.
- Feature contexts with `project_dir` keep validating for one release
  (field ignored, warning on stderr), then the field is removed.
- simsquad-pilot gets a final README pointing to simsquad, then is archived.
- `docs/execution-layer-mvp.md` is replaced by this document.

## Slices

1. **Schemas + `run validate`** — embed schemas, port validate.py and its
   tests.
2. **`run new` + `run report`** — port aggregate.py / render.py with golden
   tests against the python output.
3. **`run worker`** — port run_worker.py (process-group kill, status file).
4. **Worker prompt on mobilecli** — single cross-platform `worker.md`;
   SKILL.md from AGENTS.md using the new verbs.
5. **`skill install` / `skill status`** + `equip` offering the install.
6. **Dry run 002** on a real app, iOS and Android, in the app repo. Gate for
   release.
7. **Release 0.4.0**; archive simsquad-pilot.

## Open questions

- **Pre-installing the mobilecli runner during `deploy`** saves ~4.5 s per
  iOS device but makes `deploy` know about the driver. Deferred until dry
  run 002 shows whether it matters.
- **mobilecli telemetry** — *checked 2026-10-09, v1.0.13:* the binary has no
  analytics endpoints or telemetry switches (unlike mobile-mcp's PostHog/
  Scarf); the only hosted URLs are mobilenext.ai login for the `remote`
  cloud-device commands, which workers never call. The npm package has no
  install scripts. Re-check on version bumps.
- **mobilecli version floor** — 1.0.13 is the verified version; `skill
  status` should check a minimum once the worker prompt is written.
- **Other harnesses' skill directories** — `--dir` covers it mechanically;
  which defaults to document (pi, Codex) is to be confirmed.
