# Development

youless-tig is Go with only the standard library. Everything builds and runs in
Docker, so the host needs nothing else.

```sh
sh tools/verify.sh                  # all checks + production image (what CI runs)
sh tools/integration.sh             # the complete stack against the simulated YouLess
docker build --target test .        # unit tests only: gofmt, go vet, go test -race
```

Without Docker on your machine, run them on a Docker host:
`REMOTE=user@dockerhost sh tools/remote.sh sh tools/verify.sh`.

## Layout

```
cmd/youless-tig      the service
cmd/youless-sim      a simulated YouLess LS120 (tests, demos)
internal/youless     device client, parsers, log paging and formats
internal/collector   polling, log sync and backfill, caches for the API
internal/store       the data model in InfluxDB (docs/data-model.md)
internal/influx      line protocol, /api/v2/write and InfluxQL over HTTP
internal/api         the YouLess-compatible HTTP API
internal/config      settings from the environment
internal/sim         the simulator's logic
internal/influxtest  an in-memory InfluxDB for tests
testdata/ls120       recorded LS120 responses (scrubbed)
test/integration     tests against the running stack
```

## Tests

- **Unit tests** (`go test ./...`) need no network. The device format is checked
  against recordings of a real LS120 in `testdata/ls120`: every log page parses
  and formats back to the same bytes, and the paging (which page holds which
  hours) matches what the device showed at the time of recording.
- **The simulator** (`internal/sim`, `cmd/youless-sim`) answers like the device
  with made-up data that follows the clock, including the password login. The
  collector and API tests run against it and an in-memory InfluxDB; the API
  test checks that every page the device has is answered byte for byte the same.
- **Integration** (`tools/integration.sh`) starts `docker-compose.stack.yml` with
  the simulator in place of a YouLess and checks: the backfill, every log page
  against the simulator, live data through Telegraf, that each token can do
  only what it should, and that Grafana's data source works and every query of
  the dashboard runs.

CI never touches a real YouLess.

### Recording fixtures

Fixtures come from a real device with GET requests only, one at a time. They
are scrubbed before commit: the MAC address is replaced and meter values are
randomised, keeping their format and width. Never commit the raw P1 telegram
(`/V?p=1`): it holds the meter's serial number.

### Try it without a YouLess

```sh
docker build --target sim -t youless-sim .
docker run --rm -p 8081:8081 -e TZ=Europe/Brussels youless-sim
curl 'http://localhost:8081/V?h=1'
```

Point `YOULESS_URL` at it to run youless-tig against simulated data.

## The dashboard

`grafana/dashboards/youless.json` is generated: edit
`tools/dashboard/gen.py` and run `python3 tools/dashboard/gen.py grafana/dashboards/youless.json`.
A Go test checks that the dashboard only uses measurements and fields from the
data model; the integration test runs every query.

## Releases

1. Move the *Unreleased* notes in `CHANGELOG.md` to a new `## [X.Y.Z] - date` section.
2. Commit, tag `vX.Y.Z`, push the tag.
3. The *Publish* workflow runs all checks, builds the image for amd64, arm64 and
   arm/v7, pushes `ghcr.io/wimmme/youless-tig:X.Y.Z`, `X.Y` and `latest`, and
   creates the GitHub release with the changelog notes.
