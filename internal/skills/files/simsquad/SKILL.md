---
name: simsquad
description: Everyday simsquad use in this app repo — put the current build on simulators or emulators, list running devices, reset the app, change the squad env, tear devices down. Use when the user asks to run the app on a device or manage the dev squad. For verifying a feature, use simsquad-test.
---

# simsquad

simsquad builds this app, installs it on a squad of simulators and
emulators, and answers every command with JSON on stdout. Human progress
goes to stderr; leave it on the terminal for the user to watch.

## Requests this skill does not handle

A request to verify, test or check a feature ("verify PROJ-123", "check that
resend is disabled") is a test run: switch to the **simsquad-test** skill.
Its squad is separate, so a test run never touches the dev squad.

## The dev squad

Everything here works on one squad, the project's **dev squad**, which
keeps the state the user sets up by hand (logins, test data) out of reach of
test-run resets. Use only that squad, never `qa-<repo>` or a test-run squad.

Resolve its name once per session:

1. Find the project root: the nearest directory, from here upwards, that
   holds `simsquad.toml` or `simsquad.local.toml`. When neither exists the
   project is not equipped: ask the user to run `simsquad equip` (it is an
   interactive wizard that needs their terminal) and stop.
2. If `[agent]` has a `dev_squad` key, that is the name;
   `simsquad.local.toml` wins over `simsquad.toml`.
3. Otherwise the name is `dev-<repo>`, where `<repo>` is the project root's
   folder name, lowercased, with every run of other characters turned into
   one hyphen (`MyApp_iOS` gives `dev-myapp-ios`).

The examples below use `SQUAD` for that name.

## Reading output

Every example passes `--out <file>`, which also writes the command's JSON
to a file under `.simsquad/dev/` (`.simsquad/` is gitignored; the folder is
created as needed). Read the JSON from that file. Run the commands exactly
as shown, so each one stays a plain simsquad call the user has already
allowed.

The progress lines on stderr are for the user; the JSON file is the answer.

## Put the current build on devices

```sh
simsquad deploy --name "$SQUAD" --preserve-data --out .simsquad/dev/deploy.json
```

- Run it from the project root. It builds incrementally, installs on the
  equipped devices (the `[[ios.sims]]` and `[[android.sims]]` rows of
  `simsquad.toml`), and reuses running devices when the squad already
  exists, so a redeploy takes seconds once the build is warm.
- `--preserve-data` keeps the user's app state across the reinstall. Leave
  it out when the user asks for a clean install.
- Exit code: `0` every device ready, `2` some ready, `1` none ready or the
  build failed. On `1` or `2`, report the devices whose `status` is `error`
  with their message.
- When the user names devices the equipment lacks ("an iPhone and a Pixel"
  with no Android equipped), pass the matrix for this deploy with
  `--ios-spec 'DEVICE:RUNTIME:COUNT'` and `--android-spec
  'DEVICE:IMAGE:COUNT'`. Any spec flag replaces both platforms' matrices, so
  pass one for every platform the user wants. To keep those devices for
  every deploy, suggest `simsquad equip --add-ios-spec …` /
  `--add-android-spec …` instead.

## List running devices

```sh
simsquad devices --name "$SQUAD" --out .simsquad/dev/devices.json
```

Each row has `platform`, `id` (simulator UDID or emulator serial), `name`,
`model`, `os_version`, `status` and the squad `env`. Add `--ready` for usable
devices only, `--platform ios` or `--platform android` for one platform. For
every squad on this machine, drop `--name`:

```sh
simsquad devices --out .simsquad/dev/devices-all.json
```

## Reset the app

Terminates the app and wipes its data, without reinstalling:

```sh
simsquad reset --name "$SQUAD" --out .simsquad/dev/reset.json
simsquad reset --name "$SQUAD" --device "$DEVICE_ID" --out .simsquad/dev/reset.json
```

Take `DEVICE_ID` from the `id` field of `devices`. Reset wipes what the user
set up by hand, so run it only when they ask for it.

## Squad status

```sh
simsquad status --out .simsquad/dev/status.json
simsquad status --name "$SQUAD" --out .simsquad/dev/status.json
```

Without `--name` it lists every squad; with it, the full record of one.

## Change the squad env

The env is the free-form test context (persona, API stage, feature flags)
copied onto every device row. Changing it never touches the devices or the
app:

```sh
simsquad set-env --name "$SQUAD" --set api=staging --unset persona --out .simsquad/dev/env.json
simsquad set-env --name "$SQUAD" --clear --set user=alice --out .simsquad/dev/env.json
```

`--set KEY=VALUE` and `--unset KEY` repeat; `--clear` wipes every key first.

## Tear the devices down

```sh
simsquad dismiss --name "$SQUAD" --out .simsquad/dev/dismiss.json
```

Shuts down and deletes the squad's simulators and emulators. Run it only
when the user asks; the next deploy starts cold.
