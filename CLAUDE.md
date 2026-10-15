# CLAUDE.md

Guidance for Claude Code (and humans) working in this repository.

**youless-tig** reads a YouLess LS120 energy monitor, stores live readings and the
device's internal logs in InfluxDB (directly or through Telegraf), and serves a
YouLess-compatible API backed by that history. Go, standard library only, one
Docker image. GPL-3.0.

## Where to look

| Question | Where |
|---|---|
| What it does, how to run it | `README.md`, `docs/stack.md`, `docs/existing-influxdb.md` |
| Why it is built this way | `DECISIONS.md` |
| Measurements, tags, fields | `docs/data-model.md` (kept in step with `internal/store`; a test checks) |
| The compatible API and the device's paging | `docs/api.md`, `internal/youless/pages.go` |
| The device API | YouLess LS120 manual, chapter *Data interface / API*; recordings in `testdata/ls120` |
| Build, tests, simulator, releases | `docs/development.md` |
| Wim's own hosts and devices (git-ignored) | `CLAUDE.local.md` |

## Commands

Everything runs in Docker; the dev PC has none, so use `tools/remote.sh` (hosts in `CLAUDE.local.md`).

```sh
sh tools/verify.sh            # gofmt, vet, unit tests, images, compose files (CI)
sh tools/integration.sh       # complete stack against the simulator (CI)
python3 tools/dashboard/gen.py grafana/dashboards/youless.json   # regenerate the dashboard
```

## Standing rules

- **Never write to a real YouLess.** GET requests on the read endpoints only, one
  at a time. Password, login and every other behaviour are tested with the
  simulator. Fixtures are recorded with GETs and scrubbed (MAC, values; never the
  P1 telegram) before commit.
- **The device's behaviour is the truth.** Paging and formats were observed on a
  real LS120 (firmware 1.6.4) and are pinned by tests on `testdata/ls120`. Do not
  "fix" them without a recording that shows otherwise.
- **Tests are not optional.** Every change has unit tests; anything touching the
  stack, tokens or the dashboard runs `tools/integration.sh`. CI never touches a
  real device.
- **Security:** least-privilege tokens, secrets never logged, the container
  read-only and non-root, no telemetry. The compatible API forwards nothing to the device.
- **Dependencies:** Go standard library only. Container images pinned to a minor version.
- **No credits to other projects and no suggestion of affiliation with the
  makers of the YouLess** (README states it is independent).
- **Ask instead of assume** on design questions; record decisions in `DECISIONS.md`.

## Workflow

- Small commits, one logical change each; update `CHANGELOG.md` in the same commit.
- Commit freely; **never push, tag or create/modify the GitHub repository
  without being asked.** A `vX.Y.Z` tag publishes an image.
- Deploy with `docker compose up -d`, not `docker restart`.
