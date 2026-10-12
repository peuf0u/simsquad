# Dry run 002 — 2026-10-09 to 2026-10-12, welcome screen on a real iOS + Android app

The 0.4.0 release gate ([#12](https://github.com/peuf0u/simsquad/issues/12)):
the agent testing from spec [#1](https://github.com/peuf0u/simsquad/issues/1),
run against a real app with the skills installed and committed. iOS and
Android live in two repos under one umbrella folder that holds
`simsquad.toml`. Every attempt ran the same one-line request, "verify that
the welcome screen offers login by e-mail, visitor registration and
continuing without login", which the orchestrator drafted as a single
`@explore` scenario. Equipment: iPhone 17 / iOS 26.4, later plus a Pixel 7
emulator on `android-36.1`. The orchestrators ran headless (`claude -p`,
`codex exec`), and the workers used the tool's default model unless noted.

**Outcome:** all three legs passed.

| Leg | Run | Verdict | Time | Findings |
|---|---|---|---|---|
| Claude Code, iOS + Android | `20261011-190900` (attempt 6) | passed | 8 min | 0 bugs, 4 questions, 4 notes |
| Codex, iOS (`gpt-6-luna` workers) | `20261012-092237` | passed | 2 min | none |

Each verdict matches what the app does: both platforms show the three
entry options, and the questions are real product questions (below), not
harness noise. Codex ships as **experimental**: its sandbox can be set up
to allow deploys, but this leg is too thin to call it supported.

## Attempts

| # | Agent, platforms | Verdict | What went wrong | Fix |
|---|---|---|---|---|
| 1 | Claude Code, iOS | infra | The build failed to link: iOS derived data was shared with another project that builds the same dependency from source. `> deploy.json` needed approval, so the report said "cannot read deploy.json". The orchestrator then spent ~10 tool calls debugging the build | [#15](https://github.com/peuf0u/simsquad/issues/15), [#16](https://github.com/peuf0u/simsquad/issues/16) → PR #17: per-repo derived data, `--out`, deploy failure goes straight to the report |
| 2 | Claude Code, iOS | infra | `claude -p` ignores `.claude/settings.json` in a folder nobody has trusted interactively, so the worker had no permissions and wrote no `result.json` | PR #17: the worker command passes `--allowedTools` |
| 3 | Claude Code, iOS | failed | Correct: one low bug (a typo in a button label), one question, one note. Later runs, on an updated app, didn't see it | none needed |
| 4 | Claude Code, iOS + Android | infra | Adding the emulator changed the equipment, and deploy refused to reuse the squad. The orchestrator stopped after 10 seconds | PR #19: dismiss and deploy again, once |
| 5 | Claude Code, iOS + Android | failed | Android `avd-create` failed: Homebrew's `avdmanager` on `$PATH` read its own SDK, the equipped image folder was empty, and old cmdline-tools reject dotted API levels. iOS was clean, yet the verdict said failed | PR #20: SDK tool lookup, an empty image folder doesn't count as installed, an undeployed device with otherwise clean results is infra. Upgraded cmdline-tools |
| 6 | Claude Code, iOS + Android | passed | The Android worker found that mobilecli names an emulator by its AVD name, not `emulator-5554`, and worked around it | PR #21: `worker.md` tells the two ids apart |
| 7 | Codex, iOS | passed | See the Codex section below | none yet |

Smaller skill fixes along the way: no `Write(...)` permission rule, which
Claude Code never matches (PR #18); commands run one per call with nothing
chained, so each stays an allowed simsquad call (PR #19).

## Lessons → changes

1. **Setup was the problem, not the testing.** Every infra and wrong
   verdict came from the build, permissions, equipment or SDK tooling.
   Once a worker had a device, it tested well and wrote a valid
   `result.json` the first time, every time.
2. **Infra stays out of app verdicts, as designed, with one gap.** A
   partially deployed run used to come out "failed" with no app failure
   in it. It is now infra, and an app failure on the deployed devices
   still wins (PR #20).
3. **Approval prompts are what headless runs trip on.** Redirection, `rm`,
   `$(cat …)`, chained commands and folders nobody has trusted all stall
   or skip a step when nobody can answer. The skills now use only commands
   the installed permissions allow, and the skill tests enforce it.
4. **Platform-restricted runs and split repos need design work.** "Verify
   on iOS only" still builds and boots Android
   ([#23](https://github.com/peuf0u/simsquad/issues/23)). The umbrella
   layout worked, but it is undocumented, and the Codex worker missed the
   app notes because it looked in the app repo
   ([#22](https://github.com/peuf0u/simsquad/issues/22)).
5. **mobilecli's first-use iOS helper install: keep it deferred.** In
   attempt 6 the iOS app sat on its splash screen for about a minute right
   after `mobilecli agent install` backgrounded it, and the worker
   reported it as a question, flagging the harness as a possible cause. A
   second reset fixed it. That's one misleading question and no wrong
   verdict. Pre-install the helper during `deploy` if it happens again.
6. **App findings worth raising with the app team** (questions, not bugs):
   Android login and registration forms come prefilled with a developer's
   e-mail after a reset; Back on the Android Tickets tab exits the app; the
   guest choice isn't remembered across relaunches on either platform
   (which matches the spec on iOS).

## Codex

- **Sandbox:** deploys work with `danger-full-access` for the session and
  `codex exec --sandbox danger-full-access` for the workers, as the skill
  documents. That settles the open risk from the spec.
- **Depth:** the `gpt-6-luna` worker took one screenshot, confirmed the
  three buttons and stopped. That answers the request, but it's far less
  than the Claude Code workers did with the same charter: they opened each
  option, tried an empty registration and relaunched. Codex with a
  stronger worker model, and with app notes it can find, is untested.
- **Install path:** Codex's login shell found an older Homebrew simsquad
  (0.3.0) ahead of the dev build. `simsquad skill status` catches that
  case when the skills are newer than the binary.

Decision: Codex is **experimental** in 0.4.0, as the README and
[agent-testing.md](agent-testing.md) say.

## Not covered

- **`blocked: timeout`:** no worker hit its time limit in this dry run, so
  the kill path is covered by unit tests only (`run worker` kills the
  worker and every process it started, and writes `blocked: timeout`).
  It's left to show up in normal use.
- **App notes:** `qa/README.md` was the empty scaffold in every attempt,
  so the workers had no project knowledge.
