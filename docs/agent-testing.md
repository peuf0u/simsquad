# Agent testing

You tell your coding agent "verify PROJ-123", "verify
https://github.com/acme/app/issues/456" or "verify that resend is disabled
for 30 seconds". The agent turns that into a Gherkin feature file, starts one
headless **worker** per simulator or emulator, and hands you a **verdict**
(passed, failed or infra) with a report and screenshot evidence. Claude Code
and Codex are supported; Codex is experimental.

The simsquad binary never calls an LLM. The agent decides what to test and
drives the devices; every deterministic step (building, devices, time limits,
result validation, the verdict) is a simsquad command, so the agent has
fewer steps to get wrong.

Vocabulary: [GLOSSARY.md](../GLOSSARY.md). Decisions:
[ADR 0001](adr/0001-agent-testing-ships-inside-simsquad.md) (it ships inside
simsquad), [ADR 0002](adr/0002-mobilecli-not-mcp.md) (mobilecli drives
devices, not an MCP server),
[ADR 0003](adr/0003-gherkin-feature-files-without-step-definitions.md)
(Gherkin without step definitions). The original design is the spec in
[issue #1](https://github.com/peuf0u/simsquad/issues/1); where it and this
page differ, this page and the README describe the current behaviour.

## Set up a project

1. Install simsquad. Homebrew also brings mobilecli, the device driver:

   ```sh
   brew install peuf0u/tap/simsquad
   ```

2. In the app repo, run `simsquad equip` to describe the **equipment**: the
   app repos, build scheme or Gradle task, and the simulators and emulators
   to test on. It writes `simsquad.toml` (shared) and `simsquad.local.toml`
   (your paths, gitignored), then offers to install the agent skills. Accept,
   or run `simsquad skill install` later.

3. Fill in `qa/README.md`, the **app notes** every worker reads before it
   touches a device (see [Writing app notes](#writing-app-notes)).

4. Commit `simsquad.toml`, `.agents/`, `.claude/`, `.codex/`, `.gitignore`
   and `qa/`. Colleagues get the skills and app notes when they clone.

`skill install` writes:

| Path | What |
|---|---|
| `.agents/skills/simsquad/` | The everyday skill: deploying, listing devices, resetting, dismissing |
| `.agents/skills/simsquad-test/` | The test-run procedure (`SKILL.md`) and the worker instructions (`worker.md`) |
| `.claude/skills/<skill>` | Links for Claude Code |
| `.claude/settings.json` | Permissions merged in: `mobilecli`, `simsquad reset`, `simsquad run validate`, edits under `.simsquad/runs/` |
| `.codex/rules/simsquad.rules` | The same command allowances for Codex |
| `.gitignore` | A `.simsquad/` line (drafts and run folders stay local) |
| `qa/README.md` | The app notes scaffold, only when missing; never overwritten |

The skill files carry a do-not-edit header with the **skill contract**
number. After upgrading simsquad, re-run `simsquad skill install`;
`simsquad skill status` tells you when you need to.

## Run a test

Ask your agent, in its own words:

- "verify PROJ-123" (a Jira key: the agent reads the ticket with its own
  tools)
- "verify https://github.com/acme/app/issues/456"
- "verify that resend is disabled for 30 seconds"
- "verify on iOS only that …"
- "re-run login" (a feature file saved in `qa/features/`)

When the agent had to interpret a ticket or a longer description, it shows
you the feature file first and waits for your OK. A one-line request starts
at once, as a single exploratory scenario whose charter is your request.

At the end the agent tells you the verdict, the counts, the top findings
with screenshot paths, and where `report.md` is. It offers to save a drafted
feature file to `qa/features/<slug>.feature` so you can re-run it by name.

## What happens during a run

The agent you talk to is the **orchestrator**. It follows the simsquad-test
skill:

| Step | Command | What it does |
|---|---|---|
| Check the setup | `simsquad skill status` | The skills match the binary; mobilecli is installed and new enough |
| Draft | (the agent) | Writes the feature file to `.simsquad/drafts/<slug>.feature` |
| Start | `simsquad run new` | Parses the Gherkin, picks the squad (`qa-<repo>`) and platforms, sets the worker time limit, creates `.simsquad/runs/<run-id>/` |
| Deploy | `simsquad deploy --force-build` | Builds the working tree and installs it on the equipped devices, booting any that are down |
| List devices | `simsquad devices --ready` | The device rows the workers get |
| Workers | `simsquad run worker -- claude -p …` or `-- codex exec …` | One headless agent per device, all at once; simsquad kills each worker and everything it started at the time limit |
| Validate | `simsquad run validate` | Checks each worker's `result.json` against the result schema |
| Report | `simsquad run report` | Writes `report.json` and `report.md`; the verdict is the exit code |
| Dismiss | `simsquad dismiss` | Only for a `--fresh` squad; the project squad stays up so the next run skips the cold boot |

The project squad is reused across runs, so only the first run pays for
booting devices. If the equipment changed since the squad was deployed,
deploy refuses to reuse it and the orchestrator dismisses it and deploys
again. A failed build goes straight to the report with an infra verdict;
diagnosing it is left to you, with the build log path in the reply.

## Workers

Each worker is a fresh, headless agent with one device. It reads
`worker.md`, the run's `run.json`, the app notes and its device row, then
drives the device with mobilecli: it reads the screen (`mobilecli dump ui`),
taps, types, swipes and takes screenshots. Before every scenario it resets
the app to a clean state with `simsquad reset`.

It runs the scripted scenarios first and the `@explore` scenario, if any,
last, as timeboxed free testing against its charter. It writes `result.json`
early and rewrites it after every scenario, so a worker cut off at its time
limit still leaves a valid partial result. Its files are its only output:

```
.simsquad/runs/<run-id>/
  run.json  feature.feature  deploy.json  devices.json
  workers/<device-id>/
    prompt.md      the orchestrator's instructions to this worker
    result.json    per-scenario passed / failed / blocked, plus findings
    log.md         a short narrative of what the worker saw and did
    screenshots/   the evidence
    worker.log     the worker's raw output
    worker.json    {"status": "ok" | "blocked: timeout" | "error: …"}
  report.json  report.md
```

**Findings** are bugs (with a severity: high, medium or low), questions
(behaviour that may or may not be intended) and notes. `run validate`
enforces the evidence rule: every bug, question and blocked scenario has
evidence, and every screenshot it names exists in the worker folder.

The time limit is `120 + Σ(60 + 30 × steps)` seconds over a platform's
scenarios, plus 600 when it has the `@explore` scenario. The busiest
platform sets it, and every worker in the run gets the same limit.
Set the worker model with `[agent] worker_model` in `simsquad.toml`; it is
passed to the worker command as given.

## Verdicts

| Verdict | Exit | When |
|---|---|---|
| passed | `0` | Every scenario passed on every device, no bugs, every device deployed |
| failed | `1` | A failed or blocked scenario, a bug, or a worker that was blocked or errored |
| infra | `2` | The deploy failed, no device was ready, no worker left a usable result, or a device wasn't deployed while the devices that ran found nothing wrong |

Infrastructure problems never show up as app failures.

## Feature files

Plain Gherkin, without step definitions: the worker reads the steps and
carries them out like a person would.

```gherkin
@ios
Feature: Resend code cooldown
  Source: PROJ-123

  Scenario: Resend is disabled right after a code is sent
    Given the login screen is open
    When I enter "alice@example.com" and tap "Send code"
    Then the "Resend code" button is disabled
    And a countdown from 30 seconds is visible

  @explore
  Scenario: Probe the resend flow
    Charter: tap "Resend code" repeatedly, background the app during the
    countdown, and try an invalid email.
```

- `Source:` in the feature description links the report to the ticket,
  issue or request.
- `@ios` / `@android` restrict a feature or scenario to one platform. A tag
  for a platform the equipment lacks is refused. Untagged scenarios run on
  every equipped platform.
- At most one `@explore` scenario; its description is the charter.
- `Background:` steps run before every scenario; a `Scenario Outline`
  becomes one scenario per `Examples:` row. Other tags are kept and ignored.
- Quote UI labels verbatim, in the app's language.

## Writing app notes

`qa/README.md` is the only project knowledge a worker gets, and the main
lever on how far it gets. Keep it short and concrete:

- **Getting in:** which accounts or personas to use on which stage and
  which squad env keys hold their credentials, how to log in, one-time
  codes or how to skip them, onboarding screens to dismiss. Keep real
  secrets in the squad env, not in this file.
- **Navigation:** how to reach the main areas, hidden menus (debug menu,
  long-presses), deep links.
- **Known quirks:** slow first launch, permission dialogs, flaky screens,
  anything a worker should not report as a bug.
- **Language:** the UI language, and whether labels differ by locale.

Credentials and stage URLs that change per squad belong in the squad's
`env` (`simsquad.toml [env]` or `deploy --env`), which every worker gets in
its device row.

## Codex

Codex support is experimental. Deploys write outside the repo
(`~/.cache/simsquad/`, Xcode's DerivedData and simulator data under
`~/Library/Developer/`, the Android SDK, `~/.gradle`) and workers reach
mobilecli's on-device agent over the network, which Codex's default
`workspace-write` sandbox blocks. Start the session with
`codex --sandbox danger-full-access` (or set `sandbox_mode =
"danger-full-access"` in `~/.codex/config.toml`); the skill starts its
workers with `codex exec --sandbox danger-full-access`.

## Known gaps

- A run restricted to one platform still builds and boots every equipped
  platform ([#23](https://github.com/peuf0u/simsquad/issues/23)).
- Projects whose iOS and Android apps live in separate repos have no
  documented home for the config, skills and app notes
  ([#22](https://github.com/peuf0u/simsquad/issues/22)).
