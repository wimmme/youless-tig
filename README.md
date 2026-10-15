# youless-tig

Long-term history for your **YouLess energy monitor** (LS120), in a TIG stack:
**T**elegraf, **I**nfluxDB and **G**rafana.

A YouLess keeps only a limited history: ten hours per minute, ten days per ten
minutes, seventy days per hour and one year per day. youless-tig reads the live
readings and those logs, stores everything in InfluxDB for as long as you like,
and shows it in Grafana. It also answers **the YouLess's own API**, backed by
that long history, so any existing YouLess client can use it instead of the device.

- Live readings every 10 seconds: net power, P1 import and export per tariff,
  voltage, current and power per phase, the Belgian capacity peak, gas, water and S0.
- The device's internal logs (minute, 10-minute, hour, day) are copied too. On
  the first start youless-tig reads the device's whole history; later, any gap
  (youless-tig, InfluxDB or the network down) is filled from them.
- A YouLess-compatible API: `/e`, `/f`, `/d`, `/a` and the log pages `/V`, `/W`,
  `/K`, `/Z`, byte for byte as the device answers, but reaching back as far as
  your data does ([docs/api.md](docs/api.md)).
- One small container (a static Go binary, no dependencies), multi-arch
  (amd64, arm64, arm/v7), read-only, non-root.
- Reads only: youless-tig never changes a setting on your YouLess, and talks to
  it one request at a time.

```
                 ┌─────────────┐  line protocol  ┌──────────┐      ┌──────────┐      ┌─────────┐
  YouLess  ◄─────┤ youless-tig ├────────────────►│ Telegraf ├─────►│ InfluxDB │◄─────┤ Grafana │
  (LAN, HTTP)    └──────┬──────┘                 └──────────┘      └────▲─────┘      └─────────┘
                        │ YouLess-compatible API                        │ log pages
                        └───────────────────────────────────────────────┘
```

## Two ways to run it

| You have… | Use | Guide |
|---|---|---|
| nothing yet | `docker-compose.stack.yml`: youless-tig, Telegraf, InfluxDB and Grafana, set up and connected | [docs/stack.md](docs/stack.md) |
| an InfluxDB 2.x (and Grafana) | `docker-compose.yml`: only youless-tig | [docs/existing-influxdb.md](docs/existing-influxdb.md) |

Both read their settings from `.env`; [.env.example](.env.example) lists and explains
every setting.

### Quick start: the complete stack

```sh
git clone https://github.com/wimmme/youless-tig.git && cd youless-tig
cp .env.example .env && chmod 600 .env
# edit .env: YOULESS_URL, TZ, INFLUXDB_ADMIN_PASSWORD, GRAFANA_ADMIN_PASSWORD
docker compose -f docker-compose.stack.yml up -d
```

Open Grafana on `http://<docker host>:3000` and log in with the admin password
from `.env`. The *YouLess* dashboard is the home page; the history panels fill
within a few minutes, while youless-tig reads the device's logs.

### Quick start: with your own InfluxDB

Create a bucket and a token with read and write access to it
([docs/influxdb.md](docs/influxdb.md)), then:

```sh
mkdir youless-tig && cd youless-tig
curl -fsSLO https://raw.githubusercontent.com/wimmme/youless-tig/main/docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/wimmme/youless-tig/main/.env.example -o .env && chmod 600 .env
# edit .env: YOULESS_URL, TZ, INFLUX_URL, INFLUX_TOKEN, INFLUX_ORG, INFLUX_BUCKET
docker compose up -d
```

Import [grafana/dashboards/youless.json](grafana/dashboards/youless.json) in your
Grafana ([docs/grafana.md](docs/grafana.md)).

## Documentation

- [docs/stack.md](docs/stack.md): the complete stack: setup, tokens, backups, updates
- [docs/existing-influxdb.md](docs/existing-influxdb.md): youless-tig with your own InfluxDB, optionally through your Telegraf
- [docs/influxdb.md](docs/influxdb.md): bucket, tokens and retention in InfluxDB
- [docs/grafana.md](docs/grafana.md): data source and dashboard in Grafana
- [docs/data-model.md](docs/data-model.md): measurements, tags and fields, with example queries
- [docs/api.md](docs/api.md): the YouLess-compatible API
- [docs/configuration.md](docs/configuration.md): every setting
- [docs/development.md](docs/development.md): building, tests, the simulated YouLess, releases

New to InfluxDB and Grafana? InfluxData's
[10-minute dashboard](https://www.influxdata.com/blog/10-minute-dashboard-influxdb/)
is a good first look at how the pieces fit together.

## Requirements

- A YouLess LS120 with firmware 1.6 or newer, reachable over HTTP from the
  Docker host. A fixed IP address (a DHCP reservation in your router) is best.
- Docker with Compose v2.
- For your own InfluxDB: 2.x (youless-tig uses `/api/v2/write` and InfluxQL on
  `/query`, which InfluxDB 2 maps to the bucket).

Only the LS120 is supported and tested; the older LS110 is not.

## Licence

[GPL-3.0](LICENSE). youless-tig is an independent project, not affiliated with or
endorsed by the makers of the YouLess. "YouLess" is a trademark of its owner and
is used here only to say which device this software works with.
