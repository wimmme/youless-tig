# Configuration

youless-tig reads its settings from environment variables; the compose files
take them from `.env` ([.env.example](../.env.example)). On a mistake it stops
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
| `LISTEN` | `:8080` | address of the API inside the container |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |

Only for the compose files (not read by youless-tig itself):

| Variable | Default | |
|---|---|---|
| `YOULESS_TIG_VERSION` | `latest` | image tag |
| `YOULESS_TIG_PORT`, `YOULESS_TIG_BIND` | `8080`, `0.0.0.0` | where the API is published on the host |
| `INFLUXDB_ADMIN_USER`, `INFLUXDB_ADMIN_PASSWORD` | `admin`, required | stack: InfluxDB's first setup |
| `INFLUXDB_RETENTION` | empty (forever) | stack: retention of the bucket at first setup |
| `INFLUXDB_PORT`, `INFLUXDB_BIND` | `8086`, `127.0.0.1` | stack: InfluxDB UI |
| `GRAFANA_ADMIN_USER`, `GRAFANA_ADMIN_PASSWORD` | `admin`, required | stack: Grafana login |
| `GRAFANA_PORT`, `GRAFANA_BIND` | `3000`, `0.0.0.0` | stack: Grafana |

## More than one YouLess

Run one youless-tig container per YouLess, each with its own `DEVICE_NAME`,
`YOULESS_URL` and API port, writing to the same bucket. The dashboard's
*YouLess* variable switches between them.
