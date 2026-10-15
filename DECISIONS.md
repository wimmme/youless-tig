# Decisions

Why youless-tig is built the way it is. Newest last; a later decision overrides
an earlier one. Proposals under *Open* are not decided.

## D1 — Store in InfluxDB, not SQLite (2026-10-15)

The project this idea started from stored readings in SQLite and served them
itself. youless-tig stores them in InfluxDB: a time-series database is what
Grafana, retention and downsampling expect, the data is usable from any tool
that speaks InfluxQL or Flux, and youless-tig itself needs no volume. The
compatible API reads InfluxDB back.

## D2 — youless-tig is the only client of the YouLess (2026-10-15)

The first idea was Telegraf reading the YouLess directly (`inputs.http`).
youless-tig reads it instead, and Telegraf receives from youless-tig:

- the device is small and slow (a log page sometimes takes seconds; while
  recording fixtures one request timed out after 8 s): one client, one request at
  a time, is kinder than two pollers;
- the internal logs are paged, carry local times without a zone and need per-value
  timestamps computed from the page start and interval: not a job for a
  Telegraf parser;
- a password-protected YouLess needs a login with a cookie;
- the compatible API serves `/e` from the same reading, so clients never add
  load on the device.

Telegraf stays in the stack as the writer to InfluxDB (buffering and retries,
and a place to add other inputs). youless-tig speaks the InfluxDB v2 write
protocol, which is also what Telegraf's `influxdb_v2_listener` accepts, so the
same code writes to InfluxDB directly (`docker-compose.yml`) or through Telegraf
(`docker-compose.stack.yml`, or `INFLUX_WRITE_URL` with your own Telegraf).

## D3 — InfluxDB 2.x, InfluxQL for reading (2026-10-15)

The stack uses InfluxDB 2 (`influxdb:2.9`), the version most home TIG stacks
run. InfluxDB 3 Core limits how much history one query may cover, which works
against a long-term archive.

youless-tig writes with `/api/v2/write` and reads with InfluxQL on `/query`
(InfluxDB 2 maps the bucket name to a database by itself). The Grafana dashboard
uses InfluxQL too. Flux is in maintenance mode and absent from InfluxDB 3;
InfluxQL works on 1.8, 2.x and 3.x.

## D4 — Timestamps are the device's own (2026-10-15)

Live readings are stored at the meter's timestamp (`tm`, `gts`, `wts`, `ts0`),
log values at the start of their interval. Re-reading a value therefore writes
the same point, which InfluxDB stores once: a sync can always simply re-read,
and a restart needs no local state (where to continue is asked from InfluxDB).

## D5 — Log pages from the archive, byte for byte (2026-10-15)

The compatible API answers log pages from InfluxDB in exactly the device's
format and paging (checked against recordings of a real LS120, firmware 1.6.4),
so clients notice no difference. Pages beyond the device's range are answered
while there is stored data. A page within the device's range triggers a read of
that page from the device first, throttled (30 s for minutes … 15 min for
days), so recent pages are as fresh as on the device without letting clients
drive the device's load.

## D6 — Least-privilege tokens in the stack (2026-10-15)

On the first start an init script creates one InfluxDB token per service, with
only the rights it needs (Telegraf write, youless-tig and Grafana read), and
shares them through a volume. The admin password is only used for the
first setup and the UI; no service holds the admin token. InfluxDB's UI is
published on localhost only by default.

## D7 — One device per container (2026-10-15)

Several YouLess devices are supported by running several containers with a
different `DEVICE_NAME` and port, writing to one bucket. One device per
container keeps the configuration flat (environment variables only) and the
compatible API unambiguous (one device per port, like the real thing).

## D8 — Read only, never write (2026-10-15)

youless-tig sends only GET requests for the read endpoints (and the login when
a password is set). No settings, no time sync, no P1 telegram: the compatible
API cannot be used to change the device either.

## Open

- S0: collected only when `CHANNELS` names it. Auto-detection is unclear: a
  device without an S0 meter still reports S0 values.
- LS110: not supported; nobody has one to test with.
