# Configuration

youless-tig reads its settings from environment variables; the compose files
take them from `.env`, made from [.env.stack.example](../.env.stack.example) (complete
stack) or [.env.example](../.env.example) (your own InfluxDB). On a mistake it stops
and lists every problem at once.

Secrets can also be read from a file (a Docker secret, for instance): set
`<NAME>_FILE` to the path instead of `<NAME>`, never both. This works for
`YOULESS_PASSWORD`, `INFLUX_TOKEN` and `INFLUX_WRITE_TOKEN`.

| Variable | Default | |
|---|---|---|
| `YOULESS_URL` | required | `http://<address of the YouLess>` |
| `YOULESS_PASSWORD` | empty | only when the YouLess web interface has a password (at most 21 characters) |
| `YOULESS_TIMEOUT` | `10s` | per request to the YouLess (1s…2m) |
| `TZ` | `UTC` | **the YouLess's time zone**, e.g. `Europe/Brussels`: its logs carry local times |
| `DEVICE_NAME` | `youless` | the `device` tag on every point |
| `POLL_INTERVAL` | `10s` | how often the live values are read (2s…1h) |
| `CHANNELS` | `auto` | `auto` = electricity, plus gas and water when the meter has them; or a list of `electricity`, `gas`, `water`, `s0` |
| `HISTORY` | `true` | copy the device's logs; needed for the API's log pages and to fill gaps |
| `INFLUX_URL` | required | InfluxDB, e.g. `http://influxdb:8086` |
| `INFLUX_TOKEN` | required | token for the bucket: read and write, or read only with `INFLUX_WRITE_URL` |
| `INFLUX_ORG` | required | organisation |
| `INFLUX_BUCKET` | required | bucket |
| `INFLUX_WRITE_URL` | empty | send writes here instead, e.g. Telegraf's `influxdb_v2_listener` |
| `INFLUX_WRITE_TOKEN` | `INFLUX_TOKEN` | token for `INFLUX_WRITE_URL` |
| `LISTEN` | `:8080` | address of the API; only when running the program outside Docker (see *Ports*) |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

Only for the compose files. youless-tig itself does not read these: never add
them under youless-tig's `environment:`.

| Variable | Default | |
|---|---|---|
| `YOULESS_TIG_VERSION` | `latest` | image tag |
| `YOULESS_TIG_PORT`, `YOULESS_TIG_BIND` | `8030`, `0.0.0.0` | where the API is published on the host |
| `INFLUXDB_ADMIN_USER`, `INFLUXDB_ADMIN_PASSWORD` | `admin`, required | stack: InfluxDB's first setup |
| `INFLUXDB_RETENTION` | empty (forever) | stack: retention of the bucket at first setup |
| `INFLUXDB_PORT`, `INFLUXDB_BIND` | `8031`, `127.0.0.1` | stack: InfluxDB UI |
| `GRAFANA_ADMIN_USER`, `GRAFANA_ADMIN_PASSWORD` | `admin`, required | stack: Grafana login |
| `GRAFANA_PORT`, `GRAFANA_BIND` | `3030`, `0.0.0.0` | stack: Grafana |

## Ports

Each container listens on a fixed port inside the container; the compose files
publish it on a port of your Docker host that you choose in `.env`:

| Service | Inside the container | On the host (`.env`) |
|---|---|---|
| youless-tig API | 8080 | `YOULESS_TIG_PORT` (default 8030) |
| InfluxDB (stack) | 8086 | `INFLUXDB_PORT` (default 8031, localhost only) |
| Grafana (stack) | 3000 | `GRAFANA_PORT` (default 3030) |

The host defaults avoid 8080, 3000 and 8086 on purpose: those are the usual ports
of many services, so on a host that runs more than this they are often taken.

The compose file's line

```yaml
    ports:
      - "${YOULESS_TIG_BIND:-0.0.0.0}:${YOULESS_TIG_PORT:-8030}:8080"
```

becomes `0.0.0.0:8030:8080` with the default, or `0.0.0.0:9000:8080` with
`YOULESS_TIG_PORT=9000`: the API is then at `http://<docker host>:9000/e`, while
youless-tig still listens on 8080 inside. InfluxDB and Grafana work the same
way. So:

- To change a port, change only `YOULESS_TIG_PORT`, `GRAFANA_PORT` or
  `INFLUXDB_PORT` in `.env`, then `docker compose up -d`.
- Do not add `YOULESS_TIG_PORT` under `environment:`; youless-tig does not read it.
- Leave `LISTEN` alone in Docker: changing it moves the port inside the container,
  and the `:8080` of the mapping would then point at nothing.

Containers of the stack talk to each other over the stack's own network with the
inside ports (`http://influxdb:8086`, `http://telegraf:8186`); the host ports do
not matter for them.

## More than one YouLess

Run one youless-tig container per YouLess, each with its own `DEVICE_NAME`,
`YOULESS_URL` and `YOULESS_TIG_PORT` (and a different `container_name`), writing
to the same bucket. The dashboard's
*YouLess* variable switches between them.
