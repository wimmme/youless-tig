# The complete stack

`docker-compose.stack.yml` runs four containers on one Docker network:

| Container | Image | Role | Port on the host |
|---|---|---|---|
| `youless-tig` | `ghcr.io/wimmme/youless-tig` | reads the YouLess, sends readings to Telegraf, serves the YouLess-compatible API | `YOULESS_TIG_PORT` (8080) |
| `youless-tig-telegraf` | `telegraf:1.40` | receives the readings, buffers them, writes them to InfluxDB | none |
| `youless-tig-influxdb` | `influxdb:2.9` | stores everything | `INFLUXDB_PORT` (8086), localhost only |
| `youless-tig-grafana` | `grafana/grafana:13.2` | dashboards | `GRAFANA_PORT` (3000) |

## Setup

1. Give your YouLess a fixed IP address (a DHCP reservation in your router) and
   check that the Docker host reaches it: `curl http://<youless>/d` answers with
   `{"model":"LS120",…}`.
2. Get the files and create `.env`:

   ```sh
   git clone https://github.com/wimmme/youless-tig.git && cd youless-tig
   cp .env.example .env && chmod 600 .env
   ```

3. In `.env`, set at least:
   - `YOULESS_URL`: `http://<address of the YouLess>`
   - `TZ`: your time zone, e.g. `Europe/Brussels` or `Europe/Amsterdam`. The
     YouLess logs carry local times without a zone; youless-tig needs this to
     store them correctly.
   - `INFLUXDB_ADMIN_PASSWORD` and `GRAFANA_ADMIN_PASSWORD`: generate them, e.g.
     `openssl rand -base64 24`.
   - optionally `INFLUX_ORG` and `INFLUX_BUCKET` (default `home` and `youless`).
4. Start:

   ```sh
   docker compose -f docker-compose.stack.yml up -d
   docker compose -f docker-compose.stack.yml logs -f youless-tig
   ```

   The log shows the channels found (electricity, gas, water) and, within a few
   minutes, `log backfilled` for each of the device's logs.
5. Open Grafana at `http://<docker host>:3000`, log in as `admin` with
   `GRAFANA_ADMIN_PASSWORD`. The *YouLess* dashboard is the home page.

To type less, put `COMPOSE_FILE=docker-compose.stack.yml` in `.env`; then plain
`docker compose up -d` uses the stack.

## What happens on the first start

InfluxDB sets itself up from `.env`: organisation, bucket, admin user. Then
[`influxdb/init/10-youless-tig.sh`](../influxdb/init/10-youless-tig.sh) creates
a token per service, each with only the rights it needs, and writes them to the
`secrets` volume:

| Token file | Rights | Used by |
|---|---|---|
| `telegraf_token` | write the bucket | Telegraf, to write to InfluxDB |
| `youless_tig_token` | read the bucket | youless-tig, for the API's log pages |
| `grafana_token` | read the bucket | Grafana's data source |
| `telegraf_listener_token` | (random, not an InfluxDB token) | youless-tig → Telegraf |

No service gets the admin token. The other containers start only after the
tokens exist (InfluxDB's health check waits for them).

`INFLUXDB_ADMIN_*`, `INFLUX_ORG`, `INFLUX_BUCKET` and `INFLUXDB_RETENTION` are
used **only on that first start**. Changing them later in `.env` does not change
InfluxDB; use the InfluxDB UI for that (and keep `INFLUX_ORG`/`INFLUX_BUCKET` in
`.env` matching, Telegraf and Grafana read them).

## The InfluxDB web UI

InfluxDB is published on `127.0.0.1:8086` only. From another computer use an SSH
tunnel: `ssh -L 8086:localhost:8086 <docker host>`, then open
`http://localhost:8086` and log in with `INFLUXDB_ADMIN_USER` and
`INFLUXDB_ADMIN_PASSWORD`. Or set `INFLUXDB_BIND=0.0.0.0` in `.env` to publish it
on your LAN.

## Updating

```sh
git pull
docker compose -f docker-compose.stack.yml pull
docker compose -f docker-compose.stack.yml up -d
```

Pin `YOULESS_TIG_VERSION` in `.env` (e.g. `1.0.0`) to update youless-tig only
when you choose to.

## Backups

Everything is in Docker volumes: `youless-tig_influxdb-data` (the data),
`youless-tig_influxdb-config`, `youless-tig_grafana-data`, `youless-tig_secrets`.
For a consistent copy of the data use InfluxDB's backup:

```sh
docker exec youless-tig-influxdb influx backup /var/lib/influxdb2/backup-$(date +%F)
```

and copy that directory off the host (`docker cp`). The YouLess itself holds a
year of daily totals, ten days of 10-minute values and seventy days of hourly
values: after restoring an older backup, youless-tig fills in what is still
on the device.

## Starting over

`docker compose -f docker-compose.stack.yml down -v` removes the containers **and
all data**. Removing only the `secrets` volume while keeping the InfluxDB volumes
loses the tokens: the init script runs only on a fresh InfluxDB. Recreate them
in the InfluxDB UI (*Load Data → API Tokens*) and put them in the volume by hand,
or start over.

## Troubleshooting

| Symptom | Look at |
|---|---|
| `youless-tig` keeps restarting or logs `configuration:` | the message lists every missing or wrong setting in `.env` |
| `poll failed … connection refused / timeout` | `YOULESS_URL`; can the Docker host reach the YouLess (`curl http://<youless>/d`)? |
| `youless: password required or wrong` | `YOULESS_PASSWORD` |
| `youless-tig-influxdb` stays *unhealthy*, other containers do not start | the tokens were not created: `influxdb/init/10-youless-tig.sh` must be executable (`chmod +x`, automatic with `git clone`); then `down -v` and start again |
| `influx write: HTTP 401` | the `secrets` volume does not match InfluxDB (see *Starting over*) |
| Grafana panels empty, *Data source* error | `docker compose -f docker-compose.stack.yml logs grafana influxdb` |
| Times in the history are off by one or two hours | `TZ` must be the YouLess's time zone |
