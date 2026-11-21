---
name: simsquad-test
description: Verify an app feature on simulators and emulators with simsquad, from a Jira ticket, a GitHub issue, a plain description or a saved feature file. Use when the user asks to verify, test or check a feature on devices, or to re-run a saved feature file.
---

# simsquad-test

A **test run** verifies one feature file on the project's squad. You are the
orchestrator: you turn the source into a Gherkin feature file, start one
**worker** per device, and hand the user a **verdict** (passed, failed or
infra). Workers drive the devices; you handle setup, bookkeeping and the
reply, and keep your hands off the devices while workers run.

Every simsquad command answers with JSON on stdout and progress on stderr.
`--out <file>` also writes that JSON to a file (creating its folder); read
the JSON from the file, and leave stderr on the terminal for the user. Run
the commands exactly as shown, one per call with nothing chained (no `;`,
`&&` or `echo $?`; your tool reports the exit code), so each one stays a
plain simsquad call the user has already allowed.

Run everything from the app repo root (the folder holding `simsquad.toml`).
The examples use shell variables for the values you read from earlier
output; substitute them when your shell does not keep variables between
commands.

## 1. Check the setup

```sh
simsquad skill status --out .simsquad/skill-status.json
```

- Exit `1`: these skills are newer than the installed simsquad. Stop and
  ask the user to upgrade simsquad (`brew upgrade simsquad`).
- `mobilecli.found` false or `mobilecli.meets_minimum` false: stop. Tell
  the user that `brew install peuf0u/tap/simsquad` brings mobilecli, or to
  install mobilecli `mobilecli.minimum` or newer themselves.
- A `warning` with exit `0`: pass it on to the user (usually "re-run
  `simsquad skill install`") and continue.

## 2. Resolve the source

The **source** is what the feature file is derived from:

- A Jira key (`PROJ-123`): read the ticket with your own tools.
- A GitHub issue URL: `gh issue view <url> --comments`, or your own tools.
- A plain description ("verify that resend is disabled for 30 seconds"):
  the user's words are the source.
- The name of a saved feature file ("re-run login"): find it in
  `qa/features/` by file name or `Feature:` title and go straight to step 4.

When you cannot open a ticket or issue, ask the user to paste its content.

## 3. Draft the feature file

Write it to `.simsquad/drafts/<slug>.feature`, where `<slug>` is the
feature title in lowercase with hyphens. Conventions:

- The feature description holds a `Source:` line: the ticket key, the
  issue URL, or `user request: <their words>`. The report links to it.
- Each scenario is a user-visible behaviour, written as Given/When/Then
  steps a person could carry out on the device. Quote UI labels verbatim,
  in the app's language.
- `@ios` or `@android` on the feature or a scenario restricts it to that
  platform; use them only for platform-specific behaviour.
- At most one `@explore` scenario. Its description is the **charter**
  (what to probe, and the risks); it needs no steps. Workers run it after
  the scripted scenarios, as timeboxed free testing.
- `Background:` steps run before every scenario; a `Scenario Outline` with
  `Examples:` becomes one scenario per row. Other tags are kept and
  ignored.

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

Then decide whether to wait:

- The user wrote a one-line request: draft a single `@explore` scenario
  whose charter is their request, and start at once.
- You interpreted a ticket, an issue or a longer description: show the
  feature file and wait for the user's OK. Apply their corrections first.
- The user said "show me first": show it and wait, whatever the source.

## 4. Start the run

```sh
simsquad run new .simsquad/drafts/<slug>.feature --worker <claude or codex: the CLI you are running in> --out .simsquad/run-new.json
```

`--worker` names the CLI you run in, which the workers run on too: pass
`--worker claude` in Claude Code and `--worker codex` in Codex. Add
`--fresh` when the user asks for an isolated squad. A saved feature
file runs from its `qa/features/` path. The output gives `run_id`,
`run_dir` (absolute), `squad_name`, `fresh`, `platforms`,
`deadline_seconds`, `worker_model` (never empty: the project's choice
or the default for `--worker`) and `app_notes` (absolute; absent when the
project has no app notes); keep them for the next steps. The
run folder holds a copy of the feature file (`feature.feature`). Leave the
draft where it is: `.simsquad/` is gitignored, and the next draft with the
same slug overwrites it.

When `run new` refuses, fix the cause and run it again:

- Malformed Gherkin: fix the line it names.
- A platform tag the equipment lacks: ask the user to add that platform
  with `simsquad equip` (an interactive wizard in their terminal), or drop
  the tag.
- The squad is in use by an unfinished run that shares a platform: ask the
  user whether that run is still going. If not, finish it with the
  `simsquad run report` command the message names; otherwise pass
  `--fresh`.

## 5. Deploy

```sh
simsquad deploy --name "$SQUAD" --platform ios --platform android --force-build --out "$RUN_DIR/deploy.json"
```

Pass one `--platform` per entry of the run's `platforms` from step 4: the
example is a run on both; a run whose `platforms` is `["ios"]` passes
`--platform ios` alone. This rebuilds the app from the working tree and
installs it on those platforms' equipped devices, booting any that are not
running; the squad's other platform is left alone. The equipment is the
only source of devices and context, so pass no other flags. It can take
minutes.

- Exit `0`: every device on the run's platforms is ready. Exit `2`: some
  are; carry on with them.
- Exit `1` with an error that the squad is not reusable with the requested
  matrix: the equipment changed since the squad was deployed. Dismiss
  tears down every device of the squad, and a run on the other platform
  may be using it, so first list the other unfinished runs on it (a run
  folder whose `run.json` has this `squad_name` and no `report.json`):

  ```sh
  for d in "${RUN_DIR:?}"/../*/; do
    [ -e "$d/report.json" ] || [ "$d" -ef "$RUN_DIR" ] && continue
    if grep -q "\"squad_name\": \"$SQUAD\"" "$d/run.json"; then echo "$d"; fi
  done
  ```

  If it prints nothing, the squad is this run's alone: dismiss it and
  deploy again, once:

  ```sh
  simsquad dismiss --name "$SQUAD" --out "$RUN_DIR/dismiss.json"
  ```

  If it prints a run folder, don't dismiss. Go to step 9, and in your
  reply (step 11) name that run and suggest running the feature again
  with `run new --fresh`, an isolated squad.

- Any other exit `1`: nothing is ready or the build failed. Go straight to step 9;
  the report records an infra verdict. Diagnosing the failure is the
  user's call: in your reply (step 11), quote `builds.ios.error` or
  `builds.android.error` from `deploy.json`, which ends with the full
  build log path, and leave the log unread.

## 6. List the devices

```sh
simsquad devices --name "$SQUAD" --ready --out "$RUN_DIR/devices.json"
```

Keep the rows whose `platform` is in the run's `platforms`. Each row has
`platform`, `id`, `name`, `model`, `os_version`, `bundle_id`, `status` and
the squad `context`, where a secret such as a password shows as
`<secret>`. Leave it so: a worker fetches the value itself. No rows left:
go to step 9.

## 7. Start the workers

A worker starts in your working directory, not in its worker folder, so it
gets every path it reads or writes as an absolute path. First find the two
folders it reads outside the run folder:

- `SKILL_DIR`: this skill's folder with every symlink resolved, which can
  lie outside the repo (`.claude/skills/simsquad-test` is usually a link).
  Run `realpath` on the folder this file was loaded from and keep what it
  prints. Name `worker.md` by this path too: Claude Code refuses a read
  through a link that leads out of the folders it was given.

  ```sh
  realpath <this skill's folder, such as .claude/skills/simsquad-test>
  ```

- `NOTES_DIR`: the folder holding the app notes, which is `app_notes` from
  step 4 without its trailing `/README.md`. When step 4 gave no
  `app_notes`, there is no `NOTES_DIR`.

Then start one worker per device, all at once. For each device, with
`DEVICE_ID` its `id`:

1. Create its folder:

   ```sh
   mkdir -p "$RUN_DIR/workers/$DEVICE_ID/screenshots"
   ```

2. Write `$RUN_DIR/workers/$DEVICE_ID/prompt.md` with this content, every
   `<…>` filled in:

   ```text
   You are a simsquad-test worker. Read <SKILL_DIR>/worker.md and follow it.

   Run folder: <run_dir>
   Worker folder: <run_dir>/workers/<device id>
   Squad: <squad_name>
   Time limit: <deadline_seconds> seconds
   Device: <the device's row from devices.json, as one line of JSON>
   ```

3. Start it under `simsquad run worker`, which kills the worker and every
   process it started when the time limit passes. In Claude Code:

   ```sh
   simsquad run worker --dir "$RUN_DIR/workers/$DEVICE_ID" --timeout "$DEADLINE" \
     --out "$RUN_DIR/workers/$DEVICE_ID/worker.json" \
     -- claude -p "Read $RUN_DIR/workers/$DEVICE_ID/prompt.md and follow it." \
     --add-dir "$SKILL_DIR" --add-dir "$NOTES_DIR" \
     --allowedTools "Bash(mobilecli:*)" "Bash(simsquad reset:*)" "Bash(simsquad run validate:*)" \
     "Bash(simsquad devices:*)" \
     "Edit(/$RUN_DIR/**)" \
     --model "$WORKER_MODEL"
   ```

   Pass these permissions exactly as shown: a headless worker can't answer
   a permission prompt, and `claude -p` ignores the project's
   `.claude/settings.json` in a folder nobody has trusted interactively.

   - `"Bash(simsquad devices:*)"` lets the worker fetch a `<secret>`
     context value with `simsquad devices --reveal` to log in.
   - `--add-dir` lets the worker read `worker.md` and the app notes
     wherever they live. Leave out `--add-dir "$NOTES_DIR"` when there is
     no `NOTES_DIR`.
   - `"Edit(/$RUN_DIR/**)"` lets it create and change its files under the
     run folder, from any working directory. Claude Code reads `//` as the
     filesystem root and a single `/` as the working directory. `$RUN_DIR`
     already starts with `/`, so write exactly one more before it: the rule
     becomes `Edit(//Users/…/.simsquad/runs/<run-id>/**)`, never `///…`.

   In Codex:

   ```sh
   simsquad run worker --dir "$RUN_DIR/workers/$DEVICE_ID" --timeout "$DEADLINE" \
     --out "$RUN_DIR/workers/$DEVICE_ID/worker.json" \
     -- codex exec --sandbox danger-full-access --model "$WORKER_MODEL" "Read $RUN_DIR/workers/$DEVICE_ID/prompt.md and follow it."
   ```

   Always pass `--model "$WORKER_MODEL"`, the `worker_model` from step 4.

Start every worker as a background command (in Claude Code, a Bash call
with `run_in_background`), then wait until all have exited. Each exits by
its time limit at the latest. `worker.json` holds `{"status": …}`:

- `ok`: the worker exited in time and left a `result.json`.
- `blocked: timeout`: the time limit passed, and the worker was killed.
- `error: no result.json`: the worker exited without writing its result,
  often because it couldn't write to its folder. Its `worker.log` says why.
- `error: usage limit`: the worker's CLI stopped because the account ran
  out of usage.
- `error: worker exited <code>`: the worker failed for another reason. Its
  `worker.log` says why.

### Retry a worker

A worker that didn't end `ok`, or whose result fails step 8, can run again
once its cause is fixed: a usage limit has reset, or a missing permission
is now granted. Retrying an unfixed cause fails the same way, and simsquad
never retries a worker on its own, so ask the user when the fix is theirs.

To retry, run that device's `simsquad run worker` command from step 7
again, unchanged. Before the worker starts, it moves the earlier attempt's
files into `$RUN_DIR/workers/$DEVICE_ID/attempts/<n>/` and keeps
`prompt.md`, so only the new attempt counts. Then validate the worker
(step 8) and write the report (step 9).

Retry before step 9 when you can. Once the report exists the run is
finished: another run may already be using its platforms on the squad, and
step 10 may have dismissed a fresh squad. Running step 9 again after a
retry overwrites `report.json` and `report.md`.

## 8. Validate each worker

```sh
simsquad run validate "$RUN_DIR/workers/$DEVICE_ID" --out "$RUN_DIR/workers/$DEVICE_ID/validation.json"
```

Exit `1` means the worker's `result.json` is missing or invalid; `errors`
says why. Workers are black boxes: judge their files, and leave them as
they are. The report records an invalid worker as an error.

## 9. Write the report

```sh
simsquad run report "$RUN_DIR" --out "$RUN_DIR/report-summary.json"
```

Run it on every path that got past step 4, including a failed deploy: the
report marks the run finished, and until it exists no other run can use
this run's platforms on the squad. Exit `0` passed, `1` failed, `2` infra. The output gives
`verdict`, `report_json`, `report_md` and `counts`. The report reads each
worker's current attempt and ignores `attempts/`, so after a retry (step 7)
it can run again.

## 10. Dismiss a fresh squad

Only when `run new` said `fresh: true`:

```sh
simsquad dismiss --name "$SQUAD" --out "$RUN_DIR/dismiss.json"
```

The project squad stays up, so the next run skips the cold boot.

## 11. Reply

Read `report.json` and tell the user:

- The verdict, and its `summary.reason` when there is one (infra, or
  devices deploy couldn't get ready). After a deploy exit `1`, add the
  build error and log path from step 5.
- The counts: scenarios passed, failed and blocked; bugs, questions, notes.
- The top findings: bugs by severity (high first), then questions, each
  with its title, device and evidence path.
- The path of `report.md`.

For a feature file that came from a draft, offer to save it. On "save it",
copy `$RUN_DIR/feature.feature` to `qa/features/<slug>.feature`; the user
can then re-run it by name.

## Codex setup

Codex support is experimental. Deploys write outside the repo
(`~/.cache/simsquad/`, Xcode's DerivedData and simulator data under
`~/Library/Developer/`, the Android SDK and `~/.gradle`) and talk to the
simulator services, and workers reset apps and reach mobilecli's on-device
agent over the network. The default `workspace-write` sandbox blocks this,
so both the session that runs the test and its workers need full access:

- Start the session with `codex --sandbox danger-full-access`, or set
  `sandbox_mode = "danger-full-access"` in `~/.codex/config.toml`.
- Start workers with `codex exec --sandbox danger-full-access` (step 7).

When a deploy fails with permission errors under Codex, stop and show the
user these settings.
