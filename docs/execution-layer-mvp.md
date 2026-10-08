# Execution layer — moved

The execution layer above simsquad is now its own project:
**[simsquad-pilot](https://github.com/peuf0u/simsquad-pilot)** — an
agent-native exploratory/smoke test runner (AGENTS.md-portable; Claude Code,
pi, and other harnesses).

The full design spec lives there at `docs/spec.md`. It moved because the
runner is a *consumer* of simsquad's released CLI contract, and `AGENTS.md`
at a repo root is what other harnesses discover.
