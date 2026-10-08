# Consuming a squad

This is the recipe for the **test driver** (an AI agent like `qa-run`, a CI
job, or a developer shell) that wants to provision a squad and run tests
against it. simsquad is the producer; you are the consumer.

The contract is the CLI itself: every verb writes JSON to **stdout** and
human-readable progress to **stderr**. **Never `2>&1`** — it merges the two
and breaks parsers.

## The lifecycle

```sh
SESSION="qa-run-$(date +%s)"          # or your CI job id, PR number, etc.

# 1. Provision. Picks a unique name so no other process touches your squad.
simsquad deploy --name="$SESSION" \
  --ios-repo ~/code/myapp-ios \
  --ios-spec 'iPhone 17:iOS 26.4:2' \
  --env user=alice --env api=staging \
  > squad.json
EXIT=$?

# 2. Ensure cleanup happens no matter how you exit.
trap 'simsquad dismiss --name="$SESSION"' EXIT INT TERM

# Optional: branch on deploy's exit code.
#   0 = all devices ready
#   2 = mixed (some ready, some errored — proceed with the survivors)
#   1 = nothing usable (bail)
case "$EXIT" in
  0|2) ;;
  *)   echo "deploy failed; aborting" >&2; exit "$EXIT" ;;
esac

# 3. Fetch the device list. Filter to ready devices only.
simsquad devices --name="$SESSION" --ready > devices.json

# 4. Dispatch tests. You allocate sub-agents to devices in your own process —
#    simsquad doesn't track sub-agents.
# … see "Allocating devices to sub-agents" below …

# 5. Trap fires on exit, dismissing the squad.
```

## Why there's no `claim` / `release`

You might expect a "claim this squad while I work" verb. There isn't one,
intentionally:

- Every real consumer is the **sole owner** of the squads it deploys. No
  competing process is going to try to dismiss or redeploy your squad
  unless someone explicitly names it.
- The protection is **naming convention**, not a lease primitive. Pick a
  unique squad name per session and nothing else will touch it.

Concrete patterns:

| Consumer | Squad name |
|---|---|
| AI agent / qa-run | `agent-{session_id}` or `qa-run-{run_id}` |
| CI on a PR | `ci-pr-{pr_number}` |
| Developer's standing sim | `dev-{username}` or `dev-sim` |

If two consumers pick the same name, the second one's `deploy` will detect
the existing squad — it reuses (if the matrix matches) or errors (if not).
A simultaneous `dismiss` from another process is a footgun, but not one
that's worth machinery in simsquad; just don't share names.

## Allocating devices to sub-agents

When your orchestrator (an AI agent, a Python script, a shell loop) wants
to run N parallel test units against M devices, **you** handle the
allocation in-process. simsquad is not involved.

The standard pattern is a pool semaphore. Pseudocode:

```
devices = json.load(open("devices.json"))["devices"]
free = collections.deque(devices)
in_flight = {}

for unit in units:
    while not free:
        finished_unit, freed = wait_one(in_flight)
        free.append(freed)
    device = free.popleft()
    in_flight[unit] = spawn_subagent(unit, device)
```

Each sub-agent is given **its** device's `id` (the udid for iOS, the
`emulator-<port>` serial for Android) and the env block. The sub-agent
drives the device directly (via `mobile-mcp`, `xcuitest`, `adb`, whatever
your runner uses) — it never talks back to simsquad.

## JSON shapes you'll parse

### `deploy` / `status --name=<squad>` — `SquadPublic`

```json
{
  "name": "qa-run-1717000000",
  "created_at": "2026-05-27T14:00:00Z",
  "builds": { "ios": { "app_path": "…", "bundle_id": "…", "specs": [ … ] } },
  "devices": [ /* full Device records — see status output */ ],
  "env": { "user": "alice", "api": "staging" },
  "reused": false
}
```

### `devices --name=<squad> [--ready] [--platform=ios|android]`

```json
{
  "name": "qa-run-1717000000",
  "devices": [
    {
      "platform": "ios",
      "id": "ABC-123-…",
      "name": "simsquad-qa-run-1717000000-ios-0",
      "model": "iPhone 17",
      "os_version": "iOS 26.4",
      "bundle_id": "com.example.myapp",
      "status": "ready",
      "ready_at": "2026-05-27T14:00:30Z",
      "env": { "user": "alice", "api": "staging" }
    },
    {
      "platform": "android",
      "id": "emulator-5554",
      "name": "simsquad-qa-run-1717000000-android-0",
      "model": "pixel_7",
      "os_version": "Android API 34",
      "bundle_id": "com.example.myapp.debug",
      "status": "ready",
      "ready_at": "2026-05-27T14:00:45Z",
      "env": { "user": "alice", "api": "staging" }
    }
  ]
}
```

`id` is what you pass to `mobile-mcp`, `xcrun simctl`, `adb -s`, etc.
`bundle_id` is the installed app — everything a per-device test agent needs
to launch, terminate, or wipe the app without a second lookup.

### `devices` with no `--name` — whole-fleet view

```json
{
  "squads": [
    { "name": "alice-staging", "devices": [ … ] },
    { "name": "bob-staging",   "devices": [ … ] }
  ]
}
```

### `dismiss --name=<squad>`

```json
{ "removed": ["qa-run-1717000000"] }
```

## Multi-persona pattern

For scenario where one agent runs several test contexts in parallel (e.g.
`alice@staging` against `bob@staging` simultaneously), deploy a squad per
persona — they're independent and run on independent sims:

```sh
for persona in alice bob; do
  simsquad deploy --name="agent-$SESSION-$persona" \
    --ios-spec 'iPhone 17:iOS 26.4:1' \
    --env user=$persona --env api=staging \
    > "squad-$persona.json" &
done
wait

# Run tests against each squad…
# Then:
trap '
  for persona in alice bob; do
    simsquad dismiss --name="agent-$SESSION-$persona"
  done
' EXIT
```

## Handling errors

- **Deploy exit code 2** (mixed): some devices errored. Inspect the
  `devices` array — devices with `"status": "error"` carry an
  `"error_message"`. Decide whether to proceed against the survivors or
  bail.
- **Deploy exit code 1**: nothing usable. The JSON descriptor is still
  emitted on stdout; the failure detail is inside.
- **Reuse with a different matrix**: `deploy --name=foo` where `foo`
  already exists with a different `--ios-spec` errors with
  "not reusable; run `simsquad dismiss --name foo` first". Either change
  the spec to match, or dismiss + redeploy.
- **Crashed orchestrator**: your `trap dismiss EXIT` should fire. If
  simsquad-managed sims are left on disk anyway, `simsquad sweep` cleans
  up any orphan `simsquad-*-(ios|android)-N` sims that aren't in the
  registry.
