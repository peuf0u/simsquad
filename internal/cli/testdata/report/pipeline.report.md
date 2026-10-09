# Login with magic link — ❌ FAILED

Source: [https://github.com/acme/app/issues/456](https://github.com/acme/app/issues/456)

Run `login-magic-link-20260612-170000` · squad `qa-app` · 2026-06-12T17:00:00Z → FINISHED_AT

**2 device(s)** · scenarios 2 passed / 0 failed / 2 blocked · **1 bug(s)** · 0 question(s) · 0 note(s)

## Env

| Key | Value |
|---|---|
| `API_STAGE` | staging |
| `PERSONA` | alice \| admin |

## Results

| Scenario | iPhone 17 (ABC-123) | iPhone 17 Pro (DEF-456) |
|---|---|---|
| App launches | ✅ passed | ⚠️ blocked |
| Explore magic link login (exploration) | ✅ passed | ⚠️ blocked |

## Bugs

### [high] Magic link stays in browser

*Device: iPhone 17 (ABC-123)*

- **Expected:** Returns to app
- **Actual:** Browser stays

Repro:
1. Launch
2. Open login
3. Tap link

![evidence](workers/ABC-123/screenshots/step-001.png)

## Devices

### iPhone 17 — iOS 26.4 (`ABC-123`)

Worker: ok · exploration: completed

- ✓ App launches — Home screen shows

### iPhone 17 Pro — iOS 26.4 (`DEF-456`)

Worker: blocked · exploration: —
  — blocked: timeout

## Artifacts

Per-device `result.json`, `worker.log` and `screenshots/` sit under
`workers/<device-id>/` next to this report in the run directory.
