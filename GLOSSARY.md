# simsquad

simsquad provisions squads of simulators and emulators for mobile QA, and
ships agent skills that run feature tests on them from inside an app's
repository.

## Provisioning

**Squad**:
A named set of provisioned devices sharing one env; the unit that is
deployed, reset and dismissed.
_Avoid_: pool, fleet, lease

**Device**:
One simulator, emulator or physical phone in a squad, with the app under
test installed.
_Avoid_: sim (when the platform doesn't matter), slot

**Equipment**:
A project's build setup and device matrix as defined by `simsquad equip`;
the only source of devices for that project's squads.
_Avoid_: config, matrix

**Device spec**:
One row of a squad's device matrix: model, OS and count.
_Avoid_: spec (unqualified), matrix entry

**Env**:
The free-form key/value test context attached to a squad, such as persona,
API stage or feature flags.
_Avoid_: config, variables

## Agent testing

**Feature file**:
A Gherkin `.feature` file describing one app feature's scenarios; its steps
are carried out by workers, not by step definitions.
_Avoid_: test plan, feature context, spec

**Source**:
What a feature file was derived from: a Jira ticket, a GitHub issue or the
user's own description.
_Avoid_: requirement, ticket (when the kind doesn't matter)

**Scenario**:
One Given/When/Then case in a feature file, judged passed or failed per
device.
_Avoid_: smoke check, test case

**Exploration**:
A scenario tagged `@explore` whose charter guides timeboxed free testing
instead of scripted steps.
_Avoid_: monkey testing, freeplay

**Suite**:
The set of feature files and scenarios selected by a tag expression, such as
`@release`.
_Avoid_: collection, batch, test plan

**App notes**:
The app repo's standing knowledge for workers (navigation tricks, test
accounts, known quirks), shared by every feature file.
_Avoid_: project context, knowledge base

**Test run**:
One execution of one feature file against a squad, producing a report and a
verdict.
_Avoid_: flight, mission, job

**Suite run**:
A sequence of test runs, one per feature file in a suite, summarised into a
single verdict.
_Avoid_: batch run

**Worker**:
The agent that tests one device during a test run and returns its own
result.
_Avoid_: sub-agent, tester

**Finding**:
Something a worker reports with screenshot evidence; a bug, a question or a
note.
_Avoid_: issue, defect

**Verdict**:
The outcome of a test run or suite run: passed, failed or infra.
_Avoid_: status, result

## Skills

**simsquad skill**:
The agent skill that teaches everyday use of the CLI, such as putting the
current build on a device or resetting the app.
_Avoid_: manual (as a name)

**simsquad-test skill**:
The agent skill holding the procedure for a test run.
_Avoid_: pilot
